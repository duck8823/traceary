package hooks_test

import (
	"bufio"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"
)

func TestTracearySessionScript_StartAndStop(t *testing.T) {
	t.Parallel()

	tempDir := t.TempDir()
	fakeLogPath := filepath.Join(tempDir, "traceary.log")
	fakeTracearyPath := filepath.Join(tempDir, "traceary")
	writeFakeTraceary(t, fakeTracearyPath)

	homeDir := filepath.Join(tempDir, "home")
	if err := os.MkdirAll(homeDir, 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}

	env := append(os.Environ(),
		"TRACEARY_BIN="+fakeTracearyPath,
		"TRACEARY_FAKE_LOG="+fakeLogPath,
		"TRACEARY_REPO=work-context",
		"HOME="+homeDir,
	)

	startInput := `{"session_id":"claude-session","cwd":"/tmp/project"}`
	if err := runHookScript(t, filepath.Join(".", "traceary-session.sh"), env, startInput, "claude", "start"); err != nil {
		t.Fatalf("runHookScript(start) error = %v", err)
	}

	statePath := filepath.Join(homeDir, ".config", "traceary", "hooks", "claude-"+pidString())
	stateValue, err := os.ReadFile(statePath)
	if err != nil {
		t.Fatalf("ReadFile(state) error = %v", err)
	}
	if got, want := strings.TrimSpace(string(stateValue)), "claude-session"; got != want {
		t.Fatalf("state session id = %q, want %q", got, want)
	}

	if err := runHookScript(t, filepath.Join(".", "traceary-session.sh"), env, `{"cwd":"/tmp/project"}`, "claude", "stop"); err != nil {
		t.Fatalf("runHookScript(stop) error = %v", err)
	}

	calls := readLoggedCalls(t, fakeLogPath)
	want := [][]string{
		{"session", "start", "--client", "hook", "--agent", "claude", "--repo", "work-context", "--session-id", "claude-session"},
		{"session", "end", "--client", "hook", "--agent", "claude", "--session-id", "claude-session", "--repo", "work-context"},
	}
	if !reflect.DeepEqual(calls, want) {
		t.Fatalf("logged calls = %#v, want %#v", calls, want)
	}
	if _, err := os.Stat(statePath); !os.IsNotExist(err) {
		t.Fatalf("state file still exists: %v", err)
	}
}

func TestTracearySessionScript_UsesGeneratedSessionIDWhenInputIsEmpty(t *testing.T) {
	t.Parallel()

	tempDir := t.TempDir()
	fakeLogPath := filepath.Join(tempDir, "traceary.log")
	fakeTracearyPath := filepath.Join(tempDir, "traceary")
	writeFakeTraceary(t, fakeTracearyPath)

	homeDir := filepath.Join(tempDir, "home")
	if err := os.MkdirAll(homeDir, 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}

	env := append(os.Environ(),
		"TRACEARY_BIN="+fakeTracearyPath,
		"TRACEARY_FAKE_LOG="+fakeLogPath,
		"TRACEARY_REPO=work-context",
		"HOME="+homeDir,
	)

	if err := runHookScript(t, filepath.Join(".", "traceary-session.sh"), env, `{"cwd":"/tmp/project"}`, "codex", "start"); err != nil {
		t.Fatalf("runHookScript(start) error = %v", err)
	}
	if err := runHookScript(t, filepath.Join(".", "traceary-session.sh"), env, `{"cwd":"/tmp/project"}`, "codex", "stop"); err != nil {
		t.Fatalf("runHookScript(stop) error = %v", err)
	}

	calls := readLoggedCalls(t, fakeLogPath)
	want := [][]string{
		{"session", "start", "--client", "hook", "--agent", "codex", "--repo", "work-context"},
		{"session", "end", "--client", "hook", "--agent", "codex", "--session-id", "generated-session", "--repo", "work-context"},
	}
	if !reflect.DeepEqual(calls, want) {
		t.Fatalf("logged calls = %#v, want %#v", calls, want)
	}
}

func TestTracearyAuditScript_UsesHookPayloadAndSessionState(t *testing.T) {
	t.Parallel()

	tempDir := t.TempDir()
	fakeLogPath := filepath.Join(tempDir, "traceary.log")
	fakeTracearyPath := filepath.Join(tempDir, "traceary")
	writeFakeTraceary(t, fakeTracearyPath)

	homeDir := filepath.Join(tempDir, "home")
	if err := os.MkdirAll(homeDir, 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}

	env := append(os.Environ(),
		"TRACEARY_BIN="+fakeTracearyPath,
		"TRACEARY_FAKE_LOG="+fakeLogPath,
		"TRACEARY_REPO=work-context",
		"HOME="+homeDir,
	)

	if err := runHookScript(t, filepath.Join(".", "traceary-session.sh"), env, `{"cwd":"/tmp/project"}`, "codex", "start"); err != nil {
		t.Fatalf("runHookScript(start) error = %v", err)
	}

	auditInput := `{"cwd":"/tmp/project","tool_input":{"command":"go test ./...","description":"Run tests"},"tool_response":{"exitCode":0,"stderr":"","stdout":"ok"}}`
	if err := runHookScript(t, filepath.Join(".", "traceary-audit.sh"), env, auditInput, "codex"); err != nil {
		t.Fatalf("runHookScript(audit) error = %v", err)
	}

	calls := readLoggedCalls(t, fakeLogPath)
	if len(calls) != 2 {
		t.Fatalf("len(calls) = %d, want %d", len(calls), 2)
	}

	wantAudit := []string{
		"audit",
		"go test ./...",
		`{"command":"go test ./...","description":"Run tests"}`,
		`{"exitCode":0,"stderr":"","stdout":"ok"}`,
		"--client", "hook",
		"--agent", "codex",
		"--session-id", "generated-session",
		"--repo", "work-context",
	}
	if !reflect.DeepEqual(calls[1], wantAudit) {
		t.Fatalf("audit call = %#v, want %#v", calls[1], wantAudit)
	}
}

func TestTracearyAuditScript_UsesFailurePayloadWhenToolResponseIsMissing(t *testing.T) {
	t.Parallel()

	tempDir := t.TempDir()
	fakeLogPath := filepath.Join(tempDir, "traceary.log")
	fakeTracearyPath := filepath.Join(tempDir, "traceary")
	writeFakeTraceary(t, fakeTracearyPath)

	homeDir := filepath.Join(tempDir, "home")
	if err := os.MkdirAll(homeDir, 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}

	env := append(os.Environ(),
		"TRACEARY_BIN="+fakeTracearyPath,
		"TRACEARY_FAKE_LOG="+fakeLogPath,
		"TRACEARY_REPO=work-context",
		"HOME="+homeDir,
	)

	payload := `{"session_id":"session-123","cwd":"/tmp/project","tool_input":{"command":"npm test","description":"Run tests"},"error":"Command exited with status 1","is_interrupt":false}`
	if err := runHookScript(t, filepath.Join(".", "traceary-audit.sh"), env, payload, "claude"); err != nil {
		t.Fatalf("runHookScript(audit) error = %v", err)
	}

	calls := readLoggedCalls(t, fakeLogPath)
	want := []string{
		"audit",
		"npm test",
		`{"command":"npm test","description":"Run tests"}`,
		`{"error":"Command exited with status 1","is_interrupt":false}`,
		"--client", "hook",
		"--agent", "claude",
		"--session-id", "session-123",
		"--repo", "work-context",
	}
	if !reflect.DeepEqual(calls[0], want) {
		t.Fatalf("audit call = %#v, want %#v", calls[0], want)
	}
}

func runHookScript(t *testing.T, scriptPath string, env []string, input string, args ...string) error {
	t.Helper()

	commandArgs := append([]string{scriptPath}, args...)
	cmd := exec.Command("bash", commandArgs...)
	cmd.Dir = "."
	cmd.Env = env
	cmd.Stdin = strings.NewReader(input)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return &hookScriptError{err: err, output: string(output)}
	}
	return nil
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
python3 - "$TRACEARY_FAKE_LOG" "$@" <<'PY'
import json
import os
import sys
from urllib.parse import urlparse

log_path = sys.argv[1]
args = sys.argv[2:]

if len(args) >= 3 and args[0] == 'hooks' and args[1] == 'helper':
    helper_name = args[2]
    raw = os.environ.get('TRACEARY_HOOK_INPUT', '')
    if helper_name == 'json-get':
        path = args[3]
        default_value = args[4] if len(args) >= 5 else ''
        if not raw.strip():
            sys.stdout.write(default_value)
            raise SystemExit(0)
        try:
            current = json.loads(raw)
        except json.JSONDecodeError:
            sys.stdout.write(default_value)
            raise SystemExit(0)
        for part in path.split('.'):
            if not part:
                continue
            if isinstance(current, dict) and part in current:
                current = current[part]
                continue
            sys.stdout.write(default_value)
            raise SystemExit(0)
        if current is None:
            sys.stdout.write(default_value)
        elif isinstance(current, (dict, list)):
            sys.stdout.write(json.dumps(current, ensure_ascii=False, separators=(",", ":"), sort_keys=True))
        else:
            sys.stdout.write(str(current))
        raise SystemExit(0)
    if helper_name == 'build-failure-output':
        if not raw.strip():
            raise SystemExit(0)
        try:
            payload = json.loads(raw)
        except json.JSONDecodeError:
            raise SystemExit(0)
        result = {}
        if payload.get('error') not in (None, ''):
            result['error'] = payload['error']
        if 'is_interrupt' in payload:
            result['is_interrupt'] = payload['is_interrupt']
        if result:
            sys.stdout.write(json.dumps(result, ensure_ascii=False, separators=(",", ":"), sort_keys=True))
        raise SystemExit(0)
    if helper_name == 'normalize-git-remote':
        raw_remote = args[3] if len(args) >= 4 else ''
        raw_remote = raw_remote.strip()
        if raw_remote.endswith('.git'):
            raw_remote = raw_remote[:-4]
        if raw_remote.startswith('git@') and ':' in raw_remote:
            host_and_path = raw_remote[4:]
            host, path = host_and_path.split(':', 1)
            sys.stdout.write(host.lower().strip('/') + '/' + path.strip('/'))
            raise SystemExit(0)
        parsed = urlparse(raw_remote)
        if parsed.hostname:
            sys.stdout.write(parsed.hostname.lower() + '/' + parsed.path.strip('/'))
            raise SystemExit(0)
        sys.stdout.write(raw_remote)
        raise SystemExit(0)

with open(log_path, 'a', encoding='utf-8') as f:
    f.write(json.dumps(args, ensure_ascii=False) + "\n")

if len(args) >= 2 and args[0] == 'session' and args[1] == 'start':
    if '--session-id' in args:
        session_id = args[args.index('--session-id') + 1]
    else:
        session_id = 'generated-session'
    print(session_id)
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

func pidString() string {
	return strconv.Itoa(os.Getpid())
}
