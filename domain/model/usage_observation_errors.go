package model

import "golang.org/x/xerrors"

// ErrInvalidUsageObservation identifies a violated observation invariant.
var ErrInvalidUsageObservation = xerrors.New("invalid usage observation")

// ErrConflictingUsageObservation identifies a fail-closed replay conflict.
var ErrConflictingUsageObservation = xerrors.New("conflicting usage observation")
