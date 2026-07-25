package mcpserver_test

import (
	"bytes"
	"encoding/json"
	"flag"
	"os"
	"path/filepath"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

var updateRegistrySnapshot = flag.Bool("update", false, "update MCP tool registry snapshot fixture")

// TestServer_ToolRegistrySnapshot pins the public MCP tool registry
// (tool names, descriptions, annotations, and input/output schemas) so that a
// silent rename, removal, or addition is caught by CI before it can
// drift away from documented and integrator-facing contracts. The
// fixture covers every registered tool — the assertion is exact byte
// equality after sorting tools by name.
//
// To intentionally update the contract — for example, after adding a
// new MCP tool, renaming an action enum, or expanding a description —
// re-run with `-update` and review the diff before committing:
//
//	go test ./presentation/mcpserver -run TestServer_ToolRegistrySnapshot -update
//
// See docs/operations/json-contract-tests.md for the full process.
func TestServer_ToolRegistrySnapshot(t *testing.T) {
	listResult := runtimeToolAdvertisement(t)
	tools := sortedTools(listResult.Tools)

	type toolSnapshot struct {
		Name         string               `json:"name"`
		Description  string               `json:"description"`
		Annotations  *mcp.ToolAnnotations `json:"annotations,omitempty"`
		InputSchema  any                  `json:"inputSchema"`
		OutputSchema any                  `json:"outputSchema,omitempty"`
	}

	snapshots := make([]toolSnapshot, 0, len(tools))
	for _, tool := range tools {
		snapshots = append(snapshots, toolSnapshot{
			Name:         tool.Name,
			Description:  tool.Description,
			Annotations:  tool.Annotations,
			InputSchema:  tool.InputSchema,
			OutputSchema: tool.OutputSchema,
		})
	}

	var buf bytes.Buffer
	encoder := json.NewEncoder(&buf)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(snapshots); err != nil {
		t.Fatalf("encode snapshot: %v", err)
	}

	fixturePath := filepath.Join("testdata", "tool_registry.golden.json")

	if *updateRegistrySnapshot {
		if err := os.MkdirAll(filepath.Dir(fixturePath), 0o755); err != nil {
			t.Fatalf("create fixture directory %q: %v", filepath.Dir(fixturePath), err)
		}
		if err := os.WriteFile(fixturePath, buf.Bytes(), 0o644); err != nil {
			t.Fatalf("write fixture %q: %v", fixturePath, err)
		}
	}

	want, err := os.ReadFile(fixturePath)
	if err != nil {
		t.Fatalf("read fixture %q: %v", fixturePath, err)
	}
	if diff := cmp.Diff(string(want), buf.String()); diff != "" {
		t.Fatalf("MCP tool registry snapshot mismatch %q (-want +got):\n%s\n\nIf this drift is intentional, regenerate with:\n\tgo test ./presentation/mcpserver -run TestServer_ToolRegistrySnapshot -update\n", fixturePath, diff)
	}
}
