package cli

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"golang.org/x/xerrors"

	apptypes "github.com/duck8823/traceary/application/types"
	"github.com/duck8823/traceary/domain/types"
)

const hookStateDirEnvKey = "TRACEARY_HOOK_STATE_DIR"

const hookActiveSubagentStateTTL = 24 * time.Hour

type hookParentProcessInfo struct {
	parentPID int
	command   string
}

type hookActiveSubagentState struct {
	Children map[string]hookActiveSubagentChild `json:"children"`
}

type hookActiveSubagentChild struct {
	ChildSessionID types.SessionID `json:"child_session_id"`
	StartedAt      time.Time       `json:"started_at"`
}

type hookResolvedActiveSubagent struct {
	ParentSessionID types.SessionID
	ToolUseID       string
	ChildSessionID  types.SessionID
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
	hookCmd.AddCommand(c.newHookSubagentStartCommand())
	hookCmd.AddCommand(c.newHookSubagentStopCommand())
	hookCmd.AddCommand(c.newHookPromptCommand())
	hookCmd.AddCommand(c.newHookTranscriptCommand())

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
		Use:    "compact <client> <pre-compact|post-compact|session-start-compact>",
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

func (c *RootCLI) newHookSubagentStartCommand() *cobra.Command {
	var dbPath string

	cmd := &cobra.Command{
		Use:    "subagent-start <client>",
		Short:  "Record a subagent-start boundary event",
		Hidden: true,
		Args:   exactArgsLocalized(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runHookBestEffort("subagent-start", func() error {
				return c.runHookSubagentStart(cmd.Context(), cmd.InOrStdin(), args[0], dbPath)
			})
		},
	}
	cmd.Flags().StringVar(&dbPath, "db-path", "", dbPathFlagUsage())

	return cmd
}

func (c *RootCLI) newHookSubagentStopCommand() *cobra.Command {
	var dbPath string

	cmd := &cobra.Command{
		Use:    "subagent-stop <client>",
		Short:  "Record a subagent-stop boundary event",
		Hidden: true,
		Args:   exactArgsLocalized(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runHookBestEffort("subagent-stop", func() error {
				return c.runHookSubagentStop(cmd.Context(), cmd.InOrStdin(), args[0], dbPath)
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

func (c *RootCLI) newHookTranscriptCommand() *cobra.Command {
	var dbPath string

	cmd := &cobra.Command{
		Use:    "transcript <client>",
		Short:  "Record the last assistant message from a Stop-hook transcript_path",
		Hidden: true,
		Args:   exactArgsLocalized(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runHookBestEffort("transcript", func() error {
				return c.runHookTranscript(cmd.Context(), cmd.InOrStdin(), args[0], dbPath)
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

	// Tag downstream events with which host hook fired so
	// retrospective queries can tell a Claude SessionEnd apart from
	// a Codex Stop even though both produce a session_ended row
	// (#672). start / end map to session_start / session_end; stop
	// is Codex's session-end-equivalent.
	switch action {
	case "start":
		ctx = apptypes.WithSourceHook(ctx, "session_start")
	case "end":
		ctx = apptypes.WithSourceHook(ctx, "session_end")
	case "stop":
		ctx = apptypes.WithSourceHook(ctx, "stop")
	}

	payload, err := readHookPayload(input)
	if err != nil {
		return err
	}

	switch action {
	case "start":
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
		if err := clearHookActiveSubagentState(client, event.SessionID(), ""); err != nil {
			return err
		}
		if err := cleanupHookActiveSubagentStates(client); err != nil {
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
		agent, err := resolveHookAgent(client, payload)
		if err != nil {
			return err
		}
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

		resolvedDBPath, err := resolveDBPath(dbPath)
		if err != nil {
			return err
		}
		c.applyDatabasePath(resolvedDBPath)
		if err := c.storeManagement.Initialize(ctx); err != nil {
			return xerrors.Errorf("failed to initialize store: %w", err)
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
		if err := clearHookActiveSubagentState(client, sessionID, ""); err != nil {
			return err
		}
		if err := cleanupHookActiveSubagentStates(client); err != nil {
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
	// Tag for #672: per-client audit is PostToolUse on Claude/Codex
	// and AfterTool on Gemini. The hook runtime does not yet know
	// whether Claude Code dispatched this via PostToolUse or the
	// PostToolUseFailure variant — both go through the same handler,
	// so we stamp the coarser "post_tool_use" for now; the exit-code
	// distinction remains recoverable via the command_audits table.
	if client == "gemini" {
		ctx = apptypes.WithSourceHook(ctx, "after_tool")
	} else {
		ctx = apptypes.WithSourceHook(ctx, "post_tool_use")
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

	auditCfg := apptypes.NewAuditRedactionBuilder().
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
		auditCfg,
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
	// Tag for #672: PreCompact and PostCompact both produce the
	// compact_summary kind, so source_hook carries the phase
	// without relying on body-prefix marker parsing. The
	// session-start-compact branch writes no event so tagging
	// there is a no-op.
	switch action {
	case "pre-compact":
		ctx = apptypes.WithSourceHook(ctx, "pre_compact")
	case "post-compact":
		ctx = apptypes.WithSourceHook(ctx, "post_compact")
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
		_, err = c.event.Log(ctx, body, kind, types.Client("hook"), agent, sessionID, workspace, apptypes.LogRedaction{})
		if err != nil {
			return xerrors.Errorf("failed to record compact hook event: %w", err)
		}

		return nil
	case "pre-compact":
		// Claude Code 2026-01+ fires PreCompact before context is compacted.
		// Record the snapshot as a compact_summary with a `phase:pre` body
		// marker so replay / retrospective surfaces can tell the
		// before-compact snapshot apart from the post-compact digest.
		// The context_pack_builder's post-compact loader filters on body
		// prefix so the retrospective/handoff path only picks up the
		// post-compact summary, not this pre-compact snapshot.
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
		preContext := hookPayloadString(payload, "pre_compact_context", "")
		if preContext == "" {
			preContext = hookPayloadString(payload, "trigger", "")
		}
		// source_hook = "pre_compact" (stamped via ctx) now discriminates
		// the snapshot from post-compact digests. We intentionally drop
		// the legacy `[phase:pre-compact]` body prefix: readers fall back
		// to the marker only for pre-#672 rows that lack source_hook.
		_, err = c.event.Log(ctx, preContext, types.EventKindCompactSummary, types.Client("hook"), agent, sessionID, workspace, apptypes.LogRedaction{})
		if err != nil {
			return xerrors.Errorf("failed to record pre-compact hook event: %w", err)
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

func (c *RootCLI) runHookSubagentStart(
	ctx context.Context,
	input io.Reader,
	client string,
	dbPath string,
) error {
	if c.storeManagement == nil {
		return xerrors.Errorf("initialize store usecase is not configured")
	}
	if c.session == nil {
		return xerrors.Errorf("session usecase is not configured")
	}
	ctx = apptypes.WithSourceHook(ctx, "pre_tool_use")

	payload, err := readHookPayload(input)
	if err != nil {
		return err
	}
	parentSessionID, err := resolveHookSubagentStartParentSessionID(payload, client)
	if err != nil {
		return err
	}
	if parentSessionID == "" {
		return nil
	}
	toolUseID := resolveHookToolUseID(payload)
	if toolUseID == "" {
		return nil
	}
	subagentType := hookPayloadString(payload, "subagent_type", "")
	if subagentType == "" {
		subagentType = hookPayloadString(payload, "tool_input.subagent_type", "")
	}
	if subagentType == "" {
		subagentType = "task"
	}
	agent, err := types.AgentFrom(strings.TrimSpace(client) + "/" + subagentType)
	if err != nil {
		return xerrors.Errorf("failed to resolve subagent agent: %w", err)
	}
	workspace, err := resolveHookWorkspace(ctx, payload, client, true)
	if err != nil {
		return err
	}

	resolvedDBPath, err := resolveDBPath(dbPath)
	if err != nil {
		return xerrors.Errorf("failed to resolve DB path: %w", err)
	}
	c.applyDatabasePath(resolvedDBPath)
	if err := c.storeManagement.Initialize(ctx); err != nil {
		return xerrors.Errorf("failed to initialize store: %w", err)
	}

	childSessionID := synthesizeHookChildSessionID(parentSessionID, toolUseID)
	if _, err := c.session.StartChild(ctx, parentSessionID, childSessionID, agent, workspace, types.EventID(toolUseID), "task", time.Now()); err != nil {
		return xerrors.Errorf("failed to record subagent start: %w", err)
	}
	if err := writeHookActiveSubagentState(client, parentSessionID, toolUseID, childSessionID); err != nil {
		return err
	}
	return nil
}

func (c *RootCLI) runHookSubagentStop(
	ctx context.Context,
	input io.Reader,
	client string,
	dbPath string,
) error {
	if c.storeManagement == nil {
		return xerrors.Errorf("initialize store usecase is not configured")
	}
	if c.event == nil {
		return xerrors.Errorf("event usecase is not configured")
	}
	if c.session == nil {
		return xerrors.Errorf("session usecase is not configured")
	}
	// Tag for #672: SubagentStop produces a session_ended event
	// (distinguished today only via the `[phase:subagent]` body
	// prefix). source_hook lets downstream queries filter without
	// parsing the body string.
	ctx = apptypes.WithSourceHook(ctx, "subagent_stop")
	payload, err := readHookPayload(input)
	if err != nil {
		return err
	}
	resolvedDBPath, err := resolveDBPath(dbPath)
	if err != nil {
		return xerrors.Errorf("failed to resolve DB path: %w", err)
	}
	c.applyDatabasePath(resolvedDBPath)
	if err := c.storeManagement.Initialize(ctx); err != nil {
		return xerrors.Errorf("failed to initialize store: %w", err)
	}
	parentSessionID, err := resolveHookParentSessionID(payload, client)
	if err != nil {
		return err
	}
	if parentSessionID == "" {
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
	toolUseID := resolveHookToolUseID(payload)
	childSessionID := types.SessionID("")
	activeToolUseID := ""
	activeParentSessionID := types.SessionID("")
	childWasActive := false
	if toolUseID != "" {
		active, findErr := findHookActiveSubagentStateForTool(client, parentSessionID, toolUseID)
		if findErr != nil {
			return findErr
		}
		childSessionID = active.ChildSessionID
		activeParentSessionID = active.ParentSessionID
		childWasActive = childSessionID != ""
		if activeParentSessionID == "" {
			activeParentSessionID = parentSessionID
		}
		if childSessionID == "" {
			childSessionID = synthesizeHookChildSessionID(parentSessionID, toolUseID)
		}
		activeToolUseID = toolUseID
	} else {
		active, findErr := findHookDeepestLatestActiveSubagentState(client, parentSessionID)
		if findErr != nil {
			return findErr
		}
		activeToolUseID = active.ToolUseID
		childSessionID = active.ChildSessionID
		activeParentSessionID = active.ParentSessionID
		childWasActive = childSessionID != ""
	}
	if childSessionID != "" {
		if !childWasActive {
			childAgent := agent
			if subagentType := hookPayloadString(payload, "subagent_type", ""); subagentType != "" {
				if resolvedAgent, resolveErr := types.AgentFrom(strings.TrimSpace(client) + "/" + subagentType); resolveErr == nil {
					childAgent = resolvedAgent
				}
			}
			if _, startErr := c.session.StartChild(ctx, parentSessionID, childSessionID, childAgent, workspace, types.EventID(toolUseID), "task", time.Now()); startErr != nil {
				return xerrors.Errorf("failed to synthesize missing subagent start: %w", startErr)
			}
		}
		if _, err := c.session.End(ctx, types.Client("hook"), types.Agent(""), childSessionID, workspace, ""); err != nil {
			return xerrors.Errorf("failed to end subagent session: %w", err)
		}
		if activeParentSessionID != "" {
			parentSessionID = activeParentSessionID
		}
		if err := clearHookActiveSubagentState(client, parentSessionID, activeToolUseID); err != nil {
			return err
		}
	}
	// Claude Code's SubagentStop hook fires whenever a Task-tool subagent
	// completes. source_hook = "subagent_stop" (stamped via ctx) now
	// discriminates the subagent boundary from main-session session_ended
	// events; the legacy `[phase:subagent]` body prefix is retired on
	// write but readers still match it for pre-#672 rows.
	subagentType := hookPayloadString(payload, "subagent_type", "")
	_, err = c.event.Log(ctx, subagentType, types.EventKindSessionEnded, types.Client("hook"), agent, parentSessionID, workspace, apptypes.LogRedaction{})
	if err != nil {
		return xerrors.Errorf("failed to record subagent-stop event: %w", err)
	}
	return nil
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
	ctx = apptypes.WithSourceHook(ctx, "user_prompt_submit")

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
	_, err = c.event.Log(ctx, promptText, types.EventKindPrompt, types.Client("hook"), agent, sessionID, workspace, apptypes.LogRedaction{})

	if err != nil {
		return xerrors.Errorf("failed to record hook prompt: %w", err)
	}

	return nil
}

// runHookTranscript records the last assistant-message turn as a
// `transcript` event. It reads Stop-hook stdin to find
// the last assistant turn and stores it as a `transcript` event. The
// exact extraction strategy is client-specific:
//
//   - Claude Code delivers a `transcript_path` pointing at a JSONL
//     file maintained by the host. We read the final assistant turn's
//     `text` and `thinking` blocks; tool-use blocks are excluded
//     because they are already captured by `command_executed` audits.
//   - Codex CLI's Stop event carries the final turn verbatim via
//     `last_assistant_message`, so there is no file to parse.
//   - Gemini CLI's AfterAgent event carries the final turn via
//     `prompt_response`, again as a plain string.
//
// All paths share the same redaction policy and event kind, so the
// downstream consumers (`traceary tail`, `replay`, `get_context`)
// cannot distinguish between clients except by the recorded agent.
// If the host payload is missing or empty we fail soft — transcript
// capture is a nice-to-have, not a requirement for sessions to close
// cleanly.
func (c *RootCLI) runHookTranscript(
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
	// Tag for #672: transcript comes from Claude / Codex Stop or
	// Gemini AfterAgent. Downstream readers can distinguish via
	// source_hook without parsing the client's hook envelope.
	if client == "gemini" {
		ctx = apptypes.WithSourceHook(ctx, "after_agent")
	} else {
		ctx = apptypes.WithSourceHook(ctx, "stop")
	}

	payload, err := readHookPayload(input)
	if err != nil {
		return err
	}
	extractor, ok := transcriptExtractorFor(client)
	if !ok {
		// Unknown client — silently skip so a packaged hook invoking an
		// unsupported client never aborts the host's Stop / SessionEnd
		// hook. New clients must register an extractor in
		// `transcriptExtractorFor` before their hook is wired.
		return nil
	}
	blocks, ok := extractor(payload)
	if !ok || len(blocks) == 0 {
		return nil
	}
	// Serialize the structured blocks into the canonical JSON
	// envelope Traceary persists for kind=transcript bodies. Readers
	// that consume blocks (replay, future memory extraction) get the
	// preserved thinking/text boundaries; legacy readers fall back
	// through apptypes.ExtractPlainBody.
	body, err := apptypes.MarshalEventBodyBlocks(blocks)
	if err != nil {
		return xerrors.Errorf("failed to serialize transcript blocks: %w", err)
	}
	// Transcript bodies can echo secrets the assistant saw earlier in
	// the turn (API keys from .env, Bearer tokens from header dumps,
	// private keys pasted into chat). Hand the operator-configured
	// extra_redact_patterns to EventUsecase.Log; the usecase parses
	// the JSON envelope, applies built-in + extra redactors to each
	// block's text field, and re-serializes — so the structure stays
	// intact for block-aware downstream readers.
	logCfg := apptypes.NewLogRedactionBuilder().
		ExtraRedactPatterns(c.extraRedactPatterns).
		Build()

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
	if _, err := c.event.Log(ctx, body, types.EventKindTranscript, types.Client("hook"), agent, sessionID, workspace, logCfg); err != nil {
		return xerrors.Errorf("failed to record hook transcript: %w", err)
	}

	return nil
}

// transcriptExtractor derives the assistant-reply content for a
// single transcript hook invocation from the host-supplied stdin
// payload, as a slice of structured blocks (thinking / text).
// Implementations must return ok=false (and a nil slice) when the
// payload carries no usable reply text, so the caller can silently
// skip without logging an empty `transcript` event.
type transcriptExtractor func(payload []byte) ([]apptypes.EventBodyBlock, bool)

// transcriptExtractorFor returns the extractor registered for the
// named client. Clients without a registered extractor silently
// skip — this keeps us forward-compatible with packaged hooks that
// pass unknown client arguments during staged rollouts.
func transcriptExtractorFor(client string) (transcriptExtractor, bool) {
	switch client {
	case "claude":
		return extractClaudeTranscript, true
	case "codex":
		return extractCodexTranscript, true
	case "gemini":
		return extractGeminiTranscript, true
	default:
		return nil, false
	}
}

// extractClaudeTranscript resolves the Claude Code transcript_path
// pointer and reads the final assistant turn from the JSONL file,
// preserving thinking / text block structure so downstream readers
// can split reasoning from rendered reply.
func extractClaudeTranscript(payload []byte) ([]apptypes.EventBodyBlock, bool) {
	transcriptPath := hookPayloadString(payload, "transcript_path", "")
	if transcriptPath == "" {
		return nil, false
	}
	return readLastAssistantTranscriptBlocks(transcriptPath)
}

// extractCodexTranscript reads Codex CLI's `last_assistant_message`
// field from the Stop-hook payload. Codex delivers the final turn
// as a single pre-rendered string (no thinking/text distinction on
// the host side), so we emit one `text` block for parity with the
// Claude / Gemini shapes.
func extractCodexTranscript(payload []byte) ([]apptypes.EventBodyBlock, bool) {
	text := hookPayloadString(payload, "last_assistant_message", "")
	text = strings.TrimSpace(text)
	if text == "" {
		return nil, false
	}
	return []apptypes.EventBodyBlock{{Type: apptypes.EventBodyBlockTypeText, Text: text}}, true
}

// extractGeminiTranscript reads Gemini CLI's `prompt_response` field
// from the AfterAgent-hook payload. Gemini has no Stop event; the
// closest analogue is AfterAgent, which fires once the agent has
// produced a full response and includes the response text inline.
// Gemini renders the response as a single pre-formatted string, so
// the transcript carries a single `text` block — matching the shape
// Claude / Codex expose.
func extractGeminiTranscript(payload []byte) ([]apptypes.EventBodyBlock, bool) {
	text := hookPayloadString(payload, "prompt_response", "")
	text = strings.TrimSpace(text)
	if text == "" {
		return nil, false
	}
	return []apptypes.EventBodyBlock{{Type: apptypes.EventBodyBlockTypeText, Text: text}}, true
}

// readLastAssistantTranscriptBlocks reads the JSONL transcript file
// at path and returns the ordered content blocks of the LAST
// assistant turn.
//
// Real Claude Code transcripts use an envelope shape:
//
//	{"type":"assistant", "message":{"role":"assistant","content":[...]}}
//	{"type":"user",      "message":{"role":"user",     "content":"..."}}
//	{"type":"file-history-snapshot", ...}
//
// Each assistant turn's `message.content` is an array of blocks — we
// keep `type=text` and `type=thinking` blocks (reasoning and extended
// thinking) and drop `type=tool_use` / `type=tool_result` because
// those are already captured by `command_executed` audits. The block
// order and type distinction are preserved so downstream consumers
// can render thinking collapsed / filter reasoning out of memory
// extraction.
//
// Returns ok=false for IO / parse failure so callers can silently
// skip; slog.Debug lines preserve the underlying cause for
// TRACEARY_HOOK_DEBUG-style troubleshooting without aborting the
// host's Stop hook.
func readLastAssistantTranscriptBlocks(path string) ([]apptypes.EventBodyBlock, bool) {
	file, err := os.Open(path) // #nosec G304 -- path supplied by the host Stop hook
	if err != nil {
		slog.Debug("failed to open transcript file", "path", path, "error", err)
		return nil, false
	}
	defer func() { _ = file.Close() }()

	scanner := bufio.NewScanner(file)
	// Transcript entries can carry multi-KB reasoning payloads; lift
	// the default 64KB line limit so long turns don't truncate.
	scanner.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)

	var lastAssistantBlocks []apptypes.EventBodyBlock
	var parseErrors int
	for scanner.Scan() {
		line := bytes.TrimSpace(scanner.Bytes())
		if len(line) == 0 {
			continue
		}
		var entry transcriptLine
		if err := json.Unmarshal(line, &entry); err != nil {
			parseErrors++
			continue
		}
		// Only assistant turns contribute. The envelope carries its
		// own type field; we also verify the inner message.role to
		// avoid mismatched snapshots.
		if entry.Type != "assistant" && entry.Message.Role != "assistant" {
			continue
		}
		blocks := extractAssistantBlocks(entry.Message.Content)
		if len(blocks) == 0 {
			continue
		}
		lastAssistantBlocks = blocks
	}
	if err := scanner.Err(); err != nil {
		slog.Debug("failed while scanning transcript file", "path", path, "error", err)
		return nil, false
	}
	if parseErrors > 0 && len(lastAssistantBlocks) == 0 {
		slog.Debug("transcript file had no parseable assistant entries", "path", path, "parse_errors", parseErrors)
		return nil, false
	}
	return lastAssistantBlocks, len(lastAssistantBlocks) > 0
}

// transcriptLine is one row in Claude Code's JSONL transcript. Only
// the envelope `type` and the nested `message` matter for this
// feature; everything else (timestamps, message-id, snapshots) is
// deliberately ignored.
type transcriptLine struct {
	Type    string            `json:"type"`
	Message transcriptMessage `json:"message"`
}

type transcriptMessage struct {
	Role    string              `json:"role"`
	Content []transcriptContent `json:"content"`
}

// transcriptContent covers both `text` blocks (normal assistant
// reasoning) and `thinking` blocks (extended thinking). Tool-use /
// tool-result blocks are ignored by the extractor.
type transcriptContent struct {
	Type     string `json:"type"`
	Text     string `json:"text"`
	Thinking string `json:"thinking"`
}

// extractAssistantBlocks maps a Claude Code transcript envelope's
// content array to the structured block shape Traceary persists.
// `text` blocks become `text`; `thinking` blocks become `thinking`
// so consumers can distinguish rendered reply from internal
// reasoning. tool_use / tool_result blocks are skipped because they
// are already recorded via PostToolUse / PostToolUseFailure hooks.
func extractAssistantBlocks(blocks []transcriptContent) []apptypes.EventBodyBlock {
	if len(blocks) == 0 {
		return nil
	}
	result := make([]apptypes.EventBodyBlock, 0, len(blocks))
	for _, block := range blocks {
		var blockType apptypes.EventBodyBlockType
		var text string
		switch block.Type {
		case "text":
			blockType = apptypes.EventBodyBlockTypeText
			text = strings.TrimSpace(block.Text)
		case "thinking":
			blockType = apptypes.EventBodyBlockTypeThinking
			text = strings.TrimSpace(block.Thinking)
		default:
			continue
		}
		if text == "" {
			continue
		}
		result = append(result, apptypes.EventBodyBlock{Type: blockType, Text: text})
	}
	return result
}

func resolveHookAgent(client string, payload []byte) (types.Agent, error) {
	agentType := hookPayloadString(payload, "agent_type", "")
	agentValue := strings.TrimSpace(client)
	if agentType != "" {
		agentValue += "/" + agentType
	}

	agent, err := types.AgentFrom(agentValue)
	if err != nil {
		return "", xerrors.Errorf("failed to resolve hook agent: %w", err)
	}

	return agent, nil
}

func resolveHookSessionID(payload []byte, client string) (types.SessionID, error) {
	parentSessionID, err := resolveHookParentSessionID(payload, client)
	if err != nil {
		return "", err
	}
	if parentSessionID == "" {
		return "", nil
	}
	active, err := findHookDeepestLatestActiveSubagentState(client, parentSessionID)
	if err != nil {
		return "", err
	}
	if active.ChildSessionID != "" {
		return active.ChildSessionID, nil
	}

	return parentSessionID, nil
}

func resolveHookSubagentStartParentSessionID(payload []byte, client string) (types.SessionID, error) {
	if hookPayloadString(payload, "session_id", "") == "" {
		return resolveHookParentSessionID(payload, client)
	}
	return resolveHookSessionID(payload, client)
}

func resolveHookParentSessionID(payload []byte, client string) (types.SessionID, error) {
	sessionID := types.SessionID(hookPayloadString(payload, "session_id", ""))
	if sessionID != "" {
		return sessionID, nil
	}
	return readHookSessionState(client)
}

func synthesizeHookChildSessionID(parentSessionID types.SessionID, toolUseID string) types.SessionID {
	return types.SessionID(parentSessionID.String() + ":sub:" + strings.TrimSpace(toolUseID))
}

func resolveHookToolUseID(payload []byte) string {
	if toolUseID := strings.TrimSpace(hookPayloadString(payload, "tool_use_id", "")); toolUseID != "" {
		return toolUseID
	}
	if eventID := strings.TrimSpace(hookPayloadString(payload, "event_id", "")); eventID != "" {
		return "event-" + eventID
	}
	return ""
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
		return types.None[int]()
	}
	value, ok := lookupHookPayloadValue(payload, "tool_response.exitCode")
	if !ok || value == nil {
		return types.None[int]()
	}
	switch typedValue := value.(type) {
	case float64:
		return types.Some(int(typedValue))
	case int:
		return types.Some(typedValue)
	case int64:
		return types.Some(int(typedValue))
	case json.Number:
		parsed, err := typedValue.Int64()
		if err != nil {
			return types.None[int]()
		}
		return types.Some(int(parsed))
	case string:
		parsed, err := strconv.Atoi(strings.TrimSpace(typedValue))
		if err != nil {
			return types.None[int]()
		}
		return types.Some(parsed)
	default:
		return types.None[int]()
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

func hookActiveSubagentStatePath(client string, parentSessionID types.SessionID) (string, error) {
	stateDir, err := resolveHookStateDir()
	if err != nil {
		return "", err
	}
	sanitizedParentSessionID := sanitizeHookStateKey(parentSessionID.String())
	return filepath.Join(stateDir, "active-subagents", client+"-"+sanitizedParentSessionID), nil
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

func withHookActiveSubagentStateLock(client string, parentSessionID types.SessionID, fn func() error) error {
	statePath, err := hookActiveSubagentStatePath(client, parentSessionID)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(statePath), 0o755); err != nil {
		return xerrors.Errorf("failed to create hook active-subagent state directory: %w", err)
	}
	lockPath := statePath + ".lock"
	for i := 0; ; i++ {
		err := os.Mkdir(lockPath, 0o700)
		if err == nil {
			defer func() { _ = os.Remove(lockPath) }()
			return fn()
		}
		if !os.IsExist(err) {
			return xerrors.Errorf("failed to lock hook active-subagent state: %w", err)
		}
		if i >= 100 {
			return xerrors.Errorf("failed to lock hook active-subagent state: timed out")
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func writeHookActiveSubagentState(client string, parentSessionID types.SessionID, toolUseID string, childSessionID types.SessionID) error {
	toolUseID = strings.TrimSpace(toolUseID)
	if toolUseID == "" {
		return nil
	}
	return withHookActiveSubagentStateLock(client, parentSessionID, func() error {
		statePath, err := hookActiveSubagentStatePath(client, parentSessionID)
		if err != nil {
			return err
		}
		state, err := readHookActiveSubagentStateFile(statePath)
		if err != nil {
			return err
		}
		if state.Children == nil {
			state.Children = map[string]hookActiveSubagentChild{}
		}
		state.Children[toolUseID] = hookActiveSubagentChild{
			ChildSessionID: childSessionID,
			StartedAt:      time.Now().UTC(),
		}
		data, err := json.Marshal(state)
		if err != nil {
			return xerrors.Errorf("failed to encode hook active-subagent state: %w", err)
		}
		if err := os.WriteFile(statePath, data, 0o600); err != nil {
			return xerrors.Errorf("failed to write hook active-subagent state: %w", err)
		}
		return nil
	})
}

func readHookActiveSubagentStateFile(statePath string) (hookActiveSubagentState, error) {
	data, err := os.ReadFile(statePath)
	if err != nil {
		if os.IsNotExist(err) {
			return hookActiveSubagentState{Children: map[string]hookActiveSubagentChild{}}, nil
		}
		return hookActiveSubagentState{}, xerrors.Errorf("failed to read hook active-subagent state: %w", err)
	}
	trimmed := strings.TrimSpace(string(data))
	if trimmed == "" {
		return hookActiveSubagentState{Children: map[string]hookActiveSubagentChild{}}, nil
	}
	if !strings.HasPrefix(trimmed, "{") {
		return hookActiveSubagentState{
			Children: map[string]hookActiveSubagentChild{
				"legacy": {
					ChildSessionID: types.SessionID(trimmed),
				},
			},
		}, nil
	}
	var state hookActiveSubagentState
	if err := json.Unmarshal(data, &state); err != nil {
		return hookActiveSubagentState{}, xerrors.Errorf("failed to decode hook active-subagent state: %w", err)
	}
	if state.Children == nil {
		state.Children = map[string]hookActiveSubagentChild{}
	}
	if pruneHookActiveSubagentState(&state, time.Now().UTC()) {
		if len(state.Children) == 0 {
			if err := os.Remove(statePath); err != nil && !os.IsNotExist(err) {
				return hookActiveSubagentState{}, xerrors.Errorf("failed to clear stale hook active-subagent state: %w", err)
			}
		} else {
			data, err := json.Marshal(state)
			if err != nil {
				return hookActiveSubagentState{}, xerrors.Errorf("failed to encode hook active-subagent state: %w", err)
			}
			if err := os.WriteFile(statePath, data, 0o600); err != nil {
				return hookActiveSubagentState{}, xerrors.Errorf("failed to write hook active-subagent state: %w", err)
			}
		}
	}
	return state, nil
}

func pruneHookActiveSubagentState(state *hookActiveSubagentState, now time.Time) bool {
	if state == nil || hookActiveSubagentStateTTL <= 0 {
		return false
	}
	changed := false
	cutoff := now.Add(-hookActiveSubagentStateTTL)
	for toolUseID, child := range state.Children {
		if child.StartedAt.IsZero() {
			continue
		}
		if child.StartedAt.Before(cutoff) {
			delete(state.Children, toolUseID)
			changed = true
		}
	}
	return changed
}

func readHookActiveSubagentStateForTool(client string, parentSessionID types.SessionID, toolUseID string) (types.SessionID, bool, error) {
	statePath, err := hookActiveSubagentStatePath(client, parentSessionID)
	if err != nil {
		return "", false, err
	}
	state, err := readHookActiveSubagentStateFile(statePath)
	if err != nil {
		return "", false, err
	}
	child, ok := state.Children[toolUseID]
	if !ok || child.ChildSessionID == "" {
		return "", false, nil
	}
	return child.ChildSessionID, true, nil
}

func findHookActiveSubagentStateForTool(client string, rootParentSessionID types.SessionID, toolUseID string) (hookResolvedActiveSubagent, error) {
	if strings.TrimSpace(toolUseID) == "" {
		return hookResolvedActiveSubagent{}, nil
	}
	return findHookActiveSubagentStateForToolDepth(client, rootParentSessionID, toolUseID, 0)
}

func findHookActiveSubagentStateForToolDepth(client string, parentSessionID types.SessionID, toolUseID string, depth int) (hookResolvedActiveSubagent, error) {
	if depth >= 32 || parentSessionID == "" {
		return hookResolvedActiveSubagent{}, nil
	}
	childSessionID, ok, err := readHookActiveSubagentStateForTool(client, parentSessionID, toolUseID)
	if err != nil {
		return hookResolvedActiveSubagent{}, err
	}
	if ok {
		return hookResolvedActiveSubagent{
			ParentSessionID: parentSessionID,
			ToolUseID:       toolUseID,
			ChildSessionID:  childSessionID,
		}, nil
	}
	children, err := readHookActiveSubagentChildren(client, parentSessionID)
	if err != nil {
		return hookResolvedActiveSubagent{}, err
	}
	for _, childSessionID := range children {
		found, err := findHookActiveSubagentStateForToolDepth(client, childSessionID, toolUseID, depth+1)
		if err != nil {
			return hookResolvedActiveSubagent{}, err
		}
		if found.ChildSessionID != "" {
			return found, nil
		}
	}
	return hookResolvedActiveSubagent{}, nil
}

func readHookLatestActiveSubagentState(client string, parentSessionID types.SessionID) (string, types.SessionID, error) {
	statePath, err := hookActiveSubagentStatePath(client, parentSessionID)
	if err != nil {
		return "", "", err
	}
	state, err := readHookActiveSubagentStateFile(statePath)
	if err != nil {
		return "", "", err
	}
	var latestToolUseID string
	var latestChildID types.SessionID
	var latestStartedAt time.Time
	for toolUseID, child := range state.Children {
		if child.ChildSessionID == "" {
			continue
		}
		if latestChildID == "" || child.StartedAt.After(latestStartedAt) {
			latestToolUseID = toolUseID
			latestChildID = child.ChildSessionID
			latestStartedAt = child.StartedAt
		}
	}
	return latestToolUseID, latestChildID, nil
}

func readHookActiveSubagentChildren(client string, parentSessionID types.SessionID) ([]types.SessionID, error) {
	statePath, err := hookActiveSubagentStatePath(client, parentSessionID)
	if err != nil {
		return nil, err
	}
	state, err := readHookActiveSubagentStateFile(statePath)
	if err != nil {
		return nil, err
	}
	children := make([]types.SessionID, 0, len(state.Children))
	for _, child := range state.Children {
		if child.ChildSessionID != "" {
			children = append(children, child.ChildSessionID)
		}
	}
	return children, nil
}

func findHookDeepestLatestActiveSubagentState(client string, rootParentSessionID types.SessionID) (hookResolvedActiveSubagent, error) {
	return findHookDeepestLatestActiveSubagentStateDepth(client, rootParentSessionID, 0)
}

func findHookDeepestLatestActiveSubagentStateDepth(client string, parentSessionID types.SessionID, depth int) (hookResolvedActiveSubagent, error) {
	if depth >= 32 || parentSessionID == "" {
		return hookResolvedActiveSubagent{}, nil
	}
	toolUseID, childSessionID, err := readHookLatestActiveSubagentState(client, parentSessionID)
	if err != nil {
		return hookResolvedActiveSubagent{}, err
	}
	if childSessionID == "" {
		return hookResolvedActiveSubagent{}, nil
	}
	deeper, err := findHookDeepestLatestActiveSubagentStateDepth(client, childSessionID, depth+1)
	if err != nil {
		return hookResolvedActiveSubagent{}, err
	}
	if deeper.ChildSessionID != "" {
		return deeper, nil
	}
	return hookResolvedActiveSubagent{
		ParentSessionID: parentSessionID,
		ToolUseID:       toolUseID,
		ChildSessionID:  childSessionID,
	}, nil
}

func clearHookActiveSubagentState(client string, parentSessionID types.SessionID, toolUseID string) error {
	return withHookActiveSubagentStateLock(client, parentSessionID, func() error {
		statePath, err := hookActiveSubagentStatePath(client, parentSessionID)
		if err != nil {
			return err
		}
		if strings.TrimSpace(toolUseID) == "" || toolUseID == "legacy" {
			if err := os.Remove(statePath); err != nil && !os.IsNotExist(err) {
				return xerrors.Errorf("failed to clear hook active-subagent state: %w", err)
			}
			return nil
		}
		state, err := readHookActiveSubagentStateFile(statePath)
		if err != nil {
			return err
		}
		delete(state.Children, toolUseID)
		if len(state.Children) == 0 {
			if err := os.Remove(statePath); err != nil && !os.IsNotExist(err) {
				return xerrors.Errorf("failed to clear hook active-subagent state: %w", err)
			}
			return nil
		}
		data, err := json.Marshal(state)
		if err != nil {
			return xerrors.Errorf("failed to encode hook active-subagent state: %w", err)
		}
		if err := os.WriteFile(statePath, data, 0o600); err != nil {
			return xerrors.Errorf("failed to write hook active-subagent state: %w", err)
		}
		return nil
	})
}

func cleanupHookActiveSubagentStates(client string) error {
	stateDir, err := resolveHookStateDir()
	if err != nil {
		return err
	}
	activeDir := filepath.Join(stateDir, "active-subagents")
	entries, err := os.ReadDir(activeDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return xerrors.Errorf("failed to read hook active-subagent state directory: %w", err)
	}
	prefix := client + "-"
	for _, entry := range entries {
		if entry.IsDir() || strings.HasSuffix(entry.Name(), ".lock") || !strings.HasPrefix(entry.Name(), prefix) {
			continue
		}
		statePath := filepath.Join(activeDir, entry.Name())
		if _, err := readHookActiveSubagentStateFile(statePath); err != nil {
			return err
		}
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
