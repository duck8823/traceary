package usecase

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"golang.org/x/xerrors"

	"github.com/duck8823/traceary/application/queryservice"
	apptypes "github.com/duck8823/traceary/application/types"
	domtypes "github.com/duck8823/traceary/domain/types"
)

type memoryExportUsecase struct {
	memoryQuery queryservice.MemoryQueryService
}

// NewMemoryExportUsecase creates a MemoryExportUsecase.
func NewMemoryExportUsecase(memoryQuery queryservice.MemoryQueryService) MemoryExportUsecase {
	return &memoryExportUsecase{memoryQuery: memoryQuery}
}

// MemoryBridgeMarkerBegin / End wrap every block Traceary manages inside a
// host instruction file. Matching on these exact strings is the contract
// the import usecase relies on, so changing them is a breaking change.
const (
	MemoryBridgeMarkerBegin = "<!-- traceary-memories:begin:v1 -->"
	MemoryBridgeMarkerEnd   = "<!-- traceary-memories:end -->"
	memoryBridgeWarning     = "<!-- DO NOT EDIT: this block is managed by `traceary memory export`. Hand edits will be overwritten on the next export. -->"
)

// Export loads every accepted memory in scope, groups it by memory type,
// and renders the markdown block Traceary writes into CLAUDE.md /
// AGENTS.md / GEMINI.md. The function produces the same output for the
// same input so operators can commit the generated file and use diffs to
// track drift between memory updates.
func (u *memoryExportUsecase) Export(ctx context.Context, criteria apptypes.MemoryExportCriteria) (apptypes.MemoryExportResult, error) {
	if u.memoryQuery == nil {
		return apptypes.MemoryExportResult{}, xerrors.Errorf("memory query service is not configured")
	}
	if _, ok := apptypes.MemoryBridgeTargetOf(criteria.Target.String()); !ok {
		return apptypes.MemoryExportResult{}, xerrors.Errorf("unsupported memory export target: %s", criteria.Target)
	}

	builder := apptypes.NewMemoryListCriteriaBuilder(maxMemoryBridgeRows).
		Statuses([]domtypes.MemoryStatus{domtypes.MemoryStatusAccepted})
	if len(criteria.Scopes) > 0 {
		builder = builder.Scopes(criteria.Scopes)
	}
	list := builder.Build()

	summaries, err := u.memoryQuery.List(ctx, list)
	if err != nil {
		return apptypes.MemoryExportResult{}, xerrors.Errorf("failed to list accepted memories: %w", err)
	}

	markdown := renderMemoryBridgeBlock(criteria.Target, summaries)
	return apptypes.MemoryExportResult{
		Target:        criteria.Target,
		Scopes:        criteria.Scopes,
		Markdown:      markdown,
		ExportedCount: len(summaries),
	}, nil
}

// maxMemoryBridgeRows caps the single-shot export. Real operators run
// with a handful to a few hundred accepted memories at most, so an upper
// bound keeps the output deterministic (and detects runaway scopes
// loudly).
const maxMemoryBridgeRows = 2000

// renderMemoryBridgeBlock produces the markdown block Traceary owns
// inside the host instruction file. Sorting keeps the output
// idempotent — summaries come back in updated_at order from the store,
// which is not stable if two memories share the same timestamp, so the
// function re-sorts by memory type + fact.
func renderMemoryBridgeBlock(target apptypes.MemoryBridgeTarget, summaries []apptypes.MemorySummary) string {
	sorted := append([]apptypes.MemorySummary(nil), summaries...)
	sort.SliceStable(sorted, func(i, j int) bool {
		if sorted[i].MemoryType() != sorted[j].MemoryType() {
			return sorted[i].MemoryType() < sorted[j].MemoryType()
		}
		return sorted[i].Fact() < sorted[j].Fact()
	})

	grouped := groupMemoriesByType(sorted)
	typeOrder := []domtypes.MemoryType{
		domtypes.MemoryTypePreference,
		domtypes.MemoryTypeDecision,
		domtypes.MemoryTypeConstraint,
		domtypes.MemoryTypeLesson,
		domtypes.MemoryTypeArtifact,
	}

	var body strings.Builder
	body.WriteString(MemoryBridgeMarkerBegin)
	body.WriteString("\n")
	body.WriteString(memoryBridgeWarning)
	body.WriteString("\n\n")
	fmt.Fprintf(&body, "# Traceary-managed %s memories\n\n", target.String())
	if len(sorted) == 0 {
		body.WriteString("_No accepted durable memories matched the export scope._\n\n")
	}
	for _, memoryType := range typeOrder {
		entries, ok := grouped[memoryType]
		if !ok {
			continue
		}
		fmt.Fprintf(&body, "## %s\n\n", titleForMemoryType(memoryType))
		for _, summary := range entries {
			body.WriteString("- ")
			body.WriteString(escapeMarkdownBullet(summary.Fact()))
			fmt.Fprintf(&body, " (memory_id: %s, scope: %s)\n", summary.MemoryID().String(), memoryScopeLabel(summary.Scope()))
		}
		body.WriteString("\n")
	}
	body.WriteString(MemoryBridgeMarkerEnd)
	body.WriteString("\n")
	return body.String()
}

func groupMemoriesByType(summaries []apptypes.MemorySummary) map[domtypes.MemoryType][]apptypes.MemorySummary {
	out := make(map[domtypes.MemoryType][]apptypes.MemorySummary, 5)
	for _, summary := range summaries {
		out[summary.MemoryType()] = append(out[summary.MemoryType()], summary)
	}
	return out
}

func titleForMemoryType(memoryType domtypes.MemoryType) string {
	switch memoryType {
	case domtypes.MemoryTypePreference:
		return "Preferences"
	case domtypes.MemoryTypeDecision:
		return "Decisions"
	case domtypes.MemoryTypeConstraint:
		return "Constraints"
	case domtypes.MemoryTypeLesson:
		return "Lessons"
	case domtypes.MemoryTypeArtifact:
		return "Artifacts"
	default:
		return memoryType.String()
	}
}

func memoryScopeLabel(scope domtypes.MemoryScope) string {
	if scope == nil {
		return "?"
	}
	return fmt.Sprintf("%s=%s", scope.Kind().String(), scope.Key())
}

// escapeMarkdownBullet keeps multi-line facts readable inside a bullet by
// collapsing interior newlines to spaces. The original text remains in
// the durable memory store — this is only what lands in the exported
// instruction file.
func escapeMarkdownBullet(fact string) string {
	return strings.Join(strings.Fields(fact), " ")
}
