package mcpserver_test

import (
	"context"
	"sort"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

var expectedDefaultToolNames = []string{
	"get_context",
	"get_report",
	"list_events",
	"manage_memory",
	"manage_session",
	"query_memory",
	"record_event",
	"search",
	"session_status",
}

// runtimeToolAdvertisement observes the public tools/list response through the
// in-memory MCP transport. Contract and budget tests must use this helper
// rather than inspecting server registration internals, so they measure what a
// client receives after schema inference.
func runtimeToolAdvertisement(t *testing.T) *mcp.ListToolsResult {
	t.Helper()

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

	result, err := clientSession.ListTools(ctx, nil)
	if err != nil {
		t.Fatalf("ListTools() error = %v", err)
	}

	names := make([]string, 0, len(result.Tools))
	for _, tool := range result.Tools {
		names = append(names, tool.Name)
	}
	sort.Strings(names)
	if diff := cmp.Diff(expectedDefaultToolNames, names); diff != "" {
		t.Fatalf("default tools/list surface mismatch (-want +got):\n%s", diff)
	}
	return result
}

func sortedTools(tools []*mcp.Tool) []*mcp.Tool {
	sorted := make([]*mcp.Tool, len(tools))
	copy(sorted, tools)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].Name < sorted[j].Name })
	return sorted
}
