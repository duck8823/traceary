package cli

import (
	"context"
	"fmt"
	"io"
	"strings"
	"time"

	"golang.org/x/xerrors"

	apptypes "github.com/duck8823/traceary/application/types"
	"github.com/duck8823/traceary/domain/types"
)

type handoffCommandInput struct {
	dbPath            string
	sessionID         string
	workspace         string
	recent            int
	memories          int
	preset            string
	includeCandidates bool
	asOf              string
	staleAfter        time.Duration
	allowStale        bool
}

func (c *RootCLI) runHandoff(ctx context.Context, output io.Writer, input handoffCommandInput) error {
	if c.storeManagement == nil {
		return xerrors.New(Localize("initialize store usecase is not configured", "ストア初期化ユースケースが設定されていません"))
	}
	if c.context == nil {
		return xerrors.New(Localize("context usecase is not configured", "context ユースケースが設定されていません"))
	}

	resolvedDBPath, err := resolveDBPath(input.dbPath)
	if err != nil {
		return xerrors.Errorf("%s: %w", Localize("failed to resolve DB path", "DB パスの解決に失敗しました"), err)
	}
	c.applyDatabasePath(resolvedDBPath)
	if err := c.storeManagement.Initialize(ctx); err != nil {
		return xerrors.Errorf("%s: %w", Localize("failed to initialize store", "ストアの初期化に失敗しました"), err)
	}

	resolvedWorkspace := resolveWorkspaceValue(ctx, input.workspace)
	preset, err := apptypes.MemoryRetrievalPresetOf(input.preset)
	if err != nil {
		return xerrors.Errorf("%s: %w", Localize("failed to parse --preset", "--preset の解析に失敗しました"), err)
	}
	asOf, err := parseOptionalValidityTime(input.asOf)
	if err != nil {
		return xerrors.Errorf("%s: %w", Localize("failed to parse --as-of", "--as-of の解析に失敗しました"), err)
	}
	resolvedSessionID := types.SessionID(resolveOptionalValue(input.sessionID, "TRACEARY_SESSION_ID", ""))
	baseBuilder := apptypes.NewContextPackCriteriaBuilder().
		SessionID(resolvedSessionID).
		Workspace(types.Workspace(resolvedWorkspace)).
		RecentCommandsLimit(input.recent).
		MemoryLimit(input.memories).
		MemoryPreset(preset).
		IncludeMemoryCandidates(input.includeCandidates).
		MemoryAsOf(asOf).
		StaleAfter(input.staleAfter).
		AllowStale(input.allowStale)
	result, err := c.context.Handoff(ctx, baseBuilder.Build())
	if err != nil {
		return xerrors.Errorf("%s: %w", Localize("failed to build handoff summary", "handoff サマリーの構築に失敗しました"), err)
	}

	if _, ok := result.Value(); !ok && !input.allowStale && input.staleAfter > 0 {
		// The builder may have skipped a stale active session. Re-query
		// with allowStale=true so we can surface a specific hint instead
		// of the generic "no matching session" message — this is only a
		// best-effort lookup; an error here falls through to the empty
		// output below so the user still sees a reasonable response.
		recheck, recheckErr := c.context.Handoff(ctx, baseBuilder.AllowStale(true).Build())
		if recheckErr == nil {
			if pack, ok := recheck.Value(); ok {
				return xerrors.Errorf(
					Localize(
						"active session %s is older than %s and considered stale; pass --allow-stale or close it with session end",
						"active session %s は %s を超えており stale です。--allow-stale を指定するか session end で閉じてください",
					),
					pack.SessionID(),
					input.staleAfter,
				)
			}
		}
	}

	return writeHandoffText(output, result)
}

func writeHandoffText(output io.Writer, result types.Optional[apptypes.ContextPack]) error {
	if _, ok := result.Value(); !ok {
		if _, err := fmt.Fprintln(output, Localize("No matching session handoff.", "一致する session handoff はありません。")); err != nil {
			return xerrors.Errorf("failed to print empty handoff output: %w", err)
		}
		return nil
	}

	pack, _ := result.Value()
	if _, err := fmt.Fprintln(output, "TRACEARY HANDOFF"); err != nil {
		return xerrors.Errorf("failed to print handoff header: %w", err)
	}
	if _, err := fmt.Fprintf(output, "SESSION_ID: %s\n", pack.SessionID()); err != nil {
		return xerrors.Errorf("failed to print handoff session ID: %w", err)
	}
	if _, err := fmt.Fprintf(output, "WORKSPACE: %s\n", formatOptionalColumn(pack.Workspace().String())); err != nil {
		return xerrors.Errorf("failed to print handoff workspace: %w", err)
	}
	if pack.WorkspaceFallbackUsed() {
		if _, err := fmt.Fprintf(
			output,
			"NOTE: matched through parent workspace %s (requested %s)\n",
			pack.Workspace().String(),
			pack.RequestedWorkspace().String(),
		); err != nil {
			return xerrors.Errorf("failed to print handoff workspace fallback note: %w", err)
		}
	}
	if _, err := fmt.Fprintf(output, "LABEL: %s\n", formatOptionalColumn(pack.Label())); err != nil {
		return xerrors.Errorf("failed to print handoff label: %w", err)
	}
	if _, err := fmt.Fprintf(output, "STATUS: %s\n", formatOptionalColumn(pack.Status())); err != nil {
		return xerrors.Errorf("failed to print handoff status: %w", err)
	}
	if _, err := fmt.Fprintf(output, "TOTAL_EVENTS: %d\n", pack.TotalEvents()); err != nil {
		return xerrors.Errorf("failed to print handoff total events: %w", err)
	}
	if _, err := fmt.Fprintf(output, "COMMAND_COUNT: %d\n", pack.CommandCount()); err != nil {
		return xerrors.Errorf("failed to print handoff command count: %w", err)
	}
	if _, err := fmt.Fprintf(output, "AGENTS: %s\n", formatOptionalColumn(strings.Join(pack.Agents(), ", "))); err != nil {
		return xerrors.Errorf("failed to print handoff agents: %w", err)
	}
	if _, err := fmt.Fprintln(output, "WORKING_STATE:"); err != nil {
		return xerrors.Errorf("failed to print working-state heading: %w", err)
	}
	if _, err := fmt.Fprintf(output, "- session_summary: %s\n", formatOptionalColumn(pack.WorkingState().SessionSummary())); err != nil {
		return xerrors.Errorf("failed to print handoff session summary: %w", err)
	}
	if _, err := fmt.Fprintf(output, "- compact_summary: %s\n", formatOptionalColumn(pack.WorkingState().CompactSummary())); err != nil {
		return xerrors.Errorf("failed to print handoff compact summary: %w", err)
	}
	if _, err := fmt.Fprintln(output, "RECENT_COMMANDS:"); err != nil {
		return xerrors.Errorf("failed to print recent-commands heading: %w", err)
	}
	for _, command := range pack.RecentCommands() {
		if _, err := fmt.Fprintf(output, "- %s\n", command); err != nil {
			return xerrors.Errorf("failed to print handoff recent command: %w", err)
		}
	}
	if len(pack.RecentCommands()) == 0 {
		if _, err := fmt.Fprintln(output, "-"); err != nil {
			return xerrors.Errorf("failed to print empty recent-commands item: %w", err)
		}
	}
	if _, err := fmt.Fprintln(output, "RECENT_COMMAND_ITEMS:"); err != nil {
		return xerrors.Errorf("failed to print structured recent-commands heading: %w", err)
	}
	for _, item := range pack.RecentCommandItems() {
		extent := item.BodyExtent()
		original := "unknown"
		if value, ok := extent.OriginalBytes().Value(); ok {
			original = fmt.Sprintf("%d", value)
		}
		ingestTruncated := formatOptionalBool(extent.IngestTruncated())
		storageTruncated := formatOptionalBool(extent.StorageTruncated())
		if _, err := fmt.Fprintf(
			output,
			"- event_id=%s summary=%q returned_bytes=%d stored_bytes=%d original_bytes=%s response_truncated=%t ingest_truncated=%s storage_truncated=%s detail=%q\n",
			item.EventID(), item.Summary(), item.ReturnedBytes(), extent.StoredBytes(), original,
			item.ResponseTruncated(), ingestTruncated, storageTruncated, "traceary show "+item.EventID().String(),
		); err != nil {
			return xerrors.Errorf("failed to print structured recent command: %w", err)
		}
	}
	if len(pack.RecentCommandItems()) == 0 {
		if _, err := fmt.Fprintln(output, "-"); err != nil {
			return xerrors.Errorf("failed to print empty structured recent-command item: %w", err)
		}
	}
	if _, err := fmt.Fprintf(
		output,
		"MEMORY_COUNTS: accepted=%d candidate=%d\n",
		pack.AcceptedMemoryCount(),
		pack.CandidateMemoryCount(),
	); err != nil {
		return xerrors.Errorf("failed to print memory counts: %w", err)
	}
	if _, err := fmt.Fprintln(output, "MEMORIES:"); err != nil {
		return xerrors.Errorf("failed to print memories heading: %w", err)
	}
	for _, memory := range pack.Memories() {
		// Tag candidate (and any non-accepted) entries with a leading
		// status marker so the reader can tell pending review items
		// apart from curated ones. Accepted entries keep their
		// established two-bracket layout.
		statusPrefix := ""
		if memory.Status() != types.MemoryStatusAccepted {
			statusPrefix = fmt.Sprintf("[%s]", memory.Status())
		}
		if _, err := fmt.Fprintf(
			output,
			"- %s[%s][%s:%s] %s\n",
			statusPrefix,
			memory.MemoryType(),
			memory.Scope().Kind(),
			memory.Scope().Key(),
			memory.Fact(),
		); err != nil {
			return xerrors.Errorf("failed to print handoff memory: %w", err)
		}
	}
	if len(pack.Memories()) == 0 {
		if _, err := fmt.Fprintln(output, "-"); err != nil {
			return xerrors.Errorf("failed to print empty memories item: %w", err)
		}
	}
	if pack.CandidateMemoryCount() > 0 {
		if _, err := fmt.Fprintln(output, "MEMORY_NEEDS_REVIEW:"); err != nil {
			return xerrors.Errorf("failed to print memory needs-review heading: %w", err)
		}
		for _, memory := range pack.MemoryNeedsReview() {
			if _, err := fmt.Fprintf(
				output,
				"- [%s][%s:%s] %s\n",
				memory.MemoryType(),
				memory.Scope().Kind(),
				memory.Scope().Key(),
				memory.Fact(),
			); err != nil {
				return xerrors.Errorf("failed to print handoff memory candidate: %w", err)
			}
		}
		if len(pack.MemoryNeedsReview()) == 0 {
			if _, err := fmt.Fprintln(output, "- (candidate backlog omitted; pass --include-candidates to review)"); err != nil {
				return xerrors.Errorf("failed to print omitted memory candidates hint: %w", err)
			}
		}
	}

	return nil
}

func formatOptionalBool(value types.Optional[bool]) string {
	v, ok := value.Value()
	if !ok {
		return "unknown"
	}
	return fmt.Sprintf("%t", v)
}
