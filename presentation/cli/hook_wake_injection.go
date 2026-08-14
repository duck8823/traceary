package cli

import (
	"context"
	"io"
	"log/slog"
	"strings"

	"golang.org/x/xerrors"

	"github.com/duck8823/traceary/application"
	"github.com/duck8823/traceary/application/queryservice"
	"github.com/duck8823/traceary/domain/types"
	"github.com/duck8823/traceary/presentation"
)

// wakeInjectionRowLimit bounds how many refinement rows the SQL query may
// return. The byte budget cuts long before this on any realistic store.
const wakeInjectionRowLimit = 64

// formatWakeInjectionText selects summaries newest-first under budgetBytes and
// formats the stdout payload. Summaries are taken whole; when the next would
// exceed the budget it is dropped (and every older one). Returns "" when
// nothing fits — callers write nothing at all, not an empty line.
//
// budgetBytes counts the bytes that would actually be written, including the
// header and blank-line separators. budgetBytes <= 0 disables injection.
func formatWakeInjectionText(summaries []queryservice.SessionWakeSummary, budgetBytes int64) string {
	if budgetBytes <= 0 {
		return ""
	}

	selected := make([]string, 0, len(summaries))
	// Start with header + trailing newline that always precedes the first body.
	used := int64(len(application.WakeInjectionHeader) + 1)
	for _, row := range summaries {
		summary := strings.TrimSpace(row.Summary)
		if summary == "" {
			continue
		}
		// Separator before each summary: a blank line ("\n") after the previous
		// block. The first summary follows the header newline already counted.
		extra := int64(0)
		if len(selected) > 0 {
			extra = 1 // blank-line separator between summaries
		}
		// Each summary ends with a newline when written.
		need := extra + int64(len(summary)) + 1
		if used+need > budgetBytes {
			break
		}
		selected = append(selected, summary)
		used += need
	}
	if len(selected) == 0 {
		// Even the newest summary alone does not fit (or none were eligible).
		return ""
	}

	var b strings.Builder
	b.Grow(int(used))
	b.WriteString(application.WakeInjectionHeader)
	b.WriteByte('\n')
	for i, summary := range selected {
		if i > 0 {
			b.WriteByte('\n')
		}
		b.WriteString(summary)
		b.WriteByte('\n')
	}
	return b.String()
}

// maybeInjectWakeSummaries writes finished session summaries to output at most
// once per client+session. Failures are swallowed: injection must never fail
// the hook. When output is nil (e.g. Kimi SessionStart) the call is a no-op.
func (c *RootCLI) maybeInjectWakeSummaries(
	ctx context.Context,
	output io.Writer,
	client string,
	sessionID types.SessionID,
	workspace types.Workspace,
	dbPath string,
) {
	if output == nil {
		return
	}
	c.injectWakeSummaries(ctx, client, sessionID, workspace, dbPath, func(text string) error {
		_, err := io.WriteString(output, text)
		if err != nil {
			return xerrors.Errorf("failed to write wake injection: %w", err)
		}
		return nil
	})
}

// injectWakeSummaries selects and delivers finished session summaries at most
// once per client+session. The sink lets structured-output hosts embed the
// same text without duplicating selection or marker semantics. Failures are
// swallowed: injection must never fail the hook.
func (c *RootCLI) injectWakeSummaries(
	ctx context.Context,
	client string,
	sessionID types.SessionID,
	workspace types.Workspace,
	dbPath string,
	sink func(string) error,
) {
	if sink == nil {
		return
	}
	if strings.TrimSpace(sessionID.String()) == "" {
		return
	}
	if strings.TrimSpace(workspace.String()) == "" {
		return
	}

	budget := presentation.LoadConfig().WakeInjection.BudgetBytes
	if budget <= 0 {
		if budget < 0 {
			slog.Warn("wake_injection.budget_bytes is negative; treating as disabled", "budget_bytes", budget)
		}
		return
	}

	already, err := hookWakeInjectionAlreadyDone(client, sessionID)
	if err != nil {
		slog.Debug("wake injection marker inspect failed; proceeding", "error", err, "session_id", sessionID.String())
	} else if already {
		return
	}

	text, err := c.buildWakeInjectionText(ctx, workspace, sessionID, budget, dbPath)
	if err != nil {
		slog.Debug("wake injection skipped", "error", err, "session_id", sessionID.String())
		return
	}

	if text != "" {
		if err := sink(text); err != nil {
			slog.Debug("wake injection write failed", "error", err, "session_id", sessionID.String())
			// Do not mark: a failed write may be retried on the next firing.
			return
		}
	}

	// Mark after a successful attempt (including empty text) so resume/compact
	// and subsequent Kimi prompts do not re-query. Marker write failures are
	// best-effort — at worst injection runs twice.
	if err := markHookWakeInjected(client, sessionID); err != nil {
		slog.Debug("wake injection marker write failed", "error", err, "session_id", sessionID.String())
	}
}

func (c *RootCLI) buildWakeInjectionText(
	ctx context.Context,
	workspace types.Workspace,
	excludeSessionID types.SessionID,
	budgetBytes int64,
	dbPath string,
) (string, error) {
	if c.sessionWakeSummary == nil {
		return "", xerrors.Errorf("session wake summary query is not configured")
	}
	resolvedDBPath, err := resolveDBPath(dbPath)
	if err != nil {
		return "", err
	}
	c.applyDatabasePath(resolvedDBPath)

	summaries, err := c.sessionWakeSummary.ListEligible(
		ctx,
		workspace,
		excludeSessionID,
		wakeInjectionRowLimit,
	)
	if err != nil {
		return "", xerrors.Errorf("failed to list wake summaries: %w", err)
	}
	return formatWakeInjectionText(summaries, budgetBytes), nil
}
