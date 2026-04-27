package cli

import (
	"context"
	"io"
	"strings"

	"github.com/spf13/cobra"
	"golang.org/x/xerrors"

	apptypes "github.com/duck8823/traceary/application/types"
	"github.com/duck8823/traceary/domain/types"
)

func (c *RootCLI) newMemoryExtractCommand() *cobra.Command {
	input := memoryExtractCommandInput{}
	cmd := &cobra.Command{
		Use:   "extract",
		Short: Localize("Extract candidate durable memories from an existing session", "既存 session から candidate durable memory を抽出する"),
		Long: Localize(
			"Extract candidate durable memories from session summaries, compact summaries, prompt events, and note/review signals without auto-accepting them.",
			"session summary、compact summary、prompt event、note/review signal から candidate durable memory を抽出します。auto-accept は行いません。",
		),
		Args: noArgsLocalized(),
		RunE: func(cmd *cobra.Command, _ []string) error {
			return c.runMemoryExtract(cmd.Context(), cmd.OutOrStdout(), input)
		},
	}
	cmd.Flags().StringVar(&input.dbPath, "db-path", "", dbPathFlagUsage())
	cmd.Flags().StringVar(&input.sessionID, "session-id", "", Localize("target session ID (defaults to the active/latest session for the workspace)", "対象 session ID (省略時は workspace の active/latest session)"))
	cmd.Flags().StringVar(&input.workspace, "workspace", "", Localize("workspace used to resolve the target session", "対象 session 解決に使う workspace"))
	cmd.Flags().IntVar(&input.eventLimit, "event-limit", 5, Localize("maximum number of recent events to inspect per signal kind", "signal kind ごとに調べる recent event 数"))
	cmd.Flags().IntVar(&input.candidateLimit, "candidate-limit", 10, Localize("maximum number of candidates to create", "作成する candidate 数の上限"))
	cmd.Flags().BoolVar(&input.debugSignals, "debug-signals", false, Localize("explain segment-level extraction decisions without creating candidates", "candidate を作成せず segment 単位の抽出判断を説明する"))
	cmd.Flags().BoolVar(&input.asJSON, "json", false, Localize("print JSON output", "JSON 形式で出力する"))
	return cmd
}

func (c *RootCLI) runMemoryExtract(ctx context.Context, output io.Writer, input memoryExtractCommandInput) error {
	if c.storeManagement == nil {
		return xerrors.Errorf(Localize("initialize store usecase is not configured", "ストア初期化ユースケースが設定されていません"))
	}
	if c.memory == nil {
		return xerrors.Errorf(Localize("memory usecase is not configured", "memory extraction ユースケースが設定されていません"))
	}
	if input.eventLimit < 0 {
		return xerrors.Errorf(Localize("event-limit must be greater than or equal to 0", "event-limit は 0 以上である必要があります"))
	}
	if input.candidateLimit <= 0 {
		return xerrors.Errorf(Localize("candidate-limit must be greater than or equal to 1", "candidate-limit は 1 以上である必要があります"))
	}
	if err := c.initializeStore(ctx, input.dbPath); err != nil {
		return err
	}

	resolvedWorkspace := resolveWorkspaceValue(ctx, input.workspace)
	resolvedSessionID, resolvedLookupWorkspace, err := c.resolveMemoryExtractTargetSession(ctx, input.sessionID, resolvedWorkspace)
	if err != nil {
		return err
	}
	if strings.TrimSpace(resolvedSessionID) == "" {
		return xerrors.Errorf(Localize("failed to resolve a target session for memory extraction", "memory extraction 対象の session を解決できませんでした"))
	}

	criteria := apptypes.NewMemoryExtractionCriteriaBuilder().
		SessionID(types.SessionID(resolvedSessionID)).
		Workspace(types.Workspace(resolvedLookupWorkspace)).
		EventLimit(input.eventLimit).
		CandidateLimit(input.candidateLimit).
		Build()
	if input.debugSignals {
		report, err := c.memory.ExplainExtraction(ctx, criteria)
		if err != nil {
			return xerrors.Errorf("%s: %w", Localize("failed to explain durable-memory extraction", "durable memory extraction の説明に失敗しました"), err)
		}
		if err := writeMemoryExtractionDebugReport(output, report, input.asJSON); err != nil {
			return xerrors.Errorf("%s: %w", Localize("failed to print extraction debug report", "抽出 debug report の出力に失敗しました"), err)
		}
		return nil
	}

	details, err := c.memory.Extract(
		ctx,
		criteria,
	)
	if err != nil {
		return xerrors.Errorf("%s: %w", Localize("failed to extract durable-memory candidates", "durable memory candidate の抽出に失敗しました"), err)
	}
	if err := writeExtractedMemoryCandidatesByFormat(output, details, input.asJSON); err != nil {
		return xerrors.Errorf("%s: %w", Localize("failed to print extracted durable-memory candidates", "抽出した durable memory candidate の出力に失敗しました"), err)
	}
	return nil
}

func (c *RootCLI) resolveMemoryExtractTargetSession(ctx context.Context, sessionID string, workspace string) (string, string, error) {
	trimmedSessionID := strings.TrimSpace(sessionID)
	if trimmedSessionID != "" {
		return trimmedSessionID, "", nil
	}
	if c.session == nil {
		return "", workspace, nil
	}

	lookupCriteria := apptypes.NewSessionLookupCriteriaBuilder().
		Workspace(types.Workspace(strings.TrimSpace(workspace))).
		Build()
	active, err := c.session.Active(ctx, lookupCriteria)
	if err != nil {
		return "", workspace, xerrors.Errorf("%s: %w", Localize("failed to resolve active session for memory extraction", "memory extraction 用の active session 解決に失敗しました"), err)
	}
	if event, ok := active.Value(); ok && event != nil {
		return event.SessionID().String(), workspace, nil
	}

	latest, err := c.session.Latest(ctx, lookupCriteria)
	if err != nil {
		return "", workspace, xerrors.Errorf("%s: %w", Localize("failed to resolve latest session for memory extraction", "memory extraction 用の latest session 解決に失敗しました"), err)
	}
	if event, ok := latest.Value(); ok && event != nil {
		return event.SessionID().String(), workspace, nil
	}

	return "", workspace, nil
}
