package mcpserver_test

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"
	"testing/fstest"

	"github.com/google/go-cmp/cmp"
	"github.com/modelcontextprotocol/go-sdk/mcp"

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

	t.Run("add_log saves an event", func(t *testing.T) {
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

	t.Run("add_log with kind saves event with specified kind", func(t *testing.T) {
		result, err := clientSession.CallTool(ctx, &mcp.CallToolParams{
			Name: "add_log",
			Arguments: map[string]any{
				"message":    "compact summary text",
				"kind":       "compact_summary",
				"agent":      "claude",
				"session_id": "session-1",
			},
		})
		if err != nil {
			t.Fatalf("CallTool(add_log) error = %v", err)
		}
		if result.IsError {
			t.Fatalf("CallTool(add_log) returned tool error")
		}
		if diff := cmp.Diff("compact_summary", extractJSONStringValue(t, result, "kind")); diff != "" {
			t.Fatalf("kind mismatch (-want +got):\n%s", diff)
		}
	})

	t.Run("add_audit and get_context work together", func(t *testing.T) {
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

	t.Run("session tools work correctly", func(t *testing.T) {
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
		if diff := cmp.Diff(sessionID, extractJSONStringValue(t, activeResult, "session_id")); diff != "" {
			t.Fatalf("active session_id mismatch (-want +got):\n%s", diff)
		}
		if diff := cmp.Diff(sessionID, extractJSONStringValue(t, latestResult, "session_id")); diff != "" {
			t.Fatalf("latest session_id mismatch (-want +got):\n%s", diff)
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
		if diff := cmp.Diff("session_ended", extractJSONStringValue(t, endResult, "kind")); diff != "" {
			t.Fatalf("end kind mismatch (-want +got):\n%s", diff)
		}
	})

	t.Run("returns tool error", func(t *testing.T) {
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
		"000004_create_sessions.sql": {
			Data: []byte(`
CREATE TABLE IF NOT EXISTS sessions (
    session_id TEXT PRIMARY KEY,
    started_at TEXT NOT NULL,
    ended_at TEXT,
    client TEXT NOT NULL DEFAULT '',
    agent TEXT NOT NULL DEFAULT '',
    workspace TEXT NOT NULL DEFAULT '',
    label TEXT NOT NULL DEFAULT '',
    summary TEXT NOT NULL DEFAULT '',
    parent_session_id TEXT REFERENCES sessions(session_id)
);`),
		},
	}
	dbPath := filepath.Join(t.TempDir(), "traceary", "traceary.db")
	store := sqlite.NewStore(dbPath, migrations)

	eventUsecase := usecase.NewEventUsecase(store.EventRepository, store.EventQueryService)
	sessionUsecase := usecase.NewSessionUsecase(store.EventRepository, store.SessionRepository, store.SessionQueryService, store.EventQueryService)
	storeManagementUsecase := usecase.NewStoreManagementUsecase(store.StoreManager)

	server, err := mcpserver.NewServer(
		"test-version",
		eventUsecase,
		sessionUsecase,
		storeManagementUsecase,
	)
	if err != nil {
		t.Fatalf("NewServer() error = %v", err)
	}

	return server, dbPath
}
