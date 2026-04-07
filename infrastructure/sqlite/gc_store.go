package sqlite

import (
	"context"
	"time"

	"golang.org/x/xerrors"

	"github.com/duck8823/traceary/application/usecase"
)

var _ usecase.GarbageCollector = (*Datasource)(nil)

// CollectGarbage は指定日時より古いイベントを削除します。
func (d *Datasource) CollectGarbage(
	ctx context.Context,
	dbPath string,
	before time.Time,
	dryRun bool,
) (int, error) {
	db, err := d.openDB(ctx, dbPath)
	if err != nil {
		return 0, xerrors.Errorf("gc 用の DB オープンに失敗しました: %w", err)
	}
	defer func() { _ = db.Close() }()

	beforeValue := formatTimestamp(before)

	var deleteCount int
	if err := db.QueryRowContext(
		ctx,
		`SELECT COUNT(*) FROM events WHERE created_at < ?`,
		beforeValue,
	).Scan(&deleteCount); err != nil {
		return 0, xerrors.Errorf("削除対象件数の取得に失敗しました: %w", err)
	}

	if dryRun || deleteCount == 0 {
		return deleteCount, nil
	}

	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return 0, xerrors.Errorf("gc トランザクション開始に失敗しました: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	if _, err := tx.ExecContext(
		ctx,
		`DELETE FROM events WHERE created_at < ?`,
		beforeValue,
	); err != nil {
		return 0, xerrors.Errorf("古いイベントの削除に失敗しました: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return 0, xerrors.Errorf("gc トランザクションの commit に失敗しました: %w", err)
	}

	if _, err := db.ExecContext(ctx, `VACUUM`); err != nil {
		return 0, xerrors.Errorf("VACUUM に失敗しました: %w", err)
	}

	return deleteCount, nil
}
