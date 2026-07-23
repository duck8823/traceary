package cli_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"

	"github.com/duck8823/traceary/domain/model"
	"github.com/duck8823/traceary/domain/types"
	"github.com/duck8823/traceary/infrastructure/filesystem"
	"github.com/duck8823/traceary/presentation/cli"
)

func TestRootCLI_SessionRunCommand_FinalizesAuthoritativeOutcomes(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name       string
		command    []string
		extraArgs  []string
		wantReason types.TerminalReason
		wantCode   int
		wantOutput string
	}{
		{name: "success", command: []string{"sh", "-c", "printf child-output"}, wantReason: types.TerminalReasonSuccess, wantOutput: "child-output"},
		{name: "failure", command: []string{"sh", "-c", "exit 7"}, wantReason: types.TerminalReasonFailure, wantCode: 7},
		{name: "signal", command: []string{"sh", "-c", "kill -TERM $$"}, wantReason: types.TerminalReasonSignal, wantCode: 143},
		{name: "timeout", command: []string{"sh", "-c", "sleep 1"}, extraArgs: []string{"--timeout", "10ms"}, wantReason: types.TerminalReasonTimeout, wantCode: 124},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			sessionID := types.SessionID("one-shot-" + tc.name)
			sessionStub := &sessionUsecaseStub{startEvent: model.EventOf(
				types.EventID("event-"+tc.name), types.EventKindSessionStarted, "cli", "codex", sessionID, "duck8823/traceary", "session started", time.Now(),
			)}
			stdout := &bytes.Buffer{}
			rootCmd := cli.NewRootCLI(cli.WithStoreManagement(&storeManagementUsecaseStub{}), cli.WithSession(sessionStub)).Command()
			rootCmd.SetOut(stdout)
			rootCmd.SetErr(&bytes.Buffer{})
			args := append([]string{"session", "run", "--db-path", filepath.Join(t.TempDir(), "traceary.db"), "--agent", "codex"}, tc.extraArgs...)
			args = append(args, "--")
			args = append(args, tc.command...)
			rootCmd.SetArgs(args)

			err := rootCmd.Execute()
			if tc.wantCode == 0 && err != nil {
				t.Fatalf("Execute() error = %v", err)
			}
			if tc.wantCode != 0 {
				var exitCoder interface{ ExitCode() int }
				if !errors.As(err, &exitCoder) || exitCoder.ExitCode() != tc.wantCode {
					t.Fatalf("Execute() error = %v, exit code = %v, want %d", err, exitCoder, tc.wantCode)
				}
			}
			if got := sessionStub.startCall.runtimeMode; got != types.RuntimeModeOneShot {
				t.Fatalf("StartWithRuntimeMode mode = %q, want one_shot", got)
			}
			if sessionStub.finalizeSessionID != sessionID || sessionStub.finalizeReason != tc.wantReason {
				t.Fatalf("FinalizeOneShot() = (%q, %q), want (%q, %q)", sessionStub.finalizeSessionID, sessionStub.finalizeReason, sessionID, tc.wantReason)
			}
			if diff := cmp.Diff(tc.wantOutput, stdout.String()); diff != "" {
				t.Fatalf("stdout mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

func TestRootCLI_SessionRunCommand_FinalizesAfterParentCancellation(t *testing.T) {
	t.Parallel()
	sessionStub := &sessionUsecaseStub{startEvent: model.EventOf(
		"event-cancel", types.EventKindSessionStarted, "cli", "codex", "one-shot-cancel", "duck8823/traceary", "session started", time.Now(),
	)}
	rootCmd := cli.NewRootCLI(cli.WithStoreManagement(&storeManagementUsecaseStub{}), cli.WithSession(sessionStub)).Command()
	rootCmd.SetOut(&bytes.Buffer{})
	rootCmd.SetErr(&bytes.Buffer{})
	rootCmd.SetArgs([]string{"session", "run", "--db-path", filepath.Join(t.TempDir(), "traceary.db"), "--", "sh", "-c", "sleep 10"})
	ctx, cancel := context.WithCancel(context.Background())
	time.AfterFunc(20*time.Millisecond, cancel)

	err := rootCmd.ExecuteContext(ctx)
	var exitCoder interface{ ExitCode() int }
	if !errors.As(err, &exitCoder) || exitCoder.ExitCode() != 74 {
		t.Fatalf("ExecuteContext() error = %v, want exit code 74", err)
	}
	if sessionStub.finalizeReason != types.TerminalReasonAbortedStream {
		t.Fatalf("FinalizeOneShot reason = %q, want aborted_stream", sessionStub.finalizeReason)
	}
	if sessionStub.finalizeContextErr != nil {
		t.Fatalf("FinalizeOneShot context error = %v, want independent finalization context", sessionStub.finalizeContextErr)
	}
}

func TestRootCLI_SessionRunCommand_CapturesBodyFreeCodexHeadlessUsage(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	codex := filepath.Join(dir, "codex")
	fixture := `{"type":"thread.started","thread_id":"thread-1"}` + "\n" +
		`{"type":"item.completed","item":{"type":"agent_message","text":"private body stays on stdout"}}` + "\n" +
		`{"type":"turn.completed","usage":{"input_tokens":21,"cached_input_tokens":10,"cache_write_input_tokens":0,"output_tokens":5,"reasoning_output_tokens":2}}` + "\n"
	script := "#!/bin/sh\nprintf '%s' '" + fixture + "'\n"
	if err := os.WriteFile(codex, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	sessionID := types.SessionID("one-shot-headless")
	sessionStub := &sessionUsecaseStub{startEvent: model.EventOf(
		"event-headless", types.EventKindSessionStarted, "cli", "codex", sessionID, "duck8823/traceary", "session started", time.Now(),
	)}
	usage := &codexUsageCaptureStub{}
	stdout := &bytes.Buffer{}
	rootCmd := cli.NewRootCLI(
		cli.WithStoreManagement(&storeManagementUsecaseStub{}),
		cli.WithSession(sessionStub),
		cli.WithCodexUsage(usage),
		cli.WithCodexHeadlessUsage(filesystem.NewCodexHeadlessUsageStreamFactory()),
	).Command()
	rootCmd.SetOut(stdout)
	rootCmd.SetErr(&bytes.Buffer{})
	rootCmd.SetArgs([]string{"session", "run", "--db-path", filepath.Join(dir, "traceary.db"), "--session-id", sessionID.String(), "--", codex, "exec", "--json"})
	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if stdout.String() != fixture {
		t.Fatalf("stdout changed = %q", stdout.String())
	}
	if len(usage.headless) != 1 || len(usage.headless[0].Samples) != 1 {
		t.Fatalf("headless capture = %+v", usage.headless)
	}
	sample := usage.headless[0].Samples[0]
	if sample.RecordID != "headless_stream:thread-1:1" || sample.Model != "" || sample.Counters.TotalTokens != nil {
		t.Fatalf("headless sample = %+v", sample)
	}
	if sessionStub.finalizeReason != types.TerminalReasonSuccess {
		t.Fatalf("finalize reason = %q", sessionStub.finalizeReason)
	}
}

func TestRootCLI_SessionStartCommand(t *testing.T) {
	t.Parallel()

	eventID, err := types.EventIDFrom("event-1")
	if err != nil {
		t.Fatalf("EventIDFrom() error = %v", err)
	}
	agent, err := types.AgentFrom("codex")
	if err != nil {
		t.Fatalf("AgentFrom() error = %v", err)
	}
	sessionID, err := types.SessionIDFrom("session-1")
	if err != nil {
		t.Fatalf("SessionIDFrom() error = %v", err)
	}

	stdout := &bytes.Buffer{}
	rootCmd := cli.NewRootCLI(
		cli.WithStoreManagement(&storeManagementUsecaseStub{}),
		cli.WithSession(&sessionUsecaseStub{
			startEvent: model.EventOf(
				eventID,
				types.EventKindSessionStarted,
				"cli",
				agent,
				sessionID,
				"duck8823/traceary",
				"session started",
				time.Date(2026, 4, 7, 13, 0, 0, 0, time.UTC),
			),
		}),
	).Command()
	rootCmd.SetOut(stdout)
	rootCmd.SetErr(&bytes.Buffer{})
	rootCmd.SetArgs([]string{
		"session",
		"start",
		"--db-path",
		"/tmp/test-traceary.db",
		"--client", "cli",
		"--agent", "codex",
		"--workspace", "duck8823/traceary",
	})

	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if diff := cmp.Diff("session-1\n", stdout.String()); diff != "" {
		t.Fatalf("stdout mismatch (-want +got):\n%s", diff)
	}
}

func TestRootCLI_SessionStartCommand_AcceptsAndSerializesGrokClient(t *testing.T) {
	t.Parallel()

	sessionStub := &sessionUsecaseStub{startEvent: model.EventOf(
		types.EventID("event-grok"),
		types.EventKindSessionStarted,
		types.Client("grok"),
		types.Agent("grok"),
		types.SessionID("session-grok"),
		types.Workspace("duck8823/traceary"),
		"session started",
		time.Date(2026, 7, 13, 12, 0, 0, 0, time.UTC),
	)}
	stdout := &bytes.Buffer{}
	rootCmd := cli.NewRootCLI(
		cli.WithStoreManagement(&storeManagementUsecaseStub{}),
		cli.WithSession(sessionStub),
	).Command()
	rootCmd.SetOut(stdout)
	rootCmd.SetErr(&bytes.Buffer{})
	rootCmd.SetArgs([]string{
		"session", "start", "--json",
		"--db-path", "/tmp/test-traceary.db",
		"--client", "grok",
		"--agent", "grok",
		"--workspace", "duck8823/traceary",
	})
	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if got := sessionStub.startCall.client; got != "grok" {
		t.Fatalf("Start client = %q, want grok", got)
	}
	var output struct {
		Client string `json:"client"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &output); err != nil {
		t.Fatalf("json.Unmarshal() error = %v\n%s", err, stdout.Bytes())
	}
	if output.Client != "grok" {
		t.Fatalf("JSON client = %q, want grok", output.Client)
	}
}

func TestRootCLI_SessionStartCommand_AcceptsAndSerializesKimiClient(t *testing.T) {
	t.Parallel()

	sessionStub := &sessionUsecaseStub{startEvent: model.EventOf(
		types.EventID("event-kimi"),
		types.EventKindSessionStarted,
		types.Client("kimi"),
		types.Agent("kimi"),
		types.SessionID("session-kimi"),
		types.Workspace("duck8823/traceary"),
		"session started",
		time.Date(2026, 7, 19, 12, 0, 0, 0, time.UTC),
	)}
	stdout := &bytes.Buffer{}
	rootCmd := cli.NewRootCLI(
		cli.WithStoreManagement(&storeManagementUsecaseStub{}),
		cli.WithSession(sessionStub),
	).Command()
	rootCmd.SetOut(stdout)
	rootCmd.SetErr(&bytes.Buffer{})
	rootCmd.SetArgs([]string{
		"session", "start", "--json",
		"--db-path", "/tmp/test-traceary.db",
		"--client", "kimi",
		"--agent", "kimi",
		"--workspace", "duck8823/traceary",
	})
	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if got := sessionStub.startCall.client; got != "kimi" {
		t.Fatalf("Start client = %q, want kimi", got)
	}
	var output struct {
		Client string `json:"client"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &output); err != nil {
		t.Fatalf("json.Unmarshal() error = %v\n%s", err, stdout.Bytes())
	}
	if output.Client != "kimi" {
		t.Fatalf("JSON client = %q, want kimi", output.Client)
	}
}

func TestRootCLI_SessionStartCommand_NoParentStaysParentless(t *testing.T) {
	t.Parallel()

	sessionStub := &sessionUsecaseStub{
		startEvent: model.EventOf(
			types.EventID("event-parentless"),
			types.EventKindSessionStarted,
			types.Client("cli"),
			types.Agent("claude/planner"),
			types.SessionID("session-parentless"),
			types.Workspace("duck8823/traceary"),
			"session started",
			time.Now(),
		),
	}
	rootCmd := cli.NewRootCLI(
		cli.WithStoreManagement(&storeManagementUsecaseStub{}),
		cli.WithSession(sessionStub),
	).Command()
	rootCmd.SetOut(&bytes.Buffer{})
	rootCmd.SetErr(&bytes.Buffer{})
	rootCmd.SetArgs([]string{"session", "start", "--client", "cli", "--agent", "claude/planner", "--workspace", "duck8823/traceary"})

	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if got := sessionStub.startCall.parentSessionID; got != "" {
		t.Fatalf("parentSessionID = %q, want empty for explicit CLI start", got)
	}
}

func TestRootCLI_SessionStartCommand_IdOnly(t *testing.T) {
	t.Parallel()

	eventID, err := types.EventIDFrom("event-start-id-only")
	if err != nil {
		t.Fatalf("EventIDFrom() error = %v", err)
	}
	agent, err := types.AgentFrom("codex")
	if err != nil {
		t.Fatalf("AgentFrom() error = %v", err)
	}
	sessionID, err := types.SessionIDFrom("session-start-id-only")
	if err != nil {
		t.Fatalf("SessionIDFrom() error = %v", err)
	}

	stdout := &bytes.Buffer{}
	rootCmd := cli.NewRootCLI(
		cli.WithStoreManagement(&storeManagementUsecaseStub{}),
		cli.WithSession(&sessionUsecaseStub{
			startEvent: model.EventOf(
				eventID,
				types.EventKindSessionStarted,
				"cli",
				agent,
				sessionID,
				"duck8823/traceary",
				"session started",
				time.Date(2026, 4, 7, 13, 0, 0, 0, time.UTC),
			),
		}),
	).Command()
	rootCmd.SetOut(stdout)
	rootCmd.SetErr(&bytes.Buffer{})
	rootCmd.SetArgs([]string{"session", "start", "--db-path", "/tmp/test-traceary.db", "--id-only"})

	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if diff := cmp.Diff("session-start-id-only\n", stdout.String()); diff != "" {
		t.Fatalf("stdout mismatch (-want +got):\n%s", diff)
	}
}

func TestRootCLI_SessionStartCommand_JSON(t *testing.T) {
	t.Parallel()

	eventID := mustEventID(t, "event-start-json")
	agent := mustAgent(t, "codex")
	sessionID := mustSessionID(t, "session-start-json")

	stdout := &bytes.Buffer{}
	rootCmd := cli.NewRootCLI(
		cli.WithStoreManagement(&storeManagementUsecaseStub{}),
		cli.WithSession(&sessionUsecaseStub{
			startEvent: model.EventOf(
				eventID,
				types.EventKindSessionStarted,
				"cli",
				agent,
				sessionID,
				"duck8823/traceary",
				"session started",
				time.Date(2026, 4, 7, 13, 0, 0, 0, time.UTC),
			),
		}),
	).Command()
	rootCmd.SetOut(stdout)
	rootCmd.SetErr(&bytes.Buffer{})
	rootCmd.SetArgs([]string{"session", "start", "--db-path", "/tmp/test-traceary.db", "--json"})

	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}

	payload := decodeJSONMap(t, stdout.String())
	if diff := cmp.Diff("event-start-json", payload["event_id"]); diff != "" {
		t.Fatalf("event_id mismatch (-want +got):\n%s", diff)
	}
	if diff := cmp.Diff("session-start-json", payload["session_id"]); diff != "" {
		t.Fatalf("session_id mismatch (-want +got):\n%s", diff)
	}
}

func TestRootCLI_SessionStartCommand_UsesDetectedRepoByDefault(t *testing.T) {
	t.Setenv("TRACEARY_WORKSPACE", "")
	cli.SetDetectRepoContextFunc(func(context.Context) (string, error) {
		return "github.com/duck8823/traceary", nil
	})
	defer cli.ResetDetectRepoContextFunc()

	eventID, err := types.EventIDFrom("event-1b")
	if err != nil {
		t.Fatalf("EventIDFrom() error = %v", err)
	}
	agent, err := types.AgentFrom("codex")
	if err != nil {
		t.Fatalf("AgentFrom() error = %v", err)
	}
	sessionID, err := types.SessionIDFrom("session-auto-repo")
	if err != nil {
		t.Fatalf("SessionIDFrom() error = %v", err)
	}

	dbPath := filepath.Join(t.TempDir(), "traceary.db")
	rootCmd := cli.NewRootCLI(
		cli.WithStoreManagement(&storeManagementUsecaseStub{}),
		cli.WithSession(&sessionUsecaseStub{
			startEvent: model.EventOf(
				eventID,
				types.EventKindSessionStarted,
				"cli",
				agent,
				sessionID,
				"github.com/duck8823/traceary",
				"session started",
				time.Date(2026, 4, 7, 13, 5, 0, 0, time.UTC),
			),
		}),
	).Command()
	rootCmd.SetOut(&bytes.Buffer{})
	rootCmd.SetErr(&bytes.Buffer{})
	rootCmd.SetArgs([]string{"session", "start", "--db-path", dbPath})

	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
}

func TestRootCLI_SessionEndCommand(t *testing.T) {
	t.Setenv("TRACEARY_SESSION_ID", "session-env")
	t.Setenv("TRACEARY_WORKSPACE", "")
	cli.SetDetectRepoContextFunc(func(context.Context) (string, error) {
		return "github.com/duck8823/traceary", nil
	})
	defer cli.ResetDetectRepoContextFunc()

	eventID, err := types.EventIDFrom("event-2")
	if err != nil {
		t.Fatalf("EventIDFrom() error = %v", err)
	}
	agent, err := types.AgentFrom("codex")
	if err != nil {
		t.Fatalf("AgentFrom() error = %v", err)
	}
	sessionID, err := types.SessionIDFrom("session-env")
	if err != nil {
		t.Fatalf("SessionIDFrom() error = %v", err)
	}

	dbPath := filepath.Join(t.TempDir(), "traceary.db")
	stdout := &bytes.Buffer{}
	rootCmd := cli.NewRootCLI(
		cli.WithStoreManagement(&storeManagementUsecaseStub{}),
		cli.WithSession(&sessionUsecaseStub{
			endEvent: model.EventOf(
				eventID,
				types.EventKindSessionEnded,
				"cli",
				agent,
				sessionID,
				"",
				"session ended",
				time.Date(2026, 4, 7, 13, 30, 0, 0, time.UTC),
			),
		}),
	).Command()
	rootCmd.SetOut(stdout)
	rootCmd.SetErr(&bytes.Buffer{})
	rootCmd.SetArgs([]string{"session", "end", "--db-path", dbPath})

	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if diff := cmp.Diff("Recorded: event-2\n", stdout.String()); diff != "" {
		t.Fatalf("stdout mismatch (-want +got):\n%s", diff)
	}
}

func TestRootCLI_SessionEndCommand_DefersOmittedAttributionToUsecaseInheritance(t *testing.T) {
	t.Setenv("TRACEARY_SESSION_ID", "session-inherit")
	t.Setenv("TRACEARY_CLIENT", "")
	t.Setenv("TRACEARY_AGENT", "")
	t.Setenv("TRACEARY_WORKSPACE", "")
	cli.SetDetectRepoContextFunc(func(context.Context) (string, error) {
		return "github.com/duck8823/traceary", nil
	})
	defer cli.ResetDetectRepoContextFunc()

	sessionID := mustSessionID(t, "session-inherit")
	sessionStub := &sessionUsecaseStub{
		endEvent: model.EventOf(
			mustEventID(t, "event-end-inherit"),
			types.EventKindSessionEnded,
			types.Client("claude"),
			mustAgent(t, "qa-reviewer"),
			sessionID,
			types.Workspace("traceary"),
			"session ended",
			time.Date(2026, 4, 7, 13, 30, 0, 0, time.UTC),
		),
	}
	rootCmd := cli.NewRootCLI(
		cli.WithStoreManagement(&storeManagementUsecaseStub{}),
		cli.WithSession(sessionStub),
	).Command()
	rootCmd.SetOut(&bytes.Buffer{})
	rootCmd.SetErr(&bytes.Buffer{})
	rootCmd.SetArgs([]string{"session", "end", "--db-path", filepath.Join(t.TempDir(), "traceary.db")})

	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if got := sessionStub.endCall.client; got != "" {
		t.Fatalf("end client = %q, want empty so usecase inherits from session start", got)
	}
	if got := sessionStub.endCall.agent; got != "" {
		t.Fatalf("end agent = %q, want empty so usecase inherits from session start", got)
	}
	if got := sessionStub.endCall.workspace; got != "" {
		t.Fatalf("end workspace = %q, want empty so usecase inherits from session start", got)
	}
	if got := sessionStub.endCall.sessionID; got != sessionID {
		t.Fatalf("end sessionID = %q, want %q", got, sessionID)
	}
}

func TestRootCLI_SessionEndCommand_PreservesExplicitAttributionOverrides(t *testing.T) {
	t.Setenv("TRACEARY_SESSION_ID", "session-override")
	t.Setenv("TRACEARY_CLIENT", "env-client")
	t.Setenv("TRACEARY_AGENT", "env-agent")
	t.Setenv("TRACEARY_WORKSPACE", "env-workspace")

	sessionID := mustSessionID(t, "session-override")
	sessionStub := &sessionUsecaseStub{
		endEvent: model.EventOf(
			mustEventID(t, "event-end-override"),
			types.EventKindSessionEnded,
			types.Client("flag-client"),
			mustAgent(t, "env-agent"),
			sessionID,
			types.Workspace("flag-workspace"),
			"session ended",
			time.Date(2026, 4, 7, 13, 30, 0, 0, time.UTC),
		),
	}
	rootCmd := cli.NewRootCLI(
		cli.WithStoreManagement(&storeManagementUsecaseStub{}),
		cli.WithSession(sessionStub),
	).Command()
	rootCmd.SetOut(&bytes.Buffer{})
	rootCmd.SetErr(&bytes.Buffer{})
	rootCmd.SetArgs([]string{
		"session", "end",
		"--db-path", filepath.Join(t.TempDir(), "traceary.db"),
		"--client", "flag-client",
		"--agent", "flag-agent",
		"--workspace", "flag-workspace",
	})

	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if got, want := sessionStub.endCall.client, types.Client("flag-client"); got != want {
		t.Fatalf("end client = %q, want %q", got, want)
	}
	if got, want := sessionStub.endCall.agent, types.Agent("flag-agent"); got != want {
		t.Fatalf("end agent = %q, want %q", got, want)
	}
	if got, want := sessionStub.endCall.workspace, types.Workspace("flag-workspace"); got != want {
		t.Fatalf("end workspace = %q, want %q", got, want)
	}
	if got := sessionStub.endCall.sessionID; got != sessionID {
		t.Fatalf("end sessionID = %q, want %q", got, sessionID)
	}
}

func TestRootCLI_SessionEndCommand_AutoExtractUsesInheritedEventWorkspace(t *testing.T) {
	t.Setenv("TRACEARY_SESSION_ID", "session-extract")
	t.Setenv("TRACEARY_CLIENT", "")
	t.Setenv("TRACEARY_AGENT", "")
	t.Setenv("TRACEARY_WORKSPACE", "")
	cli.SetDetectRepoContextFunc(func(context.Context) (string, error) {
		return "detected-workspace-should-not-be-used-for-session-end", nil
	})
	defer cli.ResetDetectRepoContextFunc()

	sessionID := mustSessionID(t, "session-extract")
	memoryStub := &memoryUsecaseStub{}
	rootCmd := cli.NewRootCLI(
		cli.WithStoreManagement(&storeManagementUsecaseStub{}),
		cli.WithSession(&sessionUsecaseStub{
			endEvent: model.EventOf(
				mustEventID(t, "event-end-extract"),
				types.EventKindSessionEnded,
				types.Client("claude"),
				mustAgent(t, "qa-reviewer"),
				sessionID,
				types.Workspace("traceary"),
				"session ended",
				time.Date(2026, 4, 7, 13, 30, 0, 0, time.UTC),
			),
		}),
		cli.WithMemory(memoryStub),
	).Command()
	rootCmd.SetOut(&bytes.Buffer{})
	rootCmd.SetErr(&bytes.Buffer{})
	rootCmd.SetArgs([]string{
		"session", "end",
		"--db-path", filepath.Join(t.TempDir(), "traceary.db"),
		"--session-id", "session-extract",
	})

	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if got, want := memoryStub.extractCriteria.SessionID(), sessionID; got != want {
		t.Fatalf("extract sessionID = %q, want %q", got, want)
	}
	if got, want := memoryStub.extractCriteria.Workspace(), types.Workspace("traceary"); got != want {
		t.Fatalf("extract workspace = %q, want inherited event workspace %q", got, want)
	}
}

func TestRootCLI_SessionEndCommand_IdOnly(t *testing.T) {
	t.Setenv("TRACEARY_SESSION_ID", "session-env")

	eventID, err := types.EventIDFrom("event-end-id-only")
	if err != nil {
		t.Fatalf("EventIDFrom() error = %v", err)
	}
	agent, err := types.AgentFrom("codex")
	if err != nil {
		t.Fatalf("AgentFrom() error = %v", err)
	}
	sessionID, err := types.SessionIDFrom("session-env")
	if err != nil {
		t.Fatalf("SessionIDFrom() error = %v", err)
	}

	stdout := &bytes.Buffer{}
	rootCmd := cli.NewRootCLI(
		cli.WithStoreManagement(&storeManagementUsecaseStub{}),
		cli.WithSession(&sessionUsecaseStub{
			endEvent: model.EventOf(
				eventID,
				types.EventKindSessionEnded,
				"cli",
				agent,
				sessionID,
				"",
				"session ended",
				time.Date(2026, 4, 7, 13, 30, 0, 0, time.UTC),
			),
		}),
	).Command()
	rootCmd.SetOut(stdout)
	rootCmd.SetErr(&bytes.Buffer{})
	rootCmd.SetArgs([]string{"session", "end", "--db-path", "/tmp/test-traceary.db", "--id-only", "--session-id", "session-env"})

	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if diff := cmp.Diff("event-end-id-only\n", stdout.String()); diff != "" {
		t.Fatalf("stdout mismatch (-want +got):\n%s", diff)
	}
}

func TestRootCLI_SessionEndCommand_JSON(t *testing.T) {
	t.Parallel()

	eventID := mustEventID(t, "event-end-json")
	agent := mustAgent(t, "codex")
	sessionID := mustSessionID(t, "session-end-json")

	stdout := &bytes.Buffer{}
	rootCmd := cli.NewRootCLI(
		cli.WithStoreManagement(&storeManagementUsecaseStub{}),
		cli.WithSession(&sessionUsecaseStub{
			endEvent: model.EventOf(
				eventID,
				types.EventKindSessionEnded,
				"cli",
				agent,
				sessionID,
				"duck8823/traceary",
				"session ended",
				time.Date(2026, 4, 7, 13, 30, 0, 0, time.UTC),
			),
		}),
	).Command()
	rootCmd.SetOut(stdout)
	rootCmd.SetErr(&bytes.Buffer{})
	rootCmd.SetArgs([]string{"session", "end", "--db-path", "/tmp/test-traceary.db", "--session-id", "session-end-json", "--json"})

	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}

	payload := decodeJSONMap(t, stdout.String())
	if diff := cmp.Diff("event-end-json", payload["event_id"]); diff != "" {
		t.Fatalf("event_id mismatch (-want +got):\n%s", diff)
	}
	if diff := cmp.Diff("session_ended", payload["kind"]); diff != "" {
		t.Fatalf("kind mismatch (-want +got):\n%s", diff)
	}
}

func TestRootCLI_SessionLatestCommand(t *testing.T) {
	t.Parallel()

	eventID, err := types.EventIDFrom("event-3")
	if err != nil {
		t.Fatalf("EventIDFrom() error = %v", err)
	}
	agent, err := types.AgentFrom("codex")
	if err != nil {
		t.Fatalf("AgentFrom() error = %v", err)
	}
	sessionID, err := types.SessionIDFrom("session-latest")
	if err != nil {
		t.Fatalf("SessionIDFrom() error = %v", err)
	}

	stdout := &bytes.Buffer{}
	rootCmd := cli.NewRootCLI(
		cli.WithStoreManagement(&storeManagementUsecaseStub{}),
		cli.WithSession(&sessionUsecaseStub{
			latestEvent: model.EventOf(
				eventID,
				types.EventKindSessionStarted,
				"cli",
				agent,
				sessionID,
				"duck8823/traceary",
				"session started",
				time.Date(2026, 4, 8, 13, 0, 0, 0, time.UTC),
			),
		}),
	).Command()
	rootCmd.SetOut(stdout)
	rootCmd.SetErr(&bytes.Buffer{})
	rootCmd.SetArgs([]string{
		"session",
		"latest",
		"--db-path",
		"/tmp/test-traceary.db",
		"--client", "cli",
		"--agent", "codex",
		"--workspace", "duck8823/traceary",
	})

	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if diff := cmp.Diff("session-latest\n", stdout.String()); diff != "" {
		t.Fatalf("stdout mismatch (-want +got):\n%s", diff)
	}
}

func TestRootCLI_SessionLatestCommand_JSON(t *testing.T) {
	t.Parallel()

	stdout := &bytes.Buffer{}
	rootCmd := cli.NewRootCLI(
		cli.WithStoreManagement(&storeManagementUsecaseStub{}),
		cli.WithSession(&sessionUsecaseStub{
			latestEvent: model.EventOf(
				mustEventID(t, "event-latest-json"),
				types.EventKindSessionStarted,
				"cli",
				mustAgent(t, "codex"),
				mustSessionID(t, "session-latest-json"),
				"duck8823/traceary",
				"session started",
				time.Date(2026, 4, 8, 13, 0, 0, 0, time.UTC),
			),
		}),
	).Command()
	rootCmd.SetOut(stdout)
	rootCmd.SetErr(&bytes.Buffer{})
	rootCmd.SetArgs([]string{"session", "latest", "--db-path", "/tmp/test-traceary.db", "--json"})

	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}

	payload := decodeJSONMap(t, stdout.String())
	if diff := cmp.Diff("event-latest-json", payload["event_id"]); diff != "" {
		t.Fatalf("event_id mismatch (-want +got):\n%s", diff)
	}
	if diff := cmp.Diff("session-latest-json", payload["session_id"]); diff != "" {
		t.Fatalf("session_id mismatch (-want +got):\n%s", diff)
	}
}

func TestRootCLI_SessionActiveCommand(t *testing.T) {
	t.Parallel()

	eventID, err := types.EventIDFrom("event-4")
	if err != nil {
		t.Fatalf("EventIDFrom() error = %v", err)
	}
	agent, err := types.AgentFrom("codex")
	if err != nil {
		t.Fatalf("AgentFrom() error = %v", err)
	}
	sessionID, err := types.SessionIDFrom("session-active")
	if err != nil {
		t.Fatalf("SessionIDFrom() error = %v", err)
	}

	stdout := &bytes.Buffer{}
	rootCmd := cli.NewRootCLI(
		cli.WithStoreManagement(&storeManagementUsecaseStub{}),
		cli.WithSession(&sessionUsecaseStub{
			activeEvent: model.EventOf(
				eventID,
				types.EventKindSessionStarted,
				"cli",
				agent,
				sessionID,
				"duck8823/traceary",
				"session started",
				time.Now().Add(-1*time.Hour),
			),
		}),
	).Command()
	rootCmd.SetOut(stdout)
	rootCmd.SetErr(&bytes.Buffer{})
	rootCmd.SetArgs([]string{
		"session",
		"active",
		"--db-path",
		"/tmp/test-traceary.db",
		"--agent", "codex",
	})

	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if diff := cmp.Diff("session-active\n", stdout.String()); diff != "" {
		t.Fatalf("stdout mismatch (-want +got):\n%s", diff)
	}
}

func TestRootCLI_SessionActiveCommand_StaleError(t *testing.T) {
	t.Parallel()

	eventID, err := types.EventIDFrom("event-5")
	if err != nil {
		t.Fatalf("EventIDFrom() error = %v", err)
	}
	agent, err := types.AgentFrom("codex")
	if err != nil {
		t.Fatalf("AgentFrom() error = %v", err)
	}
	sessionID, err := types.SessionIDFrom("session-stale")
	if err != nil {
		t.Fatalf("SessionIDFrom() error = %v", err)
	}

	rootCmd := cli.NewRootCLI(
		cli.WithStoreManagement(&storeManagementUsecaseStub{}),
		cli.WithSession(&sessionUsecaseStub{
			activeEvent: model.EventOf(
				eventID,
				types.EventKindSessionStarted,
				"cli",
				agent,
				sessionID,
				"duck8823/traceary",
				"session started",
				time.Now().Add(-48*time.Hour),
			),
		}),
	).Command()
	rootCmd.SetOut(&bytes.Buffer{})
	rootCmd.SetErr(&bytes.Buffer{})
	rootCmd.SetArgs([]string{
		"session",
		"active",
		"--db-path",
		"/tmp/test-traceary.db",
	})

	err = rootCmd.Execute()
	if err == nil {
		t.Fatalf("Execute() error = nil, want error")
	}
	if !strings.Contains(err.Error(), "stale") {
		t.Fatalf("error = %q, want stale error", err.Error())
	}
}

func TestRootCLI_SessionActiveCommand_AllowStale(t *testing.T) {
	t.Parallel()

	eventID, err := types.EventIDFrom("event-6")
	if err != nil {
		t.Fatalf("EventIDFrom() error = %v", err)
	}
	agent, err := types.AgentFrom("codex")
	if err != nil {
		t.Fatalf("AgentFrom() error = %v", err)
	}
	sessionID, err := types.SessionIDFrom("session-stale")
	if err != nil {
		t.Fatalf("SessionIDFrom() error = %v", err)
	}

	stdout := &bytes.Buffer{}
	rootCmd := cli.NewRootCLI(
		cli.WithStoreManagement(&storeManagementUsecaseStub{}),
		cli.WithSession(&sessionUsecaseStub{
			activeEvent: model.EventOf(
				eventID,
				types.EventKindSessionStarted,
				"cli",
				agent,
				sessionID,
				"duck8823/traceary",
				"session started",
				time.Now().Add(-48*time.Hour),
			),
		}),
	).Command()
	rootCmd.SetOut(stdout)
	rootCmd.SetErr(&bytes.Buffer{})
	rootCmd.SetArgs([]string{
		"session",
		"active",
		"--db-path",
		"/tmp/test-traceary.db",
		"--allow-stale",
	})

	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if diff := cmp.Diff("session-stale\n", stdout.String()); diff != "" {
		t.Fatalf("stdout mismatch (-want +got):\n%s", diff)
	}
}

func TestRootCLI_SessionLatestCommand_NotFoundError(t *testing.T) {
	t.Parallel()

	dbPath := filepath.Join(t.TempDir(), "traceary.db")
	rootCmd := cli.NewRootCLI(
		cli.WithStoreManagement(&storeManagementUsecaseStub{}),
		cli.WithSession(&sessionUsecaseStub{}),
	).Command()
	rootCmd.SetOut(&bytes.Buffer{})
	rootCmd.SetErr(&bytes.Buffer{})
	rootCmd.SetArgs([]string{"session", "latest", "--db-path", dbPath})

	err := rootCmd.Execute()
	if err == nil {
		t.Fatal("Execute() error = nil, want error")
	}
	if diff := cmp.Diff("no matching session found", err.Error()); diff != "" {
		t.Fatalf("error mismatch (-want +got):\n%s", diff)
	}
}

func decodeJSONMap(t *testing.T, value string) map[string]any {
	t.Helper()

	var payload map[string]any
	if err := json.Unmarshal([]byte(value), &payload); err != nil {
		t.Fatalf("json.Unmarshal() error = %v", err)
	}

	return payload
}

func mustAgent(t *testing.T, value string) types.Agent {
	t.Helper()

	agent, err := types.AgentFrom(value)
	if err != nil {
		t.Fatalf("AgentFrom() error = %v", err)
	}

	return agent
}
