package cli

// contextOutput is the JSON shape produced by the `traceary context` command.
type contextOutput struct {
	ResolvedSessionID string      `json:"resolved_session_id,omitempty"`
	ResolvedWorkspace string      `json:"resolved_workspace,omitempty"`
	Events            []eventJSON `json:"events"`
}
