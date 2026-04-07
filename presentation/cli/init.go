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

const (
	dbPathEnvKey    = "TRACEARY_DB_PATH"
	dbPathFlagUsage = "SQLite DB パス (env: TRACEARY_DB_PATH)"
)

func (c *RootCLI) newInitCommand() *cobra.Command {
	var dbPath string

	initCmd := &cobra.Command{
		Use:   "init",
		Short: "ローカル SQLite ストアを明示的に初期化する",
		Long: strings.Join([]string{
			"ローカル SQLite ストアを明示的に初期化します。",
			"",
			"traceary の他コマンドも必要に応じて DB を自動作成し、マイグレーションを適用します。",
			"init は DB パスや書き込み権限を事前に確認したいときに使います。",
		}, "\n"),
		Example: strings.Join([]string{
			"  traceary init",
			"  TRACEARY_DB_PATH=/tmp/traceary.db traceary init",
		}, "\n"),
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return c.runInit(cmd.Context(), cmd.OutOrStdout(), dbPath)
		},
	}
	initCmd.Flags().StringVar(&dbPath, "db-path", "", dbPathFlagUsage)

	return initCmd
}

func (c *RootCLI) runInit(ctx context.Context, output io.Writer, dbPath string) error {
	resolvedPath, err := resolveDBPath(dbPath)
	if err != nil {
		return xerrors.Errorf("DB パスの解決に失敗しました: %w", err)
	}
	if err := c.initializeStoreUsecase.Run(ctx, resolvedPath); err != nil {
		return xerrors.Errorf("ストアの初期化に失敗しました: %w", err)
	}
	if _, err := fmt.Fprintf(output, "初期化しました: %s\n", resolvedPath); err != nil {
		return xerrors.Errorf("初期化結果の出力に失敗しました: %w", err)
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
			return "", xerrors.Errorf("ユーザーホームディレクトリの取得に失敗しました: %w", err)
		}
		trimmedPath = filepath.Join(homeDir, ".config", "traceary", "traceary.db")
	}

	absolutePath, err := filepath.Abs(trimmedPath)
	if err != nil {
		return "", xerrors.Errorf("絶対パスへの変換に失敗しました: %w", err)
	}

	return absolutePath, nil
}
