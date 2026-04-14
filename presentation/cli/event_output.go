package cli

import (
	"fmt"
	"io"
	"strings"

	"golang.org/x/xerrors"

	apptypes "github.com/duck8823/traceary/application/types"
	"github.com/duck8823/traceary/domain/model"
)

const messageColumnMaxWidth = 80

func writeEventsByFormat(output io.Writer, events []*model.Event, asJSON bool, textOpts eventTextFormatOptions) error {
	if asJSON {
		return writeEventsJSON(output, events)
	}

	return writeEvents(output, events, textOpts)
}

func writeEvents(output io.Writer, events []*model.Event, textOpts eventTextFormatOptions) error {
	if len(events) == 0 {
		if _, err := fmt.Fprintln(output, Localize("No matching records.", "一致する記録はありません")); err != nil {
			return xerrors.Errorf("%s: %w", Localize("failed to print empty list message", "空一覧メッセージの出力に失敗しました"), err)
		}
		return nil
	}

	if textOpts.wide {
		if _, err := fmt.Fprintln(output, formatEventWideHeader()); err != nil {
			return xerrors.Errorf("%s: %w", Localize("failed to print list header", "一覧ヘッダーの出力に失敗しました"), err)
		}
		for _, event := range events {
			if _, err := fmt.Fprintln(output, formatEventWideRow(event, textOpts)); err != nil {
				return xerrors.Errorf("%s: %w", Localize("failed to print event row", "イベント一覧行の出力に失敗しました"), err)
			}
		}
		return nil
	}

	for _, event := range events {
		if _, err := fmt.Fprintln(output, formatEventCompactRow(event, textOpts)); err != nil {
			return xerrors.Errorf("%s: %w", Localize("failed to print event row", "イベント一覧行の出力に失敗しました"), err)
		}
	}
	return nil
}

func writeEventDetailsByFormat(output io.Writer, eventDetails apptypes.EventDetails, asJSON bool) error {
	if asJSON {
		return writeEventDetailsJSON(output, eventDetails)
	}

	return writeEventDetails(output, eventDetails)
}

func truncateMessage(s string) string {
	normalized := normalizeTabularColumn(s)
	if len([]rune(normalized)) <= messageColumnMaxWidth {
		return normalized
	}
	return string([]rune(normalized)[:messageColumnMaxWidth]) + "…"
}

func normalizeTabularColumn(value string) string {
	return strings.Join(strings.Fields(value), " ")
}

func formatOptionalColumn(value string) string {
	if value == "" {
		return "-"
	}

	return value
}
