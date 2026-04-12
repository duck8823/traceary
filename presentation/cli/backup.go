package cli

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"
	"golang.org/x/xerrors"

)

var errBackupRestoreCanceled = xerrors.New(Localize("restore canceled", "復元を中止しました"))

type backupRestorePrompter struct {
	reader      io.Reader
	interactive bool
}

func newDefaultBackupRestorePrompter() *backupRestorePrompter {
	return &backupRestorePrompter{
		reader:      os.Stdin,
		interactive: isTerminalFile(os.Stdin) && isTerminalFile(os.Stdout),
	}
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
		Use:   "create [output-path]",
		Short: Localize("Create a compact SQLite backup file", "コンパクトな SQLite バックアップファイルを作成する"),
		Args:  maximumNArgsLocalized(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			resolvedOutput := outputPath
			if len(args) == 1 && resolvedOutput != "" {
				return xerrors.Errorf(Localize(
					"output path specified twice: positional argument and --output flag",
					"出力先パスが二重に指定されています（位置引数と --output フラグ）",
				))
			}
			if len(args) == 1 {
				resolvedOutput = args[0]
			}
			if resolvedOutput == "" {
				return xerrors.Errorf(Localize("output path is required (positional argument or --output flag)", "出力先パスが必要です（位置引数または --output フラグ）"))
			}
			return c.runBackupCreate(cmd.Context(), cmd.OutOrStdout(), backupCreateCommandInput{
				dbPath:     dbPath,
				outputPath: resolvedOutput,
				force:      force,
			})
		},
	}
	createCmd.Flags().StringVar(&dbPath, "db-path", "", dbPathFlagUsage())
	createCmd.Flags().StringVar(&outputPath, "output", "", Localize("backup output path", "バックアップ出力先パス"))
	createCmd.Flags().BoolVar(&force, "force", false, Localize("overwrite the backup file if it already exists", "既存のバックアップファイルがあれば上書きする"))

	return createCmd
}

func (c *RootCLI) newBackupRestoreCommand() *cobra.Command {
	var (
		dbPath    string
		inputPath string
		force     bool
		assumeYes bool
	)
	var commandSetupErr error

	restoreCmd := &cobra.Command{
		Use:   "restore",
		Short: Localize("Restore the SQLite store from a backup file", "バックアップファイルから SQLite ストアを復元する"),
		Args:  noArgsLocalized(),
		RunE: func(cmd *cobra.Command, _ []string) error {
			if commandSetupErr != nil {
				return commandSetupErr
			}
			return c.runBackupRestore(cmd.Context(), cmd.OutOrStdout(), backupRestoreCommandInput{
				dbPath:    dbPath,
				inputPath: inputPath,
				force:     force,
				assumeYes: assumeYes,
			})
		},
	}
	restoreCmd.Flags().StringVar(&dbPath, "db-path", "", dbPathFlagUsage())
	restoreCmd.Flags().StringVar(&inputPath, "input", "", Localize("backup input path", "バックアップ入力パス"))
	restoreCmd.Flags().BoolVar(&force, "force", false, Localize("overwrite the destination DB if it already exists", "復元先 DB が既に存在する場合は上書きする"))
	restoreCmd.Flags().BoolVar(&assumeYes, "yes", false, Localize("skip the interactive confirmation prompt when overwriting an existing destination DB", "既存 DB を上書きするときの対話確認を省略する"))
	commandSetupErr = configureRequiredFlag(restoreCmd, "input")
	restoreCmd.Long = Localize(
		"Restore a Traceary SQLite backup into the destination DB path.\n\nIf the destination DB already exists, you must pass --force. On an interactive terminal, Traceary asks for confirmation before the destructive overwrite unless you also pass --yes.",
		"Traceary の SQLite バックアップを復元先 DB path へ戻します。\n\n復元先 DB が既に存在する場合は --force が必要です。対話端末では、破壊的な上書きを行う前に Traceary が確認を求めます。--yes を付けると確認を省略します。",
	)

	return restoreCmd
}

func (c *RootCLI) runBackupCreate(
	ctx context.Context,
	output io.Writer,
	input backupCreateCommandInput,
) error {
	if c.storeManagement == nil {
		return xerrors.Errorf(Localize("create store backup usecase is not configured", "バックアップ作成ユースケースが設定されていません"))
	}

	resolvedDBPath, err := resolveDBPath(input.dbPath)
	if err != nil {
		return xerrors.Errorf("%s: %w", Localize("failed to resolve DB path", "DB パスの解決に失敗しました"), err)
	}
	c.applyDatabasePath(resolvedDBPath)
	resolvedOutputPath, err := resolveRequiredAbsolutePath(input.outputPath)
	if err != nil {
		return xerrors.Errorf("%s: %w", Localize("failed to resolve backup output path", "バックアップ出力先パスの解決に失敗しました"), err)
	}
	if err := c.storeManagement.CreateBackup(ctx, resolvedOutputPath, input.force); err != nil {
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
	if c.storeManagement == nil {
		return xerrors.Errorf(Localize("restore store backup usecase is not configured", "バックアップ復元ユースケースが設定されていません"))
	}

	resolvedDBPath, err := resolveDBPath(input.dbPath)
	if err != nil {
		return xerrors.Errorf("%s: %w", Localize("failed to resolve DB path", "DB パスの解決に失敗しました"), err)
	}
	c.applyDatabasePath(resolvedDBPath)
	resolvedInputPath, err := resolveRequiredAbsolutePath(input.inputPath)
	if err != nil {
		return xerrors.Errorf("%s: %w", Localize("failed to resolve backup input path", "バックアップ入力パスの解決に失敗しました"), err)
	}
	destinationExists, err := pathExists(resolvedDBPath)
	if err != nil {
		return xerrors.Errorf("%s: %w", Localize("failed to inspect destination DB", "復元先 DB の確認に失敗しました"), err)
	}
	if destinationExists && input.force && !input.assumeYes {
		prompter := input.prompter
		if prompter == nil {
			prompter = newDefaultBackupRestorePrompter()
		}
		if prompter.needsInteractiveConfirmation() {
			if err := prompter.confirm(output, resolvedDBPath); err != nil {
				return err
			}
		}
	}
	// The use case rejects restores into an existing DB unless --force is set.
	if err := c.storeManagement.RestoreBackup(ctx, resolvedInputPath, input.force); err != nil {
		return xerrors.Errorf("%s: %w", Localize("failed to restore backup", "バックアップ復元に失敗しました"), err)
	}

	if _, err := fmt.Fprintf(output, "%s: %s\n", Localize("Restored backup to", "バックアップを復元しました"), resolvedDBPath); err != nil {
		return xerrors.Errorf("%s: %w", Localize("failed to print restore result", "バックアップ復元結果の出力に失敗しました"), err)
	}

	return nil
}

func pathExists(path string) (bool, error) {
	_, err := os.Stat(path)
	if err == nil {
		return true, nil
	}
	if os.IsNotExist(err) {
		return false, nil
	}

	return false, xerrors.Errorf("%s: %w", Localize("failed to inspect path", "パスの確認に失敗しました"), err)
}

func (p *backupRestorePrompter) needsInteractiveConfirmation() bool {
	if p == nil {
		return false
	}

	return p.interactive
}

func (p *backupRestorePrompter) confirm(output io.Writer, destinationPath string) error {
	reader := io.Reader(os.Stdin)
	if p != nil && p.reader != nil {
		reader = p.reader
	}

	prompt := Localize(
		"Warning: this will overwrite the existing destination DB.\nDestination: %s\nIf that data still matters, create a fresh backup first.\nContinue with restore? [y/N]: ",
		"警告: 既存の復元先 DB を上書きします。\n復元先: %s\nそのデータがまだ必要なら、先に新しいバックアップを作成してください。\n復元を続行しますか? [y/N]: ",
	)
	if _, err := fmt.Fprintf(output, prompt, destinationPath); err != nil {
		return xerrors.Errorf("%s: %w", Localize("failed to print restore confirmation", "復元確認の出力に失敗しました"), err)
	}

	line, err := bufio.NewReader(reader).ReadString('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		return xerrors.Errorf("%s: %w", Localize("failed to read restore confirmation", "復元確認の読み取りに失敗しました"), err)
	}

	switch strings.ToLower(strings.TrimSpace(line)) {
	case "y", "yes":
		return nil
	default:
		return errBackupRestoreCanceled
	}
}

func isTerminalFile(file *os.File) bool {
	if file == nil {
		return false
	}

	info, err := file.Stat()
	if err != nil {
		return false
	}

	return (info.Mode() & os.ModeCharDevice) != 0
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
