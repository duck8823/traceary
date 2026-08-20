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
if [ "$1" = "extensions" ] && [ "$2" = "uninstall" ]; then
  rm -rf "$GEMINI_EXTENSION_HOME/traceary"
  exit 0
fi
if [ "$1" = "extensions" ] && [ "$2" = "install" ]; then
  exit 1
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
	logBytes, logErr := os.ReadFile(logPath)
	if logErr != nil {
		t.Fatalf("stub log: %v", logErr)
	}
	logged := string(logBytes)
	uninst := strings.Index(logged, "extensions uninstall traceary")
	inst := strings.Index(logged, "extensions install --consent")
	if uninst < 0 || inst < 0 || uninst > inst {
		t.Fatalf("gemini call order = %q, want uninstall then install", logged)
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

func TestInstallGeminiExtensionSkipsHooksInstallWhenHooksPresent(t *testing.T) {
	repoRoot := repoRootFromScripts(t)
	settings := filepath.Join(repoRoot, ".gemini", "settings.json")
	before, err := os.ReadFile(settings)
	if err != nil {
		t.Fatalf("tracked settings: %v", err)
	}
	if !strings.Contains(string(before), "traceary-session-start") {
		t.Skip("tracked .gemini/settings.json has no traceary-session-start marker")
	}

	home := t.TempDir()
	extHome := filepath.Join(home, ".gemini", "extensions")
	bin := t.TempDir()
	tracearyLog := filepath.Join(t.TempDir(), "traceary.log")
	geminiLog := filepath.Join(t.TempDir(), "gemini.log")
	writeLoggingTracearyStub(t, bin)
	writeStub(t, filepath.Join(bin, "gemini"), `#!/bin/sh
echo "$*" >> "$STUB_LOG"
exit 0
`)
	cmd := exec.Command("bash", filepath.Join(repoRoot, "scripts", "install-gemini-extension.sh"))
	cmd.Dir = repoRoot
	cmd.Env = append(os.Environ(),
		"PATH="+bin+string(os.PathListSeparator)+os.Getenv("PATH"),
		"HOME="+home,
		"GEMINI_EXTENSION_HOME="+extHome,
		"REPO_ROOT="+repoRoot,
		"STUB_LOG="+geminiLog,
		"STUB_TRACEARY_LOG="+tracearyLog,
		"TRACEARY_GEMINI_TIMEOUT=5",
	)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("install failed: %v\n%s", err, out)
	}
	assertNoHooksInstall(t, tracearyLog, out)
	after, readErr := os.ReadFile(settings)
	if readErr != nil {
		t.Fatalf("tracked settings after run: %v", readErr)
	}
	if string(after) != string(before) {
		t.Fatalf("tracked .gemini/settings.json changed; output=%s", out)
	}
}

func TestInstallGeminiExtensionHooksFlagStillSkipsWhenHooksPresent(t *testing.T) {
	repoRoot := repoRootFromScripts(t)
	settings := filepath.Join(repoRoot, ".gemini", "settings.json")
	before, err := os.ReadFile(settings)
	if err != nil {
		t.Fatalf("tracked settings: %v", err)
	}
	if !strings.Contains(string(before), "traceary-session-start") {
		t.Skip("tracked .gemini/settings.json has no traceary-session-start marker")
	}

	home := t.TempDir()
	extHome := filepath.Join(home, ".gemini", "extensions")
	bin := t.TempDir()
	tracearyLog := filepath.Join(t.TempDir(), "traceary.log")
	geminiLog := filepath.Join(t.TempDir(), "gemini.log")
	writeLoggingTracearyStub(t, bin)
	writeStub(t, filepath.Join(bin, "gemini"), `#!/bin/sh
echo "$*" >> "$STUB_LOG"
exit 0
`)
	cmd := exec.Command("bash", filepath.Join(repoRoot, "scripts", "install-gemini-extension.sh"), "--hooks")
	cmd.Dir = repoRoot
	cmd.Env = append(os.Environ(),
		"PATH="+bin+string(os.PathListSeparator)+os.Getenv("PATH"),
		"HOME="+home,
		"GEMINI_EXTENSION_HOME="+extHome,
		"REPO_ROOT="+repoRoot,
		"STUB_LOG="+geminiLog,
		"STUB_TRACEARY_LOG="+tracearyLog,
		"TRACEARY_GEMINI_TIMEOUT=5",
	)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("install --hooks failed: %v\n%s", err, out)
	}
	assertNoHooksInstall(t, tracearyLog, out)
	after, readErr := os.ReadFile(settings)
	if readErr != nil {
		t.Fatalf("tracked settings after run: %v", readErr)
	}
	if string(after) != string(before) {
		t.Fatalf("tracked .gemini/settings.json changed with --hooks; output=%s", out)
	}
}

func TestInstallGeminiExtensionFromSubdirCreatesNoNestedGeminiDir(t *testing.T) {
	repoRoot := repoRootFromScripts(t)
	subdir := filepath.Join(repoRoot, "presentation", "cli")
	if info, err := os.Stat(subdir); err != nil || !info.IsDir() {
		t.Skip("presentation/cli not present")
	}
	nested := filepath.Join(subdir, ".gemini")
	if _, err := os.Stat(nested); err == nil {
		t.Skip("presentation/cli/.gemini already exists locally")
	}

	home := t.TempDir()
	extHome := filepath.Join(home, ".gemini", "extensions")
	bin := t.TempDir()
	tracearyLog := filepath.Join(t.TempDir(), "traceary.log")
	geminiLog := filepath.Join(t.TempDir(), "gemini.log")
	writeLoggingTracearyStub(t, bin)
	writeStub(t, filepath.Join(bin, "gemini"), `#!/bin/sh
echo "$*" >> "$STUB_LOG"
exit 0
`)
	cmd := exec.Command("bash", filepath.Join(repoRoot, "scripts", "install-gemini-extension.sh"))
	cmd.Dir = subdir
	cmd.Env = append(os.Environ(),
		"PATH="+bin+string(os.PathListSeparator)+os.Getenv("PATH"),
		"HOME="+home,
		"GEMINI_EXTENSION_HOME="+extHome,
		"REPO_ROOT="+repoRoot,
		"STUB_LOG="+geminiLog,
		"STUB_TRACEARY_LOG="+tracearyLog,
		"TRACEARY_GEMINI_TIMEOUT=5",
	)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("install from subdir failed: %v\n%s", err, out)
	}
	if _, statErr := os.Stat(nested); !os.IsNotExist(statErr) {
		t.Fatalf("nested %s exists after subdir run (err=%v); output=%s", nested, statErr, out)
	}
	assertNoHooksInstall(t, tracearyLog, out)
	logBytes, logErr := os.ReadFile(geminiLog)
	if logErr != nil {
		t.Fatalf("gemini stub log: %v", logErr)
	}
	if !strings.Contains(string(logBytes), "extensions install --consent") {
		t.Fatalf("gemini log = %q, want extensions install --consent", logBytes)
	}
}

func TestInstallGeminiExtensionHooksFlagPinsProjectDirToRepoRoot(t *testing.T) {
	repoRoot := repoRootFromScripts(t)
	tmpRoot := t.TempDir()
	scriptsDir := filepath.Join(tmpRoot, "scripts")
	if err := os.MkdirAll(scriptsDir, 0o700); err != nil {
		t.Fatal(err)
	}
	scriptBytes, err := os.ReadFile(filepath.Join(repoRoot, "scripts", "install-gemini-extension.sh"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(scriptsDir, "install-gemini-extension.sh"), scriptBytes, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(tmpRoot, "VERSION"), []byte("0.0.0-test\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(tmpRoot, "integrations", "gemini-extension"), 0o700); err != nil {
		t.Fatal(err)
	}
	// No .gemini/settings.json in the temp tree: the marker is absent.

	home := t.TempDir()
	extHome := filepath.Join(home, ".gemini", "extensions")
	bin := t.TempDir()
	tracearyLog := filepath.Join(t.TempDir(), "traceary.log")
	writeStub(t, filepath.Join(bin, "traceary"), `#!/bin/sh
echo "$*" >> "$STUB_TRACEARY_LOG"
exit 0
`)
	writeStub(t, filepath.Join(bin, "gemini"), `#!/bin/sh
exit 0
`)
	subdir := filepath.Join(tmpRoot, "integrations", "gemini-extension")
	cmd := exec.Command("bash", filepath.Join(scriptsDir, "install-gemini-extension.sh"), "--ref", "v0.0.0-test", "--hooks")
	cmd.Dir = subdir
	cmd.Env = append(os.Environ(),
		"PATH="+bin+string(os.PathListSeparator)+os.Getenv("PATH"),
		"HOME="+home,
		"GEMINI_EXTENSION_HOME="+extHome,
		"STUB_TRACEARY_LOG="+tracearyLog,
		"TRACEARY_GEMINI_TIMEOUT=5",
	)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("install --hooks failed: %v\n%s", err, out)
	}
	logBytes, logErr := os.ReadFile(tracearyLog)
	if logErr != nil {
		t.Fatalf("traceary stub log: %v\n%s", logErr, out)
	}
	want := "hooks install --client gemini --project-dir " + tmpRoot
	if !strings.Contains(string(logBytes), want) {
		t.Fatalf("traceary argv = %q, want substring %q", logBytes, want)
	}
	if strings.Contains(string(logBytes), "--project-dir .") {
		t.Fatalf("traceary argv = %q, must not use --project-dir .", logBytes)
	}
	if strings.Contains(string(logBytes), "--force") || strings.Contains(string(logBytes), "--upgrade") {
		t.Fatalf("traceary argv = %q, must not pass --force/--upgrade", logBytes)
	}
	if _, statErr := os.Stat(filepath.Join(subdir, ".gemini")); !os.IsNotExist(statErr) {
		t.Fatalf("nested .gemini created under cwd (err=%v)", statErr)
	}
}

func writeLoggingTracearyStub(t *testing.T, bin string) {
	t.Helper()
	writeStub(t, filepath.Join(bin, "traceary"), `#!/bin/sh
echo "$*" >> "$STUB_TRACEARY_LOG"
if [ "$1" = "-v" ]; then echo "traceary $(cat "$REPO_ROOT/VERSION")"; exit 0; fi
exit 0
`)
}

func assertNoHooksInstall(t *testing.T, tracearyLog string, out []byte) {
	t.Helper()
	logBytes, err := os.ReadFile(tracearyLog)
	if err != nil {
		t.Fatalf("traceary stub log: %v\n%s", err, out)
	}
	if strings.Contains(string(logBytes), "hooks install") {
		t.Fatalf("traceary argv = %q, want no hooks install; output=%s", logBytes, out)
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
