package queryservice

import (
	"context"
	"strings"

	"golang.org/x/xerrors"

	"github.com/duck8823/traceary/domain/model"
)

// EventDetails は 1 件のイベント詳細を表します。
type EventDetails struct {
	event        *model.Event
	commandAudit *model.CommandAudit
}

// NewEventDetails は EventDetails を生成します。
func NewEventDetails(event *model.Event, commandAudit *model.CommandAudit) (*EventDetails, error) {
	if event == nil {
		return nil, xerrors.Errorf("イベントは nil にできません")
	}

	return &EventDetails{
		event:        event,
		commandAudit: commandAudit,
	}, nil
}

// Event は対象イベントを返します。
func (d *EventDetails) Event() *model.Event { return d.event }

// CommandAudit は紐づく command audit を返します。
func (d *EventDetails) CommandAudit() *model.CommandAudit { return d.commandAudit }

// EventDetailsFinder はイベント詳細取得を提供します。
type EventDetailsFinder interface {
	// GetEventDetails は event ID に対応する詳細を返します。
	GetEventDetails(ctx context.Context, dbPath string, eventID string) (*EventDetails, error)
}

// GetEventDetailsQueryService はイベント詳細クエリサービスです。
type GetEventDetailsQueryService interface {
	// Run は event ID に対応する詳細を返します。
	Run(ctx context.Context, dbPath string, eventID string) (*EventDetails, error)
}

type getEventDetailsQueryService struct {
	eventDetailsFinder EventDetailsFinder
}

// NewGetEventDetailsQueryService は GetEventDetailsQueryService を生成します。
func NewGetEventDetailsQueryService(eventDetailsFinder EventDetailsFinder) GetEventDetailsQueryService {
	return &getEventDetailsQueryService{eventDetailsFinder: eventDetailsFinder}
}

// Run は event ID に対応する詳細を返します。
func (s *getEventDetailsQueryService) Run(
	ctx context.Context,
	dbPath string,
	eventID string,
) (*EventDetails, error) {
	if s.eventDetailsFinder == nil {
		return nil, xerrors.Errorf("イベント詳細取得元が設定されていません")
	}
	if strings.TrimSpace(dbPath) == "" {
		return nil, xerrors.Errorf("DB パスは空にできません")
	}
	if strings.TrimSpace(eventID) == "" {
		return nil, xerrors.Errorf("event ID は空にできません")
	}

	eventDetails, err := s.eventDetailsFinder.GetEventDetails(ctx, dbPath, eventID)
	if err != nil {
		return nil, xerrors.Errorf("イベント詳細の取得に失敗しました: %w", err)
	}

	return eventDetails, nil
}
