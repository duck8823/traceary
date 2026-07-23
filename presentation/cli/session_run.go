package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"golang.org/x/xerrors"

	"github.com/duck8823/traceary/application"
	"github.com/duck8823/traceary/application/usecase"
	"github.com/duck8823/traceary/domain/types"
)

const (
	oneShotTimeoutExitCode = 124
	oneShotStartExitCode   = 127
	oneShotStreamExitCode  = 74
	oneShotFinalizeTimeout = 5 * time.Second
)

type oneShotExitError struct {
	message  string
	exitCode int
}

func (e oneShotExitError) Error() string { return e.message }
func (e oneShotExitError) ExitCode() int { return e.exitCode }

type sessionRunCommandInput struct {
	dbPath          string
	client          string
	agent           string
	sessionID       string
	workspace       string
	parentSessionID string
	timeout         time.Duration
}

func (c *RootCLI) newSessionRunCommand() *cobra.Command {
	input := sessionRunCommandInput{}
	cmd := &cobra.Command{
		Use:   "run -- <command> [args...]",
		Short: Localize("Run one command as an authoritative one-shot session", "1 個のコマンドを完結型セッションとして実行する"),
		Long: Localize(
			"Run one command under a Traceary-owned one-shot lifecycle. Traceary records exactly one terminal reason after the child exits. Interactive host Stop hooks remain turn boundaries and do not finalize sessions.",
			"1 個のコマンドを Traceary 管理の完結型ライフサイクルで実行します。子プロセス終了後に終了理由を 1 回だけ記録します。対話型ホストの Stop hook はターン境界のままで、セッションを終了しません。",
		),
		Args: cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return c.runSessionOneShot(cmd.Context(), cmd.InOrStdin(), cmd.OutOrStdout(), cmd.ErrOrStderr(), input, args)
		},
		DisableFlagParsing: false,
	}
	cmd.Flags().StringVar(&input.dbPath, "db-path", "", dbPathFlagUsage())
	cmd.Flags().StringVar(&input.client, "client", "", Localize("recording channel", "記録経路"))
	cmd.Flags().StringVar(&input.agent, "agent", "", Localize("actor name", "作業主体"))
	cmd.Flags().StringVar(&input.sessionID, "session-id", "", Localize("session ID (generated when omitted)", "セッション ID (省略時は自動生成)"))
	cmd.Flags().StringVar(&input.workspace, "workspace", "", Localize("auxiliary workspace identifier", "補助的な workspace 識別子"))
	cmd.Flags().StringVar(&input.parentSessionID, "parent-session-id", "", Localize("parent session ID", "親セッション ID"))
	cmd.Flags().DurationVar(&input.timeout, "timeout", 0, Localize("terminate the command after this duration", "この時間を超えたらコマンドを終了する"))
	return cmd
}

func (c *RootCLI) runSessionOneShot(ctx context.Context, stdin io.Reader, stdout, stderr io.Writer, input sessionRunCommandInput, command []string) error {
	if c.storeManagement == nil || c.session == nil {
		return xerrors.New(Localize("one-shot session dependencies are not configured", "完結型セッションの依存関係が設定されていません"))
	}
	if input.timeout < 0 {
		return xerrors.New(Localize("timeout must not be negative", "timeout は負の値にできません"))
	}
	resolvedDBPath, err := resolveDBPath(input.dbPath)
	if err != nil {
		return xerrors.Errorf("%s: %w", Localize("failed to resolve DB path", "DB パスの解決に失敗しました"), err)
	}
	c.applyDatabasePath(resolvedDBPath)
	if err := c.storeManagement.Initialize(ctx); err != nil {
		return xerrors.Errorf("%s: %w", Localize("failed to initialize store", "ストアの初期化に失敗しました"), err)
	}
	client, err := types.ClientFrom(resolveOptionalValue(input.client, "TRACEARY_CLIENT", defaultClientValue))
	if err != nil {
		return xerrors.Errorf("%s: %w", Localize("failed to resolve client", "記録経路を解決できませんでした"), err)
	}
	agent, err := types.AgentFrom(resolveOptionalValue(input.agent, "TRACEARY_AGENT", defaultAgentValue))
	if err != nil {
		return xerrors.Errorf("%s: %w", Localize("failed to resolve agent", "作業主体を解決できませんでした"), err)
	}
	workspace := types.Workspace(resolveWorkspaceValue(ctx, input.workspace))
	startEvent, err := c.session.StartWithRuntimeMode(ctx, client, agent, types.SessionID(strings.TrimSpace(input.sessionID)), workspace, types.SessionID(strings.TrimSpace(input.parentSessionID)), types.RuntimeModeOneShot)
	if err != nil {
		return xerrors.Errorf("%s: %w", Localize("failed to start one-shot session", "完結型セッションを開始できませんでした"), err)
	}

	processStdout := stdout
	codexUsageMode := ""
	claudeUsageMode := ""
	var headlessUsage application.CodexHeadlessUsageStream
	var claudeHeadlessUsage application.ClaudeHeadlessUsageStream
	if isCodexHeadlessUsageCommand(command) && c.codexHeadlessUsage != nil && c.codexUsage != nil {
		codexUsageMode = codexUsageModeHeadless
		headlessUsage = c.codexHeadlessUsage.New(stdout)
		processStdout = headlessUsage
	} else if isClaudeHeadlessUsageCommand(command) && c.claudeHeadlessUsage != nil && c.claudeUsage != nil {
		claudeUsageMode = claudeUsageModeOneShot
		claudeHeadlessUsage = c.claudeHeadlessUsage.New(stdout)
		processStdout = claudeHeadlessUsage
	}
	reason, exitCode, runErr := runOneShotProcess(
		ctx, stdin, processStdout, stderr, command, input.timeout,
		oneShotProcessEnvironment(
			resolvedDBPath, startEvent.SessionID(), types.SessionID(strings.TrimSpace(input.parentSessionID)),
			codexUsageMode, claudeUsageMode,
		),
	)
	summary := "one-shot process finished: " + reason.String()
	finalizeCtx, cancelFinalize := context.WithTimeout(context.WithoutCancel(ctx), oneShotFinalizeTimeout)
	defer cancelFinalize()
	var usageErr error
	if headlessUsage != nil {
		loaded, collectErr := headlessUsage.Complete()
		_, captureErr := c.codexUsage.CaptureHeadless(finalizeCtx, usecase.CodexUsageCaptureInput{
			SessionID: startEvent.SessionID(), DeliveryID: "session_run",
			FallbackSourceName: "headless_stream", FallbackTerminal: codexUsageTerminal(reason),
		}, loaded)
		if collectErr != nil {
			usageErr = xerrors.Errorf("failed to decode body-free Codex headless usage: %w", collectErr)
		}
		if captureErr != nil {
			usageErr = xerrors.Errorf("failed to record Codex headless usage: %w", captureErr)
		}
	}
	if claudeHeadlessUsage != nil {
		loaded, collectErr := claudeHeadlessUsage.Complete()
		_, captureErr := c.claudeUsage.CaptureHeadless(finalizeCtx, usecase.ClaudeUsageCaptureInput{
			SessionID: startEvent.SessionID(), DeliveryID: "session_run",
			FallbackSourceName: "one_shot_stream", FallbackTerminal: codexUsageTerminal(reason),
		}, loaded)
		if collectErr != nil {
			usageErr = xerrors.Errorf("failed to decode body-free Claude headless usage: %w", collectErr)
		}
		if captureErr != nil {
			usageErr = xerrors.Errorf("failed to record Claude headless usage: %w", captureErr)
		}
	}
	if _, _, finalizeErr := c.session.FinalizeOneShot(finalizeCtx, client, agent, startEvent.SessionID(), workspace, reason, summary); finalizeErr != nil {
		if exitCode == 0 {
			exitCode = 1
		}
		return oneShotExitError{message: fmt.Sprintf("%s: %v", Localize("failed to finalize one-shot session", "完結型セッションを終了できませんでした"), finalizeErr), exitCode: exitCode}
	}
	if runErr != nil {
		return oneShotExitError{message: fmt.Sprintf("%s: %v", Localize("one-shot command failed", "完結型コマンドが失敗しました"), runErr), exitCode: exitCode}
	}
	if usageErr != nil {
		return oneShotExitError{message: fmt.Sprintf("%s: %v", Localize("one-shot usage capture failed", "完結型 usage の記録に失敗しました"), usageErr), exitCode: 1}
	}
	return nil
}

func runOneShotProcess(ctx context.Context, stdin io.Reader, stdout, stderr io.Writer, command []string, timeout time.Duration, env []string) (types.TerminalReason, int, error) {
	runCtx := ctx
	cancel := func() {}
	if timeout > 0 {
		runCtx, cancel = context.WithTimeout(ctx, timeout)
	}
	defer cancel()

	child := exec.CommandContext(runCtx, command[0], command[1:]...)
	child.Stdin = stdin
	child.Stdout = stdout
	child.Stderr = stderr
	child.Env = env
	configureOneShotProcess(child)
	err := child.Run()
	if errors.Is(runCtx.Err(), context.DeadlineExceeded) {
		return types.TerminalReasonTimeout, oneShotTimeoutExitCode, xerrors.Errorf("one-shot process deadline: %w", runCtx.Err())
	}
	if errors.Is(runCtx.Err(), context.Canceled) {
		return types.TerminalReasonAbortedStream, oneShotStreamExitCode, xerrors.Errorf("one-shot process canceled: %w", runCtx.Err())
	}
	if err == nil {
		return types.TerminalReasonSuccess, 0, nil
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		if exitCode, signaled := oneShotSignalExitCode(exitErr); signaled {
			return types.TerminalReasonSignal, exitCode, xerrors.Errorf("one-shot process terminated by signal: %w", err)
		}
		return types.TerminalReasonFailure, exitErr.ExitCode(), err
	}
	var pathErr *os.PathError
	if errors.As(err, &pathErr) {
		return types.TerminalReasonFailure, oneShotStartExitCode, xerrors.Errorf("failed to start one-shot process: %w", err)
	}
	return types.TerminalReasonAbortedStream, oneShotStreamExitCode, xerrors.Errorf("one-shot process stream aborted: %w", err)
}

func oneShotProcessEnvironment(
	dbPath string,
	sessionID, parentSessionID types.SessionID,
	codexUsageMode, claudeUsageMode string,
) []string {
	overrides := map[string]string{
		"TRACEARY_DB_PATH":           dbPath,
		runtimeModeEnvKey:            types.RuntimeModeOneShot.String(),
		runtimeSessionIDEnvKey:       sessionID.String(),
		"TRACEARY_PARENT_SESSION_ID": parentSessionID.String(),
		codexUsageModeEnvKey:         codexUsageMode,
		claudeUsageModeEnvKey:        claudeUsageMode,
	}
	env := make([]string, 0, len(os.Environ())+len(overrides))
	for _, entry := range os.Environ() {
		key, _, found := strings.Cut(entry, "=")
		if _, replace := overrides[key]; found && replace {
			continue
		}
		env = append(env, entry)
	}
	for key, value := range overrides {
		env = append(env, key+"="+value)
	}
	return env
}

func isCodexHeadlessUsageCommand(command []string) bool {
	if len(command) < 2 || filepath.Base(strings.TrimSpace(command[0])) != "codex" || command[1] != "exec" {
		return false
	}
	for _, arg := range command[2:] {
		if arg == "--json" {
			return true
		}
	}
	return false
}

func isClaudeHeadlessUsageCommand(command []string) bool {
	if len(command) < 2 || filepath.Base(strings.TrimSpace(command[0])) != "claude" {
		return false
	}
	printMode := false
	jsonMode := false
	for index := 1; index < len(command); index++ {
		switch command[index] {
		case "-p", "--print":
			printMode = true
		case "--output-format":
			if index+1 < len(command) &&
				(command[index+1] == "json" || command[index+1] == "stream-json") {
				jsonMode = true
			}
		case "--output-format=json", "--output-format=stream-json":
			jsonMode = true
		}
	}
	return printMode && jsonMode
}

func codexUsageTerminal(reason types.TerminalReason) types.UsageTerminalCode {
	switch reason {
	case types.TerminalReasonSuccess:
		return types.UsageTerminalSuccess
	case types.TerminalReasonFailure:
		return types.UsageTerminalFailure
	case types.TerminalReasonTimeout:
		return types.UsageTerminalTimeout
	case types.TerminalReasonSignal:
		return types.UsageTerminalSignal
	case types.TerminalReasonAbortedStream:
		return types.UsageTerminalAbortedStream
	default:
		return types.UsageTerminalUnknown
	}
}
