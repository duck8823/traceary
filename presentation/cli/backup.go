package cli

import (
	"context"
	"fmt"
	"io"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"
	"golang.org/x/xerrors"

	"github.com/duck8823/traceary/application/usecase"
)

type backupCreateCommandInput struct {
	dbPath     string
	outputPath string
	force      bool
}

type backupRestoreCommandInput struct {
	dbPath    string
	inputPath string
	force     bool
}

func (c *RootCLI) newBackupCommand() *cobra.Command {
	backupCmd := &cobra.Command{
		Use:   "backup",
		Short: Localize("Create or restore SQLite-backed backups", "SQLite ベースのバックアップを作成・復元する"),
	}
	backupCmd.AddCommand(c.newBackupCreateCommand())
	backupCmd.AddCommand(c.newBackupRestoreCommand())

	return backupCmd
}

func (c *RootCLI) newBackupCreateCommand() *cobra.Command {
	var (
		dbPath     string
		outputPath string
		force      bool
	)

	createCmd := &cobra.Command{
		Use:   "create",
		Short: Localize("Create a compact SQLite backup file", "コンパクトな SQLite バックアップファイルを作成する"),
		Args:  noArgsJP(),
		RunE: func(cmd *cobra.Command, _ []string) error {
			return c.runBackupCreate(cmd.Context(), cmd.OutOrStdout(), backupCreateCommandInput{
				dbPath:     dbPath,
				outputPath: outputPath,
				force:      force,
			})
		},
	}
	createCmd.Flags().StringVar(&dbPath, "db-path", "", dbPathFlagUsage())
	createCmd.Flags().StringVar(&outputPath, "output", "", Localize("backup output path", "バックアップ出力先パス"))
	createCmd.Flags().BoolVar(&force, "force", false, Localize("overwrite the backup file if it already exists", "既存のバックアップファイルがあれば上書きする"))
	if err := createCmd.MarkFlagRequired("output"); err != nil {
		panic(err)
	}

	return createCmd
}

func (c *RootCLI) newBackupRestoreCommand() *cobra.Command {
	var (
		dbPath    string
		inputPath string
		force     bool
	)

	restoreCmd := &cobra.Command{
		Use:   "restore",
		Short: Localize("Restore the SQLite store from a backup file", "バックアップファイルから SQLite ストアを復元する"),
		Args:  noArgsJP(),
		RunE: func(cmd *cobra.Command, _ []string) error {
			return c.runBackupRestore(cmd.Context(), cmd.OutOrStdout(), backupRestoreCommandInput{
				dbPath:    dbPath,
				inputPath: inputPath,
				force:     force,
			})
		},
	}
	restoreCmd.Flags().StringVar(&dbPath, "db-path", "", dbPathFlagUsage())
	restoreCmd.Flags().StringVar(&inputPath, "input", "", Localize("backup input path", "バックアップ入力パス"))
	restoreCmd.Flags().BoolVar(&force, "force", false, Localize("overwrite the destination DB if it already exists", "復元先 DB が既に存在する場合は上書きする"))
	if err := restoreCmd.MarkFlagRequired("input"); err != nil {
		panic(err)
	}

	return restoreCmd
}

func (c *RootCLI) runBackupCreate(
	ctx context.Context,
	output io.Writer,
	input backupCreateCommandInput,
) error {
	if c.initializeStoreUsecase == nil {
		return xerrors.Errorf(Localize("initialize store usecase is not configured", "ストア初期化ユースケースが設定されていません"))
	}
	if c.createStoreBackupUsecase == nil {
		return xerrors.Errorf(Localize("create store backup usecase is not configured", "バックアップ作成ユースケースが設定されていません"))
	}

	resolvedDBPath, err := resolveDBPath(input.dbPath)
	if err != nil {
		return xerrors.Errorf("%s: %w", Localize("failed to resolve DB path", "DB パスの解決に失敗しました"), err)
	}
	if err := c.initializeStoreUsecase.Run(ctx, resolvedDBPath); err != nil {
		return xerrors.Errorf("%s: %w", Localize("failed to initialize store", "ストアの初期化に失敗しました"), err)
	}

	resolvedOutputPath, err := resolveRequiredAbsolutePath(input.outputPath)
	if err != nil {
		return xerrors.Errorf("%s: %w", Localize("failed to resolve backup output path", "バックアップ出力先パスの解決に失敗しました"), err)
	}
	if err := c.createStoreBackupUsecase.Run(ctx, usecase.CreateStoreBackupInput{
		DBPath:     resolvedDBPath,
		OutputPath: resolvedOutputPath,
		Overwrite:  input.force,
	}); err != nil {
		return xerrors.Errorf("%s: %w", Localize("failed to create backup", "バックアップ作成に失敗しました"), err)
	}

	if _, err := fmt.Fprintf(output, "%s: %s\n", Localize("Created backup", "バックアップを作成しました"), resolvedOutputPath); err != nil {
		return xerrors.Errorf("%s: %w", Localize("failed to print backup result", "バックアップ結果の出力に失敗しました"), err)
	}

	return nil
}

func (c *RootCLI) runBackupRestore(
	ctx context.Context,
	output io.Writer,
	input backupRestoreCommandInput,
) error {
	if c.restoreStoreBackupUsecase == nil {
		return xerrors.Errorf(Localize("restore store backup usecase is not configured", "バックアップ復元ユースケースが設定されていません"))
	}

	resolvedDBPath, err := resolveDBPath(input.dbPath)
	if err != nil {
		return xerrors.Errorf("%s: %w", Localize("failed to resolve DB path", "DB パスの解決に失敗しました"), err)
	}
	resolvedInputPath, err := resolveRequiredAbsolutePath(input.inputPath)
	if err != nil {
		return xerrors.Errorf("%s: %w", Localize("failed to resolve backup input path", "バックアップ入力パスの解決に失敗しました"), err)
	}
	if err := c.restoreStoreBackupUsecase.Run(ctx, usecase.RestoreStoreBackupInput{
		DBPath:    resolvedDBPath,
		InputPath: resolvedInputPath,
		Overwrite: input.force,
	}); err != nil {
		return xerrors.Errorf("%s: %w", Localize("failed to restore backup", "バックアップ復元に失敗しました"), err)
	}

	if _, err := fmt.Fprintf(output, "%s: %s\n", Localize("Restored backup to", "バックアップを復元しました"), resolvedDBPath); err != nil {
		return xerrors.Errorf("%s: %w", Localize("failed to print restore result", "バックアップ復元結果の出力に失敗しました"), err)
	}

	return nil
}

func resolveRequiredAbsolutePath(path string) (string, error) {
	trimmedPath := strings.TrimSpace(path)
	if trimmedPath == "" {
		return "", xerrors.Errorf(Localize("path must not be empty", "パスは空にできません"))
	}

	resolvedPath, err := filepath.Abs(trimmedPath)
	if err != nil {
		return "", xerrors.Errorf("%s: %w", Localize("failed to resolve absolute path", "絶対パス化に失敗しました"), err)
	}

	return resolvedPath, nil
}
