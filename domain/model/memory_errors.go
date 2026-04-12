package model

import "golang.org/x/xerrors"

// ErrInvalidMemoryState indicates that an operation cannot be performed
// because the memory aggregate is in an unexpected lifecycle state.
var ErrInvalidMemoryState = xerrors.New("invalid memory state")
