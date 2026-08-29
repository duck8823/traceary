package model

import (
	"context"

	"github.com/duck8823/traceary/domain/types"
)

// SessionKindRepository answers whether a session is a main (askable) session.
type SessionKindRepository interface {
	// IsMainSession is None when the session row does not exist yet.
	IsMainSession(ctx context.Context, sessionID types.SessionID) (types.Optional[bool], error)
}
