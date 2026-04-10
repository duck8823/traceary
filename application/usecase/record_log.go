package usecase

import (
	"context"
	"strings"

	"golang.org/x/xerrors"

	"github.com/duck8823/traceary/domain/port"

	"github.com/duck8823/traceary/domain/model"
	"github.com/duck8823/traceary/domain/types"
)

// EventSaver is defined in domain/port.
type EventSaver = port.EventSaver

// RecordLogInput is the input for traceary log recording.
type RecordLogInput struct {
	DBPath    string
	Message   string
	Client    string
	Agent     string
	SessionID string
	Repo      string
}

// RecordLogUsecase persists note events.
type RecordLogUsecase interface {
	// Run persists a note event.
	Run(ctx context.Context, input RecordLogInput) (*model.Event, error)
}

type recordLogUsecase struct {
	eventSaver EventSaver
}

// NewRecordLogUsecase creates a RecordLogUsecase.
func NewRecordLogUsecase(eventSaver EventSaver) RecordLogUsecase {
	return &recordLogUsecase{eventSaver: eventSaver}
}

// Run persists a note event.
func (u *recordLogUsecase) Run(ctx context.Context, input RecordLogInput) (*model.Event, error) {
	if u.eventSaver == nil {
		return nil, xerrors.Errorf("event saver is not configured")
	}
	if strings.TrimSpace(input.DBPath) == "" {
		return nil, xerrors.Errorf("DB path must not be empty")
	}

	agent, err := types.AgentOf(input.Agent)
	if err != nil {
		return nil, xerrors.Errorf("failed to resolve agent: %w", err)
	}
	sessionID, err := types.SessionIDOf(input.SessionID)
	if err != nil {
		return nil, xerrors.Errorf("failed to resolve session ID: %w", err)
	}
	eventID, err := newEventID()
	if err != nil {
		return nil, xerrors.Errorf("failed to generate event ID: %w", err)
	}

	event, err := model.NewEvent(
		eventID,
		types.EventKindNote,
		strings.TrimSpace(input.Client),
		agent,
		sessionID,
		strings.TrimSpace(input.Repo),
		input.Message,
	)
	if err != nil {
		return nil, xerrors.Errorf("failed to build log event: %w", err)
	}
	if err := u.eventSaver.Save(ctx, input.DBPath, event); err != nil {
		return nil, xerrors.Errorf("failed to save log event: %w", err)
	}

	return event, nil
}
