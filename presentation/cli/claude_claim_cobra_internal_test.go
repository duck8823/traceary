package cli

import (
	"bytes"
	"strings"
	"testing"

	app "github.com/duck8823/traceary/application/runtime"
	"github.com/duck8823/traceary/infrastructure/filesystem"
)

func TestClaudeClaimDecoderRealCobraKeepsPrimaryAndNeverInfersSelectionFromEnvironment(t *testing.T) {
	for _, mode := range []string{"baseline", "explicit", "environment_only", "absent_daemon"} {
		t.Run(mode, func(t *testing.T) {
			t.Setenv(hookStateDirEnvKey, t.TempDir())
			t.Setenv(runtimeModeEnvKey, "")
			t.Setenv(runtimeSessionIDEnvKey, "")
			for name, value := range map[string]string{"TRACEARY_RUNTIME_EPOCH": strings.Repeat("a", 32), "TRACEARY_RUNTIME_RUN_ID": strings.Repeat("b", 32), "TRACEARY_RUNTIME_GENERATION": strings.Repeat("c", 32)} {
				t.Setenv(name, value)
			}
			events := &spoolEventUsecaseStub{}
			client := &hookObservationClient{inspect: func(o app.SelectedObservation) error {
				metadata := o.Metadata.Snapshot()
				if metadata.NativeSessionID != "native-session" || metadata.NativeTurnID != "" {
					t.Error("wrong session/turn claims", metadata)
				}
				if mode == "absent_daemon" {
					return app.ErrDaemonUnavailable
				}
				return nil
			}}
			root := NewRootCLI(WithRuntimeDaemon(client), WithClaudeObservationClaims(filesystem.NewClaudeObservationClaimDecoder()), WithStoreManagement(&spoolStoreManagementStub{}), WithEvent(events)).Command()
			root.SetIn(strings.NewReader(`{"hook_event_name":"UserPromptSubmit","session_id":"native-session","prompt_id":"not-a-turn","turn_id":"foreign","prompt":"original prompt bytes","cwd":"/tmp"}`))
			var stdout, stderr bytes.Buffer
			root.SetOut(&stdout)
			root.SetErr(&stderr)
			args := []string{"hook", "prompt", "claude"}
			if mode == "explicit" || mode == "absent_daemon" {
				args = append(args, "--observe-runtime-dir", "/explicit/runtime", "--observe-epoch", strings.Repeat("a", 32), "--observe-run", strings.Repeat("b", 32), "--observe-generation", strings.Repeat("c", 32), "--observe-worktree", "w_"+strings.Repeat("d", 64), "--observe-profile", "fixture", "--observe-host-version", "2.1.318 (Claude Code)")
			}
			root.SetArgs(args)
			if err := root.Execute(); err != nil {
				t.Fatal(err)
			}
			want := 0
			if mode == "explicit" || mode == "absent_daemon" {
				want = 1
			}
			if client.calls != want || events.logCalls != 1 || events.lastMessage != "original prompt bytes" || stdout.Len() != 0 || stderr.Len() != 0 {
				t.Fatal("primary output/capture or explicit-only selection changed", client.calls, events.logCalls, stdout.String(), stderr.String())
			}
		})
	}
}
