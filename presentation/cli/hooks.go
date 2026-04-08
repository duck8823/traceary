package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"
	"golang.org/x/xerrors"
)

var hooksClientAliases = map[string]string{
	"claude":      "claude",
	"claude-code": "claude",
	"codex":       "codex",
	"codex-cli":   "codex",
	"gemini":      "gemini",
	"gemini-cli":  "gemini",
}

var hooksClientFlagUsage = Localize(
	"target client (claude|codex|gemini; aliases: claude-code, codex-cli, gemini-cli)",
	"対象クライアント (claude|codex|gemini; alias: claude-code, codex-cli, gemini-cli)",
)

type hooksSettings struct {
	Hooks map[string][]hookMatcher `json:"hooks"`
}

type hookMatcher struct {
	Matcher *string       `json:"matcher,omitempty"`
	Hooks   []hookCommand `json:"hooks"`
}

type hookCommand struct {
	Name        string `json:"name,omitempty"`
	Type        string `json:"type"`
	Command     string `json:"command"`
	Timeout     *int   `json:"timeout,omitempty"`
	Description string `json:"description,omitempty"`
}

type hooksPrintCommandInput struct {
	client      string
	tracearyBin string
}

type hooksInstallCommandInput struct {
	client      string
	projectDir  string
	tracearyBin string
	outputPath  string
	force       bool
}

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
		Use:   "install",
		Short: Localize("Write hook configuration examples to the standard config path", "標準の設定パスへ hook 設定例を書き出す"),
		Args:  noArgsJP(),
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
	if err := installCmd.MarkFlagRequired("client"); err != nil {
		panic(err)
	}

	return installCmd
}

func (c *RootCLI) newHooksPrintCommand() *cobra.Command {
	var (
		client      string
		tracearyBin string
	)

	printCmd := &cobra.Command{
		Use:   "print",
		Short: Localize("Print hook configuration examples for the current environment", "現在の環境向けの hook 設定例を出力する"),
		Args:  noArgsJP(),
		RunE: func(cmd *cobra.Command, _ []string) error {
			return c.runHooksPrint(cmd.Context(), cmd.OutOrStdout(), hooksPrintCommandInput{
				client:      client,
				tracearyBin: tracearyBin,
			})
		},
	}
	printCmd.Flags().StringVar(&client, "client", "", hooksClientFlagUsage)
	printCmd.Flags().StringVar(&tracearyBin, "traceary-bin", "", Localize("traceary binary path or command name", "traceary バイナリパス"))
	if err := printCmd.MarkFlagRequired("client"); err != nil {
		panic(err)
	}

	return printCmd
}

func (c *RootCLI) runHooksPrint(
	_ context.Context,
	output io.Writer,
	input hooksPrintCommandInput,
) error {
	resolvedTracearyBin, err := resolveHooksTracearyBin(input.tracearyBin)
	if err != nil {
		return xerrors.Errorf("%s: %w", Localize("failed to resolve traceary binary path", "traceary binary path の解決に失敗しました"), err)
	}
	resolvedScriptsDir, err := ensureHookScriptsInstalled()
	if err != nil {
		return xerrors.Errorf("%s: %w", Localize("failed to prepare hook scripts", "hook script の準備に失敗しました"), err)
	}

	settings, err := buildHooksSettings(input.client, resolvedScriptsDir, resolvedTracearyBin)
	if err != nil {
		return xerrors.Errorf("%s: %w", Localize("failed to build hook configuration example", "hook 設定例の生成に失敗しました"), err)
	}

	encoded, err := json.MarshalIndent(settings, "", "  ")
	if err != nil {
		return xerrors.Errorf("%s: %w", Localize("failed to marshal hook configuration example", "hook 設定例の JSON 変換に失敗しました"), err)
	}
	if _, err := fmt.Fprintf(output, "%s\n", encoded); err != nil {
		return xerrors.Errorf("%s: %w", Localize("failed to print hook configuration example", "hook 設定例の出力に失敗しました"), err)
	}

	return nil
}

func (c *RootCLI) runHooksInstall(
	_ context.Context,
	output io.Writer,
	input hooksInstallCommandInput,
) error {
	resolvedProjectDir, err := resolveHooksProjectDir(input.projectDir)
	if err != nil {
		return xerrors.Errorf("%s: %w", Localize("failed to resolve project directory", "project directory の解決に失敗しました"), err)
	}
	resolvedTracearyBin, err := resolveHooksTracearyBin(input.tracearyBin)
	if err != nil {
		return xerrors.Errorf("%s: %w", Localize("failed to resolve traceary binary path", "traceary binary path の解決に失敗しました"), err)
	}
	resolvedScriptsDir, err := ensureHookScriptsInstalled()
	if err != nil {
		return xerrors.Errorf("%s: %w", Localize("failed to prepare hook scripts", "hook script の準備に失敗しました"), err)
	}
	settings, err := buildHooksSettings(input.client, resolvedScriptsDir, resolvedTracearyBin)
	if err != nil {
		return xerrors.Errorf("%s: %w", Localize("failed to build hook configuration example", "hook 設定例の生成に失敗しました"), err)
	}
	resolvedOutputPath, err := resolveHooksInstallOutputPath(input.client, resolvedProjectDir, input.outputPath)
	if err != nil {
		return xerrors.Errorf("%s: %w", Localize("failed to resolve output path", "出力先の解決に失敗しました"), err)
	}
	if err := writeHooksSettingsFile(resolvedOutputPath, settings, input.force); err != nil {
		return xerrors.Errorf("%s: %w", Localize("failed to write hook configuration file", "hook 設定ファイルの書き出しに失敗しました"), err)
	}

	if _, err := fmt.Fprintf(
		output,
		Localize(
			"Wrote hook configuration: %s\nIf a config file already exists in that environment, review the diff before re-running with --force.\nNext step: traceary doctor --client %s --project-dir %s\nThen start the target client and verify with: traceary list --limit 10\n",
			"hook 設定を書き出しました: %s\n既存設定がある環境では差分を確認してから --force を使ってください\n次の確認: traceary doctor --client %s --project-dir %s\nその後、対象 client を起動して traceary list --limit 10 で確認してください\n",
		),
		resolvedOutputPath,
		normalizeHooksClientForDisplay(input.client),
		shellQuote(resolvedProjectDir),
	); err != nil {
		return xerrors.Errorf("%s: %w", Localize("failed to print hook install result", "hook 設定書き出し結果の出力に失敗しました"), err)
	}

	return nil
}

func normalizeHooksClientForDisplay(client string) string {
	resolvedClient, err := normalizeHooksClient(client)
	if err != nil {
		return client
	}

	return resolvedClient
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

func resolveHooksInstallOutputPath(client string, projectDir string, flagValue string) (string, error) {
	resolvedClient, err := normalizeHooksClient(client)
	if err != nil {
		return "", err
	}

	trimmedFlagValue := strings.TrimSpace(flagValue)
	if trimmedFlagValue != "" {
		resolvedPath, err := filepath.Abs(trimmedFlagValue)
		if err != nil {
			return "", xerrors.Errorf("%s: %w", Localize("failed to resolve absolute path", "絶対パス化に失敗しました"), err)
		}

		return resolvedPath, nil
	}

	switch resolvedClient {
	case "claude":
		return filepath.Join(projectDir, ".claude", "settings.json"), nil
	case "gemini":
		return filepath.Join(projectDir, ".gemini", "settings.json"), nil
	case "codex":
		homeDir, err := userHomeDirFunc()
		if err != nil {
			return "", xerrors.Errorf("%s: %w", Localize("failed to get user home directory", "ユーザーホームディレクトリの取得に失敗しました"), err)
		}

		return filepath.Join(homeDir, ".codex", "hooks.json"), nil
	default:
		return "", xerrors.Errorf(localizef("unsupported client: %s", "未対応の client です: %s", client))
	}
}

func writeHooksSettingsFile(outputPath string, settings *hooksSettings, force bool) error {
	if settings == nil {
		return xerrors.Errorf(Localize("hook settings must not be nil", "hook 設定は nil にできません"))
	}

	if err := os.MkdirAll(filepath.Dir(outputPath), 0o755); err != nil {
		return xerrors.Errorf("%s: %w", Localize("failed to create output directory", "出力先ディレクトリの作成に失敗しました"), err)
	}

	encoded, err := marshalHooksSettingsFile(outputPath, settings, force)
	if err != nil {
		return err
	}
	if err := os.WriteFile(outputPath, append(encoded, '\n'), 0o644); err != nil {
		return xerrors.Errorf("%s: %w", Localize("failed to write settings file", "設定ファイルの書き出しに失敗しました"), err)
	}

	return nil
}

func buildHooksSettings(
	client string,
	scriptsDir string,
	tracearyBin string,
) (*hooksSettings, error) {
	resolvedClient, err := normalizeHooksClient(client)
	if err != nil {
		return nil, err
	}

	switch resolvedClient {
	case "claude":
		return buildClaudeHooksSettings(scriptsDir, tracearyBin), nil
	case "codex":
		return buildCodexHooksSettings(scriptsDir, tracearyBin), nil
	case "gemini":
		return buildGeminiHooksSettings(scriptsDir, tracearyBin), nil
	default:
		return nil, xerrors.Errorf(localizef("unsupported client: %s", "未対応の client です: %s", client))
	}
}

func normalizeHooksClient(client string) (string, error) {
	trimmedClient := strings.ToLower(strings.TrimSpace(client))
	if resolvedClient, ok := hooksClientAliases[trimmedClient]; ok {
		return resolvedClient, nil
	}

	return "", xerrors.Errorf(
		Localize(
			"unsupported client: %s (valid values: claude, codex, gemini; aliases: claude-code, codex-cli, gemini-cli)",
			"未対応の client です: %s (有効値: claude, codex, gemini; alias: claude-code, codex-cli, gemini-cli)",
		),
		client,
	)
}

func buildClaudeHooksSettings(scriptsDir string, tracearyBin string) *hooksSettings {
	startCommand := buildHookScriptCommand(scriptsDir, tracearyBin, "traceary-session.sh", "claude", "start")
	endCommand := buildHookScriptCommand(scriptsDir, tracearyBin, "traceary-session.sh", "claude", "end")
	auditCommand := buildHookScriptCommand(scriptsDir, tracearyBin, "traceary-audit.sh", "claude")

	return &hooksSettings{
		Hooks: map[string][]hookMatcher{
			"SessionStart": {
				newHookMatcher("*", hookCommand{
					Type:    "command",
					Command: startCommand,
				}),
			},
			"SessionEnd": {
				newHookMatcher("*", hookCommand{
					Type:    "command",
					Command: endCommand,
				}),
			},
			"PostToolUse": {
				newHookMatcher("Bash", hookCommand{
					Type:    "command",
					Command: auditCommand,
				}),
			},
			"PostToolUseFailure": {
				newHookMatcher("Bash", hookCommand{
					Type:    "command",
					Command: auditCommand,
				}),
			},
		},
	}
}

func buildCodexHooksSettings(scriptsDir string, tracearyBin string) *hooksSettings {
	emptyMatcher := ""

	return &hooksSettings{
		Hooks: map[string][]hookMatcher{
			"SessionStart": {
				{
					Hooks: []hookCommand{
						{
							Type: "command",
							Command: buildHookScriptCommand(
								scriptsDir,
								tracearyBin,
								"traceary-session.sh",
								"codex",
								"start",
							),
						},
					},
				},
			},
			"Stop": {
				{
					Hooks: []hookCommand{
						{
							Type: "command",
							Command: buildHookScriptCommand(
								scriptsDir,
								tracearyBin,
								"traceary-session.sh",
								"codex",
								"stop",
							),
						},
					},
				},
			},
			"PostToolUse": {
				{
					Matcher: &emptyMatcher,
					Hooks: []hookCommand{
						{
							Type:    "command",
							Command: buildHookScriptCommand(scriptsDir, tracearyBin, "traceary-audit.sh", "codex"),
						},
					},
				},
			},
		},
	}
}

func buildGeminiHooksSettings(scriptsDir string, tracearyBin string) *hooksSettings {
	timeout := 5000

	return &hooksSettings{
		Hooks: map[string][]hookMatcher{
			"SessionStart": {
				newHookMatcher("*", hookCommand{
					Name:        "traceary-session-start",
					Type:        "command",
					Command:     buildHookScriptCommand(scriptsDir, tracearyBin, "traceary-session.sh", "gemini", "start"),
					Timeout:     &timeout,
					Description: "Start a Traceary session",
				}),
			},
			"SessionEnd": {
				newHookMatcher("*", hookCommand{
					Name:        "traceary-session-end",
					Type:        "command",
					Command:     buildHookScriptCommand(scriptsDir, tracearyBin, "traceary-session.sh", "gemini", "end"),
					Timeout:     &timeout,
					Description: "Finish a Traceary session",
				}),
			},
			"AfterTool": {
				newHookMatcher("run_shell_command", hookCommand{
					Name:        "traceary-audit",
					Type:        "command",
					Command:     buildHookScriptCommand(scriptsDir, tracearyBin, "traceary-audit.sh", "gemini"),
					Timeout:     &timeout,
					Description: "Record shell command audits in Traceary",
				}),
			},
		},
	}
}

func newHookMatcher(matcher string, command hookCommand) hookMatcher {
	return hookMatcher{
		Matcher: stringPointer(matcher),
		Hooks:   []hookCommand{command},
	}
}

func stringPointer(value string) *string {
	return &value
}

func buildHookScriptCommand(
	scriptsDir string,
	tracearyBin string,
	scriptName string,
	args ...string,
) string {
	parts := []string{
		"TRACEARY_BIN=" + shellQuote(tracearyBin),
		"bash",
		shellQuote(filepath.Join(scriptsDir, scriptName)),
	}
	for _, arg := range args {
		parts = append(parts, shellQuote(arg))
	}

	return strings.Join(parts, " ")
}

func shellQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", `'"'"'`) + "'"
}
