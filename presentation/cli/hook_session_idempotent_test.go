package cli_test

import (
	"bytes"
	"strings"
	"testing"

	"github.com/duck8823/traceary/domain/model"
	"github.com/duck8823/traceary/presentation/cli"
)

func TestHookSessionStart_AlreadyExistsIsIdempotentForSpoolReplay(t *testing.T) {
	t.Setenv("TRACEARY_HOOK_STATE_DIR", t.TempDir())

	sessionStub := &sessionUsecaseStub{
		startErr: model.ErrInvalidSessionState,
	}
	rootCmd := newTestRootCLI(
		cli.WithStoreManagement(&storeManagementUsecaseStub{}),
		cli.WithSession(sessionStub),
	).Command()
	stdout := &bytes.Buffer{}
	stderr := &bytes.Buffer{}
	rootCmd.SetOut(stdout)
	rootCmd.SetErr(stderr)
	rootCmd.SetIn(strings.NewReader(`{"session_id":"session-already","cwd":"/tmp","hook_event_name":"SessionStart"}`))
	rootCmd.SetArgs([]string{"hook", "session", "codex", "start"})

	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("Execute() error = %v\nstderr=%s", err, stderr.String())
	}
	if !strings.Contains(stdout.String(), "session-already") {
		t.Fatalf("stdout = %q, want session id printed for idempotent start", stdout.String())
	}
	if sessionStub.startCall.sessionID.String() != "session-already" {
		t.Fatalf("Start sessionID = %q", sessionStub.startCall.sessionID)
	}
}
