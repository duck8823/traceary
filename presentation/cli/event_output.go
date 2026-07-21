package cli

import (
	"fmt"
	"io"
	"strings"
	"unicode"

	"golang.org/x/xerrors"

	apptypes "github.com/duck8823/traceary/application/types"
	"github.com/duck8823/traceary/domain/model"
)

const messageColumnMaxWidth = 80

// compactExtrasResolver returns hydrated extras for the given event. It is
// provided by CLI commands that need to lazily fetch per-event data (such as
// command_audit.exit_code) only when the resolved field list requires it.
// A nil resolver renders zero-value extras for every event.
type compactExtrasResolver func(*model.Event) compactRowExtras

func writeEventsByFormat(
	output io.Writer,
	events []*model.Event,
	asJSON bool,
	jsonFieldsExplicit bool,
	textOpts eventTextFormatOptions,
	extrasFor compactExtrasResolver,
) error {
	if asJSON {
		if jsonFieldsExplicit {
			return writeEventsJSONFields(output, events, textOpts.fields, extrasFor)
		}
		return writeEventsJSON(output, events)
	}

	return writeEvents(output, events, textOpts, extrasFor)
}

func writeEvents(
	output io.Writer,
	events []*model.Event,
	textOpts eventTextFormatOptions,
	extrasFor compactExtrasResolver,
) error {
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
		extras := compactRowExtras{}
		if extrasFor != nil {
			extras = extrasFor(event)
		}
		if _, err := fmt.Fprintln(output, formatEventCompactRow(event, textOpts, extras)); err != nil {
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
	// Drop ASCII / Unicode control characters before collapsing whitespace.
	// Without this, an event body that embeds ESC / CSI / OSC sequences
	// could hijack the user's terminal once we render it into tabular
	// (wide) or compact rows — including the ANSI highlight path added in
	// v0.7-7, but also applicable to wide mode.
	return strings.Join(strings.Fields(stripTerminalControlChars(value)), " ")
}

// stripTerminalControlChars removes control runes (including the ESC that
// starts ANSI escape sequences) while keeping tab, newline, and carriage
// return as whitespace so normalizeTabularColumn can collapse them.
func stripTerminalControlChars(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range s {
		switch r {
		case '\t', '\n', '\r':
			b.WriteRune(' ')
			continue
		}
		if unicode.IsControl(r) {
			continue
		}
		b.WriteRune(r)
	}
	return b.String()
}

func formatOptionalColumn(value string) string {
	if value == "" {
		return "-"
	}

	return value
}
