package cli

import (
	"fmt"
	"io"
	"strings"
	"time"

	"golang.org/x/xerrors"

	apptypes "github.com/duck8823/traceary/application/types"
	"github.com/duck8823/traceary/domain/model"
)

// searchSessionsJSONNotice is written to stderr — never stdout — whenever
// session-tier hits exist but `--json` could not carry them. `traceary search`
// is a Public command, so its top-level `--json` array stays an array for the
// whole of v0.34; the envelope that carries sessions lands in v0.35. Without
// this notice a scripted consumer searching old history would see an empty
// array and could not tell it apart from "no results".
var searchSessionsJSONNotice = Localize(
	"traceary: %d matching session(s) are not included in --json output. "+
		"Run the same search without --json to see them. "+
		"From v0.35 `search --json` emits an object with `events` and `sessions` instead of an array.\n",
	"traceary: 一致したセッション %d 件は --json 出力に含まれていません。"+
		"--json を外して同じ検索を実行すると確認できます。"+
		"v0.35 以降 `search --json` は配列ではなく `events` と `sessions` を持つオブジェクトを出力します。\n",
)

// warnSearchSessionsOmittedFromJSON reports session hits that the v0.34 JSON
// shape cannot represent. Silence here would be a false negative, so the notice
// is unconditional whenever hits were dropped.
func warnSearchSessionsOmittedFromJSON(warnWriter io.Writer, sessionCount int) {
	if warnWriter == nil || sessionCount == 0 {
		return
	}
	_, _ = fmt.Fprintf(warnWriter, searchSessionsJSONNotice, sessionCount)
}

// warnSearchSessionsSuppressedByKind reports that the SESSIONS group is empty
// only because --kind was applied. It states presence, not a count: an accurate
// count would have to reproduce the whole kind-less search, including the event
// page whose ids are excluded from the session tier, and the number would not
// change what the operator does about it.
func warnSearchSessionsSuppressedByKind(warnWriter io.Writer) {
	if warnWriter == nil {
		return
	}
	_, _ = fmt.Fprint(warnWriter, Localize(
		"traceary: matching sessions were suppressed because --kind cannot be applied to session summaries. Run the same search without --kind to see them.\n",
		"traceary: セッション要約には --kind を適用できないため、一致したセッションを表示していません。--kind を外して同じ検索を実行すると確認できます。\n",
	))
}

// writeSearchByFormat renders search results as either event-only output
// (byte-compatible with historical `traceary search` when sessions is empty)
// or labelled event/session groups when older session hits are present.
// JSON output is always the historical top-level event array; sessions reach
// the operator through warnSearchSessionsOmittedFromJSON until v0.35.
func writeSearchByFormat(
	output io.Writer,
	events []*model.Event,
	sessions []apptypes.SearchSessionHit,
	asJSON bool,
	jsonFieldsExplicit bool,
	textOpts eventTextFormatOptions,
	extrasFor compactExtrasResolver,
) error {
	if asJSON {
		return writeSearchJSON(output, events, jsonFieldsExplicit, textOpts.fields, extrasFor)
	}
	return writeSearchText(output, events, sessions, textOpts, extrasFor)
}

func writeSearchJSON(
	output io.Writer,
	events []*model.Event,
	jsonFieldsExplicit bool,
	fields []readFieldID,
	extrasFor compactExtrasResolver,
) error {
	eventPayload := make([]any, 0, len(events))
	if jsonFieldsExplicit {
		for _, event := range events {
			extras := compactRowExtras{}
			if extrasFor != nil {
				extras = extrasFor(event)
			}
			eventPayload = append(eventPayload, newEventFieldsOutput(event, fields, extras))
		}
	} else {
		for _, event := range events {
			eventPayload = append(eventPayload, newEventOutput(event))
		}
	}
	return writeJSON(output, eventPayload)
}

func writeSearchText(
	output io.Writer,
	events []*model.Event,
	sessions []apptypes.SearchSessionHit,
	textOpts eventTextFormatOptions,
	extrasFor compactExtrasResolver,
) error {
	if len(sessions) == 0 {
		// Keep recent-only searches byte-identical to the pre-session output.
		return writeEvents(output, events, textOpts, extrasFor)
	}

	if len(events) > 0 {
		if _, err := fmt.Fprintln(output, Localize("EVENTS (recent, full text)", "EVENTS（直近・全文）")); err != nil {
			return xerrors.Errorf("%s: %w", Localize("failed to print search results", "検索結果の出力に失敗しました"), err)
		}
		for _, event := range events {
			extras := compactRowExtras{}
			if extrasFor != nil {
				extras = extrasFor(event)
			}
			row := formatEventCompactRow(event, textOpts, extras)
			if textOpts.wide {
				row = formatEventWideRow(event, textOpts)
			}
			if _, err := fmt.Fprintln(output, "  "+row); err != nil {
				return xerrors.Errorf("%s: %w", Localize("failed to print event row", "イベント一覧行の出力に失敗しました"), err)
			}
		}
		if _, err := fmt.Fprintln(output); err != nil {
			return xerrors.Errorf("%s: %w", Localize("failed to print search results", "検索結果の出力に失敗しました"), err)
		}
	}

	if _, err := fmt.Fprintln(output, Localize("SESSIONS (older, from summaries)", "SESSIONS（過去・要約）")); err != nil {
		return xerrors.Errorf("%s: %w", Localize("failed to print search results", "検索結果の出力に失敗しました"), err)
	}
	for _, session := range sessions {
		if _, err := fmt.Fprintln(output, "  "+formatSearchSessionRow(session, textOpts)); err != nil {
			return xerrors.Errorf("%s: %w", Localize("failed to print session row", "セッション一覧行の出力に失敗しました"), err)
		}
	}
	return nil
}

func formatSearchSessionRow(hit apptypes.SearchSessionHit, opts eventTextFormatOptions) string {
	loc := time.Local
	if opts.utc {
		loc = time.UTC
	} else if opts.location != nil {
		loc = opts.location
	}
	started := hit.StartedAt().In(loc).Format("2006-01-02")
	summary := truncateMessage(strings.TrimSpace(hit.Summary()))
	if summary == "" {
		summary = "-"
	}
	return hit.SessionID().String() + "  " + started + "  " + summary
}
