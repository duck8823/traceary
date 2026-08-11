package mcpserver_test

import (
	"context"
	"database/sql"
	"sort"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestServer_SearchSessionProjectionBehaviors(t *testing.T) {
	for _, tt := range []struct {
		name       string
		query      string
		setup      func(t *testing.T, dbPath string, client *mcp.ClientSession)
		want       searchBehaviorObservation
		toolChecks bool
	}{
		{
			name:  "session match is returned outside the recent event window",
			query: "unique-session-marker",
			setup: func(t *testing.T, dbPath string, _ *mcp.ClientSession) {
				seedSearchSession(t, dbPath, "session-old", "unique-session-marker", time.Date(2026, 6, 19, 10, 0, 0, 0, time.UTC))
			},
			want: searchBehaviorObservation{Sessions: []string{"session-old"}, Reasons: []string{"session_matches"}},
		},
		{
			// Events is asserted here so the empty Sessions cannot pass for the
			// wrong reason: the session must be excluded because the event tier
			// already reported it, not because the search matched nothing.
			name:  "event sessions are not double reported",
			query: "event-session-marker",
			setup: func(t *testing.T, dbPath string, client *mcp.ClientSession) {
				seedSearchSession(t, dbPath, "session-event", "event-session-marker", time.Date(2026, 6, 19, 10, 0, 0, 0, time.UTC))
				callMCPTool(t, client, "record_event", map[string]any{
					"type": "log", "message": "event-session-marker", "agent": "codex", "session_id": "session-event",
				})
			},
			want: searchBehaviorObservation{Events: []string{"session-event"}},
		},
		{
			name:  "incomplete projection reports its readiness reason",
			query: "not-present",
			setup: func(t *testing.T, dbPath string, _ *mcp.ClientSession) {
				openMCPTestDB(t, dbPath, func(db *sql.DB) {
					if _, err := db.Exec(`UPDATE search_projection_state SET state='rebuilding', active_generation_id=NULL WHERE singleton=1`); err != nil {
						t.Fatalf("mark projection incomplete: %v", err)
					}
				})
			},
			want: searchBehaviorObservation{Reasons: []string{"session_projection_not_ready"}},
		},
		{
			name: "event tools retain their own response shape",
			setup: func(t *testing.T, _ string, client *mcp.ClientSession) {
				callMCPTool(t, client, "record_event", map[string]any{
					"type": "log", "message": "shape-marker", "agent": "codex", "session_id": "session-shape",
				})
			},
			toolChecks: true,
			want:       searchBehaviorObservation{},
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			server, dbPath, _ := newTestServerWithDBPath(t)
			client := connectMCPTestClient(t, server)
			tt.setup(t, dbPath, client)

			if tt.toolChecks {
				list := callMCPTool(t, client, "list_events", map[string]any{"limit": 10})
				contextResult := callMCPTool(t, client, "get_context", map[string]any{"session_id": "session-shape", "limit": 10})
				if diff := cmp.Diff([]string{"coverage", "events", "interval", "partial"}, payloadKeys(decodeJSONPayload(t, list))); diff != "" {
					t.Fatalf("list_events response shape changed (-want +got):\n%s", diff)
				}
				if diff := cmp.Diff([]string{"coverage", "events", "partial"}, payloadKeys(decodeJSONPayload(t, contextResult))); diff != "" {
					t.Fatalf("get_context response shape changed (-want +got):\n%s", diff)
				}
				return
			}

			result := callMCPTool(t, client, "search", map[string]any{"query": tt.query, "limit": 10})
			payload := decodeJSONPayload(t, result)
			got := searchBehaviorObservation{
				Events:   stringArrayField(payload, "events"),
				Sessions: stringArrayField(payload, "sessions"),
				Reasons:  stringArrayField(payload, "reasons"),
			}
			if diff := cmp.Diff(tt.want, got); diff != "" {
				t.Fatalf("search behavior mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

// searchBehaviorObservation reduces a search response to the session ids it
// reported and where: through the event tier, through the session tier, or not
// at all. Reasons carries the machine-readable signal alongside.
type searchBehaviorObservation struct {
	Events   []string
	Sessions []string
	Reasons  []string
}

func seedSearchSession(t *testing.T, dbPath, sessionID, marker string, startedAt time.Time) {
	t.Helper()
	openMCPTestDB(t, dbPath, func(db *sql.DB) {
		const generationID = "mcp-search-test-generation"
		if _, err := db.Exec(`UPDATE search_projection_state SET generation_id=?, active_generation_id=?, state='complete', phase='complete', high_water=0 WHERE singleton=1`, generationID, generationID); err != nil {
			t.Fatalf("activate projection: %v", err)
		}
		if _, err := db.Exec(`INSERT OR REPLACE INTO search_projection_generation_lifecycle(generation_id, state, config_hash, source_revision, high_water) VALUES (?, 'complete', 'test', 0, 0)`, generationID); err != nil {
			t.Fatalf("insert projection lifecycle: %v", err)
		}
		if _, err := db.Exec(`INSERT OR REPLACE INTO sessions(session_id, started_at, client, agent, workspace) VALUES (?, ?, 'cli', 'codex', '')`, sessionID, startedAt.UTC().Format(time.RFC3339Nano)); err != nil {
			t.Fatalf("insert session: %v", err)
		}
		if _, err := db.Exec(`INSERT INTO search_projection_session_summaries(generation_id, session_id, event_count, summary_text, projection_version, summary_version) VALUES (?, ?, 12, ?, 1, 1)`, generationID, sessionID, marker); err != nil {
			t.Fatalf("insert session summary: %v", err)
		}
		if _, err := db.Exec(`INSERT INTO search_projection_session_keywords(generation_id, session_id, keyword, occurrences, keyword_version) VALUES (?, ?, ?, 1, 1)`, generationID, sessionID, marker); err != nil {
			t.Fatalf("insert session keyword: %v", err)
		}
	})
}

func openMCPTestDB(t *testing.T, dbPath string, fn func(*sql.DB)) {
	t.Helper()
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("sql.Open: %v", err)
	}
	defer func() { _ = db.Close() }()
	fn(db)
}

func connectMCPTestClient(t *testing.T, server interface {
	Build(context.Context) (*mcp.Server, error)
}) *mcp.ClientSession {
	t.Helper()
	ctx := context.Background()
	mcpServer, err := server.Build(ctx)
	if err != nil {
		t.Fatalf("Build(): %v", err)
	}
	clientTransport, serverTransport := mcp.NewInMemoryTransports()
	serverSession, err := mcpServer.Connect(ctx, serverTransport, nil)
	if err != nil {
		t.Fatalf("Connect(server): %v", err)
	}
	t.Cleanup(func() { _ = serverSession.Wait() })
	client := mcp.NewClient(&mcp.Implementation{Name: "search-behavior-test", Version: "v1"}, nil)
	clientSession, err := client.Connect(ctx, clientTransport, nil)
	if err != nil {
		t.Fatalf("Connect(client): %v", err)
	}
	t.Cleanup(func() { _ = clientSession.Close() })
	return clientSession
}

func callMCPTool(t *testing.T, client *mcp.ClientSession, name string, arguments map[string]any) *mcp.CallToolResult {
	t.Helper()
	result, err := client.CallTool(context.Background(), &mcp.CallToolParams{Name: name, Arguments: arguments})
	if err != nil || result.IsError {
		t.Fatalf("CallTool(%s): err=%v result=%s", name, err, mcpToolErrorText(result))
	}
	return result
}

func stringArrayField(payload map[string]any, key string) []string {
	values, ok := payload[key].([]any)
	if !ok || len(values) == 0 {
		// events is a required field and arrives as [], sessions and reasons
		// are omitempty and arrive absent. Normalise both to nil so the
		// expectations read the same way.
		return nil
	}
	result := make([]string, 0, len(values))
	for _, value := range values {
		if item, ok := value.(map[string]any); ok {
			if id, ok := item["session_id"].(string); ok {
				result = append(result, id)
			}
			continue
		}
		if reason, ok := value.(string); ok {
			result = append(result, reason)
		}
	}
	return result
}

func payloadKeys(payload map[string]any) []string {
	keys := make([]string, 0, len(payload))
	for key := range payload {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}
