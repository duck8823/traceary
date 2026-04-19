package mcpserver_test

import (
	"context"
	"testing"
	"unicode/utf8"

	"github.com/google/go-cmp/cmp"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// maxDescriptionRunes is the upper bound we enforce on MCP tool descriptions
// so they stay friendly to Tool Search indexing.
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
		{
			name: "add_log",
			want: toolMetadataExpectation{
				description:      "Add a log event, note, prompt, or compact summary.",
				destructiveFalse: true,
			},
		},
		{
			name: "start_session",
			want: toolMetadataExpectation{
				description:      "Start a session and record a session_started event.",
				destructiveFalse: true,
			},
		},
		{
			name: "end_session",
			want: toolMetadataExpectation{
				description:     "End a session and record a session_ended event.",
				destructiveTrue: true,
			},
		},
		{
			name: "latest_session",
			want: toolMetadataExpectation{
				description: "Get the latest session for resume or handoff by agent, client, or workspace.",
				readOnly:    true,
			},
		},
		{
			name: "active_session",
			want: toolMetadataExpectation{
				description: "Get the active or open session for resume by agent, client, or workspace.",
				readOnly:    true,
			},
		},
		{
			name: "list_events",
			want: toolMetadataExpectation{
				description: "List recent events, logs, audits, prompts, and summaries.",
				readOnly:    true,
			},
		},
		{
			name: "add_audit",
			want: toolMetadataExpectation{
				description:      "Add a shell command audit log with redacted input and output.",
				destructiveFalse: true,
			},
		},
		{
			name: "search",
			want: toolMetadataExpectation{
				description: "Search events, logs, audits, prompts, and summaries by text, time, or workspace.",
				readOnly:    true,
			},
		},
		{
			name: "get_context",
			want: toolMetadataExpectation{
				description: "Get recent context events, logs, audits, prompts, and summaries for a session or workspace.",
				readOnly:    true,
			},
		},
		{
			name: "session_handoff",
			want: toolMetadataExpectation{
				description: "Get a session handoff summary for resume, context, memory, and recent commands.",
				readOnly:    true,
			},
		},
		{
			name: "retrieve_memories",
			want: toolMetadataExpectation{
				description: "Retrieve durable memories by ID, query, status, type, agent, or workspace.",
				readOnly:    true,
			},
		},
		{
			name: "remember_memory",
			want: toolMetadataExpectation{
				description:      "Remember and record an accepted durable memory with evidence and artifacts.",
				destructiveFalse: true,
			},
		},
		{
			name: "propose_memory",
			want: toolMetadataExpectation{
				description:      "Propose and record a candidate durable memory for review.",
				destructiveFalse: true,
			},
		},
		{
			name: "accept_memory",
			want: toolMetadataExpectation{
				description:     "Accept a candidate durable memory and set confidence.",
				destructiveTrue: true,
			},
		},
		{
			name: "reject_memory",
			want: toolMetadataExpectation{
				description:     "Reject a candidate durable memory from review.",
				destructiveTrue: true,
			},
		},
		{
			name: "supersede_memory",
			want: toolMetadataExpectation{
				description:     "Supersede an accepted durable memory with a replacement memory.",
				destructiveTrue: true,
			},
		},
		{
			name: "expire_memory",
			want: toolMetadataExpectation{
				description:     "Expire or retire a durable memory at a timestamp.",
				destructiveTrue: true,
			},
		},
		{
			name: "memory_pack",
			want: toolMetadataExpectation{
				description: "Build a memory pack for prompt context, handoff, automation, and recent commands.",
				readOnly:    true,
			},
		},
		{
			name: "accept_memories_batch",
			want: toolMetadataExpectation{
				description:     "Batch accept candidate durable memories by id, mirroring `traceary memory inbox accept --ids`.",
				destructiveTrue: true,
			},
		},
		{
			name: "reject_memories_batch",
			want: toolMetadataExpectation{
				description:     "Batch reject candidate durable memories by id, mirroring `traceary memory inbox reject --ids`.",
				destructiveTrue: true,
			},
		},
	}

	actual := indexTools(listResult.Tools)

	if len(listResult.Tools) != len(cases) {
		t.Errorf("registered tool count = %d, want %d", len(listResult.Tools), len(cases))
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			tool, ok := actual[tc.name]
			if !ok {
				t.Fatalf("tool %q not registered", tc.name)
			}

			if diff := cmp.Diff(tc.want.description, tool.Description); diff != "" {
				t.Errorf("description mismatch (-want +got):\n%s", diff)
			}

			runes := utf8.RuneCountInString(tool.Description)
			if runes >= maxDescriptionRunes {
				t.Errorf("description length = %d runes, want < %d", runes, maxDescriptionRunes)
			}

			annotations := tool.Annotations
			if annotations == nil {
				t.Fatalf("annotations are nil")
			}

			if tc.want.readOnly {
				if !annotations.ReadOnlyHint {
					t.Errorf("ReadOnlyHint = false, want true for read-only tool")
				}
				if annotations.DestructiveHint != nil {
					t.Errorf("DestructiveHint = %v, want nil for read-only tool", *annotations.DestructiveHint)
				}
			}
			if tc.want.destructiveFalse {
				if annotations.ReadOnlyHint {
					t.Errorf("ReadOnlyHint = true, want false for additive write tool")
				}
				if annotations.DestructiveHint == nil {
					t.Errorf("DestructiveHint = nil, want false pointer")
				} else if *annotations.DestructiveHint {
					t.Errorf("DestructiveHint = true, want false for additive write tool")
				}
			}
			if tc.want.destructiveTrue {
				if annotations.ReadOnlyHint {
					t.Errorf("ReadOnlyHint = true, want false for mutating tool")
				}
				if annotations.DestructiveHint == nil {
					t.Errorf("DestructiveHint = nil, want true pointer")
				} else if !*annotations.DestructiveHint {
					t.Errorf("DestructiveHint = false, want true for mutating tool")
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
