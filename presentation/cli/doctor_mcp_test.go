package cli

import (
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
