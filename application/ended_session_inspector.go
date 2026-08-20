package application

import (
	"context"

	"github.com/duck8823/traceary/domain/types"
)

// EndedSessionInspector answers which of the given session IDs have a
// session_ended boundary in the store at dbPath. It is the bounded
// large-store counterpart of the session repository lookup: implementations
// must open the store read-only, key the query by session ID against the
// sessions primary key, and never walk event bodies or dbstat.
type EndedSessionInspector interface {
	FindEndedSessionIDs(ctx context.Context, dbPath string, sessionIDs []types.SessionID) (map[types.SessionID]struct{}, error)
}
