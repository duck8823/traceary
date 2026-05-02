package usecase

import (
	"context"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"golang.org/x/xerrors"

	"github.com/duck8823/traceary/application/queryservice"
	apptypes "github.com/duck8823/traceary/application/types"
	domtypes "github.com/duck8823/traceary/domain/types"
)

type memoryExportUsecase struct {
	memoryQuery queryservice.MemoryQueryService
}

// MemoryBridgeMarkerBegin / End wrap every block Traceary manages inside a
// host instruction file. The exporter always writes the current version
// (MemoryBridgeCurrentVersion) so consumers see a stable stamp; the
// importer accepts any `:v<N>` suffix through MatchMemoryBridgeBeginLine
// so a future `:v2` build never reimports an older exporter's block as
// free-form candidates.
const (
	MemoryBridgeMarkerBegin    = "<!-- traceary-memories:begin:v1 -->"
	MemoryBridgeMarkerEnd      = "<!-- traceary-memories:end -->"
	MemoryBridgeCurrentVersion = 1
	memoryBridgeWarning        = "<!-- DO NOT EDIT: this block is managed by `traceary memory export`. Hand edits will be overwritten on the next export. -->"
)

// memoryBridgeBeginPattern matches every begin marker Traceary has ever
// written or will plausibly write — the suffix is an unsigned integer so
// the importer can recognise future versions without a code change.
var memoryBridgeBeginPattern = regexp.MustCompile(`^<!-- traceary-memories:begin:v(\d+) -->$`)

// MatchMemoryBridgeBeginLine reports whether the trimmed line is a
// Traceary begin marker. The returned version is the encoded `v<N>` so
// the caller can warn when it exceeds MemoryBridgeCurrentVersion.
func MatchMemoryBridgeBeginLine(line string) (version int, ok bool) {
	match := memoryBridgeBeginPattern.FindStringSubmatch(line)
	if match == nil {
		return 0, false
	}
	parsed, err := strconv.Atoi(match[1])
	if err != nil {
		return 0, false
	}
	return parsed, true
}

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
	exportScopes := memoryExportScopes(criteria.Scopes, criteria.IncludeGlobal)
	if len(exportScopes) > 0 {
		builder = builder.Scopes(exportScopes)
	}
	list := builder.Build()

	summaries, err := u.memoryQuery.List(ctx, list)
	if err != nil {
		return apptypes.MemoryExportResult{}, xerrors.Errorf("failed to list accepted memories: %w", err)
	}

	markdown := renderMemoryBridgeBlock(criteria.Target, summaries)
	return apptypes.MemoryExportResult{
		Target:        criteria.Target,
		Scopes:        exportScopes,
		Markdown:      markdown,
		ExportedCount: len(summaries),
	}, nil
}

func memoryExportScopes(scopes []domtypes.MemoryScope, includeGlobal bool) []domtypes.MemoryScope {
	if len(scopes) == 0 {
		return nil
	}
	result := append([]domtypes.MemoryScope(nil), scopes...)
	if includeGlobal {
		result = append(result, domtypes.GlobalScopeOf())
	}
	return dedupeMemoryScopes(result)
}

func dedupeMemoryScopes(scopes []domtypes.MemoryScope) []domtypes.MemoryScope {
	seen := make(map[string]struct{}, len(scopes))
	result := make([]domtypes.MemoryScope, 0, len(scopes))
	for _, scope := range scopes {
		if scope == nil {
			result = append(result, scope)
			continue
		}
		key := scope.Kind().String() + "\x00" + scope.Key()
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		result = append(result, scope)
	}
	return result
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
// function re-sorts by scope + memory type + fact.
func renderMemoryBridgeBlock(target apptypes.MemoryBridgeTarget, summaries []apptypes.MemorySummary) string {
	sorted := append([]apptypes.MemorySummary(nil), summaries...)
	sort.SliceStable(sorted, func(i, j int) bool {
		leftScopeRank, rightScopeRank := memoryScopeRenderRank(sorted[i].Scope()), memoryScopeRenderRank(sorted[j].Scope())
		if leftScopeRank != rightScopeRank {
			return leftScopeRank < rightScopeRank
		}
		if memoryScopeLabel(sorted[i].Scope()) != memoryScopeLabel(sorted[j].Scope()) {
			return memoryScopeLabel(sorted[i].Scope()) < memoryScopeLabel(sorted[j].Scope())
		}
		if sorted[i].MemoryType() != sorted[j].MemoryType() {
			return sorted[i].MemoryType() < sorted[j].MemoryType()
		}
		return sorted[i].Fact() < sorted[j].Fact()
	})

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
	for _, scopeGroup := range groupMemoriesByScope(sorted) {
		fmt.Fprintf(&body, "## %s\n\n", titleForMemoryScope(scopeGroup.scope))
		grouped := groupMemoriesByType(scopeGroup.summaries)
		for _, memoryType := range typeOrder {
			entries, ok := grouped[memoryType]
			if !ok {
				continue
			}
			fmt.Fprintf(&body, "### %s\n\n", titleForMemoryType(memoryType))
			for _, summary := range entries {
				body.WriteString("- ")
				body.WriteString(escapeMarkdownBullet(summary.Fact()))
				fmt.Fprintf(&body, " (memory_id: %s, scope: %s)\n", summary.MemoryID().String(), memoryScopeLabel(summary.Scope()))
			}
			body.WriteString("\n")
		}
	}
	body.WriteString(MemoryBridgeMarkerEnd)
	body.WriteString("\n")
	return body.String()
}

type memoryScopeGroup struct {
	scope     domtypes.MemoryScope
	summaries []apptypes.MemorySummary
}

func groupMemoriesByScope(summaries []apptypes.MemorySummary) []memoryScopeGroup {
	groups := make([]memoryScopeGroup, 0)
	for _, summary := range summaries {
		if len(groups) == 0 || !sameMemoryScope(groups[len(groups)-1].scope, summary.Scope()) {
			groups = append(groups, memoryScopeGroup{scope: summary.Scope()})
		}
		groups[len(groups)-1].summaries = append(groups[len(groups)-1].summaries, summary)
	}
	return groups
}

func sameMemoryScope(left, right domtypes.MemoryScope) bool {
	if left == nil || right == nil {
		return left == right
	}
	return left.Kind() == right.Kind() && left.Key() == right.Key()
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

func titleForMemoryScope(scope domtypes.MemoryScope) string {
	if scope == nil {
		return "Unknown-scope memories"
	}
	switch scope.Kind() {
	case domtypes.MemoryScopeKindGlobal:
		return "Global memories"
	case domtypes.MemoryScopeKindWorkspace:
		return fmt.Sprintf("Workspace memories: %s", scope.Key())
	case domtypes.MemoryScopeKindSessionFamily:
		return fmt.Sprintf("Session-family memories: %s", scope.Key())
	case domtypes.MemoryScopeKindAgent:
		return fmt.Sprintf("Agent memories: %s", scope.Key())
	default:
		return fmt.Sprintf("%s memories: %s", scope.Kind().String(), scope.Key())
	}
}

func memoryScopeLabel(scope domtypes.MemoryScope) string {
	if scope == nil {
		return "?"
	}
	if scope.Kind() == domtypes.MemoryScopeKindGlobal {
		return "global"
	}
	return fmt.Sprintf("%s=%s", scope.Kind().String(), scope.Key())
}

func memoryScopeRenderRank(scope domtypes.MemoryScope) int {
	if scope == nil {
		return 99
	}
	switch scope.Kind() {
	case domtypes.MemoryScopeKindGlobal:
		return 0
	case domtypes.MemoryScopeKindWorkspace:
		return 1
	case domtypes.MemoryScopeKindSessionFamily:
		return 2
	case domtypes.MemoryScopeKindAgent:
		return 3
	default:
		return 98
	}
}

// escapeMarkdownBullet keeps multi-line facts readable inside a bullet by
// collapsing interior newlines to spaces. The original text remains in
// the durable memory store — this is only what lands in the exported
// instruction file.
func escapeMarkdownBullet(fact string) string {
	return strings.Join(strings.Fields(fact), " ")
}
