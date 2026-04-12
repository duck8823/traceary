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

	"github.com/duck8823/traceary/domain/types"
)

var hooksClientFlagUsage = Localize(
	"target client (claude|codex|gemini; aliases: claude-code, codex-cli, gemini-cli)",
	"対象クライアント (claude|codex|gemini; alias: claude-code, codex-cli, gemini-cli)",
)

func (c *RootCLI) newHooksCommand() *cobra.Command {
	hooksCmd := &cobra.Command{
		Use:   "hooks",
		Short: Localize("Generate hook configuration examples", "hook 設定例を生成する"),
	}
	hooksCmd.AddCommand(c.newHooksInstallCommand())
	hooksCmd.AddCommand(c.newHooksPrintCommand())
	hooksCmd.AddCommand(c.newHooksGuideCommand())
	hooksCmd.AddCommand(c.newHooksHelperCommand())

	return hooksCmd
}

func (c *RootCLI) newHooksInstallCommand() *cobra.Command {
	var (
		client      string
		projectDir  string
		tracearyBin string
		outputPath  string
		force       bool
	)

	installCmd := &cobra.Command{
		Use:   "install --client <claude|codex|gemini>",
		Short: Localize("Write hook configuration examples to the standard config path", "標準の設定パスへ hook 設定例を書き出す"),
		Long: Localize(
			"Generate hook configuration for a supported client and write it to the standard config path.\nSupported clients: claude, codex, gemini.\nAliases: claude-code, codex-cli, gemini-cli.",
			"対応 client 向けの hook 設定を生成し、標準の設定パスへ書き出します。\n対応 client: claude, codex, gemini。\nalias: claude-code, codex-cli, gemini-cli。",
		),
		Example: strings.Join([]string{
			"  traceary hooks install --client claude --project-dir .",
			"  traceary hooks install --client codex-cli --force",
		}, "\n"),
		Args: noArgsLocalized(),
		RunE: func(cmd *cobra.Command, _ []string) error {
			return c.runHooksInstall(cmd.Context(), cmd.OutOrStdout(), hooksInstallCommandInput{
				client:      client,
				projectDir:  projectDir,
				tracearyBin: tracearyBin,
				outputPath:  outputPath,
				force:       force,
			})
		},
	}
	installCmd.Flags().StringVar(&client, "client", "", hooksClientFlagUsage)
	installCmd.Flags().StringVar(&projectDir, "project-dir", "", Localize("project directory whose config file should be written", "設定ファイルを書き出す対象のプロジェクトディレクトリ"))
	installCmd.Flags().StringVar(&tracearyBin, "traceary-bin", "", Localize("traceary binary path or command name", "traceary バイナリパス"))
	installCmd.Flags().StringVar(&outputPath, "output", "", Localize("override the output file path", "書き出し先を明示する"))
	installCmd.Flags().BoolVar(&force, "force", false, Localize("overwrite the file if it already exists", "既存ファイルがある場合でも上書きする"))

	return installCmd
}

func (c *RootCLI) newHooksPrintCommand() *cobra.Command {
	var (
		client      string
		tracearyBin string
	)

	printCmd := &cobra.Command{
		Use:   "print --client <claude|codex|gemini>",
		Short: Localize("Print hook configuration examples for the current environment", "現在の環境向けの hook 設定例を出力する"),
		Long: Localize(
			"Print generated hook configuration for a supported client.\nSupported clients: claude, codex, gemini.\nAliases: claude-code, codex-cli, gemini-cli.\nWhen --traceary-bin is omitted, generated hooks call `traceary` from PATH.",
			"対応 client 向けの生成済み hook 設定を出力します。\n対応 client: claude, codex, gemini。\nalias: claude-code, codex-cli, gemini-cli。\n--traceary-bin を省略した場合、生成される hook は PATH 上の `traceary` を呼びます。",
		),
		Example: strings.Join([]string{
			"  traceary hooks print --client claude",
			"  traceary hooks print --client gemini-cli --traceary-bin ~/bin/traceary",
		}, "\n"),
		Args: noArgsLocalized(),
		RunE: func(cmd *cobra.Command, _ []string) error {
			return c.runHooksPrint(cmd.Context(), cmd.OutOrStdout(), hooksPrintCommandInput{
				client:      client,
				tracearyBin: tracearyBin,
			})
		},
	}
	printCmd.Flags().StringVar(&client, "client", "", hooksClientFlagUsage)
	printCmd.Flags().StringVar(&tracearyBin, "traceary-bin", "", Localize("traceary binary path or command name", "traceary バイナリパス"))

	return printCmd
}

func (c *RootCLI) runHooksPrint(
	ctx context.Context,
	output io.Writer,
	input hooksPrintCommandInput,
) error {
	if err := requireHooksClient(input.client); err != nil {
		return err
	}
	resolvedTracearyBin, err := resolveHooksTracearyBin(input.tracearyBin)
	if err != nil {
		return xerrors.Errorf("%s: %w", Localize("failed to resolve traceary binary path", "traceary binary path の解決に失敗しました"), err)
	}
	resolvedScriptsDir, err := c.hookScriptsInstaller.Ensure()
	if err != nil {
		return xerrors.Errorf("%s: %w", Localize("failed to prepare hook scripts", "hook script の準備に失敗しました"), err)
	}

	encoded, err := c.hooksOrchestrator.Generate(ctx, input.client, resolvedScriptsDir, resolvedTracearyBin)
	if err != nil {
		return xerrors.Errorf("%s: %w", Localize("failed to build hook configuration example", "hook 設定例の生成に失敗しました"), err)
	}
	if _, err := fmt.Fprintf(output, "%s\n", encoded); err != nil {
		return xerrors.Errorf("%s: %w", Localize("failed to print hook configuration example", "hook 設定例の出力に失敗しました"), err)
	}

	return nil
}

func (c *RootCLI) runHooksInstall(
	ctx context.Context,
	output io.Writer,
	input hooksInstallCommandInput,
) error {
	if err := requireHooksClient(input.client); err != nil {
		return err
	}
	resolvedProjectDir, err := resolveHooksProjectDir(input.projectDir)
	if err != nil {
		return xerrors.Errorf("%s: %w", Localize("failed to resolve project directory", "project directory の解決に失敗しました"), err)
	}
	resolvedTracearyBin, err := resolveHooksTracearyBin(input.tracearyBin)
	if err != nil {
		return xerrors.Errorf("%s: %w", Localize("failed to resolve traceary binary path", "traceary binary path の解決に失敗しました"), err)
	}
	resolvedScriptsDir, err := c.hookScriptsInstaller.Ensure()
	if err != nil {
		return xerrors.Errorf("%s: %w", Localize("failed to prepare hook scripts", "hook script の準備に失敗しました"), err)
	}

	outputPathOption := types.Empty[string]()
	if trimmedOutput := strings.TrimSpace(input.outputPath); trimmedOutput != "" {
		outputPathOption = types.Of(trimmedOutput)
	}

	resolvedOutputPath, err := c.hooksOrchestrator.Install(
		ctx,
		input.client,
		resolvedScriptsDir,
		resolvedTracearyBin,
		resolvedProjectDir,
		outputPathOption,
		input.force,
	)
	if err != nil {
		return xerrors.Errorf("%s: %w", Localize("failed to write hook configuration file", "hook 設定ファイルの書き出しに失敗しました"), err)
	}

	if _, err := fmt.Fprintf(
		output,
		Localize(
			"Wrote hook configuration: %s\nIf a config file already exists in that environment, review the diff before re-running with --force.\nNext step: traceary doctor --client %s --project-dir %s\nThen start the target client and verify with: traceary list --limit 10\n",
			"hook 設定を書き出しました: %s\n既存設定がある環境では差分を確認してから --force を使ってください\n次の確認: traceary doctor --client %s --project-dir %s\nその後、対象 client を起動して traceary list --limit 10 で確認してください\n",
		),
		resolvedOutputPath,
		normalizeHooksClientForDisplay(c, input.client),
		shellQuote(resolvedProjectDir),
	); err != nil {
		return xerrors.Errorf("%s: %w", Localize("failed to print hook install result", "hook 設定書き出し結果の出力に失敗しました"), err)
	}

	return nil
}

func normalizeHooksClientForDisplay(c *RootCLI, client string) string {
	if resolved, err := c.hooksOrchestrator.NormalizeClient(client); err == nil {
		return resolved
	}

	return client
}

func requireHooksClient(client string) error {
	if strings.TrimSpace(client) != "" {
		return nil
	}

	return xerrors.Errorf(
		Localize(
			"--client is required (supported: claude, codex, gemini; aliases: claude-code, codex-cli, gemini-cli)",
			"--client は必須です (対応 client: claude, codex, gemini; alias: claude-code, codex-cli, gemini-cli)",
		),
	)
}

func resolveHooksProjectDir(flagValue string) (string, error) {
	if strings.TrimSpace(flagValue) == "" {
		currentDir, err := os.Getwd()
		if err != nil {
			return "", xerrors.Errorf("%s: %w", Localize("failed to get current directory", "カレントディレクトリの取得に失敗しました"), err)
		}
		flagValue = currentDir
	}

	resolvedPath, err := filepath.Abs(strings.TrimSpace(flagValue))
	if err != nil {
		return "", xerrors.Errorf("%s: %w", Localize("failed to resolve absolute path", "絶対パス化に失敗しました"), err)
	}

	return resolvedPath, nil
}

func resolveHooksTracearyBin(flagValue string) (string, error) {
	trimmedValue := strings.TrimSpace(flagValue)
	if trimmedValue == "" {
		return "traceary", nil
	}

	if filepath.Base(trimmedValue) == trimmedValue && !strings.HasPrefix(trimmedValue, ".") {
		return trimmedValue, nil
	}

	resolvedPath, err := filepath.Abs(trimmedValue)
	if err != nil {
		return "", xerrors.Errorf("%s: %w", Localize("failed to resolve absolute path", "絶対パス化に失敗しました"), err)
	}

	return resolvedPath, nil
}

// shellQuote wraps a value in single quotes, escaping nested quotes so it can
// be safely embedded in a bash command line.
func shellQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", `'"'"'`) + "'"
}
