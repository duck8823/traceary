package cli

import (
	"os"
	"path/filepath"
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
}
