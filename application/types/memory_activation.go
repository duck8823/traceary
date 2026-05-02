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
//
// For two-file targets (Claude / Gemini) the top-level fields describe the
// host context file so codex JSON consumers continue to see the same shape;
// component-level details for both files are exposed through HostContext and
// ExternalMemory so callers that understand the pair contract can render
// per-file diffs and apply paths.
type MemoryActivationPlan struct {
	Target         MemoryBridgeTarget
	TargetPath     string
	Scopes         []domtypes.MemoryScope
	Markdown       string
	Existing       bool
	Diff           string
	ActivatedCount int
	HostContext    *MemoryActivationComponent
	ExternalMemory *MemoryActivationComponent
}

// MemoryActivationComponent is the per-file plan inside a two-file
// activation pair (host context stub + external memory file). Single-file
// targets do not populate this type; two-file targets populate one
// MemoryActivationComponent per file so callers see the planned content,
// status, and diff for each file independently.
type MemoryActivationComponent struct {
	Path     string
	Existing bool
	Markdown string
	Diff     string
	Action   MemoryActivationApplyAction
	State    MemoryActivationStatusState
	Message  string
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
	// MemoryActivationStatusMissing means the target file or managed block is absent.
	MemoryActivationStatusMissing MemoryActivationStatusState = "missing"
	// MemoryActivationStatusStale means the managed block differs from current accepted memories.
	MemoryActivationStatusStale MemoryActivationStatusState = "stale"
	// MemoryActivationStatusInSync means the managed block matches current accepted memories.
	MemoryActivationStatusInSync MemoryActivationStatusState = "in_sync"
	// MemoryActivationStatusInvalid means the target cannot be safely interpreted.
	MemoryActivationStatusInvalid MemoryActivationStatusState = "invalid"
)

// String returns the canonical string form.
func (s MemoryActivationStatusState) String() string { return string(s) }

// MemoryActivationStatusResult is the read-only activation health view used by
// `memory activate --status` and doctor.
//
// For two-file targets the top-level State is the aggregated pair state per
// the v0.13 ADR (invalid > missing > stale > in_sync), and the per-file
// HostContext / ExternalMemory components carry the underlying state and
// path so doctor can give actionable remediation without reparsing files.
type MemoryActivationStatusResult struct {
	Target         MemoryBridgeTarget
	TargetPath     string
	Scopes         []domtypes.MemoryScope
	State          MemoryActivationStatusState
	Existing       bool
	ActivatedCount int
	Message        string
	HostContext    *MemoryActivationComponent
	ExternalMemory *MemoryActivationComponent
}
