package queryservice

import (
	"context"

	"golang.org/x/xerrors"

	"github.com/duck8823/traceary/domain/model"
)

// ListRecentEventsInput は直近イベント一覧取得の入力です。
type ListRecentEventsInput struct {
	Limit  int
	Offset int
}

// RecentEventFinder は直近イベント一覧の取得を提供します。
type RecentEventFinder interface {
	// ListRecent は新しい順にイベントを取得します。
	ListRecent(ctx context.Context, dbPath string, input ListRecentEventsInput) ([]*model.Event, error)
}

// ListRecentEventsQueryService は直近イベント一覧を返すクエリサービスです。
type ListRecentEventsQueryService interface {
	// Run は直近イベント一覧を返します。
	Run(ctx context.Context, dbPath string, input ListRecentEventsInput) ([]*model.Event, error)
}

type listRecentEventsQueryService struct {
	recentEventFinder RecentEventFinder
}

// NewListRecentEventsQueryService は直近イベント一覧クエリサービスを生成します。
func NewListRecentEventsQueryService(recentEventFinder RecentEventFinder) ListRecentEventsQueryService {
	return &listRecentEventsQueryService{recentEventFinder: recentEventFinder}
}

// Run は直近イベント一覧を返します。
func (s *listRecentEventsQueryService) Run(
	ctx context.Context,
	dbPath string,
	input ListRecentEventsInput,
) ([]*model.Event, error) {
	if s.recentEventFinder == nil {
		return nil, xerrors.Errorf("直近イベント取得元が設定されていません")
	}
	if dbPath == "" {
		return nil, xerrors.Errorf("DB パスは空にできません")
	}
	if input.Limit <= 0 {
		return nil, xerrors.Errorf("limit は 1 以上である必要があります")
	}
	if input.Offset < 0 {
		return nil, xerrors.Errorf("offset は 0 以上である必要があります")
	}

	events, err := s.recentEventFinder.ListRecent(ctx, dbPath, input)
	if err != nil {
		return nil, xerrors.Errorf("直近イベントの取得に失敗しました: %w", err)
	}

	return events, nil
}
