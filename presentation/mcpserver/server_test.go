package mcpserver_test

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"
	"testing/fstest"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/duck8823/traceary/application/queryservice"
	"github.com/duck8823/traceary/application/usecase"
	"github.com/duck8823/traceary/infrastructure/sqlite"
	"github.com/duck8823/traceary/presentation/mcpserver"
)

func TestServer_BuildAndTools(t *testing.T) {
	t.Parallel()

	server, dbPath := newTestServer(t)
	ctx := context.Background()
	mcpServer, err := server.Build(ctx, dbPath)
	if err != nil {
		t.Fatalf("Build() error = %v", err)
	}

	clientTransport, serverTransport := mcp.NewInMemoryTransports()
	serverSession, err := mcpServer.Connect(ctx, serverTransport, nil)
	if err != nil {
		t.Fatalf("Connect(server) error = %v", err)
	}
	defer func() { _ = serverSession.Wait() }()

	client := mcp.NewClient(&mcp.Implementation{Name: "test-client", Version: "v1.0.0"}, nil)
	clientSession, err := client.Connect(ctx, clientTransport, nil)
	if err != nil {
		t.Fatalf("Connect(client) error = %v", err)
	}
	defer func() { _ = clientSession.Close() }()

	t.Run("add_log がイベントを保存する", func(t *testing.T) {
		result, err := clientSession.CallTool(ctx, &mcp.CallToolParams{
			Name: "add_log",
			Arguments: map[string]any{
				"message":    "hello from mcp",
				"agent":      "claude",
				"session_id": "session-1",
				"workspace":       "github.com/duck8823/traceary",
			},
		})
		if err != nil {
			t.Fatalf("CallTool(add_log) error = %v", err)
		}
		if result.IsError {
			t.Fatalf("CallTool(add_log) returned tool error")
		}

		searchResult, err := clientSession.CallTool(ctx, &mcp.CallToolParams{
			Name: "search",
			Arguments: map[string]any{
				"query": "hello from mcp",
				"limit": 10,
			},
		})
		if err != nil {
			t.Fatalf("CallTool(search) error = %v", err)
		}
		if searchResult.IsError {
			t.Fatalf("CallTool(search) returned tool error")
		}
		if len(searchResult.Content) == 0 {
			t.Fatalf("search result content is empty")
		}

		listResult, err := clientSession.CallTool(ctx, &mcp.CallToolParams{
			Name: "list_events",
			Arguments: map[string]any{
				"limit":  10,
				"offset": 0,
			},
		})
		if err != nil {
			t.Fatalf("CallTool(list_events) error = %v", err)
		}
		if listResult.IsError {
			t.Fatalf("CallTool(list_events) returned tool error")
		}
		if len(listResult.Content) == 0 {
			t.Fatalf("list_events result content is empty")
		}
	})

	t.Run("add_audit と get_context が動作する", func(t *testing.T) {
		result, err := clientSession.CallTool(ctx, &mcp.CallToolParams{
			Name: "add_audit",
			Arguments: map[string]any{
				"command":    "go test ./...",
				"input":      "stdin",
				"output":     "stdout",
				"agent":      "codex",
				"session_id": "session-2",
				"workspace":       "github.com/duck8823/traceary",
			},
		})
		if err != nil {
			t.Fatalf("CallTool(add_audit) error = %v", err)
		}
		if result.IsError {
			t.Fatalf("CallTool(add_audit) returned tool error")
		}

		contextResult, err := clientSession.CallTool(ctx, &mcp.CallToolParams{
			Name: "get_context",
			Arguments: map[string]any{
				"session_id": "session-2",
				"limit":      10,
			},
		})
		if err != nil {
			t.Fatalf("CallTool(get_context) error = %v", err)
		}
		if contextResult.IsError {
			t.Fatalf("CallTool(get_context) returned tool error")
		}
		if len(contextResult.Content) == 0 {
			t.Fatalf("get_context result content is empty")
		}
	})

	t.Run("session tools が動作する", func(t *testing.T) {
		startResult, err := clientSession.CallTool(ctx, &mcp.CallToolParams{
			Name: "start_session",
			Arguments: map[string]any{
				"agent": "codex",
				"workspace":  "github.com/duck8823/traceary",
			},
		})
		if err != nil {
			t.Fatalf("CallTool(start_session) error = %v", err)
		}
		if startResult.IsError {
			t.Fatalf("CallTool(start_session) returned tool error")
		}

		activeResult, err := clientSession.CallTool(ctx, &mcp.CallToolParams{
			Name: "active_session",
			Arguments: map[string]any{
				"workspace": "github.com/duck8823/traceary",
			},
		})
		if err != nil {
			t.Fatalf("CallTool(active_session) error = %v", err)
		}
		if activeResult.IsError {
			t.Fatalf("CallTool(active_session) returned tool error")
		}

		latestResult, err := clientSession.CallTool(ctx, &mcp.CallToolParams{
			Name: "latest_session",
			Arguments: map[string]any{
				"workspace": "github.com/duck8823/traceary",
			},
		})
		if err != nil {
			t.Fatalf("CallTool(latest_session) error = %v", err)
		}
		if latestResult.IsError {
			t.Fatalf("CallTool(latest_session) returned tool error")
		}

		sessionID := extractJSONStringValue(t, startResult, "session_id")
		if sessionID == "" {
			t.Fatalf("session_id = empty, want non-empty")
		}
		if got := extractJSONStringValue(t, activeResult, "session_id"); got != sessionID {
			t.Fatalf("active session_id = %q, want %q", got, sessionID)
		}
		if got := extractJSONStringValue(t, latestResult, "session_id"); got != sessionID {
			t.Fatalf("latest session_id = %q, want %q", got, sessionID)
		}

		endResult, err := clientSession.CallTool(ctx, &mcp.CallToolParams{
			Name: "end_session",
			Arguments: map[string]any{
				"session_id": sessionID,
			},
		})
		if err != nil {
			t.Fatalf("CallTool(end_session) error = %v", err)
		}
		if endResult.IsError {
			t.Fatalf("CallTool(end_session) returned tool error")
		}
		if got := extractJSONStringValue(t, endResult, "kind"); got != "session_ended" {
			t.Fatalf("end kind = %q, want %q", got, "session_ended")
		}
	})

	t.Run("tool error を返せる", func(t *testing.T) {
		result, err := clientSession.CallTool(ctx, &mcp.CallToolParams{
			Name: "add_log",
			Arguments: map[string]any{
				"message": "   ",
			},
		})
		if err != nil {
			t.Fatalf("CallTool(add_log) protocol error = %v", err)
		}
		if !result.IsError {
			t.Fatalf("CallTool(add_log) IsError = false, want true")
		}
	})
}

func extractJSONStringValue(t *testing.T, result *mcp.CallToolResult, key string) string {
	t.Helper()

	if len(result.Content) == 0 {
		t.Fatalf("tool result content is empty")
	}

	textContent, ok := result.Content[0].(*mcp.TextContent)
	if !ok {
		t.Fatalf("unexpected content type: %T", result.Content[0])
	}

	payload := map[string]any{}
	if err := json.Unmarshal([]byte(textContent.Text), &payload); err != nil {
		t.Fatalf("json.Unmarshal() error = %v", err)
	}

	value, ok := payload[key]
	if !ok {
		return ""
	}

	stringValue, ok := value.(string)
	if !ok {
		t.Fatalf("payload[%q] type = %T, want string", key, value)
	}

	return stringValue
}

func newTestServer(t *testing.T) (*mcpserver.Server, string) {
	t.Helper()

	migrations := fstest.MapFS{
		"000001_init.sql": {
			Data: []byte(`
CREATE TABLE events (
    id TEXT PRIMARY KEY,
    kind TEXT NOT NULL,
    agent TEXT NOT NULL,
    session_id TEXT NOT NULL,
    body TEXT NOT NULL,
    created_at TEXT NOT NULL
);`),
		},
		"000002_add_event_metadata.sql": {
			Data: []byte(`
ALTER TABLE events ADD COLUMN client TEXT NOT NULL DEFAULT '';
ALTER TABLE events ADD COLUMN workspace TEXT NOT NULL DEFAULT '';`),
		},
		"000003_create_command_audits.sql": {
			Data: []byte(`
CREATE TABLE command_audits (
    event_id TEXT PRIMARY KEY REFERENCES events(id) ON DELETE CASCADE,
    command_text TEXT NOT NULL,
    input_text TEXT NOT NULL,
    output_text TEXT NOT NULL,
    input_truncated INTEGER NOT NULL DEFAULT 0,
    output_truncated INTEGER NOT NULL DEFAULT 0,
    exit_code INTEGER
);`),
		},
	}
	dbPath := filepath.Join(t.TempDir(), "traceary", "traceary.db")
	datasource := sqlite.NewDatasource(dbPath, migrations)
	initializeStoreUsecase := usecase.NewInitializeStoreUsecase(datasource)
	recordLogUsecase := usecase.NewRecordLogUsecase(datasource)
	recordSessionBoundaryUsecase := usecase.NewRecordSessionBoundaryUsecase(datasource, datasource)
	recordCommandAuditUsecase := usecase.NewRecordCommandAuditUsecase(datasource)
	findLatestSessionQueryService := queryservice.NewFindLatestSessionQueryService(datasource)
	searchEventsQueryService := queryservice.NewSearchEventsQueryService(datasource)
	getContextQueryService := queryservice.NewGetContextQueryService(datasource)
	server, err := mcpserver.NewServer(
		"test-version",
		initializeStoreUsecase,
		recordLogUsecase,
		recordSessionBoundaryUsecase,
		recordCommandAuditUsecase,
		findLatestSessionQueryService,
		queryservice.NewListRecentEventsQueryService(datasource),
		searchEventsQueryService,
		getContextQueryService,
		queryservice.NewListSessionsQueryService(datasource),
	)
	if err != nil {
		t.Fatalf("NewServer() error = %v", err)
	}

	return server, dbPath
}
