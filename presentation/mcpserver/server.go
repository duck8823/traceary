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
	memoryHygiene       usecase.MemoryHygieneUsecase
	memoryExport        usecase.MemoryExportUsecase
	memoryBridgeImport  usecase.MemoryBridgeImportUsecase
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
	memoryHygiene usecase.MemoryHygieneUsecase,
	memoryExport usecase.MemoryExportUsecase,
	memoryBridgeImport usecase.MemoryBridgeImportUsecase,
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
		memoryHygiene:       memoryHygiene,
		memoryExport:        memoryExport,
		memoryBridgeImport:  memoryBridgeImport,
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
		Description: "Add a log event, note, prompt, or compact summary.",
		Annotations: &mcp.ToolAnnotations{DestructiveHint: boolPtr(false)},
	}, s.addLog())
	mcp.AddTool(server, &mcp.Tool{
		Name:        "start_session",
		Description: "Start a session and record a session_started event.",
		Annotations: &mcp.ToolAnnotations{DestructiveHint: boolPtr(false)},
	}, s.startSession())
	mcp.AddTool(server, &mcp.Tool{
		Name:        "end_session",
		Description: "End a session and record a session_ended event.",
		Annotations: &mcp.ToolAnnotations{DestructiveHint: boolPtr(true)},
	}, s.endSession())
	mcp.AddTool(server, &mcp.Tool{
		Name:        "latest_session",
		Description: "Get the latest session for resume or handoff by agent, client, or workspace.",
		Annotations: &mcp.ToolAnnotations{ReadOnlyHint: true},
	}, s.latestSession())
	mcp.AddTool(server, &mcp.Tool{
		Name:        "active_session",
		Description: "Get the active or open session for resume by agent, client, or workspace.",
		Annotations: &mcp.ToolAnnotations{ReadOnlyHint: true},
	}, s.activeSession())
	mcp.AddTool(server, &mcp.Tool{
		Name:        "list_events",
		Description: "List recent events, logs, audits, prompts, and summaries.",
		Annotations: &mcp.ToolAnnotations{ReadOnlyHint: true},
	}, s.listEvents())
	mcp.AddTool(server, &mcp.Tool{
		Name:        "add_audit",
		Description: "Add a shell command audit log with redacted input and output.",
		Annotations: &mcp.ToolAnnotations{DestructiveHint: boolPtr(false)},
	}, s.addAudit())
	mcp.AddTool(server, &mcp.Tool{
		Name:        "search",
		Description: "Search events, logs, audits, prompts, and summaries by text, time, or workspace.",
		Annotations: &mcp.ToolAnnotations{ReadOnlyHint: true},
	}, s.search())
	mcp.AddTool(server, &mcp.Tool{
		Name:        "get_context",
		Description: "Get recent context events, logs, audits, prompts, and summaries for a session or workspace.",
		Annotations: &mcp.ToolAnnotations{ReadOnlyHint: true},
	}, s.getContext())
	mcp.AddTool(server, &mcp.Tool{
		Name:        "session_handoff",
		Description: "Get a session handoff summary for resume, context, memory, and recent commands.",
		Annotations: &mcp.ToolAnnotations{ReadOnlyHint: true},
	}, s.sessionHandoff())
	mcp.AddTool(server, &mcp.Tool{
		Name:        "retrieve_memories",
		Description: "Retrieve durable memories by ID, query, status, type, agent, or workspace.",
		Annotations: &mcp.ToolAnnotations{ReadOnlyHint: true},
	}, s.retrieveMemories())
	mcp.AddTool(server, &mcp.Tool{
		Name:        "remember_memory",
		Description: "Remember and record an accepted durable memory with evidence and artifacts.",
		Annotations: &mcp.ToolAnnotations{DestructiveHint: boolPtr(false)},
	}, s.rememberMemory())
	mcp.AddTool(server, &mcp.Tool{
		Name:        "propose_memory",
		Description: "Propose and record a candidate durable memory for review.",
		Annotations: &mcp.ToolAnnotations{DestructiveHint: boolPtr(false)},
	}, s.proposeMemory())
	mcp.AddTool(server, &mcp.Tool{
		Name:        "accept_memory",
		Description: "Accept a candidate durable memory and set confidence.",
		Annotations: &mcp.ToolAnnotations{DestructiveHint: boolPtr(true)},
	}, s.acceptMemory())
	mcp.AddTool(server, &mcp.Tool{
		Name:        "reject_memory",
		Description: "Reject a candidate durable memory from review.",
		Annotations: &mcp.ToolAnnotations{DestructiveHint: boolPtr(true)},
	}, s.rejectMemory())
	mcp.AddTool(server, &mcp.Tool{
		Name:        "supersede_memory",
		Description: "Supersede an accepted durable memory with a replacement memory.",
		Annotations: &mcp.ToolAnnotations{DestructiveHint: boolPtr(true)},
	}, s.supersedeMemory())
	mcp.AddTool(server, &mcp.Tool{
		Name:        "expire_memory",
		Description: "Expire or retire a durable memory at a timestamp.",
		Annotations: &mcp.ToolAnnotations{DestructiveHint: boolPtr(true)},
	}, s.expireMemory())
	mcp.AddTool(server, &mcp.Tool{
		Name:        "set_memory_validity",
		Description: "Set the content validity window (valid_from / valid_to) on a durable memory.",
		Annotations: &mcp.ToolAnnotations{DestructiveHint: boolPtr(true)},
	}, s.setMemoryValidity())
	mcp.AddTool(server, &mcp.Tool{
		Name:        "memory_pack",
		Description: "Build a memory pack for prompt context, handoff, automation, and recent commands.",
		Annotations: &mcp.ToolAnnotations{ReadOnlyHint: true},
	}, s.memoryPack())
	mcp.AddTool(server, &mcp.Tool{
		Name:        "accept_memories_batch",
		Description: "Batch accept candidate durable memories by id, mirroring `traceary memory inbox accept --ids`.",
		Annotations: &mcp.ToolAnnotations{DestructiveHint: boolPtr(true)},
	}, s.acceptMemoriesBatch())
	mcp.AddTool(server, &mcp.Tool{
		Name:        "reject_memories_batch",
		Description: "Batch reject candidate durable memories by id, mirroring `traceary memory inbox reject --ids`.",
		Annotations: &mcp.ToolAnnotations{DestructiveHint: boolPtr(true)},
	}, s.rejectMemoriesBatch())
	mcp.AddTool(server, &mcp.Tool{
		Name:        "scan_memory_hygiene",
		Description: "Scan accepted memories for redaction / expiry / duplicate hygiene suggestions.",
		Annotations: &mcp.ToolAnnotations{ReadOnlyHint: true},
	}, s.scanMemoryHygiene())
	mcp.AddTool(server, &mcp.Tool{
		Name:        "export_memories",
		Description: "Render accepted memories into CLAUDE.md / AGENTS.md / GEMINI.md markdown.",
		Annotations: &mcp.ToolAnnotations{ReadOnlyHint: true},
	}, s.exportMemories())
	mcp.AddTool(server, &mcp.Tool{
		Name:        "import_memory_instructions",
		Description: "Import bullets from a host instruction file as durable-memory candidates.",
		Annotations: &mcp.ToolAnnotations{DestructiveHint: boolPtr(false)},
	}, s.importMemoryInstructions())

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
		if _, ok := result.Value(); !ok {
			return nil, sessionEventOutput{}, xerrors.Errorf("no matching session found")
		}
		latestEvent, _ := result.Value()

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
		if _, ok := result.Value(); !ok {
			return nil, sessionEventOutput{}, xerrors.Errorf("no matching active session found")
		}
		activeEvent, _ := result.Value()
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
			types.None[int](), // no exit code from MCP
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
		preset, err := apptypes.MemoryRetrievalPresetOf(input.Preset)
		if err != nil {
			return nil, sessionHandoffOutput{}, xerrors.Errorf("failed to parse preset: %w", err)
		}
		result, err := s.context.Handoff(ctx, buildContextPackCriteria(
			input.SessionID,
			input.Workspace,
			input.RecentCommandsLimit,
			input.MemoryLimit,
			preset,
		))
		if err != nil {
			return nil, sessionHandoffOutput{}, xerrors.Errorf("failed to get session handoff: %w", err)
		}

		if _, ok := result.Value(); !ok {
			return nil, sessionHandoffOutput{}, nil
		}

		pack, _ := result.Value()
		return nil, newContextPackOutput(pack), nil
	}
}

func (s *Server) memoryPack() mcp.ToolHandlerFor[memoryPackInput, memoryPackOutput] {
	return func(ctx context.Context, _ *mcp.CallToolRequest, input memoryPackInput) (*mcp.CallToolResult, memoryPackOutput, error) {
		preset, err := apptypes.MemoryRetrievalPresetOf(input.Preset)
		if err != nil {
			return nil, memoryPackOutput{}, xerrors.Errorf("failed to parse preset: %w", err)
		}
		result, err := s.context.Handoff(ctx, buildContextPackCriteria(
			input.SessionID,
			input.Workspace,
			input.RecentCommandsLimit,
			input.MemoryLimit,
			preset,
		))
		if err != nil {
			return nil, memoryPackOutput{}, xerrors.Errorf("failed to build memory pack: %w", err)
		}
		if _, ok := result.Value(); !ok {
			return nil, memoryPackOutput{}, nil
		}

		pack, _ := result.Value()
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

		asOfTime := time.Time{}
		if trimmedAsOf := strings.TrimSpace(input.AsOf); trimmedAsOf != "" {
			parsed, err := parseFlexibleTime(trimmedAsOf, false)
			if err != nil {
				return nil, memoriesOutput{}, xerrors.Errorf("failed to parse as_of: %w", err)
			}
			asOfTime = parsed
		}
		preset, err := apptypes.MemoryRetrievalPresetOf(input.Preset)
		if err != nil {
			return nil, memoriesOutput{}, xerrors.Errorf("failed to parse preset: %w", err)
		}

		var summaries []apptypes.MemorySummary
		if strings.TrimSpace(input.Query) != "" {
			searchBuilder := apptypes.NewMemorySearchCriteriaBuilder(resolveLimit(input.Limit, defaultSearchLimit)).
				Query(strings.TrimSpace(input.Query)).
				Offset(resolveOffset(input.Offset)).
				Scopes(scopes)
			if preset != "" {
				searchBuilder = preset.ApplyToMemorySearchCriteriaBuilder(searchBuilder)
			}
			if len(statuses) > 0 {
				searchBuilder = searchBuilder.Statuses(statuses)
			}
			if len(memoryTypes) > 0 {
				searchBuilder = searchBuilder.MemoryTypes(memoryTypes)
			}
			searchBuilder = searchBuilder.IncludeExpiredByValidity(input.IncludeExpired)
			if !asOfTime.IsZero() {
				searchBuilder = searchBuilder.AsOf(asOfTime)
			}
			summaries, err = s.memory.Search(ctx, searchBuilder.Build())
			if err != nil {
				return nil, memoriesOutput{}, xerrors.Errorf("failed to search memories: %w", err)
			}
		} else {
			listBuilder := apptypes.NewMemoryListCriteriaBuilder(resolveLimit(input.Limit, defaultSearchLimit)).
				Offset(resolveOffset(input.Offset)).
				Scopes(scopes)
			if preset != "" {
				listBuilder = preset.ApplyToMemoryListCriteriaBuilder(listBuilder)
			}
			if len(statuses) > 0 {
				listBuilder = listBuilder.Statuses(statuses)
			}
			if len(memoryTypes) > 0 {
				listBuilder = listBuilder.MemoryTypes(memoryTypes)
			}
			listBuilder = listBuilder.IncludeExpiredByValidity(input.IncludeExpired)
			if !asOfTime.IsZero() {
				listBuilder = listBuilder.AsOf(asOfTime)
			}
			summaries, err = s.memory.List(ctx, listBuilder.Build())
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

// acceptMemoriesBatch applies Accept to every supplied id and returns the
// per-id success / failure breakdown. Unknown or malformed ids are
// reported as failures rather than a top-level error so a partial batch
// never leaves the caller guessing which specific ids moved.
func (s *Server) acceptMemoriesBatch() mcp.ToolHandlerFor[acceptMemoriesBatchInput, inboxBatchMemoryOutput] {
	return func(ctx context.Context, _ *mcp.CallToolRequest, input acceptMemoriesBatchInput) (*mcp.CallToolResult, inboxBatchMemoryOutput, error) {
		if len(input.MemoryIDs) == 0 {
			return nil, inboxBatchMemoryOutput{}, xerrors.Errorf("memory_ids must list at least one id")
		}
		confidence, err := parseOptionalConfidence(input.Confidence)
		if err != nil {
			return nil, inboxBatchMemoryOutput{}, err
		}
		out := inboxBatchMemoryOutput{Action: "accept"}
		for _, rawID := range input.MemoryIDs {
			trimmed := strings.TrimSpace(rawID)
			if trimmed == "" {
				continue
			}
			memoryID, err := types.MemoryIDOf(trimmed)
			if err != nil {
				out.Failures = append(out.Failures, inboxBatchMemoryFailureOutput{MemoryID: trimmed, Error: err.Error()})
				continue
			}
			details, err := s.memory.Accept(ctx, memoryID, confidence)
			if err != nil {
				out.Failures = append(out.Failures, inboxBatchMemoryFailureOutput{MemoryID: trimmed, Error: err.Error()})
				continue
			}
			out.Processed = append(out.Processed, newMemoryOutput(details))
		}
		return nil, out, nil
	}
}

// scanMemoryHygiene exposes the hygiene scanner over MCP. The default
// staleness threshold matches the CLI (90 days) so agent hosts and
// operators see the same expiry cadence out of the box.
func (s *Server) scanMemoryHygiene() mcp.ToolHandlerFor[scanMemoryHygieneInput, memoryHygieneOutput] {
	return func(ctx context.Context, _ *mcp.CallToolRequest, input scanMemoryHygieneInput) (*mcp.CallToolResult, memoryHygieneOutput, error) {
		if s.memoryHygiene == nil {
			return nil, memoryHygieneOutput{}, xerrors.Errorf("memory hygiene usecase is not configured")
		}
		criteria := apptypes.MemoryHygieneScanCriteria{}
		if workspace := strings.TrimSpace(input.Workspace); workspace != "" {
			resolvedWorkspace, err := types.WorkspaceOf(workspace)
			if err != nil {
				return nil, memoryHygieneOutput{}, xerrors.Errorf("failed to resolve workspace: %w", err)
			}
			criteria.Scopes = []types.MemoryScope{types.WorkspaceScopeOf(resolvedWorkspace)}
		}
		if input.ExpiryDays > 0 {
			criteria.StalenessThreshold = time.Duration(input.ExpiryDays) * 24 * time.Hour
		}
		result, err := s.memoryHygiene.Scan(ctx, criteria)
		if err != nil {
			return nil, memoryHygieneOutput{}, xerrors.Errorf("failed to scan memory hygiene: %w", err)
		}
		out := memoryHygieneOutput{
			RedactionHitCount:       result.RedactionHitCount,
			ExpiryCandidateCount:    result.ExpiryCandidateCount,
			DuplicateCount:          result.DuplicateCount,
			SupersedeCandidateCount: result.SupersedeCandidateCount,
			Suggestions:             make([]memoryHygieneSuggestionOutput, 0, len(result.Suggestions)),
		}
		for _, suggestion := range result.Suggestions {
			entry := memoryHygieneSuggestionOutput{
				MemoryID:      suggestion.MemoryID.String(),
				Kind:          string(suggestion.Kind),
				Reason:        suggestion.Reason,
				Fact:          suggestion.Fact,
				SanitizedFact: suggestion.SanitizedFact,
				Similarity:    suggestion.Similarity,
				UpdatedAt:     suggestion.UpdatedAt.UTC().Format(time.RFC3339),
			}
			if suggestion.DuplicateMemoryID != "" {
				entry.DuplicateMemoryID = suggestion.DuplicateMemoryID.String()
			}
			if suggestion.ReplacementMemoryID != "" {
				entry.ReplacementMemoryID = suggestion.ReplacementMemoryID.String()
				entry.ReplacementFact = suggestion.ReplacementFact
			}
			if suggestion.Scope != nil {
				entry.ScopeKind = suggestion.Scope.Kind().String()
				entry.ScopeValue = suggestion.Scope.Key()
			}
			out.Suggestions = append(out.Suggestions, entry)
		}
		return nil, out, nil
	}
}

// exportMemories mirrors the CLI `memory export` command over MCP. The
// handler intentionally stays filesystem-free — the generated markdown
// is returned inline so agent hosts can decide whether to write it,
// diff it, or post-process it before committing.
func (s *Server) exportMemories() mcp.ToolHandlerFor[exportMemoriesInput, exportMemoriesOutput] {
	return func(ctx context.Context, _ *mcp.CallToolRequest, input exportMemoriesInput) (*mcp.CallToolResult, exportMemoriesOutput, error) {
		if s.memoryExport == nil {
			return nil, exportMemoriesOutput{}, xerrors.Errorf("memory export usecase is not configured")
		}
		target, ok := apptypes.MemoryBridgeTargetOf(strings.ToLower(strings.TrimSpace(input.Target)))
		if !ok {
			return nil, exportMemoriesOutput{}, xerrors.Errorf("target must be one of claude / codex / gemini, got %q", input.Target)
		}
		criteria := apptypes.MemoryExportCriteria{Target: target}
		if workspace := strings.TrimSpace(input.Workspace); workspace != "" {
			resolvedWorkspace, err := types.WorkspaceOf(workspace)
			if err != nil {
				return nil, exportMemoriesOutput{}, xerrors.Errorf("failed to resolve workspace: %w", err)
			}
			criteria.Scopes = []types.MemoryScope{types.WorkspaceScopeOf(resolvedWorkspace)}
		}
		result, err := s.memoryExport.Export(ctx, criteria)
		if err != nil {
			return nil, exportMemoriesOutput{}, xerrors.Errorf("failed to export memories: %w", err)
		}
		return nil, exportMemoriesOutput{
			Target:        result.Target.String(),
			ExportedCount: result.ExportedCount,
			Markdown:      result.Markdown,
		}, nil
	}
}

// importMemoryInstructions mirrors the CLI `memory import instructions`
// command. Path and Markdown are mutually exclusive — the caller picks
// one and the usecase rejects an empty combination.
func (s *Server) importMemoryInstructions() mcp.ToolHandlerFor[importMemoryInstructionsInput, importMemoryInstructionsOutput] {
	return func(ctx context.Context, _ *mcp.CallToolRequest, input importMemoryInstructionsInput) (*mcp.CallToolResult, importMemoryInstructionsOutput, error) {
		if s.memoryBridgeImport == nil {
			return nil, importMemoryInstructionsOutput{}, xerrors.Errorf("memory bridge import usecase is not configured")
		}
		target, ok := apptypes.MemoryBridgeTargetOf(strings.ToLower(strings.TrimSpace(input.Source)))
		if !ok {
			return nil, importMemoryInstructionsOutput{}, xerrors.Errorf("source must be one of claude / codex / gemini, got %q", input.Source)
		}
		criteria := apptypes.MemoryBridgeImportCriteria{
			Target:   target,
			Path:     strings.TrimSpace(input.Path),
			Markdown: input.Markdown,
		}
		if workspace := strings.TrimSpace(input.Workspace); workspace != "" {
			resolvedWorkspace, err := types.WorkspaceOf(workspace)
			if err != nil {
				return nil, importMemoryInstructionsOutput{}, xerrors.Errorf("failed to resolve workspace: %w", err)
			}
			criteria.WorkspaceFallback = resolvedWorkspace
		}
		result, err := s.memoryBridgeImport.ImportInstructions(ctx, criteria)
		if err != nil {
			return nil, importMemoryInstructionsOutput{}, xerrors.Errorf("failed to import instructions: %w", err)
		}
		out := importMemoryInstructionsOutput{
			SkippedDuplicateCount: result.SkippedDuplicateCount,
			SkippedRejectedCount:  result.SkippedRejectedCount,
			Warnings:              result.Warnings,
			Imported:              make([]memoryOutput, 0, len(result.Imported)),
		}
		for _, details := range result.Imported {
			out.Imported = append(out.Imported, newMemoryOutput(details))
		}
		return nil, out, nil
	}
}

// rejectMemoriesBatch applies Reject to every supplied id and returns the
// per-id success / failure breakdown. The handler mirrors the single-id
// reject_memory tool and keeps the same partial-batch semantics as
// acceptMemoriesBatch.
func (s *Server) rejectMemoriesBatch() mcp.ToolHandlerFor[rejectMemoriesBatchInput, inboxBatchMemoryOutput] {
	return func(ctx context.Context, _ *mcp.CallToolRequest, input rejectMemoriesBatchInput) (*mcp.CallToolResult, inboxBatchMemoryOutput, error) {
		if len(input.MemoryIDs) == 0 {
			return nil, inboxBatchMemoryOutput{}, xerrors.Errorf("memory_ids must list at least one id")
		}
		out := inboxBatchMemoryOutput{Action: "reject"}
		for _, rawID := range input.MemoryIDs {
			trimmed := strings.TrimSpace(rawID)
			if trimmed == "" {
				continue
			}
			memoryID, err := types.MemoryIDOf(trimmed)
			if err != nil {
				out.Failures = append(out.Failures, inboxBatchMemoryFailureOutput{MemoryID: trimmed, Error: err.Error()})
				continue
			}
			details, err := s.memory.Reject(ctx, memoryID)
			if err != nil {
				out.Failures = append(out.Failures, inboxBatchMemoryFailureOutput{MemoryID: trimmed, Error: err.Error()})
				continue
			}
			out.Processed = append(out.Processed, newMemoryOutput(details))
		}
		return nil, out, nil
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
		expiresAtOptional := types.None[time.Time]()
		if !expiresAt.IsZero() {
			expiresAtOptional = types.Some(expiresAt)
		}
		details, err := s.memory.Expire(ctx, memoryID, expiresAtOptional)
		if err != nil {
			return nil, memoryOutput{}, xerrors.Errorf("failed to expire memory: %w", err)
		}
		return nil, newMemoryOutput(details), nil
	}
}

func (s *Server) setMemoryValidity() mcp.ToolHandlerFor[setMemoryValidityInput, memoryOutput] {
	return func(ctx context.Context, _ *mcp.CallToolRequest, input setMemoryValidityInput) (*mcp.CallToolResult, memoryOutput, error) {
		memoryID, err := types.MemoryIDOf(strings.TrimSpace(input.MemoryID))
		if err != nil {
			return nil, memoryOutput{}, xerrors.Errorf("failed to resolve memory_id: %w", err)
		}
		validFromOptional := types.None[time.Time]()
		if strings.TrimSpace(input.ValidFrom) != "" {
			validFrom, err := parseFlexibleTime(input.ValidFrom, false)
			if err != nil {
				return nil, memoryOutput{}, xerrors.Errorf("failed to resolve valid_from: %w", err)
			}
			validFromOptional = types.Some(validFrom)
		}
		validToOptional := types.None[time.Time]()
		if strings.TrimSpace(input.ValidTo) != "" {
			validTo, err := parseFlexibleTime(input.ValidTo, false)
			if err != nil {
				return nil, memoryOutput{}, xerrors.Errorf("failed to resolve valid_to: %w", err)
			}
			validToOptional = types.Some(validTo)
		}
		details, err := s.memory.SetValidity(ctx, memoryID, validFromOptional, validToOptional, input.ClearValidTo)
		if err != nil {
			return nil, memoryOutput{}, xerrors.Errorf("failed to set memory validity: %w", err)
		}
		return nil, newMemoryOutput(details), nil
	}
}

func buildContextPackCriteria(sessionID string, workspace string, recentCommandsLimit *int, memoryLimit *int, preset apptypes.MemoryRetrievalPreset) apptypes.ContextPackCriteria {
	builder := apptypes.NewContextPackCriteriaBuilder().
		SessionID(types.SessionID(strings.TrimSpace(sessionID))).
		Workspace(types.Workspace(strings.TrimSpace(workspace)))
	if recentCommandsLimit != nil {
		builder.RecentCommandsLimit(*recentCommandsLimit)
	}
	if memoryLimit != nil {
		builder.MemoryLimit(*memoryLimit)
	}
	if preset != "" {
		builder.MemoryPreset(preset)
	}
	return builder.Build()
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
			ValidFrom:  summary.ValidFrom().UTC().Format(time.RFC3339Nano),
			ValidTo:    formatOptionalTime(summary.ValidTo()),
			CreatedAt:  summary.CreatedAt().UTC().Format(time.RFC3339Nano),
			UpdatedAt:  summary.UpdatedAt().UTC().Format(time.RFC3339Nano),
		})
	}
	return outputs
}

func newMemoryOutput(details apptypes.MemoryDetails) memoryOutput {
	summary := details.Summary()
	supersedes := ""
	if value, ok := summary.Supersedes().Value(); ok {
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
		ValidFrom:    summary.ValidFrom().UTC().Format(time.RFC3339Nano),
		ValidTo:      formatOptionalTime(summary.ValidTo()),
		CreatedAt:    summary.CreatedAt().UTC().Format(time.RFC3339Nano),
		UpdatedAt:    summary.UpdatedAt().UTC().Format(time.RFC3339Nano),
		EvidenceRefs: convertEvidenceRefs(details.EvidenceRefs()),
		ArtifactRefs: convertArtifactRefs(details.ArtifactRefs()),
	}
}

func newMemoryOutputFromSummary(summary apptypes.MemorySummary) memoryOutput {
	supersedes := ""
	if memoryID, ok := summary.Supersedes().Value(); ok {
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
		ValidFrom:  summary.ValidFrom().UTC().Format(time.RFC3339Nano),
		ValidTo:    formatOptionalTime(summary.ValidTo()),
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
	if timeValue, ok := value.Value(); ok {
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
		return types.None[types.Confidence](), nil
	}
	resolved, err := types.ConfidenceOf(strings.TrimSpace(value))
	if err != nil {
		return types.None[types.Confidence](), xerrors.Errorf("failed to resolve confidence: %w", err)
	}
	return types.Some(resolved), nil
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
		return types.MemoryType(""), nil, types.None[types.Confidence](), types.MemorySource(""), nil, nil, xerrors.Errorf("failed to resolve memory type: %w", err)
	}
	scope, err := parseSingleMemoryScope(workspace, agent, sessionFamily)
	if err != nil {
		return types.MemoryType(""), nil, types.None[types.Confidence](), types.MemorySource(""), nil, nil, err
	}
	resolvedConfidence, err := parseOptionalConfidence(confidence)
	if err != nil {
		return types.MemoryType(""), nil, types.None[types.Confidence](), types.MemorySource(""), nil, nil, err
	}
	resolvedSource, err := parseMemorySource(source)
	if err != nil {
		return types.MemoryType(""), nil, types.None[types.Confidence](), types.MemorySource(""), nil, nil, err
	}
	resolvedEvidenceRefs, err := parseEvidenceRefs(evidenceRefs)
	if err != nil {
		return types.MemoryType(""), nil, types.None[types.Confidence](), types.MemorySource(""), nil, nil, err
	}
	resolvedArtifactRefs, err := parseArtifactRefs(artifactRefs)
	if err != nil {
		return types.MemoryType(""), nil, types.None[types.Confidence](), types.MemorySource(""), nil, nil, err
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
			return types.MemoryType(""), nil, types.None[types.Confidence](), types.MemorySource(""), nil, nil, xerrors.Errorf("failed to resolve memory type: %w", err)
		}
		resolvedType = value
	}
	scope, err := parseOptionalMemoryScope(workspace, agent, sessionFamily)
	if err != nil {
		return types.MemoryType(""), nil, types.None[types.Confidence](), types.MemorySource(""), nil, nil, err
	}
	resolvedConfidence, err := parseOptionalConfidence(confidence)
	if err != nil {
		return types.MemoryType(""), nil, types.None[types.Confidence](), types.MemorySource(""), nil, nil, err
	}
	resolvedSource, err := parseMemorySource(source)
	if err != nil {
		return types.MemoryType(""), nil, types.None[types.Confidence](), types.MemorySource(""), nil, nil, err
	}
	resolvedEvidenceRefs, err := parseEvidenceRefs(evidenceRefs)
	if err != nil {
		return types.MemoryType(""), nil, types.None[types.Confidence](), types.MemorySource(""), nil, nil, err
	}
	resolvedArtifactRefs, err := parseArtifactRefs(artifactRefs)
	if err != nil {
		return types.MemoryType(""), nil, types.None[types.Confidence](), types.MemorySource(""), nil, nil, err
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
