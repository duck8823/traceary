package cli

// event is the JSON shape of an event in CLI output.
type event struct {
	EventID   string `json:"event_id"`
	Kind      string `json:"kind"`
	Client    string `json:"client"`
	Agent     string `json:"agent"`
	SessionID string `json:"session_id"`
	Workspace string `json:"workspace"`
	Message   string `json:"message"`
	CreatedAt string `json:"created_at"`
}

// commandAudit is the JSON shape of a command audit in CLI output.
type commandAudit struct {
	Command         string `json:"command"`
	Input           string `json:"input"`
	Output          string `json:"output"`
	InputTruncated  bool   `json:"input_truncated"`
	OutputTruncated bool   `json:"output_truncated"`
	ExitCode        *int   `json:"exit_code,omitempty"`
}

// eventDetails is the JSON shape of an event-with-audit pair in CLI output.
type eventDetails struct {
	Event        event         `json:"event"`
	CommandAudit *commandAudit `json:"command_audit,omitempty"`
}

// contextOutput is the JSON shape produced by the `traceary context` command.
type contextOutput struct {
	ResolvedSessionID string  `json:"resolved_session_id,omitempty"`
	ResolvedWorkspace string  `json:"resolved_workspace,omitempty"`
	Events            []event `json:"events"`
}

// sessionTreeNode is the JSON shape of a single session in the tree output.
type sessionTreeNode struct {
	SessionID    string             `json:"session_id"`
	Workspace    string             `json:"workspace,omitempty"`
	Label        string             `json:"label,omitempty"`
	Summary      string             `json:"summary,omitempty"`
	StartedAt    string             `json:"started_at"`
	EndedAt      *string            `json:"ended_at,omitempty"`
	Status       string             `json:"status"`
	DurationSec  *float64           `json:"duration_sec,omitempty"`
	TotalEvents  int                `json:"total_events"`
	CommandCount int                `json:"command_count"`
	Agents       []string           `json:"agents"`
	Children     []*sessionTreeNode `json:"children"`
}
