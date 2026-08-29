package cli

import (
	"context"
	"fmt"
	"io"
	"strings"

	"github.com/spf13/cobra"
	"golang.org/x/xerrors"

	"github.com/duck8823/traceary/application/queryservice"
	apptypes "github.com/duck8823/traceary/application/types"
	"github.com/duck8823/traceary/domain/model"
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
		return xerrors.New(Localize("initialize store usecase is not configured", "ストア初期化ユースケースが設定されていません"))
	}
	if c.event == nil {
		return xerrors.New(Localize("get event details query service is not configured", "イベント詳細クエリサービスが設定されていません"))
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
		"EVENT_ID: %s\nKIND: %s\nCLIENT: %s\nAGENT: %s\nSESSION_ID: %s\nWORKSPACE: %s\nSOURCE_HOOK: %s\nCREATED_AT: %s\nMESSAGE: %s\n",
		event.EventID(),
		event.Kind(),
		formatOptionalColumn(event.Client().String()),
		event.Agent(),
		event.SessionID(),
		formatOptionalColumn(event.Workspace().String()),
		formatOptionalColumn(event.SourceHook()),
		event.CreatedAt().UTC().Format("2006-01-02T15:04:05Z07:00"),
		eventBodyForDisplay(event),
	); err != nil {
		return xerrors.Errorf("%s: %w", Localize("failed to print event fields", "イベント共通項目の出力に失敗しました"), err)
	}

	auditOpt := eventDetails.CommandAudit()
	if _, ok := auditOpt.Value(); !ok {
		return nil
	}
	commandAudit, _ := auditOpt.Value()

	exitCodeDisplay := "-"
	if exitCode, ok := commandAudit.ExitCode().Value(); ok {
		exitCodeDisplay = fmt.Sprintf("%d", exitCode)
	}

	if metadata, ok := commandAudit.OutputMetadata().Value(); ok {
		if _, err := fmt.Fprintf(
			output,
			"\nCOMMAND: %s\nEXIT_CODE: %s\nINPUT_TRUNCATED: %t\nINPUT:\n%s\nOUTPUT_TRUNCATED: %t\nOUTPUT: %s\n",
			commandAudit.Command(),
			exitCodeDisplay,
			commandAudit.InputTruncated(),
			commandAudit.Input(),
			commandAudit.OutputTruncated(),
			Localize("(metadata only: read-only tool)", "(metadata only: 読み取り専用ツール)"),
		); err != nil {
			return xerrors.Errorf("%s: %w", Localize("failed to print command audit details", "command audit 詳細の出力に失敗しました"), err)
		}
		if paths := metadata.Paths(); len(paths) > 0 {
			if _, err := fmt.Fprintf(output, "  paths: %s\n", strings.Join(paths, ", ")); err != nil {
				return xerrors.Errorf("%s: %w", Localize("failed to print command audit details", "command audit 詳細の出力に失敗しました"), err)
			}
		}
		if _, err := fmt.Fprintf(output, "  bytes: %d\n  sha256: %s\n  truncated: %t\n", metadata.Bytes(), metadata.SHA256(), metadata.Truncated()); err != nil {
			return xerrors.Errorf("%s: %w", Localize("failed to print command audit details", "command audit 詳細の出力に失敗しました"), err)
		}
		return nil
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

func eventBodyForDisplay(event *model.Event) string {
	if event == nil {
		return ""
	}
	if !event.BodyAvailability().IsAvailable() {
		return Localize("[body unavailable: retention]", "[本文は保持ポリシーにより利用できません]")
	}
	if body := apptypes.ExtractPlainBody(event.Body()); strings.TrimSpace(body) != "" {
		return body
	}
	// command_executed no longer stores a composed body (#1675). List/search/
	// tail/context hydrate command_text via CommandOnlyPayload before display.
	// Do not fall back to command_name: a wrapper-stripped name looks like a
	// real command line ("go") and is worse than empty for consumers that
	// cannot tell the two apart (public JSON message field).
	if audit, ok := event.CommandAudit().Value(); ok && audit != nil {
		if command := strings.TrimSpace(audit.Command()); command != "" {
			return command
		}
	}
	return ""
}

// hydrateCommandLinesForDisplay decodes command_text onto listed events so
// eventBodyForDisplay can show the full command line without loading I/O.
func (c *RootCLI) hydrateCommandLinesForDisplay(ctx context.Context, events []*model.Event) error {
	if c == nil || c.event == nil {
		return nil
	}
	if err := c.event.HydrateCommandAudits(ctx, events, queryservice.CommandOnlyPayload()); err != nil {
		return xerrors.Errorf("%s: %w", Localize("failed to hydrate command audit payloads", "command audit ペイロードの復元に失敗しました"), err)
	}
	return nil
}
