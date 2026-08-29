package cli

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"strings"

	"github.com/duck8823/traceary/application/usecase"
	"github.com/duck8823/traceary/domain/types"
	"github.com/duck8823/traceary/presentation"
)

// consolidationExitCode is the host-facing exit status that asks the agent to
// continue and fold the session. All three allowlisted hosts read the same
// mechanism: exit 2 with the reason on stderr.
//   - Claude Code: exit 2 blocks the stop and feeds stderr to the model
//   - Codex CLI:   exit 2 + stderr is the documented continuation form
//   - Kimi Code:   exit 2 appends a continuation message for the model
//
// Gemini, Antigravity and Grok treat a non-zero stop exit as a plain failure
// (Grok has no Stop surface that continues), so they are not on the allowlist.
const consolidationExitCode = 2

// consolidationStopClients are hosts whose stop / AfterAgent hook surface
// interprets a non-zero exit as a continuation request rather than failure.
// Keep the allowlist in one place so host coverage stays obvious.
var consolidationStopClients = map[string]struct{}{
	"claude": {},
	"codex":  {},
	"kimi":   {},
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

// requestConsolidationIfDue measures unrefined body-byte pressure after the
// durable transcript run and, when due, returns exit 2 with a short agent
// prompt on stderr. Every internal failure fails open (exit 0): a
// consolidation decision must never stop the user's work.
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

	threshold := presentation.LoadConfig().Consolidation.ThresholdBytes
	if threshold == 0 {
		return nil
	}

	if c.consolidationPressure == nil {
		slog.Debug("consolidation pressure check skipped: usecase not configured")
		return nil
	}

	sessionID, err := resolveHookTranscriptSessionIDFunc(payload, client)
	if err != nil {
		slog.Debug("consolidation pressure check skipped: session resolve failed", "error", err)
		return nil
	}
	if strings.TrimSpace(sessionID.String()) == "" {
		slog.Debug("consolidation pressure check skipped: empty session id")
		return nil
	}

	resolvedDBPath, err := resolveDBPath(dbPath)
	if err != nil {
		slog.Debug("consolidation pressure check skipped: db path resolve failed", "error", err)
		return nil
	}
	c.applyDatabasePath(resolvedDBPath)

	if suppressed, why := c.consolidationRequestSuppressed(ctx, sessionID, payload); suppressed {
		slog.Debug("consolidation request suppressed", "session_id", sessionID.String(), "why", why)
		return nil
	}

	result, err := c.consolidationPressure.Check(ctx, sessionID, threshold)
	if err != nil {
		// Locked / unreachable DB, missing rows, anything: exit 0.
		slog.Debug("consolidation pressure check failed open", "session_id", sessionID.String(), "error", err)
		return nil
	}
	if !result.Due {
		return nil
	}

	atEventID := c.latestSessionEventID(ctx, sessionID)
	// Measurement is best-effort and must never change the decision. Every
	// failure below logs at debug and falls through to the exit-2 return: the
	// request is still delivered, only the bookkeeping is lost.
	c.recordConsolidationRequest(ctx, sessionID, client, result, threshold, atEventID)

	return consolidationExitError{
		message:  formatConsolidationReason(sessionID, result, atEventID),
		exitCode: consolidationExitCode,
	}
}

// consolidationRequestSuppressed is the single "may this Stop ask?" gate.
// why is "stop_hook_active" or "request_open" and is logged at Debug only.
// Any internal failure returns (false, "") so the ask is still delivered.
func (c *RootCLI) consolidationRequestSuppressed(
	ctx context.Context,
	sessionID types.SessionID,
	payload []byte,
) (bool, string) {
	if hookPayloadBool(payload, "stop_hook_active") {
		return true, "stop_hook_active"
	}
	if c.consolidationRequest == nil {
		return false, ""
	}
	open, err := c.consolidationRequest.HasOpenRequest(ctx, sessionID)
	if err != nil {
		slog.Debug("consolidation open-request lookup failed open", "session_id", sessionID.String(), "error", err)
		return false, ""
	}
	if open {
		return true, "request_open"
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

const consolidationSignalBodyBytes = usecase.ConsolidationSignalBodyBytes

// recordConsolidationRequest writes the metadata-only measurement fact for a
// request that is about to be delivered. It returns nothing: no outcome here
// may alter the caller's exit status (#2273).
func (c *RootCLI) recordConsolidationRequest(
	ctx context.Context,
	sessionID types.SessionID,
	client string,
	result usecase.ConsolidationPressureResult,
	threshold int64,
	atEventID types.Optional[types.EventID],
) {
	if c.consolidationRequest == nil {
		return
	}
	eventID, ok := atEventID.Value()
	if !ok {
		slog.Debug("consolidation request not recorded: session has no events",
			"session_id", sessionID.String())
		return
	}
	recorded, err := c.consolidationRequest.Record(ctx, usecase.ConsolidationRequestInput{
		SessionID:      sessionID,
		Client:         client,
		AtEventID:      eventID,
		Signal:         consolidationSignalBodyBytes,
		PressureValue:  result.PressureBytes,
		ThresholdValue: threshold,
		Delivery:       types.ConsolidationDeliveryStopExit2,
	})
	if err != nil {
		slog.Debug("consolidation request not recorded", "session_id", sessionID.String(), "error", err)
		return
	}
	slog.Debug("consolidation request recorded",
		"session_id", sessionID.String(), "recorded", recorded.Recorded, "re_request", recorded.ReRequest)
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
		"[Traceary] Session %s has %d bytes of unrefined material at or above the consolidation threshold. Use the traceary-session-refine skill: say why the work was undertaken and what changed; how it went is optional.\n"+
			"Run: traceary session refine %s --covers-to %s --summary \"<why + what changed>\" --produced-by agent",
		sessionID.String(),
		result.PressureBytes,
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
