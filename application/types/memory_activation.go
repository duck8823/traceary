package types

import domtypes "github.com/duck8823/traceary/domain/types"

// MemoryActivationCriteria describes a dry-run host activation plan. The
// planner resolves a target file and renders the content, but it never writes
// to disk; later write-side usecases consume the same target contract.
type MemoryActivationCriteria struct {
	Target        MemoryBridgeTarget
	Root          string
	Path          string
	Scopes        []domtypes.MemoryScope
	IncludeGlobal bool
	Diff          bool
}

// MemoryActivationPlan is the resolved dry-run output for a host-native
// activation. Markdown is the content that would be written to TargetPath.
type MemoryActivationPlan struct {
	Target     MemoryBridgeTarget
	TargetPath string
	Scopes     []domtypes.MemoryScope
	Markdown   string
	Existing   bool
	Diff       string
}
