package scripts_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestInstallGeminiExtensionRestoresPreviousOnFailedInstall(t *testing.T) {
	repoRoot := repoRootFromScripts(t)
	home := t.TempDir()
	extHome := filepath.Join(home, ".gemini", "extensions")
	prev := filepath.Join(extHome, "traceary")
	if err := os.MkdirAll(prev, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(prev, "marker"), []byte("previous"), 0o600); err != nil {
		t.Fatal(err)
	}
	bin := t.TempDir()
	writeStub(t, filepath.Join(bin, "traceary"), `#!/bin/sh
if [ "$1" = "-v" ]; then echo "traceary $(cat "$REPO_ROOT/VERSION")"; exit 0; fi
exit 0
`)
	writeStub(t, filepath.Join(bin, "gemini"), `#!/bin/sh
echo "$*" >> "$STUB_LOG"
if [ "$1" = "extensions" ] && [ "$2" = "install" ]; then
  exit 1
fi
if [ "$1" = "extensions" ] && [ "$2" = "uninstall" ]; then
  rm -rf "$GEMINI_EXTENSION_HOME/traceary"
  exit 0
fi
exit 0
`)
	logPath := filepath.Join(t.TempDir(), "gemini.log")
	cmd := exec.Command("bash", filepath.Join(repoRoot, "scripts", "install-gemini-extension.sh"))
	cmd.Dir = repoRoot
	cmd.Env = append(os.Environ(),
		"PATH="+bin+string(os.PathListSeparator)+os.Getenv("PATH"),
		"HOME="+home,
		"GEMINI_EXTENSION_HOME="+extHome,
		"REPO_ROOT="+repoRoot,
		"STUB_LOG="+logPath,
		"TRACEARY_GEMINI_TIMEOUT=5",
	)
	out, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("install succeeded, want failure; output=%s", out)
	}
	got, readErr := os.ReadFile(filepath.Join(prev, "marker"))
	if readErr != nil {
		t.Fatalf("previous extension missing after failed install: %v\n%s", readErr, out)
	}
	if string(got) != "previous" {
		t.Fatalf("marker=%q, want previous; output=%s", got, out)
	}
}

func TestInstallGeminiExtensionTimesOutWithoutRemovingPrevious(t *testing.T) {
	repoRoot := repoRootFromScripts(t)
	home := t.TempDir()
	extHome := filepath.Join(home, ".gemini", "extensions")
	prev := filepath.Join(extHome, "traceary")
	if err := os.MkdirAll(prev, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(prev, "marker"), []byte("previous"), 0o600); err != nil {
		t.Fatal(err)
	}
	bin := t.TempDir()
	writeStub(t, filepath.Join(bin, "traceary"), `#!/bin/sh
if [ "$1" = "-v" ]; then echo "traceary $(cat "$REPO_ROOT/VERSION")"; exit 0; fi
exit 0
`)
	writeStub(t, filepath.Join(bin, "gemini"), `#!/bin/sh
sleep 3
exit 0
`)
	cmd := exec.Command("bash", filepath.Join(repoRoot, "scripts", "install-gemini-extension.sh"))
	cmd.Dir = repoRoot
	cmd.Env = append(os.Environ(),
		"PATH="+bin+string(os.PathListSeparator)+os.Getenv("PATH"),
		"HOME="+home,
		"GEMINI_EXTENSION_HOME="+extHome,
		"REPO_ROOT="+repoRoot,
		"TRACEARY_GEMINI_TIMEOUT=1",
	)
	out, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("install succeeded, want timeout; output=%s", out)
	}
	if !strings.Contains(string(out), "timed out") {
		t.Fatalf("output=%s, want timeout error", out)
	}
	got, readErr := os.ReadFile(filepath.Join(prev, "marker"))
	if readErr != nil {
		t.Fatalf("previous extension missing after timeout: %v\n%s", readErr, out)
	}
	if string(got) != "previous" {
		t.Fatalf("marker=%q, want previous", got)
	}
}

func repoRootFromScripts(t *testing.T) string {
	t.Helper()
	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Base(wd) == "scripts" {
		return filepath.Dir(wd)
	}
	return wd
}

func writeStub(t *testing.T, path, body string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(body), 0o700); err != nil {
		t.Fatal(err)
	}
}
