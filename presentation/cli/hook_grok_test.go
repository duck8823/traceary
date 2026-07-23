package cli_test

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	apptypes "github.com/duck8823/traceary/application/types"
	"github.com/duck8823/traceary/domain/model"
	"github.com/duck8823/traceary/domain/types"
	cli "github.com/duck8823/traceary/presentation/cli"
)

func TestRootCLI_HookGrokCoreEvents(t *testing.T) {
	t.Setenv("TRACEARY_HOOK_STATE_DIR", t.TempDir())
	t.Setenv("TRACEARY_HOOK_STATE_KEY", "grok-core-events")
	t.Setenv("TRACEARY_WORKSPACE", "github.com/duck8823/traceary")

	homeDir := t.TempDir()
	cli.SetUserHomeDirFunc(func() (string, error) { return homeDir, nil })
	t.Cleanup(cli.ResetUserHomeDirFunc)

	t.Run("records session start with Grok identity", func(t *testing.T) {
		payload := readGrokFixture(t, "session_start.json")
		sessionStub := &sessionUsecaseStub{startEvent: model.EventOf(
			types.EventID("event-grok-start"),
			types.EventKindSessionStarted,
			types.Client("hook"),
			types.Agent("grok"),
			types.SessionID("019f0000-0000-7000-8000-000000000001"),
			types.Workspace("github.com/duck8823/traceary"),
			"session started",
			time.Now(),
		)}

		stdout, _, gotSession := runGrokHook(t, "session-start", payload, nil, sessionStub)

		if stdout != "" {
			t.Fatalf("SessionStart output = %q, want empty passive-hook output", stdout)
		}
		if got, want := gotSession.startCall.sessionID, types.SessionID("019f0000-0000-7000-8000-000000000001"); got != want {
			t.Fatalf("session ID = %q, want %q", got, want)
		}
		if got, want := gotSession.startCall.client, types.Client("hook"); got != want {
			t.Fatalf("session client = %q, want %q", got, want)
		}
		if got, want := gotSession.startCall.agent, types.Agent("grok"); got != want {
			t.Fatalf("session agent = %q, want %q", got, want)
		}
	})

	t.Run("records prompt with Grok identity", func(t *testing.T) {
		stdout, eventStub, _ := runGrokHook(t, "user-prompt-submit", readGrokFixture(t, "user_prompt_submit.json"), nil, nil)

		if stdout != "" {
			t.Fatalf("UserPromptSubmit output = %q, want empty passive-hook output", stdout)
		}
		if got, want := eventStub.logCall.kind, types.EventKindPrompt; got != want {
			t.Fatalf("prompt kind = %q, want %q", got, want)
		}
		if got, want := eventStub.logCall.message, "contract probe"; got != want {
			t.Fatalf("prompt body = %q, want %q", got, want)
		}
		if got, want := eventStub.logCall.agent, types.Agent("grok"); got != want {
			t.Fatalf("prompt agent = %q, want %q", got, want)
		}
		if got, want := eventStub.logCall.sourceHook, "user_prompt_submit"; got != want {
			t.Fatalf("prompt source hook = %q, want %q", got, want)
		}
	})

	t.Run("keeps PreToolUse fail open without recording a duplicate audit", func(t *testing.T) {
		stdout, eventStub, _ := runGrokHook(t, "pre-tool-use", readGrokFixture(t, "pre_tool_use.json"), nil, nil)

		if stdout != "" {
			t.Fatalf("PreToolUse output = %q, want empty allow-by-exit-zero output", stdout)
		}
		if eventStub.auditCall.command != "" {
			t.Fatalf("PreToolUse audit command = %q, want no completed audit", eventStub.auditCall.command)
		}
	})

	t.Run("records successful PostToolUse once", func(t *testing.T) {
		stdout, eventStub, _ := runGrokHook(t, "post-tool-use", readGrokFixture(t, "post_tool_use.json"), nil, nil)

		if stdout != "" {
			t.Fatalf("PostToolUse output = %q, want empty passive-hook output", stdout)
		}
		if got, want := eventStub.auditCall.command, "read_file"; got != want {
			t.Fatalf("audit command = %q, want %q", got, want)
		}
		if got, want := eventStub.auditCall.input, `{"target_file":"VERSION"}`; got != want {
			t.Fatalf("audit input = %q, want %q", got, want)
		}
		if !strings.Contains(eventStub.auditCall.output, `"type":"ReadFile"`) {
			t.Fatalf("audit output = %q, want ReadFile result", eventStub.auditCall.output)
		}
		if eventStub.auditCall.failed {
			t.Fatal("successful ReadFile audit was marked failed")
		}
		if got, want := eventStub.auditCall.agent, types.Agent("grok"); got != want {
			t.Fatalf("audit agent = %q, want %q", got, want)
		}
	})

	for _, fixture := range []string{"post_tool_use_missing.json", "post_tool_use_denied.json"} {
		fixture := fixture
		t.Run("records failed result variant "+fixture, func(t *testing.T) {
			_, eventStub, _ := runGrokHook(t, "post-tool-use", readGrokFixture(t, fixture), nil, nil)

			if !eventStub.auditCall.failed {
				t.Fatalf("%s audit was not marked failed", fixture)
			}
			if eventStub.auditCall.output == "" {
				t.Fatalf("%s audit output is empty", fixture)
			}
			wantReason := types.CommandFailureReasonHostError
			if fixture == "post_tool_use_denied.json" {
				wantReason = types.CommandFailureReasonHookDenied
			}
			if eventStub.auditCall.failureReason != wantReason {
				t.Fatalf("%s failure reason = %q, want %q", fixture, eventStub.auditCall.failureReason, wantReason)
			}
		})
	}
}

func TestRootCLI_HookGrokStopRecordsBestEffortTranscript(t *testing.T) {
	t.Setenv("TRACEARY_HOOK_STATE_DIR", t.TempDir())
	t.Setenv("TRACEARY_HOOK_STATE_KEY", "grok-stop")
	t.Setenv("TRACEARY_WORKSPACE", "github.com/duck8823/traceary")

	transcriptPath := filepath.Join(t.TempDir(), "updates.jsonl")
	transcript := strings.Join([]string{
		`{"method":"session/update","params":{"update":{"sessionUpdate":"user_message_chunk","content":{"type":"text","text":"old prompt"}},"_meta":{"promptId":"prompt-old"}}}`,
		`{"method":"session/update","params":{"update":{"sessionUpdate":"agent_message_chunk","content":{"type":"text","text":"old answer"}},"_meta":{"promptId":"prompt-old"}}}`,
		`not-json`,
		`{"method":"session/update","params":{"update":{"sessionUpdate":"user_message_chunk","content":{"type":"text","text":"current prompt"}},"_meta":{"promptId":"prompt-contract-probe-1"}}}`,
		`{"method":"session/update","params":{"update":{"sessionUpdate":"agent_thought_chunk","content":{"type":"text","text":"must stay excluded"}},"_meta":{"promptId":"prompt-contract-probe-1"}}}`,
		`{"method":"session/update","params":{"update":{"sessionUpdate":"agent_message_chunk","content":{"type":"text","text":"final "}},"_meta":{"promptId":"prompt-contract-probe-1"}}}`,
		`{"method":"session/update","params":{"update":{"sessionUpdate":"agent_message_chunk","content":{"type":"text","text":"answer"}},"_meta":{"promptId":"prompt-contract-probe-1"}}}`,
	}, "\n") + "\n"
	if err := os.WriteFile(transcriptPath, []byte(transcript), 0o600); err != nil {
		t.Fatalf("write synthetic Grok transcript: %v", err)
	}
	payload := grokFixtureWithField(t, "stop.json", "transcriptPath", transcriptPath)

	stdout, eventStub, sessionStub := runGrokHook(t, "stop", payload, nil, nil)

	if stdout != "" {
		t.Fatalf("Stop output = %q, want empty passive-hook output", stdout)
	}
	if got, want := eventStub.logCall.kind, types.EventKindTranscript; got != want {
		t.Fatalf("transcript kind = %q, want %q", got, want)
	}
	if got, want := apptypes.ExtractPlainBody(eventStub.logCall.message), "final answer"; got != want {
		t.Fatalf("transcript body = %q, want %q", got, want)
	}
	if got, want := eventStub.logCall.agent, types.Agent("grok"); got != want {
		t.Fatalf("transcript agent = %q, want %q", got, want)
	}
	if len(sessionStub.endCalls) != 0 {
		t.Fatalf("Stop ended %d sessions, want turn boundary only", len(sessionStub.endCalls))
	}
}

func TestRootCLI_HookGrokStopRecordsStableUsageUnavailability(t *testing.T) {
	t.Setenv("TRACEARY_HOOK_STATE_DIR", t.TempDir())
	t.Setenv("TRACEARY_HOOK_STATE_KEY", "grok-stop-usage")
	t.Setenv("TRACEARY_WORKSPACE", "github.com/duck8823/traceary")
	usage := &grokUsageCaptureStub{}

	runGrokHook(
		t, "stop", grokFixtureWithoutField(t, "stop.json", "transcriptPath"),
		nil, nil, cli.WithGrokUsage(usage),
	)

	if len(usage.hooks) != 1 ||
		usage.hooks[0].SessionID != "019f0000-0000-7000-8000-000000000001" ||
		usage.hooks[0].DeliveryID != "prompt_id:prompt-contract-probe-1" {
		t.Fatalf("usage hooks = %+v", usage.hooks)
	}
}

func TestRootCLI_HookGrokStopUsesOnlyVerifiedPromptIdentityForUsage(t *testing.T) {
	t.Setenv("TRACEARY_HOOK_STATE_DIR", t.TempDir())
	t.Setenv("TRACEARY_HOOK_STATE_KEY", "grok-stop-usage-identity")
	t.Setenv("TRACEARY_WORKSPACE", "github.com/duck8823/traceary")

	for _, test := range []struct {
		name         string
		promptID     any
		toolUseID    any
		wantDelivery string
	}{
		{name: "tool identity alone is unverified", promptID: nil, toolUseID: "tool-stop-1"},
		{name: "prompt wins when both are present", promptID: "prompt-stop-1", toolUseID: "tool-stop-1", wantDelivery: "prompt_id:prompt-stop-1"},
		{name: "blank prompt does not fall back to tool", promptID: "   ", toolUseID: "tool-stop-1"},
	} {
		t.Run(test.name, func(t *testing.T) {
			payload := grokFixtureWithoutField(t, "stop.json", "transcriptPath")
			if test.promptID == nil {
				payload = grokJSONWithoutField(t, payload, "promptId")
			} else {
				payload = grokJSONWithField(t, payload, "promptId", test.promptID)
			}
			payload = grokJSONWithField(t, payload, "toolUseId", test.toolUseID)
			usage := &grokUsageCaptureStub{}
			runGrokHook(t, "stop", payload, nil, nil, cli.WithGrokUsage(usage))
			if test.wantDelivery == "" {
				if len(usage.hooks) != 0 {
					t.Fatalf("usage hooks = %+v, want none", usage.hooks)
				}
				return
			}
			if len(usage.hooks) != 1 || usage.hooks[0].DeliveryID != test.wantDelivery {
				t.Fatalf("usage hooks = %+v, want delivery %q", usage.hooks, test.wantDelivery)
			}
		})
	}
}

func TestRootCLI_HookGrokStopSuppressesUnavailableDuringOwnedHeadlessRun(t *testing.T) {
	t.Setenv("TRACEARY_HOOK_STATE_DIR", t.TempDir())
	t.Setenv("TRACEARY_HOOK_STATE_KEY", "grok-stop-owned")
	t.Setenv("TRACEARY_WORKSPACE", "github.com/duck8823/traceary")
	t.Setenv("TRACEARY_GROK_USAGE_MODE", "one_shot_stream")
	t.Setenv("TRACEARY_RUNTIME_MODE", "one_shot")
	t.Setenv("TRACEARY_RUNTIME_SESSION_ID", "one-shot-grok-owned")
	usage := &grokUsageCaptureStub{}

	runGrokHook(
		t, "stop", grokFixtureWithoutField(t, "stop.json", "transcriptPath"),
		nil, nil, cli.WithGrokUsage(usage),
	)

	if len(usage.hooks) != 0 {
		t.Fatalf("usage hooks = %+v, want suppressed", usage.hooks)
	}
}

func TestRootCLI_HookGrokStopDoesNotTrustUsageModeAlone(t *testing.T) {
	t.Setenv("TRACEARY_HOOK_STATE_DIR", t.TempDir())
	t.Setenv("TRACEARY_HOOK_STATE_KEY", "grok-stop-mode-only")
	t.Setenv("TRACEARY_WORKSPACE", "github.com/duck8823/traceary")
	t.Setenv("TRACEARY_GROK_USAGE_MODE", "one_shot_stream")
	usage := &grokUsageCaptureStub{}
	payload := grokJSONWithField(
		t,
		grokFixtureWithoutField(t, "stop.json", "transcriptPath"),
		"promptId",
		"prompt-mode-only",
	)

	runGrokHook(t, "stop", payload, nil, nil, cli.WithGrokUsage(usage))

	if len(usage.hooks) != 1 || usage.hooks[0].DeliveryID != "prompt_id:prompt-mode-only" {
		t.Fatalf("usage hooks = %+v, want native availability capture", usage.hooks)
	}
}

func TestRootCLI_HookGrokCompactRecordsObservedMarkers(t *testing.T) {
	t.Setenv("TRACEARY_HOOK_STATE_DIR", t.TempDir())
	t.Setenv("TRACEARY_HOOK_STATE_KEY", "grok-compact")
	t.Setenv("TRACEARY_WORKSPACE", "github.com/duck8823/traceary")

	for _, tc := range []struct {
		command    string
		fixture    string
		sourceHook string
	}{
		{command: "pre-compact", fixture: "pre_compact.json", sourceHook: "pre_compact"},
		{command: "post-compact", fixture: "post_compact.json", sourceHook: "post_compact"},
	} {
		t.Run(tc.command, func(t *testing.T) {
			stdout, eventStub, _ := runGrokHook(t, tc.command, readGrokFixture(t, tc.fixture), nil, nil)
			if stdout != "" {
				t.Fatalf("%s output = %q, want empty passive-hook output", tc.command, stdout)
			}
			if got, want := eventStub.logCall.kind, types.EventKindCompactSummary; got != want {
				t.Fatalf("compact kind = %q, want %q", got, want)
			}
			if got, want := eventStub.logCall.message, "manual"; got != want {
				t.Fatalf("compact marker = %q, want %q", got, want)
			}
			if got, want := eventStub.logCall.sourceHook, tc.sourceHook; got != want {
				t.Fatalf("compact source hook = %q, want %q", got, want)
			}
			if got, want := eventStub.logCall.agent, types.Agent("grok"); got != want {
				t.Fatalf("compact agent = %q, want %q", got, want)
			}
		})
	}
}

func TestRootCLI_HookGrokCompactDegradesWhenSummaryAndSourceAreMissing(t *testing.T) {
	t.Setenv("TRACEARY_HOOK_STATE_DIR", t.TempDir())
	t.Setenv("TRACEARY_HOOK_STATE_KEY", "grok-compact-missing-source")
	t.Setenv("TRACEARY_WORKSPACE", "github.com/duck8823/traceary")

	for _, tc := range []struct {
		name    string
		command string
		fixture string
		source  any
	}{
		{name: "pre compact missing both fields", command: "pre-compact", fixture: "pre_compact.json", source: nil},
		{name: "post compact missing both fields", command: "post-compact", fixture: "post_compact.json", source: nil},
		{name: "post compact non-string source", command: "post-compact", fixture: "post_compact.json", source: map[string]any{"unexpected": true}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			payload := grokFixtureWithoutField(t, tc.fixture, "hookEventName")
			if tc.source == nil {
				var decoded map[string]any
				if err := json.Unmarshal([]byte(payload), &decoded); err != nil {
					t.Fatalf("decode compact payload: %v", err)
				}
				delete(decoded, "source")
				encoded, err := json.Marshal(decoded)
				if err != nil {
					t.Fatalf("encode compact payload: %v", err)
				}
				payload = string(encoded)
			} else {
				payload = grokJSONWithField(t, payload, "source", tc.source)
			}
			_, eventStub, _ := runGrokHook(t, tc.command, payload, nil, nil)
			if got, want := eventStub.logCall.kind, types.EventKindCompactSummary; got != want {
				t.Fatalf("compact kind = %q, want %q", got, want)
			}
			if got, want := eventStub.logCall.message, "unavailable"; got != want {
				t.Fatalf("missing compact marker = %q, want explicit %q degradation", got, want)
			}
		})
	}
}

func TestRootCLI_HookGrokStopDefersTranscriptUntilHostAppendsFinalMessage(t *testing.T) {
	stateDir := t.TempDir()
	t.Setenv("TRACEARY_HOOK_STATE_DIR", stateDir)
	t.Setenv("TRACEARY_HOOK_STATE_KEY", "grok-deferred-stop")
	t.Setenv("TRACEARY_WORKSPACE", "github.com/duck8823/traceary")

	transcriptPath := filepath.Join(t.TempDir(), "updates.jsonl")
	initial := `{"method":"session/update","params":{"update":{"sessionUpdate":"user_message_chunk","content":{"type":"text","text":"current prompt"}},"_meta":{"promptId":"prompt-contract-probe-1"}}}` + "\n"
	if err := os.WriteFile(transcriptPath, []byte(initial), 0o600); err != nil {
		t.Fatalf("write initial Grok transcript: %v", err)
	}
	payload := grokFixtureWithField(t, "stop.json", "transcriptPath", transcriptPath)
	eventStub := &eventUsecaseStub{}
	sessionStub := &sessionUsecaseStub{}
	jobPath := ""

	_, eventStub, sessionStub = runGrokHook(t, "stop", payload, eventStub, sessionStub,
		cli.WithHookGrokTranscriptLauncher(func(path string) error {
			jobPath = path
			return nil
		}))

	if eventStub.logCall.kind != "" {
		t.Fatalf("Stop recorded transcript before the host appended the final message: %q", eventStub.logCall.kind)
	}
	if jobPath == "" {
		t.Fatal("Stop did not enqueue the deferred Grok transcript job")
	}
	if info, err := os.Stat(jobPath); err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("deferred job stat = %v, %v; want regular 0600 file", info, err)
	}

	finalMessage := `{"method":"session/update","params":{"update":{"sessionUpdate":"agent_message_chunk","content":{"type":"text","text":"deferred final answer"}},"_meta":{"promptId":"prompt-contract-probe-1"}}}` + "\n"
	file, err := os.OpenFile(transcriptPath, os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatalf("open Grok transcript for append: %v", err)
	}
	if _, err := file.WriteString(finalMessage); err != nil {
		_ = file.Close()
		t.Fatalf("append final Grok message: %v", err)
	}
	if err := file.Close(); err != nil {
		t.Fatalf("close Grok transcript: %v", err)
	}

	rootCmd := newTestRootCLI(
		cli.WithStoreManagement(&storeManagementUsecaseStub{}),
		cli.WithEvent(eventStub),
		cli.WithSession(sessionStub),
	).Command()
	rootCmd.SetOut(&bytes.Buffer{})
	rootCmd.SetErr(&bytes.Buffer{})
	rootCmd.SetArgs([]string{"hook", "grok", "transcript-worker", "--job", jobPath})
	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("Execute(Grok transcript worker) error = %v", err)
	}
	if got, want := apptypes.ExtractPlainBody(eventStub.logCall.message), "deferred final answer"; got != want {
		t.Fatalf("deferred transcript body = %q, want %q", got, want)
	}
	if _, err := os.Stat(jobPath); !os.IsNotExist(err) {
		t.Fatalf("completed deferred job still exists: %v", err)
	}
}

func TestRootCLI_HookGrokFailsOpenForMalformedAndMissingPayloads(t *testing.T) {
	stateDir := t.TempDir()
	t.Setenv("TRACEARY_HOOK_STATE_DIR", stateDir)
	t.Setenv("TRACEARY_HOOK_STATE_KEY", "grok-fail-open")

	stdout, eventStub, sessionStub := runGrokHook(t, "post-tool-use", `{`, nil, nil)
	if stdout != "" {
		t.Fatalf("malformed PostToolUse output = %q, want empty", stdout)
	}
	if eventStub.auditCall.command != "" {
		t.Fatalf("malformed PostToolUse recorded audit %q", eventStub.auditCall.command)
	}
	spooled, err := filepath.Glob(filepath.Join(stateDir, "spool", "*.json"))
	if err != nil {
		t.Fatalf("list hook spool: %v", err)
	}
	if len(spooled) != 1 {
		t.Fatalf("malformed payload spool files = %v, want one diagnostic record", spooled)
	}

	failingEventStub := &eventUsecaseStub{auditErr: errors.New("synthetic audit failure")}
	stdout, _, _ = runGrokHook(t, "post-tool-use", readGrokFixture(t, "post_tool_use.json"), failingEventStub, sessionStub)
	if stdout != "" {
		t.Fatalf("failed PostToolUse output = %q, want empty fail-open output", stdout)
	}
	spooled, err = filepath.Glob(filepath.Join(stateDir, "spool", "*.json"))
	if err != nil {
		t.Fatalf("list hook spool after runtime failure: %v", err)
	}
	if len(spooled) != 2 {
		t.Fatalf("runtime failure spool files = %v, want malformed and audit failure diagnostics", spooled)
	}

	stdout, _, sessionStub = runGrokHook(t, "session-start", `{}`, eventStub, sessionStub)
	if stdout != "" {
		t.Fatalf("missing SessionStart output = %q, want empty", stdout)
	}
	if sessionStub.startCall.sessionID != "" {
		t.Fatalf("missing SessionStart recorded session %q", sessionStub.startCall.sessionID)
	}

	stdout, eventStub, _ = runGrokHook(t, "stop", grokFixtureWithoutField(t, "stop.json", "transcriptPath"), &eventUsecaseStub{}, sessionStub)
	if stdout != "" {
		t.Fatalf("Stop without transcriptPath output = %q, want empty", stdout)
	}
	if eventStub.logCall.kind != "" {
		t.Fatalf("Stop without transcriptPath recorded %q, want best-effort no-op", eventStub.logCall.kind)
	}
}

func TestRootCLI_HookGrokTranscriptWorkerRejectsJobOutsideQueue(t *testing.T) {
	t.Setenv("TRACEARY_HOOK_STATE_DIR", t.TempDir())
	outsideJob := filepath.Join(t.TempDir(), strings.Repeat("a", 64)+".json")
	if err := os.WriteFile(outsideJob, []byte(`{"schema_version":1,"payload":"{}"}`), 0o600); err != nil {
		t.Fatalf("write outside Grok transcript job: %v", err)
	}

	rootCmd := newTestRootCLI(
		cli.WithStoreManagement(&storeManagementUsecaseStub{}),
		cli.WithEvent(&eventUsecaseStub{}),
	).Command()
	rootCmd.SetOut(&bytes.Buffer{})
	rootCmd.SetErr(&bytes.Buffer{})
	rootCmd.SetArgs([]string{"hook", "grok", "transcript-worker", "--job", outsideJob})
	err := rootCmd.Execute()
	if err == nil || !strings.Contains(err.Error(), "outside the queue directory") {
		t.Fatalf("Execute(outside Grok transcript job) error = %v, want path-boundary rejection", err)
	}
}

func runGrokHook(
	t *testing.T,
	event string,
	payload string,
	eventStub *eventUsecaseStub,
	sessionStub *sessionUsecaseStub,
	opts ...cli.RootCLIOption,
) (string, *eventUsecaseStub, *sessionUsecaseStub) {
	t.Helper()
	if eventStub == nil {
		eventStub = &eventUsecaseStub{}
	}
	if sessionStub == nil {
		sessionStub = &sessionUsecaseStub{}
	}

	baseOptions := []cli.RootCLIOption{
		cli.WithStoreManagement(&storeManagementUsecaseStub{}),
		cli.WithEvent(eventStub),
		cli.WithSession(sessionStub),
	}
	rootCmd := newTestRootCLI(append(baseOptions, opts...)...).Command()
	stdout := &bytes.Buffer{}
	rootCmd.SetOut(stdout)
	rootCmd.SetErr(&bytes.Buffer{})
	rootCmd.SetIn(strings.NewReader(payload))
	rootCmd.SetArgs([]string{"hook", "grok", event})
	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("Execute(hook grok %s) error = %v", event, err)
	}
	return stdout.String(), eventStub, sessionStub
}

func readGrokFixture(t *testing.T, name string) string {
	t.Helper()
	path := filepath.Join("testdata", "grok_hooks", "v0.2.99", name)
	payload, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read Grok fixture %s: %v", path, err)
	}
	return string(payload)
}

func grokFixtureWithField(t *testing.T, name, field string, value any) string {
	t.Helper()
	return grokJSONWithField(t, readGrokFixture(t, name), field, value)
}

func grokJSONWithField(t *testing.T, input, field string, value any) string {
	t.Helper()
	var payload map[string]any
	if err := json.Unmarshal([]byte(input), &payload); err != nil {
		t.Fatalf("decode Grok JSON: %v", err)
	}
	payload[field] = value
	encoded, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("encode Grok JSON with %s=%v: %v", field, value, err)
	}
	return string(encoded)
}

func grokFixtureWithoutField(t *testing.T, name, field string) string {
	t.Helper()
	return grokJSONWithoutField(t, readGrokFixture(t, name), field)
}

func grokJSONWithoutField(t *testing.T, input, field string) string {
	t.Helper()
	var payload map[string]any
	if err := json.Unmarshal([]byte(input), &payload); err != nil {
		t.Fatalf("decode Grok JSON: %v", err)
	}
	delete(payload, field)
	encoded, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("encode Grok JSON without %s: %v", field, err)
	}
	return string(encoded)
}
