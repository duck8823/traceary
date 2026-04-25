package usecase

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"slices"
	"strings"

	"golang.org/x/xerrors"

	"github.com/duck8823/traceary/application/queryservice"
	apptypes "github.com/duck8823/traceary/application/types"
	domtypes "github.com/duck8823/traceary/domain/types"
)

type memoryBridgeImportUsecase struct {
	memoryUsecase       memoryProposer
	memoryQuery         queryservice.MemoryQueryService
	extraRedactPatterns []string
}

// ImportInstructions parses the instruction file referenced by criteria,
// drops the Traceary-managed block (that block is already represented in
// the durable-memory store via export), and creates a candidate for
// every free-form bullet so the operator can review them in the inbox.
func (u *memoryBridgeImportUsecase) ImportInstructions(ctx context.Context, criteria apptypes.MemoryBridgeImportCriteria) (apptypes.MemoryBridgeImportResult, error) {
	if u.memoryUsecase == nil {
		return apptypes.MemoryBridgeImportResult{}, xerrors.Errorf("memory usecase is not configured")
	}
	if u.memoryQuery == nil {
		return apptypes.MemoryBridgeImportResult{}, xerrors.Errorf("memory query service is not configured")
	}
	if _, ok := apptypes.MemoryBridgeTargetOf(criteria.Target.String()); !ok {
		return apptypes.MemoryBridgeImportResult{}, xerrors.Errorf("unsupported bridge import target: %s", criteria.Target)
	}

	content, err := loadBridgeSource(criteria)
	if err != nil {
		return apptypes.MemoryBridgeImportResult{}, err
	}

	sourceLabel := bridgeSourceLabel(criteria)
	bullets, warnings := parseBridgeMarkdown(content, sourceLabel)

	result := apptypes.MemoryBridgeImportResult{
		Warnings: slices.Clone(warnings),
	}
	if criteria.WorkspaceFallback.String() == "" {
		if len(bullets) > 0 {
			result.Warnings = append(result.Warnings, fmt.Sprintf("--workspace must be set to scope bridge-imported candidates; skipped %d bullets", len(bullets)))
		}
		return result, nil
	}
	scope := domtypes.WorkspaceScopeOf(criteria.WorkspaceFallback)

	existingIndex, err := u.loadExistingBridgeIndex(ctx, scope)
	if err != nil {
		return apptypes.MemoryBridgeImportResult{}, xerrors.Errorf("failed to preload existing imported memories: %w", err)
	}

	for _, bullet := range bullets {
		evidence, err := domtypes.EvidenceRefFrom(domtypes.EvidenceRefKindFile, fmt.Sprintf("%s#L%d", bullet.SourcePath, bullet.Line))
		if err != nil {
			result.Warnings = append(result.Warnings, fmt.Sprintf("skipped bullet at %s:%d (evidence ref rejected: %v)", bullet.SourcePath, bullet.Line, err))
			continue
		}
		artifact, err := domtypes.ArtifactRefFrom(domtypes.ArtifactRefKindFile, bullet.SourcePath)
		if err != nil {
			result.Warnings = append(result.Warnings, fmt.Sprintf("skipped bullet at %s:%d (artifact ref rejected: %v)", bullet.SourcePath, bullet.Line, err))
			continue
		}

		// Single sanitizer pass — fact and refs go through together so
		// redaction runs exactly once per bullet.
		sanitizedFact, sanitizedEvidence, sanitizedArtifacts, err := sanitizeMemoryPayload(bullet.Fact, []domtypes.EvidenceRef{evidence}, []domtypes.ArtifactRef{artifact}, u.extraRedactPatterns)
		if err != nil {
			result.Warnings = append(result.Warnings, fmt.Sprintf("skipped bullet at %s:%d (sanitize failed: %v)", bullet.SourcePath, bullet.Line, err))
			continue
		}
		if sanitizedFact == "" {
			continue
		}

		key := bridgeDedupeKey{ScopeKind: scope.Kind(), ScopeKey: scope.Key(), SourcePath: bullet.SourcePath, Fact: sanitizedFact}
		if existing, ok := existingIndex[key]; ok {
			if existing == domtypes.MemoryStatusCandidate || existing == domtypes.MemoryStatusAccepted {
				result.SkippedDuplicateCount++
			} else {
				result.SkippedRejectedCount++
			}
			continue
		}

		details, err := u.memoryUsecase.Propose(
			ctx,
			bridgeMemoryTypeDefault,
			scope,
			sanitizedFact,
			domtypes.MemorySourceImported,
			sanitizedEvidence,
			sanitizedArtifacts,
		)
		if err != nil {
			result.Warnings = append(result.Warnings, fmt.Sprintf("skipped bullet at %s:%d (propose failed: %v)", bullet.SourcePath, bullet.Line, err))
			continue
		}
		result.Imported = append(result.Imported, details)
		existingIndex[key] = details.Summary().Status()
	}
	return result, nil
}

// bridgeMemoryTypeDefault is the memory type applied to every bullet
// parsed out of a host instruction file. Traceary does not try to
// sub-classify free-form markdown text, so we pick preference as the most
// neutral default and leave re-classification to the reviewer.
const bridgeMemoryTypeDefault = domtypes.MemoryTypePreference

type bridgeDedupeKey struct {
	ScopeKind  domtypes.MemoryScopeKind
	ScopeKey   string
	SourcePath string
	Fact       string
}

type bridgeBullet struct {
	Fact       string
	Line       int
	SourcePath string
}

func loadBridgeSource(criteria apptypes.MemoryBridgeImportCriteria) (string, error) {
	if criteria.Markdown != "" {
		return criteria.Markdown, nil
	}
	if criteria.Path == "" {
		return "", xerrors.Errorf("bridge import requires either Path or Markdown")
	}
	data, err := os.ReadFile(criteria.Path)
	if err != nil {
		return "", xerrors.Errorf("failed to read %s: %w", criteria.Path, err)
	}
	return string(data), nil
}

// bridgeSourceLabel is the source path label used for provenance refs.
// When the caller supplied an on-disk Path we use it directly; when the
// caller supplied inline Markdown we fall back to a synthetic "<inline
// <target>>" label so the evidence / artifact refs never fail the
// "value must not be empty" guard in the domain layer.
func bridgeSourceLabel(criteria apptypes.MemoryBridgeImportCriteria) string {
	if strings.TrimSpace(criteria.Path) != "" {
		return criteria.Path
	}
	return fmt.Sprintf("<inline %s>", criteria.Target.String())
}

// parseBridgeMarkdown returns every bullet (`- ...`) outside the
// Traceary-managed marker block. Bullets inside the marker block are
// already represented in the durable-memory store and would produce
// duplicates on re-import, so they are skipped. The returned bullets
// carry their source line so the CLI can hand the operator a precise
// evidence ref.
func parseBridgeMarkdown(content, sourcePath string) ([]bridgeBullet, []string) {
	var (
		bullets    []bridgeBullet
		warnings   []string
		inside     bool
		lineNumber int
	)
	scanner := bufio.NewScanner(strings.NewReader(content))
	scanner.Buffer(make([]byte, 0, 64*1024), bridgeBulletMaxBytes+64*1024)
	for scanner.Scan() {
		lineNumber++
		line := scanner.Text()
		trimmed := strings.TrimSpace(line)
		if version, isBegin := MatchMemoryBridgeBeginLine(trimmed); isBegin {
			inside = true
			if version > MemoryBridgeCurrentVersion {
				warnings = append(warnings, fmt.Sprintf(
					"bridge block at %s:%d uses marker version v%d which this Traceary build (v%d) does not recognise; do not overwrite the block with this binary",
					sourcePath, lineNumber, version, MemoryBridgeCurrentVersion,
				))
			}
			continue
		}
		if trimmed == MemoryBridgeMarkerEnd {
			inside = false
			continue
		}
		if inside {
			continue
		}
		if !strings.HasPrefix(trimmed, "- ") {
			continue
		}
		fact := strings.TrimSpace(strings.TrimPrefix(trimmed, "-"))
		if len(fact) > bridgeBulletMaxBytes {
			warnings = append(warnings, fmt.Sprintf("bullet at %s:%d exceeds size guard; skipping", sourcePath, lineNumber))
			continue
		}
		if fact == "" {
			continue
		}
		bullets = append(bullets, bridgeBullet{Fact: fact, Line: lineNumber, SourcePath: sourcePath})
	}
	if err := scanner.Err(); err != nil {
		warnings = append(warnings, fmt.Sprintf("failed to scan %s: %v", sourcePath, err))
	}
	return bullets, warnings
}

// bridgeBulletMaxBytes clamps a single bullet so a pathological multi-
// megabyte line cannot produce a durable-memory candidate that dominates
// downstream read commands. The limit matches the Codex memory parser.
const bridgeBulletMaxBytes = 32 * 1024

// bridgeDedupePageSize caps the dedupe preload. Real operators carry a
// few hundred imported memories at most; 2000 is far beyond practical
// volumes while still honouring the store's limit >= 1 contract.
const bridgeDedupePageSize = 2000

func (u *memoryBridgeImportUsecase) loadExistingBridgeIndex(ctx context.Context, scope domtypes.MemoryScope) (map[bridgeDedupeKey]domtypes.MemoryStatus, error) {
	index := make(map[bridgeDedupeKey]domtypes.MemoryStatus)
	if scope == nil {
		return index, nil
	}
	// maxMemoryBridgeRows mirrors the export page size so the dedupe
	// preload sees the same ceiling the export path enforces. The real
	// SQLite datasource requires limit >= 1, so zero must never be used.
	criteria := apptypes.NewMemoryListCriteriaBuilder(bridgeDedupePageSize).
		Scope(scope).
		Statuses([]domtypes.MemoryStatus{
			domtypes.MemoryStatusCandidate,
			domtypes.MemoryStatusAccepted,
			domtypes.MemoryStatusRejected,
			domtypes.MemoryStatusSuperseded,
			domtypes.MemoryStatusExpired,
		}).
		Source(domtypes.MemorySourceImported).
		Build()
	summaries, err := u.memoryQuery.List(ctx, criteria)
	if err != nil {
		return nil, xerrors.Errorf("failed to list existing bridge imports: %w", err)
	}
	for _, summary := range summaries {
		details, err := u.memoryQuery.GetDetails(ctx, summary.MemoryID())
		if err != nil {
			return nil, xerrors.Errorf("failed to load existing bridge import %s: %w", summary.MemoryID().String(), err)
		}
		for _, ref := range details.ArtifactRefs() {
			if ref.Kind() != domtypes.ArtifactRefKindFile {
				continue
			}
			key := bridgeDedupeKey{ScopeKind: scope.Kind(), ScopeKey: scope.Key(), SourcePath: ref.Value(), Fact: summary.Fact()}
			if _, ok := index[key]; !ok {
				index[key] = summary.Status()
			}
		}
	}
	return index, nil
}
