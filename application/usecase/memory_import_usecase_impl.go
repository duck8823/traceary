package usecase

import (
	"context"
	"fmt"
	"slices"

	"golang.org/x/xerrors"

	"github.com/duck8823/traceary/application"
	"github.com/duck8823/traceary/application/queryservice"
	apptypes "github.com/duck8823/traceary/application/types"
	domtypes "github.com/duck8823/traceary/domain/types"
)

type memoryImportUsecase struct {
	memoryUsecase       memoryProposer
	memoryQuery         queryservice.MemoryQueryService
	codexSource         application.CodexMemorySource
	extraRedactPatterns []string
}

// NewMemoryImportUsecase creates a MemoryImportUsecase. The sanitizer uses
// the same extra redaction patterns as the durable-memory write path so a
// single config source covers both manual writes and imports.
//
// Deprecated: use NewMemoryUsecase with MemoryUsecaseDependencies and call ImportCodex.
func NewMemoryImportUsecase(
	memory memoryProposer,
	memoryQuery queryservice.MemoryQueryService,
	codexSource application.CodexMemorySource,
	extraRedactPatterns []string,
) MemoryImportUsecase {
	if facade, ok := memory.(*memoryUsecase); ok {
		facade.memoryQuery = memoryQuery
		facade.codexSource = codexSource
		facade.extraRedactPatterns = slices.Clone(extraRedactPatterns)
		return facade
	}
	return &memoryImportUsecase{
		memoryUsecase:       memory,
		memoryQuery:         memoryQuery,
		codexSource:         codexSource,
		extraRedactPatterns: slices.Clone(extraRedactPatterns),
	}
}

// ImportCodex loads candidate rows out of the configured Codex memory root,
// sanitizes each one, and proposes brand-new candidates through the shared
// durable-memory usecase. Rows that duplicate an existing memory (at any
// status, so rejected memories are never resurrected) are counted but not
// re-persisted.
func (u *memoryImportUsecase) ImportCodex(ctx context.Context, criteria apptypes.CodexImportCriteria) (apptypes.MemoryImportResult, error) {
	if u.memoryUsecase == nil {
		return apptypes.MemoryImportResult{}, xerrors.Errorf("memory usecase is not configured")
	}
	if u.memoryQuery == nil {
		return apptypes.MemoryImportResult{}, xerrors.Errorf("memory query service is not configured")
	}
	if u.codexSource == nil {
		return apptypes.MemoryImportResult{}, xerrors.Errorf("codex memory source is not configured")
	}

	candidates, warnings, err := u.codexSource.Load(ctx, criteria)
	if err != nil {
		return apptypes.MemoryImportResult{}, xerrors.Errorf("failed to load codex memory candidates: %w", err)
	}

	result := apptypes.MemoryImportResult{
		Warnings: slices.Clone(warnings),
	}

	existingIndex, err := u.loadExistingImportedIndex(ctx, candidates)
	if err != nil {
		return apptypes.MemoryImportResult{}, xerrors.Errorf("failed to load existing imported memories: %w", err)
	}

	for _, candidate := range candidates {
		sanitizedFact, sanitizedEvidence, sanitizedArtifacts, err := sanitizeMemoryPayload(
			candidate.Fact,
			candidate.EvidenceRefs,
			candidate.ArtifactRefs,
			u.extraRedactPatterns,
		)
		if err != nil {
			result.Warnings = append(result.Warnings, fmt.Sprintf("skipped candidate %q: sanitizer failed: %v", truncate(candidate.Fact, 80), err))
			continue
		}
		if sanitizedFact == "" {
			result.Warnings = append(result.Warnings, fmt.Sprintf("skipped candidate at %s: sanitized fact is empty", candidate.SourcePath))
			continue
		}

		dedupKey := importDedupeKey{
			ScopeKind:  candidate.Scope.Kind(),
			ScopeKey:   candidate.Scope.Key(),
			SourcePath: candidate.SourcePath,
			Fact:       sanitizedFact,
		}
		if existing, ok := existingIndex[dedupKey]; ok {
			if existing.Status() == domtypes.MemoryStatusCandidate || existing.Status() == domtypes.MemoryStatusAccepted {
				result.SkippedDuplicateCount++
			} else {
				result.SkippedRejectedCount++
			}
			continue
		}

		details, err := u.memoryUsecase.Propose(
			ctx,
			candidate.MemoryType,
			candidate.Scope,
			sanitizedFact,
			domtypes.MemorySourceImported,
			sanitizedEvidence,
			sanitizedArtifacts,
		)
		if err != nil {
			result.Warnings = append(result.Warnings, fmt.Sprintf("skipped candidate at %s: propose failed: %v", candidate.SourcePath, err))
			continue
		}
		result.Imported = append(result.Imported, details)
		existingIndex[dedupKey] = importDedupeHit{status: details.Summary().Status()}
	}

	return result, nil
}

// importDedupeKey is the composite fingerprint used to detect a candidate
// that is already represented in the durable-memory store. Including
// SourcePath keeps two distinct host-native files (or the same file under
// a different memory root) from silently collapsing into a single memory —
// a rejected fact from one root must not block the same fact appearing in
// another root. Imports trust this fingerprint across all lifecycle
// statuses so rejected candidates are never quietly resurrected.
type importDedupeKey struct {
	ScopeKind  domtypes.MemoryScopeKind
	ScopeKey   string
	SourcePath string
	Fact       string
}

type importDedupeHit struct {
	status domtypes.MemoryStatus
}

func (h importDedupeHit) Status() domtypes.MemoryStatus { return h.status }

// loadExistingImportedIndex prefetches every imported memory across all
// statuses. The import path is expected to be infrequent, so O(N) over
// imported memories is acceptable and keeps the dedupe logic simple.
func (u *memoryImportUsecase) loadExistingImportedIndex(
	ctx context.Context,
	candidates []apptypes.ImportedMemoryCandidate,
) (map[importDedupeKey]importDedupeHit, error) {
	index := make(map[importDedupeKey]importDedupeHit)
	if len(candidates) == 0 {
		return index, nil
	}

	// Fetch all statuses so rejected/superseded/expired memories still
	// suppress a re-import (the operator explicitly said "no" once).
	statuses := []domtypes.MemoryStatus{
		domtypes.MemoryStatusCandidate,
		domtypes.MemoryStatusAccepted,
		domtypes.MemoryStatusRejected,
		domtypes.MemoryStatusSuperseded,
		domtypes.MemoryStatusExpired,
	}

	uniqueScopes := deduplicateScopes(candidates)
	for _, scope := range uniqueScopes {
		// The real SQLite datasource requires limit >= 1; use the same
		// page size the bridge dedupe path uses so both import surfaces
		// share a ceiling (and never trip the datasource guard).
		criteria := apptypes.NewMemoryListCriteriaBuilder(bridgeDedupePageSize).
			Scope(scope).
			Statuses(statuses).
			Build()
		summaries, err := u.memoryQuery.List(ctx, criteria)
		if err != nil {
			return nil, xerrors.Errorf("failed to list memories for scope %s: %w", scope.Key(), err)
		}
		for _, summary := range summaries {
			if summary.Source() != domtypes.MemorySourceImported {
				continue
			}
			// Artifact refs carry the MEMORY.md path we use for dedupe;
			// GetDetails is the cheapest way to read them without a new
			// query-service surface. Import runs are infrequent, so the
			// extra lookup per imported memory is not a hot path.
			details, err := u.memoryQuery.GetDetails(ctx, summary.MemoryID())
			if err != nil {
				return nil, xerrors.Errorf("failed to load existing imported memory %s: %w", summary.MemoryID().String(), err)
			}
			for _, sourcePath := range extractFileArtifactPaths(details) {
				key := importDedupeKey{
					ScopeKind:  summary.Scope().Kind(),
					ScopeKey:   summary.Scope().Key(),
					SourcePath: sourcePath,
					Fact:       summary.Fact(),
				}
				if _, exists := index[key]; !exists {
					index[key] = importDedupeHit{status: summary.Status()}
				}
			}
		}
	}
	return index, nil
}

// extractFileArtifactPaths pulls every file-kind artifact ref off an
// existing memory so the import path can key dedupe on the exact source
// file (for example `/Users/x/.codex/memories/MEMORY.md`). Non-file refs
// are ignored because they do not map to a candidate's SourcePath.
func extractFileArtifactPaths(details apptypes.MemoryDetails) []string {
	paths := make([]string, 0, len(details.ArtifactRefs()))
	for _, ref := range details.ArtifactRefs() {
		if ref.Kind() != domtypes.ArtifactRefKindFile {
			continue
		}
		paths = append(paths, ref.Value())
	}
	return paths
}

func deduplicateScopes(candidates []apptypes.ImportedMemoryCandidate) []domtypes.MemoryScope {
	seen := make(map[string]struct{}, len(candidates))
	scopes := make([]domtypes.MemoryScope, 0, len(candidates))
	for _, c := range candidates {
		if c.Scope == nil {
			continue
		}
		key := string(c.Scope.Kind()) + "|" + c.Scope.Key()
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		scopes = append(scopes, c.Scope)
	}
	return scopes
}

func truncate(value string, limit int) string {
	if len(value) <= limit {
		return value
	}
	return value[:limit] + "…"
}
