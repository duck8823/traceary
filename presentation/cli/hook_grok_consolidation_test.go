package cli_test

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/duck8823/traceary/domain/types"
	cli "github.com/duck8823/traceary/presentation/cli"
)

func TestHookGrokStop_ConsolidationEnvelope(t *testing.T) {
	const sessionID = "019f0000-0000-7000-8000-000000000276"

	t.Run("emits a block envelope with the stop reason when a request is due", func(t *testing.T) {
		fx := newEnvelopeFixture(t, sessionID, "grok", 20)
		stdout, code := fx.runGrokStop(t, grokStopPayload(t, sessionID, true, false))
		if code != 0 {
			t.Fatalf("exit = %d, want 0; stdout=%q", code, stdout)
		}
		env := decodeStopEnvelope(t, stdout)
		if env["decision"] != "block" {
			t.Fatalf("decision = %v, want block; stdout=%q", env["decision"], stdout)
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

	t.Run("writes nothing to stdout when no request is due", func(t *testing.T) {
		fx := newEnvelopeFixture(t, sessionID, "grok", 0)
		stdout, code := fx.runGrokStop(t, grokStopPayload(t, sessionID, true, false))
		if code != 0 {
			t.Fatalf("exit = %d", code)
		}
		if stdout != "" {
			t.Fatalf("stdout = %q, want empty", stdout)
		}
		if countConsolidationRequests(t, fx.dbPath) != 0 {
			t.Fatal("unexpected ledger row")
		}
	})

	t.Run("never exits non-zero when a request is due", func(t *testing.T) {
		fx := newEnvelopeFixture(t, sessionID, "grok", 20)
		_, code := fx.runGrokStop(t, grokStopPayload(t, sessionID, true, false))
		if code != 0 {
			t.Fatalf("exit = %d, want 0 (never exit 2 on Grok)", code)
		}
	})

	t.Run("writes nothing when the ledger row could not be written", func(t *testing.T) {
		fx := newEnvelopeFixture(t, sessionID, "grok", 20)
		fx.requestUC = &consolidationRequestUsecaseStub{err: errors.New("insert failed")}
		stdout, code := fx.runGrokStop(t, grokStopPayload(t, sessionID, true, false))
		if code != 0 {
			t.Fatalf("exit = %d", code)
		}
		if stdout != "" {
			t.Fatalf("stdout = %q, want empty on D-4 fail-closed", stdout)
		}
	})
}

func (fx *promptChannelFixture) runGrokStop(t *testing.T, payload string) (string, int) {
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
	rootCmd.SetArgs([]string{"hook", "grok", "stop", "--db-path", fx.dbPath})
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

func grokStopPayload(t *testing.T, sessionID string, withTranscript, stopHookActive bool) string {
	t.Helper()
	payload := grokJSONWithField(t, readGrokFixture(t, "stop.json"), "sessionId", sessionID)
	if withTranscript {
		path := filepath.Join(t.TempDir(), "updates.jsonl")
		transcript := strings.Join([]string{
			`{"method":"session/update","params":{"update":{"sessionUpdate":"user_message_chunk","content":{"type":"text","text":"prompt"}},"_meta":{"promptId":"prompt-contract-probe-1"}}}`,
			`{"method":"session/update","params":{"update":{"sessionUpdate":"agent_message_chunk","content":{"type":"text","text":"grok final ` + sessionID + `"}},"_meta":{"promptId":"prompt-contract-probe-1"}}}`,
		}, "\n") + "\n"
		if err := os.WriteFile(path, []byte(transcript), 0o600); err != nil {
			t.Fatal(err)
		}
		payload = grokJSONWithField(t, payload, "transcriptPath", path)
	} else {
		payload = grokJSONWithoutField(t, payload, "transcriptPath")
	}
	if stopHookActive {
		payload = grokJSONWithField(t, payload, "stop_hook_active", true)
	}
	return payload
}
