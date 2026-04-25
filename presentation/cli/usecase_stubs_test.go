package cli_test

import (
	"context"
	"time"

	apptypes "github.com/duck8823/traceary/application/types"
	"github.com/duck8823/traceary/application/usecase"
	"github.com/duck8823/traceary/domain/model"
	"github.com/duck8823/traceary/domain/types"
)

// eventUsecaseStub implements usecase.EventUsecase for testing.
type eventUsecaseStub struct {
	logEvent         *model.Event
	logErr           error
	auditEvent       *model.Event
	auditAudit       *model.CommandAudit
	auditErr         error
	searchEvents     []*model.Event
	searchErr        error
	listEvents       []*model.Event
	listErr          error
	showDetails      apptypes.EventDetails
	showErr          error
	contextEvents    []*model.Event
	contextErr       error
	timelineBlocks   []apptypes.TimelineBlock
	timelineErr      error
	timelineCriteria apptypes.TimelineCriteria

	logCall struct {
		message    string
		kind       types.EventKind
		client     types.Client
		agent      types.Agent
		sessionID  types.SessionID
		workspace  types.Workspace
		logCfg     apptypes.LogRedaction
		sourceHook string
	}
	auditCall struct {
		command   string
		input     string
		output    string
		client    types.Client
		agent     types.Agent
		sessionID types.SessionID
		workspace types.Workspace
		exitCode  types.Optional[int]
		auditCfg  apptypes.AuditRedaction
	}
}

func (s *eventUsecaseStub) Log(ctx context.Context, message string, kind types.EventKind, client types.Client, agent types.Agent, sessionID types.SessionID, workspace types.Workspace, logCfg apptypes.LogRedaction) (*model.Event, error) {
	s.logCall.message = message
	s.logCall.kind = kind
	s.logCall.client = client
	s.logCall.agent = agent
	s.logCall.sessionID = sessionID
	s.logCall.workspace = workspace
	s.logCall.logCfg = logCfg
	s.logCall.sourceHook = apptypes.SourceHookFromContext(ctx)
	return s.logEvent, s.logErr
}
func (s *eventUsecaseStub) Audit(_ context.Context, command string, input string, output string, client types.Client, agent types.Agent, sessionID types.SessionID, workspace types.Workspace, exitCode types.Optional[int], auditCfg apptypes.AuditRedaction) (*model.Event, *model.CommandAudit, error) {
	s.auditCall.command = command
	s.auditCall.input = input
	s.auditCall.output = output
	s.auditCall.client = client
	s.auditCall.agent = agent
	s.auditCall.sessionID = sessionID
	s.auditCall.workspace = workspace
	s.auditCall.exitCode = exitCode
	s.auditCall.auditCfg = auditCfg
	return s.auditEvent, s.auditAudit, s.auditErr
}
func (s *eventUsecaseStub) Search(_ context.Context, _ apptypes.EventSearchCriteria) ([]*model.Event, error) {
	return s.searchEvents, s.searchErr
}
func (s *eventUsecaseStub) List(_ context.Context, _ apptypes.EventListCriteria) ([]*model.Event, error) {
	return s.listEvents, s.listErr
}
func (s *eventUsecaseStub) ListWindow(_ context.Context, _ apptypes.EventListCriteria) ([]*model.Event, error) {
	return s.listEvents, s.listErr
}
func (s *eventUsecaseStub) Show(_ context.Context, _ types.EventID) (apptypes.EventDetails, error) {
	return s.showDetails, s.showErr
}
func (s *eventUsecaseStub) Context(_ context.Context, _ apptypes.EventContextCriteria) ([]*model.Event, error) {
	return s.contextEvents, s.contextErr
}
func (s *eventUsecaseStub) Timeline(_ context.Context, criteria apptypes.TimelineCriteria) ([]apptypes.TimelineBlock, error) {
	s.timelineCriteria = criteria
	return s.timelineBlocks, s.timelineErr
}

// sessionUsecaseStub implements usecase.SessionUsecase for testing.
type sessionUsecaseStub struct {
	startEvent     *model.Event
	startErr       error
	endEvent       *model.Event
	endErr         error
	labelErr       error
	listResult     []apptypes.SessionSummary
	listErr        error
	listCriteria   apptypes.SessionListCriteria
	treeResult     []apptypes.SessionSummary
	treeErr        error
	lineageResult  []apptypes.SessionSummary
	lineageErr     error
	activeEvent    *model.Event
	activeErr      error
	activeCriteria apptypes.SessionLookupCriteria
	latestEvent    *model.Event
	latestErr      error
	latestCriteria apptypes.SessionLookupCriteria
	handoff        types.Optional[apptypes.HandoffSummary]
	handoffErr     error

	startCall struct {
		client          types.Client
		agent           types.Agent
		sessionID       types.SessionID
		workspace       types.Workspace
		parentSessionID types.SessionID
	}
	startChildCall struct {
		parent       types.SessionID
		childID      types.SessionID
		agent        types.Agent
		workspace    types.Workspace
		spawnEventID types.EventID
		kind         string
		startedAt    time.Time
	}
	startChildCalls []struct {
		parent       types.SessionID
		childID      types.SessionID
		agent        types.Agent
		workspace    types.Workspace
		spawnEventID types.EventID
		kind         string
		startedAt    time.Time
	}
	endCall struct {
		client    types.Client
		agent     types.Agent
		sessionID types.SessionID
		workspace types.Workspace
		summary   string
	}
	endCalls []struct {
		client    types.Client
		agent     types.Agent
		sessionID types.SessionID
		workspace types.Workspace
		summary   string
	}
}

func (s *sessionUsecaseStub) Start(_ context.Context, client types.Client, agent types.Agent, sessionID types.SessionID, workspace types.Workspace, parentSessionID types.SessionID) (*model.Event, error) {
	s.startCall.client = client
	s.startCall.agent = agent
	s.startCall.sessionID = sessionID
	s.startCall.workspace = workspace
	s.startCall.parentSessionID = parentSessionID
	return s.startEvent, s.startErr
}
func (s *sessionUsecaseStub) StartChild(_ context.Context, parent types.SessionID, childID types.SessionID, agent types.Agent, workspace types.Workspace, spawnEventID types.EventID, kind string, startedAt time.Time) (*model.Event, error) {
	s.startChildCall.parent = parent
	s.startChildCall.childID = childID
	s.startChildCall.agent = agent
	s.startChildCall.workspace = workspace
	s.startChildCall.spawnEventID = spawnEventID
	s.startChildCall.kind = kind
	s.startChildCall.startedAt = startedAt
	s.startChildCalls = append(s.startChildCalls, s.startChildCall)
	return s.startEvent, s.startErr
}
func (s *sessionUsecaseStub) End(_ context.Context, client types.Client, agent types.Agent, sessionID types.SessionID, workspace types.Workspace, summary string) (*model.Event, error) {
	s.endCall.client = client
	s.endCall.agent = agent
	s.endCall.sessionID = sessionID
	s.endCall.workspace = workspace
	s.endCall.summary = summary
	s.endCalls = append(s.endCalls, s.endCall)
	return s.endEvent, s.endErr
}
func (s *sessionUsecaseStub) Label(_ context.Context, _ types.SessionID, _ string) error {
	return s.labelErr
}
func (s *sessionUsecaseStub) List(_ context.Context, criteria apptypes.SessionListCriteria) ([]apptypes.SessionSummary, error) {
	s.listCriteria = criteria
	return s.listResult, s.listErr
}
func (s *sessionUsecaseStub) Tree(_ context.Context, _ types.Workspace, _ types.SessionID, _ int) ([]apptypes.SessionSummary, error) {
	if s.treeResult == nil && s.treeErr == nil {
		return s.listResult, s.listErr
	}
	return s.treeResult, s.treeErr
}
func (s *sessionUsecaseStub) Lineage(_ context.Context, _ types.SessionID) ([]apptypes.SessionSummary, error) {
	if s.lineageResult == nil && s.lineageErr == nil {
		return s.listResult, s.listErr
	}
	return s.lineageResult, s.lineageErr
}
func (s *sessionUsecaseStub) Active(_ context.Context, criteria apptypes.SessionLookupCriteria) (types.Optional[*model.Event], error) {
	s.activeCriteria = criteria
	if s.activeEvent == nil && s.activeErr == nil {
		return types.None[*model.Event](), nil
	}
	if s.activeErr != nil {
		return types.None[*model.Event](), s.activeErr
	}
	return types.Some(s.activeEvent), nil
}
func (s *sessionUsecaseStub) Latest(_ context.Context, criteria apptypes.SessionLookupCriteria) (types.Optional[*model.Event], error) {
	s.latestCriteria = criteria
	if s.latestEvent == nil && s.latestErr == nil {
		return types.None[*model.Event](), nil
	}
	if s.latestErr != nil {
		return types.None[*model.Event](), s.latestErr
	}
	return types.Some(s.latestEvent), nil
}
func (s *sessionUsecaseStub) Handoff(_ context.Context, _ types.SessionID, _ types.Workspace, _ int) (types.Optional[apptypes.HandoffSummary], error) {
	return s.handoff, s.handoffErr
}

type contextUsecaseStub struct {
	handoff      types.Optional[apptypes.ContextPack]
	handoffErr   error
	handoffCalls []apptypes.ContextPackCriteria
}

func (s *contextUsecaseStub) Handoff(_ context.Context, criteria apptypes.ContextPackCriteria) (types.Optional[apptypes.ContextPack], error) {
	s.handoffCalls = append(s.handoffCalls, criteria)
	return s.handoff, s.handoffErr
}

type codexIntegrationUsecaseStub struct {
	installResult   apptypes.CodexIntegrationInstallResult
	installErr      error
	uninstallResult apptypes.CodexIntegrationUninstallResult
	uninstallErr    error

	installCall struct {
		repoRoot        string
		codexHome       string
		marketplaceRoot string
		tracearyBin     string
	}
	uninstallCall struct {
		codexHome       string
		marketplaceRoot string
	}
}

func (s *codexIntegrationUsecaseStub) Install(
	_ context.Context,
	repoRoot string,
	codexHome string,
	marketplaceRoot string,
	tracearyBin string,
) (apptypes.CodexIntegrationInstallResult, error) {
	s.installCall.repoRoot = repoRoot
	s.installCall.codexHome = codexHome
	s.installCall.marketplaceRoot = marketplaceRoot
	s.installCall.tracearyBin = tracearyBin
	return s.installResult, s.installErr
}

func (s *codexIntegrationUsecaseStub) Uninstall(
	_ context.Context,
	codexHome string,
	marketplaceRoot string,
) (apptypes.CodexIntegrationUninstallResult, error) {
	s.uninstallCall.codexHome = codexHome
	s.uninstallCall.marketplaceRoot = marketplaceRoot
	return s.uninstallResult, s.uninstallErr
}

type memoryEdgeUsecaseStub struct {
	addEdge   *model.MemoryEdge
	addErr    error
	listEdges []*model.MemoryEdge
	listErr   error
}

func (s *memoryEdgeUsecaseStub) Add(_ context.Context, _ types.MemoryID, _ types.MemoryID, _ types.MemoryEdgeRelation, _ types.Optional[time.Time], _ types.Optional[time.Time]) (*model.MemoryEdge, error) {
	return s.addEdge, s.addErr
}

func (s *memoryEdgeUsecaseStub) List(_ context.Context, _ model.MemoryEdgeListFilter) ([]*model.MemoryEdge, error) {
	return s.listEdges, s.listErr
}

type bundleUsecaseStub struct {
	importResult  usecase.BundleImportResult
	importErr     error
	exportErr     error
	exportOptions usecase.BundleExportOptions
}

func (s *bundleUsecaseStub) Export(_ context.Context, options usecase.BundleExportOptions) error {
	s.exportOptions = options
	return s.exportErr
}

func (s *bundleUsecaseStub) Import(_ context.Context, _ usecase.BundleImportOptions) (usecase.BundleImportResult, error) {
	return s.importResult, s.importErr
}

type memoryUsecaseStub struct {
	listResult         []apptypes.MemorySummary
	listErr            error
	searchResult       []apptypes.MemorySummary
	searchErr          error
	showDetails        apptypes.MemoryDetails
	showErr            error
	rememberDetails    apptypes.MemoryDetails
	rememberErr        error
	proposeDetails     apptypes.MemoryDetails
	proposeErr         error
	acceptDetails      apptypes.MemoryDetails
	acceptErr          error
	rejectDetails      apptypes.MemoryDetails
	rejectErr          error
	supersedeDetails   apptypes.MemoryDetails
	supersedeErr       error
	expireDetails      apptypes.MemoryDetails
	expireErr          error
	setValidityDetails apptypes.MemoryDetails
	setValidityErr     error
	extractDetails     []apptypes.MemoryDetails
	extractErr         error
	importResult       apptypes.MemoryImportResult
	importErr          error
	bridgeImportResult apptypes.MemoryBridgeImportResult
	bridgeImportErr    error
	scanResult         apptypes.MemoryHygieneScanResult
	scanErr            error
	applyResult        apptypes.MemoryHygieneApplyResult
	applyErr           error
	exportResult       apptypes.MemoryExportResult
	exportErr          error

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
	acceptCallCount int
	rejectCallCount int

	setValidityCall struct {
		memoryID  types.MemoryID
		validFrom types.Optional[time.Time]
		validTo   types.Optional[time.Time]
		clearTo   bool
	}
	setValidityCallCount int
	extractCriteria      apptypes.MemoryExtractionCriteria
	importCalls          []apptypes.CodexImportCriteria
	bridgeImportCalls    []apptypes.MemoryBridgeImportCriteria
	exportCalls          []apptypes.MemoryExportCriteria
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
	s.acceptCallCount++
	return s.acceptDetails, s.acceptErr
}

func (s *memoryUsecaseStub) Reject(_ context.Context, _ types.MemoryID) (apptypes.MemoryDetails, error) {
	s.rejectCallCount++
	return s.rejectDetails, s.rejectErr
}

func (s *memoryUsecaseStub) Supersede(_ context.Context, _ types.MemoryID, _ types.MemoryType, _ types.MemoryScope, _ string, _ types.Optional[types.Confidence], _ types.MemorySource, _ []types.EvidenceRef, _ []types.ArtifactRef, _ types.Optional[time.Time], _ types.Optional[time.Time]) (apptypes.MemoryDetails, error) {
	return s.supersedeDetails, s.supersedeErr
}

func (s *memoryUsecaseStub) Expire(_ context.Context, _ types.MemoryID, _ types.Optional[time.Time]) (apptypes.MemoryDetails, error) {
	return s.expireDetails, s.expireErr
}

func (s *memoryUsecaseStub) SetValidity(_ context.Context, memoryID types.MemoryID, validFrom types.Optional[time.Time], validTo types.Optional[time.Time], clearTo bool) (apptypes.MemoryDetails, error) {
	s.setValidityCall.memoryID = memoryID
	s.setValidityCall.validFrom = validFrom
	s.setValidityCall.validTo = validTo
	s.setValidityCall.clearTo = clearTo
	s.setValidityCallCount++
	return s.setValidityDetails, s.setValidityErr
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

func (s *memoryUsecaseStub) Extract(_ context.Context, criteria apptypes.MemoryExtractionCriteria) ([]apptypes.MemoryDetails, error) {
	s.extractCriteria = criteria
	return s.extractDetails, s.extractErr
}

func (s *memoryUsecaseStub) ImportCodex(_ context.Context, criteria apptypes.CodexImportCriteria) (apptypes.MemoryImportResult, error) {
	s.importCalls = append(s.importCalls, criteria)
	return s.importResult, s.importErr
}

func (s *memoryUsecaseStub) ImportInstructions(_ context.Context, criteria apptypes.MemoryBridgeImportCriteria) (apptypes.MemoryBridgeImportResult, error) {
	s.bridgeImportCalls = append(s.bridgeImportCalls, criteria)
	return s.bridgeImportResult, s.bridgeImportErr
}

func (s *memoryUsecaseStub) Scan(_ context.Context, _ apptypes.MemoryHygieneScanCriteria) (apptypes.MemoryHygieneScanResult, error) {
	return s.scanResult, s.scanErr
}

func (s *memoryUsecaseStub) Apply(_ context.Context, _ apptypes.MemoryHygieneApplyCriteria) (apptypes.MemoryHygieneApplyResult, error) {
	return s.applyResult, s.applyErr
}

func (s *memoryUsecaseStub) Export(_ context.Context, criteria apptypes.MemoryExportCriteria) (apptypes.MemoryExportResult, error) {
	s.exportCalls = append(s.exportCalls, criteria)
	return s.exportResult, s.exportErr
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
func (s *storeManagementUsecaseStub) CollectGarbage(_ context.Context, _ time.Time, _ apptypes.GarbageCollectionTarget, _ bool) (apptypes.CollectGarbageResult, error) {
	return s.gcResult, s.gcErr
}
func (s *storeManagementUsecaseStub) CloseStaleSessions(_ context.Context, _ time.Duration, _ bool) (apptypes.CloseStaleSessionsResult, error) {
	return s.staleResult, s.staleErr
}
