package types

import (
	domtypes "github.com/duck8823/traceary/domain/types"
)

// ImportedMemoryCandidate represents a single durable-memory candidate that
// was parsed out of a host-native source (for example a Codex MEMORY.md
// file). It is the stable value object the import usecase consumes — the
// source adapter has already resolved a scope and attached provenance refs,
// and the usecase only has to sanitize, dedupe, and persist the candidate.
type ImportedMemoryCandidate struct {
	MemoryType   domtypes.MemoryType
	Scope        domtypes.MemoryScope
	Fact         string
	EvidenceRefs []domtypes.EvidenceRef
	ArtifactRefs []domtypes.ArtifactRef
	// SourcePath is the absolute path of the source file the fact was parsed
	// from. It is used for dedupe — re-runs that point at the same file must
	// not double-insert candidates even when the bullet line number changed.
	SourcePath string
}

// CodexImportCriteria carries the inputs for a Codex MEMORY.md import run.
// WorkspaceFallback is applied only when the source file does not declare a
// more specific `applies_to: cwd=...` hint, so an explicit --workspace flag
// never overrides host-supplied provenance.
type CodexImportCriteria struct {
	Root              string
	WorkspaceFallback domtypes.Workspace
}
