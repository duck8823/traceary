package cli

import (
	"context"
	"io"
	"strings"
	"testing"

	"github.com/duck8823/traceary/application"
	app "github.com/duck8823/traceary/application/runtime"
	"github.com/duck8823/traceary/infrastructure/filesystem"
)

func TestClaudeOptionalClaimsPreservePrimaryBytesReceiptAndNoTurnAuthority(t *testing.T) {
	for _, mode := range []string{"qualified_format", "nil", "unknown_version", "malformed_claim", "absent_daemon"} {
		t.Run(mode, func(t *testing.T) {
			t.Setenv(hookStateDirEnvKey, t.TempDir())
			payload := `{"hook_event_name":"UserPromptSubmit","session_id":"native-session","prompt_id":"prompt-not-turn","turn_id":"foreign-turn","prompt":"PRIVATE_BODY","cwd":"/tmp"}`
			if mode == "malformed_claim" {
				payload = `{"hook_event_name":"UserPromptSubmit","session_id":"native-session","session_id":"foreign","prompt":"PRIVATE_BODY"}`
			}
			var receipt, captured string
			primary := 0
			client := &hookObservationClient{inspect: func(o app.SelectedObservation) error {
				v := o.Metadata.Snapshot()
				want := "native-session"
				if mode == "nil" || mode == "unknown_version" || mode == "malformed_claim" {
					want = ""
				}
				if v.NativeSessionID != want || v.NativeTurnID != "" || v.EventID != receipt || primary != 1 {
					t.Error("optional metadata/ordering changed", v)
				}
				if mode == "absent_daemon" {
					return app.ErrDaemonUnavailable
				}
				return nil
			}}
			var decoder application.ClaudeObservationClaimDecoder
			if mode != "nil" {
				decoder = filesystem.NewClaudeObservationClaimDecoder()
			}
			root := NewRootCLI(WithRuntimeDaemon(client), WithClaudeObservationClaims(decoder))
			root.hookObservation = hookObservationFixture()
			root.hookObservation.flags.version = "2.1.318 (Claude Code)"
			if mode == "unknown_version" {
				root.hookObservation.flags.version = "2.1.319 (Claude Code)"
			}
			err := root.runHookDurably(context.Background(), "prompt", hookInvocationSpec{Command: "prompt", Client: "claude"}, strings.NewReader(payload), func(input io.Reader) error {
				primary++
				original, ok := input.(*explicitHookPayloadReader)
				if !ok {
					t.Fatal("original receipt reader replaced")
				}
				receipt = original.receiptID
				data, e := readHookPayload(input)
				captured = string(data)
				return e
			})
			if err != nil || primary != 1 || captured != payload || client.calls != 1 {
				t.Fatal("primary bytes/result changed", err, primary, client.calls)
			}
			paths, e := listHookSpoolRecordPaths()
			if e != nil || len(paths) != 0 {
				t.Fatal("original receipt ownership changed", paths, e)
			}
		})
	}
}
