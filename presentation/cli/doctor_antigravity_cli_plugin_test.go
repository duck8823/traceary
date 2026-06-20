package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const healthyAntigravityHooksJSON = `{
  "traceary": {
    "PreInvocation": [
      {"type": "command", "command": "'traceary' 'hook' 'antigravity' 'pre-invocation'", "timeout": 10}
    ],
    "Stop": [
      {"type": "command", "command": "'traceary' 'hook' 'antigravity' 'stop'", "timeout": 10}
    ]
  }
}`

const staleGeminiHooksJSON = `{
  "hooks": {
    "SessionStart": [
      {"matcher": "*", "hooks": [
        {"type": "command", "command": "'traceary' 'hook' 'session' 'gemini' 'start'", "timeout": 5000}
      ]}
    ]
  }
}`

const healthyAntigravityPluginJSON = `{
  "$schema": "https://antigravity.google/schemas/v1/plugin.json",
  "name": "traceary"
}`

func TestClassifyAntigravityCLIPluginProbe(t *testing.T) {
	tests := []struct {
		name  string
		probe antigravityCLIPluginProbe
		want  antigravityCLIPluginShape
	}{
		{
			name:  "absent when directory does not exist",
			probe: antigravityCLIPluginProbe{DirExists: false},
			want:  antigravityCLIPluginAbsent,
		},
		{
			name:  "healthy when hooks.json carries the antigravity top-level group",
			probe: antigravityCLIPluginProbe{DirExists: true, PluginSchema: antigravityPluginSchema, HooksJSON: []byte(healthyAntigravityHooksJSON)},
			want:  antigravityCLIPluginHealthy,
		},
		{
			name:  "stale when hooks.json uses the legacy gemini {\"hooks\"} envelope",
			probe: antigravityCLIPluginProbe{DirExists: true, HooksJSON: []byte(staleGeminiHooksJSON)},
			want:  antigravityCLIPluginStaleGemini,
		},
		{
			name:  "stale when legacy hooks/hooks.json carries gemini commands",
			probe: antigravityCLIPluginProbe{DirExists: true, LegacyHooksJSON: []byte(staleGeminiHooksJSON)},
			want:  antigravityCLIPluginStaleGemini,
		},
		{
			name: "stale takes precedence when both healthy and legacy files are present",
			probe: antigravityCLIPluginProbe{
				DirExists:       true,
				HooksJSON:       []byte(healthyAntigravityHooksJSON),
				LegacyHooksJSON: []byte(staleGeminiHooksJSON),
			},
			want: antigravityCLIPluginStaleGemini,
		},
		{
			name:  "unknown when directory exists but no recognized hooks shape",
			probe: antigravityCLIPluginProbe{DirExists: true, HooksJSON: []byte(`{"something": {}}`)},
			want:  antigravityCLIPluginUnknown,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := classifyAntigravityCLIPluginProbe(tt.probe); got != tt.want {
				t.Fatalf("classifyAntigravityCLIPluginProbe(%+v) = %v, want %v", tt.probe, got, tt.want)
			}
		})
	}
}

func TestProbeAndClassifyAntigravityCLIPluginFromDisk(t *testing.T) {
	t.Run("stale Gemini-imported package is detected", func(t *testing.T) {
		dir := t.TempDir()
		writeFile(t, filepath.Join(dir, "plugin.json"), healthyAntigravityPluginJSON)
		writeFile(t, filepath.Join(dir, "hooks", "hooks.json"), staleGeminiHooksJSON)

		probe := probeAntigravityCLIPlugin(dir)
		if !probe.DirExists {
			t.Fatalf("DirExists = false, want true")
		}
		if got := classifyAntigravityCLIPluginProbe(probe); got != antigravityCLIPluginStaleGemini {
			t.Fatalf("shape = %v, want stale", got)
		}
	})

	t.Run("healthy Antigravity package is detected", func(t *testing.T) {
		dir := t.TempDir()
		writeFile(t, filepath.Join(dir, "plugin.json"), healthyAntigravityPluginJSON)
		writeFile(t, filepath.Join(dir, "hooks.json"), healthyAntigravityHooksJSON)

		probe := probeAntigravityCLIPlugin(dir)
		if probe.PluginSchema != antigravityPluginSchema {
			t.Fatalf("PluginSchema = %q, want %q", probe.PluginSchema, antigravityPluginSchema)
		}
		if got := classifyAntigravityCLIPluginProbe(probe); got != antigravityCLIPluginHealthy {
			t.Fatalf("shape = %v, want healthy", got)
		}
	})

	t.Run("absent directory probes as not existing", func(t *testing.T) {
		dir := filepath.Join(t.TempDir(), "does-not-exist")
		probe := probeAntigravityCLIPlugin(dir)
		if probe.DirExists {
			t.Fatalf("DirExists = true, want false")
		}
		if got := classifyAntigravityCLIPluginProbe(probe); got != antigravityCLIPluginAbsent {
			t.Fatalf("shape = %v, want absent", got)
		}
	})
}

func TestBuildAntigravityCLIPluginCheck(t *testing.T) {
	t.Setenv("TRACEARY_LANG", "en")
	const dir = "/home/u/.gemini/antigravity-cli/plugins/traceary"

	t.Run("stale warns with remediation distinguishing the formats", func(t *testing.T) {
		check := buildAntigravityCLIPluginCheck(antigravityCLIPluginStaleGemini, dir)
		if check.Name != "antigravity-cli-plugin" {
			t.Fatalf("Name = %q", check.Name)
		}
		if check.Status != doctorStatusWarn {
			t.Fatalf("Status = %q, want warn", check.Status)
		}
		for _, want := range []string{"stale", `{"hooks"`, "hook ... gemini", "hook antigravity", dir} {
			if !strings.Contains(check.Message, want) {
				t.Fatalf("message missing %q: %q", want, check.Message)
			}
		}
		if check.FixCommand == "" {
			t.Fatalf("stale check should carry a guided FixCommand")
		}
	})

	t.Run("healthy passes", func(t *testing.T) {
		check := buildAntigravityCLIPluginCheck(antigravityCLIPluginHealthy, dir)
		if check.Status != doctorStatusPass {
			t.Fatalf("Status = %q, want pass", check.Status)
		}
	})

	t.Run("absent skips so hooks-install users are not failed", func(t *testing.T) {
		check := buildAntigravityCLIPluginCheck(antigravityCLIPluginAbsent, dir)
		if check.Status != doctorStatusSkip {
			t.Fatalf("Status = %q, want skip", check.Status)
		}
	})

	t.Run("unknown warns", func(t *testing.T) {
		check := buildAntigravityCLIPluginCheck(antigravityCLIPluginUnknown, dir)
		if check.Status != doctorStatusWarn {
			t.Fatalf("Status = %q, want warn", check.Status)
		}
	})
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}
