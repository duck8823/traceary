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
		Name:        "manage_memory",
		Description: "Dispatch durable memory writes by action; reject and expire are destructive actions.",
		Annotations: &mcp.ToolAnnotations{DestructiveHint: boolPtr(true)},
	}, s.manageMemory())
	mcp.AddTool(server, &mcp.Tool{
		Name:        "query_memory",
		Description: "Dispatch durable memory reads by action: retrieve, export, pack, or scan_hygiene.",
		Annotations: &mcp.ToolAnnotations{ReadOnlyHint: true},
	}, s.queryMemory())
	mcp.AddTool(server, &mcp.Tool{
		Name:        "manage_session",
		Description: "Dispatch session lifecycle writes by action: start or end. action=end is destructive (closes the session).",
		Annotations: &mcp.ToolAnnotations{DestructiveHint: boolPtr(true)},
	}, s.manageSession())
	mcp.AddTool(server, &mcp.Tool{
		Name:        "session_status",
		Description: "Dispatch session status reads by action: active, latest, or handoff.",
		Annotations: &mcp.ToolAnnotations{ReadOnlyHint: true},
	}, s.sessionStatus())
	mcp.AddTool(server, &mcp.Tool{
		Name:        "record_event",
		Description: "Record a log or command audit event by type, returning one uniform event shape.",
		Annotations: &mcp.ToolAnnotations{DestructiveHint: boolPtr(false)},
	}, s.recordEvent())
	mcp.AddTool(server, &mcp.Tool{
		Name:        "list_events",
		Description: "List recent events, logs, audits, prompts, transcripts, and summaries.",
		Annotations: &mcp.ToolAnnotations{ReadOnlyHint: true},
	}, s.listEvents())
	mcp.AddTool(server, &mcp.Tool{
		Name:        "search",
		Description: "Search events, logs, audits, prompts, transcripts, and summaries by text, time, or workspace.",
		Annotations: &mcp.ToolAnnotations{ReadOnlyHint: true},
	}, s.search())
	mcp.AddTool(server, &mcp.Tool{
		Name:        "get_context",
		Description: "Get recent context events, logs, audits, prompts, transcripts, and summaries for a session or workspace.",
		Annotations: &mcp.ToolAnnotations{ReadOnlyHint: true},
	}, s.getContext())
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

func (s *Server) manageMemory() mcp.ToolHandlerFor[manageMemoryInput, any] {
	return func(ctx context.Context, req *mcp.CallToolRequest, input manageMemoryInput) (*mcp.CallToolResult, any, error) {
		action := strings.ToLower(strings.TrimSpace(input.Action))
		switch action {
		case "propose":
			if strings.TrimSpace(input.MemoryType) == "" || strings.TrimSpace(input.Fact) == "" {
				return nil, nil, xerrors.Errorf("manage_memory action propose requires type and fact")
			}
			_, out, err := s.proposeMemory()(ctx, req, proposeMemoryInput{MemoryType: input.MemoryType, Workspace: input.Workspace, Agent: input.Agent, SessionFamily: input.SessionFamily, Fact: input.Fact, Source: input.Source, EvidenceRefs: input.EvidenceRefs, ArtifactRefs: input.ArtifactRefs})
			return nil, out, err
		case "remember":
			if strings.TrimSpace(input.MemoryType) == "" || strings.TrimSpace(input.Fact) == "" {
				return nil, nil, xerrors.Errorf("manage_memory action remember requires type and fact")
			}
			_, out, err := s.rememberMemory()(ctx, req, rememberMemoryInput{MemoryType: input.MemoryType, Workspace: input.Workspace, Agent: input.Agent, SessionFamily: input.SessionFamily, Fact: input.Fact, Confidence: input.Confidence, Source: input.Source, EvidenceRefs: input.EvidenceRefs, ArtifactRefs: input.ArtifactRefs})
			return nil, out, err
		case "accept":
			ids := resolveManageMemoryIDs(input)
			if len(ids) == 0 {
				return nil, nil, xerrors.Errorf("manage_memory action accept requires ids or memory_id")
			}
			if len(ids) == 1 {
				_, out, err := s.acceptMemory()(ctx, req, acceptMemoryInput{MemoryID: ids[0], Confidence: input.Confidence})
				return nil, out, err
			}
			_, out, err := s.acceptMemoriesBatch()(ctx, req, acceptMemoriesBatchInput{MemoryIDs: ids, Confidence: input.Confidence})
			return nil, out, err
		case "reject":
			ids := resolveManageMemoryIDs(input)
			if len(ids) == 0 {
				return nil, nil, xerrors.Errorf("manage_memory action reject requires ids or memory_id")
			}
			if len(ids) == 1 {
				_, out, err := s.rejectMemory()(ctx, req, rejectMemoryInput{MemoryID: ids[0]})
				return nil, out, err
			}
			_, out, err := s.rejectMemoriesBatch()(ctx, req, rejectMemoriesBatchInput{MemoryIDs: ids})
			return nil, out, err
		case "expire":
			id := strings.TrimSpace(input.MemoryID)
			if id == "" {
				ids := resolveManageMemoryIDs(input)
				if len(ids) > 0 {
					id = strings.TrimSpace(ids[0])
				}
			}
			if id == "" {
				return nil, nil, xerrors.Errorf("manage_memory action expire requires memory_id or ids")
			}
			_, out, err := s.expireMemory()(ctx, req, expireMemoryInput{MemoryID: id, ExpiresAt: input.ExpiresAt})
			return nil, out, err
		case "supersede":
			if strings.TrimSpace(input.TargetID) == "" || strings.TrimSpace(input.Fact) == "" {
				return nil, nil, xerrors.Errorf("manage_memory action supersede requires target_id and fact")
			}
			_, out, err := s.supersedeMemory()(ctx, req, supersedeMemoryInput{MemoryID: input.TargetID, MemoryType: input.MemoryType, Workspace: input.Workspace, Agent: input.Agent, SessionFamily: input.SessionFamily, Fact: input.Fact, Confidence: input.Confidence, Source: input.Source, EvidenceRefs: input.EvidenceRefs, ArtifactRefs: input.ArtifactRefs, ValidFrom: input.ValidFrom, ValidTo: input.ValidTo})
			return nil, out, err
		case "set_validity":
			id := strings.TrimSpace(input.MemoryID)
			if id == "" {
				ids := resolveManageMemoryIDs(input)
				if len(ids) > 0 {
					id = strings.TrimSpace(ids[0])
				}
			}
			if id == "" || (strings.TrimSpace(input.ValidFrom) == "" && strings.TrimSpace(input.ValidTo) == "" && !input.ClearValidTo) {
				return nil, nil, xerrors.Errorf("manage_memory action set_validity requires memory_id plus valid_from and/or valid_to")
			}
			_, out, err := s.setMemoryValidity()(ctx, req, setMemoryValidityInput{MemoryID: id, ValidFrom: input.ValidFrom, ValidTo: input.ValidTo, ClearValidTo: input.ClearValidTo})
			return nil, out, err
		case "import_instructions":
			if strings.TrimSpace(input.Source) == "" || (strings.TrimSpace(input.Path) == "" && strings.TrimSpace(input.Markdown) == "") {
				return nil, nil, xerrors.Errorf("manage_memory action import_instructions requires source and exactly one of path or markdown")
			}
			_, out, err := s.importMemoryInstructions()(ctx, req, importMemoryInstructionsInput{Source: input.Source, Path: input.Path, Markdown: input.Markdown, Workspace: input.Workspace})
			return nil, out, err
		default:
			return nil, nil, xerrors.Errorf("manage_memory action must be one of propose, remember, accept, reject, expire, supersede, set_validity, import_instructions")
		}
	}
}

func resolveManageMemoryIDs(input manageMemoryInput) []string {
	if input.IDs != nil {
		switch ids := input.IDs.(type) {
		case string:
			return []string{ids}
		case []string:
			return ids
		case []any:
			resolved := make([]string, 0, len(ids))
			for _, raw := range ids {
				value, ok := raw.(string)
				if !ok {
					resolved = append(resolved, "")
					continue
				}
				resolved = append(resolved, value)
			}
			return resolved
		}
	}
	if strings.TrimSpace(input.MemoryID) != "" {
		return []string{input.MemoryID}
	}
	return nil
}

func (s *Server) queryMemory() mcp.ToolHandlerFor[queryMemoryInput, any] {
	return func(ctx context.Context, req *mcp.CallToolRequest, input queryMemoryInput) (*mcp.CallToolResult, any, error) {
		action := strings.ToLower(strings.TrimSpace(input.Action))
		switch action {
		case "retrieve":
			_, out, err := s.retrieveMemories()(ctx, req, retrieveMemoriesInput{MemoryID: input.MemoryID, Query: input.Query, Workspace: input.Workspace, Agent: input.Agent, SessionFamily: input.SessionFamily, Statuses: input.Statuses, MemoryTypes: input.MemoryTypes, Limit: input.Limit, Offset: input.Offset, AsOf: input.AsOf, IncludeExpired: input.IncludeExpired, Preset: input.Preset})
			return nil, out, err
		case "export":
			if strings.TrimSpace(input.Target) == "" {
				return nil, nil, xerrors.Errorf("query_memory action export requires target")
			}
			_, out, err := s.exportMemories()(ctx, req, exportMemoriesInput{Target: input.Target, Workspace: input.Workspace})
			return nil, out, err
		case "pack":
			_, out, err := s.memoryPack()(ctx, req, memoryPackInput{SessionID: input.SessionID, Workspace: input.Workspace, RecentCommandsLimit: input.RecentCommandsLimit, MemoryLimit: input.MemoryLimit, Preset: input.Preset, AsOf: input.AsOf})
			return nil, out, err
		case "scan_hygiene":
			_, out, err := s.scanMemoryHygiene()(ctx, req, scanMemoryHygieneInput{Workspace: input.Workspace, ExpiryDays: input.ExpiryDays})
			return nil, out, err
		default:
			return nil, nil, xerrors.Errorf("query_memory action must be one of retrieve, export, pack, scan_hygiene")
		}
	}
}

func (s *Server) manageSession() mcp.ToolHandlerFor[sessionActionInput, sessionEventOutput] {
	return func(ctx context.Context, req *mcp.CallToolRequest, input sessionActionInput) (*mcp.CallToolResult, sessionEventOutput, error) {
		switch strings.ToLower(strings.TrimSpace(input.Action)) {
		case "start":
			return s.startSession()(ctx, req, startSessionInput{Client: input.Client, Agent: input.Agent, SessionID: input.SessionID, Workspace: input.Workspace})
		case "end":
			if strings.TrimSpace(input.SessionID) == "" {
				return nil, sessionEventOutput{}, xerrors.Errorf("manage_session action end requires session_id")
			}
			return s.endSession()(ctx, req, endSessionInput{Client: input.Client, Agent: input.Agent, SessionID: input.SessionID, Workspace: input.Workspace})
		default:
			return nil, sessionEventOutput{}, xerrors.Errorf("manage_session action must be one of start, end")
		}
	}
}

func (s *Server) sessionStatus() mcp.ToolHandlerFor[sessionActionInput, any] {
	return func(ctx context.Context, req *mcp.CallToolRequest, input sessionActionInput) (*mcp.CallToolResult, any, error) {
		switch strings.ToLower(strings.TrimSpace(input.Action)) {
		case "active":
			_, out, err := s.activeSession()(ctx, req, sessionLookupInput{Client: input.Client, Agent: input.Agent, Workspace: input.Workspace, AllowStale: input.AllowStale, StaleAfterSeconds: input.StaleAfterSeconds})
			return nil, out, err
		case "latest":
			_, out, err := s.latestSession()(ctx, req, sessionLookupInput{Client: input.Client, Agent: input.Agent, Workspace: input.Workspace, AllowStale: input.AllowStale, StaleAfterSeconds: input.StaleAfterSeconds})
			return nil, out, err
		case "handoff":
			_, out, err := s.sessionHandoff()(ctx, req, sessionHandoffInput{SessionID: input.SessionID, Workspace: input.Workspace, RecentCommandsLimit: input.RecentCommandsLimit, MemoryLimit: input.MemoryLimit, Preset: input.Preset, AsOf: input.AsOf})
			return nil, out, err
		default:
			return nil, nil, xerrors.Errorf("session_status action must be one of active, latest, handoff")
		}
	}
}

func (s *Server) recordEvent() mcp.ToolHandlerFor[recordEventInput, recordEventOutput] {
	return func(ctx context.Context, _ *mcp.CallToolRequest, input recordEventInput) (*mcp.CallToolResult, recordEventOutput, error) {
		switch strings.ToLower(strings.TrimSpace(input.Type)) {
		case "log":
			if strings.TrimSpace(input.Message) == "" {
				return nil, recordEventOutput{}, xerrors.Errorf("record_event type log requires message")
			}
			logCfg := apptypes.NewLogRedactionBuilder().ExtraRedactPatterns(s.extraRedactPatterns).Build()
			event, err := s.event.Log(ctx, input.Message, types.EventKind(strings.TrimSpace(input.Kind)), types.Client(resolveValue(input.Client, defaultClientValue)), types.Agent(resolveValue(input.Agent, defaultAgentValue)), types.SessionID(resolveValue(input.SessionID, defaultSessionValue)), types.Workspace(strings.TrimSpace(input.Workspace)), logCfg)
			if err != nil {
				return nil, recordEventOutput{}, xerrors.Errorf("failed to record log: %w", err)
			}
			blocks, _ := apptypes.DecodeCanonicalEnvelope(event.Body())
			return nil, recordEventOutput{EventID: event.EventID().String(), Type: "log", Kind: event.Kind().String(), Client: event.Client().String(), Agent: event.Agent().String(), SessionID: event.SessionID().String(), Workspace: event.Workspace().String(), Body: apptypes.ExtractPlainBody(event.Body()), BodyBlocks: blocks, SourceHook: event.SourceHook(), CreatedAt: event.CreatedAt().UTC().Format(time.RFC3339Nano)}, nil
		case "audit":
			if strings.TrimSpace(input.Command) == "" {
				return nil, recordEventOutput{}, xerrors.Errorf("record_event type audit requires command")
			}
			auditCfg := apptypes.NewAuditRedactionBuilder().ExtraRedactPatterns(s.extraRedactPatterns).Build()
			event, audit, err := s.event.Audit(ctx, input.Command, input.Input, input.Output, types.Client(resolveValue(input.Client, defaultClientValue)), types.Agent(resolveValue(input.Agent, defaultAgentValue)), types.SessionID(resolveValue(input.SessionID, defaultSessionValue)), types.Workspace(strings.TrimSpace(input.Workspace)), types.None[int](), auditCfg)
			if err != nil {
				return nil, recordEventOutput{}, xerrors.Errorf("failed to record command audit: %w", err)
			}
			return nil, recordEventOutput{EventID: event.EventID().String(), Type: "audit", Kind: event.Kind().String(), Client: event.Client().String(), Agent: event.Agent().String(), SessionID: event.SessionID().String(), Workspace: event.Workspace().String(), Body: apptypes.ExtractPlainBody(event.Body()), Command: audit.Command(), InputRedacted: audit.InputRedacted(), OutputRedacted: audit.OutputRedacted(), InputTruncated: audit.InputTruncated(), OutputTruncated: audit.OutputTruncated(), SourceHook: event.SourceHook(), CreatedAt: event.CreatedAt().UTC().Format(time.RFC3339Nano)}, nil
		default:
			return nil, recordEventOutput{}, xerrors.Errorf("record_event type must be one of log, audit")
		}
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

func (s *Server) listEvents() mcp.ToolHandlerFor[listEventsInput, eventsOutput] {
	return func(ctx context.Context, _ *mcp.CallToolRequest, input listEventsInput) (*mcp.CallToolResult, eventsOutput, error) {
		from, err := parseFlexibleTime(input.From, false)
		if err != nil {
			return nil, eventsOutput{}, xerrors.Errorf("failed to parse from: %w", err)
		}
		to, err := parseFlexibleTime(input.To, true)
		if err != nil {
			return nil, eventsOutput{}, xerrors.Errorf("failed to parse to: %w", err)
		}

		criteria := apptypes.NewEventListCriteriaBuilder(resolveLimit(input.Limit, defaultSearchLimit)).
			Offset(resolveOffset(input.Offset)).
			Kind(types.EventKind(strings.TrimSpace(input.Kind))).
			Client(types.Client(strings.TrimSpace(input.Client))).
			Agent(types.Agent(strings.TrimSpace(input.Agent))).
			SessionID(types.SessionID(strings.TrimSpace(input.SessionID))).
			Workspace(types.Workspace(strings.TrimSpace(input.Workspace))).
			SourceHook(strings.TrimSpace(input.SourceHook)).
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
		EventID:    event.EventID().String(),
		Kind:       event.Kind().String(),
		Client:     event.Client().String(),
		Agent:      event.Agent().String(),
		SessionID:  event.SessionID().String(),
		Workspace:  event.Workspace().String(),
		SourceHook: event.SourceHook(),
		CreatedAt:  event.CreatedAt().UTC().Format(time.RFC3339Nano),
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
			return nil, eventsOutput{}, xerrors.Errorf("failed to parse from: %w", err)
		}
		to, err := parseFlexibleTime(input.To, true)
		if err != nil {
			return nil, eventsOutput{}, xerrors.Errorf("failed to parse to: %w", err)
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

		// Search intentionally omits body_blocks so thinking content is
		// not re-exposed through this surface — #682 strips thinking
		// from the LIKE match, but the envelope is still attached to
		// the returned event and body_blocks would bypass the gate.
		return nil, eventsOutput{Events: convertEventsWithoutBlocks(events)}, nil
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

		// get_context also omits body_blocks for the same reason as
		// search: the canonical envelope would re-expose thinking
		// block text that other surfaces already strip.
		return nil, eventsOutput{Events: convertEventsWithoutBlocks(events)}, nil
	}
}

func (s *Server) sessionHandoff() mcp.ToolHandlerFor[sessionHandoffInput, sessionHandoffOutput] {
	return func(ctx context.Context, _ *mcp.CallToolRequest, input sessionHandoffInput) (*mcp.CallToolResult, sessionHandoffOutput, error) {
		preset, err := apptypes.MemoryRetrievalPresetOf(input.Preset)
		if err != nil {
			return nil, sessionHandoffOutput{}, xerrors.Errorf("failed to parse preset: %w", err)
		}
		asOf, err := parseFlexibleTimeOptional(input.AsOf)
		if err != nil {
			return nil, sessionHandoffOutput{}, xerrors.Errorf("failed to parse as_of: %w", err)
		}
		result, err := s.context.Handoff(ctx, buildContextPackCriteria(
			input.SessionID,
			input.Workspace,
			input.RecentCommandsLimit,
			input.MemoryLimit,
			preset,
			asOf,
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
		asOf, err := parseFlexibleTimeOptional(input.AsOf)
		if err != nil {
			return nil, memoryPackOutput{}, xerrors.Errorf("failed to parse as_of: %w", err)
		}
		result, err := s.context.Handoff(ctx, buildContextPackCriteria(
			input.SessionID,
			input.Workspace,
			input.RecentCommandsLimit,
			input.MemoryLimit,
			preset,
			asOf,
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
			memoryID, err := types.MemoryIDFrom(memoryIDValue)
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
		memoryID, err := types.MemoryIDFrom(strings.TrimSpace(input.MemoryID))
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
		memoryID, err := types.MemoryIDFrom(strings.TrimSpace(input.MemoryID))
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
			memoryID, err := types.MemoryIDFrom(trimmed)
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
		if s.memory == nil {
			return nil, memoryHygieneOutput{}, xerrors.Errorf("memory usecase is not configured")
		}
		criteria := apptypes.MemoryHygieneScanCriteria{}
		if workspace := strings.TrimSpace(input.Workspace); workspace != "" {
			resolvedWorkspace, err := types.WorkspaceFrom(workspace)
			if err != nil {
				return nil, memoryHygieneOutput{}, xerrors.Errorf("failed to resolve workspace: %w", err)
			}
			criteria.Scopes = []types.MemoryScope{types.WorkspaceScopeOf(resolvedWorkspace)}
		}
		if input.ExpiryDays > 0 {
			criteria.StalenessThreshold = time.Duration(input.ExpiryDays) * 24 * time.Hour
		}
		result, err := s.memory.Scan(ctx, criteria)
		if err != nil {
			return nil, memoryHygieneOutput{}, xerrors.Errorf("failed to scan memory hygiene: %w", err)
		}
		out := memoryHygieneOutput{
			RedactionHitCount:             result.RedactionHitCount,
			ExpiryCandidateCount:          result.ExpiryCandidateCount,
			DuplicateCount:                result.DuplicateCount,
			SupersedeCandidateCount:       result.SupersedeCandidateCount,
			ValidityOverlapSupersedeCount: result.ValidityOverlapSupersedeCount,
			Suggestions:                   make([]memoryHygieneSuggestionOutput, 0, len(result.Suggestions)),
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
		if s.memory == nil {
			return nil, exportMemoriesOutput{}, xerrors.Errorf("memory usecase is not configured")
		}
		target, ok := apptypes.MemoryBridgeTargetOf(strings.ToLower(strings.TrimSpace(input.Target)))
		if !ok {
			return nil, exportMemoriesOutput{}, xerrors.Errorf("target must be one of claude / codex / gemini, got %q", input.Target)
		}
		criteria := apptypes.MemoryExportCriteria{Target: target}
		if workspace := strings.TrimSpace(input.Workspace); workspace != "" {
			resolvedWorkspace, err := types.WorkspaceFrom(workspace)
			if err != nil {
				return nil, exportMemoriesOutput{}, xerrors.Errorf("failed to resolve workspace: %w", err)
			}
			criteria.Scopes = []types.MemoryScope{types.WorkspaceScopeOf(resolvedWorkspace)}
		}
		result, err := s.memory.Export(ctx, criteria)
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
		if s.memory == nil {
			return nil, importMemoryInstructionsOutput{}, xerrors.Errorf("memory usecase is not configured")
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
			resolvedWorkspace, err := types.WorkspaceFrom(workspace)
			if err != nil {
				return nil, importMemoryInstructionsOutput{}, xerrors.Errorf("failed to resolve workspace: %w", err)
			}
			criteria.WorkspaceFallback = resolvedWorkspace
		}
		result, err := s.memory.ImportInstructions(ctx, criteria)
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
			memoryID, err := types.MemoryIDFrom(trimmed)
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
		memoryID, err := types.MemoryIDFrom(strings.TrimSpace(input.MemoryID))
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
		validFromOptional := types.None[time.Time]()
		if strings.TrimSpace(input.ValidFrom) != "" {
			validFrom, err := parseFlexibleTime(input.ValidFrom, false)
			if err != nil {
				return nil, memoryOutput{}, xerrors.Errorf("failed to parse valid_from: %w", err)
			}
			validFromOptional = types.Some(validFrom)
		}
		validToOptional := types.None[time.Time]()
		if strings.TrimSpace(input.ValidTo) != "" {
			validTo, err := parseFlexibleTime(input.ValidTo, false)
			if err != nil {
				return nil, memoryOutput{}, xerrors.Errorf("failed to parse valid_to: %w", err)
			}
			validToOptional = types.Some(validTo)
		}
		details, err := s.memory.Supersede(ctx, memoryID, memoryType, scope, input.Fact, confidence, source, evidenceRefs, artifactRefs, validFromOptional, validToOptional)
		if err != nil {
			return nil, memoryOutput{}, xerrors.Errorf("failed to supersede memory: %w", err)
		}
		return nil, newMemoryOutput(details), nil
	}
}

func (s *Server) expireMemory() mcp.ToolHandlerFor[expireMemoryInput, memoryOutput] {
	return func(ctx context.Context, _ *mcp.CallToolRequest, input expireMemoryInput) (*mcp.CallToolResult, memoryOutput, error) {
		memoryID, err := types.MemoryIDFrom(strings.TrimSpace(input.MemoryID))
		if err != nil {
			return nil, memoryOutput{}, xerrors.Errorf("failed to resolve memory_id: %w", err)
		}
		expiresAt, err := parseFlexibleTime(input.ExpiresAt, false)
		if err != nil {
			return nil, memoryOutput{}, xerrors.Errorf("failed to parse expires_at: %w", err)
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
		memoryID, err := types.MemoryIDFrom(strings.TrimSpace(input.MemoryID))
		if err != nil {
			return nil, memoryOutput{}, xerrors.Errorf("failed to resolve memory_id: %w", err)
		}
		validFromOptional := types.None[time.Time]()
		if strings.TrimSpace(input.ValidFrom) != "" {
			validFrom, err := parseFlexibleTime(input.ValidFrom, false)
			if err != nil {
				return nil, memoryOutput{}, xerrors.Errorf("failed to parse valid_from: %w", err)
			}
			validFromOptional = types.Some(validFrom)
		}
		validToOptional := types.None[time.Time]()
		if strings.TrimSpace(input.ValidTo) != "" {
			validTo, err := parseFlexibleTime(input.ValidTo, false)
			if err != nil {
				return nil, memoryOutput{}, xerrors.Errorf("failed to parse valid_to: %w", err)
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

func buildContextPackCriteria(sessionID string, workspace string, recentCommandsLimit *int, memoryLimit *int, preset apptypes.MemoryRetrievalPreset, asOf types.Optional[time.Time]) apptypes.ContextPackCriteria {
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
	if _, ok := asOf.Value(); ok {
		builder.MemoryAsOf(asOf)
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
			Supersedes: formatOptionalMemoryIDPtr(summary.Supersedes()),
			ExpiresAt:  formatOptionalTimePtr(summary.ExpiresAt()),
			ValidFrom:  summary.ValidFrom().UTC().Format(time.RFC3339Nano),
			ValidTo:    formatOptionalTimePtr(summary.ValidTo()),
			CreatedAt:  summary.CreatedAt().UTC().Format(time.RFC3339Nano),
			UpdatedAt:  summary.UpdatedAt().UTC().Format(time.RFC3339Nano),
		})
	}
	return outputs
}

func newMemoryOutput(details apptypes.MemoryDetails) memoryOutput {
	summary := details.Summary()

	return memoryOutput{
		MemoryID:     summary.MemoryID().String(),
		Type:         summary.MemoryType().String(),
		ScopeKind:    summary.Scope().Kind().String(),
		ScopeValue:   summary.Scope().Key(),
		Fact:         summary.Fact(),
		Status:       summary.Status().String(),
		Confidence:   summary.Confidence().String(),
		Source:       summary.Source().String(),
		Supersedes:   formatOptionalMemoryIDPtr(summary.Supersedes()),
		ExpiresAt:    formatOptionalTimePtr(summary.ExpiresAt()),
		ValidFrom:    summary.ValidFrom().UTC().Format(time.RFC3339Nano),
		ValidTo:      formatOptionalTimePtr(summary.ValidTo()),
		CreatedAt:    summary.CreatedAt().UTC().Format(time.RFC3339Nano),
		UpdatedAt:    summary.UpdatedAt().UTC().Format(time.RFC3339Nano),
		EvidenceRefs: convertEvidenceRefs(details.EvidenceRefs()),
		ArtifactRefs: convertArtifactRefs(details.ArtifactRefs()),
	}
}

func newMemoryOutputFromSummary(summary apptypes.MemorySummary) memoryOutput {
	return memoryOutput{
		MemoryID:   summary.MemoryID().String(),
		Type:       summary.MemoryType().String(),
		ScopeKind:  summary.Scope().Kind().String(),
		ScopeValue: summary.Scope().Key(),
		Fact:       summary.Fact(),
		Status:     summary.Status().String(),
		Confidence: summary.Confidence().String(),
		Source:     summary.Source().String(),
		Supersedes: formatOptionalMemoryIDPtr(summary.Supersedes()),
		ExpiresAt:  formatOptionalTimePtr(summary.ExpiresAt()),
		ValidFrom:  summary.ValidFrom().UTC().Format(time.RFC3339Nano),
		ValidTo:    formatOptionalTimePtr(summary.ValidTo()),
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

// formatOptionalTimePtr renders an Optional[time.Time] as `*string`:
// nil when absent, otherwise RFC3339Nano UTC. This lets MCP memory
// outputs carry JSON `null` for unset bounds — matching the CLI
// memorySummaryOutput shape (#628).
func formatOptionalTimePtr(value types.Optional[time.Time]) *string {
	timeValue, ok := value.Value()
	if !ok {
		return nil
	}
	formatted := timeValue.UTC().Format(time.RFC3339Nano)
	return &formatted
}

// formatOptionalMemoryIDPtr renders an Optional[MemoryID] as
// `*string` so absent values emit `null` / are omitted rather than
// surfacing as an empty string distinguishable from "no predecessor".
func formatOptionalMemoryIDPtr(value types.Optional[types.MemoryID]) *string {
	memoryID, ok := value.Value()
	if !ok {
		return nil
	}
	id := memoryID.String()
	return &id
}

func parseMemoryScopes(workspace string, agent string, sessionFamily string) ([]types.MemoryScope, error) {
	scopes := make([]types.MemoryScope, 0, 3)
	if trimmedWorkspace := strings.TrimSpace(workspace); trimmedWorkspace != "" {
		resolvedWorkspace, err := types.WorkspaceFrom(trimmedWorkspace)
		if err != nil {
			return nil, xerrors.Errorf("failed to resolve workspace scope: %w", err)
		}
		scopes = append(scopes, types.WorkspaceScopeOf(resolvedWorkspace))
	}
	if trimmedAgent := strings.TrimSpace(agent); trimmedAgent != "" {
		resolvedAgent, err := types.AgentFrom(trimmedAgent)
		if err != nil {
			return nil, xerrors.Errorf("failed to resolve agent scope: %w", err)
		}
		scopes = append(scopes, types.AgentScopeOf(resolvedAgent))
	}
	if trimmedSessionFamily := strings.TrimSpace(sessionFamily); trimmedSessionFamily != "" {
		resolvedSessionID, err := types.SessionIDFrom(trimmedSessionFamily)
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
		resolved, err := types.MemoryStatusFrom(strings.TrimSpace(value))
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
		resolved, err := types.MemoryTypeFrom(strings.TrimSpace(value))
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
	resolved, err := types.ConfidenceFrom(strings.TrimSpace(value))
	if err != nil {
		return types.None[types.Confidence](), xerrors.Errorf("failed to resolve confidence: %w", err)
	}
	return types.Some(resolved), nil
}

func parseMemorySource(value string) (types.MemorySource, error) {
	if strings.TrimSpace(value) == "" {
		return types.MemorySource(""), nil
	}
	resolved, err := types.MemorySourceFrom(strings.TrimSpace(value))
	if err != nil {
		return types.MemorySource(""), xerrors.Errorf("failed to resolve memory source: %w", err)
	}
	return resolved, nil
}

func parseEvidenceRefs(refs []memoryRefInput) ([]types.EvidenceRef, error) {
	outputs := make([]types.EvidenceRef, 0, len(refs))
	for _, ref := range refs {
		kind, err := types.EvidenceRefKindFrom(strings.TrimSpace(ref.Kind))
		if err != nil {
			return nil, xerrors.Errorf("failed to resolve evidence ref kind: %w", err)
		}
		resolved, err := types.EvidenceRefFrom(kind, strings.TrimSpace(ref.Value))
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
		kind, err := types.ArtifactRefKindFrom(strings.TrimSpace(ref.Kind))
		if err != nil {
			return nil, xerrors.Errorf("failed to resolve artifact ref kind: %w", err)
		}
		resolved, err := types.ArtifactRefFrom(kind, strings.TrimSpace(ref.Value))
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
	resolvedType, err := types.MemoryTypeFrom(strings.TrimSpace(memoryType))
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
		value, err := types.MemoryTypeFrom(strings.TrimSpace(memoryType))
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

// parseFlexibleTimeOptional returns None when the input is empty and
// Some(time) otherwise, surfacing parse failures as errors. Callers
// that want to distinguish "not supplied" from "unset" use this over
// parseFlexibleTime, which returns a zero time.Time in either case.
func parseFlexibleTimeOptional(value string) (types.Optional[time.Time], error) {
	if strings.TrimSpace(value) == "" {
		return types.None[time.Time](), nil
	}
	parsed, err := parseFlexibleTime(value, false)
	if err != nil {
		return types.None[time.Time](), err
	}
	return types.Some(parsed), nil
}

// convertEvents serializes events for MCP list_events. It includes
// body_blocks (the canonical envelope form, thinking blocks and all)
// because list_events is the primary surface where programmatic
// consumers round-trip transcript structure.
//
// search / get_context deliberately use convertEventsWithoutBlocks so
// their responses do not re-expose thinking-block text through the
// MCP layer — #682 filters thinking out of the LIKE match, but the
// full envelope is still attached to the returned event, and dumping
// it via body_blocks would undo that protection on those surfaces.
func convertEvents(events []*model.Event) []eventOutput {
	return convertEventsInternal(events, true)
}

// convertEventsWithoutBlocks serializes events for MCP search /
// get_context without the body_blocks field so thinking content
// cannot leak through those surfaces.
func convertEventsWithoutBlocks(events []*model.Event) []eventOutput {
	return convertEventsInternal(events, false)
}

func convertEventsInternal(events []*model.Event, includeBlocks bool) []eventOutput {
	outputs := make([]eventOutput, 0, len(events))
	for _, event := range events {
		var blocks []apptypes.EventBodyBlock
		if includeBlocks {
			blocks, _ = apptypes.DecodeCanonicalEnvelope(event.Body())
		}
		outputs = append(outputs, eventOutput{
			EventID:    event.EventID().String(),
			Kind:       event.Kind().String(),
			Client:     event.Client().String(),
			Agent:      event.Agent().String(),
			SessionID:  event.SessionID().String(),
			Workspace:  event.Workspace().String(),
			Body:       apptypes.ExtractPlainBody(event.Body()),
			BodyBlocks: blocks,
			SourceHook: event.SourceHook(),
			CreatedAt:  event.CreatedAt().UTC().Format(time.RFC3339Nano),
		})
	}

	return outputs
}

func boolPtr(value bool) *bool {
	return &value
}
