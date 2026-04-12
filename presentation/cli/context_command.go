package cli

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"strings"

	"github.com/spf13/cobra"
	"golang.org/x/xerrors"

	"github.com/duck8823/traceary/application/usecase"
	"github.com/duck8823/traceary/domain/model"
	"github.com/duck8823/traceary/domain/types"
)

func (c *RootCLI) newContextCommand() *cobra.Command {
	var (
		dbPath    string
		sessionID string
		client    string
		agent     string
		repo      string
		limit     int
		asJSON    bool
	)

	contextCmd := &cobra.Command{
		Use:     "context",
		Aliases: []string{"handoff"},
		Short:   Localize("Print context for the next AI session", "次の AI session に渡す文脈を表示する"),
		Args:    noArgsLocalized(),
		RunE: func(cmd *cobra.Command, _ []string) error {
			return c.runContext(cmd.Context(), cmd.OutOrStdout(), contextCommandInput{
				dbPath:    dbPath,
				sessionID: sessionID,
				client:    client,
				agent:     agent,
				repo:      repo,
				limit:     limit,
				asJSON:    asJSON,
			})
		},
	}
	contextCmd.Flags().StringVar(&dbPath, "db-path", "", dbPathFlagUsage())
	contextCmd.Flags().StringVar(&sessionID, "session-id", "", Localize("target session ID", "対象の session ID"))
	contextCmd.Flags().StringVar(&client, "client", "", Localize("filter by client", "作業主体の入口で絞り込む"))
	contextCmd.Flags().StringVar(&agent, "agent", "", Localize("filter by agent", "作業主体で絞り込む"))
	contextCmd.Flags().StringVar(&repo, "workspace", "", Localize("filter by auxiliary work context identifier", "補助的なコンテキスト識別子で絞り込む"))
	contextCmd.Flags().IntVar(&limit, "limit", 10, Localize("maximum number of events to include", "表示件数"))
	contextCmd.Flags().BoolVar(&asJSON, "json", false, Localize("print JSON output", "JSON 形式で出力する"))

	return contextCmd
}

type contextCommandInput struct {
	dbPath    string
	sessionID string
	client    string
	agent     string
	repo      string
	limit     int
	asJSON    bool
}

type contextOutput struct {
	ResolvedSessionID string      `json:"resolved_session_id,omitempty"`
	ResolvedWorkspace      string      `json:"resolved_workspace,omitempty"`
	Events            []eventJSON `json:"events"`
}

func (c *RootCLI) runContext(ctx context.Context, output io.Writer, input contextCommandInput) error {
	if c.storeManagement == nil {
		return xerrors.Errorf(Localize("initialize store usecase is not configured", "ストア初期化ユースケースが設定されていません"))
	}
	if c.event == nil {
		return xerrors.Errorf(Localize("get context query service is not configured", "文脈クエリサービスが設定されていません"))
	}
	if input.limit <= 0 {
		return xerrors.Errorf(Localize("limit must be greater than or equal to 1", "limit は 1 以上である必要があります"))
	}

	_, err := resolveDBPath(input.dbPath)
	if err != nil {
		return xerrors.Errorf("%s: %w", Localize("failed to resolve DB path", "DB パスの解決に失敗しました"), err)
	}
	if err := c.storeManagement.Initialize(ctx); err != nil {
		return xerrors.Errorf("%s: %w", Localize("failed to initialize store", "ストアの初期化に失敗しました"), err)
	}

	resolvedWorkspace := resolveWorkspaceValue(ctx, input.repo)
	resolvedSessionID, err := c.resolveContextSessionID(ctx, contextCommandInput{
		sessionID: input.sessionID,
		client:    input.client,
		agent:     input.agent,
		repo:      resolvedWorkspace,
	})
	if err != nil {
		return err
	}

	events, err := c.event.Context(ctx, usecase.EventContextCriteria{
		Workspace: types.Workspace(resolvedWorkspace),
		SessionID: types.SessionID(resolvedSessionID),
		Limit:     input.limit,
	})
	if err != nil {
		return xerrors.Errorf("%s: %w", Localize("failed to get context", "文脈の取得に失敗しました"), err)
	}

	if input.asJSON {
		return writeContextJSON(output, resolvedSessionID, resolvedWorkspace, events)
	}

	return writeContextText(output, resolvedSessionID, resolvedWorkspace, events)
}

func (c *RootCLI) resolveContextSessionID(
	ctx context.Context,
	input contextCommandInput,
) (string, error) {
	trimmedSessionID := strings.TrimSpace(input.sessionID)
	if trimmedSessionID != "" {
		return trimmedSessionID, nil
	}
	if c.session == nil {
		slog.Debug("no query service configured for context session resolution")
		return "", nil
	}

	result, err := c.session.Active(ctx, usecase.SessionLookupCriteria{
		Client:    types.Client(strings.TrimSpace(input.client)),
		Agent:     types.Agent(strings.TrimSpace(input.agent)),
		Workspace: types.Workspace(strings.TrimSpace(input.repo)),
	})
	if err != nil {
		return "", xerrors.Errorf("%s: %w", Localize("failed to resolve latest session for context", "文脈用の直近 session 解決に失敗しました"), err)
	}
	if !result.IsPresent() {
		slog.Debug("no session found for context, using empty session", "client", input.client, "agent", input.agent, "workspace", input.repo)
		return "", nil
	}

	event, _ := result.Get()
	return event.SessionID().String(), nil
}

func writeContextJSON(output io.Writer, sessionID string, repo string, events []*model.Event) error {
	serializedEvents := make([]eventJSON, 0, len(events))
	for _, event := range events {
		serializedEvents = append(serializedEvents, newEventJSON(event))
	}

	return writeJSON(output, contextOutput{
		ResolvedSessionID: sessionID,
		ResolvedWorkspace: repo,
		Events:            serializedEvents,
	})
}

func writeContextText(output io.Writer, sessionID string, repo string, events []*model.Event) error {
	if _, err := fmt.Fprintln(output, "TRACEARY CONTEXT"); err != nil {
		return xerrors.Errorf("%s: %w", Localize("failed to print context header", "文脈ヘッダーの出力に失敗しました"), err)
	}
	if _, err := fmt.Fprintf(output, "SESSION_ID: %s\n", formatOptionalColumn(sessionID)); err != nil {
		return xerrors.Errorf("%s: %w", Localize("failed to print session ID", "session ID の出力に失敗しました"), err)
	}
	if _, err := fmt.Fprintf(output, "WORKSPACE: %s\n", formatOptionalColumn(repo)); err != nil {
		return xerrors.Errorf("%s: %w", Localize("failed to print workspace", "workspace の出力に失敗しました"), err)
	}
	if _, err := fmt.Fprintln(output, "EVENTS:"); err != nil {
		return xerrors.Errorf("%s: %w", Localize("failed to print context events heading", "文脈イベント見出しの出力に失敗しました"), err)
	}
	if len(events) == 0 {
		if _, err := fmt.Fprintln(output, Localize("- No matching context.", "- 一致する文脈はありません")); err != nil {
			return xerrors.Errorf("%s: %w", Localize("failed to print empty context message", "空文脈メッセージの出力に失敗しました"), err)
		}
		return nil
	}

	for _, event := range events {
		if _, err := fmt.Fprintf(
			output,
			"- %s [%s] %s %s/%s %s\n",
			event.CreatedAt().UTC().Format("2006-01-02T15:04:05Z07:00"),
			event.Kind(),
			event.EventID(),
			formatOptionalColumn(event.Client().String()),
			event.Agent(),
			singleLineSummary(event.Body()),
		); err != nil {
			return xerrors.Errorf("%s: %w", Localize("failed to print context event", "文脈イベントの出力に失敗しました"), err)
		}
	}

	return nil
}

func singleLineSummary(value string) string {
	fields := strings.Fields(value)
	if len(fields) == 0 {
		return "-"
	}

	return strings.Join(fields, " ")
}
