package usecase

import (
	"context"
	"strings"

	"golang.org/x/xerrors"

	"github.com/duck8823/traceary/domain/model"
	"github.com/duck8823/traceary/domain/types"
)

// RecordLogInput is the input for traceary log recording.
type RecordLogInput struct {
	Message   string
	Kind      string
	Client    string
	Agent     string
	SessionID string
	Workspace string
}

// RecordLogUsecase persists note events.
type RecordLogUsecase interface {
	// Run persists a note event.
	Run(ctx context.Context, input RecordLogInput) (*model.Event, error)
}

type recordLogUsecase struct {
	eventRepo model.EventRepository
}

// NewRecordLogUsecase creates a RecordLogUsecase.
func NewRecordLogUsecase(eventRepo model.EventRepository) RecordLogUsecase {
	return &recordLogUsecase{eventRepo: eventRepo}
}

// Run persists a note event.
func (u *recordLogUsecase) Run(ctx context.Context, input RecordLogInput) (*model.Event, error) {
	if u.eventRepo == nil {
		return nil, xerrors.Errorf("event repository is not configured")
	}
	agent, err := types.AgentOf(input.Agent)
	if err != nil {
		return nil, xerrors.Errorf("failed to resolve agent: %w", err)
	}
	sessionID, err := types.SessionIDOf(input.SessionID)
	if err != nil {
		return nil, xerrors.Errorf("failed to resolve session ID: %w", err)
	}
	kind := types.EventKindNote
	if strings.TrimSpace(input.Kind) != "" {
		kind, err = types.EventKindOf(input.Kind)
		if err != nil {
			return nil, xerrors.Errorf("failed to resolve event kind: %w", err)
		}
	}

	eventID, err := newEventID()
	if err != nil {
		return nil, xerrors.Errorf("failed to generate event ID: %w", err)
	}

	event, err := model.NewEvent(
		eventID,
		kind,
		strings.TrimSpace(input.Client),
		agent,
		sessionID,
		strings.TrimSpace(input.Workspace),
		input.Message,
	)
	if err != nil {
		return nil, xerrors.Errorf("failed to build log event: %w", err)
	}
	if err := u.eventRepo.Save(ctx, event); err != nil {
		return nil, xerrors.Errorf("failed to save log event: %w", err)
	}

	return event, nil
}
