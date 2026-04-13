package mcpserver

import (
	"context"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"golang.org/x/xerrors"

	apptypes "github.com/duck8823/traceary/application/types"
	"github.com/duck8823/traceary/application/usecase"
	"github.com/duck8823/traceary/domain/model"
	"github.com/duck8823/traceary/domain/types"
)

const (
	defaultClientValue      = "mcp"
	defaultAgentValue       = "manual"
	defaultSessionValue     = "default"
	defaultContextLimit     = 20
	defaultSearchLimit      = 20
	defaultServerName       = "traceary"
	defaultServerVersion    = "dev"
	defaultActiveStaleAfter = 24 * time.Hour
)

// Server provides the Traceary MCP server.
type Server struct {
	serverName          string
	serverVersion       string
	extraRedactPatterns []string
	event               usecase.EventUsecase
	session             usecase.SessionUsecase
	memory              usecase.MemoryUsecase
	context             usecase.ContextUsecase
	storeManagement     usecase.StoreManagementUsecase
}

// NewServer creates a new MCP server.
func NewServer(
	serverVersion string,
	extraRedactPatterns []string,
	event usecase.EventUsecase,
	session usecase.SessionUsecase,
	memory usecase.MemoryUsecase,
	contextUsecase usecase.ContextUsecase,
	storeManagement usecase.StoreManagementUsecase,
) (*Server, error) {
	if event == nil {
		return nil, xerrors.Errorf("event usecase is not configured")
	}
	if session == nil {
		return nil, xerrors.Errorf("session usecase is not configured")
	}
	if memory == nil {
		return nil, xerrors.Errorf("memory usecase is not configured")
	}
	if contextUsecase == nil {
		return nil, xerrors.Errorf("context usecase is not configured")
	}
	if storeManagement == nil {
		return nil, xerrors.Errorf("store management usecase is not configured")
	}

	trimmedVersion := strings.TrimSpace(serverVersion)
	if trimmedVersion == "" {
		trimmedVersion = defaultServerVersion
	}

	return &Server{
		serverName:          defaultServerName,
		serverVersion:       trimmedVersion,
		extraRedactPatterns: extraRedactPatterns,
		event:               event,
		session:             session,
		memory:              memory,
		context:             contextUsecase,
		storeManagement:     storeManagement,
	}, nil
}

// Build creates an MCP server backed by an initialized store. The DB
// path has already been resolved and applied to the shared
// sqlite.Database by the CLI before Build is invoked (see
// cli.RootCLI.applyDatabasePath), so this method does not need a
// separate dbPath argument.
func (s *Server) Build(ctx context.Context) (*mcp.Server, error) {
	if err := s.storeManagement.Initialize(ctx); err != nil {
		return nil, xerrors.Errorf("failed to initialize store: %w", err)
	}

	server := mcp.NewServer(&mcp.Implementation{
		Name:    s.serverName,
		Version: s.serverVersion,
	}, nil)

	mcp.AddTool(server, &mcp.Tool{
		Name:        "add_log",
		Description: "Add a log event to Traceary",
		Annotations: &mcp.ToolAnnotations{DestructiveHint: boolPtr(false)},
	}, s.addLog())
	mcp.AddTool(server, &mcp.Tool{
		Name:        "start_session",
		Description: "Add a session_started event to Traceary",
		Annotations: &mcp.ToolAnnotations{DestructiveHint: boolPtr(false)},
	}, s.startSession())
	mcp.AddTool(server, &mcp.Tool{
		Name:        "end_session",
		Description: "Add a session_ended event to Traceary",
		Annotations: &mcp.ToolAnnotations{DestructiveHint: boolPtr(false)},
	}, s.endSession())
	mcp.AddTool(server, &mcp.Tool{
		Name:        "latest_session",
		Description: "Return the latest session matching the filters",
		Annotations: &mcp.ToolAnnotations{ReadOnlyHint: true},
	}, s.latestSession())
	mcp.AddTool(server, &mcp.Tool{
		Name:        "active_session",
		Description: "Return the active session matching the filters",
		Annotations: &mcp.ToolAnnotations{ReadOnlyHint: true},
	}, s.activeSession())
	mcp.AddTool(server, &mcp.Tool{
		Name:        "list_events",
		Description: "List recent events in Traceary",
		Annotations: &mcp.ToolAnnotations{ReadOnlyHint: true},
	}, s.listEvents())
	mcp.AddTool(server, &mcp.Tool{
		Name:        "add_audit",
		Description: "Add a command audit event to Traceary",
		Annotations: &mcp.ToolAnnotations{DestructiveHint: boolPtr(false)},
	}, s.addAudit())
	mcp.AddTool(server, &mcp.Tool{
		Name:        "search",
		Description: "Search events in Traceary",
		Annotations: &mcp.ToolAnnotations{ReadOnlyHint: true},
	}, s.search())
	mcp.AddTool(server, &mcp.Tool{
		Name:        "get_context",
		Description: "Get recent context events matching the filters",
		Annotations: &mcp.ToolAnnotations{ReadOnlyHint: true},
	}, s.getContext())
	mcp.AddTool(server, &mcp.Tool{
		Name:        "session_handoff",
		Description: "Get a concise session summary for handoff or context resumption",
		Annotations: &mcp.ToolAnnotations{ReadOnlyHint: true},
	}, s.sessionHandoff())
	mcp.AddTool(server, &mcp.Tool{
		Name:        "retrieve_memories",
		Description: "Retrieve durable memories by ID, query, or scope filters",
		Annotations: &mcp.ToolAnnotations{ReadOnlyHint: true},
	}, s.retrieveMemories())
	mcp.AddTool(server, &mcp.Tool{
		Name:        "remember_memory",
		Description: "Record an accepted durable memory",
		Annotations: &mcp.ToolAnnotations{DestructiveHint: boolPtr(false)},
	}, s.rememberMemory())
	mcp.AddTool(server, &mcp.Tool{
		Name:        "propose_memory",
		Description: "Record a candidate durable memory",
		Annotations: &mcp.ToolAnnotations{DestructiveHint: boolPtr(false)},
	}, s.proposeMemory())
	mcp.AddTool(server, &mcp.Tool{
		Name:        "accept_memory",
		Description: "Accept a candidate durable memory",
		Annotations: &mcp.ToolAnnotations{DestructiveHint: boolPtr(false)},
	}, s.acceptMemory())
	mcp.AddTool(server, &mcp.Tool{
		Name:        "reject_memory",
		Description: "Reject a candidate durable memory",
		Annotations: &mcp.ToolAnnotations{DestructiveHint: boolPtr(false)},
	}, s.rejectMemory())
	mcp.AddTool(server, &mcp.Tool{
		Name:        "supersede_memory",
		Description: "Replace an accepted durable memory with a new accepted memory",
		Annotations: &mcp.ToolAnnotations{DestructiveHint: boolPtr(false)},
	}, s.supersedeMemory())
	mcp.AddTool(server, &mcp.Tool{
		Name:        "expire_memory",
		Description: "Expire a durable memory",
		Annotations: &mcp.ToolAnnotations{DestructiveHint: boolPtr(false)},
	}, s.expireMemory())
	mcp.AddTool(server, &mcp.Tool{
		Name:        "memory_pack",
		Description: "Build a memory-aware context pack for prompt-context enrichment or automation",
		Annotations: &mcp.ToolAnnotations{ReadOnlyHint: true},
	}, s.memoryPack())

	return server, nil
}

// Run starts the MCP server over stdio transport.
func (s *Server) Run(ctx context.Context) error {
	server, err := s.Build(ctx)
	if err != nil {
		return xerrors.Errorf("failed to build MCP server: %w", err)
	}
	if err := server.Run(ctx, &mcp.StdioTransport{}); err != nil {
		return xerrors.Errorf("failed to run MCP server: %w", err)
	}

	return nil
}

func (s *Server) addLog() mcp.ToolHandlerFor[addLogInput, addLogOutput] {
	return func(ctx context.Context, _ *mcp.CallToolRequest, input addLogInput) (*mcp.CallToolResult, addLogOutput, error) {
		event, err := s.event.Log(ctx,
			input.Message,
			types.EventKind(strings.TrimSpace(input.Kind)),
			types.Client(resolveValue(input.Client, defaultClientValue)),
			types.Agent(resolveValue(input.Agent, defaultAgentValue)),
			types.SessionID(resolveValue(input.SessionID, defaultSessionValue)),
			types.Workspace(strings.TrimSpace(input.Workspace)),
		)
		if err != nil {
			return nil, addLogOutput{}, xerrors.Errorf("failed to record log: %w", err)
		}

		return nil, addLogOutput{
			EventID:   event.EventID().String(),
			Kind:      event.Kind().String(),
			Client:    event.Client().String(),
			Agent:     event.Agent().String(),
			SessionID: event.SessionID().String(),
			Workspace: event.Workspace().String(),
			Body:      event.Body(),
			CreatedAt: event.CreatedAt().UTC().Format(time.RFC3339Nano),
		}, nil
	}
}

func (s *Server) startSession() mcp.ToolHandlerFor[startSessionInput, sessionEventOutput] {
	return func(ctx context.Context, _ *mcp.CallToolRequest, input startSessionInput) (*mcp.CallToolResult, sessionEventOutput, error) {
		event, err := s.session.Start(ctx,
			types.Client(resolveValue(input.Client, defaultClientValue)),
			types.Agent(resolveValue(input.Agent, defaultAgentValue)),
			types.SessionID(strings.TrimSpace(input.SessionID)),
			types.Workspace(strings.TrimSpace(input.Workspace)),
			types.SessionID(""), // no parent session
		)
		if err != nil {
			return nil, sessionEventOutput{}, xerrors.Errorf("failed to record session start: %w", err)
		}

		return nil, newSessionEventOutput(event), nil
	}
}

func (s *Server) endSession() mcp.ToolHandlerFor[endSessionInput, sessionEventOutput] {
	return func(ctx context.Context, _ *mcp.CallToolRequest, input endSessionInput) (*mcp.CallToolResult, sessionEventOutput, error) {
		sessionID := strings.TrimSpace(input.SessionID)
		if sessionID == "" {
			return nil, sessionEventOutput{}, xerrors.Errorf("session_id is required")
		}

		event, err := s.session.End(ctx,
			types.Client(strings.TrimSpace(input.Client)),
			types.Agent(strings.TrimSpace(input.Agent)),
			types.SessionID(sessionID),
			types.Workspace(strings.TrimSpace(input.Workspace)),
			"", // no summary from MCP
		)
		if err != nil {
			return nil, sessionEventOutput{}, xerrors.Errorf("failed to record session end: %w", err)
		}

		return nil, newSessionEventOutput(event), nil
	}
}

func (s *Server) latestSession() mcp.ToolHandlerFor[sessionLookupInput, sessionEventOutput] {
	return func(ctx context.Context, _ *mcp.CallToolRequest, input sessionLookupInput) (*mcp.CallToolResult, sessionEventOutput, error) {
		criteria := apptypes.NewSessionLookupCriteriaBuilder().
			Client(types.Client(strings.TrimSpace(input.Client))).
			Agent(types.Agent(strings.TrimSpace(input.Agent))).
			Workspace(types.Workspace(strings.TrimSpace(input.Workspace))).
			Build()
		result, err := s.session.Latest(ctx, criteria)
		if err != nil {
			return nil, sessionEventOutput{}, xerrors.Errorf("failed to get latest session: %w", err)
		}
		if !result.IsPresent() {
			return nil, sessionEventOutput{}, xerrors.Errorf("no matching session found")
		}
		latestEvent, _ := result.Get()

		return nil, newSessionEventOutput(latestEvent), nil
	}
}

func (s *Server) activeSession() mcp.ToolHandlerFor[sessionLookupInput, sessionEventOutput] {
	return func(ctx context.Context, _ *mcp.CallToolRequest, input sessionLookupInput) (*mcp.CallToolResult, sessionEventOutput, error) {
		criteria := apptypes.NewSessionLookupCriteriaBuilder().
			Client(types.Client(strings.TrimSpace(input.Client))).
			Agent(types.Agent(strings.TrimSpace(input.Agent))).
			Workspace(types.Workspace(strings.TrimSpace(input.Workspace))).
			Build()
		result, err := s.session.Active(ctx, criteria)
		if err != nil {
			return nil, sessionEventOutput{}, xerrors.Errorf("failed to get active session: %w", err)
		}
		if !result.IsPresent() {
			return nil, sessionEventOutput{}, xerrors.Errorf("no matching active session found")
		}
		activeEvent, _ := result.Get()
		if err := validateActiveSession(activeEvent, input); err != nil {
			return nil, sessionEventOutput{}, err
		}

		return nil, newSessionEventOutput(activeEvent), nil
	}
}

func (s *Server) addAudit() mcp.ToolHandlerFor[addAuditInput, addAuditOutput] {
	return func(ctx context.Context, _ *mcp.CallToolRequest, input addAuditInput) (*mcp.CallToolResult, addAuditOutput, error) {
		redaction := apptypes.NewAuditRedactionBuilder().
			ExtraRedactPatterns(s.extraRedactPatterns).
			Build()
		event, audit, err := s.event.Audit(ctx,
			input.Command,
			input.Input,
			input.Output,
			types.Client(resolveValue(input.Client, defaultClientValue)),
			types.Agent(resolveValue(input.Agent, defaultAgentValue)),
			types.SessionID(resolveValue(input.SessionID, defaultSessionValue)),
			types.Workspace(strings.TrimSpace(input.Workspace)),
			types.Empty[int](), // no exit code from MCP
			redaction,
		)
		if err != nil {
			return nil, addAuditOutput{}, xerrors.Errorf("failed to record command audit: %w", err)
		}

		return nil, addAuditOutput{
			EventID:         event.EventID().String(),
			Kind:            event.Kind().String(),
			SessionID:       event.SessionID().String(),
			Workspace:       event.Workspace().String(),
			Command:         audit.Command(),
			InputRedacted:   audit.InputRedacted(),
			OutputRedacted:  audit.OutputRedacted(),
			InputTruncated:  audit.InputTruncated(),
			OutputTruncated: audit.OutputTruncated(),
			CreatedAt:       event.CreatedAt().UTC().Format(time.RFC3339Nano),
		}, nil
	}
}

func (s *Server) listEvents() mcp.ToolHandlerFor[listEventsInput, eventsOutput] {
	return func(ctx context.Context, _ *mcp.CallToolRequest, input listEventsInput) (*mcp.CallToolResult, eventsOutput, error) {
		from, err := parseFlexibleTime(input.From, false)
		if err != nil {
			return nil, eventsOutput{}, xerrors.Errorf("failed to resolve from: %w", err)
		}
		to, err := parseFlexibleTime(input.To, true)
		if err != nil {
			return nil, eventsOutput{}, xerrors.Errorf("failed to resolve to: %w", err)
		}

		criteria := apptypes.NewEventListCriteriaBuilder(resolveLimit(input.Limit, defaultSearchLimit)).
			Offset(resolveOffset(input.Offset)).
			Kind(types.EventKind(strings.TrimSpace(input.Kind))).
			Client(types.Client(strings.TrimSpace(input.Client))).
			Agent(types.Agent(strings.TrimSpace(input.Agent))).
			SessionID(types.SessionID(strings.TrimSpace(input.SessionID))).
			Workspace(types.Workspace(strings.TrimSpace(input.Workspace))).
			From(from).
			To(to).
			Build()
		events, err := s.event.List(ctx, criteria)
		if err != nil {
			return nil, eventsOutput{}, xerrors.Errorf("failed to list events: %w", err)
		}

		return nil, eventsOutput{Events: convertEvents(events)}, nil
	}
}

func newSessionEventOutput(event *model.Event) sessionEventOutput {
	return sessionEventOutput{
		EventID:   event.EventID().String(),
		Kind:      event.Kind().String(),
		Client:    event.Client().String(),
		Agent:     event.Agent().String(),
		SessionID: event.SessionID().String(),
		Workspace: event.Workspace().String(),
		CreatedAt: event.CreatedAt().UTC().Format(time.RFC3339Nano),
	}
}

func validateActiveSession(event *model.Event, input sessionLookupInput) error {
	if input.AllowStale || event == nil {
		return nil
	}

	staleAfter := defaultActiveStaleAfter
	if input.StaleAfterSeconds > 0 {
		staleAfter = time.Duration(input.StaleAfterSeconds) * time.Second
	}
	if input.StaleAfterSeconds < 0 {
		return xerrors.Errorf("stale_after_seconds must be greater than or equal to 0")
	}

	if !event.CreatedAt().Before(time.Now().Add(-staleAfter)) {
		return nil
	}

	return xerrors.Errorf("active session %s is older than %s and considered stale", event.SessionID(), staleAfter)
}

func (s *Server) search() mcp.ToolHandlerFor[searchInput, eventsOutput] {
	return func(ctx context.Context, _ *mcp.CallToolRequest, input searchInput) (*mcp.CallToolResult, eventsOutput, error) {
		from, err := parseFlexibleTime(input.From, false)
		if err != nil {
			return nil, eventsOutput{}, xerrors.Errorf("failed to resolve from: %w", err)
		}
		to, err := parseFlexibleTime(input.To, true)
		if err != nil {
			return nil, eventsOutput{}, xerrors.Errorf("failed to resolve to: %w", err)
		}
		limit := resolveLimit(input.Limit, defaultSearchLimit)
		criteria := apptypes.NewEventSearchCriteriaBuilder(limit).
			Query(strings.TrimSpace(input.Query)).
			Workspace(types.Workspace(strings.TrimSpace(input.Workspace))).
			From(from).
			To(to).
			Build()
		events, err := s.event.Search(ctx, criteria)
		if err != nil {
			return nil, eventsOutput{}, xerrors.Errorf("failed to search events: %w", err)
		}

		return nil, eventsOutput{Events: convertEvents(events)}, nil
	}
}

func (s *Server) getContext() mcp.ToolHandlerFor[getContextInput, eventsOutput] {
	return func(ctx context.Context, _ *mcp.CallToolRequest, input getContextInput) (*mcp.CallToolResult, eventsOutput, error) {
		criteria := apptypes.NewEventContextCriteriaBuilder(resolveLimit(input.Limit, defaultContextLimit)).
			Workspace(types.Workspace(strings.TrimSpace(input.Workspace))).
			SessionID(types.SessionID(strings.TrimSpace(input.SessionID))).
			Build()
		events, err := s.event.Context(ctx, criteria)
		if err != nil {
			return nil, eventsOutput{}, xerrors.Errorf("failed to get context: %w", err)
		}

		return nil, eventsOutput{Events: convertEvents(events)}, nil
	}
}

func (s *Server) sessionHandoff() mcp.ToolHandlerFor[sessionHandoffInput, sessionHandoffOutput] {
	return func(ctx context.Context, _ *mcp.CallToolRequest, input sessionHandoffInput) (*mcp.CallToolResult, sessionHandoffOutput, error) {
		result, err := s.context.Handoff(ctx, buildContextPackCriteria(
			input.SessionID,
			input.Workspace,
			input.RecentCommandsLimit,
			input.MemoryLimit,
		))
		if err != nil {
			return nil, sessionHandoffOutput{}, xerrors.Errorf("failed to get session handoff: %w", err)
		}

		if !result.IsPresent() {
			return nil, sessionHandoffOutput{}, nil
		}

		pack, _ := result.Get()
		return nil, newContextPackOutput(pack), nil
	}
}

func (s *Server) memoryPack() mcp.ToolHandlerFor[memoryPackInput, memoryPackOutput] {
	return func(ctx context.Context, _ *mcp.CallToolRequest, input memoryPackInput) (*mcp.CallToolResult, memoryPackOutput, error) {
		result, err := s.context.Handoff(ctx, buildContextPackCriteria(
			input.SessionID,
			input.Workspace,
			input.RecentCommandsLimit,
			input.MemoryLimit,
		))
		if err != nil {
			return nil, memoryPackOutput{}, xerrors.Errorf("failed to build memory pack: %w", err)
		}
		if !result.IsPresent() {
			return nil, memoryPackOutput{}, nil
		}

		pack, _ := result.Get()
		return nil, newContextPackOutput(pack), nil
	}
}

func (s *Server) retrieveMemories() mcp.ToolHandlerFor[retrieveMemoriesInput, memoriesOutput] {
	return func(ctx context.Context, _ *mcp.CallToolRequest, input retrieveMemoriesInput) (*mcp.CallToolResult, memoriesOutput, error) {
		memoryIDValue := strings.TrimSpace(input.MemoryID)
		if memoryIDValue != "" {
			memoryID, err := types.MemoryIDOf(memoryIDValue)
			if err != nil {
				return nil, memoriesOutput{}, xerrors.Errorf("failed to resolve memory_id: %w", err)
			}
			details, err := s.memory.Show(ctx, memoryID)
			if err != nil {
				return nil, memoriesOutput{}, xerrors.Errorf("failed to retrieve memory: %w", err)
			}
			return nil, memoriesOutput{Memories: []memoryOutput{newMemoryOutput(details)}}, nil
		}

		scopes, err := parseMemoryScopes(input.Workspace, input.Agent, input.SessionFamily)
		if err != nil {
			return nil, memoriesOutput{}, err
		}
		statuses, err := parseMemoryStatuses(input.Statuses)
		if err != nil {
			return nil, memoriesOutput{}, err
		}
		memoryTypes, err := parseMemoryTypes(input.MemoryTypes)
		if err != nil {
			return nil, memoriesOutput{}, err
		}

		var summaries []apptypes.MemorySummary
		if strings.TrimSpace(input.Query) != "" {
			criteria := apptypes.NewMemorySearchCriteriaBuilder(resolveLimit(input.Limit, defaultSearchLimit)).
				Query(strings.TrimSpace(input.Query)).
				Offset(resolveOffset(input.Offset)).
				Scopes(scopes).
				Statuses(statuses).
				MemoryTypes(memoryTypes).
				Build()
			summaries, err = s.memory.Search(ctx, criteria)
			if err != nil {
				return nil, memoriesOutput{}, xerrors.Errorf("failed to search memories: %w", err)
			}
		} else {
			criteria := apptypes.NewMemoryListCriteriaBuilder(resolveLimit(input.Limit, defaultSearchLimit)).
				Offset(resolveOffset(input.Offset)).
				Scopes(scopes).
				Statuses(statuses).
				MemoryTypes(memoryTypes).
				Build()
			summaries, err = s.memory.List(ctx, criteria)
			if err != nil {
				return nil, memoriesOutput{}, xerrors.Errorf("failed to list memories: %w", err)
			}
		}

		memories := make([]memoryOutput, 0, len(summaries))
		for _, summary := range summaries {
			memories = append(memories, newMemoryOutputFromSummary(summary))
		}
		return nil, memoriesOutput{Memories: memories}, nil
	}
}

func (s *Server) rememberMemory() mcp.ToolHandlerFor[rememberMemoryInput, memoryOutput] {
	return func(ctx context.Context, _ *mcp.CallToolRequest, input rememberMemoryInput) (*mcp.CallToolResult, memoryOutput, error) {
		memoryType, scope, confidence, source, evidenceRefs, artifactRefs, err := parseMemoryWriteInput(
			input.MemoryType,
			input.Workspace,
			input.Agent,
			input.SessionFamily,
			input.Confidence,
			input.Source,
			input.EvidenceRefs,
			input.ArtifactRefs,
		)
		if err != nil {
			return nil, memoryOutput{}, err
		}
		details, err := s.memory.Remember(ctx, memoryType, scope, input.Fact, confidence, source, evidenceRefs, artifactRefs)
		if err != nil {
			return nil, memoryOutput{}, xerrors.Errorf("failed to remember memory: %w", err)
		}
		return nil, newMemoryOutput(details), nil
	}
}

func (s *Server) proposeMemory() mcp.ToolHandlerFor[proposeMemoryInput, memoryOutput] {
	return func(ctx context.Context, _ *mcp.CallToolRequest, input proposeMemoryInput) (*mcp.CallToolResult, memoryOutput, error) {
		memoryType, scope, source, evidenceRefs, artifactRefs, err := parseMemoryProposalInput(
			input.MemoryType,
			input.Workspace,
			input.Agent,
			input.SessionFamily,
			input.Source,
			input.EvidenceRefs,
			input.ArtifactRefs,
		)
		if err != nil {
			return nil, memoryOutput{}, err
		}
		details, err := s.memory.Propose(ctx, memoryType, scope, input.Fact, source, evidenceRefs, artifactRefs)
		if err != nil {
			return nil, memoryOutput{}, xerrors.Errorf("failed to propose memory: %w", err)
		}
		return nil, newMemoryOutput(details), nil
	}
}

func (s *Server) acceptMemory() mcp.ToolHandlerFor[acceptMemoryInput, memoryOutput] {
	return func(ctx context.Context, _ *mcp.CallToolRequest, input acceptMemoryInput) (*mcp.CallToolResult, memoryOutput, error) {
		memoryID, err := types.MemoryIDOf(strings.TrimSpace(input.MemoryID))
		if err != nil {
			return nil, memoryOutput{}, xerrors.Errorf("failed to resolve memory_id: %w", err)
		}
		confidence, err := parseOptionalConfidence(input.Confidence)
		if err != nil {
			return nil, memoryOutput{}, err
		}
		details, err := s.memory.Accept(ctx, memoryID, confidence)
		if err != nil {
			return nil, memoryOutput{}, xerrors.Errorf("failed to accept memory: %w", err)
		}
		return nil, newMemoryOutput(details), nil
	}
}

func (s *Server) rejectMemory() mcp.ToolHandlerFor[rejectMemoryInput, memoryOutput] {
	return func(ctx context.Context, _ *mcp.CallToolRequest, input rejectMemoryInput) (*mcp.CallToolResult, memoryOutput, error) {
		memoryID, err := types.MemoryIDOf(strings.TrimSpace(input.MemoryID))
		if err != nil {
			return nil, memoryOutput{}, xerrors.Errorf("failed to resolve memory_id: %w", err)
		}
		details, err := s.memory.Reject(ctx, memoryID)
		if err != nil {
			return nil, memoryOutput{}, xerrors.Errorf("failed to reject memory: %w", err)
		}
		return nil, newMemoryOutput(details), nil
	}
}

func (s *Server) supersedeMemory() mcp.ToolHandlerFor[supersedeMemoryInput, memoryOutput] {
	return func(ctx context.Context, _ *mcp.CallToolRequest, input supersedeMemoryInput) (*mcp.CallToolResult, memoryOutput, error) {
		memoryID, err := types.MemoryIDOf(strings.TrimSpace(input.MemoryID))
		if err != nil {
			return nil, memoryOutput{}, xerrors.Errorf("failed to resolve memory_id: %w", err)
		}
		memoryType, scope, confidence, source, evidenceRefs, artifactRefs, err := parseOptionalMemoryWriteInput(
			input.MemoryType,
			input.Workspace,
			input.Agent,
			input.SessionFamily,
			input.Confidence,
			input.Source,
			input.EvidenceRefs,
			input.ArtifactRefs,
		)
		if err != nil {
			return nil, memoryOutput{}, err
		}
		details, err := s.memory.Supersede(ctx, memoryID, memoryType, scope, input.Fact, confidence, source, evidenceRefs, artifactRefs)
		if err != nil {
			return nil, memoryOutput{}, xerrors.Errorf("failed to supersede memory: %w", err)
		}
		return nil, newMemoryOutput(details), nil
	}
}

func (s *Server) expireMemory() mcp.ToolHandlerFor[expireMemoryInput, memoryOutput] {
	return func(ctx context.Context, _ *mcp.CallToolRequest, input expireMemoryInput) (*mcp.CallToolResult, memoryOutput, error) {
		memoryID, err := types.MemoryIDOf(strings.TrimSpace(input.MemoryID))
		if err != nil {
			return nil, memoryOutput{}, xerrors.Errorf("failed to resolve memory_id: %w", err)
		}
		expiresAt, err := parseFlexibleTime(input.ExpiresAt, false)
		if err != nil {
			return nil, memoryOutput{}, xerrors.Errorf("failed to resolve expires_at: %w", err)
		}
		expiresAtOptional := types.Empty[time.Time]()
		if !expiresAt.IsZero() {
			expiresAtOptional = types.Of(expiresAt)
		}
		details, err := s.memory.Expire(ctx, memoryID, expiresAtOptional)
		if err != nil {
			return nil, memoryOutput{}, xerrors.Errorf("failed to expire memory: %w", err)
		}
		return nil, newMemoryOutput(details), nil
	}
}

func buildContextPackCriteria(sessionID string, workspace string, recentCommandsLimit int, memoryLimit int) apptypes.ContextPackCriteria {
	return apptypes.NewContextPackCriteriaBuilder().
		SessionID(types.SessionID(strings.TrimSpace(sessionID))).
		Workspace(types.Workspace(strings.TrimSpace(workspace))).
		RecentCommandsLimit(resolveLimit(recentCommandsLimit, 5)).
		MemoryLimit(resolveLimit(memoryLimit, 5)).
		Build()
}

func newContextPackOutput(pack apptypes.ContextPack) sessionHandoffOutput {
	return sessionHandoffOutput{
		SessionID:      pack.SessionID().String(),
		Workspace:      pack.Workspace().String(),
		Label:          pack.Label(),
		Status:         pack.Status(),
		TotalEvents:    pack.TotalEvents(),
		CommandCount:   pack.CommandCount(),
		Agents:         pack.Agents(),
		Summary:        pack.WorkingState().CombinedSummary(),
		WorkingState:   newWorkingStateOutput(pack.WorkingState()),
		RecentCommands: pack.RecentCommands(),
		Memories:       convertMemorySummaries(pack.Memories()),
	}
}

func newWorkingStateOutput(state apptypes.WorkingState) workingStateOutput {
	return workingStateOutput{
		SessionSummary:  state.SessionSummary(),
		CompactSummary:  state.CompactSummary(),
		CombinedSummary: state.CombinedSummary(),
	}
}

func convertMemorySummaries(memories []apptypes.MemorySummary) []memorySummaryOutput {
	outputs := make([]memorySummaryOutput, 0, len(memories))
	for _, summary := range memories {
		outputs = append(outputs, memorySummaryOutput{
			MemoryID:   summary.MemoryID().String(),
			Type:       summary.MemoryType().String(),
			ScopeKind:  summary.Scope().Kind().String(),
			ScopeValue: summary.Scope().Key(),
			Fact:       summary.Fact(),
			Status:     summary.Status().String(),
			Confidence: summary.Confidence().String(),
			Source:     summary.Source().String(),
			ExpiresAt:  formatOptionalTime(summary.ExpiresAt()),
			CreatedAt:  summary.CreatedAt().UTC().Format(time.RFC3339Nano),
			UpdatedAt:  summary.UpdatedAt().UTC().Format(time.RFC3339Nano),
		})
	}
	return outputs
}

func newMemoryOutput(details apptypes.MemoryDetails) memoryOutput {
	summary := details.Summary()
	supersedes := ""
	if value, ok := summary.Supersedes().Get(); ok {
		supersedes = value.String()
	}

	return memoryOutput{
		MemoryID:     summary.MemoryID().String(),
		Type:         summary.MemoryType().String(),
		ScopeKind:    summary.Scope().Kind().String(),
		ScopeValue:   summary.Scope().Key(),
		Fact:         summary.Fact(),
		Status:       summary.Status().String(),
		Confidence:   summary.Confidence().String(),
		Source:       summary.Source().String(),
		Supersedes:   supersedes,
		ExpiresAt:    formatOptionalTime(summary.ExpiresAt()),
		CreatedAt:    summary.CreatedAt().UTC().Format(time.RFC3339Nano),
		UpdatedAt:    summary.UpdatedAt().UTC().Format(time.RFC3339Nano),
		EvidenceRefs: convertEvidenceRefs(details.EvidenceRefs()),
		ArtifactRefs: convertArtifactRefs(details.ArtifactRefs()),
	}
}

func newMemoryOutputFromSummary(summary apptypes.MemorySummary) memoryOutput {
	supersedes := ""
	if memoryID, ok := summary.Supersedes().Get(); ok {
		supersedes = memoryID.String()
	}

	return memoryOutput{
		MemoryID:   summary.MemoryID().String(),
		Type:       summary.MemoryType().String(),
		ScopeKind:  summary.Scope().Kind().String(),
		ScopeValue: summary.Scope().Key(),
		Fact:       summary.Fact(),
		Status:     summary.Status().String(),
		Confidence: summary.Confidence().String(),
		Source:     summary.Source().String(),
		Supersedes: supersedes,
		ExpiresAt:  formatOptionalTime(summary.ExpiresAt()),
		CreatedAt:  summary.CreatedAt().UTC().Format(time.RFC3339Nano),
		UpdatedAt:  summary.UpdatedAt().UTC().Format(time.RFC3339Nano),
	}
}

func convertEvidenceRefs(refs []types.EvidenceRef) []memoryRefOutput {
	outputs := make([]memoryRefOutput, 0, len(refs))
	for _, ref := range refs {
		outputs = append(outputs, memoryRefOutput{Kind: ref.Kind().String(), Value: ref.Value()})
	}
	return outputs
}

func convertArtifactRefs(refs []types.ArtifactRef) []memoryRefOutput {
	outputs := make([]memoryRefOutput, 0, len(refs))
	for _, ref := range refs {
		outputs = append(outputs, memoryRefOutput{Kind: ref.Kind().String(), Value: ref.Value()})
	}
	return outputs
}

func formatOptionalTime(value types.Optional[time.Time]) string {
	if timeValue, ok := value.Get(); ok {
		return timeValue.UTC().Format(time.RFC3339Nano)
	}
	return ""
}

func parseMemoryScopes(workspace string, agent string, sessionFamily string) ([]types.MemoryScope, error) {
	scopes := make([]types.MemoryScope, 0, 3)
	if trimmedWorkspace := strings.TrimSpace(workspace); trimmedWorkspace != "" {
		resolvedWorkspace, err := types.WorkspaceOf(trimmedWorkspace)
		if err != nil {
			return nil, xerrors.Errorf("failed to resolve workspace scope: %w", err)
		}
		scopes = append(scopes, types.WorkspaceScopeOf(resolvedWorkspace))
	}
	if trimmedAgent := strings.TrimSpace(agent); trimmedAgent != "" {
		resolvedAgent, err := types.AgentOf(trimmedAgent)
		if err != nil {
			return nil, xerrors.Errorf("failed to resolve agent scope: %w", err)
		}
		scopes = append(scopes, types.AgentScopeOf(resolvedAgent))
	}
	if trimmedSessionFamily := strings.TrimSpace(sessionFamily); trimmedSessionFamily != "" {
		resolvedSessionID, err := types.SessionIDOf(trimmedSessionFamily)
		if err != nil {
			return nil, xerrors.Errorf("failed to resolve session_family scope: %w", err)
		}
		scopes = append(scopes, types.SessionFamilyScopeOf(resolvedSessionID))
	}
	return scopes, nil
}

func parseSingleMemoryScope(workspace string, agent string, sessionFamily string) (types.MemoryScope, error) {
	scopes, err := parseMemoryScopes(workspace, agent, sessionFamily)
	if err != nil {
		return nil, err
	}
	if len(scopes) == 0 {
		return nil, xerrors.Errorf("exactly one of workspace, agent, or session_family is required")
	}
	if len(scopes) > 1 {
		return nil, xerrors.Errorf("workspace, agent, and session_family are mutually exclusive")
	}
	return scopes[0], nil
}

func parseOptionalMemoryScope(workspace string, agent string, sessionFamily string) (types.MemoryScope, error) {
	scopes, err := parseMemoryScopes(workspace, agent, sessionFamily)
	if err != nil {
		return nil, err
	}
	if len(scopes) > 1 {
		return nil, xerrors.Errorf("workspace, agent, and session_family are mutually exclusive")
	}
	if len(scopes) == 0 {
		return nil, nil
	}
	return scopes[0], nil
}

func parseMemoryStatuses(values []string) ([]types.MemoryStatus, error) {
	statuses := make([]types.MemoryStatus, 0, len(values))
	for _, value := range values {
		resolved, err := types.MemoryStatusOf(strings.TrimSpace(value))
		if err != nil {
			return nil, xerrors.Errorf("failed to resolve memory status: %w", err)
		}
		statuses = append(statuses, resolved)
	}
	return statuses, nil
}

func parseMemoryTypes(values []string) ([]types.MemoryType, error) {
	memoryTypes := make([]types.MemoryType, 0, len(values))
	for _, value := range values {
		resolved, err := types.MemoryTypeOf(strings.TrimSpace(value))
		if err != nil {
			return nil, xerrors.Errorf("failed to resolve memory type: %w", err)
		}
		memoryTypes = append(memoryTypes, resolved)
	}
	return memoryTypes, nil
}

func parseOptionalConfidence(value string) (types.Optional[types.Confidence], error) {
	if strings.TrimSpace(value) == "" {
		return types.Empty[types.Confidence](), nil
	}
	resolved, err := types.ConfidenceOf(strings.TrimSpace(value))
	if err != nil {
		return types.Empty[types.Confidence](), xerrors.Errorf("failed to resolve confidence: %w", err)
	}
	return types.Of(resolved), nil
}

func parseMemorySource(value string) (types.MemorySource, error) {
	if strings.TrimSpace(value) == "" {
		return types.MemorySource(""), nil
	}
	resolved, err := types.MemorySourceOf(strings.TrimSpace(value))
	if err != nil {
		return types.MemorySource(""), xerrors.Errorf("failed to resolve memory source: %w", err)
	}
	return resolved, nil
}

func parseEvidenceRefs(refs []memoryRefInput) ([]types.EvidenceRef, error) {
	outputs := make([]types.EvidenceRef, 0, len(refs))
	for _, ref := range refs {
		kind, err := types.EvidenceRefKindOf(strings.TrimSpace(ref.Kind))
		if err != nil {
			return nil, xerrors.Errorf("failed to resolve evidence ref kind: %w", err)
		}
		resolved, err := types.EvidenceRefOf(kind, strings.TrimSpace(ref.Value))
		if err != nil {
			return nil, xerrors.Errorf("failed to resolve evidence ref: %w", err)
		}
		outputs = append(outputs, resolved)
	}
	return outputs, nil
}

func parseArtifactRefs(refs []memoryRefInput) ([]types.ArtifactRef, error) {
	outputs := make([]types.ArtifactRef, 0, len(refs))
	for _, ref := range refs {
		kind, err := types.ArtifactRefKindOf(strings.TrimSpace(ref.Kind))
		if err != nil {
			return nil, xerrors.Errorf("failed to resolve artifact ref kind: %w", err)
		}
		resolved, err := types.ArtifactRefOf(kind, strings.TrimSpace(ref.Value))
		if err != nil {
			return nil, xerrors.Errorf("failed to resolve artifact ref: %w", err)
		}
		outputs = append(outputs, resolved)
	}
	return outputs, nil
}

func parseMemoryWriteInput(
	memoryType string,
	workspace string,
	agent string,
	sessionFamily string,
	confidence string,
	source string,
	evidenceRefs []memoryRefInput,
	artifactRefs []memoryRefInput,
) (types.MemoryType, types.MemoryScope, types.Optional[types.Confidence], types.MemorySource, []types.EvidenceRef, []types.ArtifactRef, error) {
	resolvedType, err := types.MemoryTypeOf(strings.TrimSpace(memoryType))
	if err != nil {
		return types.MemoryType(""), nil, types.Empty[types.Confidence](), types.MemorySource(""), nil, nil, xerrors.Errorf("failed to resolve memory type: %w", err)
	}
	scope, err := parseSingleMemoryScope(workspace, agent, sessionFamily)
	if err != nil {
		return types.MemoryType(""), nil, types.Empty[types.Confidence](), types.MemorySource(""), nil, nil, err
	}
	resolvedConfidence, err := parseOptionalConfidence(confidence)
	if err != nil {
		return types.MemoryType(""), nil, types.Empty[types.Confidence](), types.MemorySource(""), nil, nil, err
	}
	resolvedSource, err := parseMemorySource(source)
	if err != nil {
		return types.MemoryType(""), nil, types.Empty[types.Confidence](), types.MemorySource(""), nil, nil, err
	}
	resolvedEvidenceRefs, err := parseEvidenceRefs(evidenceRefs)
	if err != nil {
		return types.MemoryType(""), nil, types.Empty[types.Confidence](), types.MemorySource(""), nil, nil, err
	}
	resolvedArtifactRefs, err := parseArtifactRefs(artifactRefs)
	if err != nil {
		return types.MemoryType(""), nil, types.Empty[types.Confidence](), types.MemorySource(""), nil, nil, err
	}
	return resolvedType, scope, resolvedConfidence, resolvedSource, resolvedEvidenceRefs, resolvedArtifactRefs, nil
}

func parseMemoryProposalInput(
	memoryType string,
	workspace string,
	agent string,
	sessionFamily string,
	source string,
	evidenceRefs []memoryRefInput,
	artifactRefs []memoryRefInput,
) (types.MemoryType, types.MemoryScope, types.MemorySource, []types.EvidenceRef, []types.ArtifactRef, error) {
	resolvedType, scope, _, resolvedSource, resolvedEvidenceRefs, resolvedArtifactRefs, err := parseMemoryWriteInput(
		memoryType,
		workspace,
		agent,
		sessionFamily,
		"",
		source,
		evidenceRefs,
		artifactRefs,
	)
	return resolvedType, scope, resolvedSource, resolvedEvidenceRefs, resolvedArtifactRefs, err
}

func parseOptionalMemoryWriteInput(
	memoryType string,
	workspace string,
	agent string,
	sessionFamily string,
	confidence string,
	source string,
	evidenceRefs []memoryRefInput,
	artifactRefs []memoryRefInput,
) (types.MemoryType, types.MemoryScope, types.Optional[types.Confidence], types.MemorySource, []types.EvidenceRef, []types.ArtifactRef, error) {
	var resolvedType types.MemoryType
	if strings.TrimSpace(memoryType) != "" {
		value, err := types.MemoryTypeOf(strings.TrimSpace(memoryType))
		if err != nil {
			return types.MemoryType(""), nil, types.Empty[types.Confidence](), types.MemorySource(""), nil, nil, xerrors.Errorf("failed to resolve memory type: %w", err)
		}
		resolvedType = value
	}
	scope, err := parseOptionalMemoryScope(workspace, agent, sessionFamily)
	if err != nil {
		return types.MemoryType(""), nil, types.Empty[types.Confidence](), types.MemorySource(""), nil, nil, err
	}
	resolvedConfidence, err := parseOptionalConfidence(confidence)
	if err != nil {
		return types.MemoryType(""), nil, types.Empty[types.Confidence](), types.MemorySource(""), nil, nil, err
	}
	resolvedSource, err := parseMemorySource(source)
	if err != nil {
		return types.MemoryType(""), nil, types.Empty[types.Confidence](), types.MemorySource(""), nil, nil, err
	}
	resolvedEvidenceRefs, err := parseEvidenceRefs(evidenceRefs)
	if err != nil {
		return types.MemoryType(""), nil, types.Empty[types.Confidence](), types.MemorySource(""), nil, nil, err
	}
	resolvedArtifactRefs, err := parseArtifactRefs(artifactRefs)
	if err != nil {
		return types.MemoryType(""), nil, types.Empty[types.Confidence](), types.MemorySource(""), nil, nil, err
	}
	return resolvedType, scope, resolvedConfidence, resolvedSource, resolvedEvidenceRefs, resolvedArtifactRefs, nil
}

func resolveValue(value string, defaultValue string) string {
	trimmedValue := strings.TrimSpace(value)
	if trimmedValue != "" {
		return trimmedValue
	}

	return defaultValue
}

func resolveLimit(value int, defaultValue int) int {
	if value > 0 {
		return value
	}

	return defaultValue
}

func resolveOffset(value int) int {
	if value > 0 {
		return value
	}

	return 0
}

func parseFlexibleTime(value string, endExclusive bool) (time.Time, error) {
	trimmedValue := strings.TrimSpace(value)
	if trimmedValue == "" {
		return time.Time{}, nil
	}

	if parsedTime, err := time.Parse(time.RFC3339, trimmedValue); err == nil {
		return parsedTime.UTC(), nil
	}

	parsedDate, err := time.Parse("2006-01-02", trimmedValue)
	if err != nil {
		return time.Time{}, xerrors.Errorf("time must be RFC3339 or YYYY-MM-DD: %w", err)
	}
	if endExclusive {
		return parsedDate.AddDate(0, 0, 1), nil
	}

	return parsedDate, nil
}

func convertEvents(events []*model.Event) []eventOutput {
	outputs := make([]eventOutput, 0, len(events))
	for _, event := range events {
		outputs = append(outputs, eventOutput{
			EventID:   event.EventID().String(),
			Kind:      event.Kind().String(),
			Client:    event.Client().String(),
			Agent:     event.Agent().String(),
			SessionID: event.SessionID().String(),
			Workspace: event.Workspace().String(),
			Body:      event.Body(),
			CreatedAt: event.CreatedAt().UTC().Format(time.RFC3339Nano),
		})
	}

	return outputs
}

func boolPtr(value bool) *bool {
	return &value
}
