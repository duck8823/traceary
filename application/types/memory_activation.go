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
	// MemoryActivationApplyCreated means the activation target file was created.
	MemoryActivationApplyCreated MemoryActivationApplyAction = "created"
	// MemoryActivationApplyUpdated means an existing activation target changed.
	MemoryActivationApplyUpdated MemoryActivationApplyAction = "updated"
	// MemoryActivationApplyNoop means the activation target was already current.
	MemoryActivationApplyNoop MemoryActivationApplyAction = "noop"
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

// MemoryActivationStatusState describes whether the host-native file reflects
// the currently accepted Traceary memories.
type MemoryActivationStatusState string

const (
	MemoryActivationStatusMissing MemoryActivationStatusState = "missing"
	MemoryActivationStatusStale   MemoryActivationStatusState = "stale"
	MemoryActivationStatusInSync  MemoryActivationStatusState = "in_sync"
	MemoryActivationStatusInvalid MemoryActivationStatusState = "invalid"
)

// String returns the canonical string form.
func (s MemoryActivationStatusState) String() string { return string(s) }

// MemoryActivationStatusResult is the read-only activation health view used by
// `memory activate --status` and doctor.
type MemoryActivationStatusResult struct {
	Target         MemoryBridgeTarget
	TargetPath     string
	Scopes         []domtypes.MemoryScope
	State          MemoryActivationStatusState
	Existing       bool
	ActivatedCount int
	Message        string
}
