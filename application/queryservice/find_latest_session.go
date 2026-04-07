package queryservice

import (
	"context"
	"errors"
	"strings"

	"golang.org/x/xerrors"

	"github.com/duck8823/traceary/domain/model"
)

// FindLatestSessionInput は直近セッション取得の入力です。
type FindLatestSessionInput struct {
	Client     string
	Agent      string
	Repo       string
	ActiveOnly bool
}

var (
	// ErrSessionNotFound は条件に一致する session が存在しないことを表します。
	ErrSessionNotFound = xerrors.New("条件に一致する session は存在しません")
	// ErrActiveSessionNotFound は条件に一致する active session が存在しないことを表します。
	ErrActiveSessionNotFound = xerrors.New("条件に一致する active session は存在しません")
)

type sessionLookupNotFoundError struct {
	err error
}

func (e *sessionLookupNotFoundError) Error() string { return e.err.Error() }

func (e *sessionLookupNotFoundError) Unwrap() error { return e.err }

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
		if errors.Is(err, ErrSessionNotFound) || errors.Is(err, ErrActiveSessionNotFound) {
			return nil, WrapSessionLookupNotFound(err)
		}
		return nil, xerrors.Errorf("直近セッション取得に失敗しました: %w", err)
	}

	return event, nil
}

// IsSessionLookupNotFound は session lookup 系の not found かどうかを返します。
func IsSessionLookupNotFound(err error) bool {
	return errors.Is(err, ErrSessionNotFound) || errors.Is(err, ErrActiveSessionNotFound)
}

// WrapSessionLookupNotFound は not found error をメッセージを変えずに wrap します。
func WrapSessionLookupNotFound(err error) error {
	if err == nil {
		return nil
	}
	if wrappedErr, ok := err.(*sessionLookupNotFoundError); ok {
		return wrappedErr
	}

	return &sessionLookupNotFoundError{err: err}
}
