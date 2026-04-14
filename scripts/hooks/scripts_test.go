package hooks_test

import (
	"bufio"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
)

func TestTracearySessionScript_StartDelegatesToHookRuntime(t *testing.T) {
	t.Parallel()

	tempDir := t.TempDir()
	fakeLogPath := filepath.Join(tempDir, "traceary.log")
	fakeTracearyPath := filepath.Join(tempDir, "traceary")
	writeFakeTraceary(t, fakeTracearyPath)

	env := append(os.Environ(),
		"TRACEARY_BIN="+fakeTracearyPath,
		"TRACEARY_FAKE_LOG="+fakeLogPath,
		"TRACEARY_FAKE_SESSION_OUTPUT=generated-session\n",
	)

	stdout, err := runHookScriptCapture(t, filepath.Join(".", "traceary-session.sh"), env, `{"session_id":"claude-session","cwd":"/tmp/project"}`, "claude", "start")
	if err != nil {
		t.Fatalf("runHookScriptCapture() error = %v", err)
	}
	if diff := cmp.Diff("generated-session\n", stdout); diff != "" {
		t.Fatalf("stdout mismatch (-want +got):\n%s", diff)
	}

	calls := readLoggedCalls(t, fakeLogPath)
	want := [][]string{{"hook", "session", "claude", "start"}}
	if diff := cmp.Diff(want, calls); diff != "" {
		t.Fatalf("logged calls mismatch (-want +got):\n%s", diff)
	}
}

func TestTracearySessionScript_StopAppendsDBPathAndSuppressesStdout(t *testing.T) {
	t.Parallel()

	tempDir := t.TempDir()
	fakeLogPath := filepath.Join(tempDir, "traceary.log")
	fakeTracearyPath := filepath.Join(tempDir, "traceary")
	writeFakeTraceary(t, fakeTracearyPath)

	env := append(os.Environ(),
		"TRACEARY_BIN="+fakeTracearyPath,
		"TRACEARY_FAKE_LOG="+fakeLogPath,
		"TRACEARY_DB_PATH=/tmp/traceary.db",
	)

	stdout, err := runHookScriptCapture(t, filepath.Join(".", "traceary-session.sh"), env, `{"cwd":"/tmp/project"}`, "codex", "stop")
	if err != nil {
		t.Fatalf("runHookScriptCapture() error = %v", err)
	}
	if diff := cmp.Diff("", stdout); diff != "" {
		t.Fatalf("stdout mismatch (-want +got):\n%s", diff)
	}

	calls := readLoggedCalls(t, fakeLogPath)
	want := [][]string{{"hook", "session", "codex", "stop", "--db-path", "/tmp/traceary.db"}}
	if diff := cmp.Diff(want, calls); diff != "" {
		t.Fatalf("logged calls mismatch (-want +got):\n%s", diff)
	}
}

func TestTracearyAuditScript_DelegatesToHookRuntime(t *testing.T) {
	t.Parallel()

	tempDir := t.TempDir()
	fakeLogPath := filepath.Join(tempDir, "traceary.log")
	fakeTracearyPath := filepath.Join(tempDir, "traceary")
	writeFakeTraceary(t, fakeTracearyPath)

	env := append(os.Environ(),
		"TRACEARY_BIN="+fakeTracearyPath,
		"TRACEARY_FAKE_LOG="+fakeLogPath,
	)

	stdout, err := runHookScriptCapture(t, filepath.Join(".", "traceary-audit.sh"), env, `{"tool_input":{"command":"go test ./..."}}`, "gemini")
	if err != nil {
		t.Fatalf("runHookScriptCapture() error = %v", err)
	}
	if diff := cmp.Diff("", stdout); diff != "" {
		t.Fatalf("stdout mismatch (-want +got):\n%s", diff)
	}

	calls := readLoggedCalls(t, fakeLogPath)
	want := [][]string{{"hook", "audit", "gemini"}}
	if diff := cmp.Diff(want, calls); diff != "" {
		t.Fatalf("logged calls mismatch (-want +got):\n%s", diff)
	}
}

func TestTracearyCompactScript_PostCompactSuppressesStdout(t *testing.T) {
	t.Parallel()

	tempDir := t.TempDir()
	fakeLogPath := filepath.Join(tempDir, "traceary.log")
	fakeTracearyPath := filepath.Join(tempDir, "traceary")
	writeFakeTraceary(t, fakeTracearyPath)

	env := append(os.Environ(),
		"TRACEARY_BIN="+fakeTracearyPath,
		"TRACEARY_FAKE_LOG="+fakeLogPath,
		"TRACEARY_FAKE_COMPACT_OUTPUT=context-pack\n",
	)

	stdout, err := runHookScriptCapture(t, filepath.Join(".", "traceary-compact.sh"), env, `{"session_id":"session-1"}`, "claude", "post-compact")
	if err != nil {
		t.Fatalf("runHookScriptCapture() error = %v", err)
	}
	if diff := cmp.Diff("", stdout); diff != "" {
		t.Fatalf("stdout mismatch (-want +got):\n%s", diff)
	}

	calls := readLoggedCalls(t, fakeLogPath)
	want := [][]string{{"hook", "compact", "claude", "post-compact"}}
	if diff := cmp.Diff(want, calls); diff != "" {
		t.Fatalf("logged calls mismatch (-want +got):\n%s", diff)
	}
}

func TestTracearyCompactScript_SessionStartCompactPreservesStdout(t *testing.T) {
	t.Parallel()

	tempDir := t.TempDir()
	fakeLogPath := filepath.Join(tempDir, "traceary.log")
	fakeTracearyPath := filepath.Join(tempDir, "traceary")
	writeFakeTraceary(t, fakeTracearyPath)

	env := append(os.Environ(),
		"TRACEARY_BIN="+fakeTracearyPath,
		"TRACEARY_FAKE_LOG="+fakeLogPath,
		"TRACEARY_FAKE_COMPACT_OUTPUT=context-pack\n",
	)

	stdout, err := runHookScriptCapture(t, filepath.Join(".", "traceary-compact.sh"), env, `{"session_id":"session-1"}`, "claude", "session-start-compact")
	if err != nil {
		t.Fatalf("runHookScriptCapture() error = %v", err)
	}
	if diff := cmp.Diff("context-pack\n", stdout); diff != "" {
		t.Fatalf("stdout mismatch (-want +got):\n%s", diff)
	}

	calls := readLoggedCalls(t, fakeLogPath)
	want := [][]string{{"hook", "compact", "claude", "session-start-compact"}}
	if diff := cmp.Diff(want, calls); diff != "" {
		t.Fatalf("logged calls mismatch (-want +got):\n%s", diff)
	}
}

func TestTracearyPromptScript_DelegatesToHookRuntime(t *testing.T) {
	t.Parallel()

	tempDir := t.TempDir()
	fakeLogPath := filepath.Join(tempDir, "traceary.log")
	fakeTracearyPath := filepath.Join(tempDir, "traceary")
	writeFakeTraceary(t, fakeTracearyPath)

	env := append(os.Environ(),
		"TRACEARY_BIN="+fakeTracearyPath,
		"TRACEARY_FAKE_LOG="+fakeLogPath,
	)

	stdout, err := runHookScriptCapture(t, filepath.Join(".", "traceary-prompt.sh"), env, `{"prompt":"hello"}`, "claude")
	if err != nil {
		t.Fatalf("runHookScriptCapture() error = %v", err)
	}
	if diff := cmp.Diff("", stdout); diff != "" {
		t.Fatalf("stdout mismatch (-want +got):\n%s", diff)
	}

	calls := readLoggedCalls(t, fakeLogPath)
	want := [][]string{{"hook", "prompt", "claude"}}
	if diff := cmp.Diff(want, calls); diff != "" {
		t.Fatalf("logged calls mismatch (-want +got):\n%s", diff)
	}
}

func TestTracearyHookScripts_ReturnSuccessWhenTracearyIsMissing(t *testing.T) {
	t.Parallel()

	stdout, err := runHookScriptCapture(t, filepath.Join(".", "traceary-audit.sh"), os.Environ(), `{"tool_input":{"command":"go test ./..."}}`, "claude")
	if err != nil {
		t.Fatalf("runHookScriptCapture() error = %v", err)
	}
	if diff := cmp.Diff("", stdout); diff != "" {
		t.Fatalf("stdout mismatch (-want +got):\n%s", diff)
	}
}

func TestTracearyHookScripts_SwallowTracearyFailures(t *testing.T) {
	t.Parallel()

	tempDir := t.TempDir()
	fakeLogPath := filepath.Join(tempDir, "traceary.log")
	fakeTracearyPath := filepath.Join(tempDir, "traceary")
	writeFakeTraceary(t, fakeTracearyPath)

	env := append(os.Environ(),
		"TRACEARY_BIN="+fakeTracearyPath,
		"TRACEARY_FAKE_LOG="+fakeLogPath,
		"TRACEARY_FAKE_EXIT_CODE=23",
	)

	stdout, err := runHookScriptCapture(t, filepath.Join(".", "traceary-session.sh"), env, `{"cwd":"/tmp/project"}`, "claude", "stop")
	if err != nil {
		t.Fatalf("runHookScriptCapture() error = %v", err)
	}
	if diff := cmp.Diff("", stdout); diff != "" {
		t.Fatalf("stdout mismatch (-want +got):\n%s", diff)
	}

	calls := readLoggedCalls(t, fakeLogPath)
	want := [][]string{{"hook", "session", "claude", "stop"}}
	if diff := cmp.Diff(want, calls); diff != "" {
		t.Fatalf("logged calls mismatch (-want +got):\n%s", diff)
	}
}

func runHookScriptCapture(t *testing.T, scriptPath string, env []string, input string, args ...string) (string, error) {
	t.Helper()

	commandArgs := append([]string{scriptPath}, args...)
	cmd := exec.Command("bash", commandArgs...)
	cmd.Dir = "."
	cmd.Env = env
	cmd.Stdin = strings.NewReader(input)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return "", &hookScriptError{err: err, output: string(output)}
	}
	return string(output), nil
}

type hookScriptError struct {
	err    error
	output string
}

func (e *hookScriptError) Error() string {
	return e.err.Error() + ": " + e.output
}

func writeFakeTraceary(t *testing.T, path string) {
	t.Helper()

	content := `#!/bin/bash
set -euo pipefail
python3 - "$@" <<'PY'
import json
import os
import sys

args = sys.argv[1:]
log_path = os.environ.get('TRACEARY_FAKE_LOG', '').strip()
if log_path:
    with open(log_path, 'a', encoding='utf-8') as f:
        f.write(json.dumps(args, ensure_ascii=False) + "\n")

if len(args) >= 4 and args[0] == 'hook' and args[1] == 'session' and args[3] == 'start':
    sys.stdout.write(os.environ.get('TRACEARY_FAKE_SESSION_OUTPUT', 'generated-session\n'))
elif len(args) >= 4 and args[0] == 'hook' and args[1] == 'compact' and args[3] == 'session-start-compact':
    sys.stdout.write(os.environ.get('TRACEARY_FAKE_COMPACT_OUTPUT', ''))

exit_code = os.environ.get('TRACEARY_FAKE_EXIT_CODE', '').strip()
if exit_code:
    stderr_output = os.environ.get('TRACEARY_FAKE_STDERR', '')
    if stderr_output:
        sys.stderr.write(stderr_output)
    raise SystemExit(int(exit_code))
PY
`
	if err := os.WriteFile(path, []byte(content), 0o755); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
}

func readLoggedCalls(t *testing.T, path string) [][]string {
	t.Helper()

	file, err := os.Open(path)
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	defer func() {
		if closeErr := file.Close(); closeErr != nil {
			t.Fatalf("Close() error = %v", closeErr)
		}
	}()

	var calls [][]string
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		var args []string
		if err := json.Unmarshal(scanner.Bytes(), &args); err != nil {
			t.Fatalf("Unmarshal() error = %v", err)
		}
		calls = append(calls, args)
	}
	if err := scanner.Err(); err != nil {
		t.Fatalf("Scanner.Err() error = %v", err)
	}

	return calls
}
