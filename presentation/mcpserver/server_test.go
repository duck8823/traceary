package mcpserver_test

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
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

	server := newTestServer(t)
	ctx := context.Background()
	mcpServer, err := server.Build(ctx)
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
			Name: "record_event",
			Arguments: map[string]any{
				"type":       "log",
				"message":    "hello from mcp",
				"agent":      "claude",
				"session_id": "session-1",
				"workspace":  "github.com/duck8823/traceary",
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
			Name: "record_event",
			Arguments: map[string]any{
				"type":       "log",
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
			Name: "record_event",
			Arguments: map[string]any{
				"type":       "audit",
				"command":    "go test ./...",
				"input":      "stdin",
				"output":     "stdout",
				"agent":      "codex",
				"session_id": "session-2",
				"workspace":  "github.com/duck8823/traceary",
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
			Name: "manage_session",
			Arguments: map[string]any{
				"action":    "start",
				"agent":     "codex",
				"workspace": "github.com/duck8823/traceary",
			},
		})
		if err != nil {
			t.Fatalf("CallTool(start_session) error = %v", err)
		}
		if startResult.IsError {
			t.Fatalf("CallTool(start_session) returned tool error")
		}

		activeResult, err := clientSession.CallTool(ctx, &mcp.CallToolParams{
			Name: "session_status",
			Arguments: map[string]any{
				"action":    "active",
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
			Name: "session_status",
			Arguments: map[string]any{
				"action":    "latest",
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
			Name: "manage_session",
			Arguments: map[string]any{
				"action":     "end",
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

	t.Run("memory tools manage lifecycle and retrieval", func(t *testing.T) {
		proposeResult, err := clientSession.CallTool(ctx, &mcp.CallToolParams{
			Name: "manage_memory",
			Arguments: map[string]any{
				"action":    "propose",
				"type":      "decision",
				"workspace": "github.com/duck8823/traceary",
				"fact":      "Use ContextUsecase for structured handoff output",
				"evidence_refs": []any{
					map[string]any{"kind": "issue", "value": "#464"},
				},
			},
		})
		if err != nil {
			t.Fatalf("CallTool(propose_memory) error = %v", err)
		}
		if proposeResult.IsError {
			t.Fatalf("CallTool(propose_memory) returned tool error")
		}
		proposedID := extractJSONStringValue(t, proposeResult, "memory_id")
		if proposedID == "" {
			t.Fatalf("proposed memory_id = empty, want non-empty")
		}

		acceptResult, err := clientSession.CallTool(ctx, &mcp.CallToolParams{
			Name: "manage_memory",
			Arguments: map[string]any{
				"action": "accept",
				"ids":    proposedID,
			},
		})
		if err != nil {
			t.Fatalf("CallTool(accept_memory) error = %v", err)
		}
		if acceptResult.IsError {
			t.Fatalf("CallTool(accept_memory) returned tool error")
		}
		if diff := cmp.Diff("accepted", extractJSONStringValue(t, acceptResult, "status")); diff != "" {
			t.Fatalf("accepted status mismatch (-want +got):\n%s", diff)
		}

		retrieveResult, err := clientSession.CallTool(ctx, &mcp.CallToolParams{
			Name: "query_memory",
			Arguments: map[string]any{
				"action": "retrieve",
				"query":  "ContextUsecase",
				"limit":  5,
			},
		})
		if err != nil {
			t.Fatalf("CallTool(retrieve_memories) error = %v", err)
		}
		if retrieveResult.IsError {
			t.Fatalf("CallTool(retrieve_memories) returned tool error")
		}
		payload := decodeJSONPayload(t, retrieveResult)
		memories, ok := payload["memories"].([]any)
		if !ok {
			t.Fatalf("payload[memories] type = %T, want []any", payload["memories"])
		}
		if len(memories) == 0 {
			t.Fatalf("retrieve_memories returned no memories")
		}
		firstMemory, ok := memories[0].(map[string]any)
		if !ok {
			t.Fatalf("memories[0] type = %T, want map[string]any", memories[0])
		}
		if diff := cmp.Diff(proposedID, firstMemory["memory_id"]); diff != "" {
			t.Fatalf("retrieved memory_id mismatch (-want +got):\n%s", diff)
		}

		rejectResult, err := clientSession.CallTool(ctx, &mcp.CallToolParams{
			Name: "manage_memory",
			Arguments: map[string]any{
				"action":    "propose",
				"type":      "lesson",
				"workspace": "github.com/duck8823/traceary",
				"fact":      "Candidate memory for rejection",
				"evidence_refs": []any{
					map[string]any{"kind": "issue", "value": "#464"},
				},
			},
		})
		if err != nil {
			t.Fatalf("CallTool(propose_memory) for reject flow error = %v", err)
		}
		rejectedID := extractJSONStringValue(t, rejectResult, "memory_id")
		rejectMutationResult, err := clientSession.CallTool(ctx, &mcp.CallToolParams{
			Name: "manage_memory",
			Arguments: map[string]any{
				"action":    "reject",
				"memory_id": rejectedID,
			},
		})
		if err != nil {
			t.Fatalf("CallTool(reject_memory) error = %v", err)
		}
		if rejectMutationResult.IsError {
			t.Fatalf("CallTool(reject_memory) returned tool error")
		}
		if diff := cmp.Diff("rejected", extractJSONStringValue(t, rejectMutationResult, "status")); diff != "" {
			t.Fatalf("rejected status mismatch (-want +got):\n%s", diff)
		}

		batchProposeIDs := make([]string, 0, 2)
		for i := 0; i < 2; i++ {
			res, err := clientSession.CallTool(ctx, &mcp.CallToolParams{
				Name: "manage_memory",
				Arguments: map[string]any{
					"action":    "propose",
					"type":      "preference",
					"workspace": "github.com/duck8823/traceary",
					"fact":      fmt.Sprintf("Candidate memory %d for inbox batch", i),
					"evidence_refs": []any{
						map[string]any{"kind": "issue", "value": "#557"},
					},
				},
			})
			if err != nil {
				t.Fatalf("CallTool(propose_memory) #%d error = %v", i, err)
			}
			batchProposeIDs = append(batchProposeIDs, extractJSONStringValue(t, res, "memory_id"))
		}
		batchResult, err := clientSession.CallTool(ctx, &mcp.CallToolParams{
			Name: "manage_memory",
			Arguments: map[string]any{
				"action": "accept",
				"ids":    []any{batchProposeIDs[0], batchProposeIDs[1], "not-a-real-id"},
			},
		})
		if err != nil {
			t.Fatalf("CallTool(accept_memories_batch) error = %v", err)
		}
		if batchResult.IsError {
			t.Fatalf("CallTool(accept_memories_batch) returned tool error")
		}
		batchPayload := decodeJSONPayload(t, batchResult)
		if got, want := batchPayload["action"], "accept"; got != want {
			t.Fatalf("batch action = %v, want %v", got, want)
		}
		processed, _ := batchPayload["processed"].([]any)
		if len(processed) != 2 {
			t.Fatalf("expected 2 processed memories, got %d", len(processed))
		}
		failures, _ := batchPayload["failures"].([]any)
		if len(failures) != 1 {
			t.Fatalf("expected 1 failure (not-a-real-id), got %d", len(failures))
		}

		// Accept one more candidate so the export has something concrete
		// to serialise, then exercise the MCP bridge tools end-to-end.
		bridgeProposeResult, err := clientSession.CallTool(ctx, &mcp.CallToolParams{
			Name: "manage_memory",
			Arguments: map[string]any{
				"action":    "propose",
				"type":      "preference",
				"workspace": "github.com/duck8823/traceary",
				"fact":      "Prefer concise PR descriptions",
				"evidence_refs": []any{
					map[string]any{"kind": "issue", "value": "#594"},
				},
			},
		})
		if err != nil {
			t.Fatalf("CallTool(propose_memory) for bridge export error = %v", err)
		}
		bridgeMemoryID := extractJSONStringValue(t, bridgeProposeResult, "memory_id")
		bridgeAcceptResult, err := clientSession.CallTool(ctx, &mcp.CallToolParams{
			Name: "manage_memory",
			Arguments: map[string]any{
				"action":    "accept",
				"memory_id": bridgeMemoryID,
			},
		})
		if err != nil {
			t.Fatalf("CallTool(accept_memory) for bridge export error = %v", err)
		}
		if bridgeAcceptResult.IsError {
			t.Fatalf("CallTool(accept_memory) for bridge export returned tool error")
		}

		exportResult, err := clientSession.CallTool(ctx, &mcp.CallToolParams{
			Name: "query_memory",
			Arguments: map[string]any{
				"action":    "export",
				"target":    "claude",
				"workspace": "github.com/duck8823/traceary",
			},
		})
		if err != nil {
			t.Fatalf("CallTool(export_memories) error = %v", err)
		}
		if exportResult.IsError {
			t.Fatalf("CallTool(export_memories) returned tool error")
		}
		exportPayload := decodeJSONPayload(t, exportResult)
		if got, _ := exportPayload["target"].(string); got != "claude" {
			t.Fatalf("export target = %v, want claude", exportPayload["target"])
		}
		markdown, _ := exportPayload["markdown"].(string)
		if markdown == "" {
			t.Fatalf("export markdown must not be empty")
		}
		if !strings.Contains(markdown, "<!-- traceary-memories:begin:v1 -->") {
			t.Fatalf("export markdown missing managed begin marker: %q", markdown)
		}

		importResult, err := clientSession.CallTool(ctx, &mcp.CallToolParams{
			Name: "manage_memory",
			Arguments: map[string]any{
				"action":    "import_instructions",
				"source":    "claude",
				"markdown":  "# Project\n\n- prefer monospace fonts in the CLI output\n" + markdown,
				"workspace": "github.com/duck8823/traceary",
			},
		})
		if err != nil {
			t.Fatalf("CallTool(import_memory_instructions) error = %v", err)
		}
		if importResult.IsError {
			t.Fatalf("CallTool(import_memory_instructions) returned tool error")
		}
		importPayload := decodeJSONPayload(t, importResult)
		imported, _ := importPayload["imported"].([]any)
		// Only the free-form bullet outside the managed block becomes a
		// candidate; the markdown we exported in-line is inside markers
		// and must not produce a duplicate.
		if len(imported) != 1 {
			t.Fatalf("expected 1 imported candidate from the free-form bullet, got %d", len(imported))
		}
	})

	t.Run("session_handoff and memory_pack expose structured working memory", func(t *testing.T) {
		startResult, err := clientSession.CallTool(ctx, &mcp.CallToolParams{
			Name: "manage_session",
			Arguments: map[string]any{
				"action":    "start",
				"agent":     "claude",
				"workspace": "github.com/duck8823/traceary",
			},
		})
		if err != nil {
			t.Fatalf("CallTool(start_session) error = %v", err)
		}
		sessionID := extractJSONStringValue(t, startResult, "session_id")

		_, err = clientSession.CallTool(ctx, &mcp.CallToolParams{
			Name: "record_event",
			Arguments: map[string]any{
				"type":       "log",
				"message":    "<summary>\n8. Current Work:\n   Wire MCP memory tools\n9. Pending Tasks:\n   Cover MCP server tests\n</summary>",
				"kind":       "compact_summary",
				"agent":      "claude",
				"session_id": sessionID,
				"workspace":  "github.com/duck8823/traceary",
			},
		})
		if err != nil {
			t.Fatalf("CallTool(add_log compact_summary) error = %v", err)
		}
		_, err = clientSession.CallTool(ctx, &mcp.CallToolParams{
			Name: "record_event",
			Arguments: map[string]any{
				"type":       "audit",
				"command":    "go test ./...",
				"agent":      "claude",
				"session_id": sessionID,
				"workspace":  "github.com/duck8823/traceary",
			},
		})
		if err != nil {
			t.Fatalf("CallTool(add_audit) error = %v", err)
		}
		_, err = clientSession.CallTool(ctx, &mcp.CallToolParams{
			Name: "manage_memory",
			Arguments: map[string]any{
				"action":    "remember",
				"type":      "decision",
				"workspace": "github.com/duck8823/traceary",
				"fact":      "MCP session_handoff should be backed by ContextUsecase",
				"evidence_refs": []any{
					map[string]any{"kind": "issue", "value": "#463"},
				},
			},
		})
		if err != nil {
			t.Fatalf("CallTool(remember_memory) error = %v", err)
		}

		handoffResult, err := clientSession.CallTool(ctx, &mcp.CallToolParams{
			Name: "session_status",
			Arguments: map[string]any{
				"action":     "handoff",
				"session_id": sessionID,
			},
		})
		if err != nil {
			t.Fatalf("CallTool(session_handoff) error = %v", err)
		}
		if handoffResult.IsError {
			t.Fatalf("CallTool(session_handoff) returned tool error")
		}
		handoffPayload := decodeJSONPayload(t, handoffResult)
		workingState, ok := handoffPayload["working_state"].(map[string]any)
		if !ok {
			t.Fatalf("working_state type = %T, want map[string]any", handoffPayload["working_state"])
		}
		if diff := cmp.Diff("Wire MCP memory tools | Cover MCP server tests", workingState["compact_summary"]); diff != "" {
			t.Fatalf("compact_summary mismatch (-want +got):\n%s", diff)
		}
		if diff := cmp.Diff("Wire MCP memory tools | Cover MCP server tests", handoffPayload["summary"]); diff != "" {
			t.Fatalf("summary mismatch (-want +got):\n%s", diff)
		}
		if diff := cmp.Diff(workingState["combined_summary"], handoffPayload["summary"]); diff != "" {
			t.Fatalf("summary compatibility mismatch (-want +got):\n%s", diff)
		}
		memories, ok := handoffPayload["memories"].([]any)
		if !ok || len(memories) == 0 {
			t.Fatalf("handoff memories = %T len=%d, want non-empty []any", handoffPayload["memories"], len(memories))
		}

		packResult, err := clientSession.CallTool(ctx, &mcp.CallToolParams{
			Name: "query_memory",
			Arguments: map[string]any{
				"action":                "pack",
				"session_id":            sessionID,
				"recent_commands_limit": 1,
				"memory_limit":          1,
			},
		})
		if err != nil {
			t.Fatalf("CallTool(memory_pack) error = %v", err)
		}
		if packResult.IsError {
			t.Fatalf("CallTool(memory_pack) returned tool error")
		}
		packPayload := decodeJSONPayload(t, packResult)
		recentCommands, ok := packPayload["recent_commands"].([]any)
		if !ok || len(recentCommands) != 1 {
			t.Fatalf("recent_commands = %T len=%d, want 1", packPayload["recent_commands"], len(recentCommands))
		}
	})

	t.Run("memory_pack preserves explicit zero limits", func(t *testing.T) {
		startResult, err := clientSession.CallTool(ctx, &mcp.CallToolParams{
			Name: "manage_session",
			Arguments: map[string]any{
				"action":    "start",
				"agent":     "claude",
				"workspace": "github.com/duck8823/traceary",
			},
		})
		if err != nil {
			t.Fatalf("CallTool(start_session) error = %v", err)
		}
		sessionID := extractJSONStringValue(t, startResult, "session_id")

		_, err = clientSession.CallTool(ctx, &mcp.CallToolParams{
			Name: "record_event",
			Arguments: map[string]any{
				"type":       "log",
				"message":    "Decision summary",
				"kind":       "compact_summary",
				"agent":      "claude",
				"session_id": sessionID,
				"workspace":  "github.com/duck8823/traceary",
			},
		})
		if err != nil {
			t.Fatalf("CallTool(add_log compact_summary) error = %v", err)
		}
		_, err = clientSession.CallTool(ctx, &mcp.CallToolParams{
			Name: "record_event",
			Arguments: map[string]any{
				"type":       "audit",
				"command":    "go test ./...",
				"agent":      "claude",
				"session_id": sessionID,
				"workspace":  "github.com/duck8823/traceary",
			},
		})
		if err != nil {
			t.Fatalf("CallTool(add_audit) error = %v", err)
		}
		_, err = clientSession.CallTool(ctx, &mcp.CallToolParams{
			Name: "manage_memory",
			Arguments: map[string]any{
				"action":    "remember",
				"type":      "decision",
				"workspace": "github.com/duck8823/traceary",
				"fact":      "Context packs may disable optional sections",
				"evidence_refs": []any{
					map[string]any{"kind": "issue", "value": "#464"},
				},
			},
		})
		if err != nil {
			t.Fatalf("CallTool(remember_memory) error = %v", err)
		}

		packResult, err := clientSession.CallTool(ctx, &mcp.CallToolParams{
			Name: "query_memory",
			Arguments: map[string]any{
				"action":                "pack",
				"session_id":            sessionID,
				"recent_commands_limit": 0,
				"memory_limit":          0,
			},
		})
		if err != nil {
			t.Fatalf("CallTool(memory_pack) error = %v", err)
		}
		if packResult.IsError {
			t.Fatalf("CallTool(memory_pack) returned tool error")
		}

		packPayload := decodeJSONPayload(t, packResult)
		if recentCommandsValue, exists := packPayload["recent_commands"]; exists {
			recentCommands, ok := recentCommandsValue.([]any)
			if !ok || len(recentCommands) != 0 {
				t.Fatalf("recent_commands = %T len=%d, want omitted or empty []any", recentCommandsValue, len(recentCommands))
			}
		}
		if memoriesValue, exists := packPayload["memories"]; exists {
			memories, ok := memoriesValue.([]any)
			if !ok || len(memories) != 0 {
				t.Fatalf("memories = %T len=%d, want omitted or empty []any", memoriesValue, len(memories))
			}
		}
	})

	t.Run("supersede and expire memory return updated memory details", func(t *testing.T) {
		rememberResult, err := clientSession.CallTool(ctx, &mcp.CallToolParams{
			Name: "manage_memory",
			Arguments: map[string]any{
				"action":    "remember",
				"type":      "constraint",
				"workspace": "github.com/duck8823/traceary",
				"fact":      "Old memory fact",
				"evidence_refs": []any{
					map[string]any{"kind": "issue", "value": "#464"},
				},
			},
		})
		if err != nil {
			t.Fatalf("CallTool(remember_memory) error = %v", err)
		}
		oldID := extractJSONStringValue(t, rememberResult, "memory_id")

		supersedeResult, err := clientSession.CallTool(ctx, &mcp.CallToolParams{
			Name: "manage_memory",
			Arguments: map[string]any{
				"action":    "supersede",
				"target_id": oldID,
				"fact":      "Replacement memory fact",
				"evidence_refs": []any{
					map[string]any{"kind": "issue", "value": "#464"},
				},
			},
		})
		if err != nil {
			t.Fatalf("CallTool(supersede_memory) error = %v", err)
		}
		if supersedeResult.IsError {
			t.Fatalf("CallTool(supersede_memory) returned tool error")
		}
		newID := extractJSONStringValue(t, supersedeResult, "memory_id")
		if newID == "" || newID == oldID {
			t.Fatalf("replacement memory_id = %q, want non-empty and different from %q", newID, oldID)
		}
		if diff := cmp.Diff(oldID, extractJSONStringValue(t, supersedeResult, "supersedes")); diff != "" {
			t.Fatalf("supersedes mismatch (-want +got):\n%s", diff)
		}

		expireResult, err := clientSession.CallTool(ctx, &mcp.CallToolParams{
			Name: "manage_memory",
			Arguments: map[string]any{
				"action":     "expire",
				"memory_id":  newID,
				"expires_at": "2026-04-13T00:00:00Z",
			},
		})
		if err != nil {
			t.Fatalf("CallTool(expire_memory) error = %v", err)
		}
		if expireResult.IsError {
			t.Fatalf("CallTool(expire_memory) returned tool error")
		}
		if diff := cmp.Diff("expired", extractJSONStringValue(t, expireResult, "status")); diff != "" {
			t.Fatalf("expired status mismatch (-want +got):\n%s", diff)
		}
	})

	t.Run("add_log redacts transcript kind body using built-in redactors", func(t *testing.T) {
		result, err := clientSession.CallTool(ctx, &mcp.CallToolParams{
			Name: "record_event",
			Arguments: map[string]any{
				"type":       "log",
				"message":    "assistant echoed back: Authorization: Bearer abc.def.ghi-should-not-leak",
				"kind":       "transcript",
				"agent":      "claude",
				"session_id": "session-transcript-mcp",
				"workspace":  "github.com/duck8823/traceary",
			},
		})
		if err != nil {
			t.Fatalf("CallTool(add_log transcript) error = %v", err)
		}
		if result.IsError {
			t.Fatalf("CallTool(add_log transcript) returned tool error")
		}
		body := extractJSONStringValue(t, result, "body")
		if strings.Contains(body, "abc.def.ghi-should-not-leak") {
			t.Errorf("transcript body leaked bearer token: %q", body)
		}
		if !strings.Contains(body, "[REDACTED]") {
			t.Errorf("transcript body missing [REDACTED] placeholder: %q", body)
		}
	})

	t.Run("add_log preserves prompt body verbatim (no redaction by design)", func(t *testing.T) {
		result, err := clientSession.CallTool(ctx, &mcp.CallToolParams{
			Name: "record_event",
			Arguments: map[string]any{
				"type":       "log",
				"message":    "user prompt with password=intent: preserved-by-design",
				"kind":       "prompt",
				"agent":      "claude",
				"session_id": "session-prompt-mcp",
				"workspace":  "github.com/duck8823/traceary",
			},
		})
		if err != nil {
			t.Fatalf("CallTool(add_log prompt) error = %v", err)
		}
		if result.IsError {
			t.Fatalf("CallTool(add_log prompt) returned tool error")
		}
		body := extractJSONStringValue(t, result, "body")
		if !strings.Contains(body, "preserved-by-design") {
			t.Errorf("prompt body should preserve operator intent verbatim, got: %q", body)
		}
	})

	t.Run("set_memory_validity accepts bounds and clear_valid_to separately", func(t *testing.T) {
		rememberResult, err := clientSession.CallTool(ctx, &mcp.CallToolParams{
			Name: "manage_memory",
			Arguments: map[string]any{
				"action":    "remember",
				"type":      "decision",
				"workspace": "github.com/duck8823/traceary",
				"fact":      "Memory used for validity plumbing test",
				"evidence_refs": []any{
					map[string]any{"kind": "issue", "value": "#629"},
				},
			},
		})
		if err != nil {
			t.Fatalf("CallTool(remember_memory) error = %v", err)
		}
		memoryID := extractJSONStringValue(t, rememberResult, "memory_id")
		if memoryID == "" {
			t.Fatalf("memory_id = empty")
		}

		setResult, err := clientSession.CallTool(ctx, &mcp.CallToolParams{
			Name: "manage_memory",
			Arguments: map[string]any{
				"action":     "set_validity",
				"memory_id":  memoryID,
				"valid_from": "2026-04-20T00:00:00Z",
				"valid_to":   "2026-07-01T00:00:00Z",
			},
		})
		if err != nil {
			t.Fatalf("CallTool(set_memory_validity) error = %v", err)
		}
		if setResult.IsError {
			t.Fatalf("CallTool(set_memory_validity) IsError = true, want false")
		}

		clearResult, err := clientSession.CallTool(ctx, &mcp.CallToolParams{
			Name: "manage_memory",
			Arguments: map[string]any{
				"action":         "set_validity",
				"memory_id":      memoryID,
				"clear_valid_to": true,
			},
		})
		if err != nil {
			t.Fatalf("CallTool(set_memory_validity clear) error = %v", err)
		}
		if clearResult.IsError {
			t.Fatalf("CallTool(set_memory_validity clear) IsError = true, want false")
		}

		conflictResult, err := clientSession.CallTool(ctx, &mcp.CallToolParams{
			Name: "manage_memory",
			Arguments: map[string]any{
				"action":         "set_validity",
				"memory_id":      memoryID,
				"valid_to":       "2026-12-31T00:00:00Z",
				"clear_valid_to": true,
			},
		})
		if err != nil {
			t.Fatalf("CallTool(set_memory_validity conflict) error = %v", err)
		}
		if !conflictResult.IsError {
			t.Fatalf("CallTool(set_memory_validity conflict) IsError = false, want true")
		}
	})

	t.Run("returns tool error", func(t *testing.T) {
		result, err := clientSession.CallTool(ctx, &mcp.CallToolParams{
			Name: "record_event",
			Arguments: map[string]any{
				"type":    "log",
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

func decodeJSONPayload(t *testing.T, result *mcp.CallToolResult) map[string]any {
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
	return payload
}

func newTestServer(t *testing.T) *mcpserver.Server {
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
    created_at TEXT NOT NULL,
    source_hook TEXT
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
		"000014_add_session_spawn_metadata.sql": {
			Data: []byte(`
ALTER TABLE sessions ADD COLUMN spawn_event_id TEXT;
ALTER TABLE sessions ADD COLUMN subagent_kind TEXT NOT NULL DEFAULT '';
ALTER TABLE sessions ADD COLUMN spawn_order INTEGER;
CREATE INDEX IF NOT EXISTS idx_sessions_parent_spawn_order
    ON sessions(parent_session_id, spawn_order);`),
		},
		"000008_create_memories.sql": {
			Data: []byte(`
CREATE TABLE memories (
    id TEXT PRIMARY KEY,
    type TEXT NOT NULL,
    scope_kind TEXT NOT NULL,
    scope_value TEXT NOT NULL,
    fact TEXT NOT NULL,
    status TEXT NOT NULL,
    confidence TEXT NOT NULL,
    source TEXT NOT NULL,
    supersedes_memory_id TEXT REFERENCES memories(id),
    expires_at TEXT,
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL
);

CREATE INDEX idx_memories_scope_status_updated
    ON memories(scope_kind, scope_value, status, updated_at DESC, id DESC);

CREATE INDEX idx_memories_type_status_updated
    ON memories(type, status, updated_at DESC, id DESC);

CREATE INDEX idx_memories_supersedes_memory_id
    ON memories(supersedes_memory_id);

CREATE TABLE memory_evidence_refs (
    memory_id TEXT NOT NULL REFERENCES memories(id) ON DELETE CASCADE,
    ordinal INTEGER NOT NULL,
    ref_kind TEXT NOT NULL,
    ref_value TEXT NOT NULL,
    PRIMARY KEY (memory_id, ordinal)
);

CREATE INDEX idx_memory_evidence_refs_lookup
    ON memory_evidence_refs(ref_kind, ref_value);

CREATE TABLE memory_artifact_refs (
    memory_id TEXT NOT NULL REFERENCES memories(id) ON DELETE CASCADE,
    ordinal INTEGER NOT NULL,
    ref_kind TEXT NOT NULL,
    ref_value TEXT NOT NULL,
    PRIMARY KEY (memory_id, ordinal)
);

CREATE INDEX idx_memory_artifact_refs_lookup
    ON memory_artifact_refs(ref_kind, ref_value);`),
		},
		"000009_add_memory_validity_window.sql": {
			Data: []byte(`
ALTER TABLE memories ADD COLUMN valid_from TEXT;
ALTER TABLE memories ADD COLUMN valid_to TEXT;
UPDATE memories SET valid_from = created_at WHERE valid_from IS NULL;
CREATE INDEX idx_memories_valid_window ON memories(valid_to, valid_from);`),
		},
	}
	dbPath := filepath.Join(t.TempDir(), "traceary", "traceary.db")
	db := sqlite.NewDatabase(dbPath, migrations)
	eventDatasource := sqlite.NewEventDatasource(db)
	sessionDatasource := sqlite.NewSessionDatasource(db)
	memoryDatasource := sqlite.NewMemoryDatasource(db)
	storeManagementDatasource := sqlite.NewStoreManagementDatasource(db)

	eventUsecase := usecase.NewEventUsecase(eventDatasource, eventDatasource)
	sessionUsecase := usecase.NewSessionUsecase(eventDatasource, sessionDatasource, sessionDatasource, eventDatasource)
	memoryUsecase := usecase.NewMemoryUsecase(memoryDatasource, memoryDatasource, nil)
	contextUsecase := usecase.NewContextUsecase(sessionDatasource, eventDatasource, memoryDatasource)
	storeManagementUsecase := usecase.NewStoreManagementUsecase(storeManagementDatasource)

	server, err := mcpserver.NewServer(
		"test-version",
		nil,
		eventUsecase,
		sessionUsecase,
		memoryUsecase,
		contextUsecase,
		storeManagementUsecase,
	)
	if err != nil {
		t.Fatalf("NewServer() error = %v", err)
	}

	return server
}
