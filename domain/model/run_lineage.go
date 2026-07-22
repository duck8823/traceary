package model

import (
	"fmt"
	"unicode/utf8"

	"golang.org/x/xerrors"

	"github.com/duck8823/traceary/domain/types"
)

// RunLineage is one immutable, body-free execution lineage fact.
type RunLineage struct {
	identity        types.RunIdentity
	parent          types.Optional[types.RunIdentity]
	sessionID       types.Optional[types.SessionID]
	work            types.RunWorkAttribution
	packet          types.Optional[types.PacketIdentity]
	toolOutputBytes types.Optional[int64]
}

// RunLineageOf restores one lineage fact from validated domain values.
func RunLineageOf(
	identity types.RunIdentity,
	parent types.Optional[types.RunIdentity],
	sessionID types.Optional[types.SessionID],
	work types.RunWorkAttribution,
	packet types.Optional[types.PacketIdentity],
	toolOutputBytes types.Optional[int64],
) (*RunLineage, error) {
	lineage := &RunLineage{identity: identity, parent: parent, sessionID: sessionID, work: work, packet: packet, toolOutputBytes: toolOutputBytes}
	if err := lineage.validate(); err != nil {
		return nil, err
	}
	return lineage, nil
}

func (r *RunLineage) validate() error {
	if r == nil {
		return ErrInvalidRunLineage
	}
	identity, err := types.RunIdentityFrom(r.identity.Host(), r.identity.RunID())
	if err != nil || identity != r.identity {
		return xerrors.Errorf("invalid run lineage identity: %w", ErrInvalidRunLineage)
	}
	if parent, present := r.parent.Value(); present {
		validated, err := types.RunIdentityFrom(parent.Host(), parent.RunID())
		if err != nil || validated != parent || parent == r.identity {
			return xerrors.Errorf("invalid run lineage parent: %w", ErrInvalidRunLineage)
		}
	}
	if session, present := r.sessionID.Value(); present {
		if !utf8.ValidString(session.String()) {
			return xerrors.Errorf("invalid run lineage session: %w", ErrInvalidRunLineage)
		}
		validated, err := types.SessionIDFrom(session.String())
		if err != nil || validated != session {
			return xerrors.Errorf("invalid run lineage session: %w", ErrInvalidRunLineage)
		}
	}
	work, err := types.RunWorkAttributionFrom(r.work.BatchID(), r.work.TicketRef(), r.work.Repository(), r.work.PullRequestNumber(), r.work.HeadSHA())
	if err != nil || work != r.work {
		return xerrors.Errorf("invalid run work attribution: %w", ErrInvalidRunLineage)
	}
	if packet, present := r.packet.Value(); present {
		validated, err := types.PacketIdentityFrom(packet.SHA256(), packet.Bytes())
		if err != nil || validated != packet {
			return xerrors.Errorf("invalid packet identity: %w", ErrInvalidRunLineage)
		}
	}
	if bytes, present := r.toolOutputBytes.Value(); present && bytes < 0 {
		return xerrors.Errorf("tool output bytes must not be negative: %w", ErrInvalidRunLineage)
	}
	return nil
}

// RunLineageTransition reports whether an immutable fact was inserted or replayed.
type RunLineageTransition string

const (
	// RunLineageTransitionApplied means the lineage fact was inserted.
	RunLineageTransitionApplied RunLineageTransition = "applied"
	// RunLineageTransitionAlreadyApplied means an exact fact was already durable.
	RunLineageTransitionAlreadyApplied RunLineageTransition = "already_applied"
)

// Reconcile accepts exact replay and rejects every semantic difference.
func (r *RunLineage) Reconcile(proposed *RunLineage) (RunLineageTransition, error) {
	if r == nil || proposed == nil {
		return "", ErrInvalidRunLineage
	}
	if r.identity != proposed.identity {
		return "", newRunLineageConflict(proposed.identity, "identity")
	}
	checks := []struct {
		name  string
		equal bool
	}{
		{"parent", r.parent == proposed.parent}, {"session", r.sessionID == proposed.sessionID},
		{"work attribution", r.work == proposed.work}, {"packet identity", r.packet == proposed.packet},
		{"tool output bytes", r.toolOutputBytes == proposed.toolOutputBytes},
	}
	for _, check := range checks {
		if !check.equal {
			return "", newRunLineageConflict(r.identity, check.name)
		}
	}
	return RunLineageTransitionAlreadyApplied, nil
}

// Identity returns the namespaced opaque run identity.
func (r *RunLineage) Identity() types.RunIdentity { return r.identity }

// Parent returns the optional cross-host parent run.
func (r *RunLineage) Parent() types.Optional[types.RunIdentity] { return r.parent }

// SessionID returns the optional opaque host session correlation.
func (r *RunLineage) SessionID() types.Optional[types.SessionID] { return r.sessionID }

// Work returns optional body-free work grouping facts.
func (r *RunLineage) Work() types.RunWorkAttribution { return r.work }

// Packet returns the optional sanitized packet identity.
func (r *RunLineage) Packet() types.Optional[types.PacketIdentity] { return r.packet }

// ToolOutputBytes returns optional sanitized tool-output bytes, preserving zero.
func (r *RunLineage) ToolOutputBytes() types.Optional[int64] { return r.toolOutputBytes }

// RunLineageConflictError reports a closed conflicting field without its value.
type RunLineageConflictError struct {
	identity types.RunIdentity
	field    string
}

func newRunLineageConflict(identity types.RunIdentity, field string) *RunLineageConflictError {
	return &RunLineageConflictError{identity: identity, field: field}
}
func (e *RunLineageConflictError) Error() string {
	return fmt.Sprintf("run lineage %q/%q conflicts on %s", e.identity.Host(), e.identity.RunID(), e.field)
}
func (e *RunLineageConflictError) Unwrap() error { return ErrConflictingRunLineage }

// Identity returns the namespaced identity whose immutable fact conflicted.
func (e *RunLineageConflictError) Identity() types.RunIdentity { return e.identity }
