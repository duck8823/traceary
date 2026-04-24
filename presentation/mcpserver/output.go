package mcpserver

// addLogOutput is the MCP output for the add_log tool.
type addLogOutput struct {
	EventID    string `json:"event_id" jsonschema:"saved event ID"`
	Kind       string `json:"kind" jsonschema:"event kind"`
	Client     string `json:"client" jsonschema:"recording channel"`
	Agent      string `json:"agent" jsonschema:"actor"`
	SessionID  string `json:"session_id" jsonschema:"session identifier"`
	Workspace  string `json:"workspace,omitempty" jsonschema:"auxiliary work context identifier"`
	Body       string `json:"body" jsonschema:"event body"`
	SourceHook string `json:"source_hook,omitempty" jsonschema:"hook identifier that produced this event (omitted for non-hook writes)"`
	CreatedAt  string `json:"created_at" jsonschema:"event timestamp (RFC3339Nano)"`
}

// sessionEventOutput is the MCP output for session tools (start/end/active/latest).
type sessionEventOutput struct {
	EventID    string `json:"event_id" jsonschema:"saved or referenced event ID"`
	Kind       string `json:"kind" jsonschema:"event kind"`
	Client     string `json:"client" jsonschema:"recording channel"`
	Agent      string `json:"agent" jsonschema:"actor"`
	SessionID  string `json:"session_id" jsonschema:"session identifier"`
	Workspace  string `json:"workspace,omitempty" jsonschema:"auxiliary work context identifier"`
	SourceHook string `json:"source_hook,omitempty" jsonschema:"hook identifier that produced this event (omitted for non-hook writes)"`
	CreatedAt  string `json:"created_at" jsonschema:"event timestamp (RFC3339Nano)"`
}

// addAuditOutput is the MCP output for the add_audit tool.
type addAuditOutput struct {
	EventID         string `json:"event_id" jsonschema:"saved event ID"`
	Kind            string `json:"kind" jsonschema:"event kind"`
	SessionID       string `json:"session_id" jsonschema:"session identifier"`
	Workspace       string `json:"workspace,omitempty" jsonschema:"auxiliary work context identifier"`
	Command         string `json:"command" jsonschema:"executed command"`
	InputRedacted   bool   `json:"input_redacted" jsonschema:"whether input was redacted"`
	OutputRedacted  bool   `json:"output_redacted" jsonschema:"whether output was redacted"`
	InputTruncated  bool   `json:"input_truncated" jsonschema:"whether input was truncated"`
	OutputTruncated bool   `json:"output_truncated" jsonschema:"whether output was truncated"`
	CreatedAt       string `json:"created_at" jsonschema:"event timestamp (RFC3339Nano)"`
}

// eventsOutput is the MCP output for event listing tools (list_events, search, get_context).
type eventsOutput struct {
	Events []eventOutput `json:"events" jsonschema:"events matching the filters"`
}

// eventOutput is an individual event in an eventsOutput list.
type eventOutput struct {
	EventID    string `json:"event_id" jsonschema:"event ID"`
	Kind       string `json:"kind" jsonschema:"event kind"`
	Client     string `json:"client" jsonschema:"recording channel"`
	Agent      string `json:"agent" jsonschema:"actor"`
	SessionID  string `json:"session_id" jsonschema:"session identifier"`
	Workspace  string `json:"workspace,omitempty" jsonschema:"auxiliary work context identifier"`
	Body       string `json:"body" jsonschema:"event body"`
	SourceHook string `json:"source_hook,omitempty" jsonschema:"hook identifier that produced this event (omitted for non-hook writes)"`
	CreatedAt  string `json:"created_at" jsonschema:"event timestamp (RFC3339Nano)"`
}

// sessionHandoffOutput is the MCP output for the session_handoff tool.
type sessionHandoffOutput struct {
	SessionID      string                `json:"session_id,omitempty"`
	Workspace      string                `json:"workspace,omitempty"`
	Label          string                `json:"label,omitempty"`
	Status         string                `json:"status,omitempty"`
	TotalEvents    int                   `json:"total_events"`
	CommandCount   int                   `json:"command_count"`
	Agents         []string              `json:"agents,omitempty"`
	Summary        string                `json:"summary,omitempty" jsonschema:"legacy compatibility mirror of working_state.combined_summary"`
	WorkingState   workingStateOutput    `json:"working_state"`
	RecentCommands []string              `json:"recent_commands,omitempty"`
	Memories       []memorySummaryOutput `json:"memories,omitempty"`
}

// memoryPackOutput is the MCP output for the memory_pack tool.
type memoryPackOutput = sessionHandoffOutput

// workingStateOutput represents structured working-memory signals.
type workingStateOutput struct {
	SessionSummary  string `json:"session_summary,omitempty"`
	CompactSummary  string `json:"compact_summary,omitempty"`
	CombinedSummary string `json:"combined_summary,omitempty"`
}

// memoriesOutput is the MCP output for retrieve_memories.
type memoriesOutput struct {
	Memories []memoryOutput `json:"memories" jsonschema:"durable memories matching the request"`
}

// memorySummaryOutput is the compact durable-memory shape used inside context packs.
//
// Optional timestamps are rendered as `*string` + `omitempty`: an absent
// bound produces JSON `null` / an omitted key rather than an empty
// string. This matches the CLI shape (presentation/cli/output.go
// memorySummaryOutput) so consumers of memory_pack and `memory list
// --json` can share the same TypeScript / JSON Schema shape.
type memorySummaryOutput struct {
	MemoryID   string  `json:"memory_id" jsonschema:"durable memory identifier"`
	Type       string  `json:"type" jsonschema:"memory type"`
	ScopeKind  string  `json:"scope_kind" jsonschema:"scope kind"`
	ScopeValue string  `json:"scope_value" jsonschema:"scope value"`
	Fact       string  `json:"fact" jsonschema:"distilled memory fact"`
	Status     string  `json:"status" jsonschema:"lifecycle status"`
	Confidence string  `json:"confidence" jsonschema:"confidence level"`
	Source     string  `json:"source" jsonschema:"memory source"`
	Supersedes *string `json:"supersedes,omitempty" jsonschema:"superseded memory identifier; null or omitted when this memory does not supersede another"`
	ExpiresAt  *string `json:"expires_at,omitempty" jsonschema:"expiry timestamp (RFC3339Nano); null or omitted when unset"`
	ValidFrom  string  `json:"valid_from" jsonschema:"start of content validity window (RFC3339Nano)"`
	ValidTo    *string `json:"valid_to,omitempty" jsonschema:"end of content validity window (RFC3339Nano); null or omitted means open-ended"`
	CreatedAt  string  `json:"created_at" jsonschema:"creation timestamp (RFC3339Nano)"`
	UpdatedAt  string  `json:"updated_at" jsonschema:"update timestamp (RFC3339Nano)"`
}

// memoryOutput is the full durable-memory shape returned by MCP memory
// tools. Shape matches presentation/cli/output.go memorySummaryOutput
// so a consumer can share a single JSON / TypeScript definition for
// both `memory list --json` and the MCP retrieve_memories tool — see
// #628.
type memoryOutput struct {
	MemoryID     string            `json:"memory_id" jsonschema:"durable memory identifier"`
	Type         string            `json:"type" jsonschema:"memory type"`
	ScopeKind    string            `json:"scope_kind" jsonschema:"scope kind"`
	ScopeValue   string            `json:"scope_value" jsonschema:"scope value"`
	Fact         string            `json:"fact" jsonschema:"distilled memory fact"`
	Status       string            `json:"status" jsonschema:"lifecycle status"`
	Confidence   string            `json:"confidence" jsonschema:"confidence level"`
	Source       string            `json:"source" jsonschema:"memory source"`
	Supersedes   *string           `json:"supersedes,omitempty" jsonschema:"superseded memory identifier; null or omitted when this memory does not supersede another"`
	ExpiresAt    *string           `json:"expires_at,omitempty" jsonschema:"expiry timestamp (RFC3339Nano); null or omitted when unset"`
	ValidFrom    string            `json:"valid_from" jsonschema:"start of content validity window (RFC3339Nano)"`
	ValidTo      *string           `json:"valid_to,omitempty" jsonschema:"end of content validity window (RFC3339Nano); null or omitted means open-ended"`
	CreatedAt    string            `json:"created_at" jsonschema:"creation timestamp (RFC3339Nano)"`
	UpdatedAt    string            `json:"updated_at" jsonschema:"update timestamp (RFC3339Nano)"`
	EvidenceRefs []memoryRefOutput `json:"evidence_refs,omitempty" jsonschema:"supporting evidence refs"`
	ArtifactRefs []memoryRefOutput `json:"artifact_refs,omitempty" jsonschema:"related artifact refs"`
}

// memoryRefOutput is a reference rendered in MCP responses.
type memoryRefOutput struct {
	Kind  string `json:"kind" jsonschema:"reference kind"`
	Value string `json:"value" jsonschema:"reference value"`
}

// inboxBatchMemoryOutput mirrors the CLI `memory inbox` batch summary so
// agent hosts get the same action / success / failure breakdown from MCP
// as an operator sees on stdout. Failures are keyed by the raw id the
// caller supplied so retries can target only the memories that did not
// transition.
type inboxBatchMemoryOutput struct {
	Action    string                          `json:"action" jsonschema:"batch action that was applied (accept or reject)"`
	Processed []memoryOutput                  `json:"processed" jsonschema:"memories that transitioned successfully"`
	Failures  []inboxBatchMemoryFailureOutput `json:"failures,omitempty" jsonschema:"memories that failed to transition"`
}

type inboxBatchMemoryFailureOutput struct {
	MemoryID string `json:"memory_id" jsonschema:"requested memory identifier"`
	Error    string `json:"error" jsonschema:"reason the memory did not transition"`
}

// exportMemoriesOutput mirrors the CLI summary of a memory export run so
// MCP consumers receive the generated markdown plus the same counters
// operators see on stdout.
type exportMemoriesOutput struct {
	Target        string `json:"target" jsonschema:"target host that was exported"`
	ExportedCount int    `json:"exported_count" jsonschema:"number of accepted memories included in the markdown block"`
	Markdown      string `json:"markdown" jsonschema:"generated markdown block wrapped in Traceary marker comments"`
}

// importMemoryInstructionsOutput mirrors the CLI `memory import
// instructions` shape so every import surface reports the same
// imported / skipped / warnings breakdown.
type importMemoryInstructionsOutput struct {
	Imported              []memoryOutput `json:"imported" jsonschema:"memories that were proposed as new candidates"`
	SkippedDuplicateCount int            `json:"skipped_duplicate_count" jsonschema:"bullets that matched an existing candidate / accepted memory"`
	SkippedRejectedCount  int            `json:"skipped_rejected_count" jsonschema:"bullets that matched a memory the operator already rejected"`
	Warnings              []string       `json:"warnings,omitempty" jsonschema:"non-fatal parser or sanitizer notes"`
}

// memoryHygieneOutput mirrors the CLI `memory hygiene scan` shape so
// agent hosts receive the same five-suggestion view an operator sees
// on stdout.
type memoryHygieneOutput struct {
	RedactionHitCount             int                             `json:"redaction_hit_count" jsonschema:"number of accepted memories flagged by the current redaction rules"`
	ExpiryCandidateCount          int                             `json:"expiry_candidate_count" jsonschema:"number of accepted memories older than the staleness threshold"`
	DuplicateCount                int                             `json:"duplicate_count" jsonschema:"number of accepted memories that share scope + fact with another row"`
	SupersedeCandidateCount       int                             `json:"supersede_candidate_count" jsonschema:"number of accepted memories paired by word-Jaccard similarity"`
	ValidityOverlapSupersedeCount int                             `json:"validity_overlap_supersede_count" jsonschema:"number of accepted memories paired by (scope, type) with overlapping validity windows"`
	Suggestions                   []memoryHygieneSuggestionOutput `json:"suggestions" jsonschema:"per-memory hygiene suggestions"`
}

type memoryHygieneSuggestionOutput struct {
	MemoryID            string  `json:"memory_id" jsonschema:"memory identifier"`
	Kind                string  `json:"kind" jsonschema:"suggestion kind (redaction_hit / expiry_candidate / duplicate / supersede_candidate / validity_overlap_supersede)"`
	Reason              string  `json:"reason" jsonschema:"human-readable reason the scanner flagged this memory"`
	Fact                string  `json:"fact" jsonschema:"stored fact at scan time"`
	SanitizedFact       string  `json:"sanitized_fact,omitempty" jsonschema:"masked fact the apply path would write via supersede"`
	DuplicateMemoryID   string  `json:"duplicate_memory_id,omitempty" jsonschema:"paired duplicate when kind=duplicate"`
	ReplacementMemoryID string  `json:"replacement_memory_id,omitempty" jsonschema:"newer memory whose fact is suggested as the supersede replacement"`
	ReplacementFact     string  `json:"replacement_fact,omitempty" jsonschema:"fact the apply path would write via supersede"`
	Similarity          float64 `json:"similarity,omitempty" jsonschema:"word-Jaccard similarity score (supersede_candidate / validity_overlap_supersede only)"`
	ScopeKind           string  `json:"scope_kind,omitempty" jsonschema:"scope kind"`
	ScopeValue          string  `json:"scope_value,omitempty" jsonschema:"scope value"`
	UpdatedAt           string  `json:"updated_at" jsonschema:"last update timestamp (RFC3339)"`
}
