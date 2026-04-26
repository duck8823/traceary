package mcpserver

// manageMemoryInput is the MCP input for the manage_memory action dispatcher.
type manageMemoryInput struct {
	Action        string           `json:"action" jsonschema:"required,action enum: propose, remember, accept, reject, expire, supersede, set_validity, import_instructions"`
	IDs           any              `json:"ids,omitempty" jsonschema:"single memory id or array of memory ids for accept/reject batch actions"`
	MemoryID      string           `json:"memory_id,omitempty" jsonschema:"durable memory identifier for single-id actions"`
	TargetID      string           `json:"target_id,omitempty" jsonschema:"accepted durable memory identifier to supersede"`
	MemoryType    string           `json:"type,omitempty" jsonschema:"memory type"`
	Workspace     string           `json:"workspace,omitempty" jsonschema:"workspace scope"`
	Agent         string           `json:"agent,omitempty" jsonschema:"agent scope"`
	SessionFamily string           `json:"session_family,omitempty" jsonschema:"session-family scope"`
	Fact          string           `json:"fact,omitempty" jsonschema:"distilled memory fact"`
	Confidence    string           `json:"confidence,omitempty" jsonschema:"accepted confidence (default: verified)"`
	Source        string           `json:"source,omitempty" jsonschema:"memory/import source"`
	EvidenceRefs  []memoryRefInput `json:"evidence_refs,omitempty" jsonschema:"supporting evidence refs"`
	ArtifactRefs  []memoryRefInput `json:"artifact_refs,omitempty" jsonschema:"related artifact refs"`
	ExpiresAt     string           `json:"expires_at,omitempty" jsonschema:"expiry timestamp (YYYY-MM-DD or RFC3339, defaults to now)"`
	ValidFrom     string           `json:"valid_from,omitempty" jsonschema:"start of validity window (YYYY-MM-DD or RFC3339)"`
	ValidTo       string           `json:"valid_to,omitempty" jsonschema:"end of validity window (YYYY-MM-DD or RFC3339)"`
	ClearValidTo  bool             `json:"clear_valid_to,omitempty" jsonschema:"remove the existing valid_to; incompatible with valid_to"`
	Path          string           `json:"path,omitempty" jsonschema:"absolute path to the instruction file"`
	Markdown      string           `json:"markdown,omitempty" jsonschema:"raw markdown content to parse when path is not provided"`
}

// queryMemoryInput is the MCP input for the query_memory action dispatcher.
type queryMemoryInput struct {
	Action              string   `json:"action" jsonschema:"required,action enum: retrieve, export, pack, scan_hygiene"`
	MemoryID            string   `json:"memory_id,omitempty" jsonschema:"durable memory identifier to fetch directly"`
	Query               string   `json:"query,omitempty" jsonschema:"full-text search query"`
	Workspace           string   `json:"workspace,omitempty" jsonschema:"workspace scope filter"`
	Agent               string   `json:"agent,omitempty" jsonschema:"agent scope filter"`
	SessionFamily       string   `json:"session_family,omitempty" jsonschema:"session-family scope filter"`
	Statuses            []string `json:"status,omitempty" jsonschema:"memory lifecycle status filters"`
	MemoryTypes         []string `json:"type,omitempty" jsonschema:"memory type filters"`
	Limit               int      `json:"limit,omitempty" jsonschema:"maximum number of memories to return (default: 20)"`
	Offset              int      `json:"offset,omitempty" jsonschema:"number of memories to skip before returning results (default: 0)"`
	AsOf                string   `json:"as_of,omitempty" jsonschema:"evaluate content validity at this timestamp"`
	IncludeExpired      bool     `json:"include_expired,omitempty" jsonschema:"include memories whose valid_to is in the past"`
	Preset              string   `json:"preset,omitempty" jsonschema:"built-in retrieval preset: resume | review | incident"`
	SessionID           string   `json:"session_id,omitempty" jsonschema:"session identifier filter"`
	RecentCommandsLimit *int     `json:"recent_commands_limit,omitempty" jsonschema:"maximum recent commands to include"`
	MemoryLimit         *int     `json:"memory_limit,omitempty" jsonschema:"maximum durable memories to include"`
	ExpiryDays          int      `json:"expiry_days,omitempty" jsonschema:"staleness threshold in days (default 90)"`
	Target              string   `json:"target,omitempty" jsonschema:"export target host (claude / codex / gemini)"`
}

// sessionActionInput is shared by manage_session and session_status.
type sessionActionInput struct {
	Action              string `json:"action" jsonschema:"required,action enum: start, end, active, latest, handoff, lineage, tree"`
	Client              string `json:"client,omitempty" jsonschema:"recording channel or filter"`
	Agent               string `json:"agent,omitempty" jsonschema:"actor name or filter"`
	SessionID           string `json:"session_id,omitempty" jsonschema:"session identifier"`
	Workspace           string `json:"workspace,omitempty" jsonschema:"auxiliary work context identifier"`
	ParentSessionID     string `json:"parent_session_id,omitempty" jsonschema:"parent session identifier for sub-agent sessions"`
	InferParentSession  *bool  `json:"infer_parent_session,omitempty" jsonschema:"infer parent from the active session when parent_session_id is omitted (default: true for MCP starts)"`
	AllowStale          bool   `json:"allow_stale,omitempty" jsonschema:"allow stale active sessions"`
	StaleAfterSeconds   int    `json:"stale_after_seconds,omitempty" jsonschema:"mark active sessions older than this many seconds as stale"`
	RecentCommandsLimit *int   `json:"recent_commands_limit,omitempty" jsonschema:"maximum recent commands to include"`
	MemoryLimit         *int   `json:"memory_limit,omitempty" jsonschema:"maximum durable memories to include"`
	Preset              string `json:"preset,omitempty" jsonschema:"built-in retrieval preset applied to durable memories"`
	AsOf                string `json:"as_of,omitempty" jsonschema:"evaluate durable memory validity at this timestamp"`
	Depth               *int   `json:"depth,omitempty" jsonschema:"maximum descendant depth for action=tree (0 returns only the root)"`
}

// recordEventInput is the MCP input for the record_event tool.
type recordEventInput struct {
	Type      string `json:"type" jsonschema:"required,event write type: log or audit"`
	Message   string `json:"message,omitempty" jsonschema:"log body to record"`
	Kind      string `json:"kind,omitempty" jsonschema:"event kind for log records"`
	Command   string `json:"command,omitempty" jsonschema:"executed command for audit records"`
	Input     string `json:"input,omitempty" jsonschema:"command input"`
	Output    string `json:"output,omitempty" jsonschema:"command output"`
	Client    string `json:"client,omitempty" jsonschema:"recording channel (default: mcp)"`
	Agent     string `json:"agent,omitempty" jsonschema:"actor name (default: manual)"`
	SessionID string `json:"session_id,omitempty" jsonschema:"session ID (default: default)"`
	Workspace string `json:"workspace,omitempty" jsonschema:"auxiliary work context identifier"`
}

// startSessionInput is the MCP input for the start_session tool.
type startSessionInput struct {
	Client             string `json:"client,omitempty" jsonschema:"recording channel (default: mcp)"`
	Agent              string `json:"agent,omitempty" jsonschema:"actor name (default: manual)"`
	SessionID          string `json:"session_id,omitempty" jsonschema:"session identifier (auto-generates when omitted)"`
	Workspace          string `json:"workspace,omitempty" jsonschema:"auxiliary work context identifier"`
	ParentSessionID    string `json:"parent_session_id,omitempty" jsonschema:"parent session identifier for sub-agent sessions"`
	InferParentSession *bool  `json:"infer_parent_session,omitempty" jsonschema:"infer parent from the active session when parent_session_id is omitted (default: true)"`
}

// endSessionInput is the MCP input for the end_session tool.
type endSessionInput struct {
	Client    string `json:"client,omitempty" jsonschema:"recording channel (falls back to the start event attribution)"`
	Agent     string `json:"agent,omitempty" jsonschema:"actor name (falls back to the start event attribution)"`
	SessionID string `json:"session_id" jsonschema:"required,session identifier to end"`
	Workspace string `json:"workspace,omitempty" jsonschema:"auxiliary work context identifier"`
}

// sessionLookupInput is the MCP input for active_session and latest_session tools.
type sessionLookupInput struct {
	Client            string `json:"client,omitempty" jsonschema:"filter by recording channel"`
	Agent             string `json:"agent,omitempty" jsonschema:"filter by actor"`
	Workspace         string `json:"workspace,omitempty" jsonschema:"filter by auxiliary work context identifier"`
	AllowStale        bool   `json:"allow_stale,omitempty" jsonschema:"allow stale active sessions"`
	StaleAfterSeconds int    `json:"stale_after_seconds,omitempty" jsonschema:"mark active sessions older than this many seconds as stale (0 or omitted: 86400)"`
}

// listEventsInput is the MCP input for the list_events tool.
type listEventsInput struct {
	Limit      int    `json:"limit,omitempty" jsonschema:"result limit (default: 20)"`
	Offset     int    `json:"offset,omitempty" jsonschema:"offset from the newest result (default: 0)"`
	Kind       string `json:"kind,omitempty" jsonschema:"filter by event kind (note, command_executed, reviewed, session_started, session_ended, compact_summary, prompt; alias: audit)"`
	Client     string `json:"client,omitempty" jsonschema:"filter by client"`
	Agent      string `json:"agent,omitempty" jsonschema:"filter by agent"`
	SessionID  string `json:"session_id,omitempty" jsonschema:"filter by session ID"`
	Workspace  string `json:"workspace,omitempty" jsonschema:"filter by work context"`
	From       string `json:"from,omitempty" jsonschema:"start time (YYYY-MM-DD or RFC3339)"`
	To         string `json:"to,omitempty" jsonschema:"end time (YYYY-MM-DD or RFC3339)"`
	SourceHook string `json:"source_hook,omitempty" jsonschema:"filter by hook identifier that produced the event (stop, subagent_stop, pre_compact, post_compact, session_start, session_end, user_prompt_submit, post_tool_use, after_agent, after_tool)"`
}

// searchInput is the MCP input for the search tool.
type searchInput struct {
	Query     string `json:"query" jsonschema:"search query"`
	Workspace string `json:"workspace,omitempty" jsonschema:"work context filter"`
	From      string `json:"from,omitempty" jsonschema:"start time (YYYY-MM-DD or RFC3339)"`
	To        string `json:"to,omitempty" jsonschema:"end time (YYYY-MM-DD or RFC3339)"`
	Limit     int    `json:"limit,omitempty" jsonschema:"result limit (default: 20)"`
}

// getContextInput is the MCP input for the get_context tool.
type getContextInput struct {
	Workspace string `json:"workspace,omitempty" jsonschema:"work context filter"`
	SessionID string `json:"session_id,omitempty" jsonschema:"session identifier filter"`
	Limit     int    `json:"limit,omitempty" jsonschema:"result limit (default: 20)"`
}

// sessionHandoffInput is the MCP input for the session_handoff tool.
type sessionHandoffInput struct {
	SessionID           string `json:"session_id,omitempty" jsonschema:"session identifier filter"`
	Workspace           string `json:"workspace,omitempty" jsonschema:"work context filter"`
	RecentCommandsLimit *int   `json:"recent_commands_limit,omitempty" jsonschema:"maximum recent commands to include (default: 5; explicit 0 disables recent commands)"`
	MemoryLimit         *int   `json:"memory_limit,omitempty" jsonschema:"maximum durable memories to include (default: 5; explicit 0 disables durable memories)"`
	Preset              string `json:"preset,omitempty" jsonschema:"built-in retrieval preset applied to durable memories: resume | review | incident"`
	AsOf                string `json:"as_of,omitempty" jsonschema:"evaluate durable memory validity at this timestamp (YYYY-MM-DD or RFC3339); defaults to now"`
}

// memoryPackInput is the MCP input for the memory_pack tool.
type memoryPackInput struct {
	SessionID           string `json:"session_id,omitempty" jsonschema:"session identifier filter"`
	Workspace           string `json:"workspace,omitempty" jsonschema:"work context filter"`
	RecentCommandsLimit *int   `json:"recent_commands_limit,omitempty" jsonschema:"maximum recent commands to include (default: 5; explicit 0 disables recent commands)"`
	MemoryLimit         *int   `json:"memory_limit,omitempty" jsonschema:"maximum durable memories to include (default: 5; explicit 0 disables durable memories)"`
	Preset              string `json:"preset,omitempty" jsonschema:"built-in retrieval preset applied to durable memories: resume | review | incident"`
	AsOf                string `json:"as_of,omitempty" jsonschema:"evaluate durable memory validity at this timestamp (YYYY-MM-DD or RFC3339); defaults to now"`
}

// memoryRefInput is the MCP representation of evidence/artifact references.
type memoryRefInput struct {
	Kind  string `json:"kind" jsonschema:"reference kind enum: event, session, url, file, issue, pr"`
	Value string `json:"value" jsonschema:"reference value (event/session id, URL, file path, issue or pr number)"`
}

// retrieveMemoriesInput is the MCP input for the retrieve_memories tool.
type retrieveMemoriesInput struct {
	MemoryID       string   `json:"memory_id,omitempty" jsonschema:"durable memory identifier to fetch directly"`
	Query          string   `json:"query,omitempty" jsonschema:"full-text search query"`
	Workspace      string   `json:"workspace,omitempty" jsonschema:"workspace scope filter"`
	Agent          string   `json:"agent,omitempty" jsonschema:"agent scope filter"`
	SessionFamily  string   `json:"session_family,omitempty" jsonschema:"session-family scope filter"`
	Statuses       []string `json:"status,omitempty" jsonschema:"memory lifecycle status filters"`
	MemoryTypes    []string `json:"type,omitempty" jsonschema:"memory type filters"`
	Limit          int      `json:"limit,omitempty" jsonschema:"maximum number of memories to return (default: 20)"`
	Offset         int      `json:"offset,omitempty" jsonschema:"number of memories to skip before returning results (default: 0)"`
	AsOf           string   `json:"as_of,omitempty" jsonschema:"evaluate content validity at this timestamp (YYYY-MM-DD or RFC3339); defaults to now"`
	IncludeExpired bool     `json:"include_expired,omitempty" jsonschema:"include memories whose valid_to is in the past (bypasses the default validity-window filter)"`
	Preset         string   `json:"preset,omitempty" jsonschema:"built-in retrieval preset: resume | review | incident. Explicit status / type filters still override the preset defaults"`
}

// rememberMemoryInput is the MCP input for the remember_memory tool.
type rememberMemoryInput struct {
	MemoryType    string           `json:"type" jsonschema:"memory type"`
	Workspace     string           `json:"workspace,omitempty" jsonschema:"workspace scope"`
	Agent         string           `json:"agent,omitempty" jsonschema:"agent scope"`
	SessionFamily string           `json:"session_family,omitempty" jsonschema:"session-family scope"`
	Fact          string           `json:"fact" jsonschema:"distilled memory fact"`
	Confidence    string           `json:"confidence,omitempty" jsonschema:"accepted confidence (default: verified)"`
	Source        string           `json:"source,omitempty" jsonschema:"memory source (default: manual)"`
	EvidenceRefs  []memoryRefInput `json:"evidence_refs,omitempty" jsonschema:"supporting evidence refs"`
	ArtifactRefs  []memoryRefInput `json:"artifact_refs,omitempty" jsonschema:"related artifact refs"`
}

// proposeMemoryInput is the MCP input for the propose_memory tool.
type proposeMemoryInput struct {
	MemoryType    string           `json:"type" jsonschema:"memory type"`
	Workspace     string           `json:"workspace,omitempty" jsonschema:"workspace scope"`
	Agent         string           `json:"agent,omitempty" jsonschema:"agent scope"`
	SessionFamily string           `json:"session_family,omitempty" jsonschema:"session-family scope"`
	Fact          string           `json:"fact" jsonschema:"distilled memory fact"`
	Source        string           `json:"source,omitempty" jsonschema:"memory source (default: manual)"`
	EvidenceRefs  []memoryRefInput `json:"evidence_refs,omitempty" jsonschema:"supporting evidence refs"`
	ArtifactRefs  []memoryRefInput `json:"artifact_refs,omitempty" jsonschema:"related artifact refs"`
}

// acceptMemoryInput is the MCP input for the accept_memory tool.
type acceptMemoryInput struct {
	MemoryID   string `json:"memory_id" jsonschema:"candidate durable memory identifier"`
	Confidence string `json:"confidence,omitempty" jsonschema:"accepted confidence (default: verified)"`
}

// rejectMemoryInput is the MCP input for the reject_memory tool.
type rejectMemoryInput struct {
	MemoryID string `json:"memory_id" jsonschema:"candidate durable memory identifier"`
}

// supersedeMemoryInput is the MCP input for the supersede_memory tool.
type supersedeMemoryInput struct {
	MemoryID      string           `json:"memory_id" jsonschema:"accepted durable memory identifier to supersede"`
	MemoryType    string           `json:"type,omitempty" jsonschema:"replacement memory type (inherits when omitted)"`
	Workspace     string           `json:"workspace,omitempty" jsonschema:"replacement workspace scope"`
	Agent         string           `json:"agent,omitempty" jsonschema:"replacement agent scope"`
	SessionFamily string           `json:"session_family,omitempty" jsonschema:"replacement session-family scope"`
	Fact          string           `json:"fact" jsonschema:"replacement distilled memory fact"`
	Confidence    string           `json:"confidence,omitempty" jsonschema:"replacement confidence (default: verified)"`
	Source        string           `json:"source,omitempty" jsonschema:"replacement memory source (default: manual)"`
	EvidenceRefs  []memoryRefInput `json:"evidence_refs,omitempty" jsonschema:"replacement evidence refs"`
	ArtifactRefs  []memoryRefInput `json:"artifact_refs,omitempty" jsonschema:"replacement artifact refs"`
	ValidFrom     string           `json:"valid_from,omitempty" jsonschema:"replacement validFrom (YYYY-MM-DD or RFC3339); defaults to now when omitted"`
	ValidTo       string           `json:"valid_to,omitempty" jsonschema:"replacement validTo (YYYY-MM-DD or RFC3339); defaults to open-ended when omitted"`
}

// expireMemoryInput is the MCP input for the expire_memory tool.
type expireMemoryInput struct {
	MemoryID  string `json:"memory_id" jsonschema:"durable memory identifier"`
	ExpiresAt string `json:"expires_at,omitempty" jsonschema:"expiry timestamp (YYYY-MM-DD or RFC3339, defaults to now)"`
}

// setMemoryValidityInput is the MCP input for the set_memory_validity tool.
// Mirrors `traceary memory set-validity`: valid_from / valid_to set the
// content-validity window (separate from expires_at, which is the
// lifecycle operation timestamp). clear_valid_to removes the current
// validTo when no valid_to value is supplied, returning the memory to
// open-ended validity.
type setMemoryValidityInput struct {
	MemoryID     string `json:"memory_id" jsonschema:"durable memory identifier"`
	ValidFrom    string `json:"valid_from,omitempty" jsonschema:"start of validity window (YYYY-MM-DD or RFC3339); omit to leave unchanged"`
	ValidTo      string `json:"valid_to,omitempty" jsonschema:"end of validity window (YYYY-MM-DD or RFC3339); omit to leave unchanged"`
	ClearValidTo bool   `json:"clear_valid_to,omitempty" jsonschema:"remove the existing valid_to (return to open-ended validity); incompatible with valid_to"`
}

// acceptMemoriesBatchInput is the MCP input for the accept_memories_batch
// tool. Agent hosts call this to drive the same accept-every-candidate
// flow the CLI exposes under `traceary memory inbox accept --ids`, so the
// schema keeps the id list shape minimal and leaves confidence optional.
type acceptMemoriesBatchInput struct {
	MemoryIDs  []string `json:"memory_ids" jsonschema:"candidate durable memory identifiers to accept"`
	Confidence string   `json:"confidence,omitempty" jsonschema:"accepted confidence (default: verified)"`
}

// rejectMemoriesBatchInput is the MCP input for the reject_memories_batch
// tool. The schema intentionally omits confidence because rejection has no
// notion of confidence in the lifecycle model.
type rejectMemoriesBatchInput struct {
	MemoryIDs []string `json:"memory_ids" jsonschema:"candidate durable memory identifiers to reject"`
}

// scanMemoryHygieneInput is the MCP input for the scan_memory_hygiene
// tool. The tool is read-only; agents use it to inspect redaction /
// expiry / duplicate suggestions before deciding whether to drive
// supersede / expire / reject via the single-memory tools.
type scanMemoryHygieneInput struct {
	Workspace  string `json:"workspace,omitempty" jsonschema:"workspace scope to scan (omitted scans every scope)"`
	ExpiryDays int    `json:"expiry_days,omitempty" jsonschema:"staleness threshold in days (default 90)"`
}

// exportMemoriesInput is the MCP input for the export_memories tool that
// serialises accepted durable memories into the markdown block Traceary
// writes into CLAUDE.md / AGENTS.md / GEMINI.md. The MCP surface is
// filesystem-free — the generated markdown comes back in the response so
// the caller decides what to do with it.
type exportMemoriesInput struct {
	Target    string `json:"target" jsonschema:"export target host (claude / codex / gemini)"`
	Workspace string `json:"workspace,omitempty" jsonschema:"workspace scope to export (omitted exports every accepted memory)"`
}

// importMemoryInstructionsInput is the MCP input for the
// import_memory_instructions tool. Exactly one of path / markdown must
// be supplied so the caller can hand Traceary either a file on disk or
// an inline buffer (for example when the agent already has the content
// in memory).
type importMemoryInstructionsInput struct {
	Source    string `json:"source" jsonschema:"source host that produced the instruction file (claude / codex / gemini)"`
	Path      string `json:"path,omitempty" jsonschema:"absolute path to the instruction file"`
	Markdown  string `json:"markdown,omitempty" jsonschema:"raw markdown content to parse when path is not provided"`
	Workspace string `json:"workspace,omitempty" jsonschema:"workspace scope assigned to imported candidates"`
}
