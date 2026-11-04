package cli

import (
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
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

func TestClaudeClaimDeliveryMatchesEveryBoundaryWithoutChangingPrimary(t *testing.T) {
	cases := []struct{ command, action, event string }{
		{"prompt", "", "UserPromptSubmit"}, {"audit", "", "PreToolUse"}, {"audit", "", "PostToolUse"},
		{"transcript", "", "Stop"}, {"session", "start", "SessionStart"}, {"session", "end", "SessionEnd"}, {"session", "stop", "Stop"},
	}
	for _, tc := range cases {
		for _, mismatch := range []bool{false, true} {
			name := tc.command + "_" + tc.action + "_" + tc.event
			if mismatch {
				name += "_mismatch"
			}
			t.Run(name, func(t *testing.T) {
				t.Setenv(hookStateDirEnvKey, t.TempDir())
				event := tc.event
				if mismatch {
					event = "unrecognized-event"
				}
				payload := `{"hook_event_name":"` + event + `","session_id":"native-session","prompt_id":"not-turn","turn_id":"foreign"}`
				primary := 0
				var receipt, captured string
				client := &hookObservationClient{inspect: func(o app.SelectedObservation) error {
					m := o.Metadata.Snapshot()
					want := "native-session"
					if mismatch {
						want = ""
					}
					if m.NativeSessionID != want || m.NativeTurnID != "" || m.EventID != receipt || primary != 1 {
						t.Error("boundary correlation/ordering changed", m)
					}
					return nil
				}}
				root := NewRootCLI(WithRuntimeDaemon(client), WithClaudeObservationClaims(filesystem.NewClaudeObservationClaimDecoder()))
				root.hookObservation = hookObservationFixture()
				root.hookObservation.flags.version = "2.1.318 (Claude Code)"
				spec := hookInvocationSpec{Command: tc.command, Action: tc.action, Client: "claude"}
				err := root.runHookDurably(context.Background(), "boundary fixture", spec, strings.NewReader(payload), func(input io.Reader) error {
					primary++
					reader, ok := input.(*explicitHookPayloadReader)
					if !ok {
						t.Fatal("receipt reader replaced")
					}
					receipt = reader.receiptID
					b, e := readHookPayload(input)
					captured = string(b)
					return e
				})
				if err != nil || primary != 1 || captured != payload || client.calls != 1 {
					t.Fatal("primary changed", err, primary, client.calls)
				}
				paths, e := listHookSpoolRecordPaths()
				if e != nil || len(paths) != 0 {
					t.Fatal("receipt retained", paths, e)
				}
			})
		}
	}
}

func TestClaudePackagedHookRoutesHaveExplicitClaimSupport(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("..", "..", "integrations", "claude-plugin", "hooks", "hooks.json"))
	if err != nil {
		t.Fatal(err)
	}
	var config struct {
		Hooks map[string][]struct {
			Hooks []struct {
				Command string `json:"command"`
			} `json:"hooks"`
		} `json:"hooks"`
	}
	if err = json.Unmarshal(data, &config); err != nil {
		t.Fatal(err)
	}
	root := NewRootCLI(WithClaudeObservationClaims(filesystem.NewClaudeObservationClaimDecoder()))
	supported := map[string]bool{"SessionStart": true, "SessionEnd": true, "Stop": true, "UserPromptSubmit": true, "PreToolUse": true, "PostToolUse": true}
	checked := 0
	for event, groups := range config.Hooks {
		for _, group := range groups {
			for _, hook := range group.Hooks {
				words := strings.Fields(strings.ReplaceAll(hook.Command, "'", ""))
				if len(words) < 4 || words[0] != "traceary" || words[1] != "hook" || words[3] != "claude" {
					continue
				}
				spec := hookInvocationSpec{Command: words[2], Client: words[3]}
				if len(words) > 4 {
					spec.Action = words[4]
				}
				_, kind, ok := hookObservationBoundary(spec)
				if !ok {
					continue
				}
				checked++
				payload := `{"hook_event_name":"` + event + `","session_id":"native-session"}`
				got := root.claudeHookObservationSession(context.Background(), payload, "2.1.318 (Claude Code)", kind)
				want := ""
				if supported[event] {
					want = "native-session"
				}
				if got != want {
					t.Errorf("packaged %s/%s/%s claim=%q want %q", event, spec.Command, spec.Action, got, want)
				}
			}
		}
	}
	if checked < 7 {
		t.Fatal("packaged primary observation routes disappeared", checked)
	}
}
