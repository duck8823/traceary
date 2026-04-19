package usecase

import (
	"context"
	"fmt"
	"slices"
	"time"

	"golang.org/x/xerrors"

	"github.com/duck8823/traceary/application/queryservice"
	apptypes "github.com/duck8823/traceary/application/types"
	domtypes "github.com/duck8823/traceary/domain/types"
)

type memoryHygieneUsecase struct {
	memoryQuery         queryservice.MemoryQueryService
	extraRedactPatterns []string
}

// NewMemoryHygieneUsecase creates a MemoryHygieneUsecase. The scanner
// shares the sanitizer with every other memory-write surface so a
// redaction pattern added to the config is detected uniformly: the scan
// reports any memory whose sanitized fact differs from the stored fact,
// which is exactly the set of memories a later supersede would rewrite.
func NewMemoryHygieneUsecase(memoryQuery queryservice.MemoryQueryService, extraRedactPatterns []string) MemoryHygieneUsecase {
	return &memoryHygieneUsecase{
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

	builder := apptypes.NewMemoryListCriteriaBuilder(hygieneScanPageSize).
		Statuses([]domtypes.MemoryStatus{domtypes.MemoryStatusAccepted})
	if len(criteria.Scopes) > 0 {
		builder = builder.Scopes(criteria.Scopes)
	}
	summaries, err := u.memoryQuery.List(ctx, builder.Build())
	if err != nil {
		return apptypes.MemoryHygieneScanResult{}, xerrors.Errorf("failed to list accepted memories: %w", err)
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
		// reviewer sees both sides.
		key := duplicateKey{ScopeKind: summary.Scope().Kind(), ScopeKey: summary.Scope().Key(), Fact: summary.Fact()}
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
