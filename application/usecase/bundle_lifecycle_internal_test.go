package usecase_test

import (
	"strings"
	"testing"
	"time"

	"github.com/duck8823/traceary/application/usecase"
	"github.com/duck8823/traceary/domain/model"
	"github.com/duck8823/traceary/domain/types"
)

func TestBundleCommandAuditRejectsContradictoryNonZeroHostError(t *testing.T) {
	t.Parallel()
	_, err := usecase.BundleCommandAuditFromJSONForTest([]byte(`{
		"event_id":"event-1",
		"command":"go test ./...",
		"command_name":"go",
		"exit_code":2,
		"failed":true,
		"failure_reason":"host_error"
	}`))
	if err == nil || !strings.Contains(err.Error(), "non-zero exit code cannot have failure reason host_error") {
		t.Fatalf("BundleCommandAuditFromJSONForTest() error = %v, want contradiction", err)
	}
}

func TestBundleSessionRowRoundTripsLifecycleState(t *testing.T) {
	t.Parallel()

	agent, _ := types.AgentFrom("codex")
	startedAt := time.Date(2026, 7, 22, 12, 0, 0, 0, time.UTC)
	session, err := model.NewSessionWithRuntimeMode(types.SessionID("bundle-one-shot"), startedAt, types.Client("hook"), agent, types.Workspace("workspace"), types.RuntimeModeOneShot)
	if err != nil {
		t.Fatalf("NewSessionWithRuntimeMode() error = %v", err)
	}
	if _, err := session.Terminate(startedAt.Add(time.Minute), types.TerminalReasonAbortedStream, "aborted"); err != nil {
		t.Fatalf("Terminate() error = %v", err)
	}

	restored, runtimeMode, terminalReason, err := usecase.BundleSessionRoundTripForTest(session)
	if err != nil {
		t.Fatalf("BundleSessionRoundTripForTest() error = %v", err)
	}
	if runtimeMode != "one_shot" || terminalReason != "aborted_stream" {
		t.Fatalf("encoded lifecycle = %q/%q", runtimeMode, terminalReason)
	}
	if restored.RuntimeMode() != types.RuntimeModeOneShot {
		t.Fatalf("restored RuntimeMode() = %q", restored.RuntimeMode())
	}
	if reason, ok := restored.TerminalReason().Value(); !ok || reason != types.TerminalReasonAbortedStream {
		t.Fatalf("restored TerminalReason() = %q/%v", reason, ok)
	}
}

func TestBundleSessionRowRestoresLegacyFieldsConservatively(t *testing.T) {
	t.Parallel()

	legacy := `{"session_id":"legacy","started_at":"2026-07-22T12:00:00Z","ended_at":"2026-07-22T12:01:00Z","client":"hook","agent":"codex","workspace":"workspace"}`
	restored, err := usecase.LegacyBundleSessionFromJSONForTest([]byte(legacy))
	if err != nil {
		t.Fatalf("LegacyBundleSessionFromJSONForTest() error = %v", err)
	}
	if restored.RuntimeMode() != types.RuntimeModeInteractive {
		t.Fatalf("legacy RuntimeMode() = %q, want interactive", restored.RuntimeMode())
	}
	if reason, ok := restored.TerminalReason().Value(); !ok || reason != types.TerminalReasonLegacyUnknown {
		t.Fatalf("legacy TerminalReason() = %q/%v, want legacy_unknown/present", reason, ok)
	}
}
