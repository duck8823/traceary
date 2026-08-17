package cli

import (
	"bytes"
	"os"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/spf13/cobra"
)

func TestRootCLI_Command_SilencesCobraErrorOutput(t *testing.T) {
	t.Parallel()

	rootCmd := NewRootCLI().Command()
	if !rootCmd.SilenceErrors {
		t.Fatal("rootCmd.SilenceErrors = false, want true")
	}
	if !rootCmd.SilenceUsage {
		t.Fatal("rootCmd.SilenceUsage = false, want true")
	}
}

func TestRootCLI_HelpDefaultsToEnglish(t *testing.T) {
	t.Parallel()

	stdout := &bytes.Buffer{}
	rootCmd := NewRootCLI().Command()
	rootCmd.SetOut(stdout)
	rootCmd.SetErr(&bytes.Buffer{})
	rootCmd.SetArgs([]string{"--help"})

	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if !strings.Contains(stdout.String(), "Traceary records and inspects local AI-agent work history") {
		t.Fatalf("stdout = %q", stdout.String())
	}
}

func TestRootCLI_HelpCanUseJapanese(t *testing.T) {
	t.Setenv("TRACEARY_LANG", "ja")

	stdout := &bytes.Buffer{}
	rootCmd := NewRootCLI().Command()
	rootCmd.SetOut(stdout)
	rootCmd.SetErr(&bytes.Buffer{})
	rootCmd.SetArgs([]string{"--help"})

	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if !strings.Contains(stdout.String(), "Traceary はローカルの AI agent 作業履歴を記録・確認します") {
		t.Fatalf("stdout = %q", stdout.String())
	}
}

func TestRootCLI_NoArgsShowsHelp(t *testing.T) {
	t.Parallel()

	stdout := &bytes.Buffer{}
	stderr := &bytes.Buffer{}
	rootCmd := NewRootCLI().Command()
	rootCmd.SetIn(strings.NewReader(""))
	rootCmd.SetOut(stdout)
	rootCmd.SetErr(stderr)
	rootCmd.SetArgs([]string{})

	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	for _, want := range []string{
		"Traceary records and inspects local AI-agent work history",
		"traceary list",
		"traceary search",
		"traceary doctor --json",
		"Available Commands:",
	} {
		if !strings.Contains(stdout.String(), want) {
			t.Fatalf("stdout = %q, want %q", stdout.String(), want)
		}
	}
	if strings.Contains(stdout.String(), "operator cockpit") || strings.Contains(stdout.String(), "Tail-first") {
		t.Fatalf("stdout still describes the removed cockpit:\n%s", stdout.String())
	}
	if stderr.String() != "" {
		t.Fatalf("stderr = %q, want empty stderr for plain help", stderr.String())
	}
}

func TestRootCLI_BareWithDBPathStillShowsHelp(t *testing.T) {
	t.Parallel()

	stdout := &bytes.Buffer{}
	stderr := &bytes.Buffer{}
	rootCmd := NewRootCLI().Command()
	rootCmd.SetIn(strings.NewReader(""))
	rootCmd.SetOut(stdout)
	rootCmd.SetErr(stderr)
	rootCmd.SetArgs([]string{"--db-path", "./traceary.test.db"})

	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("Execute(--db-path) error = %v", err)
	}
	if !strings.Contains(stdout.String(), "Available Commands:") {
		t.Fatalf("stdout = %q, want help", stdout.String())
	}
	if stderr.String() != "" {
		t.Fatalf("stderr = %q, want empty", stderr.String())
	}
}

func TestRootCLI_ResetStateIsUnknownFlag(t *testing.T) {
	t.Parallel()

	rootCmd := NewRootCLI().Command()
	rootCmd.SetOut(&bytes.Buffer{})
	rootCmd.SetErr(&bytes.Buffer{})
	rootCmd.SetArgs([]string{"--reset-state"})

	err := rootCmd.Execute()
	if err == nil {
		t.Fatal("Execute(--reset-state) error = nil, want unknown-flag error")
	}
	if !strings.Contains(err.Error(), "unknown flag: --reset-state") {
		t.Fatalf("error = %q, want unknown --reset-state", err.Error())
	}
}

func TestRootCLI_TuiAndDashboardAreUnknownCommands(t *testing.T) {
	t.Parallel()

	for _, command := range []string{"tui", "dashboard"} {
		t.Run(command, func(t *testing.T) {
			t.Parallel()

			rootCmd := NewRootCLI().Command()
			rootCmd.SetOut(&bytes.Buffer{})
			rootCmd.SetErr(&bytes.Buffer{})
			rootCmd.SetArgs([]string{command})

			err := rootCmd.Execute()
			if err == nil {
				t.Fatalf("Execute(%s) error = nil, want unknown-command error", command)
			}
			if !strings.Contains(err.Error(), `unknown command "`+command+`" for "traceary"`) {
				t.Fatalf("error = %q, want unknown-command error for %s", err.Error(), command)
			}
		})
	}
}

func TestRootCLI_BareMatchesHelpAndIsUnnotified(t *testing.T) {
	run := func(args ...string) (string, string) {
		stdout := &bytes.Buffer{}
		stderr := &bytes.Buffer{}
		rootCmd := NewRootCLI().Command()
		rootCmd.SetIn(strings.NewReader(""))
		rootCmd.SetOut(stdout)
		rootCmd.SetErr(stderr)
		rootCmd.SetArgs(args)
		if err := rootCmd.Execute(); err != nil {
			t.Fatalf("Execute(%v) error = %v", args, err)
		}
		return stdout.String(), stderr.String()
	}

	// Bare help and explicit --help both render usage. Cobra's help path and
	// RunE Help() can differ slightly in trailing newlines, so compare content
	// presence rather than exact bytes for the TTY/non-TTY parity contract.
	bareStdout, bareStderr := run()
	helpStdout, helpStderr := run("--help")
	for _, want := range []string{
		"Traceary records and inspects local AI-agent work history",
		"Available Commands:",
		"traceary list",
	} {
		if !strings.Contains(bareStdout, want) {
			t.Errorf("bare stdout missing %q:\n%s", want, bareStdout)
		}
		if !strings.Contains(helpStdout, want) {
			t.Errorf("help stdout missing %q:\n%s", want, helpStdout)
		}
	}
	if strings.Contains(bareStdout, "DEPRECATED:") || strings.Contains(helpStdout, "DEPRECATED:") {
		t.Errorf("help output leaked a deprecation notice")
	}
	if diff := cmp.Diff(helpStderr, bareStderr); diff != "" {
		t.Errorf("bare stderr differs from help (-help +bare):\n%s", diff)
	}
	if strings.Contains(bareStderr, "DEPRECATED:") {
		t.Errorf("bare emitted a deprecation notice: %q", bareStderr)
	}
}

func TestRootCLI_BareTTYAlsoShowsHelp(t *testing.T) {
	t.Parallel()

	stdin := createTempFile(t)
	stdoutFile := createTempFile(t)
	stderr := &bytes.Buffer{}
	rootCmd := NewRootCLI().Command()
	rootCmd.SetIn(stdin)
	rootCmd.SetOut(stdoutFile)
	rootCmd.SetErr(stderr)
	rootCmd.SetArgs([]string{})

	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if _, err := stdoutFile.Seek(0, 0); err != nil {
		t.Fatalf("Seek() error = %v", err)
	}
	got, err := os.ReadFile(stdoutFile.Name())
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}
	if !strings.Contains(string(got), "Available Commands:") {
		t.Fatalf("TTY bare stdout = %q, want help", string(got))
	}
	if strings.Contains(stderr.String(), "DEPRECATED:") {
		t.Fatalf("TTY bare emitted deprecation notice: %q", stderr.String())
	}
}

func createTempFile(t *testing.T) *os.File {
	t.Helper()

	file, err := os.CreateTemp(t.TempDir(), "traceary-root-test-*")
	if err != nil {
		t.Fatalf("CreateTemp() error = %v", err)
	}
	t.Cleanup(func() { _ = file.Close() })
	return file
}

func TestRootCLI_DashDashArgsStillFail(t *testing.T) {
	t.Parallel()

	rootCmd := NewRootCLI().Command()
	rootCmd.SetOut(&bytes.Buffer{})
	rootCmd.SetErr(&bytes.Buffer{})
	rootCmd.SetArgs([]string{"--", "extra"})

	err := rootCmd.Execute()
	if err == nil {
		t.Fatal("Execute(-- extra) error = nil, want positional argument error")
	}
	if !strings.Contains(err.Error(), "does not accept positional arguments") {
		t.Fatalf("error = %q, want positional argument error", err.Error())
	}
}

func TestRootCLI_UnknownCommandStillFailsWithSuggestions(t *testing.T) {
	t.Parallel()

	rootCmd := NewRootCLI().Command()
	rootCmd.SetOut(&bytes.Buffer{})
	rootCmd.SetErr(&bytes.Buffer{})
	rootCmd.SetArgs([]string{"taill"})

	err := rootCmd.Execute()
	if err == nil {
		t.Fatal("Execute(taill) error = nil, want unknown-command error")
	}
	for _, want := range []string{
		`unknown command "taill" for "traceary"`,
	} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("error = %q, want %q", err.Error(), want)
		}
	}
}

func TestRootCLI_UnknownCommandWithFlagStillFails(t *testing.T) {
	t.Parallel()

	rootCmd := NewRootCLI().Command()
	rootCmd.SetOut(&bytes.Buffer{})
	rootCmd.SetErr(&bytes.Buffer{})
	rootCmd.SetArgs([]string{"not-a-command", "--version"})

	err := rootCmd.Execute()
	if err == nil {
		t.Fatal("Execute(not-a-command --version) error = nil, want unknown-command error")
	}
	if !strings.Contains(err.Error(), `unknown command "not-a-command" for "traceary"`) {
		t.Fatalf("error = %q, want unknown-command error", err.Error())
	}
}

func TestRootCLI_GroupRejectsUnknownSubcommand(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name string
		args []string
	}{
		{"memory", []string{"memory", "bogus"}},
		{"store", []string{"store", "bogus"}},
		{"session", []string{"session", "bogus"}},
		{"nested memory inbox", []string{"memory", "inbox", "bogus"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			rootCmd := NewRootCLI().Command()
			rootCmd.SetOut(&bytes.Buffer{})
			rootCmd.SetErr(&bytes.Buffer{})
			rootCmd.SetArgs(tc.args)

			err := rootCmd.Execute()
			if err == nil {
				t.Fatalf("Execute(%v) error = nil, want unknown-subcommand error", tc.args)
			}
			if !strings.Contains(err.Error(), "unknown subcommand") || !strings.Contains(err.Error(), `"bogus"`) {
				t.Fatalf("error = %q, want an unknown-subcommand error naming \"bogus\"", err.Error())
			}
		})
	}
}

func TestRootCLI_GroupBareInvocationStillShowsHelp(t *testing.T) {
	t.Parallel()

	rootCmd := NewRootCLI().Command()
	var out bytes.Buffer
	rootCmd.SetOut(&out)
	rootCmd.SetErr(&bytes.Buffer{})
	rootCmd.SetArgs([]string{"memory"})

	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("Execute(memory) error = %v, want help output with no error", err)
	}
	if !strings.Contains(out.String(), "Available Commands:") {
		t.Fatalf("bare `memory` did not print help; got:\n%s", out.String())
	}
}

// TestRootCLI_GroupUnknownSubcommandWithHelpFlagStillShowsHelp pins the
// intentional exception: an explicit `--help` / `-h` is always honored, even
// alongside an unrecognized positional, because Cobra processes the help flag
// before the strict RunE. The strict error applies only to non-help
// invocations.
func TestRootCLI_GroupUnknownSubcommandWithHelpFlagStillShowsHelp(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name string
		args []string
	}{
		{"memory bogus --help", []string{"memory", "bogus", "--help"}},
		{"memory inbox bogus -h", []string{"memory", "inbox", "bogus", "-h"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			rootCmd := NewRootCLI().Command()
			var out bytes.Buffer
			rootCmd.SetOut(&out)
			rootCmd.SetErr(&bytes.Buffer{})
			rootCmd.SetArgs(tc.args)

			if err := rootCmd.Execute(); err != nil {
				t.Fatalf("Execute(%v) error = %v, want help output with no error", tc.args, err)
			}
			if !strings.Contains(out.String(), "Available Commands:") && !strings.Contains(out.String(), "Usage:") {
				t.Fatalf("Execute(%v) did not print help; got:\n%s", tc.args, out.String())
			}
		})
	}
}

func TestRootCLI_UnknownCommandHelpStillFails(t *testing.T) {
	t.Parallel()

	rootCmd := NewRootCLI().Command()
	rootCmd.SetOut(&bytes.Buffer{})
	rootCmd.SetErr(&bytes.Buffer{})
	rootCmd.SetArgs([]string{"not-a-command", "--help"})

	err := rootCmd.Execute()
	if err == nil {
		t.Fatal("Execute(not-a-command --help) error = nil, want unknown-command error")
	}
	if !strings.Contains(err.Error(), `unknown command "not-a-command" for "traceary"`) {
		t.Fatalf("error = %q, want unknown-command error", err.Error())
	}
}

func TestRootCLI_NoArgsHelpCanUseJapanese(t *testing.T) {
	t.Setenv("TRACEARY_LANG", "ja")

	stdout := &bytes.Buffer{}
	stderr := &bytes.Buffer{}
	rootCmd := NewRootCLI().Command()
	rootCmd.SetIn(strings.NewReader(""))
	rootCmd.SetOut(stdout)
	rootCmd.SetErr(stderr)
	rootCmd.SetArgs([]string{})

	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	for _, want := range []string{
		"Traceary はローカルの AI agent 作業履歴を記録・確認します",
		"traceary list",
	} {
		if !strings.Contains(stdout.String(), want) {
			t.Fatalf("stdout = %q, want %q", stdout.String(), want)
		}
	}
	if strings.Contains(stdout.String(), "operator cockpit") || strings.Contains(stdout.String(), "Tail-first") {
		t.Fatalf("Japanese help still describes the removed cockpit:\n%s", stdout.String())
	}
	if stderr.String() != "" {
		t.Fatalf("stderr = %q, want empty stderr for plain help", stderr.String())
	}
}

func TestRootCLI_AuditHelpMentionsDefaults(t *testing.T) {
	t.Parallel()

	stdout := &bytes.Buffer{}
	rootCmd := NewRootCLI().Command()
	rootCmd.SetOut(stdout)
	rootCmd.SetErr(&bytes.Buffer{})
	rootCmd.SetArgs([]string{"audit", "--help"})

	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if !strings.Contains(stdout.String(), "TRACEARY_SESSION_ID") {
		t.Fatalf("stdout = %q, want TRACEARY_SESSION_ID", stdout.String())
	}
	if !strings.Contains(stdout.String(), "TRACEARY_ALLOW_SECRETS") {
		t.Fatalf("stdout = %q, want TRACEARY_ALLOW_SECRETS", stdout.String())
	}
}

func TestRootCLI_SessionHelpOmitsRemovedLookupLeaves(t *testing.T) {
	t.Parallel()

	var sessionCmd *cobra.Command
	for _, cmd := range NewRootCLI().Command().Commands() {
		if cmd.Name() == "session" {
			sessionCmd = cmd
			break
		}
	}
	if sessionCmd == nil {
		t.Fatal("session command is missing")
	}
	got := map[string]struct{}{}
	for _, cmd := range sessionCmd.Commands() {
		got[cmd.Name()] = struct{}{}
	}
	for _, keep := range []string{"start", "end", "run", "refine", "gc", "repair-one-shot"} {
		if _, ok := got[keep]; !ok {
			t.Fatalf("missing kept session leaf %q: %#v", keep, got)
		}
	}
	for _, retired := range []string{"latest", "list", "handoff"} {
		if _, ok := got[retired]; ok {
			t.Fatalf("retired session leaf %q is still registered: %#v", retired, got)
		}
	}
}
