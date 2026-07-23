package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	apptypes "github.com/duck8823/traceary/application/types"
	"github.com/duck8823/traceary/domain/types"
)

const (
	codexCaptureCheckName          = "codex-capture"
	codexCaptureWindow             = 7 * 24 * time.Hour
	codexCaptureReasonOK           = "capture_observed"
	codexCaptureReasonEmpty        = "no_recent_capture_evidence"
	codexCaptureReasonSpool        = "hook_spool_backlog"
	codexCaptureReasonSpoolPartial = "spool_projection_partial"
	codexCaptureReasonUsage        = "usage_missing_after_stop"
	codexCaptureCWDLimit           = 64

	codexBoundarySessionStart = "session_start"
	codexBoundaryPrompt       = "prompt"
	codexBoundaryTool         = "tool"
	codexBoundaryCompact      = "compact"
	codexBoundaryStop         = "stop"
	codexBoundaryUsage        = "usage"

	codexBoundaryStored        = "stored"
	codexBoundaryPending       = "delivery_pending"
	codexBoundaryStoredPending = "stored_and_delivery_pending"
	codexBoundaryNotObserved   = "not_observed"
)

var codexCaptureBoundaryOrder = []string{
	codexBoundarySessionStart,
	codexBoundaryPrompt,
	codexBoundaryTool,
	codexBoundaryCompact,
	codexBoundaryStop,
	codexBoundaryUsage,
}

type codexCaptureEvidence struct {
	StoredEvents       int
	UsageObservations  int
	UsageKnown         int
	UsageUnavailable   int
	StopSessions       int
	StopsWithUsage     int
	PendingDeliveries  int
	UnscopedDeliveries int
	SpoolPartial       bool
	StoredBoundaries   map[string]bool
	PendingBoundaries  map[string]bool
}

type codexCaptureClassification struct {
	Status     string
	Reason     string
	Boundaries map[string]string
	UsageState string
}

type codexSpoolMetadata struct {
	Command   string
	Action    string
	SessionID string
	CWD       string
	CreatedAt time.Time
}

func (c *RootCLI) inspectCodexCapture(
	ctx context.Context,
	projectDir string,
	currentVersion string,
	pluginState codexPluginHookFallbackState,
	trust codexPluginHookTrustResult,
	spoolRecords []hookSpoolRecord,
	spoolErr error,
) doctorCheck {
	workspace := resolveDoctorEventCoverageWorkspace(ctx, projectDir)
	surface := "manual_hooks"
	if pluginState.PluginEnabled {
		surface = "plugin_managed_hooks"
	}
	identity := codexCaptureIdentity{
		Surface:         surface,
		PluginKey:       pluginState.PluginKey,
		HookTrust:       string(trust.Status),
		TracearyVersion: currentVersion,
		Workspace:       workspace.String(),
	}
	if spoolErr != nil {
		return codexCaptureFailure(identity, fmt.Sprintf("failed to inspect hook spool metadata: %v", spoolErr))
	}
	if c.codexCaptureDiagnostic == nil {
		return doctorCheck{
			Name:   codexCaptureCheckName,
			Status: doctorStatusWarn,
			Message: formatCodexCaptureIdentity(identity) +
				" reason=diagnostic_dependencies_unavailable",
		}
	}

	now := time.Now().UTC()
	criteria, err := apptypes.CodexCaptureDiagnosticCriteriaOf(
		workspace,
		now.Add(-codexCaptureWindow),
		now,
	)
	if err != nil {
		return codexCaptureFailure(identity, fmt.Sprintf("failed to build Codex capture diagnostic criteria: %v", err))
	}
	stored, err := c.codexCaptureDiagnostic.Load(ctx, criteria)
	if err != nil {
		return codexCaptureFailure(identity, fmt.Sprintf("failed to load body-free Codex capture evidence: %v", err))
	}

	evidence := codexCaptureEvidence{
		StoredEvents:      stored.StoredEvents,
		UsageObservations: stored.UsageObservations,
		UsageKnown:        stored.UsageKnown,
		UsageUnavailable:  stored.UsageUnavailable,
		StopSessions:      stored.StopSessions,
		StopsWithUsage:    stored.StopSessionsWithUsage,
		StoredBoundaries:  make(map[string]bool),
		PendingBoundaries: make(map[string]bool),
	}
	evidence.StoredBoundaries[codexBoundarySessionStart] = stored.SessionStartObserved
	evidence.StoredBoundaries[codexBoundaryPrompt] = stored.PromptObserved
	evidence.StoredBoundaries[codexBoundaryTool] = stored.ToolObserved
	evidence.StoredBoundaries[codexBoundaryCompact] = stored.CompactObserved
	evidence.StoredBoundaries[codexBoundaryStop] = stored.StopSessions > 0
	if evidence.UsageObservations > 0 {
		evidence.StoredBoundaries[codexBoundaryUsage] = true
	}

	projected, unscoped, projectionPartial := projectCodexSpoolMetadata(ctx, spoolRecords, workspace)
	evidence.UnscopedDeliveries = unscoped
	evidence.SpoolPartial = projectionPartial
	for _, pending := range projected {
		evidence.PendingDeliveries++
		for _, boundary := range codexSpoolBoundaries(pending.Command, pending.Action) {
			evidence.PendingBoundaries[boundary] = true
		}
	}

	classification := classifyCodexCapture(evidence)
	check := doctorCheck{
		Name:   codexCaptureCheckName,
		Status: classification.Status,
		Message: fmt.Sprintf(
			"%s reason=%s boundaries=%s usage=%s stored_events=%d usage_observations=%d pending_deliveries=%d unscoped_pending=%d",
			formatCodexCaptureIdentity(identity),
			classification.Reason,
			formatCodexBoundaryStates(classification.Boundaries),
			classification.UsageState,
			evidence.StoredEvents,
			evidence.UsageObservations,
			evidence.PendingDeliveries,
			evidence.UnscopedDeliveries,
		),
	}
	switch classification.Reason {
	case codexCaptureReasonSpool:
		check.Hint = Localize(
			"Codex reached Traceary, but one or more workspace deliveries remain durable and uncommitted. Resolve the database/open latency or write failure, then run `traceary doctor --client codex --fix` to drain a bounded batch.",
			"Codex から Traceary には到達していますが、この workspace の delivery が durable spool に残り未commitです。DB open latency または write failure を解消し、`traceary doctor --client codex --fix` で bounded batch を drain してください。",
		)
		check.FixCommand = "traceary doctor --client codex --fix"
	case codexCaptureReasonSpoolPartial:
		check.Hint = Localize(
			"the Codex spool contains more distinct working directories than the bounded diagnostic resolves; inspect the global hook-spool check and drain the backlog before retrying",
			"Codex spool に bounded diagnostic の解決上限を超える working directory があります。global hook-spool check を確認し、backlog を drain してから再実行してください",
		)
	case codexCaptureReasonUsage:
		check.Hint = Localize(
			"a Codex Stop was committed without a finalized usage observation or pending usage delivery; inspect the installed plugin version and local rollout availability",
			"Codex Stop は commit 済みですが finalized usage observation も pending usage delivery もありません。installed plugin version と local rollout availability を確認してください",
		)
	case codexCaptureReasonEmpty:
		check.Hint = Localize(
			"start Codex in this canonical workspace, submit one prompt, run one tool, and complete one response, then rerun doctor; no recent evidence is not treated as successful capture",
			"この canonical workspace で Codex を起動し、prompt 送信・tool 実行・response 完了後に doctor を再実行してください。recent evidence が無い状態は capture 成功として扱いません",
		)
	}
	return check
}

type codexCaptureIdentity struct {
	Surface         string
	PluginKey       string
	HookTrust       string
	TracearyVersion string
	Workspace       string
}

func formatCodexCaptureIdentity(identity codexCaptureIdentity) string {
	return fmt.Sprintf(
		"surface=%s client=codex traceary_version=%s plugin=%s hook_trust=%s workspace=%s",
		emptyAsDash(identity.Surface),
		emptyAsDash(identity.TracearyVersion),
		emptyAsDash(identity.PluginKey),
		emptyAsDash(identity.HookTrust),
		emptyAsDash(identity.Workspace),
	)
}

func codexCaptureFailure(identity codexCaptureIdentity, message string) doctorCheck {
	return doctorCheck{
		Name:    codexCaptureCheckName,
		Status:  doctorStatusFail,
		Message: formatCodexCaptureIdentity(identity) + " reason=diagnostic_read_failed error=" + message,
	}
}

func classifyCodexCapture(evidence codexCaptureEvidence) codexCaptureClassification {
	boundaries := make(map[string]string, len(codexCaptureBoundaryOrder))
	for _, boundary := range codexCaptureBoundaryOrder {
		switch {
		case evidence.StoredBoundaries[boundary] && evidence.PendingBoundaries[boundary]:
			boundaries[boundary] = codexBoundaryStoredPending
		case evidence.StoredBoundaries[boundary]:
			boundaries[boundary] = codexBoundaryStored
		case evidence.PendingBoundaries[boundary]:
			boundaries[boundary] = codexBoundaryPending
		default:
			boundaries[boundary] = codexBoundaryNotObserved
		}
	}
	usageState := codexBoundaryNotObserved
	switch {
	case evidence.UsageKnown > 0 && evidence.UsageUnavailable > 0:
		usageState = "mixed_known_and_unavailable"
	case evidence.UsageKnown > 0:
		usageState = "known"
	case evidence.UsageUnavailable > 0:
		usageState = "unavailable_recorded"
	case evidence.PendingBoundaries[codexBoundaryUsage]:
		usageState = codexBoundaryPending
	}

	classification := codexCaptureClassification{
		Status: doctorStatusPass, Reason: codexCaptureReasonOK,
		Boundaries: boundaries, UsageState: usageState,
	}
	if evidence.PendingDeliveries > 0 {
		classification.Status = doctorStatusWarn
		classification.Reason = codexCaptureReasonSpool
		return classification
	}
	if evidence.SpoolPartial {
		classification.Status = doctorStatusWarn
		classification.Reason = codexCaptureReasonSpoolPartial
		return classification
	}
	if evidence.StopSessions > evidence.StopsWithUsage {
		classification.Status = doctorStatusWarn
		classification.Reason = codexCaptureReasonUsage
		return classification
	}
	if evidence.StoredEvents == 0 && evidence.UsageObservations == 0 {
		classification.Status = doctorStatusWarn
		classification.Reason = codexCaptureReasonEmpty
	}
	return classification
}

func projectCodexSpoolMetadata(
	ctx context.Context,
	records []hookSpoolRecord,
	target types.Workspace,
) ([]codexSpoolMetadata, int, bool) {
	metadata := make([]codexSpoolMetadata, 0, len(records))
	for _, record := range records {
		if strings.TrimSpace(record.Client) != "codex" {
			continue
		}
		var envelope struct {
			SessionID string `json:"session_id"`
			CWD       string `json:"cwd"`
		}
		_ = json.Unmarshal([]byte(record.Payload), &envelope)
		metadata = append(metadata, codexSpoolMetadata{
			Command: strings.TrimSpace(record.Command), Action: strings.TrimSpace(record.Action),
			SessionID: strings.TrimSpace(envelope.SessionID), CWD: strings.TrimSpace(envelope.CWD),
			CreatedAt: record.CreatedAt,
		})
	}

	canonicalByCWD := map[string]types.Workspace{}
	workspaceBySession := map[string]types.Workspace{}
	projectionPartial := false
	for _, record := range metadata {
		if record.CWD == "" {
			continue
		}
		workspace, ok := canonicalByCWD[record.CWD]
		if !ok {
			if len(canonicalByCWD) >= codexCaptureCWDLimit {
				projectionPartial = true
				continue
			}
			workspace = canonicalWorkspaceForCodexSpool(ctx, record.CWD)
			canonicalByCWD[record.CWD] = workspace
		}
		if record.SessionID != "" && workspace != "" {
			workspaceBySession[record.SessionID] = workspace
		}
	}

	scoped := make([]codexSpoolMetadata, 0, len(metadata))
	unscoped := 0
	for _, record := range metadata {
		workspace := canonicalByCWD[record.CWD]
		if workspace == "" && record.SessionID != "" {
			workspace = workspaceBySession[record.SessionID]
		}
		if workspace == "" {
			unscoped++
			continue
		}
		if workspace == target {
			scoped = append(scoped, record)
		}
	}
	sort.Slice(scoped, func(i, j int) bool { return scoped[i].CreatedAt.Before(scoped[j].CreatedAt) })
	return scoped, unscoped, projectionPartial
}

func canonicalWorkspaceForCodexSpool(ctx context.Context, cwd string) types.Workspace {
	if detected, err := detectRepoContextFromDir(ctx, cwd); err == nil {
		return types.Workspace(detected)
	}
	return types.Workspace(normalizeLocalWorkContextPath(cwd))
}

func codexSpoolBoundaries(command, action string) []string {
	switch strings.TrimSpace(command) {
	case "session":
		if strings.TrimSpace(action) == "start" {
			return []string{codexBoundarySessionStart}
		}
		if strings.TrimSpace(action) == "stop" {
			return []string{codexBoundaryStop}
		}
	case "prompt":
		return []string{codexBoundaryPrompt}
	case "audit":
		return []string{codexBoundaryTool}
	case "compact":
		return []string{codexBoundaryCompact}
	case "transcript":
		return []string{codexBoundaryStop}
	case "usage":
		return []string{codexBoundaryStop, codexBoundaryUsage}
	}
	return nil
}

func formatCodexBoundaryStates(states map[string]string) string {
	values := make([]string, 0, len(codexCaptureBoundaryOrder))
	for _, boundary := range codexCaptureBoundaryOrder {
		values = append(values, boundary+":"+states[boundary])
	}
	return strings.Join(values, ",")
}
