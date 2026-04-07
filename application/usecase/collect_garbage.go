package usecase

import (
	"context"
	"strings"
	"time"

	"golang.org/x/xerrors"
)

// GarbageCollector は古いイベント削除を提供します。
type GarbageCollector interface {
	// CollectGarbage は指定日時より古いイベントを削除します。
	CollectGarbage(ctx context.Context, dbPath string, before time.Time, dryRun bool) (int, error)
}

// CollectGarbageInput は gc の入力です。
type CollectGarbageInput struct {
	DBPath string
	Before time.Time
	DryRun bool
}

// CollectGarbageResult は gc 実行結果です。
type CollectGarbageResult struct {
	DeletedCount int
	Before       time.Time
	DryRun       bool
}

// CollectGarbageUsecase は retention に基づく削除を行います。
type CollectGarbageUsecase interface {
	// Run は古いイベントの削除または dry-run を実行します。
	Run(ctx context.Context, input CollectGarbageInput) (*CollectGarbageResult, error)
}

type collectGarbageUsecase struct {
	garbageCollector GarbageCollector
}

// NewCollectGarbageUsecase は新しい CollectGarbageUsecase を生成します。
func NewCollectGarbageUsecase(garbageCollector GarbageCollector) CollectGarbageUsecase {
	return &collectGarbageUsecase{garbageCollector: garbageCollector}
}

// Run は古いイベントの削除または dry-run を実行します。
func (u *collectGarbageUsecase) Run(
	ctx context.Context,
	input CollectGarbageInput,
) (*CollectGarbageResult, error) {
	if u.garbageCollector == nil {
		return nil, xerrors.Errorf("gc 実行先が設定されていません")
	}
	if strings.TrimSpace(input.DBPath) == "" {
		return nil, xerrors.Errorf("DB パスは空にできません")
	}
	if input.Before.IsZero() {
		return nil, xerrors.Errorf("削除基準時刻は必須です")
	}

	deletedCount, err := u.garbageCollector.CollectGarbage(
		ctx,
		strings.TrimSpace(input.DBPath),
		input.Before,
		input.DryRun,
	)
	if err != nil {
		return nil, xerrors.Errorf("gc の実行に失敗しました: %w", err)
	}

	return &CollectGarbageResult{
		DeletedCount: deletedCount,
		Before:       input.Before,
		DryRun:       input.DryRun,
	}, nil
}
