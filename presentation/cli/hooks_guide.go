package cli

import (
	"context"
	"fmt"
	"io"
	"strings"

	"github.com/spf13/cobra"
	"golang.org/x/xerrors"

	"github.com/duck8823/traceary/domain/types"
)

func (c *RootCLI) newHooksGuideCommand() *cobra.Command {
	var (
		client     string
		projectDir string
		outputPath string
	)
	var commandSetupErr error

	guideCmd := &cobra.Command{
		Use:   "guide",
		Short: Localize("Print guided setup steps for a supported client", "対応 client 向けの guided setup 手順を出力する"),
		Args:  noArgsLocalized(),
		RunE: func(cmd *cobra.Command, _ []string) error {
			if commandSetupErr != nil {
				return commandSetupErr
			}
			return c.runHooksGuide(cmd.Context(), cmd.OutOrStdout(), hooksGuideCommandInput{
				client:     client,
				projectDir: projectDir,
				outputPath: outputPath,
			})
		},
	}
	guideCmd.Flags().StringVar(&client, "client", "", hooksClientFlagUsage)
	guideCmd.Flags().StringVar(&projectDir, "project-dir", "", Localize("project directory used for project-local client configs", "project-local client config に使う project directory"))
	guideCmd.Flags().StringVar(&outputPath, "output", "", Localize("override the expected config file path", "想定 config file path を上書きする"))
	commandSetupErr = configureRequiredFlag(guideCmd, "client")

	return guideCmd
}

func (c *RootCLI) runHooksGuide(
	_ context.Context,
	output io.Writer,
	input hooksGuideCommandInput,
) error {
	if err := requireHooksClient(input.client); err != nil {
		return err
	}
	resolvedProjectDir, err := resolveHooksProjectDir(input.projectDir)
	if err != nil {
		return xerrors.Errorf("%s: %w", Localize("failed to resolve project directory", "project directory の解決に失敗しました"), err)
	}
	guide, err := buildHooksGuide(c, input.client, resolvedProjectDir, input.outputPath)
	if err != nil {
		return xerrors.Errorf("%s: %w", Localize("failed to build hooks guide", "hooks guide の生成に失敗しました"), err)
	}
	if err := writeHooksGuide(output, guide); err != nil {
		return xerrors.Errorf("%s: %w", Localize("failed to print hooks guide", "hooks guide の出力に失敗しました"), err)
	}

	return nil
}

type hooksGuide struct {
	client         string
	outputPath     string
	installCommand string
	doctorCommand  string
	verifyCommand  string
	notes          []string
}

func buildHooksGuide(c *RootCLI, client string, projectDir string, outputPath string) (*hooksGuide, error) {
	orchestrator := c.hooksOrchestrator
	resolvedClient, err := orchestrator.NormalizeClient(client)
	if err != nil {
		return nil, xerrors.Errorf("failed to normalize client: %w", err)
	}

	outputPathOption := types.None[string]()
	if trimmedOutput := strings.TrimSpace(outputPath); trimmedOutput != "" {
		outputPathOption = types.Some(trimmedOutput)
	}
	resolvedOutputPath, err := orchestrator.ResolveInstallPath(resolvedClient, projectDir, outputPathOption)
	if err != nil {
		return nil, xerrors.Errorf("failed to resolve install path: %w", err)
	}

	quotedProjectDir := shellQuote(projectDir)
	installCommand := "traceary hooks install --client " + resolvedClient + " --project-dir " + quotedProjectDir
	doctorCommand := "traceary doctor --client " + resolvedClient + " --project-dir " + quotedProjectDir
	verifyCommand := "traceary list --limit 10"

	notes := []string{
		localizef("Expected config path: %s", "想定 config path: %s", resolvedOutputPath),
	}
	switch resolvedClient {
	case "claude":
		notes = append(notes,
			Localize("After installing, start Claude Code in the target project and run at least one Bash command.", "install 後に対象 project で Claude Code を起動し、少なくとも 1 回 Bash command を実行してください。"),
		)
	case "codex":
		notes = append(notes,
			Localize("Codex fires Stop after every assistant response, so Traceary records the turn transcript but keeps the session open. A Codex session ends only via MCP manage_session or stale GC (traceary session gc).", "Codex は assistant 応答ごとに Stop を fire するため、Traceary は turn の transcript を記録しつつ session は開いたままにします。Codex session は MCP manage_session または stale GC (traceary session gc) でのみ終了します。"),
		)
	case "gemini":
		notes = append(notes,
			Localize("Gemini requires hooksConfig.enabled=true before Traceary hooks can run.", "Gemini では Traceary hook が動く前に hooksConfig.enabled=true が必要です。"),
		)
	case "antigravity":
		notes = append(notes,
			Localize("Antigravity has no SessionStart hook: Traceary starts/refreshes the session idempotently from PreInvocation (keyed by conversationId). Like Codex, Stop is a per-execution boundary, so the session stays open and ends only via MCP manage_session or stale GC (traceary session gc).", "Antigravity には SessionStart hook がありません: Traceary は PreInvocation (conversationId 単位) から session を冪等に開始/更新します。Codex 同様 Stop は execution 単位の境界なので session は開いたままで、MCP manage_session または stale GC (traceary session gc) でのみ終了します。"),
			Localize("Command audits are paired across PreToolUse (carries the command) and PostToolUse (carries the result), so only run_command tool calls are recorded. Use --global to install to ~/.gemini/config/hooks.json instead of the workspace .agents/hooks.json.", "command audit は PreToolUse (command を保持) と PostToolUse (結果を保持) を突き合わせて記録するため、run_command tool 呼び出しのみが対象です。workspace の .agents/hooks.json ではなく ~/.gemini/config/hooks.json に入れる場合は --global を使ってください。"),
			Localize("Current interactive and headless `agy --print` runs expose Stop with transcriptPath. Traceary recovers the latest prompt and response at Stop; use `antigravity-event-coverage` to detect runtime delivery gaps.", "現在の interactive と headless `agy --print` は transcriptPath 付き Stop を提供します。Traceary は Stop で最新の prompt と response を復元します。実行時の配送欠落は `antigravity-event-coverage` で検出してください。"),
			Localize("Hook-recorded Antigravity events use client=hook, agent=antigravity, so inspect them with `traceary list --agent antigravity` (not `--client antigravity`, which returns no rows for these events).", "hook が記録する Antigravity event は client=hook, agent=antigravity なので、`traceary list --agent antigravity` で確認してください（`--client antigravity` ではこれらの event は 0 件になります）。"),
		)
	}

	return &hooksGuide{
		client:         resolvedClient,
		outputPath:     resolvedOutputPath,
		installCommand: installCommand,
		doctorCommand:  doctorCommand,
		verifyCommand:  verifyCommand,
		notes:          notes,
	}, nil
}

func writeHooksGuide(output io.Writer, guide *hooksGuide) error {
	if guide == nil {
		return xerrors.New(Localize("hooks guide must not be nil", "hooks guide は nil にできません"))
	}

	if _, err := fmt.Fprintf(output, "TRACEARY HOOKS GUIDE (%s)\n", strings.ToUpper(guide.client)); err != nil {
		return xerrors.Errorf("%s: %w", Localize("failed to print guide header", "guide ヘッダーの出力に失敗しました"), err)
	}
	if _, err := fmt.Fprintf(output, "%s\n  %s\n", Localize("Install:", "Install:"), guide.installCommand); err != nil {
		return xerrors.Errorf("%s: %w", Localize("failed to print install step", "install 手順の出力に失敗しました"), err)
	}
	if _, err := fmt.Fprintf(output, "%s\n  %s\n", Localize("Check:", "Check:"), guide.doctorCommand); err != nil {
		return xerrors.Errorf("%s: %w", Localize("failed to print check step", "check 手順の出力に失敗しました"), err)
	}
	if _, err := fmt.Fprintf(output, "%s\n  %s\n", Localize("Verify:", "Verify:"), guide.verifyCommand); err != nil {
		return xerrors.Errorf("%s: %w", Localize("failed to print verify step", "verify 手順の出力に失敗しました"), err)
	}
	if _, err := fmt.Fprintln(output, Localize("Notes:", "Notes:")); err != nil {
		return xerrors.Errorf("%s: %w", Localize("failed to print notes header", "notes ヘッダーの出力に失敗しました"), err)
	}
	for _, note := range guide.notes {
		if _, err := fmt.Fprintf(output, "- %s\n", note); err != nil {
			return xerrors.Errorf("%s: %w", Localize("failed to print guide note", "guide note の出力に失敗しました"), err)
		}
	}

	return nil
}
