package cli

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestBuildKimiDoctorChecks(t *testing.T) {
	healthy := kimiDoctorState{CLIAvailable: true, HostVersion: "0.27.0", PluginInstalled: true, PluginEnabled: true, PluginRecordKnown: true, PluginVersion: "0.28.0", NativeHooks: true, PluginMCP: true, Skills: 3}
	tests := []struct {
		name       string
		mutate     func(*kimiDoctorState)
		check      string
		status     string
		messageSub string
	}{
		{name: "absent CLI", mutate: func(s *kimiDoctorState) { *s = kimiDoctorState{} }, check: "kimi-cli", status: doctorStatusFail, messageSub: "not installed"},
		{name: "absent plugin", mutate: func(s *kimiDoctorState) { s.PluginInstalled = false }, check: "kimi-plugin", status: doctorStatusWarn, messageSub: "not installed"},
		{name: "unknown activation record", mutate: func(s *kimiDoctorState) { s.PluginRecordKnown = false }, check: "kimi-plugin", status: doctorStatusWarn, messageSub: "activation record"},
		{name: "disabled plugin", mutate: func(s *kimiDoctorState) { s.PluginEnabled = false }, check: "kimi-plugin", status: doctorStatusWarn, messageSub: "not enabled"},
		{name: "version mismatch", mutate: func(s *kimiDoctorState) { s.PluginVersion = "0.27.0" }, check: "kimi-plugin", status: doctorStatusWarn, messageSub: "does not match"},
		{name: "incomplete hooks", mutate: func(s *kimiDoctorState) { s.NativeHooks = false }, check: "kimi-hooks", status: doctorStatusWarn, messageSub: "incomplete"},
		{name: "missing MCP", mutate: func(s *kimiDoctorState) { s.PluginMCP = false }, check: "kimi-mcp", status: doctorStatusWarn, messageSub: "no traceary MCP server"},
		{name: "user mcp.json fallback", mutate: func(s *kimiDoctorState) { s.PluginMCP, s.UserMCP = false, true }, check: "kimi-mcp", status: doctorStatusPass, messageSub: "mcp.json"},
		{name: "missing skills", mutate: func(s *kimiDoctorState) { s.Skills = 2 }, check: "kimi-skills", status: doctorStatusWarn, messageSub: "2"},
		{name: "healthy", mutate: func(*kimiDoctorState) {}, check: "kimi-plugin", status: doctorStatusPass, messageSub: "enabled"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			state := healthy
			tc.mutate(&state)
			checks := buildKimiDoctorChecks(state, "0.28.0")
			var found *doctorCheck
			for i := range checks {
				if checks[i].Name == tc.check {
					found = &checks[i]
					break
				}
			}
			if found == nil || found.Status != tc.status || !strings.Contains(found.Message, tc.messageSub) {
				t.Fatalf("checks = %+v, want %s %s containing %q", checks, tc.check, tc.status, tc.messageSub)
			}
			for _, check := range checks {
				if strings.Contains(check.Message+check.Hint, "/private/") {
					t.Fatalf("check exposed private path: %+v", check)
				}
			}
		})
	}
}

func TestBuildKimiDoctorChecksHealthyStatePassesEveryCheck(t *testing.T) {
	state := kimiDoctorState{CLIAvailable: true, HostVersion: "0.27.0", PluginInstalled: true, PluginEnabled: true, PluginRecordKnown: true, PluginVersion: "0.28.0", NativeHooks: true, PluginMCP: true, Skills: 3}
	for _, check := range buildKimiDoctorChecks(state, "0.28.0") {
		if check.Status != doctorStatusPass {
			t.Fatalf("healthy state produced %s %s: %s", check.Name, check.Status, check.Message)
		}
	}
}

func TestProbeKimiDoctorStateReadsManagedPluginAndRecord(t *testing.T) {
	originalLookPath, originalOutput := kimiDoctorLookPath, kimiDoctorOutput
	t.Cleanup(func() { kimiDoctorLookPath, kimiDoctorOutput = originalLookPath, originalOutput })
	kimiDoctorLookPath = func(string) (string, error) { return "/usr/local/bin/kimi", nil }
	kimiDoctorOutput = func(_ context.Context, args ...string) ([]byte, error) {
		if len(args) == 1 && args[0] == "--version" {
			return []byte("0.27.0\n"), nil
		}
		return nil, nil
	}

	kimiHome := t.TempDir()
	t.Setenv("KIMI_CODE_HOME", kimiHome)
	manifestDir := filepath.Join(kimiHome, "plugins", "managed", "traceary")
	if err := os.MkdirAll(filepath.Join(manifestDir, "skills", "traceary-memory-remember"), 0o755); err != nil {
		t.Fatalf("mkdir skills: %v", err)
	}
	for _, skill := range []string{"traceary-memory-remember", "traceary-memory-review", "traceary-session-history"} {
		if err := os.MkdirAll(filepath.Join(manifestDir, "skills", skill), 0o755); err != nil {
			t.Fatalf("mkdir skill %s: %v", skill, err)
		}
		if err := os.WriteFile(filepath.Join(manifestDir, "skills", skill, "SKILL.md"), []byte("# skill\n"), 0o600); err != nil {
			t.Fatalf("write skill: %v", err)
		}
	}
	manifest := `{
  "name": "traceary",
  "version": "0.28.0",
  "mcpServers": {"traceary": {"command": "traceary", "args": ["mcp-server"]}},
  "hooks": [
    {"event": "SessionStart", "command": "traceary hook kimi session-start", "timeout": 5},
    {"event": "SessionEnd", "command": "traceary hook kimi session-end", "timeout": 5},
    {"event": "UserPromptSubmit", "command": "traceary hook kimi user-prompt-submit", "timeout": 5},
    {"event": "PreToolUse", "matcher": "Agent", "command": "traceary hook kimi pre-tool-use", "timeout": 5},
    {"event": "PostToolUse", "command": "traceary hook kimi post-tool-use", "timeout": 5},
    {"event": "PostToolUseFailure", "command": "traceary hook kimi post-tool-use-failure", "timeout": 5},
    {"event": "Stop", "command": "traceary hook kimi stop", "timeout": 5},
    {"event": "SubagentStop", "command": "traceary hook kimi subagent-stop", "timeout": 5},
    {"event": "PreCompact", "command": "traceary hook kimi pre-compact", "timeout": 5},
    {"event": "PostCompact", "command": "traceary hook kimi post-compact", "timeout": 5}
  ]
}`
	if err := os.WriteFile(filepath.Join(manifestDir, "kimi.plugin.json"), []byte(manifest), 0o600); err != nil {
		t.Fatalf("write manifest: %v", err)
	}
	record := `{"plugins": [{"id": "traceary", "root": "` + manifestDir + `", "source": "local-path", "enabled": true, "state": "ok", "installedAt": "2026-07-19T00:00:00Z"}]}`
	if err := os.WriteFile(filepath.Join(kimiHome, "plugins", "installed.json"), []byte(record), 0o600); err != nil {
		t.Fatalf("write record: %v", err)
	}

	state, err := probeKimiDoctorState(context.Background(), t.TempDir())
	if err != nil {
		t.Fatalf("probeKimiDoctorState() error = %v", err)
	}
	if !state.CLIAvailable || state.HostVersion != "0.27.0" {
		t.Fatalf("CLI state = %+v", state)
	}
	if !state.PluginInstalled || !state.PluginEnabled || state.PluginVersion != "0.28.0" {
		t.Fatalf("plugin state = %+v", state)
	}
	if !state.NativeHooks {
		t.Fatal("managed manifest must satisfy the verified hook contract")
	}
	if !state.PluginMCP {
		t.Fatal("managed manifest must declare the traceary MCP server")
	}
	if state.Skills != 3 {
		t.Fatalf("skills = %d, want 3", state.Skills)
	}
}

func TestProbeKimiDoctorStateDetectsUserMCPJSON(t *testing.T) {
	originalLookPath, originalOutput := kimiDoctorLookPath, kimiDoctorOutput
	t.Cleanup(func() { kimiDoctorLookPath, kimiDoctorOutput = originalLookPath, originalOutput })
	kimiDoctorLookPath = func(string) (string, error) { return "/usr/local/bin/kimi", nil }
	kimiDoctorOutput = func(_ context.Context, _ ...string) ([]byte, error) { return []byte("0.27.0\n"), nil }

	kimiHome := t.TempDir()
	t.Setenv("KIMI_CODE_HOME", kimiHome)
	if err := os.WriteFile(filepath.Join(kimiHome, "mcp.json"), []byte(`{"mcpServers": {"traceary": {"command": "traceary", "args": ["mcp-server"]}}}`), 0o600); err != nil {
		t.Fatalf("write mcp.json: %v", err)
	}

	state, err := probeKimiDoctorState(context.Background(), t.TempDir())
	if err != nil {
		t.Fatalf("probeKimiDoctorState() error = %v", err)
	}
	if !state.UserMCP {
		t.Fatal("user-level mcp.json registration must be detected")
	}
	if state.PluginInstalled {
		t.Fatal("no managed copy must mean PluginInstalled=false")
	}
}

func TestKimiMCPJSONRegistersRejectsInvalidDeclarations(t *testing.T) {
	for _, tc := range []struct {
		name string
		body string
	}{
		{name: "null server", body: `{"mcpServers": {"traceary": null}}`},
		{name: "wrong command", body: `{"mcpServers": {"traceary": {"command": "other", "args": ["mcp-server"]}}}`},
		{name: "missing mcp-server arg", body: `{"mcpServers": {"traceary": {"command": "traceary"}}}`},
		{name: "invalid JSON", body: `{not json`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			kimiHome := t.TempDir()
			if err := os.WriteFile(filepath.Join(kimiHome, "mcp.json"), []byte(tc.body), 0o600); err != nil {
				t.Fatalf("write mcp.json: %v", err)
			}
			if kimiMCPJSONRegisters(kimiHome, "") {
				t.Fatalf("kimiMCPJSONRegisters() = true for %s, want false", tc.name)
			}
		})
	}
}

func TestKimiManifestHasVerifiedHooksIsOrderIndependent(t *testing.T) {
	base := `[
    {"event": "SessionStart", "command": "traceary hook kimi session-start", "timeout": 5},
    {"event": "SessionEnd", "command": "traceary hook kimi session-end", "timeout": 5},
    {"event": "UserPromptSubmit", "command": "traceary hook kimi user-prompt-submit", "timeout": 5},
    {"event": "PreToolUse", "matcher": "Agent", "command": "traceary hook kimi pre-tool-use", "timeout": 5},
    {"event": "PostToolUse", "command": "traceary hook kimi post-tool-use", "timeout": 5},
    {"event": "PostToolUseFailure", "command": "traceary hook kimi post-tool-use-failure", "timeout": 5},
    {"event": "Stop", "command": "traceary hook kimi stop", "timeout": 5},
    {"event": "SubagentStop", "command": "traceary hook kimi subagent-stop", "timeout": 5},
    {"event": "PreCompact", "command": "traceary hook kimi pre-compact", "timeout": 5},
    {"event": "PostCompact", "command": "traceary hook kimi post-compact", "timeout": 5}
  ]`

	t.Run("shuffled order still satisfies the contract", func(t *testing.T) {
		shuffled := `[
    {"event": "PostCompact", "command": "traceary hook kimi post-compact", "timeout": 5},
    {"event": "PreCompact", "command": "traceary hook kimi pre-compact", "timeout": 5},
    {"event": "SubagentStop", "command": "traceary hook kimi subagent-stop", "timeout": 5},
    {"event": "Stop", "command": "traceary hook kimi stop", "timeout": 5},
    {"event": "PostToolUseFailure", "command": "traceary hook kimi post-tool-use-failure", "timeout": 5},
    {"event": "PostToolUse", "command": "traceary hook kimi post-tool-use", "timeout": 5},
    {"event": "PreToolUse", "matcher": "Agent", "command": "traceary hook kimi pre-tool-use", "timeout": 5},
    {"event": "UserPromptSubmit", "command": "traceary hook kimi user-prompt-submit", "timeout": 5},
    {"event": "SessionEnd", "command": "traceary hook kimi session-end", "timeout": 5},
    {"event": "SessionStart", "command": "traceary hook kimi session-start", "timeout": 5}
  ]`
		var manifest kimiPluginManifest
		if err := json.Unmarshal([]byte(`{"hooks":`+shuffled+`}`), &manifest); err != nil {
			t.Fatalf("decode shuffled manifest: %v", err)
		}
		if !kimiManifestHasVerifiedHooks(manifest) {
			t.Fatal("shuffled manifest must satisfy the verified hook contract")
		}
	})

	t.Run("extra hook rule is rejected", func(t *testing.T) {
		var manifest kimiPluginManifest
		extra := base[:len(base)-2] + `,
    {"event": "Notification", "command": "traceary hook kimi notification", "timeout": 5}
  ]`
		if err := json.Unmarshal([]byte(`{"hooks":`+extra+`}`), &manifest); err != nil {
			t.Fatalf("decode manifest with extra rule: %v", err)
		}
		if kimiManifestHasVerifiedHooks(manifest) {
			t.Fatal("manifest with an unverified extra rule must be rejected")
		}
	})
}

func TestProbeKimiDoctorStateSanitizesManifestErrors(t *testing.T) {
	originalLookPath, originalOutput := kimiDoctorLookPath, kimiDoctorOutput
	t.Cleanup(func() { kimiDoctorLookPath, kimiDoctorOutput = originalLookPath, originalOutput })
	kimiDoctorLookPath = func(string) (string, error) { return "/usr/local/bin/kimi", nil }
	kimiDoctorOutput = func(_ context.Context, _ ...string) ([]byte, error) { return []byte("0.27.0\n"), nil }

	kimiHome := t.TempDir()
	t.Setenv("KIMI_CODE_HOME", kimiHome)
	manifestDir := filepath.Join(kimiHome, "plugins", "managed", "traceary")
	if err := os.MkdirAll(manifestDir, 0o755); err != nil {
		t.Fatalf("mkdir manifest dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(manifestDir, "kimi.plugin.json"), []byte("{not json"), 0o600); err != nil {
		t.Fatalf("write corrupt manifest: %v", err)
	}

	_, err := probeKimiDoctorState(context.Background(), t.TempDir())
	if err == nil {
		t.Fatal("corrupt manifest must surface a probe error")
	}
	if strings.Contains(err.Error(), kimiHome) || strings.Contains(err.Error(), "/private/") || strings.Contains(err.Error(), "/var/") {
		t.Fatalf("probe error leaked a path: %v", err)
	}
}

func TestKimiPluginRecordEnabledTriState(t *testing.T) {
	t.Run("missing record is unknown", func(t *testing.T) {
		enabled, known := kimiPluginRecordEnabled(t.TempDir())
		if enabled || known {
			t.Fatalf("missing record = (%v, %v), want (false, false)", enabled, known)
		}
	})
	t.Run("corrupt record is unknown", func(t *testing.T) {
		kimiHome := t.TempDir()
		if err := os.MkdirAll(filepath.Join(kimiHome, "plugins"), 0o755); err != nil {
			t.Fatalf("mkdir plugins: %v", err)
		}
		if err := os.WriteFile(filepath.Join(kimiHome, "plugins", "installed.json"), []byte("{not json"), 0o600); err != nil {
			t.Fatalf("write corrupt record: %v", err)
		}
		enabled, known := kimiPluginRecordEnabled(kimiHome)
		if enabled || known {
			t.Fatalf("corrupt record = (%v, %v), want (false, false)", enabled, known)
		}
	})
	t.Run("disabled record is known but not enabled", func(t *testing.T) {
		kimiHome := t.TempDir()
		if err := os.MkdirAll(filepath.Join(kimiHome, "plugins"), 0o755); err != nil {
			t.Fatalf("mkdir plugins: %v", err)
		}
		record := `{"plugins": [{"id": "traceary", "enabled": false, "state": "ok"}]}`
		if err := os.WriteFile(filepath.Join(kimiHome, "plugins", "installed.json"), []byte(record), 0o600); err != nil {
			t.Fatalf("write record: %v", err)
		}
		enabled, known := kimiPluginRecordEnabled(kimiHome)
		if enabled || !known {
			t.Fatalf("disabled record = (%v, %v), want (false, true)", enabled, known)
		}
	})
}
