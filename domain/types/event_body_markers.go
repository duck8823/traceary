package types

// Event body markers used to discriminate lifecycle phases that share
// a single EventKind. The markers live in the domain types package so
// both the CLI-side writers (presentation/cli/hook_runtime.go) and
// the application-side readers (application/usecase/context_pack_builder.go,
// replay path, etc.) import from one source of truth — changing the
// marker string only rewrites a single definition instead of three.
//
// The markers are stored as body prefixes to keep the EventKind
// taxonomy small; a future refactor that promotes these phases to
// first-class kinds will need a migration because historical rows
// already carry the prefix verbatim.
const (
	// EventBodyMarkerCompactPreSnapshot prefixes compact_summary
	// events that Traceary records before Claude Code's PreCompact
	// hook actually compacts the conversation. Readers such as
	// `session_handoff` and `memory_pack` skip rows whose body starts
	// with this marker so a cancelled compact cycle cannot hide the
	// post-compact digest.
	EventBodyMarkerCompactPreSnapshot = "[phase:pre-compact]"

	// EventBodyMarkerSubagentStop prefixes session_ended events that
	// Traceary records on Claude Code's SubagentStop hook (a
	// Task-tool subagent completion). The marker lets consumers
	// filter the main session's session_ended stream away from the
	// subagent boundary events when that distinction matters.
	EventBodyMarkerSubagentStop = "[phase:subagent]"
)
