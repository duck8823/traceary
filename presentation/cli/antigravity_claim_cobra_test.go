package cli_test

import (
	"bytes"
	app "github.com/duck8823/traceary/application/runtime"
	"github.com/duck8823/traceary/domain/types"
	"github.com/duck8823/traceary/infrastructure/filesystem"
	"github.com/duck8823/traceary/presentation/cli"
	"strings"
	"testing"
)

func TestAntigravityClaimCobraKeepsPrimaryAndExplicitSelectionOnly(t *testing.T) {
	const id = "00112233-4455-4677-8899-aabbccddeeff"
	for _, mode := range []string{"baseline", "explicit", "environment_only", "partial", "nil", "unknown_version", "foreign_fields", "absent_daemon"} {
		t.Run(mode, func(t *testing.T) {
			t.Setenv("TRACEARY_HOOK_STATE_DIR", t.TempDir())
			t.Setenv("TRACEARY_WORKSPACE", "github.com/duck8823/traceary")
			t.Setenv("TRACEARY_RUNTIME_MODE", "")
			t.Setenv("TRACEARY_RUNTIME_SESSION_ID", "")
			for name, value := range map[string]string{"TRACEARY_RUNTIME_EPOCH": strings.Repeat("a", 32), "TRACEARY_RUNTIME_RUN_ID": strings.Repeat("b", 32), "TRACEARY_RUNTIME_GENERATION": strings.Repeat("c", 32)} {
				t.Setenv(name, value)
			}
			home := t.TempDir()
			cli.SetUserHomeDirFunc(func() (string, error) { return home, nil })
			t.Cleanup(cli.ResetUserHomeDirFunc)
			events := &eventUsecaseStub{}
			sessions := &sessionUsecaseStub{}
			client := &observeCLIClient{}
			if mode == "absent_daemon" {
				client.err = app.ErrDaemonUnavailable
			}
			options := []cli.RootCLIOption{cli.WithRuntimeDaemon(client), cli.WithCodexObservationClaims(filesystem.NewCodexObservationClaimDecoder()), cli.WithClaudeObservationClaims(filesystem.NewClaudeObservationClaimDecoder()), cli.WithStoreManagement(&storeManagementUsecaseStub{}), cli.WithEvent(events), cli.WithSession(sessions)}
			if mode != "nil" {
				options = append(options, cli.WithAntigravityObservationClaims(filesystem.NewAntigravityObservationClaimDecoder()))
			}
			cmd := newTestRootCLI(options...).Command()
			payload := `{"conversationId":"` + id + `","workspacePaths":["/tmp"],"modelName":"PRIVATE_MODEL"}`
			if mode == "foreign_fields" {
				payload = `{"conversationId":"` + id + `","workspacePaths":["/tmp"],"session_id":"foreign","turn_id":"foreign-turn"}`
			}
			cmd.SetIn(strings.NewReader(payload))
			var out, errout bytes.Buffer
			cmd.SetOut(&out)
			cmd.SetErr(&errout)
			args := []string{"hook", "antigravity", "pre-invocation"}
			if mode != "baseline" && mode != "environment_only" {
				args = append(args, "--observe-runtime-dir", "/explicit/runtime")
				if mode != "partial" {
					version := "1.5.0"
					if mode == "unknown_version" {
						version = "1.5.1"
					}
					args = append(args, "--observe-epoch", strings.Repeat("a", 32), "--observe-run", strings.Repeat("b", 32), "--observe-generation", strings.Repeat("c", 32), "--observe-worktree", "w_"+strings.Repeat("d", 64), "--observe-profile", "fixture", "--observe-host-version", version)
				}
			}
			cmd.SetArgs(args)
			if err := cmd.Execute(); err != nil {
				t.Fatal(err)
			}
			wantCalls := 1
			if mode == "baseline" || mode == "environment_only" || mode == "partial" {
				wantCalls = 0
			}
			if client.calls != wantCalls || out.String() != "{}" || errout.Len() != 0 || sessions.startCall.sessionID != types.SessionID(id) {
				t.Fatal("primary output/session or explicit selection changed", client.calls, out.String(), errout.String(), sessions.startCall.sessionID)
			}
			if wantCalls == 1 {
				m := client.value.Metadata.Snapshot()
				want := id
				if mode == "nil" || mode == "unknown_version" || mode == "foreign_fields" {
					want = ""
				}
				if m.NativeSessionID != want || m.NativeTurnID != "" {
					t.Fatal("cross-host/fallback/turn claim", m)
				}
			}
		})
	}
}
