package cli_test

import (
	"context"
	"time"

	"github.com/duck8823/traceary/application/usecase"
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
	showDetails    *usecase.EventDetails
	showErr        error
	contextEvents  []*model.Event
	contextErr     error
	timelineBlocks []*usecase.TimelineBlock
	timelineErr    error
}

func (s *eventUsecaseStub) Log(_ context.Context, _ string, _ types.EventKind, _ types.Client, _ types.Agent, _ types.SessionID, _ types.Workspace) (*model.Event, error) {
	return s.logEvent, s.logErr
}
func (s *eventUsecaseStub) Audit(_ context.Context, _ string, _ string, _ string, _ types.Client, _ types.Agent, _ types.SessionID, _ types.Workspace, _ *int, _ usecase.AuditRedaction) (*model.Event, *model.CommandAudit, error) {
	return s.auditEvent, s.auditAudit, s.auditErr
}
func (s *eventUsecaseStub) Search(_ context.Context, _ usecase.EventSearchCriteria) ([]*model.Event, error) {
	return s.searchEvents, s.searchErr
}
func (s *eventUsecaseStub) List(_ context.Context, _ usecase.EventListCriteria) ([]*model.Event, error) {
	return s.listEvents, s.listErr
}
func (s *eventUsecaseStub) Show(_ context.Context, _ types.EventID) (*usecase.EventDetails, error) {
	return s.showDetails, s.showErr
}
func (s *eventUsecaseStub) Context(_ context.Context, _ usecase.EventContextCriteria) ([]*model.Event, error) {
	return s.contextEvents, s.contextErr
}
func (s *eventUsecaseStub) Timeline(_ context.Context, _ usecase.TimelineCriteria) ([]*usecase.TimelineBlock, error) {
	return s.timelineBlocks, s.timelineErr
}

// sessionUsecaseStub implements usecase.SessionUsecase for testing.
type sessionUsecaseStub struct {
	startEvent  *model.Event
	startErr    error
	endEvent    *model.Event
	endErr      error
	labelErr    error
	listResult  []*usecase.SessionSummary
	listErr     error
	treeResult  []*usecase.SessionSummary
	treeErr     error
	activeEvent *model.Event
	activeErr   error
	latestEvent *model.Event
	latestErr   error
	handoff     *usecase.HandoffSummary
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
func (s *sessionUsecaseStub) List(_ context.Context, _ usecase.SessionListCriteria) ([]*usecase.SessionSummary, error) {
	return s.listResult, s.listErr
}
func (s *sessionUsecaseStub) Tree(_ context.Context, _ types.Workspace, _ int) ([]*usecase.SessionSummary, error) {
	return s.treeResult, s.treeErr
}
func (s *sessionUsecaseStub) Active(_ context.Context, _ usecase.SessionLookupCriteria) (types.Optional[*model.Event], error) {
	if s.activeEvent == nil && s.activeErr == nil {
		return types.Empty[*model.Event](), nil
	}
	if s.activeErr != nil {
		return types.Empty[*model.Event](), s.activeErr
	}
	return types.Of(s.activeEvent), nil
}
func (s *sessionUsecaseStub) Latest(_ context.Context, _ usecase.SessionLookupCriteria) (types.Optional[*model.Event], error) {
	if s.latestEvent == nil && s.latestErr == nil {
		return types.Empty[*model.Event](), nil
	}
	if s.latestErr != nil {
		return types.Empty[*model.Event](), s.latestErr
	}
	return types.Of(s.latestEvent), nil
}
func (s *sessionUsecaseStub) Handoff(_ context.Context, _ types.SessionID, _ types.Workspace, _ int) (*usecase.HandoffSummary, error) {
	return s.handoff, s.handoffErr
}

// storeMaintenanceUsecaseStub implements usecase.StoreMaintenanceUsecase for testing.
type storeMaintenanceUsecaseStub struct {
	initCalled      bool
	initErr         error
	createBackupErr error
	restoreErr      error
	gcResult        *usecase.CollectGarbageResult
	gcErr           error
	staleResult     *usecase.CloseStaleSessionsResult
	staleErr        error
}

func (s *storeMaintenanceUsecaseStub) Initialize(_ context.Context) error {
	s.initCalled = true
	return s.initErr
}
func (s *storeMaintenanceUsecaseStub) CreateBackup(_ context.Context, _ string, _ bool) error {
	return s.createBackupErr
}
func (s *storeMaintenanceUsecaseStub) RestoreBackup(_ context.Context, _ string, _ bool) error {
	return s.restoreErr
}
func (s *storeMaintenanceUsecaseStub) CollectGarbage(_ context.Context, _ time.Time, _ bool) (*usecase.CollectGarbageResult, error) {
	return s.gcResult, s.gcErr
}
func (s *storeMaintenanceUsecaseStub) CloseStaleSessions(_ context.Context, _ time.Duration, _ bool) (*usecase.CloseStaleSessionsResult, error) {
	return s.staleResult, s.staleErr
}
