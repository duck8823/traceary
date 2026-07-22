package model

import (
	"time"

	"golang.org/x/xerrors"

	"github.com/duck8823/traceary/domain/types"
)

// SessionSnapshot is the persistence-facing state required to rehydrate a
// Session aggregate. A missing runtime mode or terminal reason is accepted only
// for legacy data and mapped conservatively by SessionFromSnapshot.
type SessionSnapshot struct {
	SessionID       types.SessionID
	StartedAt       time.Time
	EndedAt         types.Optional[time.Time]
	Client          types.Client
	Agent           types.Agent
	Workspace       types.Workspace
	Label           string
	Summary         string
	Model           string
	RuntimeMode     types.RuntimeMode
	TerminalReason  types.Optional[types.TerminalReason]
	ParentSessionID types.SessionID
	SpawnEventID    types.EventID
	SubagentKind    string
	SpawnOrder      types.Optional[int]
}

// SessionFromSnapshot validates persisted lifecycle state before rehydrating the
// aggregate. Legacy empty runtime modes become interactive (the safe mode that
// is never synthetically finalized), and legacy ended rows without a reason
// become legacy_unknown rather than fabricated success or failure.
func SessionFromSnapshot(snapshot SessionSnapshot) (*Session, error) {
	mode := snapshot.RuntimeMode
	if mode.String() == "" {
		mode = types.RuntimeModeInteractive
	}
	validatedMode, err := types.RuntimeModeFrom(mode.String())
	if err != nil {
		return nil, xerrors.Errorf("restore session runtime mode: %w", err)
	}

	endedAt, ended := snapshot.EndedAt.Value()
	reason, hasReason := snapshot.TerminalReason.Value()
	if !ended && hasReason {
		return nil, xerrors.Errorf("active session cannot have terminal reason %q: %w", reason, ErrInvalidSessionState)
	}
	terminalReason := types.None[types.TerminalReason]()
	if ended {
		if endedAt.IsZero() || endedAt.Before(snapshot.StartedAt) {
			return nil, xerrors.Errorf("session terminal time precedes its start: %w", ErrInvalidSessionState)
		}
		if !hasReason {
			reason = types.TerminalReasonLegacyUnknown
		}
		validatedReason, err := types.TerminalReasonFrom(reason.String())
		if err != nil {
			return nil, xerrors.Errorf("restore session terminal reason: %w", err)
		}
		terminalReason = types.Some(validatedReason)
	}

	return &Session{
		sessionID:       snapshot.SessionID,
		startedAt:       snapshot.StartedAt,
		endedAt:         snapshot.EndedAt,
		client:          snapshot.Client,
		agent:           snapshot.Agent,
		workspace:       snapshot.Workspace,
		label:           snapshot.Label,
		summary:         snapshot.Summary,
		model:           snapshot.Model,
		runtimeMode:     validatedMode,
		terminalReason:  terminalReason,
		parentSessionID: snapshot.ParentSessionID,
		spawnEventID:    snapshot.SpawnEventID,
		subagentKind:    snapshot.SubagentKind,
		spawnOrder:      snapshot.SpawnOrder,
	}, nil
}
