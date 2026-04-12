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
	SessionID      string   `json:"session_id,omitempty"`
	Workspace      string   `json:"workspace,omitempty"`
	Label          string   `json:"label,omitempty"`
	Status         string   `json:"status,omitempty"`
	TotalEvents    int      `json:"total_events"`
	CommandCount   int      `json:"command_count"`
	Agents         []string `json:"agents,omitempty"`
	Summary        string   `json:"summary,omitempty"`
	RecentCommands []string `json:"recent_commands,omitempty"`
}
