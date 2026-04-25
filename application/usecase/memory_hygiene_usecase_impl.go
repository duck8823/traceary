package usecase

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"time"

	"golang.org/x/xerrors"

	"github.com/duck8823/traceary/application/queryservice"
	apptypes "github.com/duck8823/traceary/application/types"
	domtypes "github.com/duck8823/traceary/domain/types"
)

type memoryHygieneUsecase struct {
	memory              MemoryUsecase
	memoryQuery         queryservice.MemoryQueryService
	extraRedactPatterns []string
}

// NewMemoryHygieneUsecase creates a MemoryHygieneUsecase. The scanner
// shares the sanitizer with every other memory-write surface so a
// redaction pattern added to the config is detected uniformly: the scan
// reports any memory whose sanitized fact differs from the stored fact,
// which is exactly the set of memories a later supersede would rewrite.
func NewMemoryHygieneUsecase(memory MemoryUsecase, memoryQuery queryservice.MemoryQueryService, extraRedactPatterns []string) MemoryHygieneUsecase {
	return &memoryHygieneUsecase{
		memory:              memory,
		memoryQuery:         memoryQuery,
		extraRedactPatterns: slices.Clone(extraRedactPatterns),
	}
}

// defaultStalenessThreshold controls the expiry-suggestion window when
// the caller does not override it via --expiry-days. 90 days is long
// enough that seasonal / release-boundary memories still register as
// fresh but short enough to flag truly forgotten entries.
const defaultStalenessThreshold = 90 * 24 * time.Hour

// defaultSupersedeSimilarityThreshold controls the supersede_candidate
// detector: two accepted memories in the same scope are paired when
// their word-Jaccard similarity meets or exceeds this value but the
// fact text itself differs. 0.6 catches real re-phrasings (for example
// "prefer bulleted commits" vs "prefer bulleted commit messages") while
// steering clear of shared-keyword coincidences.
const defaultSupersedeSimilarityThreshold = 0.6

// hygieneScanPageSize caps the number of accepted memories the scanner
// walks in one run. Real memory stores stay well under this bound; the
// ceiling is here to keep the single-shot scan deterministic.
const hygieneScanPageSize = 2000

// Scan loads every accepted memory in scope and emits one suggestion per
// memory that trips any of the three hygiene rules. The function is
// deliberately simple: each rule is independent so an operator can
// triage the suggestions without the scanner second-guessing them.
func (u *memoryHygieneUsecase) Scan(ctx context.Context, criteria apptypes.MemoryHygieneScanCriteria) (apptypes.MemoryHygieneScanResult, error) {
	if u.memoryQuery == nil {
		return apptypes.MemoryHygieneScanResult{}, xerrors.Errorf("memory query service is not configured")
	}

	now := criteria.Now
	if now.IsZero() {
		now = time.Now().UTC()
	}
	staleness := criteria.StalenessThreshold
	if staleness <= 0 {
		staleness = defaultStalenessThreshold
	}

	summaries, err := u.loadAcceptedSummaries(ctx, criteria.Scopes)
	if err != nil {
		return apptypes.MemoryHygieneScanResult{}, err
	}

	result := apptypes.MemoryHygieneScanResult{
		Suggestions: make([]apptypes.MemoryHygieneSuggestion, 0, len(summaries)),
	}

	duplicateIndex := make(map[duplicateKey][]apptypes.MemorySummary, len(summaries))
	for _, summary := range summaries {
		// Redaction re-scan: sanitize the stored fact with the current
		// pattern set and flag any memory whose sanitized output is
		// different from what the store already holds.
		sanitizedFact, _, _, err := sanitizeMemoryPayload(summary.Fact(), nil, nil, u.extraRedactPatterns)
		if err != nil {
			result.Suggestions = append(result.Suggestions, apptypes.MemoryHygieneSuggestion{
				MemoryID:  summary.MemoryID(),
				Kind:      apptypes.MemoryHygieneSuggestionRedactionHit,
				Reason:    fmt.Sprintf("sanitizer failed: %v", err),
				Fact:      summary.Fact(),
				Scope:     summary.Scope(),
				UpdatedAt: summary.UpdatedAt(),
			})
			result.RedactionHitCount++
			continue
		}
		if sanitizedFact != summary.Fact() {
			result.Suggestions = append(result.Suggestions, apptypes.MemoryHygieneSuggestion{
				MemoryID:      summary.MemoryID(),
				Kind:          apptypes.MemoryHygieneSuggestionRedactionHit,
				Reason:        "current redaction patterns mask this fact",
				Fact:          summary.Fact(),
				SanitizedFact: sanitizedFact,
				Scope:         summary.Scope(),
				UpdatedAt:     summary.UpdatedAt(),
			})
			result.RedactionHitCount++
		}

		// Expiry suggestion: use updated_at as a staleness proxy. The
		// store does not yet track retrieval timestamps, so
		// conservative-leaning updated_at is the honest signal.
		if now.Sub(summary.UpdatedAt()) > staleness {
			result.Suggestions = append(result.Suggestions, apptypes.MemoryHygieneSuggestion{
				MemoryID:  summary.MemoryID(),
				Kind:      apptypes.MemoryHygieneSuggestionExpiryCandidate,
				Reason:    fmt.Sprintf("no updates for more than %s", staleness),
				Fact:      summary.Fact(),
				Scope:     summary.Scope(),
				UpdatedAt: summary.UpdatedAt(),
			})
			result.ExpiryCandidateCount++
		}

		// Duplicate suggestion: group by scope + fact. Any bucket with
		// more than one entry becomes a pair of suggestions so the
		// reviewer sees both sides. Summaries are expected to carry a
		// non-nil scope (MemorySummaryOf rejects nil), but the scanner
		// falls back to a sentinel key so a malformed row still surfaces
		// in the duplicate bucket instead of panicking here.
		key := duplicateKey{Fact: summary.Fact()}
		if summary.Scope() != nil {
			key.ScopeKind = summary.Scope().Kind()
			key.ScopeKey = summary.Scope().Key()
		}
		duplicateIndex[key] = append(duplicateIndex[key], summary)
	}

	for _, bucket := range duplicateIndex {
		if len(bucket) < 2 {
			continue
		}
		for i, summary := range bucket {
			other := bucket[(i+1)%len(bucket)]
			result.Suggestions = append(result.Suggestions, apptypes.MemoryHygieneSuggestion{
				MemoryID:          summary.MemoryID(),
				Kind:              apptypes.MemoryHygieneSuggestionDuplicate,
				Reason:            fmt.Sprintf("shares fact with %s", other.MemoryID().String()),
				Fact:              summary.Fact(),
				DuplicateMemoryID: other.MemoryID(),
				Scope:             summary.Scope(),
				UpdatedAt:         summary.UpdatedAt(),
			})
			result.DuplicateCount++
		}
	}

	// Supersede candidate: pair accepted memories whose fact text differs
	// but shares enough word-level overlap to likely be re-phrasings of
	// the same idea. The older memory becomes the one to supersede; the
	// newer memory's content is the suggested replacement. Memories that
	// already collided as exact duplicates are excluded so both detectors
	// stay additive and the reviewer sees a clean split between "same
	// content" and "overlapping content".
	similarityThreshold := criteria.SimilarityThreshold
	if similarityThreshold <= 0 {
		similarityThreshold = defaultSupersedeSimilarityThreshold
	}
	// Validity-annotated classifier runs first because it produces the
	// more specific signal: (scope, type) match plus explicit temporal
	// evidence splits pairs cleanly into "same policy, overlapping"
	// (emit validity_overlap_supersede) and "separate historical
	// facts, disjoint" (silent). Both outcomes are then excluded from
	// the generic supersede_candidate pass so the reviewer never sees
	// a temporally-bounded pair reported under the weaker kind.
	validityOverlapSuggestions, temporalDisjointPairs := classifyValidityAnnotatedPairs(summaries, similarityThreshold)
	validityPairs := make(map[string]struct{}, len(validityOverlapSuggestions))
	for _, suggestion := range validityOverlapSuggestions {
		validityPairs[pairKey(suggestion.MemoryID.String(), suggestion.ReplacementMemoryID.String())] = struct{}{}
	}

	supersedeSuggestions := detectSupersedeCandidates(summaries, duplicateIndex, similarityThreshold)
	filteredSupersede := make([]apptypes.MemoryHygieneSuggestion, 0, len(supersedeSuggestions))
	for _, suggestion := range supersedeSuggestions {
		key := pairKey(suggestion.MemoryID.String(), suggestion.ReplacementMemoryID.String())
		if _, captured := validityPairs[key]; captured {
			continue
		}
		if _, disjoint := temporalDisjointPairs[key]; disjoint {
			continue
		}
		filteredSupersede = append(filteredSupersede, suggestion)
	}
	result.Suggestions = append(result.Suggestions, filteredSupersede...)
	result.SupersedeCandidateCount = len(filteredSupersede)

	result.Suggestions = append(result.Suggestions, validityOverlapSuggestions...)
	result.ValidityOverlapSupersedeCount = len(validityOverlapSuggestions)

	return result, nil
}

// detectSupersedeCandidates groups accepted memories by scope, computes
// pairwise word-Jaccard similarity, and emits a supersede_candidate for
// every pair above the threshold. The older memory is the one that gets
// superseded; the newer memory's fact is the replacement. Pairs are
// de-duplicated so only one direction is emitted per memory pair.
func detectSupersedeCandidates(summaries []apptypes.MemorySummary, duplicateBuckets map[duplicateKey][]apptypes.MemorySummary, threshold float64) []apptypes.MemoryHygieneSuggestion {
	if threshold <= 0 || threshold > 1 {
		return nil
	}
	exactDuplicatePairs := make(map[string]struct{}, len(duplicateBuckets))
	for _, bucket := range duplicateBuckets {
		if len(bucket) < 2 {
			continue
		}
		for i := range bucket {
			for j := i + 1; j < len(bucket); j++ {
				exactDuplicatePairs[pairKey(bucket[i].MemoryID().String(), bucket[j].MemoryID().String())] = struct{}{}
			}
		}
	}

	scopeGroups := make(map[string][]apptypes.MemorySummary, len(summaries))
	for _, summary := range summaries {
		if summary.Scope() == nil {
			continue
		}
		key := string(summary.Scope().Kind()) + "|" + summary.Scope().Key()
		scopeGroups[key] = append(scopeGroups[key], summary)
	}

	var suggestions []apptypes.MemoryHygieneSuggestion
	for _, group := range scopeGroups {
		if len(group) < 2 {
			continue
		}
		wordSets := make([]map[string]struct{}, len(group))
		for i, summary := range group {
			wordSets[i] = toWordSet(summary.Fact())
		}
		for i := 0; i < len(group); i++ {
			for j := i + 1; j < len(group); j++ {
				if group[i].Fact() == group[j].Fact() {
					continue
				}
				if _, ok := exactDuplicatePairs[pairKey(group[i].MemoryID().String(), group[j].MemoryID().String())]; ok {
					continue
				}
				sim := jaccardSimilarity(wordSets[i], wordSets[j])
				if sim < threshold {
					continue
				}
				older, newer := group[i], group[j]
				if older.UpdatedAt().After(newer.UpdatedAt()) {
					older, newer = newer, older
				} else if older.UpdatedAt().Equal(newer.UpdatedAt()) {
					// Tiebreak on memory id so two memories with the
					// exact same updated_at (common when the store is
					// bulk-imported in a single transaction) produce
					// deterministic suggestions on every scan run.
					if older.MemoryID().String() > newer.MemoryID().String() {
						older, newer = newer, older
					}
				}
				suggestions = append(suggestions, apptypes.MemoryHygieneSuggestion{
					MemoryID:            older.MemoryID(),
					Kind:                apptypes.MemoryHygieneSuggestionSupersedeCandidate,
					Reason:              fmt.Sprintf("scope overlap with %s at similarity %.2f", newer.MemoryID().String(), sim),
					Fact:                older.Fact(),
					ReplacementMemoryID: newer.MemoryID(),
					ReplacementFact:     newer.Fact(),
					Similarity:          sim,
					Scope:               older.Scope(),
					UpdatedAt:           older.UpdatedAt(),
				})
			}
		}
	}
	return suggestions
}

// classifyValidityAnnotatedPairs groups accepted memories by
// (scope, type), inspects each pair whose similarity meets the
// threshold and where at least one side carries an explicit valid_to,
// and returns:
//
//   - validity_overlap_supersede suggestions for pairs whose windows
//     overlap under half-open semantics (aligned with runtime validity
//     evaluation), and
//   - a set of pair keys whose windows are disjoint under the same
//     semantics. The caller excludes those pairs from the generic
//     supersede_candidate pass: they are separate historical facts
//     the operator intentionally time-bounded, not merge candidates.
//
// Pairs where neither side carries an explicit valid_to fall through
// (not reported in either output) so the caller's supersede_candidate
// pipeline still handles them.
func classifyValidityAnnotatedPairs(
	summaries []apptypes.MemorySummary,
	threshold float64,
) ([]apptypes.MemoryHygieneSuggestion, map[string]struct{}) {
	if threshold <= 0 || threshold > 1 {
		return nil, nil
	}
	disjointPairs := map[string]struct{}{}

	type scopeTypeKey struct {
		ScopeKind  domtypes.MemoryScopeKind
		ScopeKey   string
		MemoryType domtypes.MemoryType
	}
	groups := make(map[scopeTypeKey][]apptypes.MemorySummary, len(summaries))
	for _, summary := range summaries {
		if summary.Scope() == nil {
			continue
		}
		key := scopeTypeKey{
			ScopeKind:  summary.Scope().Kind(),
			ScopeKey:   summary.Scope().Key(),
			MemoryType: summary.MemoryType(),
		}
		groups[key] = append(groups[key], summary)
	}

	var suggestions []apptypes.MemoryHygieneSuggestion
	for _, group := range groups {
		if len(group) < 2 {
			continue
		}
		wordSets := make([]map[string]struct{}, len(group))
		for i, summary := range group {
			wordSets[i] = toWordSet(summary.Fact())
		}
		for i := 0; i < len(group); i++ {
			for j := i + 1; j < len(group); j++ {
				if group[i].Fact() == group[j].Fact() {
					continue
				}
				_, aHasTo := group[i].ValidTo().Value()
				_, bHasTo := group[j].ValidTo().Value()
				// Pairs without any explicit upper bound are handled
				// by the generic supersede_candidate pipeline — the
				// operator has not expressed temporal intent yet.
				if !aHasTo && !bHasTo {
					continue
				}
				sim := jaccardSimilarity(wordSets[i], wordSets[j])
				if sim < threshold {
					continue
				}
				older, newer := group[i], group[j]
				if older.UpdatedAt().After(newer.UpdatedAt()) {
					older, newer = newer, older
				} else if older.UpdatedAt().Equal(newer.UpdatedAt()) {
					if older.MemoryID().String() > newer.MemoryID().String() {
						older, newer = newer, older
					}
				}
				if !validityWindowsOverlap(group[i], group[j]) {
					// Temporally-bounded but disjoint — historical
					// fact, exclude from supersede_candidate so the
					// generic pass cannot report it either.
					disjointPairs[pairKey(older.MemoryID().String(), newer.MemoryID().String())] = struct{}{}
					continue
				}
				suggestions = append(suggestions, apptypes.MemoryHygieneSuggestion{
					MemoryID:            older.MemoryID(),
					Kind:                apptypes.MemoryHygieneSuggestionValidityOverlapSupersede,
					Reason:              fmt.Sprintf("validity window overlaps %s at similarity %.2f", newer.MemoryID().String(), sim),
					Fact:                older.Fact(),
					ReplacementMemoryID: newer.MemoryID(),
					ReplacementFact:     newer.Fact(),
					Similarity:          sim,
					Scope:               older.Scope(),
					UpdatedAt:           older.UpdatedAt(),
				})
			}
		}
	}
	return suggestions, disjointPairs
}

// validityWindowsOverlap reports whether the half-open temporal
// validity windows [validFrom, validTo) of two memories intersect.
// valid_to is treated as exclusive to stay consistent with the
// runtime retrieval semantics (infrastructure/sqlite/memory_datasource
// evaluates valid_from <= as_of AND valid_to > as_of), so two
// adjacent windows — [t1, t2) and [t2, t3) — are reported as
// disjoint rather than overlapping.
func validityWindowsOverlap(a, b apptypes.MemorySummary) bool {
	aFrom := a.ValidFrom()
	bFrom := b.ValidFrom()
	aTo, aHasTo := a.ValidTo().Value()
	bTo, bHasTo := b.ValidTo().Value()

	// Half-open overlap: [aFrom, aTo) ∩ [bFrom, bTo) is non-empty iff
	//   aFrom < bTo  &&  bFrom < aTo
	// An open upper bound collapses the strict less-than check to
	// "always true" on that side.
	if aHasTo && !bFrom.Before(aTo) {
		return false
	}
	if bHasTo && !aFrom.Before(bTo) {
		return false
	}
	return true
}

// pairKey returns a stable key for an unordered pair of memory ids so
// the duplicate-exclusion set and future pair de-duplication do not
// depend on traversal order.
func pairKey(a, b string) string {
	if a < b {
		return a + "\x00" + b
	}
	return b + "\x00" + a
}

// toWordSet splits fact text into lowercase word tokens and drops empty
// tokens. A Go regexp is intentionally avoided here so the scanner stays
// allocation-light for large stores; strings.Fields+unicode mapping
// covers the typical ASCII and CJK word shapes Traceary sees.
func toWordSet(fact string) map[string]struct{} {
	out := make(map[string]struct{})
	for _, token := range strings.Fields(strings.ToLower(fact)) {
		token = strings.TrimFunc(token, func(r rune) bool {
			switch r {
			case '.', ',', ';', ':', '!', '?', '(', ')', '[', ']', '"', '\'':
				return true
			}
			return false
		})
		if token == "" {
			continue
		}
		out[token] = struct{}{}
	}
	return out
}

// jaccardSimilarity returns |A ∩ B| / |A ∪ B| for two word sets. Empty
// sets score zero so an accidental empty-fact entry cannot collide with
// every other memory.
func jaccardSimilarity(a, b map[string]struct{}) float64 {
	if len(a) == 0 || len(b) == 0 {
		return 0
	}
	intersect := 0
	smaller, larger := a, b
	if len(smaller) > len(larger) {
		smaller, larger = larger, smaller
	}
	for token := range smaller {
		if _, ok := larger[token]; ok {
			intersect++
		}
	}
	union := len(a) + len(b) - intersect
	if union == 0 {
		return 0
	}
	return float64(intersect) / float64(union)
}

type duplicateKey struct {
	ScopeKind domtypes.MemoryScopeKind
	ScopeKey  string
	Fact      string
}

// Apply walks the requested memory ids, re-scans to confirm the
// transition is still appropriate, and applies the lifecycle verb that
// matches the suggestion kind. Unknown or stale ids land in Failures so
// the caller sees exactly which memories moved.
func (u *memoryHygieneUsecase) Apply(ctx context.Context, criteria apptypes.MemoryHygieneApplyCriteria) (apptypes.MemoryHygieneApplyResult, error) {
	if u.memory == nil {
		return apptypes.MemoryHygieneApplyResult{}, xerrors.Errorf("memory usecase is not configured")
	}
	scanResult, err := u.Scan(ctx, apptypes.MemoryHygieneScanCriteria{
		StalenessThreshold: criteria.StalenessThreshold,
		Now:                criteria.Now,
	})
	if err != nil {
		return apptypes.MemoryHygieneApplyResult{}, err
	}
	suggestionByID := make(map[string]apptypes.MemoryHygieneSuggestion, len(scanResult.Suggestions))
	for _, suggestion := range scanResult.Suggestions {
		// Prefer redaction_hit when the same id has multiple
		// suggestions — rewriting the fact is the safer default
		// because it also satisfies the duplicate / expiry reasons.
		if existing, ok := suggestionByID[suggestion.MemoryID.String()]; ok {
			if existing.Kind == apptypes.MemoryHygieneSuggestionRedactionHit {
				continue
			}
		}
		suggestionByID[suggestion.MemoryID.String()] = suggestion
	}

	result := apptypes.MemoryHygieneApplyResult{}
	for _, rawID := range criteria.MemoryIDs {
		trimmed := strings.TrimSpace(rawID)
		if trimmed == "" {
			continue
		}
		suggestion, ok := suggestionByID[trimmed]
		if !ok {
			result.Failures = append(result.Failures, apptypes.MemoryHygieneApplyFailure{
				MemoryID: trimmed,
				Error:    "no current hygiene suggestion for this memory",
			})
			continue
		}
		memoryID, err := domtypes.MemoryIDFrom(trimmed)
		if err != nil {
			result.Failures = append(result.Failures, apptypes.MemoryHygieneApplyFailure{
				MemoryID: trimmed,
				Error:    err.Error(),
			})
			continue
		}
		applied, err := u.applyOne(ctx, memoryID, suggestion)
		if err != nil {
			result.Failures = append(result.Failures, apptypes.MemoryHygieneApplyFailure{
				MemoryID: trimmed,
				Error:    err.Error(),
			})
			continue
		}
		result.Applied = append(result.Applied, applied)
	}
	return result, nil
}

func (u *memoryHygieneUsecase) applyOne(ctx context.Context, memoryID domtypes.MemoryID, suggestion apptypes.MemoryHygieneSuggestion) (apptypes.MemoryHygieneApplied, error) {
	switch suggestion.Kind {
	case apptypes.MemoryHygieneSuggestionRedactionHit:
		details, err := u.memory.Show(ctx, memoryID)
		if err != nil {
			return apptypes.MemoryHygieneApplied{}, xerrors.Errorf("failed to show memory: %w", err)
		}
		// Redaction hit replaces the fact content but keeps the
		// existing memory's temporal window — the operator-set
		// validity is independent of the content sanitization.
		superseded, err := u.memory.Supersede(
			ctx,
			memoryID,
			details.Summary().MemoryType(),
			details.Summary().Scope(),
			suggestion.SanitizedFact,
			domtypes.Some(details.Summary().Confidence()),
			details.Summary().Source(),
			details.EvidenceRefs(),
			details.ArtifactRefs(),
			domtypes.Some(details.Summary().ValidFrom()),
			details.Summary().ValidTo(),
		)
		if err != nil {
			return apptypes.MemoryHygieneApplied{}, xerrors.Errorf("failed to supersede memory: %w", err)
		}
		return apptypes.MemoryHygieneApplied{MemoryID: memoryID.String(), Kind: suggestion.Kind, Transition: "supersede", Details: superseded}, nil
	case apptypes.MemoryHygieneSuggestionExpiryCandidate:
		expired, err := u.memory.Expire(ctx, memoryID, domtypes.None[time.Time]())
		if err != nil {
			return apptypes.MemoryHygieneApplied{}, xerrors.Errorf("failed to expire memory: %w", err)
		}
		return apptypes.MemoryHygieneApplied{MemoryID: memoryID.String(), Kind: suggestion.Kind, Transition: "expire", Details: expired}, nil
	case apptypes.MemoryHygieneSuggestionDuplicate:
		rejected, err := u.memory.Reject(ctx, memoryID)
		if err != nil {
			return apptypes.MemoryHygieneApplied{}, xerrors.Errorf("failed to reject memory: %w", err)
		}
		return apptypes.MemoryHygieneApplied{MemoryID: memoryID.String(), Kind: suggestion.Kind, Transition: "reject", Details: rejected}, nil
	case apptypes.MemoryHygieneSuggestionSupersedeCandidate,
		apptypes.MemoryHygieneSuggestionValidityOverlapSupersede:
		details, err := u.memory.Show(ctx, memoryID)
		if err != nil {
			return apptypes.MemoryHygieneApplied{}, xerrors.Errorf("failed to show memory: %w", err)
		}
		replacementFact := suggestion.ReplacementFact
		if strings.TrimSpace(replacementFact) == "" {
			return apptypes.MemoryHygieneApplied{}, xerrors.Errorf("%s missing replacement fact", suggestion.Kind)
		}
		// Inherit the existing memory's validity window so the
		// replacement keeps operator-set temporal boundaries.
		// validity_overlap_supersede fires *because* the pair has
		// overlapping windows — dropping the window at apply time
		// would silently erase the temporal evidence that justified
		// the suggestion in the first place (#665).
		superseded, err := u.memory.Supersede(
			ctx,
			memoryID,
			details.Summary().MemoryType(),
			details.Summary().Scope(),
			replacementFact,
			domtypes.Some(details.Summary().Confidence()),
			details.Summary().Source(),
			details.EvidenceRefs(),
			details.ArtifactRefs(),
			domtypes.Some(details.Summary().ValidFrom()),
			details.Summary().ValidTo(),
		)
		if err != nil {
			return apptypes.MemoryHygieneApplied{}, xerrors.Errorf("failed to supersede memory: %w", err)
		}
		return apptypes.MemoryHygieneApplied{MemoryID: memoryID.String(), Kind: suggestion.Kind, Transition: "supersede", Details: superseded}, nil
	default:
		return apptypes.MemoryHygieneApplied{}, xerrors.Errorf("unknown suggestion kind: %s", suggestion.Kind)
	}
}

// loadAcceptedSummaries walks every accepted memory in scope in
// hygieneScanPageSize-sized pages so a store with more than one page
// worth of memories is still scanned in full. The scan stops when the
// datasource returns fewer rows than the requested page size.
func (u *memoryHygieneUsecase) loadAcceptedSummaries(ctx context.Context, scopes []domtypes.MemoryScope) ([]apptypes.MemorySummary, error) {
	var all []apptypes.MemorySummary
	offset := 0
	for {
		builder := apptypes.NewMemoryListCriteriaBuilder(hygieneScanPageSize).
			Offset(offset).
			Statuses([]domtypes.MemoryStatus{domtypes.MemoryStatusAccepted})
		if len(scopes) > 0 {
			builder = builder.Scopes(scopes)
		}
		page, err := u.memoryQuery.List(ctx, builder.Build())
		if err != nil {
			return nil, xerrors.Errorf("failed to list accepted memories: %w", err)
		}
		if len(page) == 0 {
			break
		}
		all = append(all, page...)
		if len(page) < hygieneScanPageSize {
			break
		}
		offset += hygieneScanPageSize
	}
	return all, nil
}
