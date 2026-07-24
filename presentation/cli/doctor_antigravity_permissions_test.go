package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
)

func TestEvaluateAntigravityHeadlessPermissions(t *testing.T) {
	exactAllow := append([]string(nil), antigravityRequiredHookPermissions...)
	tests := []struct {
		name           string
		rules          antigravityPermissionRules
		wantExecutable bool
		wantMissing    int
		wantShadowed   int
		wantUnsafe     int
	}{
		{
			name:        "absent rules miss every required hook",
			wantMissing: len(antigravityRequiredHookPermissions),
		},
		{
			name:           "four exact allows provide executable coverage",
			rules:          antigravityPermissionRules{Allow: exactAllow},
			wantExecutable: true,
		},
		{
			name: "partial exact allows remain incomplete",
			rules: antigravityPermissionRules{
				Allow: exactAllow[:2],
			},
			wantMissing: 2,
		},
		{
			name: "ask wildcard shadows exact allows",
			rules: antigravityPermissionRules{
				Allow: exactAllow,
				Ask:   []string{"command(*)"},
			},
			wantShadowed: len(antigravityRequiredHookPermissions),
		},
		{
			name: "deny prefix shadows only matching traceary hooks",
			rules: antigravityPermissionRules{
				Allow: exactAllow,
				Deny:  []string{"command(traceary hook antigravity pre-tool-use)"},
			},
			wantShadowed: 1,
		},
		{
			name: "broad command allow never substitutes for exact rules",
			rules: antigravityPermissionRules{
				Allow: []string{"command(*)"},
			},
			wantMissing: len(antigravityRequiredHookPermissions),
			wantUnsafe:  1,
		},
		{
			name: "traceary prefix is broader than the packaged hook commands",
			rules: antigravityPermissionRules{
				Allow: []string{"command(traceary hook antigravity)"},
			},
			wantMissing: len(antigravityRequiredHookPermissions),
			wantUnsafe:  1,
		},
		{
			name: "unsandboxed traceary hook grant is unsafe even with exact command allows",
			rules: antigravityPermissionRules{
				Allow: append(exactAllow, "unsandboxed(traceary hook antigravity)"),
			},
			wantUnsafe: 1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := evaluateAntigravityHeadlessPermissions(tt.rules)
			if got.Executable != tt.wantExecutable {
				t.Fatalf("Executable = %v, want %v", got.Executable, tt.wantExecutable)
			}
			if len(got.Missing) != tt.wantMissing {
				t.Fatalf("Missing = %v, want %d item(s)", got.Missing, tt.wantMissing)
			}
			if len(got.Shadowed) != tt.wantShadowed {
				t.Fatalf("Shadowed = %v, want %d item(s)", got.Shadowed, tt.wantShadowed)
			}
			if len(got.Unsafe) != tt.wantUnsafe {
				t.Fatalf("Unsafe = %v, want %d item(s)", got.Unsafe, tt.wantUnsafe)
			}
		})
	}
}

func TestReadAntigravityPermissionRules(t *testing.T) {
	t.Run("reads the documented top-level permissions object", func(t *testing.T) {
		data := []byte(`{
			"permissions":{
				"allow":[
					"command(traceary hook antigravity pre-invocation)",
					"command(traceary hook antigravity stop)"
				],
				"deny":["command(sudo)"]
			},
			"unrelated":{"permissions":{"allow":["command(*)"]}}
		}`)
		rules, err := readAntigravityPermissionRules(data)
		if err != nil {
			t.Fatalf("readAntigravityPermissionRules() error = %v", err)
		}
		if len(rules.Allow) != 2 || len(rules.Deny) != 1 {
			t.Fatalf("rules = %+v, want two allow and one deny", rules)
		}
	})

	t.Run("rejects malformed permission lists", func(t *testing.T) {
		_, err := readAntigravityPermissionRules([]byte(`{"permissions":{"allow":"command(*)"}}`))
		if err == nil {
			t.Fatal("readAntigravityPermissionRules() error = nil, want malformed list error")
		}
	})
}

func TestBuildAntigravityHeadlessCoverageCheck(t *testing.T) {
	t.Setenv("TRACEARY_LANG", "en")
	healthyRoutes := []antigravityHookRoute{
		healthyRoute(antigravityRoutePluginLabel),
	}

	t.Run("skips when no hook route is installed", func(t *testing.T) {
		check := buildAntigravityHeadlessCoverageCheck(
			[]antigravityHookRoute{absentRoute(antigravityRoutePluginLabel)},
			antigravityPermissionAssessment{},
		)
		if check.Status != doctorStatusSkip {
			t.Fatalf("Status = %q, want skip", check.Status)
		}
	})

	t.Run("warns when hooks are installed but exact permission is absent", func(t *testing.T) {
		check := buildAntigravityHeadlessCoverageCheck(
			healthyRoutes,
			evaluateAntigravityHeadlessPermissions(antigravityPermissionRules{}),
		)
		if check.Status != doctorStatusWarn {
			t.Fatalf("Status = %q, want warn", check.Status)
		}
		if !strings.Contains(check.Message, "installed") || !strings.Contains(check.Message, "not executable") {
			t.Fatalf("Message = %q, want installed versus executable distinction", check.Message)
		}
		if strings.Contains(check.Message, "command(*)") {
			t.Fatalf("Message = %q, must not recommend command(*)", check.Message)
		}
	})

	t.Run("passes with one route and all exact effective rules", func(t *testing.T) {
		check := buildAntigravityHeadlessCoverageCheck(
			healthyRoutes,
			evaluateAntigravityHeadlessPermissions(antigravityPermissionRules{
				Allow: append([]string(nil), antigravityRequiredHookPermissions...),
			}),
		)
		if check.Status != doctorStatusPass {
			t.Fatalf("Status = %q, want pass (message=%q)", check.Status, check.Message)
		}
	})

	t.Run("warns with multiple healthy routes even when permissions are exact", func(t *testing.T) {
		check := buildAntigravityHeadlessCoverageCheck(
			[]antigravityHookRoute{
				healthyRoute(antigravityRouteWorkspaceLabel),
				healthyRoute(antigravityRoutePluginLabel),
			},
			evaluateAntigravityHeadlessPermissions(antigravityPermissionRules{
				Allow: append([]string(nil), antigravityRequiredHookPermissions...),
			}),
		)
		if check.Status != doctorStatusWarn {
			t.Fatalf("Status = %q, want warn (message=%q)", check.Status, check.Message)
		}
		if !strings.Contains(check.Message, "multiple routes") {
			t.Fatalf("Message = %q, want duplicate-route guidance", check.Message)
		}
	})
}

func TestPackagedAntigravityPermissionsAreExact(t *testing.T) {
	pluginDir := filepath.Join("..", "..", "integrations", "antigravity-plugin")
	data, err := os.ReadFile(filepath.Join(pluginDir, "permissions.example.json"))
	if err != nil {
		t.Fatalf("read packaged permission fragment: %v", err)
	}
	rules, err := readAntigravityPermissionRules(data)
	if err != nil {
		t.Fatalf("parse packaged permission fragment: %v", err)
	}
	got := evaluateAntigravityHeadlessPermissions(rules)
	if !got.Executable || len(got.Missing) != 0 || len(got.Shadowed) != 0 || len(got.Unsafe) != 0 {
		t.Fatalf("packaged permission assessment = %+v, want exact executable coverage", got)
	}
	if len(rules.Allow) != len(antigravityRequiredHookPermissions) {
		t.Fatalf("packaged allow rules = %v, want exactly %v", rules.Allow, antigravityRequiredHookPermissions)
	}

	hooksData, err := os.ReadFile(filepath.Join(pluginDir, "hooks.json"))
	if err != nil {
		t.Fatalf("read packaged hooks: %v", err)
	}
	var hooksDocument any
	if err := json.Unmarshal(hooksData, &hooksDocument); err != nil {
		t.Fatalf("decode packaged hooks: %v", err)
	}
	var hookCommands []string
	var collectCommands func(any)
	collectCommands = func(value any) {
		switch typed := value.(type) {
		case map[string]any:
			for key, nested := range typed {
				if key == "command" {
					if command, ok := nested.(string); ok {
						command = strings.ReplaceAll(command, "'", "")
						hookCommands = append(hookCommands, "command("+strings.Join(strings.Fields(command), " ")+")")
					}
					continue
				}
				collectCommands(nested)
			}
		case []any:
			for _, nested := range typed {
				collectCommands(nested)
			}
		}
	}
	collectCommands(hooksDocument)
	wantHookCommands := append([]string(nil), antigravityRequiredHookPermissions...)
	slices.Sort(wantHookCommands)
	slices.Sort(hookCommands)
	if diff := cmp.Diff(wantHookCommands, hookCommands); diff != "" {
		t.Fatalf("packaged hook commands do not match scoped permissions (-want +got):\n%s", diff)
	}
}

func TestInspectAntigravityHeadlessPermissionsUsesOnlyCLIGlobalSettings(t *testing.T) {
	home := t.TempDir()
	cliSettingsDir := filepath.Join(home, ".gemini", "antigravity-cli")
	if err := os.MkdirAll(cliSettingsDir, 0o700); err != nil {
		t.Fatalf("MkdirAll(%s): %v", cliSettingsDir, err)
	}
	if err := os.WriteFile(
		filepath.Join(cliSettingsDir, "settings.json"),
		[]byte(`{"permissions":{"allow":[]}}`),
		0o600,
	); err != nil {
		t.Fatalf("write CLI settings: %v", err)
	}

	exactRules, err := os.ReadFile(filepath.Join("..", "..", "integrations", "antigravity-plugin", "permissions.example.json"))
	if err != nil {
		t.Fatalf("read packaged rules: %v", err)
	}
	for path := range map[string]struct{}{
		filepath.Join(home, ".gemini", "settings.json"):                     {},
		filepath.Join(home, ".gemini", "config", "projects", "active.json"): {},
	} {
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatalf("MkdirAll(%s): %v", filepath.Dir(path), err)
		}
		if err := os.WriteFile(path, exactRules, 0o600); err != nil {
			t.Fatalf("write non-CLI settings %s: %v", path, err)
		}
	}

	originalHomeDirFunc := userHomeDirFunc
	userHomeDirFunc = func() (string, error) { return home, nil }
	t.Cleanup(func() { userHomeDirFunc = originalHomeDirFunc })

	got := inspectAntigravityHeadlessPermissions()
	if got.Executable || len(got.Missing) != len(antigravityRequiredHookPermissions) {
		t.Fatalf("assessment = %+v, want non-CLI settings to be ignored", got)
	}

	if err := os.WriteFile(filepath.Join(cliSettingsDir, "settings.json"), exactRules, 0o600); err != nil {
		t.Fatalf("write exact CLI settings: %v", err)
	}
	got = inspectAntigravityHeadlessPermissions()
	if !got.Executable || len(got.Unsafe) != 0 {
		t.Fatalf("assessment = %+v, want exact global CLI settings only", got)
	}
}
