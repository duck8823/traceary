package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	apptypes "github.com/duck8823/traceary/application/types"
	"github.com/duck8823/traceary/domain/model"
	"github.com/duck8823/traceary/domain/types"
)

func (c *RootCLI) inspectCommandAuditReliability(ctx context.Context, strict bool) doctorCheck {
	const checkName = "audit-reliability"
	if c.event == nil {
		return doctorCheck{
			Name:    checkName,
			Status:  doctorStatusSkip,
			Message: localizef("event usecase is not configured", "event usecase が設定されていません"),
		}
	}
	events, err := c.event.List(ctx, apptypes.NewEventListCriteriaBuilder(commandAuditReliabilityScanLimit).
		Kind(types.EventKindCommandExecuted).
		Build())
	if err != nil {
		return doctorCheck{
			Name:    checkName,
			Status:  doctorStatusFail,
			Message: localizef("failed to list recent command audits: %v", "recent command audit の取得に失敗しました: %v", err),
		}
	}
	details := make([]apptypes.EventDetails, 0, len(events))
	for _, event := range events {
		detail, err := c.event.Show(ctx, event.EventID())
		if err != nil {
			return doctorCheck{
				Name:    checkName,
				Status:  doctorStatusFail,
				Message: localizef("failed to inspect command audit %s: %v", "command audit %s の検査に失敗しました: %v", event.EventID(), err),
			}
		}
		details = append(details, detail)
	}
	return commandAuditReliabilityCheckFromFindings(commandAuditReliabilityFindingsFromDetails(ctx, details, strict), strict)
}

func commandAuditReliabilityCheckFromFindings(findings commandAuditReliabilityFindings, strict bool) doctorCheck {
	const checkName = "audit-reliability"
	duplicateRecordCount := 0
	for _, group := range findings.DuplicateGroups {
		duplicateRecordCount += group.Count
	}
	if len(findings.DuplicateGroups) == 0 && len(findings.WorkspaceDriftSamples) == 0 {
		return doctorCheck{
			Name:   checkName,
			Status: doctorStatusPass,
			Message: localizef(
				"scanned %d recent command audit(s); no duplicate groups or workspace-drift candidates found",
				"%d 件の recent command audit を検査しました。duplicate group / workspace drift candidate はありません",
				findings.ScannedAuditCount,
			),
		}
	}

	hint := Localize(
		"likely hook duplicates (identity-matching audits within "+commandAuditDuplicateProximityWindow.String()+"); intentional re-runs minutes apart are excluded. Re-run with --strict to surface every exact duplicate group, then inspect with `traceary show <event_id>`",
		"hook 由来とみられる duplicate（"+commandAuditDuplicateProximityWindow.String()+" 以内の identity 一致 audit）です。数分離れた意図的な re-run は除外されます。完全一致する duplicate group をすべて見るには --strict を付け、`traceary show <event_id>` で確認してください",
	)
	if strict {
		hint = Localize(
			"--strict: every exact duplicate group is reported regardless of time gap, so intentional re-runs appear too; inspect the sampled event IDs with `traceary show <event_id>` before drawing process conclusions",
			"--strict: 時間差に関係なく完全一致する duplicate group をすべて報告します（意図的な re-run も含みます）。process の結論を出す前に sample event ID を `traceary show <event_id>` で確認してください",
		)
	}

	return doctorCheck{
		Name:   checkName,
		Status: doctorStatusWarn,
		Hint:   hint,
		Message: localizef(
			"scanned %d recent command audit(s); duplicate_groups=%d duplicate_records=%d workspace_drift_candidates=%d samples: duplicates=[%s] drift=[%s]",
			"%d 件の recent command audit を検査しました。duplicate_groups=%d duplicate_records=%d workspace_drift_candidates=%d samples: duplicates=[%s] drift=[%s]",
			findings.ScannedAuditCount,
			len(findings.DuplicateGroups),
			duplicateRecordCount,
			len(findings.WorkspaceDriftSamples),
			formatCommandAuditDuplicateSamples(findings.DuplicateGroups),
			formatCommandAuditWorkspaceDriftSamples(findings.WorkspaceDriftSamples),
		),
	}
}

func commandAuditReliabilityFindingsFromDetails(ctx context.Context, details []apptypes.EventDetails, strict bool) commandAuditReliabilityFindings {
	findings := commandAuditReliabilityFindings{}
	groups := map[commandAuditDuplicateGroupKey][]commandAuditDuplicateRecord{}
	for _, detail := range details {
		event := detail.Event()
		audit, ok := detail.CommandAudit().Value()
		if event == nil || !ok || audit == nil {
			continue
		}
		findings.ScannedAuditCount++
		key := newCommandAuditDuplicateGroupKey(event, audit)
		groups[key] = append(groups[key], commandAuditDuplicateRecord{
			eventID:   event.EventID().String(),
			createdAt: event.CreatedAt(),
		})

		if drift, ok := commandAuditWorkspaceDriftFromDetail(ctx, event, audit); ok {
			findings.WorkspaceDriftSamples = append(findings.WorkspaceDriftSamples, drift)
		}
	}
	for _, records := range groups {
		findings.DuplicateGroups = append(findings.DuplicateGroups, commandAuditDuplicateGroupsFromRecords(records, strict)...)
	}
	sort.Slice(findings.DuplicateGroups, func(i, j int) bool {
		return findings.DuplicateGroups[i].EventIDs[0] < findings.DuplicateGroups[j].EventIDs[0]
	})
	sort.Slice(findings.WorkspaceDriftSamples, func(i, j int) bool {
		return findings.WorkspaceDriftSamples[i].EventID < findings.WorkspaceDriftSamples[j].EventID
	})
	return findings
}

// commandAuditDuplicateRecord is one identity-matching audit considered for
// duplicate grouping, carrying the timestamp used for time-proximity clustering.
type commandAuditDuplicateRecord struct {
	eventID   string
	createdAt time.Time
}

// commandAuditDuplicateGroupsFromRecords turns the identity-matching records of
// a single group key into reportable duplicate groups. In strict mode any group
// of 2+ exact matches is reported regardless of time. By default the records are
// clustered by time proximity (consecutive records within
// commandAuditDuplicateProximityWindow) so that only near-simultaneous writes —
// the likely hook duplicates — are reported, and intentional re-runs minutes
// apart are excluded.
func commandAuditDuplicateGroupsFromRecords(records []commandAuditDuplicateRecord, strict bool) []commandAuditDuplicateGroup {
	if len(records) <= 1 {
		return nil
	}
	sort.Slice(records, func(i, j int) bool {
		if !records[i].createdAt.Equal(records[j].createdAt) {
			return records[i].createdAt.Before(records[j].createdAt)
		}
		return records[i].eventID < records[j].eventID
	})

	groupFromRun := func(run []commandAuditDuplicateRecord) (commandAuditDuplicateGroup, bool) {
		if len(run) < 2 {
			return commandAuditDuplicateGroup{}, false
		}
		ids := make([]string, len(run))
		for i, record := range run {
			ids[i] = record.eventID
		}
		sort.Strings(ids)
		return commandAuditDuplicateGroup{EventIDs: ids, Count: len(ids)}, true
	}

	if strict {
		if group, ok := groupFromRun(records); ok {
			return []commandAuditDuplicateGroup{group}
		}
		return nil
	}

	var groups []commandAuditDuplicateGroup
	run := []commandAuditDuplicateRecord{records[0]}
	for _, record := range records[1:] {
		if record.createdAt.Sub(run[len(run)-1].createdAt) <= commandAuditDuplicateProximityWindow {
			run = append(run, record)
			continue
		}
		if group, ok := groupFromRun(run); ok {
			groups = append(groups, group)
		}
		run = []commandAuditDuplicateRecord{record}
	}
	if group, ok := groupFromRun(run); ok {
		groups = append(groups, group)
	}
	return groups
}

func newCommandAuditDuplicateGroupKey(event *model.Event, audit *model.CommandAudit) commandAuditDuplicateGroupKey {
	exitCode := "-"
	if value, ok := audit.ExitCode().Value(); ok {
		exitCode = strconv.Itoa(value)
	}
	return commandAuditDuplicateGroupKey{
		Client:          event.Client().String(),
		Agent:           event.Agent().String(),
		SessionID:       event.SessionID().String(),
		Workspace:       event.Workspace().String(),
		Command:         audit.Command(),
		Input:           audit.Input(),
		Output:          audit.Output(),
		InputTruncated:  audit.InputTruncated(),
		OutputTruncated: audit.OutputTruncated(),
		ExitCode:        exitCode,
		Failed:          audit.Failed(),
	}
}

func commandAuditWorkspaceDriftFromDetail(ctx context.Context, event *model.Event, audit *model.CommandAudit) (commandAuditWorkspaceDriftSample, bool) {
	cwd, ok := commandAuditInputCWD(audit.Input())
	if !ok {
		return commandAuditWorkspaceDriftSample{}, false
	}
	evidenceWorkspace := commandAuditWorkspaceEvidenceFromCWD(ctx, cwd)
	storedWorkspace := event.Workspace().String()
	if storedWorkspace == "" || evidenceWorkspace == "" || storedWorkspace == evidenceWorkspace {
		return commandAuditWorkspaceDriftSample{}, false
	}
	return commandAuditWorkspaceDriftSample{
		EventID:           event.EventID().String(),
		StoredWorkspace:   storedWorkspace,
		EvidenceWorkspace: evidenceWorkspace,
		CWD:               cwd,
	}, true
}

func commandAuditInputCWD(input string) (string, bool) {
	var value any
	if err := json.Unmarshal([]byte(input), &value); err != nil {
		return "", false
	}
	return findCWDInJSONValue(value)
}

func findCWDInJSONValue(value any) (string, bool) {
	object, ok := value.(map[string]any)
	if !ok {
		switch typed := value.(type) {
		case []any:
			for _, item := range typed {
				if cwd, ok := findCWDInJSONValue(item); ok {
					return cwd, true
				}
			}
		}
		return "", false
	}
	for _, key := range []string{"cwd", "workdir", "working_directory"} {
		if raw, ok := object[key]; ok {
			if cwd, ok := raw.(string); ok && strings.TrimSpace(cwd) != "" {
				return strings.TrimSpace(cwd), true
			}
		}
	}
	keys := make([]string, 0, len(object))
	for key := range object {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		if cwd, ok := findCWDInJSONValue(object[key]); ok {
			return cwd, true
		}
	}
	return "", false
}

func commandAuditWorkspaceEvidenceFromCWD(ctx context.Context, cwd string) string {
	trimmed := strings.TrimSpace(cwd)
	if trimmed == "" {
		return ""
	}
	if workspace, err := detectRepoContextFromDir(ctx, trimmed); err == nil && strings.TrimSpace(workspace) != "" {
		return workspace
	}
	return normalizeLocalWorkContextPath(trimmed)
}

func formatCommandAuditDuplicateSamples(groups []commandAuditDuplicateGroup) string {
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
		parts = append(parts, fmt.Sprintf("count=%d event_ids=%s", group.Count, strings.Join(eventIDs, ",")))
	}
	return strings.Join(parts, "; ")
}

func formatCommandAuditWorkspaceDriftSamples(samples []commandAuditWorkspaceDriftSample) string {
	if len(samples) == 0 {
		return "-"
	}
	limit := len(samples)
	if limit > 3 {
		limit = 3
	}
	parts := make([]string, 0, limit)
	for _, sample := range samples[:limit] {
		parts = append(parts, fmt.Sprintf(
			"event_id=%s stored=%q cwd_workspace=%q",
			sample.EventID,
			sample.StoredWorkspace,
			sample.EvidenceWorkspace,
		))
	}
	return strings.Join(parts, "; ")
}
