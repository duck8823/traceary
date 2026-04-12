package mcpserver

// addLogInput is the MCP input for the add_log tool.
type addLogInput struct {
	Message   string `json:"message" jsonschema:"log body to record"`
	Kind      string `json:"kind,omitempty" jsonschema:"event kind (default: note; allowed: note, compact_summary, prompt)"`
	Client    string `json:"client,omitempty" jsonschema:"recording channel (default: mcp)"`
	Agent     string `json:"agent,omitempty" jsonschema:"actor name (default: manual)"`
	SessionID string `json:"session_id,omitempty" jsonschema:"session ID (default: default)"`
	Workspace string `json:"workspace,omitempty" jsonschema:"auxiliary work context identifier"`
}

// startSessionInput is the MCP input for the start_session tool.
type startSessionInput struct {
	Client    string `json:"client,omitempty" jsonschema:"recording channel (default: mcp)"`
	Agent     string `json:"agent,omitempty" jsonschema:"actor name (default: manual)"`
	SessionID string `json:"session_id,omitempty" jsonschema:"session identifier (auto-generates when omitted)"`
	Workspace string `json:"workspace,omitempty" jsonschema:"auxiliary work context identifier"`
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

// addAuditInput is the MCP input for the add_audit tool.
type addAuditInput struct {
	Command   string `json:"command" jsonschema:"executed command"`
	Input     string `json:"input,omitempty" jsonschema:"command input"`
	Output    string `json:"output,omitempty" jsonschema:"command output"`
	Client    string `json:"client,omitempty" jsonschema:"recording channel (default: mcp)"`
	Agent     string `json:"agent,omitempty" jsonschema:"actor name (default: manual)"`
	SessionID string `json:"session_id,omitempty" jsonschema:"session ID (default: default)"`
	Workspace string `json:"workspace,omitempty" jsonschema:"auxiliary work context identifier"`
}

// listEventsInput is the MCP input for the list_events tool.
type listEventsInput struct {
	Limit     int    `json:"limit,omitempty" jsonschema:"result limit (default: 20)"`
	Offset    int    `json:"offset,omitempty" jsonschema:"offset from the newest result (default: 0)"`
	Kind      string `json:"kind,omitempty" jsonschema:"filter by event kind (note, command_executed, reviewed, session_started, session_ended, compact_summary, prompt; alias: audit)"`
	Client    string `json:"client,omitempty" jsonschema:"filter by client"`
	Agent     string `json:"agent,omitempty" jsonschema:"filter by agent"`
	SessionID string `json:"session_id,omitempty" jsonschema:"filter by session ID"`
	Workspace string `json:"workspace,omitempty" jsonschema:"filter by work context"`
	From      string `json:"from,omitempty" jsonschema:"start time (YYYY-MM-DD or RFC3339)"`
	To        string `json:"to,omitempty" jsonschema:"end time (YYYY-MM-DD or RFC3339)"`
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
	SessionID string `json:"session_id,omitempty"`
	Workspace string `json:"workspace,omitempty"`
}
