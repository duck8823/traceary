package cli

import (
	"fmt"
	"io"

	"golang.org/x/xerrors"

	"github.com/duck8823/traceary/domain/model"
)

func writeEvents(output io.Writer, events []*model.Event) error {
	if len(events) == 0 {
		if _, err := fmt.Fprintln(output, "一致する記録はありません"); err != nil {
			return xerrors.Errorf("空一覧メッセージの出力に失敗しました: %w", err)
		}
		return nil
	}

	if _, err := fmt.Fprintln(output, "CREATED_AT\tKIND\tCLIENT\tAGENT\tSESSION_ID\tREPO\tMESSAGE"); err != nil {
		return xerrors.Errorf("一覧ヘッダーの出力に失敗しました: %w", err)
	}
	for _, event := range events {
		if _, err := fmt.Fprintf(
			output,
			"%s\t%s\t%s\t%s\t%s\t%s\t%s\n",
			event.CreatedAt().UTC().Format("2006-01-02T15:04:05Z07:00"),
			event.Kind(),
			formatOptionalColumn(event.Client()),
			event.Agent(),
			event.SessionID(),
			formatOptionalColumn(event.Repo()),
			event.Body(),
		); err != nil {
			return xerrors.Errorf("イベント一覧行の出力に失敗しました: %w", err)
		}
	}

	return nil
}

func formatOptionalColumn(value string) string {
	if value == "" {
		return "-"
	}

	return value
}
