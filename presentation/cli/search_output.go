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
	Tier       string `json:"tier,omitempty"`
}

type searchEventJSONOutput struct {
	event
	Tier string `json:"tier"`
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

func warnSearchSessionsSuppressedByFailures(warnWriter io.Writer) {
	if warnWriter == nil {
		return
	}
	_, _ = fmt.Fprint(warnWriter, Localize(
		"traceary: matching sessions were suppressed because --failures cannot be applied to session refinements. Run the same search without --failures to see them.\n",
		"traceary: セッション refinement には --failures を適用できないため、一致したセッションを表示していません。--failures を外して同じ検索を実行すると確認できます。\n",
	))
}

// searchSessionNotices collects the stderr advisories a session-tier lookup can
// raise. They answer different questions — "sessions matched but --kind hid
// them", "sessions matched but --failures-only hid them" — so none may
// swallow another, and each is a no-op when its own condition does not hold.
type searchSessionNotices struct {
	kindSuppressed     bool
	failuresSuppressed bool
}

func (n searchSessionNotices) write(warnWriter io.Writer) {
	if n.kindSuppressed {
		warnSearchSessionsSuppressedByKind(warnWriter)
	}
	if n.failuresSuppressed {
		warnSearchSessionsSuppressedByFailures(warnWriter)
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
	eventTier apptypes.SearchHitTier,
) error {
	if asJSON {
		return writeSearchJSON(output, events, sessions, jsonFieldsExplicit, textOpts.fields, extrasFor, eventTier)
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
	eventTier apptypes.SearchHitTier,
) error {
	eventPayload := make([]any, 0, len(events))
	if jsonFieldsExplicit {
		for _, event := range events {
			extras := compactRowExtras{}
			if extrasFor != nil {
				extras = extrasFor(event)
			}
			eventPayload = append(eventPayload, stampSearchEventTier(newEventFieldsOutput(event, fields, extras), eventTier))
		}
	} else {
		for _, event := range events {
			eventPayload = append(eventPayload, stampSearchEventTier(newEventOutput(event), eventTier))
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
	eventTier apptypes.SearchHitTier,
) error {
	eventPayload := make([]any, 0, len(metadata))
	for _, event := range metadata {
		eventPayload = append(eventPayload, stampSearchEventTier(newEventMetadataFieldsOutput(event, fields), eventTier))
	}
	return writeSearchJSONEnvelope(output, eventPayload, sessions)
}

func writeSearchEventHitsMetadataJSON(
	output io.Writer,
	hits []apptypes.SearchEventHit,
	sessions []apptypes.SearchSessionHit,
	fields []readFieldID,
) error {
	eventPayload := make([]any, 0, len(hits))
	for _, hit := range hits {
		payload := newEventFieldsOutput(hit.Event(), fields, compactRowExtras{})
		eventPayload = append(eventPayload, stampSearchEventTier(payload, hit.Tier()))
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
		Tier:       string(hit.Tier()),
	}
}

func stampSearchEventTier(payload any, tier apptypes.SearchHitTier) any {
	if tier == "" {
		return payload
	}
	switch typed := payload.(type) {
	case event:
		return searchEventJSONOutput{event: typed, Tier: string(tier)}
	case map[string]any:
		typed["tier"] = string(tier)
		return typed
	default:
		return payload
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
