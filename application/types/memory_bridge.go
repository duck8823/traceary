package types

import (
	"slices"

	domtypes "github.com/duck8823/traceary/domain/types"
)

// MemoryBridgeTarget names a host-native instruction file (CLAUDE.md,
// AGENTS.md, GEMINI.md) that the bridge export and import usecases
// translate to and from. Keeping the target set small and explicit means
// the usecase can pick the right markdown template without the CLI having
// to hand-wave host-specific conventions.
type MemoryBridgeTarget string

const (
	// MemoryBridgeTargetClaude targets Claude Code's CLAUDE.md instructions.
	MemoryBridgeTargetClaude MemoryBridgeTarget = "claude"
	// MemoryBridgeTargetCodex targets Codex's AGENTS.md instructions.
	MemoryBridgeTargetCodex MemoryBridgeTarget = "codex"
	// MemoryBridgeTargetGemini targets Gemini CLI's GEMINI.md instructions.
	MemoryBridgeTargetGemini MemoryBridgeTarget = "gemini"
)

// String returns the canonical string form.
func (t MemoryBridgeTarget) String() string { return string(t) }

// MemoryExportCriteria is the input to the export usecase. An empty scope
// means the caller accepted the default (workspace scope resolved from
// the environment) and the usecase will apply the active-workspace rule.
type MemoryExportCriteria struct {
	Target MemoryBridgeTarget
	Scopes []domtypes.MemoryScope
}

// MemoryExportResult carries the serialized markdown emitted by an export
// run so the CLI / MCP layer can write it to disk or return it as JSON
// without the usecase having to reach into the filesystem itself.
type MemoryExportResult struct {
	Target   MemoryBridgeTarget
	Scopes   []domtypes.MemoryScope
	Markdown string
	// ExportedCount tracks the number of accepted memories included in
	// the Markdown output so operators can sanity-check the volume and
	// JSON consumers can drive their own "nothing to do" guards.
	ExportedCount int
}

// MemoryBridgeImportCriteria carries the inputs to the instruction-file
// import usecase. Target picks the marker format, Path / Markdown are the
// raw source (exactly one of them is consumed by the usecase so the CLI
// can stream either a file or an in-memory buffer). WorkspaceFallback is
// applied when a bullet does not carry a scope hint of its own.
type MemoryBridgeImportCriteria struct {
	Target            MemoryBridgeTarget
	Path              string
	Markdown          string
	WorkspaceFallback domtypes.Workspace
}

// MemoryBridgeImportResult reports the outcome of one import run and
// reuses MemoryImportResult's shape so downstream consumers (CLI,
// documentation, telemetry) do not have to branch between the Codex
// memory import surface and the instruction-file surface.
type MemoryBridgeImportResult struct {
	Imported              []MemoryDetails
	SkippedDuplicateCount int
	SkippedRejectedCount  int
	Warnings              []string
}

// knownMemoryBridgeTargets enumerates the accepted --target / --source
// values so the CLI can validate flags before the usecase runs.
var knownMemoryBridgeTargets = []MemoryBridgeTarget{
	MemoryBridgeTargetClaude,
	MemoryBridgeTargetCodex,
	MemoryBridgeTargetGemini,
}

// MemoryBridgeTargetOf validates and returns a MemoryBridgeTarget.
func MemoryBridgeTargetOf(value string) (MemoryBridgeTarget, bool) {
	candidate := MemoryBridgeTarget(value)
	if slices.Contains(knownMemoryBridgeTargets, candidate) {
		return candidate, true
	}
	return MemoryBridgeTarget(""), false
}
