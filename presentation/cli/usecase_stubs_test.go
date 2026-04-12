package cli_test

import (
	"context"
	"time"

	apptypes "github.com/duck8823/traceary/application/types"
	"github.com/duck8823/traceary/domain/model"
	"github.com/duck8823/traceary/domain/types"
)

// eventUsecaseStub implements usecase.EventUsecase for testing.
type eventUsecaseStub struct {
	logEvent       *model.Event
	logErr         error
	auditEvent     *model.Event
	auditAudit     *model.CommandAudit
	auditErr       error
	searchEvents   []*model.Event
	searchErr      error
	listEvents     []*model.Event
	listErr        error
	showDetails    apptypes.EventDetails
	showErr        error
	contextEvents  []*model.Event
	contextErr     error
	timelineBlocks []apptypes.TimelineBlock
	timelineErr    error
}

func (s *eventUsecaseStub) Log(_ context.Context, _ string, _ types.EventKind, _ types.Client, _ types.Agent, _ types.SessionID, _ types.Workspace) (*model.Event, error) {
	return s.logEvent, s.logErr
}
func (s *eventUsecaseStub) Audit(_ context.Context, _ string, _ string, _ string, _ types.Client, _ types.Agent, _ types.SessionID, _ types.Workspace, _ types.Optional[int], _ apptypes.AuditRedaction) (*model.Event, *model.CommandAudit, error) {
	return s.auditEvent, s.auditAudit, s.auditErr
}
func (s *eventUsecaseStub) Search(_ context.Context, _ apptypes.EventSearchCriteria) ([]*model.Event, error) {
	return s.searchEvents, s.searchErr
}
func (s *eventUsecaseStub) List(_ context.Context, _ apptypes.EventListCriteria) ([]*model.Event, error) {
	return s.listEvents, s.listErr
}
func (s *eventUsecaseStub) Show(_ context.Context, _ types.EventID) (apptypes.EventDetails, error) {
	return s.showDetails, s.showErr
}
func (s *eventUsecaseStub) Context(_ context.Context, _ apptypes.EventContextCriteria) ([]*model.Event, error) {
	return s.contextEvents, s.contextErr
}
func (s *eventUsecaseStub) Timeline(_ context.Context, _ apptypes.TimelineCriteria) ([]apptypes.TimelineBlock, error) {
	return s.timelineBlocks, s.timelineErr
}

// sessionUsecaseStub implements usecase.SessionUsecase for testing.
type sessionUsecaseStub struct {
	startEvent  *model.Event
	startErr    error
	endEvent    *model.Event
	endErr      error
	labelErr    error
	listResult  []apptypes.SessionSummary
	listErr     error
	treeResult  []apptypes.SessionSummary
	treeErr     error
	activeEvent *model.Event
	activeErr   error
	latestEvent *model.Event
	latestErr   error
	handoff     types.Optional[apptypes.HandoffSummary]
	handoffErr  error
}

func (s *sessionUsecaseStub) Start(_ context.Context, _ types.Client, _ types.Agent, _ types.SessionID, _ types.Workspace, _ types.SessionID) (*model.Event, error) {
	return s.startEvent, s.startErr
}
func (s *sessionUsecaseStub) End(_ context.Context, _ types.Client, _ types.Agent, _ types.SessionID, _ types.Workspace, _ string) (*model.Event, error) {
	return s.endEvent, s.endErr
}
func (s *sessionUsecaseStub) Label(_ context.Context, _ types.SessionID, _ string) error {
	return s.labelErr
}
func (s *sessionUsecaseStub) List(_ context.Context, _ apptypes.SessionListCriteria) ([]apptypes.SessionSummary, error) {
	return s.listResult, s.listErr
}
func (s *sessionUsecaseStub) Tree(_ context.Context, _ types.Workspace, _ int) ([]apptypes.SessionSummary, error) {
	return s.treeResult, s.treeErr
}
func (s *sessionUsecaseStub) Active(_ context.Context, _ apptypes.SessionLookupCriteria) (types.Optional[*model.Event], error) {
	if s.activeEvent == nil && s.activeErr == nil {
		return types.Empty[*model.Event](), nil
	}
	if s.activeErr != nil {
		return types.Empty[*model.Event](), s.activeErr
	}
	return types.Of(s.activeEvent), nil
}
func (s *sessionUsecaseStub) Latest(_ context.Context, _ apptypes.SessionLookupCriteria) (types.Optional[*model.Event], error) {
	if s.latestEvent == nil && s.latestErr == nil {
		return types.Empty[*model.Event](), nil
	}
	if s.latestErr != nil {
		return types.Empty[*model.Event](), s.latestErr
	}
	return types.Of(s.latestEvent), nil
}
func (s *sessionUsecaseStub) Handoff(_ context.Context, _ types.SessionID, _ types.Workspace, _ int) (types.Optional[apptypes.HandoffSummary], error) {
	return s.handoff, s.handoffErr
}

// storeManagementUsecaseStub implements usecase.StoreManagementUsecase for testing.
type storeManagementUsecaseStub struct {
	initCalled      bool
	initErr         error
	createBackupErr error
	restoreErr      error
	gcResult        apptypes.CollectGarbageResult
	gcErr           error
	staleResult     apptypes.CloseStaleSessionsResult
	staleErr        error
}

func (s *storeManagementUsecaseStub) Initialize(_ context.Context) error {
	s.initCalled = true
	return s.initErr
}
func (s *storeManagementUsecaseStub) CreateBackup(_ context.Context, _ string, _ bool) error {
	return s.createBackupErr
}
func (s *storeManagementUsecaseStub) RestoreBackup(_ context.Context, _ string, _ bool) error {
	return s.restoreErr
}
func (s *storeManagementUsecaseStub) CollectGarbage(_ context.Context, _ time.Time, _ bool) (apptypes.CollectGarbageResult, error) {
	return s.gcResult, s.gcErr
}
func (s *storeManagementUsecaseStub) CloseStaleSessions(_ context.Context, _ time.Duration, _ bool) (apptypes.CloseStaleSessionsResult, error) {
	return s.staleResult, s.staleErr
}
