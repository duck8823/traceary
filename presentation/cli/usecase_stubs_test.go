package cli_test

import (
	"context"
	"strconv"
	"strings"
	"sync"
	"time"

	"golang.org/x/xerrors"

	"github.com/duck8823/traceary/application/queryservice"
	apptypes "github.com/duck8823/traceary/application/types"
	"github.com/duck8823/traceary/application/usecase"
	"github.com/duck8823/traceary/domain/model"
	"github.com/duck8823/traceary/domain/types"
)

// eventUsecaseStub implements usecase.EventUsecase for testing.
type eventLogCall struct {
	id         types.EventID
	message    string
	kind       types.EventKind
	client     types.Client
	agent      types.Agent
	sessionID  types.SessionID
	workspace  types.Workspace
	logCfg     apptypes.LogRedaction
	sourceHook string
}

type eventUsecaseStub struct {
	logEvent            *model.Event
	logErr              error
	auditEvent          *model.Event
	auditAudit          *model.CommandAudit
	auditErr            error
	searchEvents        []*model.Event
	searchErr           error
	listEvents          []*model.Event
	listErr             error
	showDetails         apptypes.EventDetails
	showErr             error
	contextEvents       []*model.Event
	contextErr          error
	timelineBlocks      []apptypes.TimelineBlock
	timelineErr         error
	listCriteria        apptypes.EventListCriteria
	eventSearchCriteria apptypes.EventSearchCriteria
	listCalls           int
	searchCalls         int
	timelineCriteria    apptypes.TimelineCriteria

	// hydrateCalls / lastHydrateFields record HydrateCommandAudits usage so
	// body-rendering paths can assert command-only vs full vs none.
	hydrateCalls      int
	lastHydrateFields queryservice.CommandAuditPayloadFields
	// hydrateCommandByEventID, when set, simulates command-only decode by
	// replacing metadata-only audits with the mapped command line.
	hydrateCommandByEventID map[string]string

	// logMu guards logCall/logCalls against concurrent Log() invocations.
	// Kimi's Stop hook can fire effectively concurrently for the same turn
	// (#1681); tests exercising that race invoke Log() from multiple
	// goroutines and need this stub to record calls safely rather than
	// racing on the slice append.
	logMu               sync.Mutex
	logSeq              int
	logCall             eventLogCall
	logCalls            []eventLogCall
	deleteTranscriptErr error
	deleteTranscriptIDs []types.EventID
	auditCall           struct {
		command       string
		input         string
		output        string
		client        types.Client
		agent         types.Agent
		sessionID     types.SessionID
		workspace     types.Workspace
		exitCode      types.Optional[int]
		failed        bool
		failureReason types.CommandFailureReason
		auditCfg      apptypes.AuditRedaction
	}
}

// projectionSessionSearchStub implements queryservice.ProjectionSessionSearch.
type projectionSessionSearchStub struct {
	hits     []apptypes.SearchSessionHit
	err      error
	ready    *bool
	readyErr error
	calls    int
	criteria []apptypes.EventSearchCriteria
	excludes [][]types.SessionID
}

func (s *projectionSessionSearchStub) SearchSessionPage(
	_ context.Context,
	criteria apptypes.EventSearchCriteria,
	exclude []types.SessionID,
) (apptypes.SearchSessionPage, error) {
	if s == nil {
		return apptypes.SearchSessionPageOf(nil, apptypes.SearchSessionTierNotApplicable), nil
	}
	s.calls++
	s.criteria = append(s.criteria, criteria)
	s.excludes = append(s.excludes, exclude)
	if s.err != nil {
		return apptypes.SearchSessionPage{}, s.err
	}
	if criteria.Kind().String() != "" || strings.TrimSpace(criteria.Query()) == "" || criteria.Offset() > 0 || !criteria.PageAnchor().IsZero() {
		return apptypes.SearchSessionPageOf(nil, apptypes.SearchSessionTierNotApplicable), nil
	}
	if s.readyErr != nil {
		return apptypes.SearchSessionPage{}, s.readyErr
	}
	if s.ready != nil && !*s.ready {
		return apptypes.SearchSessionPageOf(nil, apptypes.SearchSessionTierNotReady), nil
	}
	return apptypes.SearchSessionPageOf(s.hits, apptypes.SearchSessionTierReady), nil
}

func (s *eventUsecaseStub) Log(ctx context.Context, message string, kind types.EventKind, client types.Client, agent types.Agent, sessionID types.SessionID, workspace types.Workspace, logCfg apptypes.LogRedaction) (apptypes.EventWriteResult, error) {
	s.logMu.Lock()
	defer s.logMu.Unlock()
	s.logCall.message = message
	s.logCall.kind = kind
	s.logCall.client = client
	s.logCall.agent = agent
	s.logCall.sessionID = sessionID
	s.logCall.workspace = workspace
	s.logCall.logCfg = logCfg
	s.logCall.sourceHook = apptypes.SourceHookFromContext(ctx)
	s.logSeq++
	id := types.EventID("stub-" + strconv.Itoa(s.logSeq))
	s.logCall.id = id
	s.logCalls = append(s.logCalls, s.logCall)
	if s.logEvent != nil || s.logErr != nil {
		return apptypes.EventWriteResultOf(s.logEvent, true), s.logErr
	}
	event, err := model.NewEvent(id, kind, client, agent, sessionID, workspace, message)
	if err != nil {
		return apptypes.EventWriteResult{}, xerrors.Errorf("failed to build stub log event: %w", err)
	}
	return apptypes.EventWriteResultOf(event, true), nil
}

func (s *eventUsecaseStub) DeleteTranscript(_ context.Context, eventID types.EventID) error {
	s.logMu.Lock()
	defer s.logMu.Unlock()
	s.deleteTranscriptIDs = append(s.deleteTranscriptIDs, eventID)
	if s.deleteTranscriptErr != nil {
		return s.deleteTranscriptErr
	}
	kept := s.logCalls[:0]
	for _, call := range s.logCalls {
		if call.id == eventID {
			continue
		}
		kept = append(kept, call)
	}
	s.logCalls = kept
	return nil
}
func (s *eventUsecaseStub) Audit(_ context.Context, in apptypes.AuditInput, auditCfg apptypes.AuditRedaction) (apptypes.EventWriteResult, *model.CommandAudit, error) {
	s.auditCall.command = in.Command
	s.auditCall.input = in.Input
	s.auditCall.output = in.Output
	s.auditCall.client = in.Client
	s.auditCall.agent = in.Agent
	s.auditCall.sessionID = in.SessionID
	s.auditCall.workspace = in.Workspace
	s.auditCall.exitCode = in.ExitCode
	s.auditCall.failed = in.Failed
	s.auditCall.failureReason = in.FailureReason
	s.auditCall.auditCfg = auditCfg
	return apptypes.EventWriteResultOf(s.auditEvent, true), s.auditAudit, s.auditErr
}
func (s *eventUsecaseStub) Search(_ context.Context, criteria apptypes.EventSearchCriteria) ([]*model.Event, error) {
	s.searchCalls++
	s.eventSearchCriteria = criteria
	return s.searchEvents, s.searchErr
}
func (s *eventUsecaseStub) List(_ context.Context, criteria apptypes.EventListCriteria) ([]*model.Event, error) {
	s.listCalls++
	s.listCriteria = criteria
	return s.listEvents, s.listErr
}

type eventMetadataUsecaseStub struct {
	timestampKinds        []apptypes.EventTimestampKind
	listMetadata          []apptypes.EventMetadata
	searchMetadata        []apptypes.EventMetadata
	contextMetadata       []apptypes.EventMetadata
	listErr               error
	searchErr             error
	contextErr            error
	listCalls             int
	listCriteria          apptypes.EventListCriteria
	timestampKindCriteria apptypes.EventListCriteria
	timestampKindCalls    int
	searchCalls           int
	contextCalls          int
}

func (s *eventMetadataUsecaseStub) ListTimestampKinds(_ context.Context, criteria apptypes.EventListCriteria) ([]apptypes.EventTimestampKind, error) {
	s.timestampKindCalls++
	s.timestampKindCriteria = criteria
	return s.timestampKinds, nil
}

func (s *eventMetadataUsecaseStub) List(_ context.Context, criteria apptypes.EventListCriteria) ([]apptypes.EventMetadata, error) {
	s.listCalls++
	s.listCriteria = criteria
	return s.listMetadata, s.listErr
}

func (s *eventMetadataUsecaseStub) Search(_ context.Context, _ apptypes.EventSearchCriteria) ([]apptypes.EventMetadata, error) {
	s.searchCalls++
	return s.searchMetadata, s.searchErr
}

func (s *eventMetadataUsecaseStub) Context(_ context.Context, _ apptypes.EventContextCriteria) ([]apptypes.EventMetadata, error) {
	s.contextCalls++
	return s.contextMetadata, s.contextErr
}

func (s *eventUsecaseStub) ListWindow(_ context.Context, criteria apptypes.EventListCriteria) ([]*model.Event, error) {
	s.listCriteria = criteria
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
func (s *eventUsecaseStub) HydrateCommandAudits(_ context.Context, events []*model.Event, fields queryservice.CommandAuditPayloadFields) error {
	s.hydrateCalls++
	s.lastHydrateFields = fields
	if !fields.Command || len(s.hydrateCommandByEventID) == 0 {
		return nil
	}
	for _, event := range events {
		if event == nil {
			continue
		}
		command, ok := s.hydrateCommandByEventID[event.EventID().String()]
		if !ok || strings.TrimSpace(command) == "" {
			continue
		}
		audit, ok := event.CommandAudit().Value()
		if !ok || audit == nil {
			continue
		}
		restored, err := model.CommandAuditFromSnapshot(model.CommandAuditSnapshot{
			EventID:             audit.EventID(),
			Command:             command,
			Wrapper:             audit.CommandIdentity().Wrapper(),
			CommandName:         audit.CommandIdentity().Command(),
			Input:               audit.Input(),
			Output:              audit.Output(),
			InputTruncated:      audit.InputTruncated(),
			OutputTruncated:     audit.OutputTruncated(),
			InputOriginalBytes:  audit.InputOriginalBytes(),
			OutputOriginalBytes: audit.OutputOriginalBytes(),
			ExitCode:            audit.ExitCode(),
			Failed:              audit.Failed(),
			FailureReason:       audit.FailureReason(),
		})
		if err != nil {
			return xerrors.Errorf("stub hydrate command audit: %w", err)
		}
		event.AttachCommandAudit(restored)
	}
	return nil
}

// sessionUsecaseStub implements usecase.SessionUsecase for testing.
type sessionUsecaseStub struct {
	startEvent      *model.Event
	startErr        error
	endEvent        *model.Event
	endErr          error
	labelErr        error
	listResult      []apptypes.SessionSummary
	listErr         error
	listCriteria    apptypes.SessionListCriteria
	treeResult      []apptypes.SessionSummary
	treeErr         error
	lineageResult   []apptypes.SessionSummary
	lineageErr      error
	activeEvent     *model.Event
	activeErr       error
	activeCriteria  apptypes.SessionLookupCriteria
	latestEvent     *model.Event
	latestErr       error
	latestCriteria  apptypes.SessionLookupCriteria
	handoff         types.Optional[apptypes.HandoffSummary]
	handoffErr      error
	setModelCalls   map[types.SessionID]string
	endedSessionIDs map[types.SessionID]struct{}

	startCall struct {
		client          types.Client
		agent           types.Agent
		sessionID       types.SessionID
		workspace       types.Workspace
		parentSessionID types.SessionID
		runtimeMode     types.RuntimeMode
	}
	finalizeReason     types.TerminalReason
	finalizeSessionID  types.SessionID
	finalizeContextErr error
	finalizeTransition model.SessionTerminalTransition
	finalizeEvent      *model.Event
	finalizeErr        error
	startChildCall     struct {
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
func (s *sessionUsecaseStub) StartWithRuntimeMode(_ context.Context, client types.Client, agent types.Agent, sessionID types.SessionID, workspace types.Workspace, parentSessionID types.SessionID, runtimeMode types.RuntimeMode) (*model.Event, error) {
	s.startCall.client = client
	s.startCall.agent = agent
	s.startCall.sessionID = sessionID
	s.startCall.workspace = workspace
	s.startCall.parentSessionID = parentSessionID
	s.startCall.runtimeMode = runtimeMode
	return s.startEvent, s.startErr
}
func (s *sessionUsecaseStub) FinalizeOneShot(ctx context.Context, _ types.Client, _ types.Agent, sessionID types.SessionID, _ types.Workspace, reason types.TerminalReason, _ string) (model.SessionTerminalTransition, *model.Event, error) {
	s.finalizeSessionID = sessionID
	s.finalizeReason = reason
	s.finalizeContextErr = ctx.Err()
	return s.finalizeTransition, s.finalizeEvent, s.finalizeErr
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
func (s *sessionUsecaseStub) FindEndedSessionIDs(_ context.Context, _ []types.SessionID) (map[types.SessionID]struct{}, error) {
	if s.endedSessionIDs != nil {
		return s.endedSessionIDs, nil
	}
	return map[types.SessionID]struct{}{}, nil
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

func (s *sessionUsecaseStub) SetModelIfEmpty(_ context.Context, sessionID types.SessionID, modelName string) (bool, error) {
	if s.setModelCalls == nil {
		s.setModelCalls = make(map[types.SessionID]string)
	}
	if strings.TrimSpace(modelName) == "" {
		return false, nil
	}
	s.setModelCalls[sessionID] = modelName
	return true, nil
}

type contextUsecaseStub struct {
	handoff      types.Optional[apptypes.ContextPack]
	handoffErr   error
	handoffCalls []apptypes.ContextPackCriteria
	// handoffFn, when set, overrides the static handoff/handoffErr fields
	// so a test can vary the response across calls (e.g. the handoff
	// re-query that distinguishes stale-skip from missing-session).
	handoffFn func(apptypes.ContextPackCriteria) (types.Optional[apptypes.ContextPack], error)
}

func (s *contextUsecaseStub) Handoff(_ context.Context, criteria apptypes.ContextPackCriteria) (types.Optional[apptypes.ContextPack], error) {
	s.handoffCalls = append(s.handoffCalls, criteria)
	if s.handoffFn != nil {
		return s.handoffFn(criteria)
	}
	return s.handoff, s.handoffErr
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
	listResult          []apptypes.MemorySummary
	listErr             error
	listFunc            func(context.Context, apptypes.MemoryListCriteria) ([]apptypes.MemorySummary, error)
	staleResult         apptypes.StaleMemoryListResult
	staleErr            error
	searchResult        []apptypes.MemorySummary
	searchErr           error
	showDetails         apptypes.MemoryDetails
	showDetailsByID     map[types.MemoryID]apptypes.MemoryDetails
	showErr             error
	rememberDetails     apptypes.MemoryDetails
	rememberErr         error
	proposeDetails      apptypes.MemoryDetails
	proposeErr          error
	acceptDetails       apptypes.MemoryDetails
	acceptErr           error
	distillResult       apptypes.MemoryDistillResult
	distillErr          error
	rejectDetails       apptypes.MemoryDetails
	rejectDetailsByID   map[types.MemoryID]apptypes.MemoryDetails
	rejectErr           error
	rejectErrByID       map[types.MemoryID]error
	attachDetails       apptypes.MemoryDetails
	attachErr           error
	supersedeDetails    apptypes.MemoryDetails
	supersedeErr        error
	expireDetails       apptypes.MemoryDetails
	expireErr           error
	setValidityDetails  apptypes.MemoryDetails
	setValidityErr      error
	extractDetails      []apptypes.MemoryDetails
	extractErr          error
	extractFunc         func(context.Context, apptypes.MemoryExtractionCriteria) ([]apptypes.MemoryDetails, error)
	extractCallCount    int
	importResult        apptypes.MemoryImportResult
	importErr           error
	bridgeImportResult  apptypes.MemoryBridgeImportResult
	bridgeImportErr     error
	scanResult          apptypes.MemoryHygieneScanResult
	scanErr             error
	applyResult         apptypes.MemoryHygieneApplyResult
	applyErr            error
	exportResult        apptypes.MemoryExportResult
	exportErr           error
	activationPlan      apptypes.MemoryActivationPlan
	activationPlanErr   error
	activationResult    apptypes.MemoryActivationApplyResult
	activationErr       error
	activationStatus    apptypes.MemoryActivationStatusResult
	activationStatusErr error

	rememberCall struct {
		memoryType   types.MemoryType
		scope        types.MemoryScope
		fact         string
		confidence   types.Optional[types.Confidence]
		source       types.MemorySource
		evidenceRefs []types.EvidenceRef
		artifactRefs []types.ArtifactRef
	}
	listCriteria          apptypes.MemoryListCriteria
	sourceCounts          apptypes.MemorySourceCounts
	sourceCountsSet       bool
	sourceCountErr        error
	countBySourceCriteria apptypes.MemoryListCriteria
	staleCriteria         apptypes.StaleMemoryListCriteria
	staleCalls            int
	searchCriteria        apptypes.MemorySearchCriteria
	showMemoryID          types.MemoryID
	acceptCall            struct {
		memoryID   types.MemoryID
		confidence types.Optional[types.Confidence]
	}
	acceptCallCount int
	distillCalls    []apptypes.MemoryDistillCriteria
	rejectCall      struct {
		memoryID types.MemoryID
	}
	rejectCallCount int
	attachCall      struct {
		memoryID     types.MemoryID
		evidenceRefs []types.EvidenceRef
		artifactRefs []types.ArtifactRef
	}
	attachCallCount int

	setValidityCall struct {
		memoryID  types.MemoryID
		validFrom types.Optional[time.Time]
		validTo   types.Optional[time.Time]
		clearTo   bool
	}
	setValidityCallCount int
	expireCall           struct {
		memoryID  types.MemoryID
		expiresAt types.Optional[time.Time]
	}
	expireCallCount       int
	extractCriteria       apptypes.MemoryExtractionCriteria
	importCalls           []apptypes.CodexImportCriteria
	bridgeImportCalls     []apptypes.MemoryBridgeImportCriteria
	scanCriteria          apptypes.MemoryHygieneScanCriteria
	exportCalls           []apptypes.MemoryExportCriteria
	activationPlanCalls   []apptypes.MemoryActivationCriteria
	activationCalls       []apptypes.MemoryActivationCriteria
	activationStatusCalls []apptypes.MemoryActivationCriteria
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

func (s *memoryUsecaseStub) Distill(_ context.Context, criteria apptypes.MemoryDistillCriteria) (apptypes.MemoryDistillResult, error) {
	s.distillCalls = append(s.distillCalls, criteria)
	return s.distillResult, s.distillErr
}

func (s *memoryUsecaseStub) Reject(_ context.Context, memoryID types.MemoryID) (apptypes.MemoryDetails, error) {
	s.rejectCall.memoryID = memoryID
	s.rejectCallCount++
	if err, ok := s.rejectErrByID[memoryID]; ok {
		return apptypes.MemoryDetails{}, err
	}
	if details, ok := s.rejectDetailsByID[memoryID]; ok {
		return details, nil
	}
	return s.rejectDetails, s.rejectErr
}

func (s *memoryUsecaseStub) AttachCandidateRefs(_ context.Context, memoryID types.MemoryID, evidenceRefs []types.EvidenceRef, artifactRefs []types.ArtifactRef) (apptypes.MemoryDetails, error) {
	s.attachCall.memoryID = memoryID
	s.attachCall.evidenceRefs = append([]types.EvidenceRef(nil), evidenceRefs...)
	s.attachCall.artifactRefs = append([]types.ArtifactRef(nil), artifactRefs...)
	s.attachCallCount++
	return s.attachDetails, s.attachErr
}

func (s *memoryUsecaseStub) Supersede(_ context.Context, _ types.MemoryID, _ types.MemoryType, _ types.MemoryScope, _ string, _ types.Optional[types.Confidence], _ types.MemorySource, _ []types.EvidenceRef, _ []types.ArtifactRef, _ types.Optional[time.Time], _ types.Optional[time.Time]) (apptypes.MemoryDetails, error) {
	return s.supersedeDetails, s.supersedeErr
}

func (s *memoryUsecaseStub) Expire(_ context.Context, memoryID types.MemoryID, expiresAt types.Optional[time.Time]) (apptypes.MemoryDetails, error) {
	s.expireCall.memoryID = memoryID
	s.expireCall.expiresAt = expiresAt
	s.expireCallCount++
	return s.expireDetails, s.expireErr
}
func (s *memoryUsecaseStub) Decay(_ context.Context, _ apptypes.MemoryDecayCriteria) (apptypes.MemoryDecayResult, error) {
	return apptypes.MemoryDecayResult{}, nil
}
func (s *memoryUsecaseStub) Restore(_ context.Context, _ types.MemoryID) (apptypes.MemoryDetails, error) {
	return apptypes.MemoryDetails{}, nil
}

func (s *memoryUsecaseStub) SetValidity(_ context.Context, memoryID types.MemoryID, validFrom types.Optional[time.Time], validTo types.Optional[time.Time], clearTo bool) (apptypes.MemoryDetails, error) {
	s.setValidityCall.memoryID = memoryID
	s.setValidityCall.validFrom = validFrom
	s.setValidityCall.validTo = validTo
	s.setValidityCall.clearTo = clearTo
	s.setValidityCallCount++
	return s.setValidityDetails, s.setValidityErr
}

func (s *memoryUsecaseStub) List(ctx context.Context, criteria apptypes.MemoryListCriteria) ([]apptypes.MemorySummary, error) {
	s.listCriteria = criteria
	if s.listFunc != nil {
		return s.listFunc(ctx, criteria)
	}
	return s.listResult, s.listErr
}

func (s *memoryUsecaseStub) CountBySource(_ context.Context, criteria apptypes.MemoryListCriteria) (apptypes.MemorySourceCounts, error) {
	s.countBySourceCriteria = criteria
	if s.sourceCountErr != nil {
		return apptypes.MemorySourceCounts{}, s.sourceCountErr
	}
	if s.sourceCountsSet {
		return s.sourceCounts, nil
	}
	bySource := make(map[types.MemorySource]int)
	for _, item := range s.listResult {
		bySource[item.Source()]++
	}
	return apptypes.MemorySourceCountsFrom(bySource), nil
}

func (s *memoryUsecaseStub) ListStale(_ context.Context, criteria apptypes.StaleMemoryListCriteria) (apptypes.StaleMemoryListResult, error) {
	s.staleCriteria = criteria
	s.staleCalls++
	return s.staleResult, s.staleErr
}

func (s *memoryUsecaseStub) Search(_ context.Context, criteria apptypes.MemorySearchCriteria) ([]apptypes.MemorySummary, error) {
	s.searchCriteria = criteria
	return s.searchResult, s.searchErr
}

func (s *memoryUsecaseStub) Show(_ context.Context, memoryID types.MemoryID) (apptypes.MemoryDetails, error) {
	s.showMemoryID = memoryID
	if details, ok := s.showDetailsByID[memoryID]; ok {
		return details, s.showErr
	}
	return s.showDetails, s.showErr
}

func (s *memoryUsecaseStub) Extract(ctx context.Context, criteria apptypes.MemoryExtractionCriteria) ([]apptypes.MemoryDetails, error) {
	s.extractCriteria = criteria
	s.extractCallCount++
	if s.extractFunc != nil {
		return s.extractFunc(ctx, criteria)
	}
	return s.extractDetails, s.extractErr
}

func (s *memoryUsecaseStub) ExplainExtraction(context.Context, apptypes.MemoryExtractionCriteria) (apptypes.MemoryExtractionDebugReport, error) {
	return apptypes.MemoryExtractionDebugReport{}, nil
}

func (s *memoryUsecaseStub) ImportCodex(_ context.Context, criteria apptypes.CodexImportCriteria) (apptypes.MemoryImportResult, error) {
	s.importCalls = append(s.importCalls, criteria)
	return s.importResult, s.importErr
}

func (s *memoryUsecaseStub) ImportInstructions(_ context.Context, criteria apptypes.MemoryBridgeImportCriteria) (apptypes.MemoryBridgeImportResult, error) {
	s.bridgeImportCalls = append(s.bridgeImportCalls, criteria)
	return s.bridgeImportResult, s.bridgeImportErr
}

func (s *memoryUsecaseStub) Scan(_ context.Context, criteria apptypes.MemoryHygieneScanCriteria) (apptypes.MemoryHygieneScanResult, error) {
	s.scanCriteria = criteria
	return s.scanResult, s.scanErr
}

func (s *memoryUsecaseStub) Apply(_ context.Context, _ apptypes.MemoryHygieneApplyCriteria) (apptypes.MemoryHygieneApplyResult, error) {
	return s.applyResult, s.applyErr
}

func (s *memoryUsecaseStub) Export(_ context.Context, criteria apptypes.MemoryExportCriteria) (apptypes.MemoryExportResult, error) {
	s.exportCalls = append(s.exportCalls, criteria)
	return s.exportResult, s.exportErr
}

func (s *memoryUsecaseStub) ActivatePlan(_ context.Context, criteria apptypes.MemoryActivationCriteria) (apptypes.MemoryActivationPlan, error) {
	s.activationPlanCalls = append(s.activationPlanCalls, criteria)
	return s.activationPlan, s.activationPlanErr
}

func (s *memoryUsecaseStub) Activate(_ context.Context, criteria apptypes.MemoryActivationCriteria) (apptypes.MemoryActivationApplyResult, error) {
	s.activationCalls = append(s.activationCalls, criteria)
	return s.activationResult, s.activationErr
}

func (s *memoryUsecaseStub) ActivationStatus(_ context.Context, criteria apptypes.MemoryActivationCriteria) (apptypes.MemoryActivationStatusResult, error) {
	s.activationStatusCalls = append(s.activationStatusCalls, criteria)
	return s.activationStatus, s.activationStatusErr
}

// storeManagementUsecaseStub implements usecase.StoreManagementUsecase for testing.
type storeManagementUsecaseStub struct {
	staleMu           sync.Mutex
	staleDelay        time.Duration
	initCalled        bool
	authorizedCalled  bool
	initErr           error
	previewOffline    []int64
	previewCalls      int
	createBackupErr   error
	createBackupPath  string
	createBackupCalls int
	operations        []string
	restoreErr        error
	gcResult          apptypes.CollectGarbageResult
	gcErr             error
	gcCalled          bool
	callLog           *[]string
	dedupeResult      apptypes.ContentEventDedupeResult
	dedupeErr         error
	dedupeParams      []apptypes.ContentEventDedupeParams
	restoreResult     apptypes.ContentEventDedupeRestoreResult
	restoreRunErr     error
	restoreRunIDs     []string
	purgeResult       apptypes.ContentEventDedupePurgeResult
	purgeErr          error
	purgeRunIDs       []string
	dedupeRuns        []apptypes.ContentEventDedupeRun
	dedupeRunsErr     error
	dedupeRunsCalls   int
	staleResult       apptypes.CloseStaleSessionsResult
	staleErr          error
	staleCalls        []struct {
		staleAfter          time.Duration
		dryRun              bool
		protectedSessionIDs []types.SessionID
	}
	archiveCreateParams apptypes.StoreArchiveCreateParams
	archiveCreateResult apptypes.StoreArchiveResult
	archiveVerifyPath   string
	archiveRestorePath  string
	archiveRestoreDry   bool
}

func (s *storeManagementUsecaseStub) Initialize(_ context.Context) error {
	s.staleMu.Lock()
	defer s.staleMu.Unlock()
	s.initCalled = true
	s.operations = append(s.operations, "initialize")
	return s.initErr
}
func (s *storeManagementUsecaseStub) InitializeAuthorized(ctx context.Context) error {
	s.staleMu.Lock()
	s.authorizedCalled = true
	s.staleMu.Unlock()
	return s.Initialize(ctx)
}
func (s *storeManagementUsecaseStub) PreviewOfflineMigrations(context.Context) ([]int64, error) {
	s.staleMu.Lock()
	s.previewCalls++
	pending := s.previewOffline
	s.staleMu.Unlock()
	return pending, nil
}
func (s *storeManagementUsecaseStub) CreateBackup(_ context.Context, path string, _ bool) error {
	s.createBackupCalls++
	s.createBackupPath = path
	s.operations = append(s.operations, "backup")
	return s.createBackupErr
}
func (s *storeManagementUsecaseStub) RestoreBackup(_ context.Context, _ string, _ bool) error {
	return s.restoreErr
}
func (s *storeManagementUsecaseStub) CollectGarbage(_ context.Context, _ time.Time, _ apptypes.GarbageCollectionTarget, _ bool) (apptypes.CollectGarbageResult, error) {
	s.gcCalled = true
	if s.callLog != nil {
		*s.callLog = append(*s.callLog, "gc")
	}
	return s.gcResult, s.gcErr
}
func (s *storeManagementUsecaseStub) DedupeContentEvents(_ context.Context, params apptypes.ContentEventDedupeParams) (apptypes.ContentEventDedupeResult, error) {
	s.dedupeParams = append(s.dedupeParams, params)
	return s.dedupeResult, s.dedupeErr
}
func (s *storeManagementUsecaseStub) RestoreContentEventDedupeRun(_ context.Context, runID string) (apptypes.ContentEventDedupeRestoreResult, error) {
	s.restoreRunIDs = append(s.restoreRunIDs, runID)
	return s.restoreResult, s.restoreRunErr
}
func (s *storeManagementUsecaseStub) CreateStoreArchive(_ context.Context, params apptypes.StoreArchiveCreateParams) (apptypes.StoreArchiveResult, error) {
	s.archiveCreateParams = params
	if s.archiveCreateResult.Path == "" && params.OutputPath != "" {
		return apptypes.StoreArchiveResult{Path: params.OutputPath, TotalRows: s.archiveCreateResult.TotalRows}, nil
	}
	return s.archiveCreateResult, nil
}
func (s *storeManagementUsecaseStub) VerifyStoreArchive(_ context.Context, path string, _ []byte) error {
	s.archiveVerifyPath = path
	return nil
}
func (s *storeManagementUsecaseStub) RestoreStoreArchive(_ context.Context, path string, _ []byte, dryRun bool) (apptypes.StoreArchiveRestoreResult, error) {
	s.archiveRestorePath = path
	s.archiveRestoreDry = dryRun
	return apptypes.StoreArchiveRestoreResult{DryRun: dryRun}, nil
}
func (s *storeManagementUsecaseStub) CloseStaleSessions(_ context.Context, staleAfter time.Duration, dryRun bool, protectedSessionIDs []types.SessionID) (apptypes.CloseStaleSessionsResult, error) {
	s.staleMu.Lock()
	s.staleCalls = append(s.staleCalls, struct {
		staleAfter          time.Duration
		dryRun              bool
		protectedSessionIDs []types.SessionID
	}{staleAfter: staleAfter, dryRun: dryRun, protectedSessionIDs: append([]types.SessionID(nil), protectedSessionIDs...)})
	s.staleMu.Unlock()
	if s.staleDelay > 0 {
		time.Sleep(s.staleDelay)
	}
	return s.staleResult, s.staleErr
}

func (s *storeManagementUsecaseStub) PurgeContentEventDedupeRun(_ context.Context, runID string) (apptypes.ContentEventDedupePurgeResult, error) {
	s.purgeRunIDs = append(s.purgeRunIDs, runID)
	return s.purgeResult, s.purgeErr
}

func (s *storeManagementUsecaseStub) ListContentEventDedupeRuns(_ context.Context) ([]apptypes.ContentEventDedupeRun, error) {
	s.dedupeRunsCalls++
	return s.dedupeRuns, s.dedupeRunsErr
}
