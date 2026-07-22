package model

import "golang.org/x/xerrors"

// ErrInvalidSessionState indicates that an operation cannot be performed
// because the session is in an unexpected state (e.g. ending a session
// that does not exist, or ending an already-ended session).
var ErrInvalidSessionState = xerrors.New("invalid session state")

// ErrConflictingTerminalState indicates that a different effective terminal
// reason is already stored for the session. The first terminal state is never
// overwritten.
var ErrConflictingTerminalState = xerrors.Errorf("%w: conflicting terminal state", ErrInvalidSessionState)
