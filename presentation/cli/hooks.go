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

func (c *RootCLI) newHooksCommand() *cobra.Command {
	hooksCmd := &cobra.Command{
		Use:   "hooks",
		Short: "hook 設定例を生成する",
	}
	hooksCmd.AddCommand(c.newHooksPrintCommand())

	return hooksCmd
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
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return c.runHooksPrint(cmd.Context(), cmd.OutOrStdout(), hooksPrintCommandInput{
				client:      client,
				projectDir:  projectDir,
				tracearyBin: tracearyBin,
			})
		},
	}
	printCmd.Flags().StringVar(&client, "client", "", "対象クライアント (claude|codex|gemini)")
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
	if strings.TrimSpace(flagValue) == "" {
		executablePath, err := os.Executable()
		if err != nil {
			return "", xerrors.Errorf("実行中バイナリパスの取得に失敗しました: %w", err)
		}
		flagValue = executablePath
	}

	resolvedPath, err := filepath.Abs(strings.TrimSpace(flagValue))
	if err != nil {
		return "", xerrors.Errorf("絶対パス化に失敗しました: %w", err)
	}

	return resolvedPath, nil
}

func buildHooksSettings(
	client string,
	projectDir string,
	tracearyBin string,
) (*hooksSettings, error) {
	switch strings.ToLower(strings.TrimSpace(client)) {
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
