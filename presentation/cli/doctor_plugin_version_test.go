package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestInspectPluginVersion(t *testing.T) {
	manifest := filepath.Join(t.TempDir(), "plugin.json")
	if err := os.WriteFile(manifest, []byte(`{"name":"traceary","version":"0.9.0"}`), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	install := doctorPluginInstall{Client: "codex", ManifestPath: manifest, UpdateHint: "reinstall plugin to align"}

	got := inspectPluginVersion(install, "0.10.0")
	if got.Status != doctorStatusWarn {
		t.Fatalf("status = %q, want warn", got.Status)
	}
	if got.Hint != "reinstall plugin to align" || got.FixCommand == "" {
		t.Fatalf("hint/fix missing: %+v", got)
	}
	if !strings.Contains(got.Message, "0.9.0") || !strings.Contains(got.Message, "0.10.0") {
		t.Fatalf("message should include both versions, got %q", got.Message)
	}
}

func TestInspectPluginVersionNormalizesBuildMetadata(t *testing.T) {
	tests := map[string]struct {
		manifestVersion string
		runningVersion  string
	}{
		"running version has build details": {
			manifestVersion: "0.9.0",
			runningVersion:  "0.9.0 (commit=abc, date=2026, go=1.24)",
		},
		"manifest version has build details": {
			manifestVersion: "v0.9.0 (commit=abc)",
			runningVersion:  "0.9.0",
		},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			manifest := filepath.Join(t.TempDir(), "plugin.json")
			content := []byte(`{"name":"traceary","version":"` + tt.manifestVersion + `"}`)
			if err := os.WriteFile(manifest, content, 0o644); err != nil {
				t.Fatalf("WriteFile() error = %v", err)
			}
			install := doctorPluginInstall{Client: "codex", ManifestPath: manifest, UpdateHint: "reinstall plugin to align"}

			got := inspectPluginVersion(install, tt.runningVersion)
			if got.Status != doctorStatusPass {
				t.Fatalf("status = %q, want pass; msg=%q", got.Status, got.Message)
			}
		})
	}
}

func TestInspectPluginVersionSoftPassesDevBuilds(t *testing.T) {
	tests := map[string]string{
		"pseudo version": "0.9.1-0.20260425142357-7c744ac214bf",
		"devel marker":   "devel (local build)",
		"dirty marker":   "0.10.0 (commit=abc, dirty)",
	}

	for name, runningVersion := range tests {
		t.Run(name, func(t *testing.T) {
			manifest := filepath.Join(t.TempDir(), "plugin.json")
			if err := os.WriteFile(manifest, []byte(`{"name":"traceary","version":"0.9.0"}`), 0o644); err != nil {
				t.Fatalf("WriteFile() error = %v", err)
			}
			install := doctorPluginInstall{Client: "codex", ManifestPath: manifest, UpdateHint: "reinstall plugin to align"}

			got := inspectPluginVersion(install, runningVersion)
			if got.Status != doctorStatusPass {
				t.Fatalf("status = %q, want pass; msg=%q", got.Status, got.Message)
			}
			if got.Hint != "running a dev build (rebuild + reinstall plugin to verify version alignment)" {
				t.Fatalf("hint = %q, want dev build hint", got.Hint)
			}
			if got.FixCommand != "" {
				t.Fatalf("FixCommand = %q, want empty", got.FixCommand)
			}
		})
	}
}

func TestInspectPluginVersionDevBuildValidatesManifestBeforeSoftPass(t *testing.T) {
	tests := map[string]struct {
		manifestContent string
		manifestPath    func(t *testing.T) string
		wantStatus      string
		wantMessagePart string
		wantHint        string
	}{
		"valid mismatched plugin version soft-passes comparison": {
			manifestContent: `{"name":"traceary","version":"0.9.0"}`,
			wantStatus:      doctorStatusPass,
			wantMessagePart: "soft-passed",
			wantHint:        "running a dev build (rebuild + reinstall plugin to verify version alignment)",
		},
		"unreadable manifest fails before soft-pass": {
			manifestPath: func(t *testing.T) string {
				t.Helper()
				return t.TempDir()
			},
			wantStatus:      doctorStatusFail,
			wantMessagePart: "failed to read codex plugin manifest version",
		},
		"parse error fails before soft-pass": {
			manifestContent: `{`,
			wantStatus:      doctorStatusFail,
			wantMessagePart: "failed to read codex plugin manifest version",
		},
		"missing version warns before soft-pass": {
			manifestContent: `{"name":"traceary"}`,
			wantStatus:      doctorStatusWarn,
			wantMessagePart: "has no version",
			wantHint:        "reinstall plugin to align",
		},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			manifest := ""
			if tt.manifestPath != nil {
				manifest = tt.manifestPath(t)
			} else {
				manifest = filepath.Join(t.TempDir(), "plugin.json")
				if err := os.WriteFile(manifest, []byte(tt.manifestContent), 0o644); err != nil {
					t.Fatalf("WriteFile() error = %v", err)
				}
			}
			install := doctorPluginInstall{Client: "codex", ManifestPath: manifest, UpdateHint: "reinstall plugin to align"}

			got := inspectPluginVersion(install, "dev")
			if got.Status != tt.wantStatus {
				t.Fatalf("status = %q, want %q; msg=%q", got.Status, tt.wantStatus, got.Message)
			}
			if !strings.Contains(got.Message, tt.wantMessagePart) {
				t.Fatalf("message = %q, want to contain %q", got.Message, tt.wantMessagePart)
			}
			if got.Hint != tt.wantHint {
				t.Fatalf("hint = %q, want %q", got.Hint, tt.wantHint)
			}
		})
	}
}

func TestInspectPluginVersionReleaseTaggedBuildMatch(t *testing.T) {
	manifest := filepath.Join(t.TempDir(), "plugin.json")
	if err := os.WriteFile(manifest, []byte(`{"name":"traceary","version":"0.10.0"}`), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	install := doctorPluginInstall{Client: "gemini", ManifestPath: manifest, UpdateHint: "gemini extensions update traceary"}

	got := inspectPluginVersion(install, "0.10.0")
	if got.Status != doctorStatusPass {
		t.Fatalf("status = %q, want pass; msg=%q", got.Status, got.Message)
	}
	if got.Hint != "" || got.FixCommand != "" {
		t.Fatalf("hint/fix should be empty for matching release: %+v", got)
	}
}

func TestCoalesceAntigravityPluginVersionChecks_skipsIncompleteTwin(t *testing.T) {
	t.Parallel()
	checks := []doctorCheck{
		{
			Name:    "antigravity-plugin-version",
			Status:  doctorStatusPass,
			Message: "antigravity plugin version matches running traceary version 0.28.0 (healthy)",
		},
		{
			Name:       "antigravity-plugin-version",
			Status:     doctorStatusWarn,
			Message:    "antigravity plugin manifest has no version: /tmp/broken/plugin.json",
			Hint:       "reinstall plugin to align",
			FixCommand: "agy plugin install",
		},
		{
			Name:    "codex-plugin-version",
			Status:  doctorStatusWarn,
			Message: "codex plugin version 0.24.0 does not match",
		},
	}
	got := coalesceAntigravityPluginVersionChecks(checks, "0.28.0")
	if got[0].Status != doctorStatusPass {
		t.Fatalf("healthy path status = %q", got[0].Status)
	}
	if got[1].Status != doctorStatusSkip {
		t.Fatalf("incomplete twin status = %q, want skip", got[1].Status)
	}
	if got[1].FixCommand != "" {
		t.Fatalf("skip twin should clear FixCommand, got %q", got[1].FixCommand)
	}
	if got[2].Status != doctorStatusWarn {
		t.Fatalf("non-antigravity check mutated: %+v", got[2])
	}
}

func TestDetectPluginInstallsIncludesAntigravityManifest(t *testing.T) {
	home := t.TempDir()
	SetUserHomeDirFunc(func() (string, error) { return home, nil })
	t.Cleanup(ResetUserHomeDirFunc)
	manifest := filepath.Join(home, ".gemini", "antigravity-cli", "plugins", "traceary", "plugin.json")
	if err := os.MkdirAll(filepath.Dir(manifest), 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	if err := os.WriteFile(manifest, []byte(`{"name":"traceary","version":"0.21.4"}`), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	installs := (&RootCLI{}).detectPluginInstalls()
	for _, install := range installs {
		if install.Client == "antigravity" && install.ManifestPath == manifest {
			return
		}
	}
	t.Fatalf("detectPluginInstalls() = %+v, want Antigravity manifest", installs)
}

func TestDetectPluginInstallsIncludesGrokInstallerRootManifest(t *testing.T) {
	home := t.TempDir()
	SetUserHomeDirFunc(func() (string, error) { return home, nil })
	t.Cleanup(ResetUserHomeDirFunc)
	manifest := filepath.Join(home, ".grok", "installed-plugins", "grok-plugin-be141821", "integrations", "grok-plugin", "plugin.json")
	if err := os.MkdirAll(filepath.Dir(manifest), 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	if err := os.WriteFile(manifest, []byte(`{"name":"traceary-grok","version":"0.39.0"}`), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	installs := (&RootCLI{}).detectPluginInstalls()
	for _, install := range installs {
		if install.Client == "grok" && install.ManifestPath == manifest {
			return
		}
	}
	t.Fatalf("detectPluginInstalls() = %+v, want Grok installer-root manifest", installs)
}

func TestDetectPluginInstallsGrokDoesNotDependOnCloneDirName(t *testing.T) {
	home := t.TempDir()
	SetUserHomeDirFunc(func() (string, error) { return home, nil })
	t.Cleanup(ResetUserHomeDirFunc)
	manifest := filepath.Join(home, ".grok", "installed-plugins", "grok-plugin-deadbeef", "integrations", "grok-plugin", "plugin.json")
	if err := os.MkdirAll(filepath.Dir(manifest), 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	if err := os.WriteFile(manifest, []byte(`{"name":"traceary-grok","version":"0.39.0"}`), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	installs := (&RootCLI{}).detectPluginInstalls()
	for _, install := range installs {
		if install.Client == "grok" && install.ManifestPath == manifest {
			return
		}
	}
	t.Fatalf("detectPluginInstalls() = %+v, want Grok manifest regardless of clone dir name", installs)
}

func TestDetectPluginInstallsReportsAllGrokClones(t *testing.T) {
	home := t.TempDir()
	SetUserHomeDirFunc(func() (string, error) { return home, nil })
	t.Cleanup(ResetUserHomeDirFunc)
	manifestA := filepath.Join(home, ".grok", "installed-plugins", "grok-plugin-aaaaaaaa", "integrations", "grok-plugin", "plugin.json")
	manifestB := filepath.Join(home, ".grok", "installed-plugins", "grok-plugin-bbbbbbbb", "integrations", "grok-plugin", "plugin.json")
	for _, manifest := range []string{manifestA, manifestB} {
		if err := os.MkdirAll(filepath.Dir(manifest), 0o755); err != nil {
			t.Fatalf("MkdirAll() error = %v", err)
		}
		if err := os.WriteFile(manifest, []byte(`{"name":"traceary-grok","version":"0.39.0"}`), 0o600); err != nil {
			t.Fatalf("WriteFile() error = %v", err)
		}
	}

	installs := (&RootCLI{}).detectPluginInstalls()
	grokCount := 0
	for _, install := range installs {
		if install.Client == "grok" {
			grokCount++
		}
	}
	if grokCount != 2 {
		t.Fatalf("detectPluginInstalls() grok count = %d, want 2 (%+v)", grokCount, installs)
	}
}

func TestDetectPluginInstallsIgnoresLegacyGrokPluginsPath(t *testing.T) {
	home := t.TempDir()
	SetUserHomeDirFunc(func() (string, error) { return home, nil })
	t.Cleanup(ResetUserHomeDirFunc)
	legacyManifest := filepath.Join(home, ".grok", "plugins", "traceary", "plugin.json")
	if err := os.MkdirAll(filepath.Dir(legacyManifest), 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	if err := os.WriteFile(legacyManifest, []byte(`{"name":"traceary-grok","version":"0.39.0"}`), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	installs := (&RootCLI{}).detectPluginInstalls()
	for _, install := range installs {
		if install.Client == "grok" {
			t.Fatalf("detectPluginInstalls() = %+v, want no grok install from legacy path", installs)
		}
	}
}

func TestInspectPluginVersionChecksGrokInstallerRootPass(t *testing.T) {
	home := t.TempDir()
	SetUserHomeDirFunc(func() (string, error) { return home, nil })
	t.Cleanup(ResetUserHomeDirFunc)
	manifest := filepath.Join(home, ".grok", "installed-plugins", "grok-plugin-be141821", "integrations", "grok-plugin", "plugin.json")
	if err := os.MkdirAll(filepath.Dir(manifest), 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	if err := os.WriteFile(manifest, []byte(`{"name":"traceary-grok","version":"0.39.0"}`), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	checks := (&RootCLI{}).inspectPluginVersionChecks("0.39.0")
	for _, check := range checks {
		if check.Name == "grok-plugin-version" && check.Status == doctorStatusPass {
			return
		}
	}
	t.Fatalf("inspectPluginVersionChecks() = %+v, want passing grok-plugin-version", checks)
}

func TestInspectPluginVersionChecksGrokInstallerRootWarnsOnMismatch(t *testing.T) {
	home := t.TempDir()
	SetUserHomeDirFunc(func() (string, error) { return home, nil })
	t.Cleanup(ResetUserHomeDirFunc)
	manifest := filepath.Join(home, ".grok", "installed-plugins", "grok-plugin-be141821", "integrations", "grok-plugin", "plugin.json")
	if err := os.MkdirAll(filepath.Dir(manifest), 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	if err := os.WriteFile(manifest, []byte(`{"name":"traceary-grok","version":"0.38.0"}`), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	checks := (&RootCLI{}).inspectPluginVersionChecks("0.39.0")
	for _, check := range checks {
		if check.Name == "grok-plugin-version" {
			if check.Status != doctorStatusWarn {
				t.Fatalf("grok-plugin-version status = %v, want warn", check.Status)
			}
			if !strings.Contains(check.FixCommand, "install-grok-plugin.sh") {
				t.Fatalf("grok-plugin-version FixCommand = %q, want install-grok-plugin.sh", check.FixCommand)
			}
			return
		}
	}
	t.Fatalf("inspectPluginVersionChecks() = %+v, want grok-plugin-version warn", checks)
}

func TestInspectPluginVersionChecksSilentWhenGrokNotInstalled(t *testing.T) {
	home := t.TempDir()
	SetUserHomeDirFunc(func() (string, error) { return home, nil })
	t.Cleanup(ResetUserHomeDirFunc)

	checks := (&RootCLI{}).inspectPluginVersionChecks("0.39.0")
	for _, check := range checks {
		if check.Name == "grok-plugin-version" {
			t.Fatalf("inspectPluginVersionChecks() = %+v, want no grok-plugin-version check when Grok is not installed", checks)
		}
	}
}

func TestNormalizeDoctorVersion(t *testing.T) {
	tests := map[string]string{
		"0.9.0 (commit=abc, date=2026, go=1.24)": "0.9.0",
		"v0.9.0 (commit=abc)":                    "0.9.0",
		" 0.9.0\n":                               "0.9.0",
		"dev (local)":                            "dev",
	}
	for input, want := range tests {
		if got := normalizeDoctorVersion(input); got != want {
			t.Fatalf("normalizeDoctorVersion(%q) = %q, want %q", input, got, want)
		}
	}
}
