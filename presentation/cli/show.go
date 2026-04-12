package cli

import (
	"context"
	"fmt"
	"io"

	"github.com/spf13/cobra"
	"golang.org/x/xerrors"

	apptypes "github.com/duck8823/traceary/application/types"
	"github.com/duck8823/traceary/domain/types"
)

func (c *RootCLI) newShowCommand() *cobra.Command {
	var (
		dbPath string
		asJSON bool
	)

	showCmd := &cobra.Command{
		Use:   "show <event-id>",
		Short: Localize("Show event details", "イベント詳細を表示する"),
		Args:  exactArgsLocalized(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return c.runShow(cmd.Context(), cmd.OutOrStdout(), dbPath, args[0], asJSON)
		},
	}
	showCmd.Flags().StringVar(&dbPath, "db-path", "", dbPathFlagUsage())
	showCmd.Flags().BoolVar(&asJSON, "json", false, Localize("print JSON output", "JSON 形式で出力する"))

	return showCmd
}

func (c *RootCLI) runShow(ctx context.Context, output io.Writer, dbPath string, eventID string, asJSON bool) error {
	if c.storeManagement == nil {
		return xerrors.Errorf(Localize("initialize store usecase is not configured", "ストア初期化ユースケースが設定されていません"))
	}
	if c.event == nil {
		return xerrors.Errorf(Localize("get event details query service is not configured", "イベント詳細クエリサービスが設定されていません"))
	}

	resolvedDBPath, err := resolveDBPath(dbPath)
	if err != nil {
		return xerrors.Errorf("%s: %w", Localize("failed to resolve DB path", "DB パスの解決に失敗しました"), err)
	}
	c.applyDatabasePath(resolvedDBPath)
	if err := c.storeManagement.Initialize(ctx); err != nil {
		return xerrors.Errorf("%s: %w", Localize("failed to initialize store", "ストアの初期化に失敗しました"), err)
	}

	eventDetails, err := c.event.Show(ctx, types.EventID(eventID))
	if err != nil {
		return xerrors.Errorf("%s: %w", Localize("failed to get event details", "イベント詳細の取得に失敗しました"), err)
	}

	if err := writeEventDetailsByFormat(output, eventDetails, asJSON); err != nil {
		return xerrors.Errorf("%s: %w", Localize("failed to print event details", "イベント詳細の出力に失敗しました"), err)
	}

	return nil
}

func writeEventDetails(output io.Writer, eventDetails apptypes.EventDetails) error {
	event := eventDetails.Event()
	if _, err := fmt.Fprintf(
		output,
		"EVENT_ID: %s\nKIND: %s\nCLIENT: %s\nAGENT: %s\nSESSION_ID: %s\nWORKSPACE: %s\nCREATED_AT: %s\nMESSAGE: %s\n",
		event.EventID(),
		event.Kind(),
		formatOptionalColumn(event.Client().String()),
		event.Agent(),
		event.SessionID(),
		formatOptionalColumn(event.Workspace().String()),
		event.CreatedAt().UTC().Format("2006-01-02T15:04:05Z07:00"),
		event.Body(),
	); err != nil {
		return xerrors.Errorf("%s: %w", Localize("failed to print event fields", "イベント共通項目の出力に失敗しました"), err)
	}

	auditOpt := eventDetails.CommandAudit()
	if !auditOpt.IsPresent() {
		return nil
	}
	commandAudit, _ := auditOpt.Get()

	exitCodeDisplay := "-"
	if exitCode, ok := commandAudit.ExitCode().Get(); ok {
		exitCodeDisplay = fmt.Sprintf("%d", exitCode)
	}

	if _, err := fmt.Fprintf(
		output,
		"\nCOMMAND: %s\nEXIT_CODE: %s\nINPUT_TRUNCATED: %t\nINPUT:\n%s\nOUTPUT_TRUNCATED: %t\nOUTPUT:\n%s\n",
		commandAudit.Command(),
		exitCodeDisplay,
		commandAudit.InputTruncated(),
		commandAudit.Input(),
		commandAudit.OutputTruncated(),
		commandAudit.Output(),
	); err != nil {
		return xerrors.Errorf("%s: %w", Localize("failed to print command audit details", "command audit 詳細の出力に失敗しました"), err)
	}

	return nil
}
