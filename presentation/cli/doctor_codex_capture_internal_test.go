package cli

import (
	"context"
	"strconv"
	"strings"
	"testing"
	"time"

	apptypes "github.com/duck8823/traceary/application/types"
	"github.com/duck8823/traceary/domain/types"
)

func TestClassifyCodexCapture(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name         string
		evidence     codexCaptureEvidence
		wantStatus   string
		wantReason   string
		wantUsage    string
		wantBoundary map[string]string
	}{
		{
			name: "workspace backlog is actionable and exposes pending boundaries",
			evidence: codexCaptureEvidence{
				PendingDeliveries: 4,
				StoredBoundaries:  map[string]bool{codexBoundaryTool: true},
				PendingBoundaries: map[string]bool{
					codexBoundarySessionStart: true,
					codexBoundaryPrompt:       true,
					codexBoundaryTool:         true,
					codexBoundaryUsage:        true,
				},
			},
			wantStatus: doctorStatusWarn,
			wantReason: codexCaptureReasonSpool,
			wantUsage:  codexBoundaryPending,
			wantBoundary: map[string]string{
				codexBoundarySessionStart: codexBoundaryPending,
				codexBoundaryPrompt:       codexBoundaryPending,
				codexBoundaryTool:         codexBoundaryStoredPending,
				codexBoundaryUsage:        codexBoundaryPending,
			},
		},
		{
			name: "stored stop without usage warns instead of returning unknown",
			evidence: codexCaptureEvidence{
				StoredEvents:      2,
				StoredBoundaries:  map[string]bool{codexBoundarySessionStart: true, codexBoundaryStop: true},
				PendingBoundaries: map[string]bool{},
			},
			wantStatus: doctorStatusWarn,
			wantReason: codexCaptureReasonUsage,
			wantUsage:  codexBoundaryNotObserved,
		},
		{
			name: "partial usage scan does not claim usage is missing",
			evidence: codexCaptureEvidence{
				StoredEvents:      500,
				UsageScanPartial:  true,
				StoredBoundaries:  map[string]bool{codexBoundaryStop: true},
				PendingBoundaries: map[string]bool{},
			},
			wantStatus: doctorStatusWarn,
			wantReason: codexCaptureReasonPartial,
			wantUsage:  "partial_scan_no_codex_observation",
		},
		{
			name: "no evidence never passes",
			evidence: codexCaptureEvidence{
				StoredBoundaries:  map[string]bool{},
				PendingBoundaries: map[string]bool{},
			},
			wantStatus: doctorStatusWarn,
			wantReason: codexCaptureReasonEmpty,
			wantUsage:  codexBoundaryNotObserved,
		},
		{
			name: "known and explicitly unavailable usage stay distinct",
			evidence: codexCaptureEvidence{
				StoredEvents:      5,
				UsageObservations: 2,
				UsageKnown:        1,
				UsageUnavailable:  1,
				StoredBoundaries: map[string]bool{
					codexBoundarySessionStart: true,
					codexBoundaryPrompt:       true,
					codexBoundaryTool:         true,
					codexBoundaryCompact:      true,
					codexBoundaryStop:         true,
					codexBoundaryUsage:        true,
				},
				PendingBoundaries: map[string]bool{},
			},
			wantStatus: doctorStatusPass,
			wantReason: codexCaptureReasonOK,
			wantUsage:  "mixed_known_and_unavailable",
			wantBoundary: map[string]string{
				codexBoundarySessionStart: codexBoundaryStored,
				codexBoundaryPrompt:       codexBoundaryStored,
				codexBoundaryTool:         codexBoundaryStored,
				codexBoundaryCompact:      codexBoundaryStored,
				codexBoundaryStop:         codexBoundaryStored,
				codexBoundaryUsage:        codexBoundaryStored,
			},
		},
		{
			name: "active session before stop is healthy but stop remains not observed",
			evidence: codexCaptureEvidence{
				StoredEvents:      2,
				StoredBoundaries:  map[string]bool{codexBoundarySessionStart: true, codexBoundaryPrompt: true},
				PendingBoundaries: map[string]bool{},
			},
			wantStatus: doctorStatusPass,
			wantReason: codexCaptureReasonOK,
			wantUsage:  codexBoundaryNotObserved,
			wantBoundary: map[string]string{
				codexBoundaryStop:  codexBoundaryNotObserved,
				codexBoundaryUsage: codexBoundaryNotObserved,
			},
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := classifyCodexCapture(tt.evidence)
			if got.Status != tt.wantStatus || got.Reason != tt.wantReason || got.UsageState != tt.wantUsage {
				t.Fatalf("classifyCodexCapture() = status %q reason %q usage %q, want %q %q %q", got.Status, got.Reason, got.UsageState, tt.wantStatus, tt.wantReason, tt.wantUsage)
			}
			for boundary, want := range tt.wantBoundary {
				if got.Boundaries[boundary] != want {
					t.Errorf("boundary %s = %q, want %q", boundary, got.Boundaries[boundary], want)
				}
			}
		})
	}
}

func TestProjectCodexSpoolMetadata_IsWorkspaceScopedAndBodyFree(t *testing.T) {
	t.Parallel()
	repo := t.TempDir()
	now := time.Date(2026, 7, 23, 1, 2, 3, 0, time.UTC)
	private := "private prompt and tool output must not escape"
	records := []hookSpoolRecord{
		{
			Client: "codex", Command: "session", Action: "start", CreatedAt: now,
			Payload: `{"session_id":"same-session","cwd":` + strconv.Quote(repo) + `,"prompt":"` + private + `"}`,
		},
		{
			Client: "codex", Command: "usage", CreatedAt: now.Add(time.Second),
			Payload: `{"session_id":"same-session","event_id":"stop-1"}`,
		},
		{
			Client: "codex", Command: "prompt", CreatedAt: now.Add(2 * time.Second),
			Payload: `{"session_id":"other-session","cwd":"/different/workspace","prompt":"` + private + `"}`,
		},
		{
			Client: "claude", Command: "prompt", CreatedAt: now.Add(3 * time.Second),
			Payload: `{"session_id":"same-session","cwd":` + strconv.Quote(repo) + `}`,
		},
	}

	target := types.Workspace(normalizeLocalWorkContextPath(repo))
	got, unscoped := projectCodexSpoolMetadata(context.Background(), records, target)
	if unscoped != 0 {
		t.Fatalf("unscoped = %d, want 0", unscoped)
	}
	if len(got) != 2 {
		t.Fatalf("scoped records = %d, want 2: %+v", len(got), got)
	}
	rendered := formatCodexBoundaryStates(classifyCodexCapture(codexCaptureEvidence{
		PendingDeliveries: len(got),
		StoredBoundaries:  map[string]bool{},
		PendingBoundaries: map[string]bool{
			codexBoundarySessionStart: true,
			codexBoundaryUsage:        true,
		},
	}).Boundaries)
	if strings.Contains(rendered, private) || strings.Contains(rendered, "same-session") {
		t.Fatalf("body-free diagnostic leaked private/session data: %q", rendered)
	}
	if !strings.Contains(rendered, "session_start:delivery_pending") ||
		!strings.Contains(rendered, "usage:delivery_pending") {
		t.Fatalf("boundary output = %q", rendered)
	}
}

func TestCodexSpoolBoundaries(t *testing.T) {
	t.Parallel()
	tests := []struct {
		command string
		action  string
		want    string
	}{
		{command: "session", action: "start", want: "session_start"},
		{command: "prompt", want: "prompt"},
		{command: "audit", want: "tool"},
		{command: "compact", action: "post-compact", want: "compact"},
		{command: "transcript", want: "stop"},
		{command: "usage", want: "stop,usage"},
	}
	for _, tt := range tests {
		got := strings.Join(codexSpoolBoundaries(tt.command, tt.action), ",")
		if got != tt.want {
			t.Errorf("codexSpoolBoundaries(%q, %q) = %q, want %q", tt.command, tt.action, got, tt.want)
		}
	}
}

func TestRootCLI_InspectCodexCapture_ReportsSurfaceAndCanonicalEvidence(t *testing.T) {
	t.Parallel()
	projectDir := t.TempDir()
	workspace := types.Workspace(normalizeLocalWorkContextPath(projectDir))
	now := time.Now().UTC().Add(-time.Minute)
	bodyExtent, err := apptypes.EventBodyExtentOf(
		types.None[int](),
		0,
		types.None[bool](),
		types.None[bool](),
		types.None[int](),
	)
	if err != nil {
		t.Fatalf("EventBodyExtentOf() error = %v", err)
	}
	event, err := apptypes.EventMetadataOf(
		types.EventID("event-stop"),
		types.EventKindTranscript,
		types.Client("hook"),
		types.Agent("codex"),
		types.SessionID("session-body-free"),
		workspace,
		"stop",
		now,
		bodyExtent,
		types.None[apptypes.CommandAuditMetadata](),
	)
	if err != nil {
		t.Fatalf("EventMetadataOf() error = %v", err)
	}
	root := &RootCLI{
		eventMetadata: &codexCaptureEventMetadataStub{events: []apptypes.EventMetadata{event}},
		report: &codexCaptureReportStub{snapshot: apptypes.ReportSnapshot{
			Usage: apptypes.ReportUsageSnapshot{Aggregates: []apptypes.ReportUsageAggregateRow{
				{
					Engine:       "codex",
					Observations: 1,
					TotalTokens:  apptypes.ReportUsageMetric{UnavailableObservations: 1},
				},
			}},
		}},
	}
	check := root.inspectCodexCapture(
		context.Background(),
		projectDir,
		"0.32.0",
		codexPluginHookFallbackState{
			PluginEnabled: true,
			PluginKey:     "traceary@traceary-marketplace",
		},
		codexPluginHookTrustResult{
			PluginKey: "traceary@traceary-marketplace",
			Status:    codexPluginHookTrustTrusted,
		},
		nil,
		nil,
	)
	if check.Status != doctorStatusPass {
		t.Fatalf("status = %q, want pass: %s", check.Status, check.Message)
	}
	for _, want := range []string{
		"surface=plugin_managed_hooks",
		"client=codex",
		"traceary_version=0.32.0",
		"plugin=traceary@traceary-marketplace",
		"hook_trust=trusted",
		"workspace=" + workspace.String(),
		"reason=capture_observed",
		"stop:stored",
		"usage:stored",
		"usage=unavailable_recorded",
	} {
		if !strings.Contains(check.Message, want) {
			t.Errorf("message missing %q: %s", want, check.Message)
		}
	}
	if strings.Contains(check.Message, "session-body-free") {
		t.Fatalf("diagnostic leaked session identity: %s", check.Message)
	}
}

// These fakes implement only body-free read contracts and return prebuilt
// observable state; they do not duplicate production classification behavior.
type codexCaptureEventMetadataStub struct {
	events []apptypes.EventMetadata
	err    error
}

func (s *codexCaptureEventMetadataStub) List(context.Context, apptypes.EventListCriteria) ([]apptypes.EventMetadata, error) {
	return s.events, s.err
}

func (*codexCaptureEventMetadataStub) Search(context.Context, apptypes.EventSearchCriteria) ([]apptypes.EventMetadata, error) {
	return nil, nil
}

func (*codexCaptureEventMetadataStub) Context(context.Context, apptypes.EventContextCriteria) ([]apptypes.EventMetadata, error) {
	return nil, nil
}

type codexCaptureReportStub struct {
	snapshot apptypes.ReportSnapshot
	err      error
}

func (s *codexCaptureReportStub) Generate(context.Context, apptypes.ReportCriteria) (apptypes.ReportSnapshot, error) {
	return s.snapshot, s.err
}
