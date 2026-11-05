package cli

import (
	"bytes"
	"strings"
	"testing"

	app "github.com/duck8823/traceary/application/runtime"
	"github.com/duck8823/traceary/infrastructure/filesystem"
)

// Observe the public command's primary output and delivered metadata, not its
// private dispatcher. No native source or generation authority is certified.
func TestOptionalNativeClaimPortsRemainHostAndVersionIsolated(t *testing.T) {
	payload := `{"hook_event_name":"UserPromptSubmit","session_id":"native-session","turn_id":"native-turn","prompt_id":"not-claude-turn","prompt":"fixture prompt","cwd":"/tmp"}`
	for _, tc := range []struct {
		name, host, version, session, turn string
		codex, claude                      bool
	}{
		{"both_codex", "codex", "codex-cli 0.166.0", "native-session", "native-turn", true, true},
		{"both_claude", "claude", "2.1.318 (Claude Code)", "native-session", "", true, true},
		{"missing_codex_cannot_use_claude", "codex", "codex-cli 0.166.0", "", "", false, true},
		{"missing_claude_cannot_use_codex", "claude", "2.1.318 (Claude Code)", "", "", true, false},
		{"unknown_codex_no_legacy_fallback", "codex", "codex-cli 0.166.1", "", "", true, true},
		{"unknown_claude_no_legacy_fallback", "claude", "2.1.319 (Claude Code)", "", "", true, true},
		{"foreign_version_codex", "codex", "2.1.318 (Claude Code)", "", "", true, true},
		{"foreign_version_claude", "claude", "codex-cli 0.166.0", "", "", true, true},
		{"other_host_unchanged_untrusted", "grok", "fixture 1", "native-session", "native-turn", true, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv(hookStateDirEnvKey, t.TempDir())
			t.Setenv(runtimeModeEnvKey, "")
			t.Setenv(runtimeSessionIDEnvKey, "")
			events := &spoolEventUsecaseStub{}
			client := &hookObservationClient{inspect: func(o app.SelectedObservation) error {
				m := o.Metadata.Snapshot()
				if m.Host != tc.host || m.NativeSessionID != tc.session || m.NativeTurnID != tc.turn {
					t.Error("host ports leaked", m)
				}
				return nil
			}}
			options := []RootCLIOption{WithRuntimeDaemon(client), WithAntigravityObservationClaims(filesystem.NewAntigravityObservationClaimDecoder()), WithStoreManagement(&spoolStoreManagementStub{}), WithEvent(events)}
			if tc.codex {
				options = append(options, WithCodexObservationClaims(filesystem.NewCodexObservationClaimDecoder()))
			}
			if tc.claude {
				options = append(options, WithClaudeObservationClaims(filesystem.NewClaudeObservationClaimDecoder()))
			}
			root := NewRootCLI(options...).Command()
			root.SetIn(strings.NewReader(payload))
			var stdout, stderr bytes.Buffer
			root.SetOut(&stdout)
			root.SetErr(&stderr)
			root.SetArgs([]string{"hook", "prompt", tc.host, "--observe-runtime-dir", "/explicit/runtime", "--observe-epoch", strings.Repeat("a", 32), "--observe-run", strings.Repeat("b", 32), "--observe-generation", strings.Repeat("c", 32), "--observe-worktree", "w_" + strings.Repeat("d", 64), "--observe-profile", "fixture", "--observe-host-version", tc.version})
			if err := root.Execute(); err != nil {
				t.Fatal(err)
			}
			if client.calls != 1 || events.logCalls != 1 || events.lastMessage != "fixture prompt" || stdout.Len() != 0 || stderr.Len() != 0 {
				t.Fatal("primary capture/output changed", client.calls, events.logCalls, stdout.String(), stderr.String())
			}
		})
	}
}
