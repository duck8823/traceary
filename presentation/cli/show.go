package cli

import (
	"context"
	"fmt"
	"io"

	"github.com/spf13/cobra"
	"golang.org/x/xerrors"

	"github.com/duck8823/traceary/application/queryservice"
)

func (c *RootCLI) newShowCommand() *cobra.Command {
	var dbPath string

	showCmd := &cobra.Command{
		Use:   "show <event-id>",
		Short: "イベント詳細を表示する",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return c.runShow(cmd.Context(), cmd.OutOrStdout(), dbPath, args[0])
		},
	}
	showCmd.Flags().StringVar(&dbPath, "db-path", "", "SQLite DB パス")

	return showCmd
}

func (c *RootCLI) runShow(ctx context.Context, output io.Writer, dbPath string, eventID string) error {
	if c.initializeStoreUsecase == nil {
		return xerrors.Errorf("ストア初期化ユースケースが設定されていません")
	}
	if c.getEventDetailsQueryService == nil {
		return xerrors.Errorf("イベント詳細クエリサービスが設定されていません")
	}

	resolvedPath, err := resolveDBPath(dbPath)
	if err != nil {
		return xerrors.Errorf("DB パスの解決に失敗しました: %w", err)
	}
	if err := c.initializeStoreUsecase.Run(ctx, resolvedPath); err != nil {
		return xerrors.Errorf("ストアの初期化に失敗しました: %w", err)
	}

	eventDetails, err := c.getEventDetailsQueryService.Run(ctx, resolvedPath, eventID)
	if err != nil {
		return xerrors.Errorf("イベント詳細の取得に失敗しました: %w", err)
	}

	if err := writeEventDetails(output, eventDetails); err != nil {
		return xerrors.Errorf("イベント詳細の出力に失敗しました: %w", err)
	}

	return nil
}

func writeEventDetails(output io.Writer, eventDetails *queryservice.EventDetails) error {
	if eventDetails == nil {
		return xerrors.Errorf("イベント詳細は nil にできません")
	}

	event := eventDetails.Event()
	if _, err := fmt.Fprintf(
		output,
		"EVENT_ID: %s\nKIND: %s\nCLIENT: %s\nAGENT: %s\nSESSION_ID: %s\nREPO: %s\nCREATED_AT: %s\nMESSAGE: %s\n",
		event.EventID(),
		event.Kind(),
		formatOptionalColumn(event.Client()),
		event.Agent(),
		event.SessionID(),
		formatOptionalColumn(event.Repo()),
		event.CreatedAt().UTC().Format("2006-01-02T15:04:05Z07:00"),
		event.Body(),
	); err != nil {
		return xerrors.Errorf("イベント共通項目の出力に失敗しました: %w", err)
	}

	commandAudit := eventDetails.CommandAudit()
	if commandAudit == nil {
		return nil
	}

	if _, err := fmt.Fprintf(
		output,
		"\nCOMMAND: %s\nINPUT_TRUNCATED: %t\nINPUT:\n%s\nOUTPUT_TRUNCATED: %t\nOUTPUT:\n%s\n",
		commandAudit.Command(),
		commandAudit.InputTruncated(),
		commandAudit.Input(),
		commandAudit.OutputTruncated(),
		commandAudit.Output(),
	); err != nil {
		return xerrors.Errorf("command audit 詳細の出力に失敗しました: %w", err)
	}

	return nil
}
