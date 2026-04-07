package queryservice

import (
	"context"
	"strings"

	"golang.org/x/xerrors"

	"github.com/duck8823/traceary/domain/model"
)

// GetContextInput は文脈取得の入力です。
type GetContextInput struct {
	Repo      string
	SessionID string
	Limit     int
}

// ContextEventFinder は文脈向けイベント取得を提供します。
type ContextEventFinder interface {
	// GetContextEvents は条件に一致するイベントを新しい順に返します。
	GetContextEvents(ctx context.Context, dbPath string, input GetContextInput) ([]*model.Event, error)
}

// GetContextQueryService はイベント文脈を返すクエリサービスです。
type GetContextQueryService interface {
	// Run は条件に一致するイベントを返します。
	Run(ctx context.Context, dbPath string, input GetContextInput) ([]*model.Event, error)
}

type getContextQueryService struct {
	contextEventFinder ContextEventFinder
}

// NewGetContextQueryService は GetContextQueryService を生成します。
func NewGetContextQueryService(contextEventFinder ContextEventFinder) GetContextQueryService {
	return &getContextQueryService{contextEventFinder: contextEventFinder}
}

// Run は条件に一致するイベントを返します。
func (s *getContextQueryService) Run(
	ctx context.Context,
	dbPath string,
	input GetContextInput,
) ([]*model.Event, error) {
	if s.contextEventFinder == nil {
		return nil, xerrors.Errorf("文脈イベント取得元が設定されていません")
	}
	if strings.TrimSpace(dbPath) == "" {
		return nil, xerrors.Errorf("DB パスは空にできません")
	}
	if input.Limit <= 0 {
		return nil, xerrors.Errorf("limit は 1 以上である必要があります")
	}

	events, err := s.contextEventFinder.GetContextEvents(ctx, dbPath, input)
	if err != nil {
		return nil, xerrors.Errorf("文脈イベントの取得に失敗しました: %w", err)
	}

	return events, nil
}
