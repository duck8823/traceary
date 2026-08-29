package cli

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"strings"
	"sync"

	"github.com/duck8823/traceary/application/usecase"
	"github.com/duck8823/traceary/domain/types"
	"github.com/duck8823/traceary/presentation"
)

const thresholdBytesDeprecatedWarn = "[WARN] consolidation.threshold_bytes is deprecated and ignored: the stop-hook consolidation trigger is now work-based (consolidation.min_commands, consolidation.stop_cadence). The key is removed in the next minor; remove it from your config.json.\n"

var (
	thresholdBytesDeprecatedOnce sync.Once
	thresholdBytesWarnWriter     io.Writer = os.Stderr
)

// ResetConsolidationDeprecationWarnForTest resets the once-per-process WARN.
func ResetConsolidationDeprecationWarnForTest() {
	thresholdBytesDeprecatedOnce = sync.Once{}
}

// SetConsolidationWarnWriterForTest redirects the deprecation WARN.
func SetConsolidationWarnWriterForTest(w io.Writer) {
	if w == nil {
		thresholdBytesWarnWriter = os.Stderr
		return
	}
	thresholdBytesWarnWriter = w
}

// consolidationExitCode is the host-facing exit status that asks the agent to
// continue and fold the session. All three allowlisted hosts read the same
// mechanism: exit 2 with the reason on stderr.
//   - Claude Code: exit 2 blocks the stop and feeds stderr to the model
//   - Codex CLI:   exit 2 + stderr is the documented continuation form
//   - Kimi Code:   exit 2 appends a continuation message for the model
//
// Gemini, Antigravity and Grok treat a non-zero stop exit as a plain failure,
// so they are not on this allowlist. Those hosts use prompt-context or
// Stop-envelope channels instead (see hook_consolidation_prompt.go).
const consolidationExitCode = 2

// consolidationStopClients are hosts whose stop / AfterAgent hook surface
// interprets a non-zero exit as a continuation request rather than failure.
// Keep the allowlist in one place so host coverage stays obvious.
var consolidationStopClients = map[string]struct{}{
	"claude": {},
	"codex":  {},
	"kimi":   {},
}

// consolidationRequest is the shared decision a delivery sink encodes.
type consolidationRequest struct {
	SessionID   types.SessionID
	Client      string
	Result      usecase.ConsolidationPressureResult
	AtEventID   types.Optional[types.EventID]
	MinCommands int64
}

// consolidationExitError is a cliExitCoder (see main.go) that surfaces the
// consolidation reason on stderr with a non-zero exit.
type consolidationExitError struct {
	message  string
	exitCode int
}

func (e consolidationExitError) Error() string { return e.message }
func (e consolidationExitError) ExitCode() int { return e.exitCode }

// runDurableHookThenMaybeConsolidate runs a durable hook whose closure may
// capture a non-nil payload when a transcript row was actually persisted on
// this firing, then measures consolidation pressure outside runHookDurably.
// Consolidation must not live inside a durable run: runHookBestEffort
// swallows every error, so a non-zero exit can never escape from inside one
// (#1674). Callers set the captured payload only when recorded=true — never
// on fail-soft skips or idempotent redeliveries that wrote nothing.
func (c *RootCLI) runDurableHookThenMaybeConsolidate(
	ctx context.Context,
	name string,
	spec hookInvocationSpec,
	input io.Reader,
	client string,
	dbPath string,
	run func(input io.Reader) (consolidationPayload []byte, err error),
) error {
	var payload []byte
	if err := c.runHookDurably(ctx, name, spec, input, func(input io.Reader) error {
		captured, err := run(input)
		// Capture before returning err so a successful transcript write still
		// reaches the pressure check when a sibling best-effort step (e.g.
		// usage capture) fails and would otherwise leave payload unset.
		if captured != nil {
			payload = captured
		}
		return err
	}); err != nil {
		return err
	}
	if payload == nil {
		// The durable run never reached the closure, recording failed
		// (swallowed by best-effort), the turn was fail-soft-skipped, or an
		// idempotency guard skipped this firing. Nothing to measure.
		return nil
	}
	return c.requestConsolidationIfDue(ctx, client, payload, dbPath)
}

// requestConsolidationIfDue is the exit-2 Stop sink for hosts on
// consolidationStopClients. The due/not-due decision lives in
// consolidationRequestIfDue so prompt-context and Stop-envelope sinks share it.
func (c *RootCLI) requestConsolidationIfDue(
	ctx context.Context,
	client string,
	payload []byte,
	dbPath string,
) error {
	client = strings.TrimSpace(client)
	if _, ok := consolidationStopClients[client]; !ok {
		return nil
	}
	req, ok := c.consolidationRequestIfDue(ctx, client, payload, dbPath)
	if !ok {
		return nil
	}
	// Ledger insert failures still fail open (ask delivered).
	_ = c.recordConsolidationRequest(ctx, req, types.ConsolidationDeliveryStopExit2)
	return consolidationExitError{
		message:  formatConsolidationReason(req.SessionID, req.Result, req.AtEventID),
		exitCode: consolidationExitCode,
	}
}

// consolidationRequestIfDue is the single due/not-due decision for every
// delivery channel. Every failure returns (zero, false) and logs at Debug.
func (c *RootCLI) consolidationRequestIfDue(
	ctx context.Context,
	client string,
	payload []byte,
	dbPath string,
) (consolidationRequest, bool) {
	client = strings.TrimSpace(client)

	cfg := presentation.LoadConfig().Consolidation
	if cfg.ThresholdBytesSet {
		thresholdBytesDeprecatedOnce.Do(func() {
			_, _ = io.WriteString(thresholdBytesWarnWriter, thresholdBytesDeprecatedWarn)
		})
	}
	policy := usecase.ConsolidationPolicy{
		MinCommands: cfg.MinCommands,
		StopCadence: cfg.StopCadence,
	}
	if policy.MinCommands <= 0 || policy.StopCadence <= 0 {
		return consolidationRequest{}, false
	}

	if c.consolidationPressure == nil {
		slog.Debug("consolidation pressure check skipped: usecase not configured")
		return consolidationRequest{}, false
	}

	sessionID, err := resolveHookTranscriptSessionIDFunc(payload, client)
	if err != nil {
		slog.Debug("consolidation pressure check skipped: session resolve failed", "error", err)
		return consolidationRequest{}, false
	}
	if strings.TrimSpace(sessionID.String()) == "" {
		slog.Debug("consolidation pressure check skipped: empty session id")
		return consolidationRequest{}, false
	}

	resolvedDBPath, err := resolveDBPath(dbPath)
	if err != nil {
		slog.Debug("consolidation pressure check skipped: db path resolve failed", "error", err)
		return consolidationRequest{}, false
	}
	c.applyDatabasePath(resolvedDBPath)

	if suppressed, why := c.consolidationRequestSuppressed(ctx, sessionID, payload); suppressed {
		slog.Debug("consolidation request suppressed", "session_id", sessionID.String(), "why", why)
		return consolidationRequest{}, false
	}

	result, err := c.consolidationPressure.Check(ctx, sessionID, policy)
	if err != nil {
		// Reads that inform the decision fail closed (no ask).
		slog.Debug("consolidation pressure check failed closed", "session_id", sessionID.String(), "error", err)
		return consolidationRequest{}, false
	}
	if !result.Due {
		slog.Debug("consolidation not due", "session_id", sessionID.String(), "skipped", result.Skipped)
		return consolidationRequest{}, false
	}

	return consolidationRequest{
		SessionID:   sessionID,
		Client:      client,
		Result:      result,
		AtEventID:   c.latestSessionEventID(ctx, sessionID),
		MinCommands: policy.MinCommands,
	}, true
}

// consolidationRequestSuppressed is the single payload-side "may this Stop ask?"
// gate. why is "stop_hook_active" and is logged at Debug only.
func (c *RootCLI) consolidationRequestSuppressed(
	_ context.Context,
	_ types.SessionID,
	payload []byte,
) (bool, string) {
	if hookPayloadBool(payload, "stop_hook_active") {
		return true, "stop_hook_active"
	}
	return false, ""
}

func (c *RootCLI) latestSessionEventID(ctx context.Context, sessionID types.SessionID) types.Optional[types.EventID] {
	if c.sessionEventOrder == nil {
		return types.None[types.EventID]()
	}
	latest, err := c.sessionEventOrder.LatestEventID(ctx, sessionID)
	if err != nil {
		slog.Debug("consolidation latest event lookup failed", "session_id", sessionID.String(), "error", err)
		return types.None[types.EventID]()
	}
	return latest
}

// recordConsolidationRequest writes the metadata-only measurement fact for a
// request that is about to be delivered. It never returns an error: false means
// the row was not written (nil usecase, no events, insert failed, or duplicate).
// Exit-2 and prompt-context sinks ignore the result (fail open). Stop-envelope
// sinks require true so a continue/block cannot re-enter without a cadence bound.
func (c *RootCLI) recordConsolidationRequest(
	ctx context.Context,
	req consolidationRequest,
	delivery types.ConsolidationDelivery,
) bool {
	if c.consolidationRequest == nil {
		return false
	}
	eventID, ok := req.AtEventID.Value()
	if !ok {
		slog.Debug("consolidation request not recorded: session has no events",
			"session_id", req.SessionID.String())
		return false
	}
	recorded, err := c.consolidationRequest.Record(ctx, usecase.ConsolidationRequestInput{
		SessionID:      req.SessionID,
		Client:         req.Client,
		AtEventID:      eventID,
		Signal:         usecase.ConsolidationSignalWork,
		PressureValue:  req.Result.Commands,
		ThresholdValue: req.MinCommands,
		Delivery:       delivery,
	})
	if err != nil {
		slog.Debug("consolidation request not recorded", "session_id", req.SessionID.String(), "error", err)
		return false
	}
	slog.Debug("consolidation request recorded",
		"session_id", req.SessionID.String(), "recorded", recorded.Recorded, "re_request", recorded.ReRequest)
	return recorded.Recorded
}

// formatConsolidationReason is the only channel that reaches the agent. Keep
// it short: English, no ANSI, no emoji. When a previous refinement exists,
// include its summary and covers_to so the agent can merge rather than rewrite.
func formatConsolidationReason(sessionID types.SessionID, result usecase.ConsolidationPressureResult, atEventID types.Optional[types.EventID]) string {
	coversTo := "<event-id>"
	if id, ok := atEventID.Value(); ok {
		coversTo = id.String()
	}
	var b strings.Builder
	fmt.Fprintf(&b,
		"[Traceary] Session %s has %d audited commands of unrefined material since the last refinement. Use the traceary-session-refine skill: say why the work was undertaken and what changed; how it went is optional.\n"+
			"Run: traceary session refine %s --covers-to %s --summary \"<why + what changed>\" --produced-by agent",
		sessionID.String(),
		result.Commands,
		sessionID.String(),
		coversTo,
	)
	if summary, ok := result.PreviousSummary.Value(); ok {
		prev, _ := result.PreviousCoversTo.Value()
		fmt.Fprintf(&b,
			"\nMerge with the previous summary (covers_to=%s) rather than rewriting it: %s",
			prev.String(),
			summary,
		)
	}
	return b.String()
}
