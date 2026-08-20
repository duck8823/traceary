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
	healthy := grokDoctorState{CLIAvailable: true, HostVersion: "0.2.99", PluginInstalled: true, PluginEnabled: true, PluginVersion: "0.23.0", ProjectTrusted: true, NativeHooks: true, MCPServers: 0, Skills: 4}
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
		{name: "missing skills", mutate: func(s *grokDoctorState) { s.Skills = 2 }, check: "grok-skills", status: doctorStatusWarn, messageSub: "2"},
		{name: "missing hooks", mutate: func(s *grokDoctorState) { s.NativeHooks, s.NativeHooksPresent = false, true }, check: "grok-hooks", status: doctorStatusWarn, messageSub: "incomplete"},
		{name: "missing local plugin source", mutate: func(s *grokDoctorState) { s.PluginSource, s.PluginSourceMissing = "/does/not/exist/traceary", true }, check: "grok-plugin", status: doctorStatusFail, messageSub: "does not exist"},
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
	state := grokDoctorState{CLIAvailable: true, HostVersion: "0.2.99", PluginInstalled: true, PluginEnabled: true, PluginVersion: "0.22.0", ProjectTrusted: true, NativeHooks: true, MCPServers: 0, Skills: 4}
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
	pluginHook := filepath.Join(projectDir, "integrations", "grok-plugin", "hooks", "hooks.json")
	writeGrokDoctorHookFixture(t, pluginHook, true)
	calls := []string{}
	grokDoctorOutput = func(_ context.Context, args ...string) ([]byte, error) {
		calls = append(calls, strings.Join(args, " "))
		switch strings.Join(args, " ") {
		case "--version":
			return []byte("grok 0.2.99 (build)\n"), nil
		case "plugin list --json":
			return []byte(`[{"name":"traceary-grok","version":"0.23.0","path":"/Users/operator/.grok/plugins/traceary-grok"}]`), nil
		case "--cwd " + projectDir + " inspect --json":
			return []byte(`{"projectTrusted":true,"plugins":[{"name":"traceary-grok","enabled":true,"provides":{"skills":4,"hooks":true,"mcpServers":0}}],"hooks":[{"target":` + strconv.Quote(pluginHook) + `,"source":{"type":"plugin","plugin_name":"traceary-grok"}}]}`), nil
		default:
			t.Fatalf("unexpected Grok arguments: %v", args)
			return nil, nil
		}
	}

	state, err := probeGrokDoctorState(context.Background(), projectDir)
	if err != nil {
		t.Fatalf("probeGrokDoctorState() error = %v", err)
	}
	if !state.CLIAvailable || state.HostVersion != "0.2.99" || !state.PluginInstalled || !state.PluginEnabled || state.PluginVersion != "0.23.0" || !state.NativeHooks || state.ResolvedPathClass != grokPluginPathClassNative || state.Skills != 4 {
		t.Fatalf("state = %+v, want healthy host inventory", state)
	}
	if got, want := strings.Join(calls, "|"), "--version|plugin list --json|--cwd "+projectDir+" inspect --json"; got != want {
		t.Fatalf("Grok calls = %q, want %q", got, want)
	}
}

func TestProbeGrokDoctorStateAcceptsCleanHomeCanonicalInstalledPluginPath(t *testing.T) {
	originalLookPath, originalOutput := grokDoctorLookPath, grokDoctorOutput
	t.Cleanup(func() { grokDoctorLookPath, grokDoctorOutput = originalLookPath, originalOutput })
	grokDoctorLookPath = func(string) (string, error) { return "/usr/local/bin/grok", nil }

	home := t.TempDir()
	projectDir := t.TempDir()
	pluginHook := filepath.Join(home, ".grok", "installed-plugins", "grok-plugin-traceary-grok", "hooks", "hooks.json")
	writeGrokDoctorHookFixture(t, pluginHook, true)
	grokDoctorOutput = func(_ context.Context, args ...string) ([]byte, error) {
		switch strings.Join(args, " ") {
		case "--version":
			return []byte("grok 0.2.111\n"), nil
		case "plugin list --json":
			return []byte(`[{"name":"traceary-grok","version":"0.32.0","path":` + strconv.Quote(filepath.Dir(filepath.Dir(pluginHook))) + `}]`), nil
		case "--cwd " + projectDir + " inspect --json":
			return []byte(`{"projectTrusted":true,"plugins":[{"name":"traceary-grok","enabled":true,"provides":{"skills":4,"mcpServers":0}}],"hooks":[{"target":` + strconv.Quote(pluginHook) + `,"source":{"type":"plugin","plugin_name":"traceary-grok"}}]}`), nil
		default:
			t.Fatalf("unexpected Grok arguments: %v", args)
			return nil, nil
		}
	}

	state, err := probeGrokDoctorState(context.Background(), projectDir)
	if err != nil {
		t.Fatalf("probeGrokDoctorState() error = %v", err)
	}
	if !state.NativeHooks || state.ResolvedPathClass != grokPluginPathClassNative {
		t.Fatalf("state = %+v, want native clean-home route", state)
	}
	checks := buildGrokDoctorChecks(state, "0.32.0")
	for _, check := range checks {
		if (check.Name == "grok-plugin-resolution" || check.Name == "grok-hooks") && check.Status != doctorStatusPass {
			t.Fatalf("%s = %+v, want pass for canonical clean-home path", check.Name, check)
		}
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
	pluginHook := filepath.Join(projectDir, "integrations", "grok-plugin", "hooks", "hooks.json")
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
	routes[0].(map[string]any)["matcher"] = "unexpected"
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
			return []byte(`[{"name":"traceary-grok","version":"0.23.0"}]`), nil
		default:
			return []byte(`{"projectTrusted":false,"plugins":[{"name":"traceary-grok","enabled":true,"provides":{"skills":4,"hooks":true,"mcpServers":0}}],"hooks":[{"target":` + strconv.Quote(pluginHook) + `,"source":{"type":"plugin","plugin_name":"traceary-grok"}}]}`), nil
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

// TestProbeGrokDoctorStateWarnsWhenPluginHooksNotDispatched reproduces the
// Grok 1.0.5 dogfood: the traceary-grok plugin is installed and enabled and
// its on-disk hook file satisfies the seven-event contract, yet inspect
// dispatches only user-level hook sources. A listed plugin is not capture, so
// grok-hooks must WARN instead of claiming the native route covers all events.
func TestProbeGrokDoctorStateWarnsWhenPluginHooksNotDispatched(t *testing.T) {
	t.Setenv("TRACEARY_LANG", "en")
	originalLookPath, originalOutput, originalHome := grokDoctorLookPath, grokDoctorOutput, currentUserHomeDirFunc()
	t.Cleanup(func() {
		grokDoctorLookPath, grokDoctorOutput = originalLookPath, originalOutput
		storeUserHomeDirFunc(originalHome)
	})

	home := t.TempDir()
	storeUserHomeDirFunc(func() (string, error) { return home, nil })
	grokDoctorLookPath = func(string) (string, error) { return "/usr/local/bin/grok", nil }

	projectDir := t.TempDir()
	userHookPath := filepath.Join(home, ".grok", "hooks", "traceary.json")
	writeGrokDoctorHookFixture(t, userHookPath, true)
	pluginHook := filepath.Join(home, ".grok", "installed-plugins", "grok-plugin-traceary-grok", "hooks", "hooks.json")
	writeGrokDoctorHookFixture(t, pluginHook, true)

	grokDoctorOutput = func(_ context.Context, args ...string) ([]byte, error) {
		switch strings.Join(args, " ") {
		case "--version":
			return []byte("grok 1.0.5\n"), nil
		case "plugin list --json":
			return []byte(`[{"name":"traceary-grok","version":"0.46.0","path":` + strconv.Quote(filepath.Dir(filepath.Dir(pluginHook))) + `}]`), nil
		case "--cwd " + projectDir + " inspect --json":
			return []byte(`{"projectTrusted":true,"plugins":[{"name":"traceary-grok","enabled":true,"provides":{"skills":4,"hooks":true,"mcpServers":0}}],"hooks":[{"target":` + strconv.Quote(userHookPath) + `,"source":{"type":"user"}}]}`), nil
		default:
			t.Fatalf("unexpected Grok arguments: %v", args)
			return nil, nil
		}
	}

	state, err := probeGrokDoctorState(context.Background(), projectDir)
	if err != nil {
		t.Fatalf("probeGrokDoctorState() error = %v", err)
	}
	if !state.PluginInstalled || !state.PluginEnabled || !state.PluginHookFileVerified {
		t.Fatalf("state = %+v, want installed/enabled plugin with verified hook file", state)
	}
	if state.NativeHooks || state.NativeHooksPresent {
		t.Fatalf("state = %+v, user-only dispatch must not count as a native plugin route", state)
	}

	checks := buildGrokDoctorChecks(state, "0.46.0")
	byName := map[string]doctorCheck{}
	for _, check := range checks {
		byName[check.Name] = check
		if strings.Contains(check.Message+check.Hint, "/private/") {
			t.Fatalf("check exposed private path: %+v", check)
		}
	}
	hooksCheck, ok := byName["grok-hooks"]
	if !ok || hooksCheck.Status != doctorStatusWarn {
		t.Fatalf("grok-hooks = %+v, want WARN for undispatched plugin hooks", hooksCheck)
	}
	if strings.Contains(hooksCheck.Message, "cover all seven") {
		t.Fatalf("grok-hooks = %+v, must not claim wired native coverage", hooksCheck)
	}
	if !strings.Contains(hooksCheck.Message, "does not dispatch") || !strings.Contains(hooksCheck.Message, "user-level") {
		t.Fatalf("grok-hooks = %+v, want message naming plugin hooks absent from the dispatched inspect list", hooksCheck)
	}
	routesCheck, ok := byName["grok-hooks-routes"]
	if !ok || routesCheck.Status != doctorStatusPass || !strings.Contains(routesCheck.Message, "user-level") {
		t.Fatalf("grok-hooks-routes = %+v, want single user-level route", routesCheck)
	}
}

// TestProbeGrokDoctorStatePassesWhenPluginSourceDispatched keeps the happy
// path honest: inspect reporting a plugin-source traceary-grok hook whose
// target file still has verified seven-event coverage passes grok-hooks.
func TestProbeGrokDoctorStatePassesWhenPluginSourceDispatched(t *testing.T) {
	t.Setenv("TRACEARY_LANG", "en")
	originalLookPath, originalOutput := grokDoctorLookPath, grokDoctorOutput
	t.Cleanup(func() { grokDoctorLookPath, grokDoctorOutput = originalLookPath, originalOutput })
	grokDoctorLookPath = func(string) (string, error) { return "/usr/local/bin/grok", nil }

	projectDir := t.TempDir()
	pluginHook := filepath.Join(projectDir, "integrations", "grok-plugin", "hooks", "hooks.json")
	writeGrokDoctorHookFixture(t, pluginHook, true)
	grokDoctorOutput = func(_ context.Context, args ...string) ([]byte, error) {
		switch strings.Join(args, " ") {
		case "--version":
			return []byte("grok 1.0.5\n"), nil
		case "plugin list --json":
			return []byte(`[{"name":"traceary-grok","version":"0.46.0","path":"/cache/grok-plugin-traceary-grok"}]`), nil
		case "--cwd " + projectDir + " inspect --json":
			return []byte(`{"projectTrusted":true,"plugins":[{"name":"traceary-grok","enabled":true,"provides":{"skills":4,"mcpServers":0}}],"hooks":[{"target":` + strconv.Quote(pluginHook) + `,"source":{"type":"plugin","plugin_name":"traceary-grok"}}]}`), nil
		default:
			t.Fatalf("unexpected Grok arguments: %v", args)
			return nil, nil
		}
	}

	state, err := probeGrokDoctorState(context.Background(), projectDir)
	if err != nil {
		t.Fatalf("probeGrokDoctorState() error = %v", err)
	}
	if !state.NativeHooks || !state.NativeHooksPresent {
		t.Fatalf("state = %+v, want dispatched plugin route with verified coverage", state)
	}
	checks := buildGrokDoctorChecks(state, "0.46.0")
	for _, check := range checks {
		if check.Name == "grok-hooks" && (check.Status != doctorStatusPass || !strings.Contains(check.Message, "cover all seven")) {
			t.Fatalf("grok-hooks = %+v, want PASS for dispatched plugin-source route", check)
		}
	}
}

func writeGrokDoctorHookFixture(t *testing.T, path string, complete bool) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
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
			"timeout": 10,
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

func TestProbeGrokDoctorStateDetectsSameNameClaudePluginShadowing(t *testing.T) {
	originalLookPath, originalOutput := grokDoctorLookPath, grokDoctorOutput
	t.Cleanup(func() { grokDoctorLookPath, grokDoctorOutput = originalLookPath, originalOutput })
	grokDoctorLookPath = func(string) (string, error) { return "/usr/local/bin/grok", nil }
	projectDir := t.TempDir()
	grokDoctorOutput = func(_ context.Context, args ...string) ([]byte, error) {
		switch strings.Join(args, " ") {
		case "--version":
			return []byte("grok 0.2.99\n"), nil
		case "plugin list --json":
			return []byte(`[{"name":"traceary-grok","version":"0.23.0","path":"/Users/operator/.grok/plugins/traceary-grok"}]`), nil
		default:
			return []byte(`{"projectTrusted":true,"plugins":[{"name":"traceary-grok","enabled":true,"provides":{"skills":4,"mcpServers":0}},{"name":"traceary","enabled":true,"provides":{"skills":4,"mcpServers":0}}],"hooks":[{"target":"/Users/operator/.claude/plugins/cache/traceary/hooks/hooks.json","source":{"type":"plugin","plugin_name":"traceary"}}]}`), nil
		}
	}

	state, err := probeGrokDoctorState(context.Background(), projectDir)
	if err != nil {
		t.Fatalf("probeGrokDoctorState() error = %v", err)
	}
	if !state.PluginInstalled || !state.PluginEnabled || state.NativeHooks || !state.LegacyPluginDetected {
		t.Fatalf("state = %+v, want installed native package with Claude shadowing", state)
	}
	checks := buildGrokDoctorChecks(state, "0.23.0")
	for _, check := range checks {
		if check.Name == "grok-plugin" && check.Status != doctorStatusWarn {
			t.Fatalf("grok-plugin = %+v, want warning for shadowed resolution", check)
		}
		if check.Name == "grok-plugin-resolution" && (check.Status != doctorStatusWarn || !strings.Contains(check.Message, "claude-plugin")) {
			t.Fatalf("grok-plugin-resolution = %+v, want Claude path-class warning", check)
		}
	}
}

func TestProbeGrokDoctorStateWarnsForLegacyTracearyRoutes(t *testing.T) {
	tests := []struct {
		name           string
		listPlugins    string
		plugins        string
		hookTarget     string
		hookPluginName string
		wantInstalled  bool
		wantPathClass  string
	}{
		{
			name:           "legacy native only",
			listPlugins:    `[{"name":"traceary","version":"0.32.0"}]`,
			plugins:        `[{"name":"traceary","enabled":true,"provides":{"skills":4,"mcpServers":0}}]`,
			hookTarget:     "/Users/operator/.grok/installed-plugins/grok-plugin-legacy/hooks/hooks.json",
			hookPluginName: legacyTracearyPluginName,
			wantPathClass:  grokPluginPathClassNative,
		},
		{
			name:           "legacy Claude route only",
			listPlugins:    `[{"name":"traceary","version":"0.32.0"}]`,
			plugins:        `[{"name":"traceary","enabled":true,"provides":{"skills":4,"mcpServers":0}}]`,
			hookTarget:     "/Users/operator/.claude/plugins/cache/traceary/hooks/hooks.json",
			hookPluginName: legacyTracearyPluginName,
			wantPathClass:  grokPluginPathClassClaude,
		},
		{
			name:           "canonical only",
			listPlugins:    `[{"name":"traceary-grok","version":"0.32.0"}]`,
			plugins:        `[{"name":"traceary-grok","enabled":true,"provides":{"skills":4,"mcpServers":0}}]`,
			hookTarget:     "/Users/operator/.grok/installed-plugins/grok-plugin-canonical/hooks/hooks.json",
			hookPluginName: grokTracearyPluginName,
			wantInstalled:  true,
			wantPathClass:  grokPluginPathClassNative,
		},
		{
			name:           "canonical and legacy native coexist",
			listPlugins:    `[{"name":"traceary-grok","version":"0.32.0"},{"name":"traceary","version":"0.32.0"}]`,
			plugins:        `[{"name":"traceary-grok","enabled":true,"provides":{"skills":4,"mcpServers":0}},{"name":"traceary","enabled":true,"provides":{"skills":4,"mcpServers":0}}]`,
			hookTarget:     "/Users/operator/.grok/installed-plugins/grok-plugin-legacy/hooks/hooks.json",
			hookPluginName: legacyTracearyPluginName,
			wantInstalled:  true,
			wantPathClass:  grokPluginPathClassNative,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			originalLookPath, originalOutput := grokDoctorLookPath, grokDoctorOutput
			t.Cleanup(func() { grokDoctorLookPath, grokDoctorOutput = originalLookPath, originalOutput })
			grokDoctorLookPath = func(string) (string, error) { return "/usr/local/bin/grok", nil }
			projectDir := t.TempDir()
			grokDoctorOutput = func(_ context.Context, args ...string) ([]byte, error) {
				switch strings.Join(args, " ") {
				case "--version":
					return []byte("grok 0.2.111\n"), nil
				case "plugin list --json":
					return []byte(tc.listPlugins), nil
				default:
					return []byte(`{"projectTrusted":true,"plugins":` + tc.plugins + `,"hooks":[{"target":` + strconv.Quote(tc.hookTarget) + `,"source":{"type":"plugin","plugin_name":` + strconv.Quote(tc.hookPluginName) + `}}]}`), nil
				}
			}

			state, err := probeGrokDoctorState(context.Background(), projectDir)
			if err != nil {
				t.Fatalf("probeGrokDoctorState() error = %v", err)
			}
			if state.PluginInstalled != tc.wantInstalled || state.ResolvedPathClass != tc.wantPathClass {
				t.Fatalf("state = %+v, want installed=%v pathClass=%q", state, tc.wantInstalled, tc.wantPathClass)
			}
			checks := buildGrokDoctorChecks(state, "0.32.0")
			for _, check := range checks {
				if check.Name == "grok-plugin" && tc.name != "canonical only" && check.Status != doctorStatusWarn {
					t.Fatalf("grok-plugin = %+v, want legacy warning", check)
				}
				if check.Name == "grok-plugin" && tc.name == "canonical only" && check.Status != doctorStatusPass {
					t.Fatalf("grok-plugin = %+v, want canonical pass", check)
				}
			}
		})
	}
}

func TestProbeGrokDoctorStateDiagnosesLocalRepositoryIdentitySeparately(t *testing.T) {
	originalLookPath, originalOutput := grokDoctorLookPath, grokDoctorOutput
	t.Cleanup(func() { grokDoctorLookPath, grokDoctorOutput = originalLookPath, originalOutput })
	grokDoctorLookPath = func(string) (string, error) { return "/usr/local/bin/grok", nil }
	projectDir := t.TempDir()
	grokDoctorOutput = func(_ context.Context, args ...string) ([]byte, error) {
		switch strings.Join(args, " ") {
		case "--version":
			return []byte("grok 0.2.111\n"), nil
		case "plugin list --json":
			return []byte(`[{"name":"traceary","repo_key":"grok-plugin-4d1bd2fe","version":"0.32.0","path":"/Users/operator/.grok/installed-plugins/grok-plugin-4d1bd2fe","source":"/Users/operator/Repositories/traceary/integrations/grok-plugin"}]`), nil
		default:
			return []byte(`{"projectTrusted":true,"plugins":[{"name":"traceary","enabled":true,"provides":{"skills":4,"mcpServers":0}}],"hooks":[]}`), nil
		}
	}

	state, err := probeGrokDoctorState(context.Background(), projectDir)
	if err != nil {
		t.Fatalf("probeGrokDoctorState() error = %v", err)
	}
	if state.PluginInstalled || !state.LocalRepoConflict {
		t.Fatalf("state = %+v, want only a local-repository identity conflict", state)
	}
	checks := buildGrokDoctorChecks(state, "0.32.1")
	found := map[string]bool{}
	for _, check := range checks {
		if check.Name != "grok-plugin" && check.Name != "grok-plugin-resolution" {
			continue
		}
		found[check.Name] = true
		if check.Status != doctorStatusWarn || check.Hint != "scripts/install-grok-plugin.sh --migrate-local-repo-identity" {
			t.Fatalf("%s = %+v, want bounded migration warning", check.Name, check)
		}
		if strings.Contains(check.Message+check.Hint, "4d1bd2fe") || strings.Contains(check.Message+check.Hint, "/Users/") {
			t.Fatalf("%s leaked inventory identifier or path: %+v", check.Name, check)
		}
	}
	if !found["grok-plugin"] || !found["grok-plugin-resolution"] {
		t.Fatalf("checks = %+v, want plugin and resolution migration checks", checks)
	}
}

func TestGrokIsLocalRepositoryIdentity(t *testing.T) {
	tests := []struct {
		name   string
		plugin grokPluginListEntry
		want   bool
	}{
		{name: "subdirectory local repository identity", plugin: grokPluginListEntry{Name: "traceary", RepoKey: "grok-plugin-4d1bd2fe", Source: "/repo/integrations/grok-plugin"}, want: true},
		{name: "canonical package", plugin: grokPluginListEntry{Name: "traceary-grok", RepoKey: "grok-plugin-4d1bd2fe", Source: "/repo"}},
		{name: "legacy package from another host", plugin: grokPluginListEntry{Name: "traceary", RepoKey: "marketplace-traceary", Source: "duck8823/traceary"}},
		{name: "local repository root with canonical subdirectory", plugin: grokPluginListEntry{Name: "traceary-grok", RepoKey: "grok-plugin-4d1bd2fe", Source: "/repo"}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := grokIsLocalRepositoryIdentity(tc.plugin); got != tc.want {
				t.Fatalf("grokIsLocalRepositoryIdentity(%+v) = %v, want %v", tc.plugin, got, tc.want)
			}
		})
	}
}

func TestProbeGrokDoctorStateUserHookRoutes(t *testing.T) {
	t.Setenv("TRACEARY_LANG", "en")

	tests := []struct {
		name             string
		writeUserHooks   bool
		invalidUserHooks bool
		nativePlugin     bool
		inspectUserHooks bool
		wantUserHooks    bool
		wantRoutesStatus string
		wantRoutesSubstr []string
		wantUserStatus   string
		wantUserSubstr   []string
	}{
		{
			name:             "user file absent with native plugin has no duplicate warning",
			nativePlugin:     true,
			wantUserHooks:    false,
			wantRoutesStatus: doctorStatusPass,
			wantRoutesSubstr: []string{"single route", "native plugin"},
			wantUserStatus:   doctorStatusSkip,
			wantUserSubstr:   []string{"no user-level", "~/.grok/hooks/traceary.json"},
		},
		{
			name:             "user file and native plugin warn for duplicate routes",
			writeUserHooks:   true,
			nativePlugin:     true,
			inspectUserHooks: true,
			wantUserHooks:    true,
			wantRoutesStatus: doctorStatusWarn,
			wantRoutesSubstr: []string{"exactly one", "user-level", "native plugin", "~/.grok/hooks/traceary.json"},
			wantUserStatus:   doctorStatusPass,
			wantUserSubstr:   []string{"user-level", "~/.grok/hooks/traceary.json"},
		},
		{
			name:             "user file present without plugin reports user route",
			writeUserHooks:   true,
			wantUserHooks:    true,
			wantRoutesStatus: doctorStatusPass,
			wantRoutesSubstr: []string{"single route", "user-level"},
			wantUserStatus:   doctorStatusPass,
			wantUserSubstr:   []string{"user-level", "~/.grok/hooks/traceary.json"},
		},
		{
			name:             "invalid user file fails the user route check",
			writeUserHooks:   true,
			invalidUserHooks: true,
			wantUserHooks:    true,
			wantRoutesStatus: doctorStatusPass,
			wantRoutesSubstr: []string{"single route", "user-level"},
			wantUserStatus:   doctorStatusFail,
			wantUserSubstr:   []string{"invalid", "~/.grok/hooks/traceary.json"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			originalLookPath, originalOutput, originalHome := grokDoctorLookPath, grokDoctorOutput, currentUserHomeDirFunc()
			t.Cleanup(func() {
				grokDoctorLookPath, grokDoctorOutput = originalLookPath, originalOutput
				storeUserHomeDirFunc(originalHome)
			})

			home := t.TempDir()
			storeUserHomeDirFunc(func() (string, error) { return home, nil })
			grokDoctorLookPath = func(string) (string, error) { return "/usr/local/bin/grok", nil }

			projectDir := t.TempDir()
			userHookPath := filepath.Join(home, ".grok", "hooks", "traceary.json")
			if tc.writeUserHooks {
				if tc.invalidUserHooks {
					if err := os.MkdirAll(filepath.Dir(userHookPath), 0o700); err != nil {
						t.Fatalf("MkdirAll() error = %v", err)
					}
					if err := os.WriteFile(userHookPath, []byte(`not-json`), 0o600); err != nil {
						t.Fatalf("WriteFile() error = %v", err)
					}
				} else {
					writeGrokDoctorHookFixture(t, userHookPath, true)
				}
			}

			var pluginHook string
			if tc.nativePlugin {
				pluginHook = filepath.Join(home, ".grok", "installed-plugins", "grok-plugin-traceary-grok", "hooks", "hooks.json")
				writeGrokDoctorHookFixture(t, pluginHook, true)
			}

			grokDoctorOutput = func(_ context.Context, args ...string) ([]byte, error) {
				switch strings.Join(args, " ") {
				case "--version":
					return []byte("grok 0.2.111\n"), nil
				case "plugin list --json":
					if tc.nativePlugin {
						return []byte(`[{"name":"traceary-grok","version":"0.34.0","path":` + strconv.Quote(filepath.Dir(filepath.Dir(pluginHook))) + `}]`), nil
					}
					return []byte(`[]`), nil
				case "--cwd " + projectDir + " inspect --json":
					hooks := []string{}
					if tc.nativePlugin {
						hooks = append(hooks, `{"target":`+strconv.Quote(pluginHook)+`,"source":{"type":"plugin","plugin_name":"traceary-grok"}}`)
					}
					if tc.inspectUserHooks || tc.writeUserHooks {
						// Corroborate the user file route when present; doctor also stats the file.
						hooks = append(hooks, `{"target":`+strconv.Quote(userHookPath)+`,"source":{"type":"user"}}`)
					}
					plugins := `[]`
					if tc.nativePlugin {
						plugins = `[{"name":"traceary-grok","enabled":true,"provides":{"skills":4,"mcpServers":0}}]`
					}
					return []byte(`{"projectTrusted":true,"plugins":` + plugins + `,"hooks":[` + strings.Join(hooks, ",") + `]}`), nil
				default:
					t.Fatalf("unexpected Grok arguments: %v", args)
					return nil, nil
				}
			}

			state, err := probeGrokDoctorState(context.Background(), projectDir)
			if err != nil {
				t.Fatalf("probeGrokDoctorState() error = %v", err)
			}
			if state.UserHooks != tc.wantUserHooks {
				t.Fatalf("UserHooks = %v, want %v (state=%+v)", state.UserHooks, tc.wantUserHooks, state)
			}
			if tc.wantUserHooks {
				if state.UserHooksPath != userHookPath {
					t.Fatalf("UserHooksPath = %q, want %q", state.UserHooksPath, userHookPath)
				}
				if state.UserHooksInvalid != tc.invalidUserHooks {
					t.Fatalf("UserHooksInvalid = %v, want %v", state.UserHooksInvalid, tc.invalidUserHooks)
				}
			}
			if state.NativeHooks != tc.nativePlugin || state.NativeHooksPresent != tc.nativePlugin {
				t.Fatalf("NativeHooks/Present = %v/%v, want both %v", state.NativeHooks, state.NativeHooksPresent, tc.nativePlugin)
			}

			checks := buildGrokDoctorChecks(state, "0.34.0")
			byName := map[string]doctorCheck{}
			for _, check := range checks {
				byName[check.Name] = check
				if strings.Contains(check.Message+check.Hint, "/private/") {
					t.Fatalf("check exposed private path: %+v", check)
				}
			}

			userCheck, ok := byName["grok-hooks-user"]
			if !ok {
				t.Fatalf("checks = %+v, want grok-hooks-user", checks)
			}
			if userCheck.Status != tc.wantUserStatus {
				t.Fatalf("grok-hooks-user = %+v, want status %s", userCheck, tc.wantUserStatus)
			}
			for _, sub := range tc.wantUserSubstr {
				if !strings.Contains(userCheck.Message+userCheck.Hint, sub) {
					t.Fatalf("grok-hooks-user = %+v, want substring %q", userCheck, sub)
				}
			}

			routesCheck, ok := byName["grok-hooks-routes"]
			if !ok {
				t.Fatalf("checks = %+v, want grok-hooks-routes", checks)
			}
			if routesCheck.Status != tc.wantRoutesStatus {
				t.Fatalf("grok-hooks-routes = %+v, want status %s", routesCheck, tc.wantRoutesStatus)
			}
			for _, sub := range tc.wantRoutesSubstr {
				if !strings.Contains(routesCheck.Message+routesCheck.Hint, sub) {
					t.Fatalf("grok-hooks-routes = %+v, want substring %q", routesCheck, sub)
				}
			}
		})
	}
}

func TestProbeGrokDoctorStateWarnsDuplicateWhenNativeCoverageIncomplete(t *testing.T) {
	t.Setenv("TRACEARY_LANG", "en")
	originalLookPath, originalOutput, originalHome := grokDoctorLookPath, grokDoctorOutput, currentUserHomeDirFunc()
	t.Cleanup(func() {
		grokDoctorLookPath, grokDoctorOutput = originalLookPath, originalOutput
		storeUserHomeDirFunc(originalHome)
	})

	home := t.TempDir()
	storeUserHomeDirFunc(func() (string, error) { return home, nil })
	grokDoctorLookPath = func(string) (string, error) { return "/usr/local/bin/grok", nil }

	projectDir := t.TempDir()
	userHookPath := filepath.Join(home, ".grok", "hooks", "traceary.json")
	writeGrokDoctorHookFixture(t, userHookPath, true)

	// Incomplete native fixture: Grok still merges it, but coverage fails.
	pluginHook := filepath.Join(home, ".grok", "installed-plugins", "grok-plugin-traceary-grok", "hooks", "hooks.json")
	writeGrokDoctorHookFixture(t, pluginHook, false)

	grokDoctorOutput = func(_ context.Context, args ...string) ([]byte, error) {
		switch strings.Join(args, " ") {
		case "--version":
			return []byte("grok 0.2.111\n"), nil
		case "plugin list --json":
			return []byte(`[{"name":"traceary-grok","version":"0.34.0","path":` + strconv.Quote(filepath.Dir(filepath.Dir(pluginHook))) + `}]`), nil
		case "--cwd " + projectDir + " inspect --json":
			return []byte(`{"projectTrusted":true,"plugins":[{"name":"traceary-grok","enabled":true,"provides":{"skills":4,"mcpServers":0}}],"hooks":[{"target":` + strconv.Quote(pluginHook) + `,"source":{"type":"plugin","plugin_name":"traceary-grok"}},{"target":` + strconv.Quote(userHookPath) + `,"source":{"type":"user"}}]}`), nil
		default:
			t.Fatalf("unexpected Grok arguments: %v", args)
			return nil, nil
		}
	}

	state, err := probeGrokDoctorState(context.Background(), projectDir)
	if err != nil {
		t.Fatalf("probeGrokDoctorState() error = %v", err)
	}
	if !state.UserHooks || !state.NativeHooksPresent || state.NativeHooks {
		t.Fatalf("state = %+v, want user + present incomplete native route", state)
	}

	checks := buildGrokDoctorChecks(state, "0.34.0")
	byName := map[string]doctorCheck{}
	for _, check := range checks {
		byName[check.Name] = check
		if strings.Contains(check.Message+check.Hint, "/private/") {
			t.Fatalf("check exposed private path: %+v", check)
		}
	}

	hooksCheck, ok := byName["grok-hooks"]
	if !ok || hooksCheck.Status != doctorStatusWarn || !strings.Contains(hooksCheck.Message, "incomplete") {
		t.Fatalf("grok-hooks = %+v, want incomplete coverage warning", hooksCheck)
	}
	routesCheck, ok := byName["grok-hooks-routes"]
	if !ok || routesCheck.Status != doctorStatusWarn {
		t.Fatalf("grok-hooks-routes = %+v, want duplicate-route warning", routesCheck)
	}
	for _, sub := range []string{"exactly one", "user-level", "native plugin", "~/.grok/hooks/traceary.json"} {
		if !strings.Contains(routesCheck.Message+routesCheck.Hint, sub) {
			t.Fatalf("grok-hooks-routes = %+v, want substring %q", routesCheck, sub)
		}
	}
}

// TestProbeGrokDoctorStateDetectsMissingLocalPluginSource drives the shipped
// probe and check builder with a stubbed Grok CLI: a local plugin Source that
// is absent on disk must surface as a FAIL (never a version PASS), while
// present local Sources and remote git URLs must not be flagged.
func TestProbeGrokDoctorStateDetectsMissingLocalPluginSource(t *testing.T) {
	originalLookPath, originalOutput := grokDoctorLookPath, grokDoctorOutput
	t.Cleanup(func() { grokDoctorLookPath, grokDoctorOutput = originalLookPath, originalOutput })
	grokDoctorLookPath = func(string) (string, error) { return "/usr/local/bin/grok", nil }

	presentSource := t.TempDir()
	if err := os.WriteFile(filepath.Join(presentSource, "plugin.json"), []byte(`{"name":"traceary-grok","version":"0.45.0"}`), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	missingSource := filepath.Join(t.TempDir(), "does-not-exist")

	tests := []struct {
		name        string
		source      string
		wantMissing bool
		wantStatus  string
	}{
		{name: "missing local source fails despite cached version", source: missingSource, wantMissing: true, wantStatus: doctorStatusFail},
		{name: "present local source passes", source: presentSource, wantMissing: false, wantStatus: doctorStatusPass},
		{name: "https git source is not missing", source: "https://github.com/duck8823/traceary.git", wantMissing: false, wantStatus: doctorStatusPass},
		{name: "scp-style git source is not missing", source: "git@github.com:duck8823/traceary.git", wantMissing: false, wantStatus: doctorStatusPass},
		{name: "empty source is not flagged", source: "", wantMissing: false, wantStatus: doctorStatusPass},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			projectDir := t.TempDir()
			grokDoctorOutput = func(_ context.Context, args ...string) ([]byte, error) {
				switch strings.Join(args, " ") {
				case "--version":
					return []byte("grok 1.0.5\n"), nil
				case "plugin list --json":
					return []byte(`[{"name":"traceary-grok","version":"0.45.0","path":"/cache/grok-plugin-13dae94b","source":` + strconv.Quote(tc.source) + `}]`), nil
				case "--cwd " + projectDir + " inspect --json":
					return []byte(`{"projectTrusted":true,"plugins":[{"name":"traceary-grok","enabled":true,"provides":{"skills":4,"mcpServers":0}}],"hooks":[]}`), nil
				default:
					t.Fatalf("unexpected Grok arguments: %v", args)
					return nil, nil
				}
			}

			state, err := probeGrokDoctorState(context.Background(), projectDir)
			if err != nil {
				t.Fatalf("probeGrokDoctorState() error = %v", err)
			}
			if !state.PluginInstalled || state.PluginVersion != "0.45.0" {
				t.Fatalf("state = %+v, want installed cached plugin 0.45.0", state)
			}
			if state.PluginSourceMissing != tc.wantMissing {
				t.Fatalf("state.PluginSourceMissing = %v, want %v (state = %+v)", state.PluginSourceMissing, tc.wantMissing, state)
			}
			var found *doctorCheck
			checks := buildGrokDoctorChecks(state, "0.45.0")
			for i := range checks {
				if checks[i].Name == "grok-plugin" {
					found = &checks[i]
					break
				}
			}
			if found == nil || found.Status != tc.wantStatus {
				t.Fatalf("grok-plugin check = %+v, want status %s", found, tc.wantStatus)
			}
			if tc.wantMissing {
				if !strings.Contains(found.Message, tc.source) || !strings.Contains(found.Hint, "install-grok-plugin.sh") {
					t.Fatalf("grok-plugin check = %+v, want missing source path and reinstall hint", found)
				}
			}
			for _, check := range checks {
				if strings.Contains(check.Message+check.Hint, "/private/") {
					t.Fatalf("check exposed private path: %+v", check)
				}
			}
		})
	}
}

func TestBuildGrokHookRoutesSummary(t *testing.T) {
	t.Setenv("TRACEARY_LANG", "en")
	tests := []struct {
		name       string
		state      grokDoctorState
		status     string
		messageSub string
		hintSub    string
	}{
		{
			name:       "native only passes",
			state:      grokDoctorState{NativeHooksPresent: true, NativeHooks: true},
			status:     doctorStatusPass,
			messageSub: "native plugin",
		},
		{
			name:       "user plus native warns with path",
			state:      grokDoctorState{NativeHooksPresent: true, NativeHooks: true, UserHooks: true, UserHooksPath: "/tmp/home/.grok/hooks/traceary.json"},
			status:     doctorStatusWarn,
			messageSub: "exactly one",
			hintSub:    "traceary.json",
		},
		{
			name:       "user plus incomplete native still warns",
			state:      grokDoctorState{NativeHooksPresent: true, NativeHooks: false, UserHooks: true, UserHooksPath: "/tmp/home/.grok/hooks/traceary.json"},
			status:     doctorStatusWarn,
			messageSub: "exactly one",
			hintSub:    "traceary.json",
		},
		{
			name:       "user plus project warns",
			state:      grokDoctorState{ProjectHooks: true, UserHooks: true, UserHooksPath: "/tmp/home/.grok/hooks/traceary.json"},
			status:     doctorStatusWarn,
			messageSub: "user-level",
			hintSub:    "exactly one",
		},
		{
			name:       "no routes skips",
			state:      grokDoctorState{},
			status:     doctorStatusSkip,
			messageSub: "no Grok hook route",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			check := buildGrokHookRoutesSummary(tc.state)
			if check.Name != "grok-hooks-routes" || check.Status != tc.status {
				t.Fatalf("check = %+v, want status %s", check, tc.status)
			}
			if !strings.Contains(check.Message+check.Hint, tc.messageSub) {
				t.Fatalf("check = %+v, want message substring %q", check, tc.messageSub)
			}
			if tc.hintSub != "" && !strings.Contains(check.Message+check.Hint, tc.hintSub) {
				t.Fatalf("check = %+v, want hint substring %q", check, tc.hintSub)
			}
			if strings.Contains(check.Message+check.Hint, "/private/") {
				t.Fatalf("check exposed private path: %+v", check)
			}
		})
	}
}
