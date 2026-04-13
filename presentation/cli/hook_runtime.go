package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/spf13/cobra"
	"golang.org/x/xerrors"

	apptypes "github.com/duck8823/traceary/application/types"
	"github.com/duck8823/traceary/domain/types"
)

const hookStateDirEnvKey = "TRACEARY_HOOK_STATE_DIR"

type hookParentProcessInfo struct {
	parentPID int
	command   string
}

var hookParentProcessLookup = defaultHookParentProcessLookup

func (c *RootCLI) newHookCommand() *cobra.Command {
	hookCmd := &cobra.Command{
		Use:    "hook",
		Short:  "Runtime entrypoints used by packaged Traceary hooks",
		Hidden: true,
	}
	hookCmd.AddCommand(c.newHookSessionCommand())
	hookCmd.AddCommand(c.newHookAuditCommand())
	hookCmd.AddCommand(c.newHookCompactCommand())
	hookCmd.AddCommand(c.newHookPromptCommand())

	return hookCmd
}

func (c *RootCLI) newHookSessionCommand() *cobra.Command {
	var dbPath string

	cmd := &cobra.Command{
		Use:    "session <client> <start|end|stop>",
		Short:  "Record hook-driven session lifecycle events",
		Hidden: true,
		Args:   exactArgsLocalized(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runHookBestEffort("session", func() error {
				return c.runHookSession(cmd.Context(), cmd.OutOrStdout(), cmd.InOrStdin(), args[0], args[1], dbPath)
			})
		},
	}
	cmd.Flags().StringVar(&dbPath, "db-path", "", dbPathFlagUsage())

	return cmd
}

func (c *RootCLI) newHookAuditCommand() *cobra.Command {
	var dbPath string

	cmd := &cobra.Command{
		Use:    "audit <client>",
		Short:  "Record hook-driven command audits",
		Hidden: true,
		Args:   exactArgsLocalized(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runHookBestEffort("audit", func() error {
				return c.runHookAudit(cmd.Context(), cmd.InOrStdin(), args[0], dbPath)
			})
		},
	}
	cmd.Flags().StringVar(&dbPath, "db-path", "", dbPathFlagUsage())

	return cmd
}

func (c *RootCLI) newHookCompactCommand() *cobra.Command {
	var dbPath string

	cmd := &cobra.Command{
		Use:    "compact <client> <post-compact|session-start-compact>",
		Short:  "Record or emit compact hook state",
		Hidden: true,
		Args:   exactArgsLocalized(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runHookBestEffort("compact", func() error {
				return c.runHookCompact(cmd.Context(), cmd.OutOrStdout(), cmd.InOrStdin(), args[0], args[1], dbPath)
			})
		},
	}
	cmd.Flags().StringVar(&dbPath, "db-path", "", dbPathFlagUsage())

	return cmd
}

func (c *RootCLI) newHookPromptCommand() *cobra.Command {
	var dbPath string

	cmd := &cobra.Command{
		Use:    "prompt <client>",
		Short:  "Record hook-driven prompt capture",
		Hidden: true,
		Args:   exactArgsLocalized(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runHookBestEffort("prompt", func() error {
				return c.runHookPrompt(cmd.Context(), cmd.InOrStdin(), args[0], dbPath)
			})
		},
	}
	cmd.Flags().StringVar(&dbPath, "db-path", "", dbPathFlagUsage())

	return cmd
}

func (c *RootCLI) runHookSession(
	ctx context.Context,
	output io.Writer,
	input io.Reader,
	client string,
	action string,
	dbPath string,
) error {
	if c.storeManagement == nil {
		return xerrors.Errorf("initialize store usecase is not configured")
	}
	if c.session == nil {
		return xerrors.Errorf("record session boundary usecase is not configured")
	}

	if action != "start" && action != "end" && action != "stop" {
		return xerrors.Errorf("unsupported hook session action: %s", action)
	}

	payload, err := readHookPayload(input)
	if err != nil {
		return err
	}
	resolvedDBPath, err := resolveDBPath(dbPath)
	if err != nil {
		return err
	}
	c.applyDatabasePath(resolvedDBPath)
	if err := c.storeManagement.Initialize(ctx); err != nil {
		return xerrors.Errorf("failed to initialize store: %w", err)
	}

	agent, err := resolveHookAgent(client, payload)
	if err != nil {
		return err
	}

	switch action {
	case "start":
		workspace, err := resolveHookWorkspace(ctx, payload, client, false)
		if err != nil {
			return err
		}
		sessionID := types.SessionID(hookPayloadString(payload, "session_id", ""))
		parentSessionID := types.SessionID(strings.TrimSpace(os.Getenv("TRACEARY_PARENT_SESSION_ID")))
		event, err := c.session.Start(ctx, types.Client("hook"), agent, sessionID, workspace, parentSessionID)
		if err != nil {
			return xerrors.Errorf("failed to record hook session start: %w", err)
		}
		if err := writeHookSessionState(client, event.SessionID()); err != nil {
			return err
		}
		if err := clearHookSessionEndMarker(client, event.SessionID()); err != nil {
			return err
		}
		if workspace != "" {
			if err := writeHookWorkspaceState(client, workspace); err != nil {
				return err
			}
		} else if err := clearHookWorkspaceState(client); err != nil {
			return err
		}
		if output != nil {
			if _, err := fmt.Fprintln(output, event.SessionID()); err != nil {
				return xerrors.Errorf("failed to print session ID: %w", err)
			}
		}
		return nil
	case "end", "stop":
		sessionID := types.SessionID(hookPayloadString(payload, "session_id", ""))
		if sessionID == "" {
			sessionID, err = readHookSessionState(client)
			if err != nil {
				return err
			}
		}
		if sessionID == "" {
			return nil
		}
		if alreadyEnded, err := hookSessionEndAlreadyRecorded(client, sessionID); err == nil && alreadyEnded {
			if clearErr := clearHookSessionState(client); clearErr != nil {
				return clearErr
			}
			if clearErr := clearHookWorkspaceState(client); clearErr != nil {
				return clearErr
			}
			return nil
		} else if err != nil {
			return err
		}

		workspace, err := resolveHookWorkspace(ctx, payload, client, true)
		if err != nil {
			return err
		}
		if _, err := c.session.End(ctx, types.Client("hook"), agent, sessionID, workspace, ""); err != nil {
			return xerrors.Errorf("failed to record hook session end: %w", err)
		}
		if err := clearHookSessionState(client); err != nil {
			return err
		}
		if err := clearHookWorkspaceState(client); err != nil {
			return err
		}
		if err := markHookSessionEnded(client, sessionID); err != nil {
			return err
		}
		return nil
	default:
		return xerrors.Errorf("unsupported hook session action: %s", action)
	}
}

func (c *RootCLI) runHookAudit(
	ctx context.Context,
	input io.Reader,
	client string,
	dbPath string,
) error {
	if c.storeManagement == nil {
		return xerrors.Errorf("initialize store usecase is not configured")
	}
	if c.event == nil {
		return xerrors.Errorf("record command audit usecase is not configured")
	}

	payload, err := readHookPayload(input)
	if err != nil {
		return err
	}
	command := hookPayloadString(payload, "tool_input.command", "")
	if command == "" {
		command = hookPayloadString(payload, "tool_name", "")
	}
	if command == "" {
		return nil
	}

	sessionID, err := resolveHookSessionID(payload, client)
	if err != nil {
		return err
	}
	if sessionID == "" {
		return nil
	}
	workspace, err := resolveHookWorkspace(ctx, payload, client, true)
	if err != nil {
		return err
	}
	agent, err := resolveHookAgent(client, payload)
	if err != nil {
		return err
	}
	resolvedDBPath, err := resolveDBPath(dbPath)
	if err != nil {
		return err
	}
	c.applyDatabasePath(resolvedDBPath)
	if err := c.storeManagement.Initialize(ctx); err != nil {
		return xerrors.Errorf("failed to initialize store: %w", err)
	}

	auditInput := hookPayloadString(payload, "tool_input", "{}")
	if auditInput == "" || auditInput == "{}" {
		toolName := hookPayloadString(payload, "tool_name", "")
		if toolName != "" {
			encodedValue, err := marshalStableJSON(map[string]any{"tool_name": toolName})
			if err != nil {
				return err
			}
			auditInput = string(encodedValue)
		}
	}
	auditOutput := hookPayloadString(payload, "tool_response", "")
	if auditOutput == "" {
		auditOutput, err = buildHookFailureOutput(payload)
		if err != nil {
			return err
		}
	}

	redaction := apptypes.NewAuditRedactionBuilder().
		ExtraRedactPatterns(c.extraRedactPatterns).
		Build()
	_, _, err = c.event.Audit(
		ctx,
		command,
		auditInput,
		auditOutput,
		types.Client("hook"),
		agent,
		sessionID,
		workspace,
		hookPayloadExitCode(payload),
		redaction,
	)

	if err != nil {
		return xerrors.Errorf("failed to record hook audit: %w", err)
	}

	return nil
}

func (c *RootCLI) runHookCompact(
	ctx context.Context,
	output io.Writer,
	input io.Reader,
	client string,
	action string,
	dbPath string,
) error {
	if c.storeManagement == nil {
		return xerrors.Errorf("initialize store usecase is not configured")
	}

	payload, err := readHookPayload(input)
	if err != nil {
		return err
	}
	resolvedDBPath, err := resolveDBPath(dbPath)
	if err != nil {
		return err
	}
	c.applyDatabasePath(resolvedDBPath)
	if err := c.storeManagement.Initialize(ctx); err != nil {
		return xerrors.Errorf("failed to initialize store: %w", err)
	}

	sessionID, err := resolveHookSessionID(payload, client)
	if err != nil {
		return err
	}

	switch action {
	case "post-compact":
		if c.event == nil || sessionID == "" {
			return nil
		}
		workspace, err := resolveHookWorkspace(ctx, payload, client, true)
		if err != nil {
			return err
		}
		agent, err := resolveHookAgent(client, payload)
		if err != nil {
			return err
		}
		compactSummary := hookPayloadString(payload, "compact_summary", "")
		body := compactSummary
		kind := types.EventKindCompactSummary
		if body == "" {
			body = "compact triggered"
			kind = types.EventKind("")
		}
		_, err = c.event.Log(ctx, body, kind, types.Client("hook"), agent, sessionID, workspace)
		if err != nil {
			return xerrors.Errorf("failed to record compact hook event: %w", err)
		}

		return nil
	case "session-start-compact":
		if output == nil {
			return nil
		}
		return c.printCompactSummary(ctx, output, resolvedDBPath, sessionID.String(), "", 3)
	default:
		return xerrors.Errorf("unsupported hook compact action: %s", action)
	}
}

func (c *RootCLI) runHookPrompt(
	ctx context.Context,
	input io.Reader,
	client string,
	dbPath string,
) error {
	if c.storeManagement == nil {
		return xerrors.Errorf("initialize store usecase is not configured")
	}
	if c.event == nil {
		return xerrors.Errorf("record log usecase is not configured")
	}

	payload, err := readHookPayload(input)
	if err != nil {
		return err
	}
	promptText := hookPayloadString(payload, "prompt", "")
	if promptText == "" {
		return nil
	}
	sessionID, err := resolveHookSessionID(payload, client)
	if err != nil {
		return err
	}
	if sessionID == "" {
		return nil
	}
	workspace, err := resolveHookWorkspace(ctx, payload, client, true)
	if err != nil {
		return err
	}
	agent, err := resolveHookAgent(client, payload)
	if err != nil {
		return err
	}
	resolvedDBPath, err := resolveDBPath(dbPath)
	if err != nil {
		return err
	}
	c.applyDatabasePath(resolvedDBPath)
	if err := c.storeManagement.Initialize(ctx); err != nil {
		return xerrors.Errorf("failed to initialize store: %w", err)
	}
	_, err = c.event.Log(ctx, promptText, types.EventKindPrompt, types.Client("hook"), agent, sessionID, workspace)

	if err != nil {
		return xerrors.Errorf("failed to record hook prompt: %w", err)
	}

	return nil
}

func resolveHookAgent(client string, payload []byte) (types.Agent, error) {
	agentType := hookPayloadString(payload, "agent_type", "")
	agentValue := strings.TrimSpace(client)
	if agentType != "" {
		agentValue += "/" + agentType
	}

	agent, err := types.AgentOf(agentValue)
	if err != nil {
		return "", xerrors.Errorf("failed to resolve hook agent: %w", err)
	}

	return agent, nil
}

func resolveHookSessionID(payload []byte, client string) (types.SessionID, error) {
	sessionID := types.SessionID(hookPayloadString(payload, "session_id", ""))
	if sessionID != "" {
		return sessionID, nil
	}

	return readHookSessionState(client)
}

func resolveHookWorkspace(ctx context.Context, payload []byte, client string, preferState bool) (types.Workspace, error) {
	if preferState {
		// Once a session has started, subsequent hook events should stay bound to
		// the persisted workspace so audit/prompt/end events do not drift when the
		// current cwd or explicit override changes mid-session.
		workspace, err := readHookWorkspaceState(client)
		if err != nil {
			return "", err
		}
		if workspace != "" {
			return workspace, nil
		}
	}
	if explicit := strings.TrimSpace(os.Getenv("TRACEARY_WORKSPACE")); explicit != "" {
		return types.Workspace(explicit), nil
	}

	cwd := hookPayloadString(payload, "cwd", "")
	if cwd == "" {
		return "", nil
	}

	workspace, err := detectRepoContextFromDir(ctx, cwd)
	if err == nil {
		return types.Workspace(workspace), nil
	}

	return types.Workspace(normalizeLocalWorkContextPath(cwd)), nil
}

func hookPayloadString(payload []byte, path string, defaultValue string) string {
	if len(payload) == 0 {
		return defaultValue
	}
	value, ok := lookupHookPayloadValue(payload, path)
	if !ok || value == nil {
		return defaultValue
	}
	renderedValue, err := renderHookHelperValue(value)
	if err != nil {
		return defaultValue
	}

	return renderedValue
}

func hookPayloadExitCode(payload []byte) types.Optional[int] {
	if len(payload) == 0 {
		return types.Empty[int]()
	}
	value, ok := lookupHookPayloadValue(payload, "tool_response.exitCode")
	if !ok || value == nil {
		return types.Empty[int]()
	}
	switch typedValue := value.(type) {
	case float64:
		return types.Of(int(typedValue))
	case int:
		return types.Of(typedValue)
	case int64:
		return types.Of(int(typedValue))
	case json.Number:
		parsed, err := typedValue.Int64()
		if err != nil {
			return types.Empty[int]()
		}
		return types.Of(int(parsed))
	case string:
		parsed, err := strconv.Atoi(strings.TrimSpace(typedValue))
		if err != nil {
			return types.Empty[int]()
		}
		return types.Of(parsed)
	default:
		return types.Empty[int]()
	}
}

func buildHookFailureOutput(payload []byte) (string, error) {
	if len(payload) == 0 {
		return "", nil
	}
	result := map[string]any{}
	if errorValue := hookPayloadString(payload, "error", ""); errorValue != "" {
		result["error"] = errorValue
	}
	if interruptValue, ok := lookupHookPayloadValue(payload, "is_interrupt"); ok {
		result["is_interrupt"] = interruptValue
	}
	if len(result) == 0 {
		return "", nil
	}
	encodedValue, err := marshalStableJSON(result)
	if err != nil {
		return "", xerrors.Errorf("failed to marshal failure payload: %w", err)
	}

	return string(encodedValue), nil
}

func resolveHookStateDir() (string, error) {
	if envValue := strings.TrimSpace(os.Getenv(hookStateDirEnvKey)); envValue != "" {
		resolvedPath, err := filepath.Abs(envValue)
		if err != nil {
			return "", xerrors.Errorf("failed to resolve hook state directory: %w", err)
		}

		return resolvedPath, nil
	}

	homeDir, err := userHomeDirFunc()
	if err != nil {
		return "", xerrors.Errorf("failed to get user home directory: %w", err)
	}

	return filepath.Join(homeDir, ".config", "traceary", "hooks"), nil
}

func resolveHookStateKey() string {
	if explicit := strings.TrimSpace(os.Getenv("TRACEARY_HOOK_STATE_KEY")); explicit != "" {
		return sanitizeHookStateKey(explicit)
	}
	if parentPID := os.Getppid(); parentPID > 0 {
		if processInfo, err := hookParentProcessLookup(parentPID); err == nil {
			if processInfo.parentPID > 0 && looksLikeHookWrapperProcess(processInfo.command) {
				return sanitizeHookStateKey(strconv.Itoa(processInfo.parentPID))
			}
		}
		return sanitizeHookStateKey(strconv.Itoa(parentPID))
	}

	return sanitizeHookStateKey(strconv.Itoa(os.Getpid()))
}

func defaultHookParentProcessLookup(pid int) (hookParentProcessInfo, error) {
	output, err := exec.Command("ps", "-o", "ppid=,comm=", "-p", strconv.Itoa(pid)).Output()
	if err != nil {
		return hookParentProcessInfo{}, xerrors.Errorf("failed to resolve hook parent process: %w", err)
	}
	fields := strings.Fields(strings.TrimSpace(string(output)))
	if len(fields) == 0 {
		return hookParentProcessInfo{}, nil
	}
	parentPID, err := strconv.Atoi(fields[0])
	if err != nil {
		return hookParentProcessInfo{}, xerrors.Errorf("failed to parse hook parent process ID: %w", err)
	}
	command := ""
	if len(fields) > 1 {
		command = fields[1]
	}
	return hookParentProcessInfo{parentPID: parentPID, command: command}, nil
}

func looksLikeHookWrapperProcess(command string) bool {
	baseName := filepath.Base(strings.TrimSpace(command))
	switch baseName {
	case "sh", "bash", "zsh", "dash":
		return true
	default:
		return false
	}
}

func runHookBestEffort(commandName string, run func() error) error {
	if err := run(); err != nil {
		if debugEnabled := strings.TrimSpace(os.Getenv("TRACEARY_HOOK_DEBUG")); debugEnabled != "" {
			_, _ = fmt.Fprintf(os.Stderr, "traceary hook %s suppressed error: %v\n", commandName, err)
		}
		return nil
	}
	return nil
}

func sanitizeHookStateKey(value string) string {
	var builder strings.Builder
	for _, r := range value {
		switch {
		case r >= 'A' && r <= 'Z':
			builder.WriteRune(r)
		case r >= 'a' && r <= 'z':
			builder.WriteRune(r)
		case r >= '0' && r <= '9':
			builder.WriteRune(r)
		case r == '.' || r == '_' || r == '-':
			builder.WriteRune(r)
		default:
			builder.WriteRune('_')
		}
	}
	if builder.Len() == 0 {
		return "default"
	}

	return builder.String()
}

func hookSessionStatePath(client string) (string, error) {
	stateDir, err := resolveHookStateDir()
	if err != nil {
		return "", err
	}

	return filepath.Join(stateDir, client+"-"+resolveHookStateKey()), nil
}

func hookWorkspaceStatePath(client string) (string, error) {
	statePath, err := hookSessionStatePath(client)
	if err != nil {
		return "", err
	}

	return statePath + "-repo", nil
}

func hookSessionEndMarkerPath(client string, sessionID types.SessionID) (string, error) {
	stateDir, err := resolveHookStateDir()
	if err != nil {
		return "", err
	}
	sanitizedSessionID := sanitizeHookStateKey(sessionID.String())

	return filepath.Join(stateDir, "ended", client+"-"+sanitizedSessionID), nil
}

func writeHookSessionState(client string, sessionID types.SessionID) error {
	statePath, err := hookSessionStatePath(client)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(statePath), 0o755); err != nil {
		return xerrors.Errorf("failed to create hook state directory: %w", err)
	}

	if err := os.WriteFile(statePath, []byte(sessionID), 0o600); err != nil {
		return xerrors.Errorf("failed to write hook session state: %w", err)
	}

	return nil
}

func readHookSessionState(client string) (types.SessionID, error) {
	statePath, err := hookSessionStatePath(client)
	if err != nil {
		return "", err
	}
	data, err := os.ReadFile(statePath)
	if err != nil {
		if os.IsNotExist(err) {
			return "", nil
		}
		return "", xerrors.Errorf("failed to read hook session state: %w", err)
	}

	return types.SessionID(strings.TrimSpace(string(data))), nil
}

func clearHookSessionState(client string) error {
	statePath, err := hookSessionStatePath(client)
	if err != nil {
		return err
	}
	if err := os.Remove(statePath); err != nil && !os.IsNotExist(err) {
		return xerrors.Errorf("failed to clear hook session state: %w", err)
	}

	return nil
}

func writeHookWorkspaceState(client string, workspace types.Workspace) error {
	statePath, err := hookWorkspaceStatePath(client)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(statePath), 0o755); err != nil {
		return xerrors.Errorf("failed to create hook state directory: %w", err)
	}

	if err := os.WriteFile(statePath, []byte(workspace), 0o600); err != nil {
		return xerrors.Errorf("failed to write hook workspace state: %w", err)
	}

	return nil
}

func readHookWorkspaceState(client string) (types.Workspace, error) {
	statePath, err := hookWorkspaceStatePath(client)
	if err != nil {
		return "", err
	}
	data, err := os.ReadFile(statePath)
	if err != nil {
		if os.IsNotExist(err) {
			return "", nil
		}
		return "", xerrors.Errorf("failed to read hook workspace state: %w", err)
	}

	return types.Workspace(strings.TrimSpace(string(data))), nil
}

func clearHookWorkspaceState(client string) error {
	statePath, err := hookWorkspaceStatePath(client)
	if err != nil {
		return err
	}
	if err := os.Remove(statePath); err != nil && !os.IsNotExist(err) {
		return xerrors.Errorf("failed to clear hook workspace state: %w", err)
	}

	return nil
}

func hookSessionEndAlreadyRecorded(client string, sessionID types.SessionID) (bool, error) {
	markerPath, err := hookSessionEndMarkerPath(client, sessionID)
	if err != nil {
		return false, err
	}
	_, err = os.Stat(markerPath)
	if err == nil {
		return true, nil
	}
	if os.IsNotExist(err) {
		return false, nil
	}

	return false, xerrors.Errorf("failed to inspect hook end marker: %w", err)
}

func markHookSessionEnded(client string, sessionID types.SessionID) error {
	markerPath, err := hookSessionEndMarkerPath(client, sessionID)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(markerPath), 0o755); err != nil {
		return xerrors.Errorf("failed to create hook end-marker directory: %w", err)
	}

	if err := os.WriteFile(markerPath, []byte{}, 0o600); err != nil {
		return xerrors.Errorf("failed to write hook end marker: %w", err)
	}

	return nil
}

func clearHookSessionEndMarker(client string, sessionID types.SessionID) error {
	markerPath, err := hookSessionEndMarkerPath(client, sessionID)
	if err != nil {
		return err
	}
	if err := os.Remove(markerPath); err != nil && !os.IsNotExist(err) {
		return xerrors.Errorf("failed to clear hook end marker: %w", err)
	}

	return nil
}
