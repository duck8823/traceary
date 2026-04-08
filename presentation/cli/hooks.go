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

const hooksClientFlagUsage = "対象クライアント (claude|codex|gemini; alias: claude-code, codex-cli, gemini-cli)"

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
	projectDir  string
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
		Short: "hook 設定例を生成する",
	}
	hooksCmd.AddCommand(c.newHooksInstallCommand())
	hooksCmd.AddCommand(c.newHooksPrintCommand())

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
		Short: "標準の設定パスへ hook 設定例を書き出す",
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
	installCmd.Flags().StringVar(&projectDir, "project-dir", "", "hook script があるプロジェクトディレクトリ")
	installCmd.Flags().StringVar(&tracearyBin, "traceary-bin", "", "traceary バイナリパス")
	installCmd.Flags().StringVar(&outputPath, "output", "", "書き出し先を明示する")
	installCmd.Flags().BoolVar(&force, "force", false, "既存ファイルがある場合でも上書きする")
	if err := installCmd.MarkFlagRequired("client"); err != nil {
		panic(err)
	}

	return installCmd
}

func (c *RootCLI) newHooksPrintCommand() *cobra.Command {
	var (
		client      string
		projectDir  string
		tracearyBin string
	)

	printCmd := &cobra.Command{
		Use:   "print",
		Short: "現在の環境向けの hook 設定例を出力する",
		Args:  noArgsJP(),
		RunE: func(cmd *cobra.Command, _ []string) error {
			return c.runHooksPrint(cmd.Context(), cmd.OutOrStdout(), hooksPrintCommandInput{
				client:      client,
				projectDir:  projectDir,
				tracearyBin: tracearyBin,
			})
		},
	}
	printCmd.Flags().StringVar(&client, "client", "", hooksClientFlagUsage)
	printCmd.Flags().StringVar(&projectDir, "project-dir", "", "hook script があるプロジェクトディレクトリ")
	printCmd.Flags().StringVar(&tracearyBin, "traceary-bin", "", "traceary バイナリパス")
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
	resolvedProjectDir, err := resolveHooksProjectDir(input.projectDir)
	if err != nil {
		return xerrors.Errorf("project directory の解決に失敗しました: %w", err)
	}
	resolvedTracearyBin, err := resolveHooksTracearyBin(input.tracearyBin)
	if err != nil {
		return xerrors.Errorf("traceary binary path の解決に失敗しました: %w", err)
	}

	settings, err := buildHooksSettings(input.client, resolvedProjectDir, resolvedTracearyBin)
	if err != nil {
		return xerrors.Errorf("hook 設定例の生成に失敗しました: %w", err)
	}

	encoded, err := json.MarshalIndent(settings, "", "  ")
	if err != nil {
		return xerrors.Errorf("hook 設定例の JSON 変換に失敗しました: %w", err)
	}
	if _, err := fmt.Fprintf(output, "%s\n", encoded); err != nil {
		return xerrors.Errorf("hook 設定例の出力に失敗しました: %w", err)
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
		return xerrors.Errorf("project directory の解決に失敗しました: %w", err)
	}
	resolvedTracearyBin, err := resolveHooksTracearyBin(input.tracearyBin)
	if err != nil {
		return xerrors.Errorf("traceary binary path の解決に失敗しました: %w", err)
	}
	settings, err := buildHooksSettings(input.client, resolvedProjectDir, resolvedTracearyBin)
	if err != nil {
		return xerrors.Errorf("hook 設定例の生成に失敗しました: %w", err)
	}
	resolvedOutputPath, err := resolveHooksInstallOutputPath(input.client, resolvedProjectDir, input.outputPath)
	if err != nil {
		return xerrors.Errorf("出力先の解決に失敗しました: %w", err)
	}
	if err := writeHooksSettingsFile(resolvedOutputPath, settings, input.force); err != nil {
		return xerrors.Errorf("hook 設定ファイルの書き出しに失敗しました: %w", err)
	}

	if _, err := fmt.Fprintf(
		output,
		"hook 設定を書き出しました: %s\n既存設定がある環境では差分を確認してから --force を使ってください\n",
		resolvedOutputPath,
	); err != nil {
		return xerrors.Errorf("hook 設定書き出し結果の出力に失敗しました: %w", err)
	}

	return nil
}

func resolveHooksProjectDir(flagValue string) (string, error) {
	if strings.TrimSpace(flagValue) == "" {
		currentDir, err := os.Getwd()
		if err != nil {
			return "", xerrors.Errorf("カレントディレクトリの取得に失敗しました: %w", err)
		}
		flagValue = currentDir
	}

	resolvedPath, err := filepath.Abs(strings.TrimSpace(flagValue))
	if err != nil {
		return "", xerrors.Errorf("絶対パス化に失敗しました: %w", err)
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
		return "", xerrors.Errorf("絶対パス化に失敗しました: %w", err)
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
			return "", xerrors.Errorf("絶対パス化に失敗しました: %w", err)
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
			return "", xerrors.Errorf("ユーザーホームディレクトリの取得に失敗しました: %w", err)
		}

		return filepath.Join(homeDir, ".codex", "hooks.json"), nil
	default:
		return "", xerrors.Errorf("未対応の client です: %s", client)
	}
}

func writeHooksSettingsFile(outputPath string, settings *hooksSettings, force bool) error {
	if settings == nil {
		return xerrors.Errorf("hook 設定は nil にできません")
	}

	if _, err := os.Stat(outputPath); err == nil && !force {
		return xerrors.Errorf("既存ファイルがあるため上書きしません: %s", outputPath)
	} else if err != nil && !os.IsNotExist(err) {
		return xerrors.Errorf("既存ファイルの確認に失敗しました: %w", err)
	}

	encoded, err := json.MarshalIndent(settings, "", "  ")
	if err != nil {
		return xerrors.Errorf("hook 設定例の JSON 変換に失敗しました: %w", err)
	}

	if err := os.MkdirAll(filepath.Dir(outputPath), 0o755); err != nil {
		return xerrors.Errorf("出力先ディレクトリの作成に失敗しました: %w", err)
	}
	if err := os.WriteFile(outputPath, append(encoded, '\n'), 0o644); err != nil {
		return xerrors.Errorf("設定ファイルの書き出しに失敗しました: %w", err)
	}

	return nil
}

func buildHooksSettings(
	client string,
	projectDir string,
	tracearyBin string,
) (*hooksSettings, error) {
	resolvedClient, err := normalizeHooksClient(client)
	if err != nil {
		return nil, err
	}

	switch resolvedClient {
	case "claude":
		return buildClaudeHooksSettings(projectDir, tracearyBin), nil
	case "codex":
		return buildCodexHooksSettings(projectDir, tracearyBin), nil
	case "gemini":
		return buildGeminiHooksSettings(projectDir, tracearyBin), nil
	default:
		return nil, xerrors.Errorf("未対応の client です: %s", client)
	}
}

func normalizeHooksClient(client string) (string, error) {
	trimmedClient := strings.ToLower(strings.TrimSpace(client))
	if resolvedClient, ok := hooksClientAliases[trimmedClient]; ok {
		return resolvedClient, nil
	}

	return "", xerrors.Errorf(
		"未対応の client です: %s (有効値: claude, codex, gemini; alias: claude-code, codex-cli, gemini-cli)",
		client,
	)
}

func buildClaudeHooksSettings(projectDir string, tracearyBin string) *hooksSettings {
	startCommand := buildHookScriptCommand(projectDir, tracearyBin, "traceary-session.sh", "claude", "start")
	endCommand := buildHookScriptCommand(projectDir, tracearyBin, "traceary-session.sh", "claude", "end")
	auditCommand := buildHookScriptCommand(projectDir, tracearyBin, "traceary-audit.sh", "claude")

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

func buildCodexHooksSettings(projectDir string, tracearyBin string) *hooksSettings {
	emptyMatcher := ""

	return &hooksSettings{
		Hooks: map[string][]hookMatcher{
			"SessionStart": {
				{
					Hooks: []hookCommand{
						{
							Type: "command",
							Command: buildHookScriptCommand(
								projectDir,
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
								projectDir,
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
							Command: buildHookScriptCommand(projectDir, tracearyBin, "traceary-audit.sh", "codex"),
						},
					},
				},
			},
		},
	}
}

func buildGeminiHooksSettings(projectDir string, tracearyBin string) *hooksSettings {
	timeout := 5000

	return &hooksSettings{
		Hooks: map[string][]hookMatcher{
			"SessionStart": {
				newHookMatcher("*", hookCommand{
					Name:        "traceary-session-start",
					Type:        "command",
					Command:     buildHookScriptCommand(projectDir, tracearyBin, "traceary-session.sh", "gemini", "start"),
					Timeout:     &timeout,
					Description: "Start a Traceary session",
				}),
			},
			"SessionEnd": {
				newHookMatcher("*", hookCommand{
					Name:        "traceary-session-end",
					Type:        "command",
					Command:     buildHookScriptCommand(projectDir, tracearyBin, "traceary-session.sh", "gemini", "end"),
					Timeout:     &timeout,
					Description: "Finish a Traceary session",
				}),
			},
			"AfterTool": {
				newHookMatcher("run_shell_command", hookCommand{
					Name:        "traceary-audit",
					Type:        "command",
					Command:     buildHookScriptCommand(projectDir, tracearyBin, "traceary-audit.sh", "gemini"),
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
	projectDir string,
	tracearyBin string,
	scriptName string,
	args ...string,
) string {
	parts := []string{
		"TRACEARY_BIN=" + shellQuote(tracearyBin),
		"bash",
		shellQuote(filepath.Join(projectDir, "scripts", "hooks", scriptName)),
	}
	for _, arg := range args {
		parts = append(parts, shellQuote(arg))
	}

	return strings.Join(parts, " ")
}

func shellQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", `'"'"'`) + "'"
}
