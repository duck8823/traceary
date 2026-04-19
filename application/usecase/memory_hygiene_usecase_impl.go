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

	return result, nil
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
		memoryID, err := domtypes.MemoryIDOf(trimmed)
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
