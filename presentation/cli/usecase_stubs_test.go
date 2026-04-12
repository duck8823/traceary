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

type memoryUsecaseStub struct {
	listResult       []apptypes.MemorySummary
	listErr          error
	searchResult     []apptypes.MemorySummary
	searchErr        error
	showDetails      apptypes.MemoryDetails
	showErr          error
	rememberDetails  apptypes.MemoryDetails
	rememberErr      error
	proposeDetails   apptypes.MemoryDetails
	proposeErr       error
	acceptDetails    apptypes.MemoryDetails
	acceptErr        error
	rejectDetails    apptypes.MemoryDetails
	rejectErr        error
	supersedeDetails apptypes.MemoryDetails
	supersedeErr     error
	expireDetails    apptypes.MemoryDetails
	expireErr        error

	rememberCall struct {
		memoryType   types.MemoryType
		scope        types.MemoryScope
		fact         string
		confidence   types.Optional[types.Confidence]
		source       types.MemorySource
		evidenceRefs []types.EvidenceRef
		artifactRefs []types.ArtifactRef
	}
	listCriteria   apptypes.MemoryListCriteria
	searchCriteria apptypes.MemorySearchCriteria
	showMemoryID   types.MemoryID
	acceptCall     struct {
		memoryID   types.MemoryID
		confidence types.Optional[types.Confidence]
	}
}

func (s *memoryUsecaseStub) Remember(_ context.Context, memoryType types.MemoryType, scope types.MemoryScope, fact string, confidence types.Optional[types.Confidence], source types.MemorySource, evidenceRefs []types.EvidenceRef, artifactRefs []types.ArtifactRef) (apptypes.MemoryDetails, error) {
	s.rememberCall.memoryType = memoryType
	s.rememberCall.scope = scope
	s.rememberCall.fact = fact
	s.rememberCall.confidence = confidence
	s.rememberCall.source = source
	s.rememberCall.evidenceRefs = append([]types.EvidenceRef(nil), evidenceRefs...)
	s.rememberCall.artifactRefs = append([]types.ArtifactRef(nil), artifactRefs...)
	return s.rememberDetails, s.rememberErr
}

func (s *memoryUsecaseStub) Propose(_ context.Context, _ types.MemoryType, _ types.MemoryScope, _ string, _ types.MemorySource, _ []types.EvidenceRef, _ []types.ArtifactRef) (apptypes.MemoryDetails, error) {
	return s.proposeDetails, s.proposeErr
}

func (s *memoryUsecaseStub) Accept(_ context.Context, memoryID types.MemoryID, confidence types.Optional[types.Confidence]) (apptypes.MemoryDetails, error) {
	s.acceptCall.memoryID = memoryID
	s.acceptCall.confidence = confidence
	return s.acceptDetails, s.acceptErr
}

func (s *memoryUsecaseStub) Reject(_ context.Context, _ types.MemoryID) (apptypes.MemoryDetails, error) {
	return s.rejectDetails, s.rejectErr
}

func (s *memoryUsecaseStub) Supersede(_ context.Context, _ types.MemoryID, _ types.MemoryType, _ types.MemoryScope, _ string, _ types.Optional[types.Confidence], _ types.MemorySource, _ []types.EvidenceRef, _ []types.ArtifactRef) (apptypes.MemoryDetails, error) {
	return s.supersedeDetails, s.supersedeErr
}

func (s *memoryUsecaseStub) Expire(_ context.Context, _ types.MemoryID, _ types.Optional[time.Time]) (apptypes.MemoryDetails, error) {
	return s.expireDetails, s.expireErr
}

func (s *memoryUsecaseStub) List(_ context.Context, criteria apptypes.MemoryListCriteria) ([]apptypes.MemorySummary, error) {
	s.listCriteria = criteria
	return s.listResult, s.listErr
}

func (s *memoryUsecaseStub) Search(_ context.Context, criteria apptypes.MemorySearchCriteria) ([]apptypes.MemorySummary, error) {
	s.searchCriteria = criteria
	return s.searchResult, s.searchErr
}

func (s *memoryUsecaseStub) Show(_ context.Context, memoryID types.MemoryID) (apptypes.MemoryDetails, error) {
	s.showMemoryID = memoryID
	return s.showDetails, s.showErr
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
