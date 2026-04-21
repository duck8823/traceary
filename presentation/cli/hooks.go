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
		global      bool
		force       bool
		matcher     string
	)

	installCmd := &cobra.Command{
		Use:   "install --client <claude|codex|gemini>",
		Short: Localize("Write hook configuration examples to the standard config path", "標準の設定パスへ hook 設定例を書き出す"),
		Long: Localize(
			"Generate hook configuration for a supported client and write it to the standard config path.\nSupported clients: claude, codex, gemini.\nAliases: claude-code, codex-cli, gemini-cli.\nUse --global to write to the user-level config (~/.claude/settings.json for Claude, ~/.gemini/settings.json for Gemini). Codex hooks are already user-level, so --global is a no-op there.",
			"対応 client 向けの hook 設定を生成し、標準の設定パスへ書き出します。\n対応 client: claude, codex, gemini。\nalias: claude-code, codex-cli, gemini-cli。\n--global を指定すると user-level 設定に書き込みます (Claude は ~/.claude/settings.json、Gemini は ~/.gemini/settings.json)。Codex の hook は元から user-level なため --global は効果ありません。",
		),
		Example: strings.Join([]string{
			"  traceary hooks install --client claude --project-dir .",
			"  traceary hooks install --client claude --global",
			"  traceary hooks install --client codex-cli --force",
		}, "\n"),
		Args: noArgsLocalized(),
		RunE: func(cmd *cobra.Command, _ []string) error {
			return c.runHooksInstall(cmd.Context(), cmd.OutOrStdout(), hooksInstallCommandInput{
				client:      client,
				projectDir:  projectDir,
				tracearyBin: tracearyBin,
				outputPath:  outputPath,
				global:      global,
				force:       force,
				matcher:     matcher,
			})
		},
	}
	installCmd.Flags().StringVar(&client, "client", "", hooksClientFlagUsage)
	installCmd.Flags().StringVar(&projectDir, "project-dir", "", Localize("project directory whose config file should be written", "設定ファイルを書き出す対象のプロジェクトディレクトリ"))
	installCmd.Flags().StringVar(&tracearyBin, "traceary-bin", "", Localize("traceary binary path or command name", "traceary バイナリパス"))
	installCmd.Flags().StringVar(&outputPath, "output", "", Localize("override the output file path", "書き出し先を明示する"))
	installCmd.Flags().BoolVar(&global, "global", false, Localize("write to the user-level config instead of the project config (mutually exclusive with --output)", "project ではなく user-level 設定へ書き込む (--output とは排他)"))
	installCmd.Flags().BoolVar(&force, "force", false, Localize("overwrite the file if it already exists", "既存ファイルがある場合でも上書きする"))
	installCmd.Flags().StringVar(&matcher, "matcher", "", Localize("Claude PostToolUse matcher preset: minimal (Bash + mcp__.*), default (+ built-in tool list), all (+ .*). Ignored for other clients.", "Claude PostToolUse matcher preset: minimal (Bash + mcp__.*), default (+ 組み込み tool 列), all (+ .*)。他 client では無視されます。"))

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

	encoded, err := c.hooksOrchestrator.Generate(ctx, input.client, resolvedTracearyBin)
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
	if input.global && strings.TrimSpace(input.outputPath) != "" {
		return xerrors.Errorf(
			Localize(
				"--global and --output are mutually exclusive",
				"--global と --output は同時指定できません",
			),
		)
	}
	resolvedProjectDir, err := resolveHooksProjectDir(input.projectDir)
	if err != nil {
		return xerrors.Errorf("%s: %w", Localize("failed to resolve project directory", "project directory の解決に失敗しました"), err)
	}
	resolvedTracearyBin, err := resolveHooksTracearyBin(input.tracearyBin)
	if err != nil {
		return xerrors.Errorf("%s: %w", Localize("failed to resolve traceary binary path", "traceary binary path の解決に失敗しました"), err)
	}

	// When the Claude Code plugin is active it already delivers Traceary
	// hooks. Writing the same hooks into settings.json would cause every
	// audit event to fire twice. Skip by default and tell the user how
	// to override; require --force for an intentional duplicate.
	canonicalClient := normalizeHooksClientForDisplay(c, input.client)
	if canonicalClient == "claude" {
		detection := detectClaudeTracearyPluginForCLI()
		if detection.Active && !input.force {
			if _, err := fmt.Fprintf(
				output,
				Localize(
					"Skipped install: Traceary plugin %q is already enabled in %s.\nThat plugin already delivers Traceary hooks to Claude Code, so installing them into settings.json would record every audit event twice.\nIf you still want both registrations (e.g. for local development), re-run with --force.\n",
					"インストールをスキップしました: Traceary plugin %q が %s で既に有効です。\nこの plugin が Claude Code に Traceary hooks を既に提供しているため、settings.json に同じ hook を書き込むと audit が二重記録されます。\n意図的に両方登録したい場合 (開発用途など) は --force を付けて再実行してください。\n",
				),
				detection.PluginKey,
				detection.SettingsPath,
			); err != nil {
				return xerrors.Errorf("%s: %w", Localize("failed to print plugin skip notice", "plugin skip 通知の出力に失敗しました"), err)
			}
			return nil
		}
		if detection.Active && input.force {
			if _, err := fmt.Fprintf(
				output,
				Localize(
					"Warning: Traceary plugin %q is active in %s. Continuing anyway because --force was set. Every audit event will be recorded twice while both registrations coexist.\n",
					"警告: Traceary plugin %q が %s で有効です。--force が指定されているため処理を続行しますが、両方の登録が共存する間は audit が二重記録されます。\n",
				),
				detection.PluginKey,
				detection.SettingsPath,
			); err != nil {
				return xerrors.Errorf("%s: %w", Localize("failed to print plugin override warning", "plugin override 警告の出力に失敗しました"), err)
			}
		}
	}

	outputPathOption := types.None[string]()
	if trimmedOutput := strings.TrimSpace(input.outputPath); trimmedOutput != "" {
		outputPathOption = types.Some(trimmedOutput)
	}

	if input.global {
		globalPath, resolved, err := resolveHooksGlobalPath(canonicalClient)
		if err != nil {
			return xerrors.Errorf("%s: %w", Localize("failed to resolve global config path", "global 設定パスの解決に失敗しました"), err)
		}
		if !resolved {
			if _, err := fmt.Fprintf(
				output,
				Localize(
					"--global has no effect for %s: its hooks config is already user-level. Proceeding with the standard install path.\n",
					"%s では --global は意味を持ちません (hooks config は元から user-level です)。通常の install パスで続行します。\n",
				),
				canonicalClient,
			); err != nil {
				return xerrors.Errorf("%s: %w", Localize("failed to print global-noop notice", "global no-op 通知の出力に失敗しました"), err)
			}
		} else {
			outputPathOption = types.Some(globalPath)
		}
	}

	matcherPreset := strings.TrimSpace(input.matcher)
	if matcherPreset != "" {
		switch matcherPreset {
		case "minimal", "default", "all":
			// accepted
		default:
			return xerrors.Errorf(
				"%s: %q %s",
				Localize("invalid --matcher value", "--matcher の値が不正です"),
				matcherPreset,
				Localize("(allowed: minimal, default, all)", "(許容値: minimal, default, all)"),
			)
		}
	}

	resolvedOutputPath, err := c.hooksOrchestrator.InstallWithMatcher(
		ctx,
		input.client,
		resolvedTracearyBin,
		resolvedProjectDir,
		outputPathOption,
		input.force,
		matcherPreset,
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

// resolveHooksGlobalPath returns the user-level config path for --global,
// along with a bool indicating whether --global was actually resolved.
// Codex hooks already live under ~/.codex, so we return resolved=false
// and let the caller emit a no-op notice.
func resolveHooksGlobalPath(canonicalClient string) (string, bool, error) {
	home, err := userHomeDirFunc()
	if err != nil {
		return "", false, xerrors.Errorf("%s: %w", Localize("failed to resolve user home directory", "ユーザーホームディレクトリの解決に失敗しました"), err)
	}
	if !filepath.IsAbs(home) {
		return "", false, xerrors.Errorf(
			Localize(
				"refusing --global because resolved home directory is not absolute: %q. Ensure $HOME is set to an absolute path before running --global",
				"解決されたホームディレクトリが絶対パスではないため --global を拒否しました: %q。--global を使う前に $HOME を絶対パスに設定してください",
			),
			home,
		)
	}
	switch canonicalClient {
	case "claude":
		return filepath.Join(home, ".claude", "settings.json"), true, nil
	case "gemini":
		return filepath.Join(home, ".gemini", "settings.json"), true, nil
	case "codex":
		return "", false, nil
	}
	return "", false, xerrors.Errorf(
		Localize(
			"--global is not supported for client %q",
			"--global は client %q では未対応です",
		),
		canonicalClient,
	)
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
