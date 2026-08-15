package cli

import (
	"context"
	"testing"
	"time"

	"github.com/duck8823/traceary/application/queryservice"
	apptypes "github.com/duck8823/traceary/application/types"
	"github.com/duck8823/traceary/domain/model"
	"github.com/duck8823/traceary/domain/types"
)

// hydrateCallTrackingEventUC is a minimal EventUsecase stand-in that records
// HydrateCommandAudits calls. Doctor checks that only need session/kind
// metadata must not invoke it.
type hydrateCallTrackingEventUC struct {
	listEvents   []*model.Event
	hydrateCalls int
}

func (u *hydrateCallTrackingEventUC) Log(context.Context, string, types.EventKind, types.Client, types.Agent, types.SessionID, types.Workspace, apptypes.LogRedaction) (apptypes.EventWriteResult, error) {
	return apptypes.EventWriteResult{}, nil
}
func (u *hydrateCallTrackingEventUC) DeleteTranscript(context.Context, types.EventID) error {
	return nil
}
func (u *hydrateCallTrackingEventUC) Audit(context.Context, apptypes.AuditInput, apptypes.AuditRedaction) (apptypes.EventWriteResult, *model.CommandAudit, error) {
	return apptypes.EventWriteResult{}, nil, nil
}
func (u *hydrateCallTrackingEventUC) Search(context.Context, apptypes.EventSearchCriteria) ([]*model.Event, error) {
	return nil, nil
}
func (u *hydrateCallTrackingEventUC) List(_ context.Context, _ apptypes.EventListCriteria) ([]*model.Event, error) {
	return u.listEvents, nil
}
func (u *hydrateCallTrackingEventUC) ListWindow(context.Context, apptypes.EventListCriteria) ([]*model.Event, error) {
	return nil, nil
}
func (u *hydrateCallTrackingEventUC) Show(context.Context, types.EventID) (apptypes.EventDetails, error) {
	return apptypes.EventDetails{}, nil
}
func (u *hydrateCallTrackingEventUC) Context(context.Context, apptypes.EventContextCriteria) ([]*model.Event, error) {
	return nil, nil
}
func (u *hydrateCallTrackingEventUC) Timeline(context.Context, apptypes.TimelineCriteria) ([]apptypes.TimelineBlock, error) {
	return nil, nil
}
func (u *hydrateCallTrackingEventUC) HydrateCommandAudits(context.Context, []*model.Event, queryservice.CommandAuditPayloadFields) error {
	u.hydrateCalls++
	return nil
}

func TestDoctorEventCoverageStaysMetadataOnly(t *testing.T) {
	t.Parallel()

	agent, err := types.AgentFrom("codex")
	if err != nil {
		t.Fatalf("AgentFrom() error = %v", err)
	}
	sessionID, err := types.SessionIDFrom("session-doctor-meta")
	if err != nil {
		t.Fatalf("SessionIDFrom() error = %v", err)
	}
	eventID, err := types.EventIDFrom("evt-doctor-meta")
	if err != nil {
		t.Fatalf("EventIDFrom() error = %v", err)
	}
	event := model.EventOf(
		eventID,
		types.EventKindNote,
		"cli",
		agent,
		sessionID,
		"duck8823/traceary",
		"note body",
		time.Date(2026, 4, 7, 12, 0, 0, 0, time.UTC),
	)
	uc := &hydrateCallTrackingEventUC{listEvents: []*model.Event{event}}
	root := NewRootCLI(WithEvent(uc))

	// event-coverage lists sessions/kinds only; it must not hydrate command
	// payloads (doctor_*.go stays metadata-only except sensitive-access).
	_ = root.inspectClientEventCoverage(context.Background(), "codex", "", "", 0.5)
	if uc.hydrateCalls != 0 {
		t.Fatalf("inspectClientEventCoverage hydrateCalls = %d, want 0 (metadata-only)", uc.hydrateCalls)
	}
}
