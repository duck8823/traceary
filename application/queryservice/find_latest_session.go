package queryservice

import (
	"context"
	"strings"

	"golang.org/x/xerrors"

	"github.com/duck8823/traceary/domain/model"
)

// FindLatestSessionInput は直近セッション取得の入力です。
type FindLatestSessionInput struct {
	Client string
	Agent  string
	Repo   string
}

// LatestSessionFinder は直近セッション開始イベントの取得を提供します。
type LatestSessionFinder interface {
	// FindLatestSessionStartedEvent は直近の session_started イベントを返します。
	FindLatestSessionStartedEvent(
		ctx context.Context,
		dbPath string,
		input FindLatestSessionInput,
	) (*model.Event, error)
}

// FindLatestSessionQueryService は直近セッション取得クエリサービスです。
type FindLatestSessionQueryService interface {
	// Run は直近の session_started イベントを返します。
	Run(ctx context.Context, dbPath string, input FindLatestSessionInput) (*model.Event, error)
}

type findLatestSessionQueryService struct {
	latestSessionFinder LatestSessionFinder
}

// NewFindLatestSessionQueryService は FindLatestSessionQueryService を生成します。
func NewFindLatestSessionQueryService(latestSessionFinder LatestSessionFinder) FindLatestSessionQueryService {
	return &findLatestSessionQueryService{latestSessionFinder: latestSessionFinder}
}

// Run は直近の session_started イベントを返します。
func (s *findLatestSessionQueryService) Run(
	ctx context.Context,
	dbPath string,
	input FindLatestSessionInput,
) (*model.Event, error) {
	if s.latestSessionFinder == nil {
		return nil, xerrors.Errorf("直近セッション取得元が設定されていません")
	}
	if strings.TrimSpace(dbPath) == "" {
		return nil, xerrors.Errorf("DB パスは空にできません")
	}

	event, err := s.latestSessionFinder.FindLatestSessionStartedEvent(ctx, dbPath, input)
	if err != nil {
		return nil, xerrors.Errorf("直近セッション取得に失敗しました: %w", err)
	}

	return event, nil
}
