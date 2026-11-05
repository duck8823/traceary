package cli

import (
	"bytes"
	"context"
	"io"
	"strings"
	"testing"

	"github.com/duck8823/traceary/application"
	app "github.com/duck8823/traceary/application/runtime"
	"github.com/duck8823/traceary/infrastructure/filesystem"
)

// Synthetic source-contract integration, not native host qualification.
func TestCodexObservationClaimsPreservePrimaryIdentityAndSpoolReceipt(t *testing.T) {
	for _, mode := range []string{"canonical", "duplicate_primary_last_wins", "legacy_only", "unknown_version", "nil_decoder"} {
		t.Run(mode, func(t *testing.T) {
			t.Setenv(hookStateDirEnvKey, t.TempDir())
			t.Setenv(runtimeModeEnvKey, "")
			t.Setenv(runtimeSessionIDEnvKey, "")
			payload := `{"hook_event_name":"UserPromptSubmit","session_id":"canonical","turn_id":"turn","prompt":"PRIVATE_BODY","event_id":"HOST_EVENT_NOT_USED"}`
			wantPrimary, wantOptional := "canonical", "canonical"
			if mode == "duplicate_primary_last_wins" {
				payload = `{"hook_event_name":"UserPromptSubmit","session_id":"first","session_id":"last","prompt":"PRIVATE_BODY"}`
				wantPrimary = "last"
				wantOptional = ""
			}
			if mode == "legacy_only" {
				payload = `{"hook_event_name":"UserPromptSubmit","thread_id":"legacy","prompt":"PRIVATE_BODY"}`
				wantPrimary = ""
				wantOptional = ""
			}
			if mode == "unknown_version" || mode == "nil_decoder" {
				wantOptional = ""
			}
			var receipt, primaryID, captured string
			client := &hookObservationClient{inspect: func(o app.SelectedObservation) error {
				m := o.Metadata.Snapshot()
				if m.NativeSessionID != wantOptional || m.EventID != receipt || !strings.HasPrefix(m.EventID, "spool:") {
					t.Error("claim replaced ownership or receipt", m)
				}
				if wantOptional == "" && m.NativeTurnID != "" {
					t.Error("partial claims escaped", m)
				}
				return nil
			}}
			var decoder application.CodexObservationClaimDecoder
			if mode != "nil_decoder" {
				decoder = filesystem.NewCodexObservationClaimDecoder()
			}
			root := NewRootCLI(WithRuntimeDaemon(client), WithCodexObservationClaims(decoder))
			root.hookObservation = hookObservationFixture()
			root.hookObservation.flags.version = "codex-cli 0.166.0"
			if mode == "unknown_version" {
				root.hookObservation.flags.version = "codex-cli 0.166.1"
			}
			primary := 0
			err := root.runHookDurably(context.Background(), "prompt", hookInvocationSpec{Command: "prompt", Client: "codex"}, strings.NewReader(payload), func(input io.Reader) error {
				primary++
				reader, ok := input.(*explicitHookPayloadReader)
				if !ok {
					t.Fatal("original receipt reader replaced")
				}
				receipt = reader.receiptID
				body, e := readHookPayload(input)
				if e != nil {
					return e
				}
				captured = string(body)
				id, e := resolveHookParentSessionID(body, "codex")
				primaryID = id.String()
				return e
			})
			if err != nil || primary != 1 || primaryID != wantPrimary || captured != payload || client.calls != 1 {
				t.Fatal("primary capture/decision changed", err, primary, primaryID, client.calls)
			}
			paths, e := listHookSpoolRecordPaths()
			if e != nil || len(paths) != 0 {
				t.Fatal("receipt ownership changed", paths, e)
			}
		})
	}
}

func TestCodexObservationClaimsRealCobraFlagsKeepOneOriginalCapture(t *testing.T) {
	for _, mode := range []string{"baseline", "queued", "absent", "nil_decoder"} {
		t.Run(mode, func(t *testing.T) {
			t.Setenv(hookStateDirEnvKey, t.TempDir())
			t.Setenv(runtimeModeEnvKey, "")
			t.Setenv(runtimeSessionIDEnvKey, "")
			events := &spoolEventUsecaseStub{}
			client := &hookObservationClient{inspect: func(o app.SelectedObservation) error {
				if mode != "nil_decoder" && o.Metadata.Snapshot().NativeSessionID != "canonical" {
					t.Error("actual injected decoder not reached", o.Metadata.Snapshot())
				}
				if mode == "absent" {
					return app.ErrDaemonUnavailable
				}
				return nil
			}}
			var decoder application.CodexObservationClaimDecoder
			if mode != "nil_decoder" {
				decoder = filesystem.NewCodexObservationClaimDecoder()
			}
			root := NewRootCLI(WithRuntimeDaemon(client), WithCodexObservationClaims(decoder), WithStoreManagement(&spoolStoreManagementStub{}), WithEvent(events)).Command()
			root.SetIn(strings.NewReader(`{"hook_event_name":"UserPromptSubmit","session_id":"canonical","turn_id":"turn","prompt":"original prompt bytes","cwd":"/tmp"}`))
			var stdout, stderr bytes.Buffer
			root.SetOut(&stdout)
			root.SetErr(&stderr)
			args := []string{"hook", "prompt", "codex"}
			if mode != "baseline" {
				args = append(args, "--observe-runtime-dir", "/explicit/runtime", "--observe-epoch", strings.Repeat("a", 32), "--observe-run", strings.Repeat("b", 32), "--observe-generation", strings.Repeat("c", 32), "--observe-worktree", "w_"+strings.Repeat("d", 64), "--observe-profile", "fixture", "--observe-host-version", "codex-cli 0.166.0")
			}
			root.SetArgs(args)
			if err := root.Execute(); err != nil {
				t.Fatal(err)
			}
			want := 1
			if mode == "baseline" {
				want = 0
			}
			if client.calls != want || events.logCalls != 1 || events.lastMessage != "original prompt bytes" || stdout.Len() != 0 || stderr.Len() != 0 {
				t.Fatal("original handler/result changed", client.calls, events.logCalls, stdout.String(), stderr.String())
			}
		})
	}
}
