package mcpserver

import (
	"context"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"golang.org/x/xerrors"

	"github.com/duck8823/traceary/application/queryservice"
	"github.com/duck8823/traceary/application/usecase"
	"github.com/duck8823/traceary/domain/model"
)

const (
	defaultClientValue   = "mcp"
	defaultAgentValue    = "manual"
	defaultSessionValue  = "default"
	defaultContextLimit  = 20
	defaultSearchLimit   = 20
	defaultServerName    = "traceary"
	defaultServerVersion = "dev"
)

// Server は Traceary の MCP server を提供します。
type Server struct {
	serverName                string
	serverVersion             string
	initializeStoreUsecase    usecase.InitializeStoreUsecase
	recordLogUsecase          usecase.RecordLogUsecase
	recordCommandAuditUsecase usecase.RecordCommandAuditUsecase
	searchEventsQueryService  queryservice.SearchEventsQueryService
	getContextQueryService    queryservice.GetContextQueryService
}

// NewServer は新しい MCP server を生成します。
func NewServer(
	serverVersion string,
	initializeStoreUsecase usecase.InitializeStoreUsecase,
	recordLogUsecase usecase.RecordLogUsecase,
	recordCommandAuditUsecase usecase.RecordCommandAuditUsecase,
	searchEventsQueryService queryservice.SearchEventsQueryService,
	getContextQueryService queryservice.GetContextQueryService,
) (*Server, error) {
	if initializeStoreUsecase == nil {
		return nil, xerrors.Errorf("ストア初期化ユースケースが設定されていません")
	}
	if recordLogUsecase == nil {
		return nil, xerrors.Errorf("ログ記録ユースケースが設定されていません")
	}
	if recordCommandAuditUsecase == nil {
		return nil, xerrors.Errorf("監査ログ記録ユースケースが設定されていません")
	}
	if searchEventsQueryService == nil {
		return nil, xerrors.Errorf("検索クエリサービスが設定されていません")
	}
	if getContextQueryService == nil {
		return nil, xerrors.Errorf("文脈取得クエリサービスが設定されていません")
	}

	trimmedVersion := strings.TrimSpace(serverVersion)
	if trimmedVersion == "" {
		trimmedVersion = defaultServerVersion
	}

	return &Server{
		serverName:                defaultServerName,
		serverVersion:             trimmedVersion,
		initializeStoreUsecase:    initializeStoreUsecase,
		recordLogUsecase:          recordLogUsecase,
		recordCommandAuditUsecase: recordCommandAuditUsecase,
		searchEventsQueryService:  searchEventsQueryService,
		getContextQueryService:    getContextQueryService,
	}, nil
}

// Build は起動済みストアを参照する MCP server を構築します。
func (s *Server) Build(ctx context.Context, dbPath string) (*mcp.Server, error) {
	trimmedDBPath := strings.TrimSpace(dbPath)
	if trimmedDBPath == "" {
		return nil, xerrors.Errorf("DB パスは空にできません")
	}
	if err := s.initializeStoreUsecase.Run(ctx, trimmedDBPath); err != nil {
		return nil, xerrors.Errorf("ストアの初期化に失敗しました: %w", err)
	}

	server := mcp.NewServer(&mcp.Implementation{
		Name:    s.serverName,
		Version: s.serverVersion,
	}, nil)

	mcp.AddTool(server, &mcp.Tool{
		Name:        "add_log",
		Description: "Traceary にログイベントを追加します",
		Annotations: &mcp.ToolAnnotations{DestructiveHint: boolPtr(false)},
	}, s.addLog(trimmedDBPath))
	mcp.AddTool(server, &mcp.Tool{
		Name:        "add_audit",
		Description: "Traceary にコマンド監査イベントを追加します",
		Annotations: &mcp.ToolAnnotations{DestructiveHint: boolPtr(false)},
	}, s.addAudit(trimmedDBPath))
	mcp.AddTool(server, &mcp.Tool{
		Name:        "search",
		Description: "Traceary のイベントを検索します",
		Annotations: &mcp.ToolAnnotations{ReadOnlyHint: true},
	}, s.search(trimmedDBPath))
	mcp.AddTool(server, &mcp.Tool{
		Name:        "get_context",
		Description: "指定条件の最近のイベント文脈を取得します",
		Annotations: &mcp.ToolAnnotations{ReadOnlyHint: true},
	}, s.getContext(trimmedDBPath))

	return server, nil
}

// Run は stdio transport 上で MCP server を起動します。
func (s *Server) Run(ctx context.Context, dbPath string) error {
	server, err := s.Build(ctx, dbPath)
	if err != nil {
		return xerrors.Errorf("MCP server の構築に失敗しました: %w", err)
	}
	if err := server.Run(ctx, &mcp.StdioTransport{}); err != nil {
		return xerrors.Errorf("MCP server の実行に失敗しました: %w", err)
	}

	return nil
}

type addLogInput struct {
	Message   string `json:"message" jsonschema:"記録するログ本文"`
	Client    string `json:"client,omitempty" jsonschema:"記録経路。省略時は mcp"`
	Agent     string `json:"agent,omitempty" jsonschema:"作業主体。省略時は manual"`
	SessionID string `json:"session_id,omitempty" jsonschema:"セッション識別子。省略時は default"`
	Repo      string `json:"repo,omitempty" jsonschema:"補助的な work context 識別子"`
}

type addLogOutput struct {
	EventID   string `json:"event_id" jsonschema:"保存したイベント ID"`
	Kind      string `json:"kind" jsonschema:"イベント種別"`
	Client    string `json:"client" jsonschema:"記録経路"`
	Agent     string `json:"agent" jsonschema:"作業主体"`
	SessionID string `json:"session_id" jsonschema:"セッション識別子"`
	Repo      string `json:"repo,omitempty" jsonschema:"補助的な work context 識別子"`
	Body      string `json:"body" jsonschema:"イベント本文"`
	CreatedAt string `json:"created_at" jsonschema:"イベント記録時刻 (RFC3339Nano)"`
}

type addAuditInput struct {
	Command   string `json:"command" jsonschema:"実行したコマンド"`
	Input     string `json:"input,omitempty" jsonschema:"コマンド入力"`
	Output    string `json:"output,omitempty" jsonschema:"コマンド出力"`
	Client    string `json:"client,omitempty" jsonschema:"記録経路。省略時は mcp"`
	Agent     string `json:"agent,omitempty" jsonschema:"作業主体。省略時は manual"`
	SessionID string `json:"session_id,omitempty" jsonschema:"セッション識別子。省略時は default"`
	Repo      string `json:"repo,omitempty" jsonschema:"補助的な work context 識別子"`
}

type addAuditOutput struct {
	EventID         string `json:"event_id" jsonschema:"保存したイベント ID"`
	Kind            string `json:"kind" jsonschema:"イベント種別"`
	SessionID       string `json:"session_id" jsonschema:"セッション識別子"`
	Repo            string `json:"repo,omitempty" jsonschema:"補助的な work context 識別子"`
	Command         string `json:"command" jsonschema:"実行したコマンド"`
	InputRedacted   bool   `json:"input_redacted" jsonschema:"入力が伏せ字化されたか"`
	OutputRedacted  bool   `json:"output_redacted" jsonschema:"出力が伏せ字化されたか"`
	InputTruncated  bool   `json:"input_truncated" jsonschema:"入力が切り詰められたか"`
	OutputTruncated bool   `json:"output_truncated" jsonschema:"出力が切り詰められたか"`
	CreatedAt       string `json:"created_at" jsonschema:"イベント記録時刻 (RFC3339Nano)"`
}

type searchInput struct {
	Query string `json:"query" jsonschema:"検索語"`
	Repo  string `json:"repo,omitempty" jsonschema:"絞り込む work context"`
	From  string `json:"from,omitempty" jsonschema:"開始時刻。YYYY-MM-DD または RFC3339"`
	To    string `json:"to,omitempty" jsonschema:"終了時刻。YYYY-MM-DD または RFC3339"`
	Limit int    `json:"limit,omitempty" jsonschema:"返却件数。省略時は 20"`
}

type getContextInput struct {
	Repo      string `json:"repo,omitempty" jsonschema:"絞り込む work context"`
	SessionID string `json:"session_id,omitempty" jsonschema:"絞り込むセッション識別子"`
	Limit     int    `json:"limit,omitempty" jsonschema:"返却件数。省略時は 20"`
}

type eventsOutput struct {
	Events []eventOutput `json:"events" jsonschema:"条件に一致したイベント一覧"`
}

type eventOutput struct {
	EventID   string `json:"event_id" jsonschema:"イベント ID"`
	Kind      string `json:"kind" jsonschema:"イベント種別"`
	Client    string `json:"client" jsonschema:"記録経路"`
	Agent     string `json:"agent" jsonschema:"作業主体"`
	SessionID string `json:"session_id" jsonschema:"セッション識別子"`
	Repo      string `json:"repo,omitempty" jsonschema:"補助的な work context 識別子"`
	Body      string `json:"body" jsonschema:"イベント本文"`
	CreatedAt string `json:"created_at" jsonschema:"イベント記録時刻 (RFC3339Nano)"`
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
			return nil, addLogOutput{}, xerrors.Errorf("ログ記録に失敗しました: %w", err)
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

func (s *Server) addAudit(dbPath string) mcp.ToolHandlerFor[addAuditInput, addAuditOutput] {
	return func(ctx context.Context, _ *mcp.CallToolRequest, input addAuditInput) (*mcp.CallToolResult, addAuditOutput, error) {
		event, audit, err := s.recordCommandAuditUsecase.Run(ctx, usecase.RecordCommandAuditInput{
			DBPath:    dbPath,
			Command:   input.Command,
			Input:     input.Input,
			Output:    input.Output,
			Client:    resolveValue(input.Client, defaultClientValue),
			Agent:     resolveValue(input.Agent, defaultAgentValue),
			SessionID: resolveValue(input.SessionID, defaultSessionValue),
			Repo:      strings.TrimSpace(input.Repo),
		})
		if err != nil {
			return nil, addAuditOutput{}, xerrors.Errorf("監査ログ記録に失敗しました: %w", err)
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

func (s *Server) search(dbPath string) mcp.ToolHandlerFor[searchInput, eventsOutput] {
	return func(ctx context.Context, _ *mcp.CallToolRequest, input searchInput) (*mcp.CallToolResult, eventsOutput, error) {
		from, err := parseFlexibleTime(input.From, false)
		if err != nil {
			return nil, eventsOutput{}, xerrors.Errorf("from の解決に失敗しました: %w", err)
		}
		to, err := parseFlexibleTime(input.To, true)
		if err != nil {
			return nil, eventsOutput{}, xerrors.Errorf("to の解決に失敗しました: %w", err)
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
			return nil, eventsOutput{}, xerrors.Errorf("検索に失敗しました: %w", err)
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
			return nil, eventsOutput{}, xerrors.Errorf("文脈取得に失敗しました: %w", err)
		}

		return nil, eventsOutput{Events: convertEvents(events)}, nil
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
		return time.Time{}, xerrors.Errorf("時刻は RFC3339 または YYYY-MM-DD 形式で指定してください: %w", err)
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
