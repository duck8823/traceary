package mcpserver

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"golang.org/x/xerrors"

	"github.com/duck8823/traceary/application/queryservice"
	"github.com/duck8823/traceary/application/usecase"
	"github.com/duck8823/traceary/domain/model"
	"github.com/duck8823/traceary/domain/types"
	"github.com/duck8823/traceary/presentation"
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
	serverName                    string
	serverVersion                 string
	extraRedactPatterns           []string
	initializeStoreUsecase        usecase.InitializeStoreUsecase
	recordLogUsecase              usecase.RecordLogUsecase
	recordSessionBoundaryUsecase  usecase.RecordSessionBoundaryUsecase
	recordCommandAuditUsecase     usecase.RecordCommandAuditUsecase
	findLatestSessionQueryService queryservice.FindLatestSessionQueryService
	listEventsQueryService        queryservice.ListRecentEventsQueryService
	searchEventsQueryService      queryservice.SearchEventsQueryService
	getContextQueryService        queryservice.GetContextQueryService
	listSessionsQueryService      queryservice.ListSessionsQueryService
}

// NewServer creates a new MCP server.
func NewServer(
	serverVersion string,
	initializeStoreUsecase usecase.InitializeStoreUsecase,
	recordLogUsecase usecase.RecordLogUsecase,
	recordSessionBoundaryUsecase usecase.RecordSessionBoundaryUsecase,
	recordCommandAuditUsecase usecase.RecordCommandAuditUsecase,
	findLatestSessionQueryService queryservice.FindLatestSessionQueryService,
	listEventsQueryService queryservice.ListRecentEventsQueryService,
	searchEventsQueryService queryservice.SearchEventsQueryService,
	getContextQueryService queryservice.GetContextQueryService,
	listSessionsQueryService queryservice.ListSessionsQueryService,
) (*Server, error) {
	if initializeStoreUsecase == nil {
		return nil, xerrors.Errorf("initialize store usecase is not configured")
	}
	if recordLogUsecase == nil {
		return nil, xerrors.Errorf("record log usecase is not configured")
	}
	if recordSessionBoundaryUsecase == nil {
		return nil, xerrors.Errorf("record session boundary usecase is not configured")
	}
	if recordCommandAuditUsecase == nil {
		return nil, xerrors.Errorf("record command audit usecase is not configured")
	}
	if findLatestSessionQueryService == nil {
		return nil, xerrors.Errorf("find latest session query service is not configured")
	}
	if listEventsQueryService == nil {
		return nil, xerrors.Errorf("list recent events query service is not configured")
	}
	if searchEventsQueryService == nil {
		return nil, xerrors.Errorf("search events query service is not configured")
	}
	if getContextQueryService == nil {
		return nil, xerrors.Errorf("get context query service is not configured")
	}

	trimmedVersion := strings.TrimSpace(serverVersion)
	if trimmedVersion == "" {
		trimmedVersion = defaultServerVersion
	}

	config := presentation.LoadConfig()

	return &Server{
		serverName:                    defaultServerName,
		serverVersion:                 trimmedVersion,
		extraRedactPatterns:           config.Redact.ExtraPatterns,
		initializeStoreUsecase:        initializeStoreUsecase,
		recordLogUsecase:              recordLogUsecase,
		recordSessionBoundaryUsecase:  recordSessionBoundaryUsecase,
		recordCommandAuditUsecase:     recordCommandAuditUsecase,
		findLatestSessionQueryService: findLatestSessionQueryService,
		listEventsQueryService:        listEventsQueryService,
		searchEventsQueryService:      searchEventsQueryService,
		getContextQueryService:        getContextQueryService,
		listSessionsQueryService:      listSessionsQueryService,
	}, nil
}

// Build creates an MCP server backed by an initialized store.
func (s *Server) Build(ctx context.Context, dbPath string) (*mcp.Server, error) {
	trimmedDBPath := strings.TrimSpace(dbPath)
	if trimmedDBPath == "" {
		return nil, xerrors.Errorf("DB path must not be empty")
	}
	if err := s.initializeStoreUsecase.Run(ctx, trimmedDBPath); err != nil {
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
	}, s.addLog(trimmedDBPath))
	mcp.AddTool(server, &mcp.Tool{
		Name:        "start_session",
		Description: "Add a session_started event to Traceary",
		Annotations: &mcp.ToolAnnotations{DestructiveHint: boolPtr(false)},
	}, s.startSession(trimmedDBPath))
	mcp.AddTool(server, &mcp.Tool{
		Name:        "end_session",
		Description: "Add a session_ended event to Traceary",
		Annotations: &mcp.ToolAnnotations{DestructiveHint: boolPtr(false)},
	}, s.endSession(trimmedDBPath))
	mcp.AddTool(server, &mcp.Tool{
		Name:        "latest_session",
		Description: "Return the latest session matching the filters",
		Annotations: &mcp.ToolAnnotations{ReadOnlyHint: true},
	}, s.latestSession(trimmedDBPath))
	mcp.AddTool(server, &mcp.Tool{
		Name:        "active_session",
		Description: "Return the active session matching the filters",
		Annotations: &mcp.ToolAnnotations{ReadOnlyHint: true},
	}, s.activeSession(trimmedDBPath))
	mcp.AddTool(server, &mcp.Tool{
		Name:        "list_events",
		Description: "List recent events in Traceary",
		Annotations: &mcp.ToolAnnotations{ReadOnlyHint: true},
	}, s.listEvents(trimmedDBPath))
	mcp.AddTool(server, &mcp.Tool{
		Name:        "add_audit",
		Description: "Add a command audit event to Traceary",
		Annotations: &mcp.ToolAnnotations{DestructiveHint: boolPtr(false)},
	}, s.addAudit(trimmedDBPath))
	mcp.AddTool(server, &mcp.Tool{
		Name:        "search",
		Description: "Search events in Traceary",
		Annotations: &mcp.ToolAnnotations{ReadOnlyHint: true},
	}, s.search(trimmedDBPath))
	mcp.AddTool(server, &mcp.Tool{
		Name:        "get_context",
		Description: "Get recent context events matching the filters",
		Annotations: &mcp.ToolAnnotations{ReadOnlyHint: true},
	}, s.getContext(trimmedDBPath))
	mcp.AddTool(server, &mcp.Tool{
		Name:        "session_handoff",
		Description: "Get a concise session summary for handoff or context resumption",
		Annotations: &mcp.ToolAnnotations{ReadOnlyHint: true},
	}, s.sessionHandoff(trimmedDBPath))

	return server, nil
}

// Run starts the MCP server over stdio transport.
func (s *Server) Run(ctx context.Context, dbPath string) error {
	server, err := s.Build(ctx, dbPath)
	if err != nil {
		return xerrors.Errorf("failed to build MCP server: %w", err)
	}
	if err := server.Run(ctx, &mcp.StdioTransport{}); err != nil {
		return xerrors.Errorf("failed to run MCP server: %w", err)
	}

	return nil
}

type addLogInput struct {
	Message   string `json:"message" jsonschema:"log body to record"`
	Client    string `json:"client,omitempty" jsonschema:"recording channel (default: mcp)"`
	Agent     string `json:"agent,omitempty" jsonschema:"actor name (default: manual)"`
	SessionID string `json:"session_id,omitempty" jsonschema:"session ID (default: default)"`
	Repo      string `json:"repo,omitempty" jsonschema:"auxiliary work context identifier"`
}

type addLogOutput struct {
	EventID   string `json:"event_id" jsonschema:"saved event ID"`
	Kind      string `json:"kind" jsonschema:"event kind"`
	Client    string `json:"client" jsonschema:"recording channel"`
	Agent     string `json:"agent" jsonschema:"actor"`
	SessionID string `json:"session_id" jsonschema:"session identifier"`
	Repo      string `json:"repo,omitempty" jsonschema:"auxiliary work context identifier"`
	Body      string `json:"body" jsonschema:"event body"`
	CreatedAt string `json:"created_at" jsonschema:"event timestamp (RFC3339Nano)"`
}

type sessionBoundaryInput struct {
	Client    string `json:"client,omitempty" jsonschema:"recording channel (start default: mcp; end falls back to the start event attribution)"`
	Agent     string `json:"agent,omitempty" jsonschema:"actor name (start default: manual; end falls back to the start event attribution)"`
	SessionID string `json:"session_id,omitempty" jsonschema:"session identifier (start auto-generates when omitted; end requires it)"`
	Repo      string `json:"repo,omitempty" jsonschema:"auxiliary work context identifier"`
}

type sessionLookupInput struct {
	Client            string `json:"client,omitempty" jsonschema:"filter by recording channel"`
	Agent             string `json:"agent,omitempty" jsonschema:"filter by actor"`
	Repo              string `json:"repo,omitempty" jsonschema:"filter by auxiliary work context identifier"`
	AllowStale        bool   `json:"allow_stale,omitempty" jsonschema:"allow stale active sessions"`
	StaleAfterSeconds int    `json:"stale_after_seconds,omitempty" jsonschema:"mark active sessions older than this many seconds as stale (0 or omitted: 86400)"`
}

type sessionEventOutput struct {
	EventID   string `json:"event_id" jsonschema:"saved or referenced event ID"`
	Kind      string `json:"kind" jsonschema:"event kind"`
	Client    string `json:"client" jsonschema:"recording channel"`
	Agent     string `json:"agent" jsonschema:"actor"`
	SessionID string `json:"session_id" jsonschema:"session identifier"`
	Repo      string `json:"repo,omitempty" jsonschema:"auxiliary work context identifier"`
	CreatedAt string `json:"created_at" jsonschema:"event timestamp (RFC3339Nano)"`
}

type addAuditInput struct {
	Command   string `json:"command" jsonschema:"executed command"`
	Input     string `json:"input,omitempty" jsonschema:"command input"`
	Output    string `json:"output,omitempty" jsonschema:"command output"`
	Client    string `json:"client,omitempty" jsonschema:"recording channel (default: mcp)"`
	Agent     string `json:"agent,omitempty" jsonschema:"actor name (default: manual)"`
	SessionID string `json:"session_id,omitempty" jsonschema:"session ID (default: default)"`
	Repo      string `json:"repo,omitempty" jsonschema:"auxiliary work context identifier"`
}

type addAuditOutput struct {
	EventID         string `json:"event_id" jsonschema:"saved event ID"`
	Kind            string `json:"kind" jsonschema:"event kind"`
	SessionID       string `json:"session_id" jsonschema:"session identifier"`
	Repo            string `json:"repo,omitempty" jsonschema:"auxiliary work context identifier"`
	Command         string `json:"command" jsonschema:"executed command"`
	InputRedacted   bool   `json:"input_redacted" jsonschema:"whether input was redacted"`
	OutputRedacted  bool   `json:"output_redacted" jsonschema:"whether output was redacted"`
	InputTruncated  bool   `json:"input_truncated" jsonschema:"whether input was truncated"`
	OutputTruncated bool   `json:"output_truncated" jsonschema:"whether output was truncated"`
	CreatedAt       string `json:"created_at" jsonschema:"event timestamp (RFC3339Nano)"`
}

type listEventsInput struct {
	Limit  int `json:"limit,omitempty" jsonschema:"result limit (default: 20)"`
	Offset int `json:"offset,omitempty" jsonschema:"offset from the newest result (default: 0)"`
}

type searchInput struct {
	Query string `json:"query" jsonschema:"search query"`
	Repo  string `json:"repo,omitempty" jsonschema:"work context filter"`
	From  string `json:"from,omitempty" jsonschema:"start time (YYYY-MM-DD or RFC3339)"`
	To    string `json:"to,omitempty" jsonschema:"end time (YYYY-MM-DD or RFC3339)"`
	Limit int    `json:"limit,omitempty" jsonschema:"result limit (default: 20)"`
}

type getContextInput struct {
	Repo      string `json:"repo,omitempty" jsonschema:"work context filter"`
	SessionID string `json:"session_id,omitempty" jsonschema:"session identifier filter"`
	Limit     int    `json:"limit,omitempty" jsonschema:"result limit (default: 20)"`
}

type eventsOutput struct {
	Events []eventOutput `json:"events" jsonschema:"events matching the filters"`
}

type eventOutput struct {
	EventID   string `json:"event_id" jsonschema:"event ID"`
	Kind      string `json:"kind" jsonschema:"event kind"`
	Client    string `json:"client" jsonschema:"recording channel"`
	Agent     string `json:"agent" jsonschema:"actor"`
	SessionID string `json:"session_id" jsonschema:"session identifier"`
	Repo      string `json:"repo,omitempty" jsonschema:"auxiliary work context identifier"`
	Body      string `json:"body" jsonschema:"event body"`
	CreatedAt string `json:"created_at" jsonschema:"event timestamp (RFC3339Nano)"`
}

func (s *Server) addLog(dbPath string) mcp.ToolHandlerFor[addLogInput, addLogOutput] {
	return func(ctx context.Context, _ *mcp.CallToolRequest, input addLogInput) (*mcp.CallToolResult, addLogOutput, error) {
		event, err := s.recordLogUsecase.Run(ctx, usecase.RecordLogInput{
			DBPath:    dbPath,
			Message:   input.Message,
			Client:    resolveValue(input.Client, defaultClientValue),
			Agent:     resolveValue(input.Agent, defaultAgentValue),
			SessionID: resolveValue(input.SessionID, defaultSessionValue),
			Repo:      strings.TrimSpace(input.Repo),
		})
		if err != nil {
			return nil, addLogOutput{}, xerrors.Errorf("failed to record log: %w", err)
		}

		return nil, addLogOutput{
			EventID:   event.EventID().String(),
			Kind:      event.Kind().String(),
			Client:    event.Client(),
			Agent:     event.Agent().String(),
			SessionID: event.SessionID().String(),
			Repo:      event.Repo(),
			Body:      event.Body(),
			CreatedAt: event.CreatedAt().UTC().Format(time.RFC3339Nano),
		}, nil
	}
}

func (s *Server) startSession(dbPath string) mcp.ToolHandlerFor[sessionBoundaryInput, sessionEventOutput] {
	return func(ctx context.Context, _ *mcp.CallToolRequest, input sessionBoundaryInput) (*mcp.CallToolResult, sessionEventOutput, error) {
		event, err := s.recordSessionBoundaryUsecase.Run(ctx, usecase.RecordSessionBoundaryInput{
			DBPath:        dbPath,
			Client:        strings.TrimSpace(input.Client),
			DefaultClient: defaultClientValue,
			Agent:         strings.TrimSpace(input.Agent),
			DefaultAgent:  defaultAgentValue,
			SessionID:     strings.TrimSpace(input.SessionID),
			Repo:          strings.TrimSpace(input.Repo),
			DefaultRepo:   "",
			Kind:          types.EventKindSessionStarted,
		})
		if err != nil {
			return nil, sessionEventOutput{}, xerrors.Errorf("failed to record session start: %w", err)
		}

		return nil, newSessionEventOutput(event), nil
	}
}

func (s *Server) endSession(dbPath string) mcp.ToolHandlerFor[sessionBoundaryInput, sessionEventOutput] {
	return func(ctx context.Context, _ *mcp.CallToolRequest, input sessionBoundaryInput) (*mcp.CallToolResult, sessionEventOutput, error) {
		sessionID := strings.TrimSpace(input.SessionID)
		if sessionID == "" {
			return nil, sessionEventOutput{}, xerrors.Errorf("session_id is required")
		}

		event, err := s.recordSessionBoundaryUsecase.Run(ctx, usecase.RecordSessionBoundaryInput{
			DBPath:        dbPath,
			Client:        strings.TrimSpace(input.Client),
			DefaultClient: defaultClientValue,
			Agent:         strings.TrimSpace(input.Agent),
			DefaultAgent:  defaultAgentValue,
			SessionID:     sessionID,
			Repo:          strings.TrimSpace(input.Repo),
			DefaultRepo:   "",
			Kind:          types.EventKindSessionEnded,
		})
		if err != nil {
			return nil, sessionEventOutput{}, xerrors.Errorf("failed to record session end: %w", err)
		}

		return nil, newSessionEventOutput(event), nil
	}
}

func (s *Server) latestSession(dbPath string) mcp.ToolHandlerFor[sessionLookupInput, sessionEventOutput] {
	return func(ctx context.Context, _ *mcp.CallToolRequest, input sessionLookupInput) (*mcp.CallToolResult, sessionEventOutput, error) {
		event, err := s.findLatestSessionQueryService.Run(ctx, dbPath, queryservice.FindLatestSessionInput{
			Client: strings.TrimSpace(input.Client),
			Agent:  strings.TrimSpace(input.Agent),
			Repo:   strings.TrimSpace(input.Repo),
		})
		if err != nil {
			if errors.Is(err, queryservice.ErrSessionNotFound) {
				return nil, sessionEventOutput{}, xerrors.Errorf("no matching session found")
			}
			return nil, sessionEventOutput{}, xerrors.Errorf("failed to get latest session: %w", err)
		}

		return nil, newSessionEventOutput(event), nil
	}
}

func (s *Server) activeSession(dbPath string) mcp.ToolHandlerFor[sessionLookupInput, sessionEventOutput] {
	return func(ctx context.Context, _ *mcp.CallToolRequest, input sessionLookupInput) (*mcp.CallToolResult, sessionEventOutput, error) {
		event, err := s.findLatestSessionQueryService.Run(ctx, dbPath, queryservice.FindLatestSessionInput{
			Client:     strings.TrimSpace(input.Client),
			Agent:      strings.TrimSpace(input.Agent),
			Repo:       strings.TrimSpace(input.Repo),
			ActiveOnly: true,
		})
		if err != nil {
			if errors.Is(err, queryservice.ErrActiveSessionNotFound) {
				return nil, sessionEventOutput{}, xerrors.Errorf("no matching active session found")
			}
			return nil, sessionEventOutput{}, xerrors.Errorf("failed to get active session: %w", err)
		}
		if err := validateActiveSession(event, input); err != nil {
			return nil, sessionEventOutput{}, err
		}

		return nil, newSessionEventOutput(event), nil
	}
}

func (s *Server) addAudit(dbPath string) mcp.ToolHandlerFor[addAuditInput, addAuditOutput] {
	return func(ctx context.Context, _ *mcp.CallToolRequest, input addAuditInput) (*mcp.CallToolResult, addAuditOutput, error) {
		event, audit, err := s.recordCommandAuditUsecase.Run(ctx, usecase.RecordCommandAuditInput{
			DBPath:              dbPath,
			Command:             input.Command,
			Input:               input.Input,
			Output:              input.Output,
			Client:              resolveValue(input.Client, defaultClientValue),
			Agent:               resolveValue(input.Agent, defaultAgentValue),
			SessionID:           resolveValue(input.SessionID, defaultSessionValue),
			Repo:                strings.TrimSpace(input.Repo),
			ExtraRedactPatterns: s.extraRedactPatterns,
		})
		if err != nil {
			return nil, addAuditOutput{}, xerrors.Errorf("failed to record command audit: %w", err)
		}

		return nil, addAuditOutput{
			EventID:         event.EventID().String(),
			Kind:            event.Kind().String(),
			SessionID:       event.SessionID().String(),
			Repo:            event.Repo(),
			Command:         audit.Command(),
			InputRedacted:   audit.InputRedacted(),
			OutputRedacted:  audit.OutputRedacted(),
			InputTruncated:  audit.InputTruncated(),
			OutputTruncated: audit.OutputTruncated(),
			CreatedAt:       event.CreatedAt().UTC().Format(time.RFC3339Nano),
		}, nil
	}
}

func (s *Server) listEvents(dbPath string) mcp.ToolHandlerFor[listEventsInput, eventsOutput] {
	return func(ctx context.Context, _ *mcp.CallToolRequest, input listEventsInput) (*mcp.CallToolResult, eventsOutput, error) {
		events, err := s.listEventsQueryService.Run(ctx, dbPath, queryservice.ListRecentEventsInput{
			Limit:  resolveLimit(input.Limit, defaultSearchLimit),
			Offset: resolveOffset(input.Offset),
		})
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
		Client:    event.Client(),
		Agent:     event.Agent().String(),
		SessionID: event.SessionID().String(),
		Repo:      event.Repo(),
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

func (s *Server) search(dbPath string) mcp.ToolHandlerFor[searchInput, eventsOutput] {
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
		events, err := s.searchEventsQueryService.Run(ctx, dbPath, queryservice.SearchEventsInput{
			Query: strings.TrimSpace(input.Query),
			Repo:  strings.TrimSpace(input.Repo),
			From:  from,
			To:    to,
			Limit: limit,
		})
		if err != nil {
			return nil, eventsOutput{}, xerrors.Errorf("failed to search events: %w", err)
		}

		return nil, eventsOutput{Events: convertEvents(events)}, nil
	}
}

func (s *Server) getContext(dbPath string) mcp.ToolHandlerFor[getContextInput, eventsOutput] {
	return func(ctx context.Context, _ *mcp.CallToolRequest, input getContextInput) (*mcp.CallToolResult, eventsOutput, error) {
		events, err := s.getContextQueryService.Run(ctx, dbPath, queryservice.GetContextInput{
			Repo:      strings.TrimSpace(input.Repo),
			SessionID: strings.TrimSpace(input.SessionID),
			Limit:     resolveLimit(input.Limit, defaultContextLimit),
		})
		if err != nil {
			return nil, eventsOutput{}, xerrors.Errorf("failed to get context: %w", err)
		}

		return nil, eventsOutput{Events: convertEvents(events)}, nil
	}
}

type sessionHandoffInput struct {
	SessionID string `json:"session_id,omitempty"`
	Repo      string `json:"repo,omitempty"`
}

type sessionHandoffOutput struct {
	SessionID    string   `json:"session_id,omitempty"`
	Repo         string   `json:"repo,omitempty"`
	Label        string   `json:"label,omitempty"`
	Status       string   `json:"status,omitempty"`
	TotalEvents  int      `json:"total_events"`
	CommandCount int      `json:"command_count"`
	Agents       []string `json:"agents,omitempty"`
	Summary      string   `json:"summary,omitempty"`
	RecentCommands []string `json:"recent_commands,omitempty"`
}

func (s *Server) sessionHandoff(dbPath string) mcp.ToolHandlerFor[sessionHandoffInput, sessionHandoffOutput] {
	return func(ctx context.Context, _ *mcp.CallToolRequest, input sessionHandoffInput) (*mcp.CallToolResult, sessionHandoffOutput, error) {
		sessions, err := s.listSessionsQueryService.Run(ctx, dbPath, queryservice.ListSessionsInput{
			Limit: 1,
			Repo:  strings.TrimSpace(input.Repo),
		})
		if err != nil {
			return nil, sessionHandoffOutput{}, xerrors.Errorf("failed to list sessions: %w", err)
		}

		if len(sessions) == 0 {
			return nil, sessionHandoffOutput{}, nil
		}

		session := sessions[0]
		events, err := s.listEventsQueryService.Run(ctx, dbPath, queryservice.ListRecentEventsInput{
			Limit:     5,
			SessionID: session.SessionID,
			Kind:      "command_executed",
		})
		if err != nil {
			return nil, sessionHandoffOutput{}, xerrors.Errorf("failed to list events: %w", err)
		}

		var recentCommands []string
		for _, e := range events {
			cmd := e.Body()
			if len([]rune(cmd)) > 60 {
				cmd = string([]rune(cmd)[:60]) + "…"
			}
			recentCommands = append(recentCommands, cmd)
		}

		return nil, sessionHandoffOutput{
			SessionID:      session.SessionID,
			Repo:           session.Repo,
			Label:          session.Label,
			Status:         session.Status,
			TotalEvents:    session.TotalEvents,
			CommandCount:   session.CommandCount,
			Agents:         session.Agents,
			Summary:        session.Summary,
			RecentCommands: recentCommands,
		}, nil
	}
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
			Client:    event.Client(),
			Agent:     event.Agent().String(),
			SessionID: event.SessionID().String(),
			Repo:      event.Repo(),
			Body:      event.Body(),
			CreatedAt: event.CreatedAt().UTC().Format(time.RFC3339Nano),
		})
	}

	return outputs
}

func boolPtr(value bool) *bool {
	return &value
}
