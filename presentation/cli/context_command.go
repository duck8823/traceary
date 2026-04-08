package cli

import (
	"context"
	"fmt"
	"io"
	"strings"

	"github.com/spf13/cobra"
	"golang.org/x/xerrors"

	"github.com/duck8823/traceary/application/queryservice"
	"github.com/duck8823/traceary/domain/model"
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
		Short:   "次の AI session に渡す文脈を表示する",
		Args:    noArgsJP(),
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
	contextCmd.Flags().StringVar(&dbPath, "db-path", "", dbPathFlagUsage)
	contextCmd.Flags().StringVar(&sessionID, "session-id", "", "対象の session ID")
	contextCmd.Flags().StringVar(&client, "client", "", "作業主体の入口で絞り込む")
	contextCmd.Flags().StringVar(&agent, "agent", "", "作業主体で絞り込む")
	contextCmd.Flags().StringVar(&repo, "repo", "", "補助的なコンテキスト識別子で絞り込む")
	contextCmd.Flags().IntVar(&limit, "limit", 10, "表示件数")
	contextCmd.Flags().BoolVar(&asJSON, "json", false, "JSON 形式で出力する")

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
	ResolvedRepo      string      `json:"resolved_repo,omitempty"`
	Events            []eventJSON `json:"events"`
}

func (c *RootCLI) runContext(ctx context.Context, output io.Writer, input contextCommandInput) error {
	if c.initializeStoreUsecase == nil {
		return xerrors.Errorf("ストア初期化ユースケースが設定されていません")
	}
	if c.getContextQueryService == nil {
		return xerrors.Errorf("文脈クエリサービスが設定されていません")
	}

	resolvedPath, err := resolveDBPath(input.dbPath)
	if err != nil {
		return xerrors.Errorf("DB パスの解決に失敗しました: %w", err)
	}
	if err := c.initializeStoreUsecase.Run(ctx, resolvedPath); err != nil {
		return xerrors.Errorf("ストアの初期化に失敗しました: %w", err)
	}

	resolvedRepo := resolveRepoValue(ctx, input.repo)
	resolvedSessionID, err := c.resolveContextSessionID(ctx, resolvedPath, contextCommandInput{
		sessionID: input.sessionID,
		client:    input.client,
		agent:     input.agent,
		repo:      resolvedRepo,
	})
	if err != nil {
		return err
	}

	events, err := c.getContextQueryService.Run(ctx, resolvedPath, queryservice.GetContextInput{
		Repo:      resolvedRepo,
		SessionID: resolvedSessionID,
		Limit:     input.limit,
	})
	if err != nil {
		return xerrors.Errorf("文脈の取得に失敗しました: %w", err)
	}

	if input.asJSON {
		return writeContextJSON(output, resolvedSessionID, resolvedRepo, events)
	}

	return writeContextText(output, resolvedSessionID, resolvedRepo, events)
}

func (c *RootCLI) resolveContextSessionID(
	ctx context.Context,
	dbPath string,
	input contextCommandInput,
) (string, error) {
	trimmedSessionID := strings.TrimSpace(input.sessionID)
	if trimmedSessionID != "" {
		return trimmedSessionID, nil
	}
	if c.findLatestSessionQueryService == nil {
		return "", nil
	}

	event, err := c.findLatestSessionQueryService.Run(ctx, dbPath, queryservice.FindLatestSessionInput{
		Client: strings.TrimSpace(input.client),
		Agent:  strings.TrimSpace(input.agent),
		Repo:   strings.TrimSpace(input.repo),
	})
	if err != nil {
		if queryservice.IsSessionLookupNotFound(err) {
			return "", nil
		}
		return "", xerrors.Errorf("文脈用の直近 session 解決に失敗しました: %w", err)
	}

	return event.SessionID().String(), nil
}

func writeContextJSON(output io.Writer, sessionID string, repo string, events []*model.Event) error {
	serializedEvents := make([]eventJSON, 0, len(events))
	for _, event := range events {
		serializedEvents = append(serializedEvents, newEventJSON(event))
	}

	return writeJSON(output, contextOutput{
		ResolvedSessionID: sessionID,
		ResolvedRepo:      repo,
		Events:            serializedEvents,
	})
}

func writeContextText(output io.Writer, sessionID string, repo string, events []*model.Event) error {
	if _, err := fmt.Fprintln(output, "TRACEARY CONTEXT"); err != nil {
		return xerrors.Errorf("文脈ヘッダーの出力に失敗しました: %w", err)
	}
	if _, err := fmt.Fprintf(output, "SESSION_ID: %s\n", formatOptionalColumn(sessionID)); err != nil {
		return xerrors.Errorf("session ID の出力に失敗しました: %w", err)
	}
	if _, err := fmt.Fprintf(output, "REPO: %s\n", formatOptionalColumn(repo)); err != nil {
		return xerrors.Errorf("repo の出力に失敗しました: %w", err)
	}
	if _, err := fmt.Fprintln(output, "EVENTS:"); err != nil {
		return xerrors.Errorf("文脈イベント見出しの出力に失敗しました: %w", err)
	}
	if len(events) == 0 {
		if _, err := fmt.Fprintln(output, "- 一致する文脈はありません"); err != nil {
			return xerrors.Errorf("空文脈メッセージの出力に失敗しました: %w", err)
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
			formatOptionalColumn(event.Client()),
			event.Agent(),
			singleLineSummary(event.Body()),
		); err != nil {
			return xerrors.Errorf("文脈イベントの出力に失敗しました: %w", err)
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
