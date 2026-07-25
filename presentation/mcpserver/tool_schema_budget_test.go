package mcpserver_test

import (
	"bytes"
	"encoding/json"
	"flag"
	"os"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

const (
	maxListToolsBytes     = 45056
	maxInputSchemaBytes   = 18432
	budgetWarningPercent  = 90
	toolBudgetFixturePath = "testdata/tool_schema_budget.golden.json"
)

var updateToolSchemaBudget = flag.Bool("update-tool-schema-budget", false, "update MCP tool schema budget fixture")

// maxToolAdvertisementBytes is deliberately hand-edited policy, not a value
// derived from the fixture. It leaves bounded headroom for compatible schema
// evolution while forcing a reviewed budget decision before one tool absorbs a
// disproportionate part of tools/list.
var maxToolAdvertisementBytes = map[string]int{
	"get_context":    5500,
	"get_report":     13500,
	"list_events":    6500,
	"manage_memory":  3200,
	"manage_session": 3100,
	"query_memory":   3000,
	"record_event":   4000,
	"search":         6000,
	"session_status": 2300,
}

type toolSchemaBudgetReport struct {
	ListToolsBytes   int                    `json:"listToolsBytes"`
	InputSchemaBytes int                    `json:"inputSchemaBytes"`
	Tools            []toolSchemaBudgetTool `json:"tools"`
}

type toolSchemaBudgetTool struct {
	Name              string `json:"name"`
	ToolBytes         int    `json:"toolBytes"`
	InputSchemaBytes  int    `json:"inputSchemaBytes"`
	OutputSchemaBytes int    `json:"outputSchemaBytes"`
}

type budgetStatus string

const (
	budgetPass    budgetStatus = "pass"
	budgetWarning budgetStatus = "warning"
	budgetFailure budgetStatus = "failure"
)

// TestServer_ToolAdvertisementBudget measures the exact JSON values returned
// by runtime tools/list. The fixture is a focused CI report: it makes per-tool
// schema growth reviewable even when the aggregate hard limit remains safe.
func TestServer_ToolAdvertisementBudget(t *testing.T) {
	result := runtimeToolAdvertisement(t)
	report := collectToolSchemaBudget(t, result)

	encoded := encodePrettyJSON(t, report)
	if *updateToolSchemaBudget {
		if err := os.WriteFile(toolBudgetFixturePath, encoded, 0o644); err != nil {
			t.Fatalf("write fixture %q: %v", toolBudgetFixturePath, err)
		}
	}
	want, err := os.ReadFile(toolBudgetFixturePath)
	if err != nil {
		t.Fatalf("read fixture %q: %v", toolBudgetFixturePath, err)
	}
	if diff := cmp.Diff(string(want), string(encoded)); diff != "" {
		t.Fatalf("MCP tool schema budget report mismatch %q (-want +got):\n%s\n\nIf this change is intentional, regenerate with:\n\tgo test ./presentation/mcpserver -run TestServer_ToolAdvertisementBudget -update-tool-schema-budget\n", toolBudgetFixturePath, diff)
	}

	assertToolSchemaBudget(t, "tools/list", report.ListToolsBytes, maxListToolsBytes)
	assertToolSchemaBudget(t, "all input schemas", report.InputSchemaBytes, maxInputSchemaBytes)
	for _, tool := range report.Tools {
		limit, ok := maxToolAdvertisementBytes[tool.Name]
		if !ok {
			t.Fatalf("missing hand-edited advertisement budget for tool %q", tool.Name)
		}
		assertToolSchemaBudget(t, "tool "+tool.Name, tool.ToolBytes, limit)
	}
	if len(maxToolAdvertisementBytes) != len(report.Tools) {
		t.Fatalf("tool advertisement budget count = %d, want %d", len(maxToolAdvertisementBytes), len(report.Tools))
	}

	t.Logf("MCP tool schema budget: tools/list=%d/%d B, inputSchemas=%d/%d B", report.ListToolsBytes, maxListToolsBytes, report.InputSchemaBytes, maxInputSchemaBytes)
}

func TestToolSchemaBudgetStatus(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name          string
		actual, limit int
		want          budgetStatus
	}{
		{name: "below warning", actual: 89, limit: 100, want: budgetPass},
		{name: "at warning", actual: 90, limit: 100, want: budgetWarning},
		{name: "below hard limit", actual: 99, limit: 100, want: budgetWarning},
		{name: "at hard limit", actual: 100, limit: 100, want: budgetWarning},
		{name: "over hard limit", actual: 101, limit: 100, want: budgetFailure},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := classifyToolSchemaBudget(tc.actual, tc.limit); got != tc.want {
				t.Fatalf("classifyToolSchemaBudget(%d, %d) = %q, want %q", tc.actual, tc.limit, got, tc.want)
			}
		})
	}
}

func collectToolSchemaBudget(t *testing.T, result *mcp.ListToolsResult) toolSchemaBudgetReport {
	t.Helper()

	listToolsJSON := encodeCompactJSON(t, result)
	report := toolSchemaBudgetReport{ListToolsBytes: len(listToolsJSON)}
	for _, tool := range sortedTools(result.Tools) {
		inputSchemaJSON := encodeCompactJSON(t, tool.InputSchema)
		outputSchemaBytes := 0
		if tool.OutputSchema != nil {
			outputSchemaBytes = len(encodeCompactJSON(t, tool.OutputSchema))
		}
		report.InputSchemaBytes += len(inputSchemaJSON)
		report.Tools = append(report.Tools, toolSchemaBudgetTool{
			Name:              tool.Name,
			ToolBytes:         len(encodeCompactJSON(t, tool)),
			InputSchemaBytes:  len(inputSchemaJSON),
			OutputSchemaBytes: outputSchemaBytes,
		})
	}
	return report
}

func assertToolSchemaBudget(t *testing.T, name string, actual, limit int) {
	t.Helper()
	switch status := classifyToolSchemaBudget(actual, limit); status {
	case budgetFailure:
		t.Fatalf("MCP tool schema budget exceeded for %s: %d B > %d B", name, actual, limit)
	case budgetWarning:
		t.Logf("WARNING: MCP tool schema budget is at least %d%% for %s: %d/%d B", budgetWarningPercent, name, actual, limit)
	}
}

func classifyToolSchemaBudget(actual, limit int) budgetStatus {
	if actual > limit {
		return budgetFailure
	}
	if actual*100 >= limit*budgetWarningPercent {
		return budgetWarning
	}
	return budgetPass
}

func encodeCompactJSON(t *testing.T, value any) []byte {
	t.Helper()
	encoded, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("marshal compact JSON: %v", err)
	}
	return encoded
}

func encodePrettyJSON(t *testing.T, value any) []byte {
	t.Helper()
	var buf bytes.Buffer
	encoder := json.NewEncoder(&buf)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(value); err != nil {
		t.Fatalf("encode pretty JSON: %v", err)
	}
	return buf.Bytes()
}
