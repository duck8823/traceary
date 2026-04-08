package cli

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"
	"golang.org/x/xerrors"
)

var userHomeDirFunc = os.UserHomeDir

const dbPathEnvKey = "TRACEARY_DB_PATH"

func dbPathFlagUsage() string {
	return Localize("SQLite DB path (env: TRACEARY_DB_PATH)", "SQLite DB パス (env: TRACEARY_DB_PATH)")
}

func (c *RootCLI) newInitCommand() *cobra.Command {
	var dbPath string

	initCmd := &cobra.Command{
		Use:   "init",
		Short: Localize("Explicitly initialize the local SQLite store", "ローカル SQLite ストアを明示的に初期化する"),
		Long: strings.Join([]string{
			Localize("Explicitly initialize the local SQLite store.", "ローカル SQLite ストアを明示的に初期化します。"),
			"",
			Localize(
				"Other traceary commands create the DB and apply migrations on demand.",
				"traceary の他コマンドも必要に応じて DB を自動作成し、マイグレーションを適用します。",
			),
			Localize(
				"Use init when you want to verify the DB path or write permissions before a session starts.",
				"init は DB パスや書き込み権限を事前に確認したいときに使います。",
			),
		}, "\n"),
		Example: strings.Join([]string{
			"  traceary init",
			"  TRACEARY_DB_PATH=/tmp/traceary.db traceary init",
		}, "\n"),
		Args: noArgsJP(),
		RunE: func(cmd *cobra.Command, _ []string) error {
			return c.runInit(cmd.Context(), cmd.OutOrStdout(), dbPath)
		},
	}
	initCmd.Flags().StringVar(&dbPath, "db-path", "", dbPathFlagUsage())

	return initCmd
}

func (c *RootCLI) runInit(ctx context.Context, output io.Writer, dbPath string) error {
	resolvedPath, err := resolveDBPath(dbPath)
	if err != nil {
		return xerrors.Errorf("%s: %w", Localize("failed to resolve DB path", "DB パスの解決に失敗しました"), err)
	}
	if err := c.initializeStoreUsecase.Run(ctx, resolvedPath); err != nil {
		return xerrors.Errorf("%s: %w", Localize("failed to initialize store", "ストアの初期化に失敗しました"), err)
	}
	if _, err := fmt.Fprintf(output, "%s: %s\n", Localize("Initialized", "初期化しました"), resolvedPath); err != nil {
		return xerrors.Errorf("%s: %w", Localize("failed to print init result", "初期化結果の出力に失敗しました"), err)
	}
	return nil
}

func resolveDBPath(dbPath string) (string, error) {
	trimmedPath := strings.TrimSpace(dbPath)
	if trimmedPath == "" {
		trimmedPath = strings.TrimSpace(os.Getenv(dbPathEnvKey))
	}
	if trimmedPath == "" {
		homeDir, err := userHomeDirFunc()
		if err != nil {
			return "", xerrors.Errorf("%s: %w", Localize("failed to get user home directory", "ユーザーホームディレクトリの取得に失敗しました"), err)
		}
		trimmedPath = filepath.Join(homeDir, ".config", "traceary", "traceary.db")
	}

	absolutePath, err := filepath.Abs(trimmedPath)
	if err != nil {
		return "", xerrors.Errorf("%s: %w", Localize("failed to resolve absolute path", "絶対パスへの変換に失敗しました"), err)
	}

	return absolutePath, nil
}
