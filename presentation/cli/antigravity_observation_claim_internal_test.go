package cli

import (
	"context"
	"github.com/duck8823/traceary/application"
	app "github.com/duck8823/traceary/application/runtime"
	"github.com/duck8823/traceary/infrastructure/filesystem"
	"io"
	"strings"
	"testing"
)

func TestAntigravityOptionalClaimsPreservePrimaryReceiptAndBoundaryRestrictions(t *testing.T) {
	const id = "00112233-4455-4677-8899-aabbccddeeff"
	for _, action := range []string{"pre-invocation", "stop", "pre-tool-use", "post-tool-use"} {
		for _, mode := range []string{"qualified_format", "nil", "unknown_version", "malformed_claim", "foreign_fields", "absent_daemon"} {
			t.Run(action+"_"+mode, func(t *testing.T) {
				t.Setenv(hookStateDirEnvKey, t.TempDir())
				payload := `{"conversationId":"` + id + `","fullyIdle":true,"body":{"turn_id":"PRIVATE_BODY"}}`
				if mode == "malformed_claim" {
					payload = `{"conversationId":"` + id + `","conversationId":"foreign"}`
				}
				if mode == "foreign_fields" {
					payload = `{"conversationId":"` + id + `","session_id":"foreign","turn_id":"foreign-turn"}`
				}
				primary := 0
				var receipt, captured string
				client := &hookObservationClient{inspect: func(o app.SelectedObservation) error {
					v := o.Metadata.Snapshot()
					want := id
					if action == "pre-tool-use" || action == "post-tool-use" || mode == "nil" || mode == "unknown_version" || mode == "malformed_claim" || mode == "foreign_fields" {
						want = ""
					}
					if v.NativeSessionID != want || v.NativeTurnID != "" || v.EventID != receipt || primary != 1 {
						t.Error("optional correlation or primary ordering changed", v)
					}
					if mode == "absent_daemon" {
						return app.ErrDaemonUnavailable
					}
					return nil
				}}
				var decoder application.AntigravityObservationClaimDecoder
				if mode != "nil" {
					decoder = filesystem.NewAntigravityObservationClaimDecoder()
				}
				c := NewRootCLI(WithRuntimeDaemon(client), WithCodexObservationClaims(filesystem.NewCodexObservationClaimDecoder()), WithAntigravityObservationClaims(decoder), WithClaudeObservationClaims(filesystem.NewClaudeObservationClaimDecoder()))
				c.hookObservation = hookObservationFixture()
				c.hookObservation.flags.version = "1.5.0"
				if mode == "unknown_version" {
					c.hookObservation.flags.version = "1.5.1"
				}
				err := c.runHookDurably(context.Background(), "AGY native claim fixture", hookInvocationSpec{Command: "antigravity", Action: action, Client: "antigravity"}, strings.NewReader(payload), func(input io.Reader) error {
					primary++
					original, ok := input.(*explicitHookPayloadReader)
					if !ok {
						t.Fatal("original receipt reader replaced")
					}
					receipt = original.receiptID
					b, e := readHookPayload(input)
					captured = string(b)
					return e
				})
				if err != nil || primary != 1 || captured != payload || client.calls != 1 {
					t.Fatal("primary bytes/result changed", err, primary, client.calls)
				}
				paths, e := listHookSpoolRecordPaths()
				if e != nil || len(paths) != 0 {
					t.Fatal("receipt ownership changed", paths, e)
				}
			})
		}
	}
}
