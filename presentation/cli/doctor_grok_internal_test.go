package cli

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func TestBuildGrokDoctorChecks(t *testing.T) {
	healthy := grokDoctorState{CLIAvailable: true, HostVersion: "0.2.99", PluginInstalled: true, PluginEnabled: true, PluginVersion: "0.23.0", ProjectTrusted: true, NativeHooks: true, MCPServers: 1, Skills: 3}
	tests := []struct {
		name       string
		mutate     func(*grokDoctorState)
		check      string
		status     string
		messageSub string
	}{
		{name: "absent CLI", mutate: func(s *grokDoctorState) { *s = grokDoctorState{} }, check: "grok-cli", status: doctorStatusFail, messageSub: "not installed"},
		{name: "absent plugin", mutate: func(s *grokDoctorState) { s.PluginInstalled = false }, check: "grok-plugin", status: doctorStatusWarn, messageSub: "not installed"},
		{name: "version mismatch", mutate: func(s *grokDoctorState) { s.PluginVersion = "0.22.0" }, check: "grok-plugin", status: doctorStatusWarn, messageSub: "does not match"},
		{name: "untrusted project hooks", mutate: func(s *grokDoctorState) { s.ProjectHooks, s.ProjectTrusted = true, false }, check: "grok-hook-trust", status: doctorStatusWarn, messageSub: "not trusted"},
		{name: "missing MCP", mutate: func(s *grokDoctorState) { s.MCPServers = 0 }, check: "grok-mcp", status: doctorStatusWarn, messageSub: "0"},
		{name: "missing skills", mutate: func(s *grokDoctorState) { s.Skills = 2 }, check: "grok-skills", status: doctorStatusWarn, messageSub: "2"},
		{name: "missing hooks", mutate: func(s *grokDoctorState) { s.NativeHooks = false }, check: "grok-hooks", status: doctorStatusWarn, messageSub: "incomplete"},
		{name: "healthy", mutate: func(*grokDoctorState) {}, check: "grok-plugin", status: doctorStatusPass, messageSub: "enabled"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			state := healthy
			tc.mutate(&state)
			checks := buildGrokDoctorChecks(state, "0.23.0")
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

func TestBuildGrokDoctorChecksSkipsParityForDevelopmentVersion(t *testing.T) {
	state := grokDoctorState{CLIAvailable: true, HostVersion: "0.2.99", PluginInstalled: true, PluginEnabled: true, PluginVersion: "0.22.0", ProjectTrusted: true, NativeHooks: true, MCPServers: 1, Skills: 3}
	checks := buildGrokDoctorChecks(state, "dev (commit=none)")
	for _, check := range checks {
		if check.Name == "grok-plugin" && check.Status != doctorStatusPass {
			t.Fatalf("development version parity check = %+v, want pass", check)
		}
	}
}

func TestProbeGrokDoctorStateUsesHostInventoryAndHookFile(t *testing.T) {
	originalLookPath, originalOutput := grokDoctorLookPath, grokDoctorOutput
	t.Cleanup(func() { grokDoctorLookPath, grokDoctorOutput = originalLookPath, originalOutput })
	grokDoctorLookPath = func(string) (string, error) { return "/usr/local/bin/grok", nil }

	projectDir := t.TempDir()
	pluginHook := filepath.Join(t.TempDir(), "hooks.json")
	writeGrokDoctorHookFixture(t, pluginHook, true)
	calls := []string{}
	grokDoctorOutput = func(_ context.Context, args ...string) ([]byte, error) {
		calls = append(calls, strings.Join(args, " "))
		switch strings.Join(args, " ") {
		case "--version":
			return []byte("grok 0.2.99 (build)\n"), nil
		case "plugin list --json":
			return []byte(`[{"name":"traceary","version":"0.23.0"}]`), nil
		case "--cwd " + projectDir + " inspect --json":
			return []byte(`{"projectTrusted":true,"plugins":[{"name":"traceary","enabled":true,"provides":{"skills":3,"hooks":true,"mcpServers":1}}],"hooks":[{"target":` + strconv.Quote(pluginHook) + `,"source":{"type":"plugin","plugin_name":"traceary"}}]}`), nil
		default:
			t.Fatalf("unexpected Grok arguments: %v", args)
			return nil, nil
		}
	}

	state, err := probeGrokDoctorState(context.Background(), projectDir)
	if err != nil {
		t.Fatalf("probeGrokDoctorState() error = %v", err)
	}
	if !state.CLIAvailable || state.HostVersion != "0.2.99" || !state.PluginInstalled || !state.PluginEnabled || state.PluginVersion != "0.23.0" || !state.NativeHooks || state.MCPServers != 1 || state.Skills != 3 {
		t.Fatalf("state = %+v, want healthy host inventory", state)
	}
	if got, want := strings.Join(calls, "|"), "--version|plugin list --json|--cwd "+projectDir+" inspect --json"; got != want {
		t.Fatalf("Grok calls = %q, want %q", got, want)
	}
}

func TestProbeGrokDoctorStateDoesNotTrustProvidesHooksBoolean(t *testing.T) {
	originalLookPath, originalOutput := grokDoctorLookPath, grokDoctorOutput
	t.Cleanup(func() { grokDoctorLookPath, grokDoctorOutput = originalLookPath, originalOutput })
	grokDoctorLookPath = func(string) (string, error) { return "/usr/local/bin/grok", nil }
	projectDir := t.TempDir()
	projectHook := filepath.Join(projectDir, ".grok", "hooks", "traceary.json")
	if err := os.MkdirAll(filepath.Dir(projectHook), 0o700); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	if err := os.WriteFile(projectHook, []byte(`{"hooks":{}}`), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	pluginHook := filepath.Join(t.TempDir(), "hooks.json")
	writeGrokDoctorHookFixture(t, pluginHook, true)
	var file map[string]any
	data, err := os.ReadFile(pluginHook)
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}
	if err := json.Unmarshal(data, &file); err != nil {
		t.Fatalf("Unmarshal() error = %v", err)
	}
	hooks := file["hooks"].(map[string]any)
	routes := hooks["PostCompact"].([]any)
	commands := routes[0].(map[string]any)["hooks"].([]any)
	commands[0].(map[string]any)["command"] = `"${GROK_PLUGIN_ROOT}/scripts/traceary-grok.sh" "stop"`
	data, err = json.Marshal(file)
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}
	if err := os.WriteFile(pluginHook, data, 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	grokDoctorOutput = func(_ context.Context, args ...string) ([]byte, error) {
		switch strings.Join(args, " ") {
		case "--version":
			return []byte("grok 0.2.99\n"), nil
		case "plugin list --json":
			return []byte(`[{"name":"traceary","version":"0.23.0"}]`), nil
		default:
			return []byte(`{"projectTrusted":false,"plugins":[{"name":"traceary","enabled":true,"provides":{"skills":3,"hooks":true,"mcpServers":1}}],"hooks":[{"target":` + strconv.Quote(pluginHook) + `,"source":{"type":"plugin","plugin_name":"traceary"}}]}`), nil
		}
	}
	state, err := probeGrokDoctorState(context.Background(), projectDir)
	if err != nil {
		t.Fatalf("probeGrokDoctorState() error = %v", err)
	}
	if state.NativeHooks || !state.ProjectHooks || state.ProjectTrusted {
		t.Fatalf("state = %+v, invalid hook contract must not pass and untrusted project hooks must be detected", state)
	}
}

func writeGrokDoctorHookFixture(t *testing.T, path string, complete bool) {
	t.Helper()
	contracts := []struct{ event, name, action string }{
		{"SessionStart", "traceary-session-start", "session-start"},
		{"UserPromptSubmit", "traceary-prompt", "user-prompt-submit"},
		{"PreToolUse", "traceary-tool-pre", "pre-tool-use"},
		{"PostToolUse", "traceary-audit", "post-tool-use"},
		{"Stop", "traceary-stop", "stop"},
		{"PreCompact", "traceary-compact-pre", "pre-compact"},
		{"PostCompact", "traceary-compact-post", "post-compact"},
	}
	if !complete {
		contracts = contracts[:1]
	}
	hooks := map[string]any{}
	for _, contract := range contracts {
		hooks[contract.event] = []any{map[string]any{"hooks": []any{map[string]any{
			"name":    contract.name,
			"type":    "command",
			"command": `"${GROK_PLUGIN_ROOT}/scripts/traceary-grok.sh" "` + contract.action + `"`,
			"timeout": 5,
		}}}}
	}
	data, err := json.Marshal(map[string]any{"hooks": hooks})
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
}
