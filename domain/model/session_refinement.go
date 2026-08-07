package model

import (
	"strings"
	"time"

	"golang.org/x/xerrors"

	"github.com/duck8823/traceary/domain/types"
)

// SessionRefinement is the durable L2 session summary for one session.
// Traceary never composes summary text; it stores what a caller hands it and
// owns only generation and coverage bookkeeping.
type SessionRefinement struct {
	sessionID         types.SessionID
	generation        int
	coversFromEventID types.EventID
	coversToEventID   types.EventID
	summary           string
	keywords          string
	producedBy        string
	producedAt        time.Time
	degraded          bool
}

// NewSessionRefinement constructs a refinement that satisfies the stored
// invariants: generation ≥ 1, non-empty summary and produced_by, and non-empty
// coverage event ids. keywords may be empty.
func NewSessionRefinement(
	sessionID types.SessionID,
	generation int,
	coversFromEventID types.EventID,
	coversToEventID types.EventID,
	summary string,
	keywords string,
	producedBy string,
	producedAt time.Time,
	degraded bool,
) (*SessionRefinement, error) {
	refinement := &SessionRefinement{
		sessionID:         sessionID,
		generation:        generation,
		coversFromEventID: coversFromEventID,
		coversToEventID:   coversToEventID,
		summary:           strings.TrimSpace(summary),
		keywords:          keywords,
		producedBy:        strings.TrimSpace(producedBy),
		producedAt:        producedAt.UTC(),
		degraded:          degraded,
	}
	if err := refinement.validate(); err != nil {
		return nil, err
	}
	return refinement, nil
}

// SessionRefinementOf restores a refinement from persisted values. It applies
// the same invariants as NewSessionRefinement so corrupt rows fail closed.
func SessionRefinementOf(
	sessionID types.SessionID,
	generation int,
	coversFromEventID types.EventID,
	coversToEventID types.EventID,
	summary string,
	keywords string,
	producedBy string,
	producedAt time.Time,
	degraded bool,
) (*SessionRefinement, error) {
	return NewSessionRefinement(
		sessionID,
		generation,
		coversFromEventID,
		coversToEventID,
		summary,
		keywords,
		producedBy,
		producedAt,
		degraded,
	)
}

func (r *SessionRefinement) validate() error {
	if r == nil {
		return xerrors.Errorf("session refinement must not be nil: %w", ErrInvalidSessionRefinement)
	}
	if strings.TrimSpace(r.sessionID.String()) == "" {
		return xerrors.Errorf("session id is required: %w", ErrInvalidSessionRefinement)
	}
	if r.generation < 1 {
		return xerrors.Errorf("generation must be at least 1: %w", ErrInvalidSessionRefinement)
	}
	if strings.TrimSpace(r.coversFromEventID.String()) == "" {
		return xerrors.Errorf("covers_from event id is required: %w", ErrInvalidSessionRefinement)
	}
	if strings.TrimSpace(r.coversToEventID.String()) == "" {
		return xerrors.Errorf("covers_to event id is required: %w", ErrInvalidSessionRefinement)
	}
	if r.summary == "" {
		return xerrors.Errorf("summary must not be empty: %w", ErrInvalidSessionRefinement)
	}
	if r.producedBy == "" {
		return xerrors.Errorf("produced_by must not be empty: %w", ErrInvalidSessionRefinement)
	}
	if r.producedAt.IsZero() {
		return xerrors.Errorf("produced_at is required: %w", ErrInvalidSessionRefinement)
	}
	return nil
}

// SessionID returns the refined session.
func (r *SessionRefinement) SessionID() types.SessionID { return r.sessionID }

// Generation returns how many merges produced the stored row.
func (r *SessionRefinement) Generation() int { return r.generation }

// CoversFromEventID returns the earliest event covered by this refinement.
func (r *SessionRefinement) CoversFromEventID() types.EventID { return r.coversFromEventID }

// CoversToEventID returns the latest event covered by this refinement.
func (r *SessionRefinement) CoversToEventID() types.EventID { return r.coversToEventID }

// Summary returns the agent-authored (or gc-authored degraded) summary text.
func (r *SessionRefinement) Summary() string { return r.summary }

// Keywords returns the free-form keyword string stored with the refinement.
func (r *SessionRefinement) Keywords() string { return r.keywords }

// ProducedBy returns who authored the stored summary text.
func (r *SessionRefinement) ProducedBy() string { return r.producedBy }

// ProducedAt returns when the stored row was written.
func (r *SessionRefinement) ProducedAt() time.Time { return r.producedAt }

// Degraded reports whether the summary was machine-authored by gc.
func (r *SessionRefinement) Degraded() bool { return r.degraded }

// SessionRefineOutcome classifies the write-port result for callers.
type SessionRefineOutcome string

const (
	// SessionRefineOutcomeCreated means a first refinement was inserted.
	SessionRefineOutcomeCreated SessionRefineOutcome = "created"
	// SessionRefineOutcomeSuperseded means an existing refinement was replaced
	// by a later covers_to with generation incremented.
	SessionRefineOutcomeSuperseded SessionRefineOutcome = "superseded"
	// SessionRefineOutcomeUnchanged means the incoming covers_to did not
	// advance coverage, so generation and text were left alone.
	SessionRefineOutcomeUnchanged SessionRefineOutcome = "unchanged"
)

// SessionRefineResult is the write-port outcome plus the resulting row state.
type SessionRefineResult struct {
	outcome    SessionRefineOutcome
	refinement *SessionRefinement
}

// SessionRefineResultOf builds a result value.
func SessionRefineResultOf(outcome SessionRefineOutcome, refinement *SessionRefinement) (SessionRefineResult, error) {
	if refinement == nil {
		return SessionRefineResult{}, xerrors.Errorf("session refine result requires a refinement: %w", ErrInvalidSessionRefinement)
	}
	switch outcome {
	case SessionRefineOutcomeCreated, SessionRefineOutcomeSuperseded, SessionRefineOutcomeUnchanged:
	default:
		return SessionRefineResult{}, xerrors.Errorf("unknown session refine outcome %q: %w", outcome, ErrInvalidSessionRefinement)
	}
	return SessionRefineResult{outcome: outcome, refinement: refinement}, nil
}

// Outcome reports created, superseded, or unchanged.
func (r SessionRefineResult) Outcome() SessionRefineOutcome { return r.outcome }

// Refinement returns the row state after the operation (existing state on no-op).
func (r SessionRefineResult) Refinement() *SessionRefinement { return r.refinement }

// ErrInvalidSessionRefinement indicates a refinement invariant was violated.
var ErrInvalidSessionRefinement = xerrors.New("invalid session refinement")
