package sqlite

import (
	"context"
	"database/sql"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	_ "modernc.org/sqlite" // SQLite ドライバーを登録するために必要です。

	"golang.org/x/xerrors"

	"github.com/duck8823/traceary/application/usecase"
)

// Datasource は SQLite ベースのデータソースです。
type Datasource struct {
	migrations fs.FS
}

var _ usecase.StoreInitializer = (*Datasource)(nil)

// NewDatasource は新しい Datasource を生成します。
func NewDatasource(migrations fs.FS) *Datasource {
	return &Datasource{migrations: migrations}
}

// Initialize は SQLite ストアを初期化します。
func (d *Datasource) Initialize(ctx context.Context, dbPath string) (err error) {
	trimmedPath := strings.TrimSpace(dbPath)
	if trimmedPath == "" {
		return xerrors.Errorf("DB パスは空にできません")
	}

	if err := os.MkdirAll(filepath.Dir(trimmedPath), 0o700); err != nil {
		return xerrors.Errorf("DB ディレクトリの作成に失敗しました: %w", err)
	}

	db, err := sql.Open("sqlite", trimmedPath)
	if err != nil {
		return xerrors.Errorf("SQLite 接続の初期化に失敗しました: %w", err)
	}
	defer func() {
		if closeErr := db.Close(); closeErr != nil && err == nil {
			err = xerrors.Errorf("SQLite 接続のクローズに失敗しました: %w", closeErr)
		}
	}()

	if err := db.PingContext(ctx); err != nil {
		return xerrors.Errorf("SQLite への接続確認に失敗しました: %w", err)
	}
	if err := os.Chmod(trimmedPath, 0o600); err != nil {
		return xerrors.Errorf("SQLite DB ファイル権限の設定に失敗しました: %w", err)
	}

	if _, err := db.ExecContext(ctx, `PRAGMA foreign_keys = ON;`); err != nil {
		return xerrors.Errorf("SQLite の PRAGMA 設定に失敗しました: %w", err)
	}

	if err := d.migrate(ctx, db); err != nil {
		return xerrors.Errorf("SQLite マイグレーションに失敗しました: %w", err)
	}

	return nil
}
