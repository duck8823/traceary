package usecase

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"golang.org/x/xerrors"

	"github.com/duck8823/traceary/application/queryservice"
	apptypes "github.com/duck8823/traceary/application/types"
	domtypes "github.com/duck8823/traceary/domain/types"
)

type memoryHygieneUsecase struct {
	memory              memoryHygieneWriter
	memoryQuery         queryservice.MemoryQueryService
	extraRedactPatterns []string
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

const memoryHygienePreviewMaxBytes = 240

// Scan traverses revision-consistent source pages until a finite invocation
// budget is exhausted or all four phases complete. A partial result owns an
// authenticated encrypted cursor whose keyset points after the last unit whose
// suggestions were committed to the result. A cross-page revision change
// preserves that keyset but permanently marks the chain best-effort.
func (u *memoryHygieneUsecase) Scan(ctx context.Context, criteria apptypes.MemoryHygieneScanCriteria) (apptypes.MemoryHygieneScanResult, error) {
	source, ok := u.memoryQuery.(queryservice.MemoryHygieneScanSource)
	if !ok {
		return apptypes.MemoryHygieneScanResult{}, xerrors.Errorf("bounded memory hygiene scan source is not configured")
	}
	budget := criteria.Budget
	if budget.IsZero() {
		budget = apptypes.DefaultMemoryHygieneScanBudget()
	}
	staleness := criteria.StalenessThreshold
	if staleness <= 0 {
		staleness = defaultStalenessThreshold
	}
	similarity := criteria.SimilarityThreshold
	if similarity <= 0 {
		similarity = defaultSupersedeSimilarityThreshold
	}
	if similarity > 1 {
		return apptypes.MemoryHygieneScanResult{}, xerrors.Errorf("memory hygiene similarity threshold must be less than or equal to 1")
	}

	now := criteria.Now.UTC()
	phase := apptypes.MemoryHygieneScanPhaseAcceptedRows
	keyset := apptypes.MemoryHygieneScanKeyset{}
	revision := domtypes.None[int64]()
	consistency := apptypes.MemoryHygieneScanConsistencyConsistent
	var consistencyReason apptypes.MemoryHygieneConsistencyReason
	var cursorPayload memoryHygieneCursorPayload
	if criteria.Cursor != "" {
		decoded, err := decodeMemoryHygieneCursor(criteria.Cursor)
		if err != nil {
			return apptypes.MemoryHygieneScanResult{}, xerrors.Errorf("failed to decode memory hygiene cursor: %w", err)
		}
		cursorPayload = decoded
		cursorTime, _ := time.Parse(time.RFC3339Nano, decoded.ScanAt)
		now = cursorTime
		phase = decoded.Phase
		keyset = decoded.Keyset
		revision = domtypes.Some(decoded.Revision)
		consistency = decoded.Consistency
		consistencyReason = decoded.ConsistencyReason
	} else if now.IsZero() {
		now = time.Now().UTC()
	}
	digest := memoryHygieneCriteriaDigest(criteria.Scopes, staleness, similarity, criteria.IncludeHiddenCandidates)
	if criteria.Cursor != "" && cursorPayload.CriteriaDigest != digest {
		return apptypes.MemoryHygieneScanResult{}, xerrors.Errorf("memory hygiene cursor criteria mismatch")
	}
	initialPhase := phase
	initialKeyset := keyset
	initialRevision := revision

	result := apptypes.MemoryHygieneScanResult{
		Suggestions:       []apptypes.MemoryHygieneSuggestion{},
		Consistency:       consistency,
		ConsistencyReason: consistencyReason,
	}
	startedAt := time.Now()
	finishPartial := func(reason apptypes.MemoryHygieneStopReason) (apptypes.MemoryHygieneScanResult, error) {
		return finishPartialMemoryHygieneResult(
			result,
			reason,
			phase,
			keyset,
			revision,
			consistency,
			consistencyReason,
			digest,
			now,
			startedAt,
			initialPhase,
			initialKeyset,
			initialRevision,
		)
	}
	scanCtx, cancel := context.WithTimeout(ctx, budget.MaxDuration())
	defer cancel()

	for {
		if time.Since(startedAt) >= budget.MaxDuration() {
			if _, hasRevision := revision.Value(); hasRevision {
				return finishPartial(apptypes.MemoryHygieneStopReasonTimeLimit)
			}
		}
		remainingRows := budget.MaxRows() - result.Usage.ScannedRows
		remainingScanBytes := budget.MaxScanBytes() - result.Usage.ScannedBytes
		remainingComparisons := budget.MaxComparisons() - result.Usage.Comparisons
		if remainingRows < 1 {
			return finishPartial(apptypes.MemoryHygieneStopReasonRowLimit)
		}
		if remainingScanBytes < 1 {
			return finishPartial(apptypes.MemoryHygieneStopReasonScanByteLimit)
		}
		if (phase == apptypes.MemoryHygieneScanPhaseExactDuplicates || phase == apptypes.MemoryHygieneScanPhaseSimilarityPairs) && remainingComparisons < 1 {
			return finishPartial(apptypes.MemoryHygieneStopReasonComparisonLimit)
		}
		sourceComparisons := remainingComparisons
		if sourceComparisons < 1 {
			sourceComparisons = 1
		}
		page, err := source.ScanMemoryHygienePage(scanCtx, apptypes.MemoryHygieneScanPageCriteria{
			Phase:                   phase,
			Keyset:                  keyset,
			Consistency:             consistency,
			Scopes:                  criteria.Scopes,
			IncludeHiddenCandidates: criteria.IncludeHiddenCandidates,
			ExpectedRevision:        revision,
			MaxRows:                 remainingRows,
			MaxScanBytes:            remainingScanBytes,
			MaxComparisons:          sourceComparisons,
		})
		if err != nil {
			var revisionChanged *queryservice.MemoryHygieneRevisionChangedError
			if errors.As(err, &revisionChanged) {
				if revisionChanged.CurrentRevision < 0 {
					return apptypes.MemoryHygieneScanResult{}, xerrors.Errorf("memory hygiene scan source returned an invalid revision")
				}
				revision = domtypes.Some(revisionChanged.CurrentRevision)
				consistency = apptypes.MemoryHygieneScanConsistencyBestEffort
				consistencyReason = apptypes.MemoryHygieneConsistencyReasonRevisionChanged
				result.Consistency = consistency
				result.ConsistencyReason = consistencyReason
				return finishPartial(apptypes.MemoryHygieneStopReasonRevisionChanged)
			}
			if errors.Is(err, context.DeadlineExceeded) || errors.Is(scanCtx.Err(), context.DeadlineExceeded) {
				return finishPartial(apptypes.MemoryHygieneStopReasonTimeLimit)
			}
			return apptypes.MemoryHygieneScanResult{}, xerrors.Errorf("failed to scan memory hygiene source page: %w", err)
		}
		if _, hasRevision := revision.Value(); !hasRevision {
			revision = domtypes.Some(page.Revision)
		}
		result.Usage.ScannedRows += page.ScannedRows
		result.Usage.ScannedBytes += page.ScannedBytes
		result.Usage.Comparisons += page.Comparisons

		for _, unit := range page.Units {
			if time.Since(startedAt) >= budget.MaxDuration() {
				return finishPartial(apptypes.MemoryHygieneStopReasonTimeLimit)
			}
			matches := u.matchesForScanUnit(phase, unit, now, staleness, similarity)
			safeSuggestions := make([]apptypes.MemoryHygieneSuggestion, 0, len(matches))
			var suggestionBytes int64
			for _, match := range matches {
				suggestion := u.safeMemoryHygieneSuggestion(match)
				encoded, marshalErr := json.Marshal(suggestion)
				if marshalErr != nil {
					return apptypes.MemoryHygieneScanResult{}, xerrors.Errorf("failed to size memory hygiene suggestion")
				}
				suggestionBytes += int64(len(encoded) + 1)
				safeSuggestions = append(safeSuggestions, suggestion)
			}
			if result.Usage.ResultBytes+suggestionBytes > budget.MaxResultBytes() {
				return finishPartial(apptypes.MemoryHygieneStopReasonResultByteLimit)
			}
			for _, suggestion := range safeSuggestions {
				result.Suggestions = append(result.Suggestions, suggestion)
				incrementMemoryHygieneCount(&result, suggestion.Kind)
			}
			result.Usage.ResultBytes += suggestionBytes
			keyset = unit.NextKeyset
		}
		keyset = page.ProgressKeyset

		if page.Done {
			nextPhase, complete := nextMemoryHygieneScanPhase(phase)
			if complete {
				result.Complete = true
				result.Partial = false
				result.StopReason = apptypes.MemoryHygieneStopReasonComplete
				result.Consistency = consistency
				result.ConsistencyReason = consistencyReason
				result.Usage.Elapsed = time.Since(startedAt)
				result.Usage.ElapsedMillis = result.Usage.Elapsed.Milliseconds()
				return result, nil
			}
			phase = nextPhase
			keyset = apptypes.MemoryHygieneScanKeyset{}
			continue
		}
		if page.StopReason == "" {
			return apptypes.MemoryHygieneScanResult{}, xerrors.Errorf("memory hygiene scan source stopped without a reason")
		}
		return finishPartial(page.StopReason)
	}
}

type memoryHygieneMatch struct {
	MemoryID            domtypes.MemoryID
	Kind                apptypes.MemoryHygieneSuggestionKind
	Reason              string
	rawFact             string
	sanitizedFact       string
	DuplicateMemoryID   domtypes.MemoryID
	ReplacementMemoryID domtypes.MemoryID
	replacementFact     string
	Similarity          float64
	Scope               domtypes.MemoryScope
	UpdatedAt           time.Time
	Status              domtypes.MemoryStatus
	Source              domtypes.MemorySource
	QualityReasons      []string
}

func (u *memoryHygieneUsecase) matchesForScanUnit(
	phase apptypes.MemoryHygieneScanPhase,
	unit apptypes.MemoryHygieneScanUnit,
	now time.Time,
	staleness time.Duration,
	similarity float64,
) []memoryHygieneMatch {
	switch phase {
	case apptypes.MemoryHygieneScanPhaseAcceptedRows:
		return u.acceptedRowHygieneMatches(unit.Row, now, staleness)
	case apptypes.MemoryHygieneScanPhaseExactDuplicates:
		peerID, ok := unit.RelatedMemoryID.Value()
		if !ok {
			return nil
		}
		return []memoryHygieneMatch{{
			MemoryID:          unit.Row.MemoryID(),
			Kind:              apptypes.MemoryHygieneSuggestionDuplicate,
			Reason:            fmt.Sprintf("shares fact with %s", peerID.String()),
			rawFact:           unit.Row.Fact(),
			DuplicateMemoryID: peerID,
			Scope:             unit.Row.Scope(),
			UpdatedAt:         unit.Row.UpdatedAt(),
		}}
	case apptypes.MemoryHygieneScanPhaseSimilarityPairs:
		peer, ok := unit.Peer.Value()
		if !ok {
			return nil
		}
		match, matched := similarityPairHygieneMatch(unit.Row, peer, similarity)
		if !matched {
			return nil
		}
		return []memoryHygieneMatch{match}
	case apptypes.MemoryHygieneScanPhaseCandidateRows:
		reasons := classifyExtractionNoise(unit.Row.Fact())
		if len(reasons) == 0 {
			return nil
		}
		return []memoryHygieneMatch{{
			MemoryID:       unit.Row.MemoryID(),
			Kind:           apptypes.MemoryHygieneSuggestionLowQualityCandidate,
			Reason:         fmt.Sprintf("low-quality extraction: %s", strings.Join(reasons, ",")),
			rawFact:        unit.Row.Fact(),
			Scope:          unit.Row.Scope(),
			UpdatedAt:      unit.Row.UpdatedAt(),
			Status:         unit.Row.Status(),
			Source:         unit.Row.Source(),
			QualityReasons: reasons,
		}}
	default:
		return nil
	}
}

func (u *memoryHygieneUsecase) acceptedRowHygieneMatches(
	summary apptypes.MemorySummary,
	now time.Time,
	staleness time.Duration,
) []memoryHygieneMatch {
	sanitized, _, _, err := sanitizeMemoryPayload(summary.Fact(), nil, nil, u.extraRedactPatterns)
	if err != nil {
		return []memoryHygieneMatch{{
			MemoryID:  summary.MemoryID(),
			Kind:      apptypes.MemoryHygieneSuggestionRedactionHit,
			Reason:    "sanitizer could not evaluate this fact",
			rawFact:   summary.Fact(),
			Scope:     summary.Scope(),
			UpdatedAt: summary.UpdatedAt(),
		}}
	}
	matches := make([]memoryHygieneMatch, 0, 2)
	if sanitized != summary.Fact() {
		matches = append(matches, memoryHygieneMatch{
			MemoryID:      summary.MemoryID(),
			Kind:          apptypes.MemoryHygieneSuggestionRedactionHit,
			Reason:        "current redaction patterns mask this fact",
			rawFact:       summary.Fact(),
			sanitizedFact: sanitized,
			Scope:         summary.Scope(),
			UpdatedAt:     summary.UpdatedAt(),
		})
	}
	if now.Sub(summary.UpdatedAt()) > staleness {
		matches = append(matches, memoryHygieneMatch{
			MemoryID:  summary.MemoryID(),
			Kind:      apptypes.MemoryHygieneSuggestionExpiryCandidate,
			Reason:    fmt.Sprintf("no updates for more than %s", staleness),
			rawFact:   summary.Fact(),
			Scope:     summary.Scope(),
			UpdatedAt: summary.UpdatedAt(),
		})
	}
	return matches
}

func similarityPairHygieneMatch(
	a apptypes.MemorySummary,
	b apptypes.MemorySummary,
	threshold float64,
) (memoryHygieneMatch, bool) {
	if a.Fact() == b.Fact() || threshold <= 0 || threshold > 1 {
		return memoryHygieneMatch{}, false
	}
	similarity := jaccardSimilarity(toWordSet(a.Fact()), toWordSet(b.Fact()))
	if similarity < threshold {
		return memoryHygieneMatch{}, false
	}
	older, newer := orderedMemoryHygienePair(a, b)
	_, aHasTo := a.ValidTo().Value()
	_, bHasTo := b.ValidTo().Value()
	if a.MemoryType() == b.MemoryType() && (aHasTo || bHasTo) {
		if !validityWindowsOverlap(a, b) {
			return memoryHygieneMatch{}, false
		}
		return memoryHygieneMatch{
			MemoryID:            older.MemoryID(),
			Kind:                apptypes.MemoryHygieneSuggestionValidityOverlapSupersede,
			Reason:              fmt.Sprintf("validity window overlaps %s at similarity %.2f", newer.MemoryID().String(), similarity),
			rawFact:             older.Fact(),
			ReplacementMemoryID: newer.MemoryID(),
			replacementFact:     newer.Fact(),
			Similarity:          similarity,
			Scope:               older.Scope(),
			UpdatedAt:           older.UpdatedAt(),
		}, true
	}
	return memoryHygieneMatch{
		MemoryID:            older.MemoryID(),
		Kind:                apptypes.MemoryHygieneSuggestionSupersedeCandidate,
		Reason:              fmt.Sprintf("scope overlap with %s at similarity %.2f", newer.MemoryID().String(), similarity),
		rawFact:             older.Fact(),
		ReplacementMemoryID: newer.MemoryID(),
		replacementFact:     newer.Fact(),
		Similarity:          similarity,
		Scope:               older.Scope(),
		UpdatedAt:           older.UpdatedAt(),
	}, true
}

func orderedMemoryHygienePair(a, b apptypes.MemorySummary) (apptypes.MemorySummary, apptypes.MemorySummary) {
	older, newer := a, b
	if older.UpdatedAt().After(newer.UpdatedAt()) ||
		(older.UpdatedAt().Equal(newer.UpdatedAt()) && older.MemoryID().String() > newer.MemoryID().String()) {
		older, newer = newer, older
	}
	return older, newer
}

func (u *memoryHygieneUsecase) safeMemoryHygieneSuggestion(match memoryHygieneMatch) apptypes.MemoryHygieneSuggestion {
	factPreview, factTruncated := u.safeMemoryHygienePreview(match.rawFact)
	sanitizedPreview, sanitizedTruncated := "", false
	if match.sanitizedFact != "" {
		sanitizedPreview, sanitizedTruncated = u.safeMemoryHygienePreview(match.sanitizedFact)
	}
	replacementPreview, replacementTruncated := "", false
	if match.replacementFact != "" {
		replacementPreview, replacementTruncated = u.safeMemoryHygienePreview(match.replacementFact)
	}
	return apptypes.MemoryHygieneSuggestion{
		MemoryID:                    match.MemoryID,
		Kind:                        match.Kind,
		Reason:                      match.Reason,
		Fact:                        match.rawFact,
		FactPreview:                 factPreview,
		FactPreviewTruncated:        factTruncated,
		SanitizedFact:               match.sanitizedFact,
		SanitizedFactPreview:        sanitizedPreview,
		SanitizedPreviewTruncated:   sanitizedTruncated,
		DuplicateMemoryID:           match.DuplicateMemoryID,
		ReplacementMemoryID:         match.ReplacementMemoryID,
		ReplacementFact:             match.replacementFact,
		ReplacementFactPreview:      replacementPreview,
		ReplacementPreviewTruncated: replacementTruncated,
		Similarity:                  match.Similarity,
		Scope:                       match.Scope,
		UpdatedAt:                   match.UpdatedAt,
		Status:                      match.Status,
		Source:                      match.Source,
		QualityReasons:              append([]string(nil), match.QualityReasons...),
	}
}

func (u *memoryHygieneUsecase) safeMemoryHygienePreview(raw string) (string, bool) {
	sanitized, _, _, err := sanitizeMemoryPayload(raw, nil, nil, u.extraRedactPatterns)
	if err != nil {
		return "[preview unavailable]", false
	}
	return truncateMemoryHygienePreview(sanitized, memoryHygienePreviewMaxBytes)
}

func truncateMemoryHygienePreview(value string, maxBytes int) (string, bool) {
	if len(value) <= maxBytes {
		return value, false
	}
	const marker = "…"
	cut := maxBytes - len(marker)
	if cut < 0 {
		cut = 0
	}
	for cut > 0 && !utf8.RuneStart(value[cut]) {
		cut--
	}
	return value[:cut] + marker, true
}

func incrementMemoryHygieneCount(result *apptypes.MemoryHygieneScanResult, kind apptypes.MemoryHygieneSuggestionKind) {
	switch kind {
	case apptypes.MemoryHygieneSuggestionRedactionHit:
		result.RedactionHitCount++
	case apptypes.MemoryHygieneSuggestionExpiryCandidate:
		result.ExpiryCandidateCount++
	case apptypes.MemoryHygieneSuggestionDuplicate:
		result.DuplicateCount++
	case apptypes.MemoryHygieneSuggestionSupersedeCandidate:
		result.SupersedeCandidateCount++
	case apptypes.MemoryHygieneSuggestionValidityOverlapSupersede:
		result.ValidityOverlapSupersedeCount++
	case apptypes.MemoryHygieneSuggestionLowQualityCandidate:
		result.LowQualityCandidateCount++
	}
}

func nextMemoryHygieneScanPhase(phase apptypes.MemoryHygieneScanPhase) (apptypes.MemoryHygieneScanPhase, bool) {
	switch phase {
	case apptypes.MemoryHygieneScanPhaseAcceptedRows:
		return apptypes.MemoryHygieneScanPhaseExactDuplicates, false
	case apptypes.MemoryHygieneScanPhaseExactDuplicates:
		return apptypes.MemoryHygieneScanPhaseSimilarityPairs, false
	case apptypes.MemoryHygieneScanPhaseSimilarityPairs:
		return apptypes.MemoryHygieneScanPhaseCandidateRows, false
	case apptypes.MemoryHygieneScanPhaseCandidateRows:
		return "", true
	default:
		return "", true
	}
}

func finishPartialMemoryHygieneResult(
	result apptypes.MemoryHygieneScanResult,
	reason apptypes.MemoryHygieneStopReason,
	phase apptypes.MemoryHygieneScanPhase,
	keyset apptypes.MemoryHygieneScanKeyset,
	revision domtypes.Optional[int64],
	consistency apptypes.MemoryHygieneScanConsistency,
	consistencyReason apptypes.MemoryHygieneConsistencyReason,
	digest string,
	now time.Time,
	startedAt time.Time,
	initialPhase apptypes.MemoryHygieneScanPhase,
	initialKeyset apptypes.MemoryHygieneScanKeyset,
	initialRevision domtypes.Optional[int64],
) (apptypes.MemoryHygieneScanResult, error) {
	if phase == initialPhase && keyset == initialKeyset {
		revisionValue, hasRevision := revision.Value()
		initialRevisionValue, hadInitialRevision := initialRevision.Value()
		if !hasRevision || !hadInitialRevision || revisionValue == initialRevisionValue {
			return apptypes.MemoryHygieneScanResult{}, xerrors.Errorf("%w: %s budget stopped before advancing the cursor", queryservice.ErrMemoryHygieneContinuationCannotProgress, reason)
		}
	}
	revisionValue, ok := revision.Value()
	if !ok {
		return apptypes.MemoryHygieneScanResult{}, xerrors.Errorf("memory hygiene scan stopped before reading a revision")
	}
	cursor, err := encodeMemoryHygieneCursor(memoryHygieneCursorPayload{
		Revision:          revisionValue,
		CriteriaDigest:    digest,
		ScanAt:            now.UTC().Format(time.RFC3339Nano),
		Phase:             phase,
		Keyset:            keyset,
		Consistency:       consistency,
		ConsistencyReason: consistencyReason,
	})
	if err != nil {
		return apptypes.MemoryHygieneScanResult{}, err
	}
	result.Complete = false
	result.Partial = true
	result.StopReason = reason
	result.Consistency = consistency
	result.ConsistencyReason = consistencyReason
	result.NextCursor = cursor
	result.Usage.Elapsed = time.Since(startedAt)
	result.Usage.ElapsedMillis = result.Usage.Elapsed.Milliseconds()
	return result, nil
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

// Apply revalidates only requested memory IDs and their same-scope peers
// before applying a lifecycle transition. Every requested revalidation must
// complete at the same revision before the first mutation, so a partial or
// stale result fails closed without applying a prefix of the request.
func (u *memoryHygieneUsecase) Apply(ctx context.Context, criteria apptypes.MemoryHygieneApplyCriteria) (apptypes.MemoryHygieneApplyResult, error) {
	if u.memory == nil {
		return apptypes.MemoryHygieneApplyResult{}, xerrors.Errorf("memory usecase is not configured")
	}
	source, ok := u.memoryQuery.(queryservice.MemoryHygieneScanSource)
	if !ok {
		return apptypes.MemoryHygieneApplyResult{}, xerrors.Errorf("bounded memory hygiene revalidation source is not configured")
	}

	now := criteria.Now.UTC()
	if now.IsZero() {
		now = time.Now().UTC()
	}
	staleness := criteria.StalenessThreshold
	if staleness <= 0 {
		staleness = defaultStalenessThreshold
	}
	budget := apptypes.DefaultMemoryHygieneScanBudget()
	revalidationCtx, cancel := context.WithTimeout(ctx, budget.MaxDuration())
	defer cancel()

	result := apptypes.MemoryHygieneApplyResult{}
	type applyPlan struct {
		memoryID   domtypes.MemoryID
		suggestion apptypes.MemoryHygieneSuggestion
	}
	plans := make([]applyPlan, 0, len(criteria.MemoryIDs))
	var revision int64
	hasRevision := false

	for _, rawID := range criteria.MemoryIDs {
		trimmed := strings.TrimSpace(rawID)
		if trimmed == "" {
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

		revalidation, err := source.RevalidateMemoryHygiene(revalidationCtx, apptypes.MemoryHygieneRevalidationCriteria{
			MemoryID:                memoryID,
			IncludeHiddenCandidates: criteria.IncludeHiddenCandidates,
			MaxRows:                 budget.MaxRows(),
			MaxScanBytes:            budget.MaxScanBytes(),
			MaxComparisons:          budget.MaxComparisons(),
		})
		if !hasRevision {
			revision = revalidation.Revision
			hasRevision = true
		} else if revalidation.Revision != revision {
			return apptypes.MemoryHygieneApplyResult{}, xerrors.Errorf("%w", queryservice.ErrMemoryHygieneRevisionChanged)
		}
		if err != nil {
			if errors.Is(err, queryservice.ErrMemoryHygieneRevisionChanged) {
				return apptypes.MemoryHygieneApplyResult{}, xerrors.Errorf("failed to revalidate memory hygiene: %w", err)
			}
			if errors.Is(err, context.DeadlineExceeded) || errors.Is(revalidationCtx.Err(), context.DeadlineExceeded) {
				return apptypes.MemoryHygieneApplyResult{}, xerrors.Errorf("memory hygiene apply revalidation stopped at %s", apptypes.MemoryHygieneStopReasonTimeLimit)
			}
			result.Failures = append(result.Failures, apptypes.MemoryHygieneApplyFailure{
				MemoryID: trimmed,
				Error:    err.Error(),
			})
			continue
		}
		if !revalidation.Complete {
			return apptypes.MemoryHygieneApplyResult{}, xerrors.Errorf("memory hygiene apply revalidation stopped at %s", revalidation.StopReason)
		}
		suggestion, found := u.suggestionForMemoryHygieneRevalidation(
			revalidation,
			now,
			staleness,
			defaultSupersedeSimilarityThreshold,
			criteria.IncludeHiddenCandidates,
		)
		if !found {
			result.Failures = append(result.Failures, apptypes.MemoryHygieneApplyFailure{
				MemoryID: trimmed,
				Error:    "no current hygiene suggestion for this memory",
			})
			continue
		}
		plans = append(plans, applyPlan{memoryID: memoryID, suggestion: suggestion})
	}
	if errors.Is(revalidationCtx.Err(), context.DeadlineExceeded) {
		return apptypes.MemoryHygieneApplyResult{}, xerrors.Errorf("memory hygiene apply revalidation stopped at %s", apptypes.MemoryHygieneStopReasonTimeLimit)
	}

	for _, plan := range plans {
		applied, err := u.applyOne(ctx, plan.memoryID, plan.suggestion)
		if err != nil {
			result.Failures = append(result.Failures, apptypes.MemoryHygieneApplyFailure{
				MemoryID: plan.memoryID.String(),
				Error:    err.Error(),
			})
			continue
		}
		result.Applied = append(result.Applied, applied)
	}
	return result, nil
}

func (u *memoryHygieneUsecase) suggestionForMemoryHygieneRevalidation(
	revalidation apptypes.MemoryHygieneRevalidationSourceResult,
	now time.Time,
	staleness time.Duration,
	similarity float64,
	includeHiddenCandidates bool,
) (apptypes.MemoryHygieneSuggestion, bool) {
	target := revalidation.Target
	var selected memoryHygieneMatch
	hasSelected := false
	selectMatch := func(match memoryHygieneMatch) {
		if hasSelected && selected.Kind == apptypes.MemoryHygieneSuggestionRedactionHit {
			return
		}
		selected = match
		hasSelected = true
	}

	switch target.Status() {
	case domtypes.MemoryStatusAccepted:
		for _, match := range u.acceptedRowHygieneMatches(target, now, staleness) {
			selectMatch(match)
		}
		if peerID, ok := revalidation.ExactDuplicateMemoryID.Value(); ok {
			selectMatch(memoryHygieneMatch{
				MemoryID:          target.MemoryID(),
				Kind:              apptypes.MemoryHygieneSuggestionDuplicate,
				Reason:            fmt.Sprintf("shares fact with %s", peerID.String()),
				rawFact:           target.Fact(),
				DuplicateMemoryID: peerID,
				Scope:             target.Scope(),
				UpdatedAt:         target.UpdatedAt(),
			})
		}
		for _, peer := range revalidation.Peers {
			match, matched := similarityPairHygieneMatch(target, peer, similarity)
			if matched && match.MemoryID == target.MemoryID() {
				selectMatch(match)
			}
		}
	case domtypes.MemoryStatusCandidate:
		if target.Source() != domtypes.MemorySourceExtracted &&
			(!includeHiddenCandidates || target.Source() != domtypes.MemorySourceExtractedHidden) {
			return apptypes.MemoryHygieneSuggestion{}, false
		}
		reasons := classifyExtractionNoise(target.Fact())
		if len(reasons) > 0 {
			selectMatch(memoryHygieneMatch{
				MemoryID:       target.MemoryID(),
				Kind:           apptypes.MemoryHygieneSuggestionLowQualityCandidate,
				Reason:         fmt.Sprintf("low-quality extraction: %s", strings.Join(reasons, ",")),
				rawFact:        target.Fact(),
				Scope:          target.Scope(),
				UpdatedAt:      target.UpdatedAt(),
				Status:         target.Status(),
				Source:         target.Source(),
				QualityReasons: reasons,
			})
		}
	}
	if !hasSelected {
		return apptypes.MemoryHygieneSuggestion{}, false
	}
	return u.safeMemoryHygieneSuggestion(selected), true
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
	case apptypes.MemoryHygieneSuggestionLowQualityCandidate:
		// Low-quality candidates are rejected outright. The scan only
		// flags status=candidate rows so this branch can never receive
		// an accepted memory id, satisfying the issue's guarantee that
		// the candidate cleanup path leaves accepted memories alone.
		// Reject() in turn refuses to operate on accepted memories, so
		// any race that flipped the row to accepted between scan and
		// apply surfaces as an error in the per-id failure list.
		rejected, err := u.memory.Reject(ctx, memoryID)
		if err != nil {
			return apptypes.MemoryHygieneApplied{}, xerrors.Errorf("failed to reject low-quality candidate: %w", err)
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
