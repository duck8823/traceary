package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestInspectMCPRegistration(t *testing.T) {
	t.Run("codex toml mcp server passes", func(t *testing.T) {
		home := t.TempDir()
		SetUserHomeDirFunc(func() (string, error) { return home, nil })
		t.Cleanup(ResetUserHomeDirFunc)
		configPath := filepath.Join(home, ".codex", "config.toml")
		if err := os.MkdirAll(filepath.Dir(configPath), 0o755); err != nil {
			t.Fatalf("MkdirAll() error = %v", err)
		}
		if err := os.WriteFile(configPath, []byte("[mcp_servers.traceary]\ncommand = \"traceary\"\nargs = [\"mcp-server\"]\n"), 0o644); err != nil {
			t.Fatalf("WriteFile() error = %v", err)
		}

		got := (&RootCLI{}).inspectMCPRegistrationForClient("codex", "")
		if got.Status != doctorStatusPass {
			t.Fatalf("status = %q, want pass; msg=%q", got.Status, got.Message)
		}
		if got.Section != "" {
			// Section is filled by finalizeDoctorReport, not individual checks.
			t.Fatalf("unexpected section before finalize: %q", got.Section)
		}
	})

	t.Run("claude config without traceary mcp warns with fix command", func(t *testing.T) {
		settings := filepath.Join(t.TempDir(), "settings.json")
		if err := os.WriteFile(settings, []byte(`{"mcpServers": {}}`), 0o644); err != nil {
			t.Fatalf("WriteFile() error = %v", err)
		}
		got := inspectJSONMCPRegistration("claude-mcp", "claude", []string{settings}, "traceary mcp-server")
		if got.Status != doctorStatusWarn {
			t.Fatalf("status = %q, want warn", got.Status)
		}
		if got.FixCommand == "" {
			t.Fatalf("FixCommand is empty")
		}
	})
	t.Run("json config read errors fail instead of falling through", func(t *testing.T) {
		dir := t.TempDir()
		unreadable := filepath.Join(dir, "settings.json")
		readable := filepath.Join(dir, "fallback.json")
		if err := os.WriteFile(unreadable, []byte(`{"mcpServers":{"traceary":{"command":"traceary","args":["mcp-server"]}}}`), 0o644); err != nil {
			t.Fatalf("WriteFile(unreadable) error = %v", err)
		}
		if err := os.Chmod(unreadable, 0o000); err != nil {
			t.Fatalf("Chmod() error = %v", err)
		}
		t.Cleanup(func() { _ = os.Chmod(unreadable, 0o644) })
		if err := os.WriteFile(readable, []byte(`{"mcpServers":{"traceary":{"command":"traceary","args":["mcp-server"]}}}`), 0o644); err != nil {
			t.Fatalf("WriteFile(readable) error = %v", err)
		}

		got := inspectJSONMCPRegistration("claude-mcp", "claude", []string{unreadable, readable}, "traceary mcp-server")
		if got.Status != doctorStatusFail {
			t.Fatalf("status = %q, want fail; msg=%q", got.Status, got.Message)
		}
		if !strings.Contains(got.Message, "failed to read claude MCP config") || !strings.Contains(got.Message, unreadable) {
			t.Fatalf("message should include read failure and path, got %q", got.Message)
		}
	})

}

func TestWriteJSONMCPRegistrationPreservesExistingServerFields(t *testing.T) {
	settings := filepath.Join(t.TempDir(), "settings.json")
	if err := os.WriteFile(settings, []byte(`{
  "mcpServers": {
    "filesystem": {
      "command": "node",
      "args": ["server.js"],
      "env": {"TOKEN": "secret"},
      "cwd": "/tmp/work",
      "transport": {"type": "stdio"}
    }
  }
}`), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	if err := writeJSONMCPRegistration(settings); err != nil {
		t.Fatalf("writeJSONMCPRegistration() error = %v", err)
	}

	content, err := os.ReadFile(settings)
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}
	var root struct {
		MCPServers map[string]json.RawMessage `json:"mcpServers"`
	}
	if err := json.Unmarshal(content, &root); err != nil {
		t.Fatalf("json.Unmarshal() error = %v\n%s", err, content)
	}
	var filesystem map[string]any
	if err := json.Unmarshal(root.MCPServers["filesystem"], &filesystem); err != nil {
		t.Fatalf("json.Unmarshal(filesystem) error = %v", err)
	}
	if _, ok := filesystem["env"]; !ok {
		t.Fatalf("filesystem env was dropped: %#v", filesystem)
	}
	if got := filesystem["cwd"]; got != "/tmp/work" {
		t.Fatalf("filesystem cwd = %#v, want /tmp/work", got)
	}
	if _, ok := filesystem["transport"]; !ok {
		t.Fatalf("filesystem transport was dropped: %#v", filesystem)
	}
	var traceary doctorMCPServer
	if err := json.Unmarshal(root.MCPServers["traceary"], &traceary); err != nil {
		t.Fatalf("json.Unmarshal(traceary) error = %v", err)
	}
	if !hasMCPServerCommand(traceary) {
		t.Fatalf("traceary registration = %#v, want traceary mcp-server", traceary)
	}
}

func TestWriteTOMLMCPRegistrationReplacesExistingTracearyTable(t *testing.T) {
	configPath := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(configPath, []byte(`
[mcp_servers.other]
command = "other"
args = ["serve"]

[mcp_servers.traceary]
command = "/stale/bin/traceary"
args = ["mcp-server"]
env = { KEEP = "not-required-for-traceary" }

[plugins."traceary@local"]
enabled = true
`), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	if err := writeTOMLMCPRegistration(configPath); err != nil {
		t.Fatalf("writeTOMLMCPRegistration() error = %v", err)
	}

	content, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}
	text := string(content)
	if got := strings.Count(text, "[mcp_servers.traceary]"); got != 1 {
		t.Fatalf("traceary table count = %d, want 1\n%s", got, text)
	}
	if strings.Contains(text, "/stale/bin/traceary") {
		t.Fatalf("stale traceary command was not replaced:\n%s", text)
	}
	if !strings.Contains(text, "[mcp_servers.other]") || !strings.Contains(text, `[plugins."traceary@local"]`) {
		t.Fatalf("unrelated TOML tables were not preserved:\n%s", text)
	}
	got := (&RootCLI{}).inspectTOMLMCPRegistration("codex-mcp", "codex", configPath, "traceary mcp-server")
	if got.Status != doctorStatusPass {
		t.Fatalf("status = %q, want pass; msg=%q\n%s", got.Status, got.Message, text)
	}
}
