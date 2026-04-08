package sqlite

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/xerrors"

	"github.com/duck8823/traceary/application/usecase"
)

var _ usecase.StoreBackupCreator = (*Datasource)(nil)
var _ usecase.StoreBackupRestorer = (*Datasource)(nil)

// CreateBackup は SQLite DB のバックアップを作成します。
func (d *Datasource) CreateBackup(ctx context.Context, dbPath string, outputPath string, overwrite bool) (err error) {
	sourcePath, destinationPath, err := validateDistinctDBPaths(dbPath, outputPath)
	if err != nil {
		return xerrors.Errorf("バックアップパスの検証に失敗しました: %w", err)
	}
	if err := d.Initialize(ctx, sourcePath); err != nil {
		return xerrors.Errorf("バックアップ元ストアの初期化に失敗しました: %w", err)
	}
	if err := ensureParentDir(destinationPath); err != nil {
		return xerrors.Errorf("バックアップ出力先ディレクトリの準備に失敗しました: %w", err)
	}
	if err := prepareDestinationFile(destinationPath, overwrite); err != nil {
		return xerrors.Errorf("バックアップ出力先の準備に失敗しました: %w", err)
	}

	db, err := d.openDB(ctx, sourcePath)
	if err != nil {
		return xerrors.Errorf("バックアップ元 DB のオープンに失敗しました: %w", err)
	}
	defer func() {
		if closeErr := db.Close(); closeErr != nil && err == nil {
			err = xerrors.Errorf("バックアップ元 DB のクローズに失敗しました: %w", closeErr)
		}
	}()

	statement := fmt.Sprintf("VACUUM INTO %s", quoteSQLiteStringLiteral(destinationPath))
	if _, err := db.ExecContext(ctx, statement); err != nil {
		return xerrors.Errorf("SQLite バックアップの作成に失敗しました: %w", err)
	}
	if err := os.Chmod(destinationPath, 0o600); err != nil {
		return xerrors.Errorf("バックアップファイル権限の設定に失敗しました: %w", err)
	}

	return nil
}

// RestoreBackup はバックアップファイルから SQLite DB を復元します。
func (d *Datasource) RestoreBackup(ctx context.Context, inputPath string, dbPath string, overwrite bool) (err error) {
	sourcePath, destinationPath, err := validateDistinctDBPaths(inputPath, dbPath)
	if err != nil {
		return xerrors.Errorf("復元パスの検証に失敗しました: %w", err)
	}
	inputInfo, err := os.Stat(sourcePath)
	if err != nil {
		return xerrors.Errorf("バックアップ入力ファイルの確認に失敗しました: %w", err)
	}
	if !inputInfo.Mode().IsRegular() {
		return xerrors.Errorf("バックアップ入力ファイルは通常ファイルである必要があります")
	}
	if err := ensureParentDir(destinationPath); err != nil {
		return xerrors.Errorf("復元先ディレクトリの準備に失敗しました: %w", err)
	}
	if err := prepareDestinationFile(destinationPath, overwrite); err != nil {
		return xerrors.Errorf("復元先ファイルの準備に失敗しました: %w", err)
	}

	if err := copyFileViaTempRename(sourcePath, destinationPath); err != nil {
		return xerrors.Errorf("バックアップファイルのコピーに失敗しました: %w", err)
	}
	if err := os.Chmod(destinationPath, 0o600); err != nil {
		return xerrors.Errorf("復元した DB ファイル権限の設定に失敗しました: %w", err)
	}
	if err := d.Initialize(ctx, destinationPath); err != nil {
		return xerrors.Errorf("復元後のストア初期化に失敗しました: %w", err)
	}

	return nil
}

func validateDistinctDBPaths(firstPath string, secondPath string) (string, string, error) {
	trimmedFirstPath := strings.TrimSpace(firstPath)
	if trimmedFirstPath == "" {
		return "", "", xerrors.Errorf("パスは空にできません")
	}
	trimmedSecondPath := strings.TrimSpace(secondPath)
	if trimmedSecondPath == "" {
		return "", "", xerrors.Errorf("パスは空にできません")
	}

	resolvedFirstPath, err := filepath.Abs(trimmedFirstPath)
	if err != nil {
		return "", "", xerrors.Errorf("絶対パス解決に失敗しました: %w", err)
	}
	resolvedSecondPath, err := filepath.Abs(trimmedSecondPath)
	if err != nil {
		return "", "", xerrors.Errorf("絶対パス解決に失敗しました: %w", err)
	}
	if resolvedFirstPath == resolvedSecondPath {
		return "", "", xerrors.Errorf("同じパスは指定できません")
	}

	return resolvedFirstPath, resolvedSecondPath, nil
}

func ensureParentDir(path string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return xerrors.Errorf("親ディレクトリの作成に失敗しました: %w", err)
	}

	return nil
}

func prepareDestinationFile(path string, overwrite bool) error {
	if !overwrite {
		if _, err := os.Stat(path); err == nil {
			return xerrors.Errorf("出力先はすでに存在します")
		} else if !os.IsNotExist(err) {
			return xerrors.Errorf("出力先の確認に失敗しました: %w", err)
		}

		return nil
	}

	for _, candidate := range []string{path, path + "-wal", path + "-shm"} {
		if err := removeFileIfExists(candidate); err != nil {
			return xerrors.Errorf("既存ファイルの削除に失敗しました: %w", err)
		}
	}

	return nil
}

func removeFileIfExists(path string) error {
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return xerrors.Errorf("ファイル削除に失敗しました: %w", err)
	}

	return nil
}

func copyFileViaTempRename(sourcePath string, destinationPath string) (err error) {
	sourceFile, err := os.Open(sourcePath)
	if err != nil {
		return xerrors.Errorf("入力ファイルのオープンに失敗しました: %w", err)
	}
	defer func() {
		if closeErr := sourceFile.Close(); closeErr != nil && err == nil {
			err = xerrors.Errorf("入力ファイルのクローズに失敗しました: %w", closeErr)
		}
	}()

	tempFile, err := os.CreateTemp(filepath.Dir(destinationPath), "traceary-restore-*.db")
	if err != nil {
		return xerrors.Errorf("一時ファイルの作成に失敗しました: %w", err)
	}
	tempPath := tempFile.Name()
	defer func() {
		if err != nil {
			_ = os.Remove(tempPath)
		}
	}()

	if err := tempFile.Chmod(0o600); err != nil {
		return xerrors.Errorf("一時ファイル権限の設定に失敗しました: %w", err)
	}
	if _, err := io.Copy(tempFile, sourceFile); err != nil {
		return xerrors.Errorf("入力ファイルのコピーに失敗しました: %w", err)
	}
	if err := tempFile.Sync(); err != nil {
		return xerrors.Errorf("一時ファイルの同期に失敗しました: %w", err)
	}
	if err := tempFile.Close(); err != nil {
		return xerrors.Errorf("一時ファイルのクローズに失敗しました: %w", err)
	}
	if err := os.Rename(tempPath, destinationPath); err != nil {
		return xerrors.Errorf("一時ファイルの配置に失敗しました: %w", err)
	}

	return nil
}

func quoteSQLiteStringLiteral(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "''") + "'"
}
