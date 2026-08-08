package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"strings"

	"github.com/spf13/cobra"
	"golang.org/x/xerrors"

	"github.com/duck8823/traceary/application/usecase"
)

const kimiHookClient = "kimi"

// newHookKimiCommand wires the native Kimi Code hook protocol boundary. The
// adapter normalizes the live-observed snake_case payload (host contract:
// docs/hooks/host-contract.json) and delegates Traceary event semantics to
// the existing shared hook runtime.
func (c *RootCLI) newHookKimiCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:    "kimi <session-start|session-end|user-prompt-submit|pre-tool-use|post-tool-use|post-tool-use-failure|stop>",
		Short:  "Runtime entrypoints for Kimi Code hooks",
		Hidden: true,
	}
	cmd.AddCommand(c.newHookKimiEventCommand("session-start", c.runHookKimiSessionStart))
	cmd.AddCommand(c.newHookKimiEventCommand("session-end", c.runHookKimiSessionEnd))
	cmd.AddCommand(c.newHookKimiEventCommand("user-prompt-submit", c.runHookKimiUserPromptSubmit))
	cmd.AddCommand(c.newHookKimiEventCommand("pre-tool-use", c.runHookKimiPreToolUse))
	cmd.AddCommand(c.newHookKimiEventCommand("post-tool-use", c.runHookKimiPostToolUse))
	cmd.AddCommand(c.newHookKimiEventCommand("post-tool-use-failure", c.runHookKimiPostToolUseFailure))
	// Stop is special-cased: after a durable transcript write it may request
	// consolidation with exit 2, which cannot escape from inside runHookDurably
	// (see newHookKimiStopCommand / hook transcript's RunE).
	cmd.AddCommand(c.newHookKimiStopCommand())
	cmd.AddCommand(c.newHookKimiEventCommand("pre-compact", c.runHookKimiPreCompact))
	cmd.AddCommand(c.newHookKimiEventCommand("post-compact", c.runHookKimiPostCompact))
	cmd.AddCommand(c.newHookKimiEventCommand("subagent-stop", c.runHookKimiSubagentStop))
	return cmd
}

// newHookKimiStopCommand wires the native Kimi Stop command the same way the
// shared hook transcript command is wired: durable record first, then
// consolidation outside runHookDurably and only when a transcript row landed
// on this firing. The packaged Kimi plugin invokes `traceary hook kimi stop`
// (not `hook transcript kimi`), so without this boundary the allowlist entry
// for kimi in consolidationStopClients could never fire (#1674 / #1711).
func (c *RootCLI) newHookKimiStopCommand() *cobra.Command {
	var dbPath string
	cmd := &cobra.Command{
		Use:    "stop",
		Short:  "Record Kimi Code stop hook events",
		Hidden: true,
		Args:   noArgsLocalized(),
		RunE: func(cmd *cobra.Command, _ []string) error {
			return c.runDurableHookThenMaybeConsolidate(
				cmd.Context(),
				"kimi stop",
				hookInvocationSpec{
					Command: "kimi",
					Client:  kimiHookClient,
					Action:  "stop",
					DBPath:  dbPath,
				},
				cmd.InOrStdin(),
				kimiHookClient,
				dbPath,
				func(input io.Reader) ([]byte, error) {
					// Return the same normalized payload the recording used so
					// the session resolver sees the fields extract/record saw.
					recorded, normalized, err := c.runHookKimiStop(cmd.Context(), input, dbPath)
					if recorded {
						return normalized, err
					}
					return nil, err
				},
			)
		},
	}
	cmd.Flags().StringVar(&dbPath, "db-path", "", dbPathFlagUsage())
	return cmd
}

func (c *RootCLI) newHookKimiEventCommand(
	action string,
	run func(context.Context, io.Writer, io.Reader, string) error,
) *cobra.Command {
	var dbPath string
	cmd := &cobra.Command{
		Use:    action,
		Short:  "Record Kimi Code " + action + " hook events",
		Hidden: true,
		Args:   noArgsLocalized(),
		RunE: func(cmd *cobra.Command, _ []string) error {
			return c.runHookDurably(cmd.Context(), "kimi "+action, hookInvocationSpec{
				Command: "kimi",
				Client:  kimiHookClient,
				Action:  action,
				DBPath:  dbPath,
			}, cmd.InOrStdin(), func(input io.Reader) error {
				return run(cmd.Context(), cmd.OutOrStdout(), input, dbPath)
			})
		},
	}
	cmd.Flags().StringVar(&dbPath, "db-path", "", dbPathFlagUsage())
	return cmd
}

func (c *RootCLI) runHookKimiSessionStart(ctx context.Context, _ io.Writer, input io.Reader, dbPath string) error {
	normalized, err := normalizeKimiHookPayload(input)
	if err != nil {
		return err
	}
	if strings.TrimSpace(hookPayloadString(normalized, "session_id", "")) == "" {
		return nil
	}
	// Kimi SessionStart is fire-and-forget — keep output nil so wake injection
	// happens on the first UserPromptSubmit instead (#1684).
	return c.runHookSession(ctx, nil, bytes.NewReader(normalized), kimiHookClient, "start", dbPath)
}

func (c *RootCLI) runHookKimiSessionEnd(ctx context.Context, _ io.Writer, input io.Reader, dbPath string) error {
	normalized, err := normalizeKimiHookPayload(input)
	if err != nil {
		return err
	}
	if strings.TrimSpace(hookPayloadString(normalized, "session_id", "")) == "" {
		return nil
	}
	sessionErr := c.runHookSession(ctx, nil, bytes.NewReader(normalized), kimiHookClient, "end", dbPath)
	usageErr := c.captureKimiUsage(ctx, normalized, dbPath, usecase.KimiUsageBoundarySessionEnd)
	return errors.Join(sessionErr, usageErr)
}

func (c *RootCLI) runHookKimiUserPromptSubmit(ctx context.Context, output io.Writer, input io.Reader, dbPath string) error {
	normalized, err := normalizeKimiHookPayload(input)
	if err != nil {
		return err
	}
	if err := c.runHookPrompt(ctx, bytes.NewReader(normalized), kimiHookClient, dbPath); err != nil {
		return err
	}
	// Kimi injects on the first UserPromptSubmit because SessionStart is ignored.
	sessionID, err := resolveHookSessionID(normalized, kimiHookClient)
	if err != nil {
		slog.Debug("kimi wake injection skipped: session resolve failed", "error", err)
		return nil
	}
	if sessionID == "" {
		return nil
	}
	workspace, err := resolveHookWorkspace(ctx, normalized, kimiHookClient, true)
	if err != nil {
		slog.Debug("kimi wake injection skipped: workspace resolve failed", "error", err)
		return nil
	}
	c.maybeInjectWakeSummaries(ctx, output, kimiHookClient, sessionID, workspace, dbPath)
	return nil
}

// PreToolUse is a subagent-start boundary for Agent tool calls and a
// validation-only boundary for everything else. Kimi's PostToolUse already
// carries both input and output, so recording a general pre-tool audit here
// would duplicate the completed audit; only Agent calls carry the
// correlating tool_call_id + tool_input.subagent_type the subagent
// parent/child attribution needs.
func (c *RootCLI) runHookKimiPreToolUse(ctx context.Context, _ io.Writer, input io.Reader, dbPath string) error {
	normalized, err := normalizeKimiHookPayload(input)
	if err != nil {
		return err
	}
	if strings.TrimSpace(hookPayloadString(normalized, "tool_name", "")) != "Agent" {
		return nil
	}
	return c.runHookSubagentStart(ctx, bytes.NewReader(normalized), kimiHookClient, dbPath)
}

// Kimi's SubagentStop carries agent_name and the subagent response but no
// tool_use_id, so the shared subagent-stop path falls back to the latest
// active child of the parent session (same semantics as Claude).
func (c *RootCLI) runHookKimiSubagentStop(ctx context.Context, _ io.Writer, input io.Reader, dbPath string) error {
	normalized, err := normalizeKimiHookPayload(input)
	if err != nil {
		return err
	}
	return c.runHookSubagentStop(ctx, bytes.NewReader(normalized), kimiHookClient, dbPath)
}

// Kimi's compact hooks expose trigger and token counts but no summary body,
// so the shared compact path records markers only (mirroring Grok).
func (c *RootCLI) runHookKimiPreCompact(ctx context.Context, _ io.Writer, input io.Reader, dbPath string) error {
	normalized, err := normalizeKimiHookPayload(input)
	if err != nil {
		return err
	}
	return c.runHookCompact(ctx, nil, bytes.NewReader(normalized), kimiHookClient, "pre-compact", dbPath)
}

func (c *RootCLI) runHookKimiPostCompact(ctx context.Context, _ io.Writer, input io.Reader, dbPath string) error {
	normalized, err := normalizeKimiHookPayload(input)
	if err != nil {
		return err
	}
	return c.runHookCompact(ctx, nil, bytes.NewReader(normalized), kimiHookClient, "post-compact", dbPath)
}

func (c *RootCLI) runHookKimiPostToolUse(ctx context.Context, _ io.Writer, input io.Reader, dbPath string) error {
	normalized, err := normalizeKimiHookPayload(input)
	if err != nil {
		return err
	}
	return c.runHookAudit(ctx, bytes.NewReader(normalized), kimiHookClient, dbPath)
}

func (c *RootCLI) runHookKimiPostToolUseFailure(ctx context.Context, _ io.Writer, input io.Reader, dbPath string) error {
	normalized, err := normalizeKimiHookPayload(input)
	if err != nil {
		return err
	}
	return c.runHookAudit(ctx, bytes.NewReader(normalized), kimiHookClient, dbPath)
}

// Kimi's Stop is a turn boundary, not a session end: the host emits a true
// SessionEnd (reason=exit), so Stop only captures the assistant transcript
// via the session wire log side channel.
//
// recorded is true only when a transcript row was persisted on this firing
// (propagated from runHookKimiTranscript). Callers that gate consolidation
// on a successful write must honour it: idempotent redeliveries and
// fail-soft skips return recorded=false so pressure is not re-checked for a
// turn that did not land again. normalized is the payload the recording used.
func (c *RootCLI) runHookKimiStop(ctx context.Context, input io.Reader, dbPath string) (recorded bool, normalized []byte, err error) {
	normalized, err = normalizeKimiHookPayload(input)
	if err != nil {
		return false, nil, err
	}
	recorded, transcriptErr := c.runHookKimiTranscript(ctx, normalized, dbPath)
	usageErr := c.captureKimiUsage(ctx, normalized, dbPath, usecase.KimiUsageBoundaryStop)
	return recorded, normalized, errors.Join(transcriptErr, usageErr)
}

// runHookKimiTranscript wraps the shared transcript recorder with a
// per-(session, wire turn ID, content fingerprint) idempotency guard. Kimi's
// Stop hook fires roughly two dozen times per completed turn, with
// redeliveries observed as little as ~0.14s apart — including effectively
// concurrent firings. Extraction itself is correct (it already resolves only
// the final turn's blocks), so without this guard the same turn is
// re-recorded on every firing, measured at 23x live (#1681).
//
// Turn ID alone is not a completed-turn boundary: Kimi's Stop can re-fire
// while a turn is still streaming, so a later firing with the same turn ID
// can carry strictly more content than an earlier one (measured live: 2,053
// of 52,475 consecutive same-session pairs grew, 153 of those gained the
// only "text" block the turn ever produced). Keying on turn ID alone would
// record the marker on the first, incomplete firing and silently discard
// everything the turn produced afterwards — a worse failure than the
// duplication being fixed. The content fingerprint catches that growth: a
// firing is skipped only when both the turn ID and the fingerprint of its
// extracted blocks match the marker.
//
// The blocks extracted here for the fingerprint are the SAME blocks passed
// to runHookTranscriptWithBlocks below — the shared recorder never
// re-extracts from the wire log on this path. Two independent reads of
// wire.jsonl (one for the fingerprint, one inside the recorder) used to run
// per firing; besides doubling the per-firing cost of a bufio scan of the
// whole session wire log, it meant the fingerprint keyed one read while the
// persisted row came from a second, later read of a file that can change
// between the two (rotated, truncated, rewritten). Passing the blocks
// through closes that window: the fingerprint and the persisted body are
// now guaranteed to describe the same content (#1681 HIGH finding).
//
// This deliberately does not delete or supersede the earlier, shorter row
// that a growing turn leaves behind — that needs a delete port this hook
// does not have. Recording each growth snapshot still collapses ~24
// same-turn firings to roughly 2-3 rows (turn ID unchanged, fingerprint
// growing), a ~95% reduction with zero content loss.
//
// The check-then-record-then-mark sequence runs inside
// withKimiTranscriptTurnStateLock so two racing firings for the same
// (turn ID, fingerprint) cannot both observe "not yet recorded" and both
// write a row. Marking is additionally gated on the recorder's own
// recorded=true return value, never inferred from a nil error: the shared
// recorder fails soft on several unrelated conditions (e.g. an unresolved
// session), and treating any of those nil-error returns as "a row was
// persisted" would let a turn that was never actually stored get marked as
// done — silently discarding every later redelivery of that turn forever
// (#1681 CRITICAL finding).
//
// When the wire turn cannot be resolved (missing index entry, missing wire
// log, escaped path, ...), the fingerprint cannot be computed, or the marker
// infrastructure is unusable, this falls through to the shared recorder
// unguarded — a hook must never fail or silently drop a turn over
// housekeeping state, even at the cost of an occasional duplicate.
//
// Losing the race for the lock is the one case that does NOT fall open: it
// means another firing is recording this turn, and Kimi will redeliver it
// ~0.14s later anyway. See errKimiTranscriptTurnLockBusy.
//
// The bool return is true only when a transcript row was persisted on this
// firing. Idempotent skips, lock-busy yields, and fail-soft recorder skips
// return false so consolidation (and any other post-write gate) does not
// treat a no-op redelivery as a new turn (#1674 / #1681).
func (c *RootCLI) runHookKimiTranscript(ctx context.Context, payload []byte, dbPath string) (bool, error) {
	sessionID := strings.TrimSpace(hookPayloadString(payload, "session_id", ""))
	blocks, turnID, turnResolved := extractKimiTranscriptTurn(payload)
	turnID = strings.TrimSpace(turnID)
	if !turnResolved || sessionID == "" || turnID == "" {
		return c.runHookTranscriptWithBlocks(ctx, bytes.NewReader(payload), kimiHookClient, dbPath, nil)
	}
	fingerprint, fingerprintErr := kimiTranscriptBlocksFingerprint(blocks)
	if fingerprintErr != nil {
		slog.Debug("Kimi transcript fingerprint unavailable; recording unguarded", "session_id", sessionID, "turn_id", turnID, "error", fingerprintErr)
		return c.runHookTranscriptWithBlocks(ctx, bytes.NewReader(payload), kimiHookClient, dbPath, blocks)
	}

	var recorded bool
	var recordErr error
	lockErr := withKimiTranscriptTurnStateLock(sessionID, func() error {
		if kimiTranscriptTurnAlreadyRecorded(sessionID, turnID, fingerprint) {
			// This firing wrote nothing; leave recorded=false so consolidation
			// does not re-request for an already-persisted turn.
			return nil
		}
		var err error
		recorded, err = c.runHookTranscriptWithBlocks(ctx, bytes.NewReader(payload), kimiHookClient, dbPath, blocks)
		if err != nil {
			recordErr = err
			return err
		}
		if recorded {
			markKimiTranscriptTurnRecorded(sessionID, turnID, fingerprint)
		}
		return nil
	})
	if errors.Is(lockErr, errKimiTranscriptTurnLockBusy) {
		// Another firing held the lock for the whole acquisition budget,
		// so it is recording this turn right now. Yield instead of
		// recording unguarded: the budget is the same 1s as SQLite's
		// busy_timeout, so a contended store exhausts it routinely, and
		// recording here would put a duplicate in the store on exactly
		// the workload #1681 is about. Kimi redelivers Stop for this turn
		// roughly two dozen times, so skipping costs at most one more
		// redelivery (~0.14s) — and if the holder succeeds, the marker it
		// writes makes those redeliveries correctly no-ops.
		slog.Debug("Kimi transcript turn is being recorded by another firing; skipping", "session_id", sessionID, "turn_id", turnID)
		return false, nil
	}
	if lockErr != nil && recordErr == nil {
		// The marker infrastructure itself is unusable (an unresolvable
		// state directory, a failed mkdir) — no firing can take the lock,
		// so yielding would drop the turn entirely. Fail open: record
		// unguarded rather than silently dropping a possibly-new turn. No
		// marker is written on this path either way — it runs outside
		// withKimiTranscriptTurnStateLock, so writing one here would race
		// the very lock this falls back from.
		slog.Debug("Kimi transcript turn lock unavailable; recording unguarded", "session_id", sessionID, "turn_id", turnID, "error", lockErr)
		return c.runHookTranscriptWithBlocks(ctx, bytes.NewReader(payload), kimiHookClient, dbPath, blocks)
	}
	return recorded, lockErr
}

func (c *RootCLI) captureKimiUsage(
	ctx context.Context,
	payload []byte,
	dbPath string,
	boundary usecase.KimiUsageBoundary,
) error {
	if c.kimiUsage == nil {
		return nil
	}
	providerSessionID := strings.TrimSpace(hookPayloadString(payload, "session_id", ""))
	if providerSessionID == "" {
		return nil
	}
	sessionID, err := resolveHookParentSessionID(payload, kimiHookClient)
	if err != nil {
		return err
	}
	if sessionID == "" {
		return nil
	}
	if c.storeManagement == nil {
		return xerrors.Errorf("usage capture store dependency is not configured")
	}
	resolvedDBPath, err := resolveDBPath(dbPath)
	if err != nil {
		return err
	}
	c.applyDatabasePath(resolvedDBPath)
	if err := c.storeManagement.Initialize(ctx); err != nil {
		return xerrors.Errorf("failed to initialize store: %w", err)
	}
	if _, err := c.kimiUsage.Capture(ctx, usecase.KimiUsageCaptureInput{
		SessionID:         sessionID,
		ProviderSessionID: providerSessionID,
		Boundary:          boundary,
	}); err != nil {
		return xerrors.Errorf("failed to capture Kimi usage: %w", err)
	}
	return nil
}

// normalizeKimiHookPayload maps fields proven by the versioned live contract
// into the shared snake_case hook envelope. Kimi's payload is already
// snake_case, so this is mostly a pass-through with these renames:
//
//   - tool_call_id → tool_use_id (shared tool correlation field)
//   - tool_output  → tool_response (shared audit output field)
//   - agent_name   → subagent_type (shared subagent label field)
//   - error{code,message,retryable} → error (string; shared failure signal)
//   - prompt content-block array → prompt (plain text, joined)
//
// Unknown fields remain at the host boundary and unavailable events are not
// synthesized.
func normalizeKimiHookPayload(input io.Reader) ([]byte, error) {
	payload, err := readHookPayload(input)
	if err != nil {
		return nil, err
	}
	if len(bytes.TrimSpace(payload)) == 0 {
		return []byte("{}"), nil
	}

	var source map[string]any
	if err := json.Unmarshal(payload, &source); err != nil {
		return nil, xerrors.Errorf("failed to decode Kimi hook payload: %w", err)
	}
	normalized := map[string]any{}
	copyKimiHookField(normalized, "session_id", source, "session_id")
	copyKimiHookField(normalized, "cwd", source, "cwd")
	copyKimiHookField(normalized, "tool_name", source, "tool_name")
	copyKimiHookField(normalized, "tool_input", source, "tool_input")
	copyKimiHookField(normalized, "tool_use_id", source, "tool_call_id")
	copyKimiHookField(normalized, "tool_response", source, "tool_output")
	copyKimiHookField(normalized, "trigger", source, "trigger")
	copyKimiHookField(normalized, "subagent_type", source, "agent_name")
	if errorValue, ok := source["error"]; ok {
		if message := kimiErrorMessage(errorValue); message != "" {
			normalized["error"] = message
		}
	}
	if promptValue, ok := source["prompt"]; ok {
		if text := flattenKimiPrompt(promptValue); text != "" {
			normalized["prompt"] = text
		}
	}

	encoded, err := marshalStableJSON(normalized)
	if err != nil {
		return nil, xerrors.Errorf("failed to normalize Kimi hook payload: %w", err)
	}
	return encoded, nil
}

func copyKimiHookField(target map[string]any, targetName string, source map[string]any, sourceName string) {
	value, ok := source[sourceName]
	if !ok || value == nil {
		return
	}
	if stringValue, ok := value.(string); ok && strings.TrimSpace(stringValue) == "" {
		return
	}
	target[targetName] = value
}

// kimiErrorMessage flattens Kimi's PostToolUseFailure error object
// {code, message, retryable} into the shared string failure signal. The
// message is preferred; a missing/blank message falls back to the code, and
// a shapeless error still yields a fixed marker so a failure event can never
// degrade into a silent success. code/retryable are otherwise intentionally
// dropped — the shared signal has no structured fields for them.
func kimiErrorMessage(value any) string {
	if message, ok := value.(string); ok {
		if trimmed := strings.TrimSpace(message); trimmed != "" {
			return trimmed
		}
		return "unknown error"
	}
	object, ok := value.(map[string]any)
	if !ok {
		return "unknown error"
	}
	if message, _ := object["message"].(string); strings.TrimSpace(message) != "" {
		return strings.TrimSpace(message)
	}
	if code, _ := object["code"].(string); strings.TrimSpace(code) != "" {
		return "kimi tool error: " + strings.TrimSpace(code)
	}
	return "unknown error"
}

// flattenKimiPrompt renders Kimi's UserPromptSubmit prompt — a content-block
// array of {type, text} objects — as plain text. A plain-string prompt (as
// observed on SubagentStart) passes through unchanged.
func flattenKimiPrompt(value any) string {
	if text, ok := value.(string); ok {
		return strings.TrimSpace(text)
	}
	blocks, ok := value.([]any)
	if !ok {
		return ""
	}
	parts := make([]string, 0, len(blocks))
	for _, block := range blocks {
		object, ok := block.(map[string]any)
		if !ok {
			continue
		}
		if object["type"] != "text" {
			continue
		}
		if text, ok := object["text"].(string); ok && strings.TrimSpace(text) != "" {
			parts = append(parts, text)
		}
	}
	return strings.Join(parts, "\n")
}
