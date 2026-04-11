package usecase

import "golang.org/x/xerrors"

// ErrSessionNotFound indicates that no matching session was found.
var ErrSessionNotFound = xerrors.New("no matching session found")
