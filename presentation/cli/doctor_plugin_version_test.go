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
