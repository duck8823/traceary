package cli

import (
	"context"
	"fmt"
	"log/slog"
	"strings"

	"github.com/duck8823/traceary/application/usecase"
	"github.com/duck8823/traceary/domain/types"
	"github.com/duck8823/traceary/presentation"
)

// consolidationExitCode is the host-facing exit status that requests the
// agent continue and fold the session. Host mechanisms:
//
//   - Claude Code: exit 2 continues the conversation
//   - Codex CLI: {"decision":"block","reason":...} (stderr reason + exit 2)
//   - Kimi Code: exit 2 appends a continuation message for the model
//
// Gemini and Antigravity treat a non-zero stop exit as a plain failure, so
// they are not on the allowlist below.
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

	result, err := c.consolidationPressure.Check(ctx, sessionID, threshold)
	if err != nil {
		// Locked / unreachable DB, missing rows, anything: exit 0.
		slog.Debug("consolidation pressure check failed open", "session_id", sessionID.String(), "error", err)
		return nil
	}
	if !result.Due {
		return nil
	}

	return consolidationExitError{
		message:  formatConsolidationReason(sessionID, result),
		exitCode: consolidationExitCode,
	}
}

// formatConsolidationReason is the only channel that reaches the agent. Keep
// it short: English, no ANSI, no emoji. When a previous refinement exists,
// include its summary and covers_to so the agent can merge rather than rewrite.
func formatConsolidationReason(sessionID types.SessionID, result usecase.ConsolidationPressureResult) string {
	var b strings.Builder
	fmt.Fprintf(&b,
		"Session %s has unrefined material at or above the consolidation threshold (%d bytes). "+
			"Write a session refinement with `traceary session refine` covering this session's events so far.",
		sessionID.String(),
		result.PressureBytes,
	)
	if summary, ok := result.PreviousSummary.Value(); ok {
		coversTo, _ := result.PreviousCoversTo.Value()
		fmt.Fprintf(&b,
			" Merge with the previous summary (covers_to=%s): %s",
			coversTo.String(),
			summary,
		)
	}
	return b.String()
}
