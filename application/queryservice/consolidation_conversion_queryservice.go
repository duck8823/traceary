package queryservice

import (
	"context"
	"time"
)

// ConsolidationConversionRow is one host's conversion over a doctor window.
type ConsolidationConversionRow struct {
	Client            string
	Requests          int
	SessionsRequested int
	RequestsAccepted  int
	SessionsRefined   int
}

// RefinementAuthorshipRow is one (client, produced_by) bucket over sessions
// that were asked in the window. ProducedBy == "" means asked with no
// refinement at or after the first ask.
type RefinementAuthorshipRow struct {
	Client     string
	ProducedBy string
	Sessions   int
}

// ConsolidationConversionQueryService is the bounded doctor conversion read.
type ConsolidationConversionQueryService interface {
	ConversionSince(ctx context.Context, since time.Time) ([]ConsolidationConversionRow, error)
	RefinementAuthorshipSince(ctx context.Context, since time.Time) ([]RefinementAuthorshipRow, error)
}
