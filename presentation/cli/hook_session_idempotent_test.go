package cli_test

import (
	"bytes"
	"github.com/duck8823/traceary/domain/types"
	"strings"
	"testing"
	"time"

	"github.com/duck8823/traceary/domain/model"
	"github.com/duck8823/traceary/presentation/cli"
)

func TestHookSessionStart_AlreadyExistsIsIdempotentForSpoolReplay(t *testing.T) {
	t.Setenv("TRACEARY_HOOK_STATE_DIR", t.TempDir())

	sessionStub := &sessionUsecaseStub{
		startEvent: model.EventOf(types.EventID("canonical-start"), types.EventKindSessionStarted, "hook", "codex", "session-already", "/tmp", "session started", time.Now()),
	}
	rootCmd := newTestRootCLI(cli.WithStoreManagement(&storeManagementUsecaseStub{}), cli.WithSession(sessionStub)).Command()
	stdout := &bytes.Buffer{}
	stderr := &bytes.Buffer{}
	rootCmd.SetOut(stdout)
	rootCmd.SetErr(stderr)
	rootCmd.SetIn(strings.NewReader(`{"session_id":"session-already","cwd":"/tmp","hook_event_name":"SessionStart"}`))
	rootCmd.SetArgs([]string{"hook", "session", "codex", "start"})
	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("Execute() error = %v stderr=%s", err, stderr.String())
	}
	// SessionStart stdout is the wake-injection channel only (#1684); a bare
	// session id must not appear. With no wake query wired, expect empty.
	if len(stdout.Bytes()) != 0 {
		t.Fatalf("stdout = %q, want empty (no accidental session-id injection)", stdout.String())
	}
	if sessionStub.startCall.sessionID.String() != "session-already" {
		t.Fatalf("Start sessionID = %q", sessionStub.startCall.sessionID)
	}
}

func TestHookSubagentStart_AlreadyExistsIsIdempotentForSpoolReplay(t *testing.T) {
	t.Setenv("TRACEARY_HOOK_STATE_DIR", t.TempDir())

	// StartChild reuses startErr on the stub (same as Start).
	sessionStub := &sessionUsecaseStub{}
	rootCmd := newTestRootCLI(
		cli.WithStoreManagement(&storeManagementUsecaseStub{}),
		cli.WithSession(sessionStub),
		cli.WithEvent(&eventUsecaseStub{}),
	).Command()
	stdout := &bytes.Buffer{}
	stderr := &bytes.Buffer{}
	rootCmd.SetOut(stdout)
	rootCmd.SetErr(stderr)
	rootCmd.SetIn(strings.NewReader(`{"session_id":"parent-session","agent_id":"agent-already","agent_type":"reviewer","cwd":"/tmp"}`))
	rootCmd.SetArgs([]string{"hook", "subagent-start", "codex"})

	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("Execute(subagent-start) error = %v\nstderr=%s", err, stderr.String())
	}
	if len(sessionStub.startChildCalls) != 1 {
		t.Fatalf("StartChild calls = %d, want 1", len(sessionStub.startChildCalls))
	}
}

func TestHookSubagentStop_AlreadyExistsIsIdempotentForSpoolReplay(t *testing.T) {
	t.Setenv("TRACEARY_HOOK_STATE_DIR", t.TempDir())

	// No active subagent state → stop synthesizes StartChild then End.
	// Both hit ErrInvalidSessionState (already committed before kill).
	sessionStub := &sessionUsecaseStub{}
	rootCmd := newTestRootCLI(
		cli.WithStoreManagement(&storeManagementUsecaseStub{}),
		cli.WithSession(sessionStub),
		cli.WithEvent(&eventUsecaseStub{}),
	).Command()
	stdout := &bytes.Buffer{}
	stderr := &bytes.Buffer{}
	rootCmd.SetOut(stdout)
	rootCmd.SetErr(stderr)
	// tool_use_id present so stop synthesizes the child when active state is missing.
	rootCmd.SetIn(strings.NewReader(`{"session_id":"parent-session","agent_id":"agent-stop-already","agent_type":"reviewer","cwd":"/tmp"}`))
	rootCmd.SetArgs([]string{"hook", "subagent-stop", "codex"})

	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("Execute(subagent-stop) error = %v\nstderr=%s", err, stderr.String())
	}
	if len(sessionStub.startChildCalls) != 1 {
		t.Fatalf("StartChild calls = %d, want 1 (lazy synthesize)", len(sessionStub.startChildCalls))
	}
	if len(sessionStub.endCalls) != 1 {
		t.Fatalf("End calls = %d, want 1", len(sessionStub.endCalls))
	}
}

func TestHookSessionEndSupervisorOwnershipRefusalDrainsReplay(t *testing.T) {
	t.Setenv("TRACEARY_HOOK_STATE_DIR", t.TempDir())
	t.Setenv("TRACEARY_RUNTIME_SESSION_ID", "")
	t.Setenv("TRACEARY_RUNTIME_MODE", "")
	sessions := &sessionUsecaseStub{endErr: model.ErrSupervisorOwnedSession}
	for attempt := 0; attempt < 2; attempt++ {
		cmd := newTestRootCLI(cli.WithStoreManagement(&storeManagementUsecaseStub{}), cli.WithSession(sessions), cli.WithEvent(&eventUsecaseStub{})).Command()
		out, errs := &bytes.Buffer{}, &bytes.Buffer{}
		cmd.SetOut(out)
		cmd.SetErr(errs)
		cmd.SetIn(strings.NewReader(`{"session_id":"owned","cwd":"/tmp"}`))
		cmd.SetArgs([]string{"hook", "session", "claude", "end"})
		if err := cmd.Execute(); err != nil {
			t.Fatalf("end/replay refused to drain: %v", err)
		}
	}
	if len(sessions.endCalls) != 2 {
		t.Fatalf("calls=%d want one refusal then local replay cleanup", len(sessions.endCalls))
	}
}
