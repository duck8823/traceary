package types

// CloseStaleSessionsResult is the result of a stale-session cleanup.
type CloseStaleSessionsResult struct {
	closedCount int
}

// CloseStaleSessionsResultOf creates a CloseStaleSessionsResult.
func CloseStaleSessionsResultOf(closedCount int) CloseStaleSessionsResult {
	return CloseStaleSessionsResult{closedCount: closedCount}
}

// ClosedCount returns the number of closed sessions.
func (r CloseStaleSessionsResult) ClosedCount() int { return r.closedCount }
