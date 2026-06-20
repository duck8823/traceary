package cli

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	apptypes "github.com/duck8823/traceary/application/types"
	"github.com/duck8823/traceary/domain/model"
	"github.com/duck8823/traceary/domain/types"
)

const contentEventReliabilityScanLimit = 200

// contentEventDuplicateProximityWindow bounds how close in time two
// identity-matching prompt/transcript events must be to count as a likely hook
// double-write rather than a genuine repeat. Real hook duplicates land
// near-simultaneously (the write-side guard, duplicateHookContentEventWindow,
// suppresses exact repeats within 2s); a user legitimately re-sending the same
// prompt does so seconds-to-minutes apart. This window sits an order of
// magnitude above the 2s write guard (to absorb clock skew and slow writes) yet
// far below the spacing of deliberate repeats, so the default diagnostic stays
// actionable. `traceary doctor --strict` ignores this window and reports every
// exact duplicate group for forensic analysis. It mirrors
// commandAuditDuplicateProximityWindow but is a separate constant: command
// audits and content events keep independent dedup semantics.
const contentEventDuplicateProximityWindow = 10 * time.Second

type contentEventReliabilityFindings struct {
	ScannedContentCount int
	DuplicateGroups     []contentEventDuplicateGroup
}

type contentEventDuplicateGroup struct {
	EventIDs   []string
	Count      int
	Kind       string
	SourceHook string
}

type contentEventDuplicateGroupKey struct {
	Kind           string
	Client         string
	Agent          string
	SessionID      string
	Workspace      string
	SourceHook     string
	NormalizedBody string
}

// contentEventDuplicateRecord is one identity-matching content event considered
// for duplicate grouping, carrying the timestamp used for time clustering.
type contentEventDuplicateRecord struct {
	eventID   string
	createdAt time.Time
}

// inspectContentEventReliability scans recent hook-originated prompt/transcript
// events and reports duplicate groups. command_executed is intentionally out of
// scope (it is covered by inspectCommandAuditReliability, whose re-run semantics
// differ): this diagnostic lists only the prompt and transcript kinds filtered
// to client="hook", matching the write-side dedup eligibility in
// isDedupEligibleHookContentEvent.
func (c *RootCLI) inspectContentEventReliability(ctx context.Context, strict bool) doctorCheck {
	const checkName = "content-event-reliability"
	if c.event == nil {
		return doctorCheck{
			Name:    checkName,
			Status:  doctorStatusSkip,
			Message: localizef("event usecase is not configured", "event usecase が設定されていません"),
		}
	}

	events := make([]*model.Event, 0, contentEventReliabilityScanLimit*2)
	for _, kind := range []types.EventKind{types.EventKindPrompt, types.EventKindTranscript} {
		listed, err := c.event.List(ctx, apptypes.NewEventListCriteriaBuilder(contentEventReliabilityScanLimit).
			Kind(kind).
			Client(types.Client("hook")).
			Build())
		if err != nil {
			return doctorCheck{
				Name:    checkName,
				Status:  doctorStatusFail,
				Message: localizef("failed to list recent %s events: %v", "recent %s event の取得に失敗しました: %v", kind.String(), err),
			}
		}
		for _, event := range listed {
			if event == nil {
				continue
			}
			events = append(events, event)
		}
	}

	return contentEventReliabilityCheckFromFindings(contentEventReliabilityFindingsFromEvents(events, strict), strict)
}

func contentEventReliabilityFindingsFromEvents(events []*model.Event, strict bool) contentEventReliabilityFindings {
	findings := contentEventReliabilityFindings{}
	groups := map[contentEventDuplicateGroupKey][]contentEventDuplicateRecord{}
	for _, event := range events {
		if event == nil {
			continue
		}
		// Defensive: only hook-originated prompt/transcript content participates,
		// mirroring isDedupEligibleHookContentEvent. command_executed never reaches
		// here because the caller lists only the prompt/transcript kinds.
		if event.Client().String() != "hook" {
			continue
		}
		if event.Kind() != types.EventKindPrompt && event.Kind() != types.EventKindTranscript {
			continue
		}
		findings.ScannedContentCount++
		key := newContentEventDuplicateGroupKey(event)
		groups[key] = append(groups[key], contentEventDuplicateRecord{
			eventID:   event.EventID().String(),
			createdAt: event.CreatedAt(),
		})
	}
	for key, records := range groups {
		findings.DuplicateGroups = append(findings.DuplicateGroups, contentEventDuplicateGroupsFromRecords(key, records, strict)...)
	}
	sort.Slice(findings.DuplicateGroups, func(i, j int) bool {
		return findings.DuplicateGroups[i].EventIDs[0] < findings.DuplicateGroups[j].EventIDs[0]
	})
	return findings
}

func newContentEventDuplicateGroupKey(event *model.Event) contentEventDuplicateGroupKey {
	return contentEventDuplicateGroupKey{
		Kind:           event.Kind().String(),
		Client:         event.Client().String(),
		Agent:          event.Agent().String(),
		SessionID:      event.SessionID().String(),
		Workspace:      event.Workspace().String(),
		SourceHook:     event.SourceHook(),
		NormalizedBody: normalizeContentEventBody(event.Body()),
	}
}

// normalizeContentEventBody trims surrounding whitespace so trivially different
// trailing newlines do not split an otherwise identical pair. It intentionally
// does not collapse interior whitespace: genuinely different prompts must remain
// distinct.
func normalizeContentEventBody(body string) string {
	return strings.TrimSpace(body)
}

// contentEventDuplicateGroupsFromRecords turns the identity-matching records of
// a single group key into reportable duplicate groups. In strict mode any group
// of 2+ exact matches is reported regardless of time. By default the records are
// clustered by time proximity (consecutive records within
// contentEventDuplicateProximityWindow) so that only near-simultaneous writes —
// the likely hook duplicates — are reported. This mirrors
// commandAuditDuplicateGroupsFromRecords.
func contentEventDuplicateGroupsFromRecords(key contentEventDuplicateGroupKey, records []contentEventDuplicateRecord, strict bool) []contentEventDuplicateGroup {
	if len(records) <= 1 {
		return nil
	}
	sort.Slice(records, func(i, j int) bool {
		if !records[i].createdAt.Equal(records[j].createdAt) {
			return records[i].createdAt.Before(records[j].createdAt)
		}
		return records[i].eventID < records[j].eventID
	})

	groupFromRun := func(run []contentEventDuplicateRecord) (contentEventDuplicateGroup, bool) {
		if len(run) < 2 {
			return contentEventDuplicateGroup{}, false
		}
		ids := make([]string, len(run))
		for i, record := range run {
			ids[i] = record.eventID
		}
		sort.Strings(ids)
		return contentEventDuplicateGroup{
			EventIDs:   ids,
			Count:      len(ids),
			Kind:       key.Kind,
			SourceHook: key.SourceHook,
		}, true
	}

	if strict {
		if group, ok := groupFromRun(records); ok {
			return []contentEventDuplicateGroup{group}
		}
		return nil
	}

	var groups []contentEventDuplicateGroup
	run := []contentEventDuplicateRecord{records[0]}
	for _, record := range records[1:] {
		if record.createdAt.Sub(run[len(run)-1].createdAt) <= contentEventDuplicateProximityWindow {
			run = append(run, record)
			continue
		}
		if group, ok := groupFromRun(run); ok {
			groups = append(groups, group)
		}
		run = []contentEventDuplicateRecord{record}
	}
	if group, ok := groupFromRun(run); ok {
		groups = append(groups, group)
	}
	return groups
}

func contentEventReliabilityCheckFromFindings(findings contentEventReliabilityFindings, strict bool) doctorCheck {
	const checkName = "content-event-reliability"
	duplicateRecordCount := 0
	for _, group := range findings.DuplicateGroups {
		duplicateRecordCount += group.Count
	}
	if len(findings.DuplicateGroups) == 0 {
		return doctorCheck{
			Name:   checkName,
			Status: doctorStatusPass,
			Message: localizef(
				"scanned %d recent prompt/transcript hook event(s); no duplicate content groups found",
				"%d 件の recent prompt/transcript hook event を検査しました。duplicate content group はありません",
				findings.ScannedContentCount,
			),
		}
	}

	hint := Localize(
		"likely hook duplicates (identity-matching prompt/transcript content within "+contentEventDuplicateProximityWindow.String()+"); deliberate repeats farther apart are excluded. Re-run with --strict to surface every exact duplicate group, then inspect with `traceary show <event_id>`. No automatic cleanup is performed",
		"hook 由来とみられる duplicate（"+contentEventDuplicateProximityWindow.String()+" 以内の identity 一致 prompt/transcript content）です。離れた意図的な再送は除外されます。完全一致する duplicate group をすべて見るには --strict を付け、`traceary show <event_id>` で確認してください。自動的な削除は行いません",
	)
	if strict {
		hint = Localize(
			"--strict: every exact duplicate content group is reported regardless of time gap, so deliberate repeats appear too; inspect the sampled event IDs with `traceary show <event_id>` before drawing conclusions. No automatic cleanup is performed",
			"--strict: 時間差に関係なく完全一致する duplicate content group をすべて報告します（意図的な再送も含みます）。結論を出す前に sample event ID を `traceary show <event_id>` で確認してください。自動的な削除は行いません",
		)
	}

	return doctorCheck{
		Name:   checkName,
		Status: doctorStatusWarn,
		Hint:   hint,
		Message: localizef(
			"scanned %d recent prompt/transcript hook event(s); duplicate_groups=%d duplicate_records=%d samples: %s",
			"%d 件の recent prompt/transcript hook event を検査しました。duplicate_groups=%d duplicate_records=%d samples: %s",
			findings.ScannedContentCount,
			len(findings.DuplicateGroups),
			duplicateRecordCount,
			formatContentEventDuplicateSamples(findings.DuplicateGroups),
		),
	}
}

func formatContentEventDuplicateSamples(groups []contentEventDuplicateGroup) string {
	if len(groups) == 0 {
		return "-"
	}
	limit := len(groups)
	if limit > 3 {
		limit = 3
	}
	parts := make([]string, 0, limit)
	for _, group := range groups[:limit] {
		eventIDs := group.EventIDs
		if len(eventIDs) > 4 {
			eventIDs = eventIDs[:4]
		}
		sourceHook := group.SourceHook
		if sourceHook == "" {
			sourceHook = "-"
		}
		parts = append(parts, fmt.Sprintf(
			"kind=%s source_hook=%s count=%d event_ids=%s",
			group.Kind,
			sourceHook,
			group.Count,
			strings.Join(eventIDs, ","),
		))
	}
	return strings.Join(parts, "; ")
}
