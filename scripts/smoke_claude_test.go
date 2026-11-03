package scripts_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestClaudePackageSmokePreservesLauncherHome(t *testing.T) {
	for _, fail := range []bool{false, true} {
		t.Run(map[bool]string{false: "validates both packages", true: "propagates validation failure"}[fail], func(t *testing.T) {
			root := t.TempDir()
			home := t.TempDir()
			bin := t.TempDir()
			log := filepath.Join(root, "calls")
			if err := os.MkdirAll(filepath.Join(root, "scripts"), 0700); err != nil {
				t.Fatal(err)
			}
			source, err := os.ReadFile(filepath.Join(repoRootFromScripts(t), "scripts", "smoke_test_integrations.sh"))
			if err != nil {
				t.Fatal(err)
			}
			script := filepath.Join(root, "scripts", "smoke_test_integrations.sh")
			if err := os.WriteFile(script, source, 0700); err != nil {
				t.Fatal(err)
			}
			writeStub(t, filepath.Join(bin, "claude"), "#!/bin/sh\nexec \"$HOME/real-claude\" \"$@\"\n")
			writeStub(t, filepath.Join(home, "real-claude"), `#!/bin/sh
set -eu
[ "$HOME" = "$EXPECTED_HOME" ]
[ -n "$CLAUDE_CONFIG_DIR" ]
[ "$CLAUDE_CONFIG_DIR" != "$HOME/.claude" ]
printf '%s\n' "$CLAUDE_CONFIG_DIR" >> "$CALLS"
[ "$1 $2" = "plugins validate" ]
printf '%s\n' "$3" >> "$CALLS"
if [ "$FAIL_SECOND" = 1 ] && [ "$3" = "$EXPECTED_ROOT/integrations/claude-plugin" ]; then exit 7; fi
`)
			failure := "0"
			if fail {
				failure = "1"
			}
			cmd := exec.Command("bash", script, "claude")
			cmd.Env = append(os.Environ(), "HOME="+home, "EXPECTED_HOME="+home, "EXPECTED_ROOT="+root, "CALLS="+log, "FAIL_SECOND="+failure, "PATH="+bin+string(os.PathListSeparator)+os.Getenv("PATH"))
			out, err := cmd.CombinedOutput()
			if (err != nil) != fail {
				t.Fatalf("failure=%v err=%v output=%s", fail, err, out)
			}
			if fail {
				if exit, ok := err.(*exec.ExitError); !ok || exit.ExitCode() != 7 {
					t.Fatalf("expected exit 7: %v", err)
				}
			}
			data, err := os.ReadFile(log)
			if err != nil {
				t.Fatal(err)
			}
			lines := strings.Split(strings.TrimSpace(string(data)), "\n")
			if len(lines) != 4 || lines[1] != filepath.Join(root, ".claude-plugin", "marketplace.json") || lines[3] != filepath.Join(root, "integrations", "claude-plugin") {
				t.Fatalf("unexpected calls: %s", data)
			}
			if _, err := os.Stat(lines[0]); !os.IsNotExist(err) {
				t.Fatalf("temporary config not cleaned: %v", err)
			}
		})
	}
}
