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
	memoryUsecase       MemoryUsecase
	memoryQuery         queryservice.MemoryQueryService
	extraRedactPatterns []string
}

// NewMemoryBridgeImportUsecase creates the CLAUDE.md / AGENTS.md /
// GEMINI.md import usecase. Sanitizer and dedupe are shared with the
// Codex memories import path so everything imported through Traceary
// goes through the same redaction and "never resurrect rejected
// memories" guarantees.
func NewMemoryBridgeImportUsecase(
	memoryUsecase MemoryUsecase,
	memoryQuery queryservice.MemoryQueryService,
	extraRedactPatterns []string,
) MemoryBridgeImportUsecase {
	return &memoryBridgeImportUsecase{
		memoryUsecase:       memoryUsecase,
		memoryQuery:         memoryQuery,
		extraRedactPatterns: slices.Clone(extraRedactPatterns),
	}
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

	bullets, warnings := parseBridgeMarkdown(content, criteria.Path)

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
		sanitizedFact, _, _, err := sanitizeMemoryPayload(bullet.Fact, nil, nil, u.extraRedactPatterns)
		if err != nil {
			result.Warnings = append(result.Warnings, fmt.Sprintf("skipped bullet at %s:%d (sanitize failed: %v)", bullet.SourcePath, bullet.Line, err))
			continue
		}
		if sanitizedFact == "" {
			continue
		}

		evidence, err := domtypes.EvidenceRefOf(domtypes.EvidenceRefKindFile, fmt.Sprintf("%s#L%d", bullet.SourcePath, bullet.Line))
		if err != nil {
			result.Warnings = append(result.Warnings, fmt.Sprintf("skipped bullet at %s:%d (evidence ref rejected: %v)", bullet.SourcePath, bullet.Line, err))
			continue
		}
		artifact, err := domtypes.ArtifactRefOf(domtypes.ArtifactRefKindFile, bullet.SourcePath)
		if err != nil {
			result.Warnings = append(result.Warnings, fmt.Sprintf("skipped bullet at %s:%d (artifact ref rejected: %v)", bullet.SourcePath, bullet.Line, err))
			continue
		}

		sanitizedEvidence, sanitizedArtifacts := []domtypes.EvidenceRef{evidence}, []domtypes.ArtifactRef{artifact}
		sanitizedFact, sanitizedEvidence, sanitizedArtifacts, err = sanitizeMemoryPayload(sanitizedFact, sanitizedEvidence, sanitizedArtifacts, u.extraRedactPatterns)
		if err != nil {
			result.Warnings = append(result.Warnings, fmt.Sprintf("skipped bullet at %s:%d (ref sanitize failed: %v)", bullet.SourcePath, bullet.Line, err))
			continue
		}

		key := bridgeDedupeKey{ScopeKey: scope.Key(), SourcePath: bullet.SourcePath, Fact: sanitizedFact}
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
		switch trimmed {
		case MemoryBridgeMarkerBegin:
			inside = true
			continue
		case MemoryBridgeMarkerEnd:
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

func (u *memoryBridgeImportUsecase) loadExistingBridgeIndex(ctx context.Context, scope domtypes.MemoryScope) (map[bridgeDedupeKey]domtypes.MemoryStatus, error) {
	index := make(map[bridgeDedupeKey]domtypes.MemoryStatus)
	if scope == nil {
		return index, nil
	}
	criteria := apptypes.NewMemoryListCriteriaBuilder(0).
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
			key := bridgeDedupeKey{ScopeKey: scope.Key(), SourcePath: ref.Value(), Fact: summary.Fact()}
			if _, ok := index[key]; !ok {
				index[key] = summary.Status()
			}
		}
	}
	return index, nil
}
