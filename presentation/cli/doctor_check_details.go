package cli

import (
	"github.com/duck8823/traceary/application/hostcoverage"
)

// doctorCheckDetails carries optional machine-readable diagnostics for a
// doctor check. It is additive to name/status/severity/message/hint: consumers
// must treat an absent block as "not available" and fall back to status.
// Field values are language-independent; localized prose stays in message/hint.
type doctorCheckDetails struct {
	HostCapabilities *doctorHostCapabilitiesDetails `json:"host_capabilities,omitempty"`
	CodexCapture     *doctorCodexCaptureDetails     `json:"codex_capture,omitempty"`
}

// doctorHostCapabilitiesDetails projects the application/hostcoverage matrix
// row for one host. It describes Traceary's hook wiring, not observed capture.
type doctorHostCapabilitiesDetails struct {
	SchemaVersion int                         `json:"schema_version"`
	Source        string                      `json:"source"`
	LastVerified  string                      `json:"last_verified"`
	Host          string                      `json:"host"`
	Package       string                      `json:"package"`
	Events        []doctorHostCapabilityEvent `json:"events"`
}

type doctorHostCapabilityEvent struct {
	ID     string `json:"id"`
	Status string `json:"status"`
}

const doctorHostCapabilitiesSource = "application/hostcoverage"

func newDoctorHostCapabilitiesDetails(matrix hostcoverage.Matrix, host hostcoverage.Host) *doctorCheckDetails {
	events := make([]doctorHostCapabilityEvent, 0, len(host.Events))
	for _, event := range matrix.LifecycleEvents {
		cell, ok := host.Events[event.ID]
		if !ok {
			continue
		}
		events = append(events, doctorHostCapabilityEvent{ID: event.ID, Status: string(cell.Status)})
	}
	return &doctorCheckDetails{HostCapabilities: &doctorHostCapabilitiesDetails{
		SchemaVersion: matrix.SchemaVersion,
		Source:        doctorHostCapabilitiesSource,
		LastVerified:  matrix.LastVerified,
		Host:          host.ID,
		Package:       host.Package,
		Events:        events,
	}}
}

// Codex capture inspection states. Only "complete" and "partial" carry
// boundaries, usage_state, and counts; the other states omit them so an
// uninspected diagnostic is never read as zero observed evidence.
const (
	codexCaptureInspectionComplete     = "complete"
	codexCaptureInspectionPartial      = "partial"
	codexCaptureInspectionReadFailed   = "read_failed"
	codexCaptureInspectionDependencies = "dependencies_unavailable"
	codexCaptureInspectionNotInspected = "not_inspected"

	codexCaptureReasonReadFailed   = "diagnostic_read_failed"
	codexCaptureReasonDependencies = "diagnostic_dependencies_unavailable"
	codexCaptureReasonNotInspected = "large_store_bounded"
)

// doctorCodexCaptureDetails exposes the existing codex-capture classification.
// It is evidence that native Codex hooks were recorded in this workspace
// window, not a process-alive or waiting-for-input signal.
type doctorCodexCaptureDetails struct {
	Inspection    string                       `json:"inspection"`
	Reason        string                       `json:"reason"`
	Surface       string                       `json:"surface"`
	HookTrust     string                       `json:"hook_trust"`
	WindowSeconds int64                        `json:"window_seconds,omitempty"`
	Boundaries    []doctorCodexCaptureBoundary `json:"boundaries,omitempty"`
	UsageState    string                       `json:"usage_state,omitempty"`
	Counts        *doctorCodexCaptureCounts    `json:"counts,omitempty"`
}

type doctorCodexCaptureBoundary struct {
	Name  string `json:"name"`
	State string `json:"state"`
}

type doctorCodexCaptureCounts struct {
	StoredEvents              int `json:"stored_events"`
	PromptTurns               int `json:"prompt_turns"`
	UncoveredFinalTurns       int `json:"uncovered_final_turns"`
	UsageObservations         int `json:"usage_observations"`
	HeadlessUsageObservations int `json:"headless_usage_observations"`
	StopSessions              int `json:"stop_sessions"`
	StopSessionsWithUsage     int `json:"stop_sessions_with_usage"`
	PendingDeliveries         int `json:"pending_deliveries"`
	UnscopedPendingDeliveries int `json:"unscoped_pending_deliveries"`
}

func newCodexCaptureInspectedDetails(
	identity codexCaptureIdentity,
	classification codexCaptureClassification,
	evidence codexCaptureEvidence,
	promptTurns int,
) *doctorCheckDetails {
	inspection := codexCaptureInspectionComplete
	if evidence.SpoolPartial {
		inspection = codexCaptureInspectionPartial
	}
	boundaries := make([]doctorCodexCaptureBoundary, 0, len(codexCaptureBoundaryOrder))
	for _, name := range codexCaptureBoundaryOrder {
		boundaries = append(boundaries, doctorCodexCaptureBoundary{Name: name, State: classification.Boundaries[name]})
	}
	return &doctorCheckDetails{CodexCapture: &doctorCodexCaptureDetails{
		Inspection:    inspection,
		Reason:        classification.Reason,
		Surface:       identity.Surface,
		HookTrust:     identity.HookTrust,
		WindowSeconds: int64(codexCaptureWindow.Seconds()),
		Boundaries:    boundaries,
		UsageState:    classification.UsageState,
		Counts: &doctorCodexCaptureCounts{
			StoredEvents:              evidence.StoredEvents,
			PromptTurns:               promptTurns,
			UncoveredFinalTurns:       evidence.UncoveredFinalTurns,
			UsageObservations:         evidence.UsageObservations,
			HeadlessUsageObservations: evidence.HeadlessUsageObservations,
			StopSessions:              evidence.StopSessions,
			StopSessionsWithUsage:     evidence.StopsWithUsage,
			PendingDeliveries:         evidence.PendingDeliveries,
			UnscopedPendingDeliveries: evidence.UnscopedDeliveries,
		},
	}}
}

func newCodexCaptureUninspectedDetails(identity codexCaptureIdentity, inspection, reason string) *doctorCheckDetails {
	return &doctorCheckDetails{CodexCapture: &doctorCodexCaptureDetails{
		Inspection: inspection,
		Reason:     reason,
		Surface:    identity.Surface,
		HookTrust:  identity.HookTrust,
	}}
}
