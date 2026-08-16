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

// searchJSONEnvelope is the v0.35+ contract for `traceary search --json`.
// Both keys are always present so an empty tier is distinguishable from a
// missing field; empty arrays mean the tier was consulted and returned no hits.
type searchJSONEnvelope struct {
	Events   []any                     `json:"events"`
	Sessions []searchSessionJSONOutput `json:"sessions"`
}

// searchSessionJSONOutput is the stable object shape for session-tier hits.
// Field names match the MCP search session row so CLI and MCP stay aligned.
type searchSessionJSONOutput struct {
	SessionID  string `json:"session_id"`
	Summary    string `json:"summary"`
	EventCount int    `json:"event_count"`
	StartedAt  string `json:"started_at"`
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

// warnSearchSessionsProjectionNotReady reports that the SESSIONS group was
// not consulted because the session-tier projection has no readable generation.
//
// Readiness is exactly `state == complete` with an active generation
// (event_search_query.go:66-73), so the first clause states the state rather
// than claiming no complete generation exists: rows from an earlier complete
// generation can still be present while the state has moved on.
//
// The notice deliberately names no recovery command. The recovery depends on
// the state and the phase, and every attempt to compress that into one line
// produced a sentence that was false somewhere: "the rebuild advances on
// ordinary opens" is wrong for a parked generation; "watch whether the
// checkpoint moves" is not a progress signal, because inventory advances a
// separate cursor and cleanup deletes rows while holding the checkpoint; and
// "`start` replaces the generation" is refused outright while the state is
// rebuilding (search_projection_usecase.go:59-61). A notice that names a command
// the store will reject creates the stall it exists to prevent.
//
// So it states the one fact that holds in every state that can reach it, and
// sends the reader to `status` for the state and to the rebuild document for
// what that state needs. The document has room for the whole table; stderr does
// not.
func warnSearchSessionsProjectionNotReady(warnWriter io.Writer) {
	if warnWriter == nil {
		return
	}
	_, _ = fmt.Fprint(warnWriter, Localize(
		"traceary: the session tier was not consulted because the search projection is not in the `complete` state, so sessions that match only their summary or keywords are not listed. Run `traceary store search-projection status` for the current state, and see docs/search-projection-rebuild.md for what that state needs.\n",
		"traceary: search projection が `complete` 状態でないため、session tier は参照されませんでした。要約やキーワードだけが一致する session は一覧に出ません。現在の state は `traceary store search-projection status` で確認し、その state に必要な操作は docs/search-projection-rebuild.ja.md を参照してください。\n",
	))
}

// warnSearchSessionsProjectionReadinessUnknown reports that an empty SESSIONS
// group could not be attributed, because the readiness check itself failed.
// Staying silent here would restore exactly the ambiguity the not-ready notice
// exists to remove: the reader could not tell a refused tier from a consulted
// one, and nothing would say so.
func warnSearchSessionsProjectionReadinessUnknown(warnWriter io.Writer) {
	if warnWriter == nil {
		return
	}
	_, _ = fmt.Fprint(warnWriter, Localize(
		"traceary: could not determine whether the search projection is ready, so the empty SESSIONS group may be ambiguous. Run `traceary store search-projection status` to inspect it.\n",
		"traceary: search projection の準備状態を確認できなかったため、空の SESSIONS グループの意味を判定できません。`traceary store search-projection status` で状態を確認してください。\n",
	))
}

// searchSessionNotices collects the stderr advisories a session-tier lookup can
// raise. They answer different questions — "sessions matched but --kind hid
// them", "the tier was refused", "the tier's state is unknown" — so none may
// swallow another, and each is a no-op when its own condition does not hold.
// Gathering them here keeps that rule in one place rather than repeated at
// every call site of searchProjectionSessions.
type searchSessionNotices struct {
	kindSuppressed     bool
	projectionNotReady bool
	readinessUnknown   bool
}

func (n searchSessionNotices) write(warnWriter io.Writer) {
	if n.kindSuppressed {
		warnSearchSessionsSuppressedByKind(warnWriter)
	}
	if n.projectionNotReady {
		warnSearchSessionsProjectionNotReady(warnWriter)
	}
	if n.readinessUnknown {
		warnSearchSessionsProjectionReadinessUnknown(warnWriter)
	}
}

// writeSearchByFormat renders search results as either the events/sessions
// JSON object or labelled text groups. Text output stays event-only when the
// session tier is empty so recent-only searches stay byte-identical.
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
		return writeSearchJSON(output, events, sessions, jsonFieldsExplicit, textOpts.fields, extrasFor)
	}
	return writeSearchText(output, events, sessions, textOpts, extrasFor)
}

func writeSearchJSON(
	output io.Writer,
	events []*model.Event,
	sessions []apptypes.SearchSessionHit,
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
	return writeSearchJSONEnvelope(output, eventPayload, sessions)
}

// writeSearchMetadataJSON writes the search envelope when --json --fields
// selects a body-free metadata projection for the event tier.
func writeSearchMetadataJSON(
	output io.Writer,
	metadata []apptypes.EventMetadata,
	sessions []apptypes.SearchSessionHit,
	fields []readFieldID,
) error {
	eventPayload := make([]any, 0, len(metadata))
	for _, event := range metadata {
		eventPayload = append(eventPayload, newEventMetadataFieldsOutput(event, fields))
	}
	return writeSearchJSONEnvelope(output, eventPayload, sessions)
}

func writeSearchJSONEnvelope(
	output io.Writer,
	events []any,
	sessions []apptypes.SearchSessionHit,
) error {
	sessionPayload := make([]searchSessionJSONOutput, 0, len(sessions))
	for _, hit := range sessions {
		sessionPayload = append(sessionPayload, newSearchSessionJSONOutput(hit))
	}
	return writeJSON(output, searchJSONEnvelope{
		Events:   events,
		Sessions: sessionPayload,
	})
}

func newSearchSessionJSONOutput(hit apptypes.SearchSessionHit) searchSessionJSONOutput {
	return searchSessionJSONOutput{
		SessionID:  hit.SessionID().String(),
		Summary:    hit.Summary(),
		EventCount: hit.EventCount(),
		StartedAt:  formatJSONTime(hit.StartedAt()),
	}
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
		if _, err := fmt.Fprintln(output, Localize("EVENTS (literal matches)", "EVENTS（本文一致）")); err != nil {
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

	if _, err := fmt.Fprintln(output, Localize("SESSIONS (summary or keyword matches)", "SESSIONS（要約・キーワード一致）")); err != nil {
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
