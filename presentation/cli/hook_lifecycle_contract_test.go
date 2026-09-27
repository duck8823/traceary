package cli_test

import (
	"bytes"
	"context"
	"database/sql"
	"github.com/duck8823/traceary/domain/model"
	"github.com/duck8823/traceary/domain/types"
	"github.com/duck8823/traceary/presentation/cli"
	"strings"
	"testing"
)

// Documentation-derived regressions, not evidence of live host dispatch.
func TestHookPassiveLifecycleContract(t *testing.T) {
	for _, tc := range []struct{ client, action string }{{"codex", "interrupt"}, {"codex", "session_end"}, {"claude", "stop_failure"}, {"kimi", "interrupt"}} {
		t.Run(tc.client+tc.action, func(t *testing.T) {
			t.Setenv("TRACEARY_HOOK_STATE_DIR", t.TempDir())
			events := &eventUsecaseStub{}
			sessions := &sessionUsecaseStub{}
			root := newTestRootCLI(cli.WithEvent(events), cli.WithSession(sessions), cli.WithStoreManagement(&storeManagementUsecaseStub{})).Command()
			out := &bytes.Buffer{}
			root.SetOut(out)
			root.SetErr(&bytes.Buffer{})
			root.SetIn(strings.NewReader(`{"session_id":"passive-session","cwd":"/tmp","reason":"RAW_PRIVATE_ERROR","last_assistant_message":"RAW_PRIVATE_ERROR"}`))
			args := []string{"hook", "passive", tc.client, tc.action}
			if tc.client == "kimi" {
				args = []string{"hook", "kimi", "interrupt"}
			}
			root.SetArgs(args)
			if err := root.Execute(); err != nil {
				t.Fatal(err)
			}
			if out.Len() != 0 {
				t.Fatalf("control output = %q", out.String())
			}
			if len(sessions.endCalls) != 0 {
				t.Fatal("passive signal ended session")
			}
			if tc.action != "session_end" && (events.logCall.kind != types.EventKindNote || events.logCall.sourceHook != tc.action) {
				t.Fatalf("note = %+v", events.logCall)
			}
			if strings.Contains(events.logCall.message, "RAW_PRIVATE_ERROR") {
				t.Fatal("raw payload copied")
			}
		})
	}
}

func TestHookAntigravityStopNotFullyIdle(t *testing.T) {
	fx := newEnvelopeFixture(t, "not-idle", "antigravity", 20)
	payload := antigravityStopPayload(t, "not-idle", true, false)
	payload = strings.TrimSuffix(payload, "}") + `,"fullyIdle":false}`
	stdout, code := fx.runAntigravityStop(t, payload)
	if code != 0 || stdout != `{"decision":""}` {
		t.Fatalf("code=%d stdout=%q", code, stdout)
	}
	if countConsolidationRequests(t, fx.dbPath) != 0 {
		t.Fatal("not-idle Stop requested consolidation")
	}
}

func TestHookAntigravityStopIdleTransition(t *testing.T) {
	fx := newEnvelopeFixture(t, "idle-transition", "antigravity", 20)
	payload := antigravityStopPayload(t, "idle-transition", true, false)
	for _, idle := range []string{"false", "false", "true"} {
		stdout, code := fx.runAntigravityStop(t, strings.TrimSuffix(payload, "}")+`,"fullyIdle":`+idle+`}`)
		if code != 0 {
			t.Fatalf("exit=%d", code)
		}
		if idle == "false" && stdout != `{"decision":""}` {
			t.Fatalf("nonidle=%q", stdout)
		}
		if idle == "true" && !strings.Contains(stdout, `"decision":"continue"`) {
			t.Fatalf("final idle=%q", stdout)
		}
	}
}

func TestCodexRuntimeCloseNotesKeepResumableSessionOpen(t *testing.T) {
	for _, native := range []bool{false, true} {
		t.Run(map[bool]string{false: "without native delivery ID", true: "with native delivery ID"}[native], func(t *testing.T) {
			fx := newEnvelopeFixture(t, "resumable-thread", "codex", 0)
			for _, id := range []string{"first", "first", "second"} {
				payload := `{"session_id":"resumable-thread","cwd":"/tmp"`
				if native {
					payload += `,"event_id":"` + id + `"`
				}
				payload += `}`
				root := cli.NewRootCLI(cli.WithStoreManagement(fx.storeUC), cli.WithEvent(fx.eventUC), cli.WithSession(fx.sessionUC), cli.WithDatabasePathSetter(fx.db.SetPath)).Command()
				root.SetOut(&bytes.Buffer{})
				root.SetErr(&bytes.Buffer{})
				root.SetIn(strings.NewReader(payload))
				root.SetArgs([]string{"hook", "passive", "codex", "session_end", "--db-path", fx.dbPath})
				if err := root.Execute(); err != nil {
					t.Fatal(err)
				}
			}
			db, err := sql.Open("sqlite", fx.dbPath)
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = db.Close() }()
			var count int
			if err := db.QueryRowContext(context.Background(), `SELECT COUNT(*) FROM events WHERE kind='note'`).Scan(&count); err != nil {
				t.Fatal(err)
			}
			want := 0
			if count != want {
				t.Fatalf("close notes=%d want=%d", count, want)
			}
			var ended sql.NullString
			if err := db.QueryRow(`SELECT ended_at FROM sessions WHERE session_id='resumable-thread'`).Scan(&ended); err != nil {
				t.Fatal(err)
			}
			if ended.Valid {
				t.Fatal("runtime close permanently ended logical session")
			}
		})
	}
}

func TestGeminiAfterAgentDoesNotEndSession(t *testing.T) {
	t.Setenv("TRACEARY_HOOK_STATE_DIR", t.TempDir())
	sessions := &sessionUsecaseStub{}
	root := newTestRootCLI(cli.WithSession(sessions), cli.WithEvent(&eventUsecaseStub{}), cli.WithStoreManagement(&storeManagementUsecaseStub{})).Command()
	root.SetOut(&bytes.Buffer{})
	root.SetErr(&bytes.Buffer{})
	root.SetIn(strings.NewReader(`{"session_id":"gemini-live-thread","prompt_response":"completed turn","hook_event_name":"AfterAgent"}`))
	root.SetArgs([]string{"hook", "transcript", "gemini"})
	if err := root.Execute(); err != nil {
		t.Fatal(err)
	}
	if len(sessions.endCalls) != 0 {
		t.Fatal("AfterAgent closed session")
	}
}

func TestGrokBackgroundStartRetainsPromptIdentity(t *testing.T) {
	t.Setenv("TRACEARY_HOOK_STATE_DIR", t.TempDir())
	sessions := &sessionUsecaseStub{startErr: model.ErrInvalidSessionState}
	events := &eventUsecaseStub{}
	for _, action := range []string{"user-prompt-submit", "session-start", "session-start", "user-prompt-submit"} {
		root := newTestRootCLI(cli.WithSession(sessions), cli.WithEvent(events), cli.WithStoreManagement(&storeManagementUsecaseStub{})).Command()
		root.SetOut(&bytes.Buffer{})
		root.SetErr(&bytes.Buffer{})
		root.SetIn(strings.NewReader(`{"sessionId":"background-thread","cwd":"/tmp","prompt":"same logical thread"}`))
		root.SetArgs([]string{"hook", "grok", action})
		if err := root.Execute(); err != nil {
			t.Fatal(err)
		}
		if action == "user-prompt-submit" && events.logCall.sessionID != "background-thread" {
			t.Fatalf("prompt attached to %q", events.logCall.sessionID)
		}
	}
	if len(sessions.endCalls) != 0 {
		t.Fatal("background startup closed session")
	}
}

func TestAntigravityUnknownIdleSuppressesContinuation(t *testing.T) {
	for _, idle := range []string{"null", `"unknown"`, "1"} {
		t.Run(idle, func(t *testing.T) {
			fx := newEnvelopeFixture(t, "unknown-idle", "antigravity", 20)
			payload := strings.TrimSuffix(antigravityStopPayload(t, "unknown-idle", true, false), "}") + `,"fullyIdle":` + idle + `}`
			stdout, code := fx.runAntigravityStop(t, payload)
			if code != 0 || stdout != `{"decision":""}` {
				t.Fatalf("unknown idle code=%d output=%q", code, stdout)
			}
			if countConsolidationRequests(t, fx.dbPath) != 0 {
				t.Fatal("unknown idle requested continuation")
			}
		})
	}
}

func TestPassiveHookUsesExplicitOneShotSessionOnlyWithNativeIdentity(t *testing.T) {
	for _, native := range []bool{true, false} {
		t.Run(map[bool]string{true: "native identity", false: "missing identity"}[native], func(t *testing.T) {
			t.Setenv("TRACEARY_HOOK_STATE_DIR", t.TempDir())
			t.Setenv("TRACEARY_RUNTIME_MODE", "one_shot")
			t.Setenv("TRACEARY_RUNTIME_SESSION_ID", "wrapper-session")
			events := &eventUsecaseStub{}
			sessions := &sessionUsecaseStub{}
			root := newTestRootCLI(cli.WithSession(sessions), cli.WithEvent(events), cli.WithStoreManagement(&storeManagementUsecaseStub{})).Command()
			payload := `{}`
			if native {
				payload = `{"session_id":"different-native-session","cwd":"/tmp"}`
			}
			root.SetIn(strings.NewReader(payload))
			root.SetOut(&bytes.Buffer{})
			root.SetErr(&bytes.Buffer{})
			root.SetArgs([]string{"hook", "passive", "codex", "interrupt"})
			if err := root.Execute(); err != nil {
				t.Fatal(err)
			}
			want := types.SessionID("")
			if native {
				want = "wrapper-session"
			}
			if events.logCall.sessionID != want {
				t.Fatalf("passive session=%q want=%q", events.logCall.sessionID, want)
			}
			if len(sessions.endCalls) != 0 {
				t.Fatal("passive hook mutated wrapper terminal state")
			}
		})
	}
}
