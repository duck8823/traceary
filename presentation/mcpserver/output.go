package mcpserver

// addLogOutput is the MCP output for the add_log tool.
type addLogOutput struct {
	EventID   string `json:"event_id" jsonschema:"saved event ID"`
	Kind      string `json:"kind" jsonschema:"event kind"`
	Client    string `json:"client" jsonschema:"recording channel"`
	Agent     string `json:"agent" jsonschema:"actor"`
	SessionID string `json:"session_id" jsonschema:"session identifier"`
	Workspace string `json:"workspace,omitempty" jsonschema:"auxiliary work context identifier"`
	Body      string `json:"body" jsonschema:"event body"`
	CreatedAt string `json:"created_at" jsonschema:"event timestamp (RFC3339Nano)"`
}

// sessionEventOutput is the MCP output for session tools (start/end/active/latest).
type sessionEventOutput struct {
	EventID   string `json:"event_id" jsonschema:"saved or referenced event ID"`
	Kind      string `json:"kind" jsonschema:"event kind"`
	Client    string `json:"client" jsonschema:"recording channel"`
	Agent     string `json:"agent" jsonschema:"actor"`
	SessionID string `json:"session_id" jsonschema:"session identifier"`
	Workspace string `json:"workspace,omitempty" jsonschema:"auxiliary work context identifier"`
	CreatedAt string `json:"created_at" jsonschema:"event timestamp (RFC3339Nano)"`
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
	EventID   string `json:"event_id" jsonschema:"event ID"`
	Kind      string `json:"kind" jsonschema:"event kind"`
	Client    string `json:"client" jsonschema:"recording channel"`
	Agent     string `json:"agent" jsonschema:"actor"`
	SessionID string `json:"session_id" jsonschema:"session identifier"`
	Workspace string `json:"workspace,omitempty" jsonschema:"auxiliary work context identifier"`
	Body      string `json:"body" jsonschema:"event body"`
	CreatedAt string `json:"created_at" jsonschema:"event timestamp (RFC3339Nano)"`
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
type memorySummaryOutput struct {
	MemoryID   string `json:"memory_id" jsonschema:"durable memory identifier"`
	Type       string `json:"type" jsonschema:"memory type"`
	ScopeKind  string `json:"scope_kind" jsonschema:"scope kind"`
	ScopeValue string `json:"scope_value" jsonschema:"scope value"`
	Fact       string `json:"fact" jsonschema:"distilled memory fact"`
	Status     string `json:"status" jsonschema:"lifecycle status"`
	Confidence string `json:"confidence" jsonschema:"confidence level"`
	Source     string `json:"source" jsonschema:"memory source"`
	ExpiresAt  string `json:"expires_at,omitempty" jsonschema:"expiry timestamp (RFC3339Nano)"`
	CreatedAt  string `json:"created_at" jsonschema:"creation timestamp (RFC3339Nano)"`
	UpdatedAt  string `json:"updated_at" jsonschema:"update timestamp (RFC3339Nano)"`
}

// memoryOutput is the full durable-memory shape returned by MCP memory tools.
type memoryOutput struct {
	MemoryID     string            `json:"memory_id" jsonschema:"durable memory identifier"`
	Type         string            `json:"type" jsonschema:"memory type"`
	ScopeKind    string            `json:"scope_kind" jsonschema:"scope kind"`
	ScopeValue   string            `json:"scope_value" jsonschema:"scope value"`
	Fact         string            `json:"fact" jsonschema:"distilled memory fact"`
	Status       string            `json:"status" jsonschema:"lifecycle status"`
	Confidence   string            `json:"confidence" jsonschema:"confidence level"`
	Source       string            `json:"source" jsonschema:"memory source"`
	Supersedes   string            `json:"supersedes,omitempty" jsonschema:"superseded memory identifier"`
	ExpiresAt    string            `json:"expires_at,omitempty" jsonschema:"expiry timestamp (RFC3339Nano)"`
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
