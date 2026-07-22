package model

import "golang.org/x/xerrors"

// ErrInvalidRunLineage identifies a violated lineage invariant.
var ErrInvalidRunLineage = xerrors.New("invalid run lineage")

// ErrConflictingRunLineage identifies immutable same-identity semantic drift.
var ErrConflictingRunLineage = xerrors.New("conflicting run lineage")
