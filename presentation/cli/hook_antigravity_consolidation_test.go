package cli_test

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/duck8823/traceary/domain/types"
	cli "github.com/duck8823/traceary/presentation/cli"
)

func TestHookAntigravityStop_ConsolidationEnvelope(t *testing.T) {
	const sessionID = "agy-consol-sess"

	t.Run("emits a continue envelope with the stop reason when a request is due", func(t *testing.T) {
		fx := newEnvelopeFixture(t, sessionID, "antigravity", 20)
		stdout, code := fx.runAntigravityStop(t, antigravityStopPayload(t, sessionID, true, false))
		if code != 0 {
			t.Fatalf("exit = %d, want 0; stdout=%q", code, stdout)
		}
		env := decodeStopEnvelope(t, stdout)
		if env["decision"] != "continue" {
			t.Fatalf("decision = %v, want continue; stdout=%q", env["decision"], stdout)
		}
		reason, _ := env["reason"].(string)
		if !strings.HasPrefix(reason, "[Traceary] Session "+sessionID) {
			t.Fatalf("reason = %q", reason)
		}
		row := mustConsolidationRow(t, fx.dbPath, sessionID)
		if row.delivery != types.ConsolidationDeliveryAdditionalContext.String() {
			t.Fatalf("delivery = %q", row.delivery)
		}
	})

	t.Run("emits an empty decision when no request is due", func(t *testing.T) {
		fx := newEnvelopeFixture(t, sessionID, "antigravity", 0)
		stdout, code := fx.runAntigravityStop(t, antigravityStopPayload(t, sessionID, true, false))
		if code != 0 {
			t.Fatalf("exit = %d", code)
		}
		if stdout != `{"decision":""}` {
			t.Fatalf("stdout = %q, want empty decision", stdout)
		}
		if countConsolidationRequests(t, fx.dbPath) != 0 {
			t.Fatal("unexpected ledger row")
		}
	})

	t.Run("emits an empty decision when the transcript was not recorded", func(t *testing.T) {
		fx := newEnvelopeFixture(t, sessionID, "antigravity", 20)
		stdout, code := fx.runAntigravityStop(t, antigravityStopPayload(t, sessionID, false, false))
		if code != 0 {
			t.Fatalf("exit = %d", code)
		}
		if stdout != `{"decision":""}` {
			t.Fatalf("stdout = %q", stdout)
		}
		if countConsolidationRequests(t, fx.dbPath) != 0 {
			t.Fatal("ledger row without a recorded transcript")
		}
	})

	t.Run("emits an empty decision when the ledger row could not be written", func(t *testing.T) {
		fx := newEnvelopeFixture(t, sessionID, "antigravity", 20)
		fx.requestUC = &consolidationRequestUsecaseStub{err: errors.New("insert failed")}
		stdout, code := fx.runAntigravityStop(t, antigravityStopPayload(t, sessionID, true, false))
		if code != 0 {
			t.Fatalf("exit = %d", code)
		}
		if stdout != `{"decision":""}` {
			t.Fatalf("stdout = %q, want fail-closed empty decision", stdout)
		}
	})

	t.Run("emits an empty decision when the payload carries stop_hook_active", func(t *testing.T) {
		fx := newEnvelopeFixture(t, sessionID, "antigravity", 20)
		stdout, code := fx.runAntigravityStop(t, antigravityStopPayload(t, sessionID, true, true))
		if code != 0 {
			t.Fatalf("exit = %d", code)
		}
		if stdout != `{"decision":""}` {
			t.Fatalf("stdout = %q", stdout)
		}
		if countConsolidationRequests(t, fx.dbPath) != 0 {
			t.Fatal("ledger row despite stop_hook_active")
		}
	})

	t.Run("exit status stays 0 in every case", func(t *testing.T) {
		fx := newEnvelopeFixture(t, sessionID, "antigravity", 20)
		_, code := fx.runAntigravityStop(t, antigravityStopPayload(t, sessionID, true, false))
		if code != 0 {
			t.Fatalf("exit = %d, want 0", code)
		}
	})
}

func (fx *promptChannelFixture) runAntigravityStop(t *testing.T, payload string) (string, int) {
	t.Helper()
	stdout := &bytes.Buffer{}
	rootCmd := cli.NewRootCLI(
		cli.WithStoreManagement(fx.storeUC),
		cli.WithEvent(fx.eventUC),
		cli.WithSession(fx.sessionUC),
		cli.WithConsolidationPressure(fx.pressureUC),
		cli.WithConsolidationRequest(fx.requestUC),
		cli.WithSessionEventOrder(fx.order),
		cli.WithDatabasePathSetter(fx.db.SetPath),
	).Command()
	rootCmd.SetIn(strings.NewReader(payload))
	rootCmd.SetOut(stdout)
	rootCmd.SetErr(&bytes.Buffer{})
	rootCmd.SetArgs([]string{"hook", "antigravity", "stop", "--db-path", fx.dbPath})
	err := rootCmd.Execute()
	if err == nil {
		return stdout.String(), 0
	}
	var coder interface{ ExitCode() int }
	if errors.As(err, &coder) {
		return stdout.String(), coder.ExitCode()
	}
	t.Fatalf("Execute() error = %v (type %T)", err, err)
	return stdout.String(), 0
}

func newEnvelopeFixture(t *testing.T, sessionID, agent string, commandCount int) *promptChannelFixture {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("TRACEARY_HOOK_STATE_DIR", t.TempDir())
	t.Setenv("TRACEARY_WORKSPACE", "github.com/dogfood/test")
	cli.SetUserHomeDirFunc(func() (string, error) { return home, nil })
	t.Cleanup(cli.ResetUserHomeDirFunc)
	return newPromptChannelFixture(t, sessionID, agent, commandCount, false)
}

func antigravityStopPayload(t *testing.T, sessionID string, withTranscript, stopHookActive bool) string {
	t.Helper()
	payload := map[string]any{
		"conversationId":    sessionID,
		"workspacePaths":    []string{"/tmp"},
		"terminationReason": "completed",
	}
	if withTranscript {
		path := filepath.Join(t.TempDir(), "transcript.jsonl")
		body := strings.Join([]string{
			`{"step_index":0,"source":"USER_EXPLICIT","type":"USER_INPUT","status":"DONE","content":"agy prompt"}`,
			`{"step_index":1,"source":"MODEL","type":"PLANNER_RESPONSE","status":"DONE","thinking":"reason","content":"agy final ` + sessionID + `"}`,
		}, "\n") + "\n"
		if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		payload["transcriptPath"] = path
	}
	if stopHookActive {
		payload["stop_hook_active"] = true
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

func decodeStopEnvelope(t *testing.T, stdout string) map[string]any {
	t.Helper()
	var env map[string]any
	if err := json.Unmarshal([]byte(stdout), &env); err != nil {
		t.Fatalf("decode envelope: %v; stdout=%q", err, stdout)
	}
	return env
}
