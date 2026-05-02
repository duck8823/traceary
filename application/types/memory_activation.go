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
	Target         MemoryBridgeTarget
	TargetPath     string
	Scopes         []domtypes.MemoryScope
	Markdown       string
	Existing       bool
	Diff           string
	ActivatedCount int
}

// MemoryActivationApplyAction summarizes the observable filesystem outcome of
// a non-dry-run activation.
type MemoryActivationApplyAction string

const (
	MemoryActivationApplyCreated MemoryActivationApplyAction = "created"
	MemoryActivationApplyUpdated MemoryActivationApplyAction = "updated"
	MemoryActivationApplyNoop    MemoryActivationApplyAction = "noop"
)

// String returns the canonical string form.
func (a MemoryActivationApplyAction) String() string { return string(a) }

// MemoryActivationApplyResult reports the effect of writing accepted memories
// into a host-native activation target.
type MemoryActivationApplyResult struct {
	Target         MemoryBridgeTarget
	TargetPath     string
	Scopes         []domtypes.MemoryScope
	Action         MemoryActivationApplyAction
	Existing       bool
	ActivatedCount int
}
