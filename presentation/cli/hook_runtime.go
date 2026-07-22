package cli

import (
	"context"
	"io"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"golang.org/x/xerrors"

	apptypes "github.com/duck8823/traceary/application/types"
	"github.com/duck8823/traceary/domain/types"
)

const (
	hookStateDirEnvKey         = "TRACEARY_HOOK_STATE_DIR"
	hookAuditSuppressionEnvKey = "TRACEARY_NO_AUDIT"
)

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
	hookCmd.AddCommand(c.newHookAntigravityCommand())
	hookCmd.AddCommand(c.newHookGrokCommand())
	hookCmd.AddCommand(c.newHookKimiCommand())
	hookCmd.AddCommand(c.newHookMemoryExtractWorkerCommand())

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
			return c.runHookDurably(cmd.Context(), "session", hookInvocationSpec{Command: "session", Client: args[0], Action: args[1], DBPath: dbPath}, cmd.InOrStdin(), func(input io.Reader) error {
				return c.runHookSession(cmd.Context(), cmd.OutOrStdout(), input, args[0], args[1], dbPath)
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
			return c.runHookDurably(cmd.Context(), "audit", hookInvocationSpec{Command: "audit", Client: args[0], DBPath: dbPath}, cmd.InOrStdin(), func(input io.Reader) error {
				return c.runHookAudit(cmd.Context(), input, args[0], dbPath)
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
			return c.runHookDurably(cmd.Context(), "compact", hookInvocationSpec{Command: "compact", Client: args[0], Action: args[1], DBPath: dbPath}, cmd.InOrStdin(), func(input io.Reader) error {
				return c.runHookCompact(cmd.Context(), cmd.OutOrStdout(), input, args[0], args[1], dbPath)
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
			return c.runHookDurably(cmd.Context(), "subagent-start", hookInvocationSpec{Command: "subagent-start", Client: args[0], DBPath: dbPath}, cmd.InOrStdin(), func(input io.Reader) error {
				return c.runHookSubagentStart(cmd.Context(), input, args[0], dbPath)
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
			return c.runHookDurably(cmd.Context(), "subagent-stop", hookInvocationSpec{Command: "subagent-stop", Client: args[0], DBPath: dbPath}, cmd.InOrStdin(), func(input io.Reader) error {
				return c.runHookSubagentStop(cmd.Context(), input, args[0], dbPath)
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
			return c.runHookDurably(cmd.Context(), "prompt", hookInvocationSpec{Command: "prompt", Client: args[0], DBPath: dbPath}, cmd.InOrStdin(), func(input io.Reader) error {
				return c.runHookPrompt(cmd.Context(), input, args[0], dbPath)
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
			return c.runHookDurably(cmd.Context(), "transcript", hookInvocationSpec{Command: "transcript", Client: args[0], DBPath: dbPath}, cmd.InOrStdin(), func(input io.Reader) error {
				return c.runHookTranscript(cmd.Context(), input, args[0], dbPath)
			})
		},
	}
	cmd.Flags().StringVar(&dbPath, "db-path", "", dbPathFlagUsage())

	return cmd
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
	ctx = withResolvedHookDelivery(ctx, payload, client)
	command := hookPayloadString(payload, "tool_input.command", "")
	if command == "" {
		command = hookPayloadString(payload, "tool_name", "")
	}
	if command == "" {
		return nil
	}
	if shouldSuppressHookAudit(payload, command) {
		return nil
	}

	sessionID, err := resolveHookSessionID(payload, client)
	if err != nil {
		return err
	}
	if sessionID == "" {
		return nil
	}
	workspace, err := resolveHookWorkspaceForAudit(ctx, payload, client)
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

	maxInputBytes, err := resolveAuditMaxBytes(0, false, "TRACEARY_MAX_AUDIT_INPUT_BYTES", c.defaultAuditMaxInputBytes)
	if err != nil {
		return xerrors.Errorf("failed to resolve input byte limit: %w", err)
	}
	maxOutputBytes, err := resolveAuditMaxBytes(0, false, "TRACEARY_MAX_AUDIT_OUTPUT_BYTES", c.defaultAuditMaxOutputBytes)
	if err != nil {
		return xerrors.Errorf("failed to resolve output byte limit: %w", err)
	}

	auditCfg := apptypes.NewAuditRedactionBuilder().
		MaxInputBytes(maxInputBytes).
		MaxOutputBytes(maxOutputBytes).
		ExtraRedactPatterns(c.extraRedactPatterns).
		StructuredRules(c.structuredRedactRules).
		Build()
	_, _, err = c.event.Audit(
		ctx,
		apptypes.AuditInput{
			Command:   command,
			Input:     auditInput,
			Output:    auditOutput,
			Client:    types.Client("hook"),
			Agent:     agent,
			SessionID: sessionID,
			Workspace: workspace,
			ExitCode:  hookPayloadExitCode(payload),
			Failed:    hookPayloadFailed(payload),
		},
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
	ctx = withResolvedHookDelivery(ctx, payload, client)
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
			body = hookPayloadString(payload, "trigger", "")
			if body == "" {
				body = "compact triggered"
				kind = types.EventKind("")
			}
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
		// Sync the digest into sessions.summary so timeline / handoff
		// surfaces have a useful header without waiting for SessionEnd.
		// Only Claude carries a real digest — Gemini's PreCompress body
		// is just the trigger value, not a summary.
		if client == "claude" && c.session != nil {
			rawDigest := hookPayloadString(payload, "pre_compact_context", "")
			if strings.TrimSpace(rawDigest) != "" {
				if _, err := c.session.SetSummaryIfEmpty(ctx, sessionID, rawDigest); err != nil {
					return xerrors.Errorf("failed to sync pre-compact summary into session: %w", err)
				}
			}
		}
		return nil
	case "session-start-compact":
		if output == nil {
			return nil
		}
		return c.printCompactSummaryWithOptions(ctx, output, compactSummaryOptions{
			sessionID:   sessionID.String(),
			recentCount: compactSummaryDefaultRecent,
			memoryLimit: compactSummaryDefaultRecent,
		})
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
	// Gemini delivers prompts on `BeforeAgent`, while Claude / Codex use
	// `UserPromptSubmit`. Persist the host-specific source_hook so the
	// distinction remains recoverable downstream.
	switch client {
	case "gemini":
		ctx = apptypes.WithSourceHook(ctx, "before_agent")
	case "antigravity":
		// Antigravity does not expose prompt text directly on PreInvocation.
		// The Stop adapter recovers the latest USER_INPUT from transcriptPath.
		ctx = apptypes.WithSourceHook(ctx, "stop_transcript")
	default:
		ctx = apptypes.WithSourceHook(ctx, "user_prompt_submit")
	}

	payload, err := readHookPayload(input)
	if err != nil {
		return err
	}
	ctx = withResolvedHookDelivery(ctx, payload, client)
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
	ctx = withResolvedHookDelivery(ctx, payload, client)
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
		StructuredRules(c.structuredRedactRules).
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
