package mcpserver_test

import (
	"context"
	"testing"
	"unicode/utf8"

	"github.com/google/go-cmp/cmp"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

const maxDescriptionRunes = 120

type toolMetadataExpectation struct {
	description      string
	readOnly         bool
	destructiveFalse bool
	destructiveTrue  bool
}

func TestServer_ToolMetadata(t *testing.T) {
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

	listResult, err := clientSession.ListTools(ctx, nil)
	if err != nil {
		t.Fatalf("ListTools() error = %v", err)
	}

	cases := []struct {
		name string
		want toolMetadataExpectation
	}{
		{name: "manage_memory", want: toolMetadataExpectation{description: "Dispatch durable memory writes by action; reject and expire are destructive actions.", destructiveTrue: true}},
		{name: "query_memory", want: toolMetadataExpectation{description: "Dispatch durable memory reads by action: retrieve, export, pack, or scan_hygiene.", readOnly: true}},
		{name: "manage_session", want: toolMetadataExpectation{description: "Dispatch session lifecycle writes by action: start or end. action=end is destructive (closes the session).", destructiveTrue: true}},
		{name: "session_status", want: toolMetadataExpectation{description: "Dispatch session status reads by action: active, latest, handoff, lineage, or tree.", readOnly: true}},
		{name: "record_event", want: toolMetadataExpectation{description: "Record a log or command audit event by type, returning one uniform event shape.", destructiveFalse: true}},
		{name: "list_events", want: toolMetadataExpectation{description: "List recent events, logs, audits, prompts, transcripts, and summaries.", readOnly: true}},
		{name: "search", want: toolMetadataExpectation{description: "Search events, logs, audits, prompts, transcripts, and summaries by text, time, or workspace.", readOnly: true}},
		{name: "get_context", want: toolMetadataExpectation{description: "Get recent context events, logs, audits, prompts, transcripts, and summaries for a session or workspace.", readOnly: true}},
	}
	if diff := cmp.Diff(8, len(listResult.Tools)); diff != "" {
		t.Fatalf("registered tool count mismatch (-want +got):\n%s", diff)
	}
	indexed := indexTools(listResult.Tools)
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tool, ok := indexed[tc.name]
			if !ok {
				t.Fatalf("tool %q not registered", tc.name)
			}
			if diff := cmp.Diff(tc.want.description, tool.Description); diff != "" {
				t.Errorf("description mismatch (-want +got):\n%s", diff)
			}
			if utf8.RuneCountInString(tool.Description) > maxDescriptionRunes {
				t.Errorf("description is too long: %d runes", utf8.RuneCountInString(tool.Description))
			}
			annotations := tool.Annotations
			if annotations == nil {
				t.Fatalf("annotations = nil")
			}
			if tc.want.readOnly {
				if !annotations.ReadOnlyHint {
					t.Errorf("ReadOnlyHint = false, want true")
				}
				if annotations.DestructiveHint != nil {
					t.Errorf("DestructiveHint = %v, want nil for read-only tool", *annotations.DestructiveHint)
				}
			}
			if tc.want.destructiveFalse {
				if annotations.ReadOnlyHint {
					t.Errorf("ReadOnlyHint = true, want false for additive write tool")
				}
				if annotations.DestructiveHint == nil || *annotations.DestructiveHint {
					t.Errorf("DestructiveHint = %v, want false", annotations.DestructiveHint)
				}
			}
			if tc.want.destructiveTrue {
				if annotations.ReadOnlyHint {
					t.Errorf("ReadOnlyHint = true, want false for mutating tool")
				}
				if annotations.DestructiveHint == nil || !*annotations.DestructiveHint {
					t.Errorf("DestructiveHint = %v, want true", annotations.DestructiveHint)
				}
			}
		})
	}
}

func indexTools(tools []*mcp.Tool) map[string]*mcp.Tool {
	indexed := make(map[string]*mcp.Tool, len(tools))
	for _, tool := range tools {
		indexed[tool.Name] = tool
	}
	return indexed
}
