package queryservice

import (
	"context"
	"strings"
	"time"

	"golang.org/x/xerrors"

	"github.com/duck8823/traceary/domain/model"
	"github.com/duck8823/traceary/domain/types"
)

// SearchEventsInput は検索入力です。
type SearchEventsInput struct {
	Query     string
	Repo      string
	SessionID string
	Client    string
	Agent     string
	Kind      string
	From      time.Time
	To        time.Time
	Limit     int
}

// EventSearcher はイベント検索を提供します。
type EventSearcher interface {
	// SearchEvents は条件に一致するイベントを新しい順に返します。
	SearchEvents(ctx context.Context, dbPath string, input SearchEventsInput) ([]*model.Event, error)
}

// SearchEventsQueryService はイベント検索クエリサービスです。
type SearchEventsQueryService interface {
	// Run は条件に一致するイベントを返します。
	Run(ctx context.Context, dbPath string, input SearchEventsInput) ([]*model.Event, error)
}

type searchEventsQueryService struct {
	eventSearcher EventSearcher
}

// NewSearchEventsQueryService は SearchEventsQueryService を生成します。
func NewSearchEventsQueryService(eventSearcher EventSearcher) SearchEventsQueryService {
	return &searchEventsQueryService{eventSearcher: eventSearcher}
}

// Run は条件に一致するイベントを返します。
func (s *searchEventsQueryService) Run(
	ctx context.Context,
	dbPath string,
	input SearchEventsInput,
) ([]*model.Event, error) {
	if s.eventSearcher == nil {
		return nil, xerrors.Errorf("イベント検索元が設定されていません")
	}
	if strings.TrimSpace(dbPath) == "" {
		return nil, xerrors.Errorf("DB パスは空にできません")
	}
	if !hasSearchConstraint(input) {
		return nil, xerrors.Errorf("検索条件は1つ以上必要です")
	}
	if input.Limit <= 0 {
		return nil, xerrors.Errorf("limit は 1 以上である必要があります")
	}
	if !input.From.IsZero() && !input.To.IsZero() && input.From.After(input.To) {
		return nil, xerrors.Errorf("from は to より前である必要があります")
	}
	input.Query = strings.TrimSpace(input.Query)
	input.Repo = strings.TrimSpace(input.Repo)
	input.SessionID = strings.TrimSpace(input.SessionID)
	input.Client = strings.TrimSpace(input.Client)
	input.Agent = strings.TrimSpace(input.Agent)

	kind, err := resolveOptionalSearchKind(input.Kind)
	if err != nil {
		return nil, err
	}
	input.Kind = kind.String()

	events, err := s.eventSearcher.SearchEvents(ctx, dbPath, input)
	if err != nil {
		return nil, xerrors.Errorf("イベント検索に失敗しました: %w", err)
	}

	return events, nil
}

func hasSearchConstraint(input SearchEventsInput) bool {
	return strings.TrimSpace(input.Query) != "" ||
		strings.TrimSpace(input.Repo) != "" ||
		strings.TrimSpace(input.SessionID) != "" ||
		strings.TrimSpace(input.Client) != "" ||
		strings.TrimSpace(input.Agent) != "" ||
		strings.TrimSpace(input.Kind) != "" ||
		!input.From.IsZero() ||
		!input.To.IsZero()
}

func resolveOptionalSearchKind(value string) (types.EventKind, error) {
	trimmedValue := strings.TrimSpace(value)
	if trimmedValue == "" {
		return "", nil
	}

	kind, err := types.EventKindOf(trimmedValue)
	if err != nil {
		return "", xerrors.Errorf("kind の解決に失敗しました: %w", err)
	}

	return kind, nil
}
