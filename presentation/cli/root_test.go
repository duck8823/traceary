package cli

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"strings"
	"testing"
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

func TestRootCLI_NoArgsNonInteractiveShowsHelpAndTuiGuidance(t *testing.T) {
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
		"Tail-first operator cockpit",
		"traceary list",
		"traceary sessions --snapshot",
		"traceary doctor --json",
		"Available Commands:",
	} {
		if !strings.Contains(stdout.String(), want) {
			t.Fatalf("stdout = %q, want %q", stdout.String(), want)
		}
	}
	if stderr.String() != "" {
		t.Fatalf("stderr = %q, want empty stderr for plain non-TTY help", stderr.String())
	}
}

func TestRootCLI_BareCockpitFlagsRequireInteractiveTTY(t *testing.T) {
	t.Parallel()

	stdout := &bytes.Buffer{}
	stderr := &bytes.Buffer{}
	rootCmd := NewRootCLI().Command()
	rootCmd.SetIn(strings.NewReader(""))
	rootCmd.SetOut(stdout)
	rootCmd.SetErr(stderr)
	rootCmd.SetArgs([]string{"--db-path", "./traceary.test.db", "--reset-state"})

	err := rootCmd.Execute()
	if err == nil {
		t.Fatal("Execute(--db-path --reset-state) error = nil, want TTY-required error")
	}
	var coder interface{ ExitCode() int }
	if !errors.As(err, &coder) || coder.ExitCode() != cockpitExitCodeNotInteractive {
		t.Fatalf("error = %T %v, want exit code %d", err, err, cockpitExitCodeNotInteractive)
	}
	if !strings.Contains(err.Error(), "Cockpit flags require an interactive TTY") {
		t.Fatalf("error = %q, want TTY-required guidance", err.Error())
	}
	if stderr.String() != "" {
		t.Fatalf("stderr = %q, want cobra-silenced error output", stderr.String())
	}
	if stdout.String() != "" {
		t.Fatalf("stdout = %q, want empty stdout for cockpit flag error", stdout.String())
	}
}

func TestRootCLI_BareInteractiveDispatchesCockpit(t *testing.T) {
	t.Parallel()

	stdin := createTempFile(t)
	stdout := createTempFile(t)
	stderr := &bytes.Buffer{}

	var called bool
	var gotOpts cockpitCommandOptions
	var gotInput io.Reader
	var gotOutput io.Writer
	root := NewRootCLI(withCockpitRuntimeForTest(
		func(*os.File, *os.File) bool { return true },
		func(_ context.Context, input io.Reader, output io.Writer, opts cockpitCommandOptions) error {
			gotInput = input
			gotOutput = output
			called = true
			gotOpts = opts
			return nil
		},
	))
	rootCmd := root.Command()
	rootCmd.SetIn(stdin)
	rootCmd.SetOut(stdout)
	rootCmd.SetErr(stderr)
	rootCmd.SetArgs([]string{"--db-path", "./traceary.test.db", "--reset-state"})

	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if !called {
		t.Fatal("cockpit runner was not called")
	}
	if gotOpts.dbPath != "./traceary.test.db" || !gotOpts.resetState {
		t.Fatalf("cockpit options = %+v, want db path and reset state", gotOpts)
	}
	if gotInput != stdin || gotOutput != stdout {
		t.Fatalf("runner I/O = (%T, %T), want temp stdin/stdout", gotInput, gotOutput)
	}
	if stderr.String() != "" {
		t.Fatalf("stderr = %q, want empty stderr", stderr.String())
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
		"Did you mean this?",
		"tail",
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

func TestRootCLI_NoArgsNonInteractiveGuidanceCanUseJapanese(t *testing.T) {
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
		"Tail-first operator cockpit",
		"traceary list",
	} {
		if !strings.Contains(stdout.String(), want) {
			t.Fatalf("stdout = %q, want %q", stdout.String(), want)
		}
	}
	if stderr.String() != "" {
		t.Fatalf("stderr = %q, want empty stderr for plain non-TTY help", stderr.String())
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

func TestRootCLI_SessionLatestHelpExplainsSemantics(t *testing.T) {
	t.Parallel()

	stdout := &bytes.Buffer{}
	rootCmd := NewRootCLI().Command()
	rootCmd.SetOut(stdout)
	rootCmd.SetErr(&bytes.Buffer{})
	rootCmd.SetArgs([]string{"session", "latest", "--help"})

	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if !strings.Contains(stdout.String(), "most recent lifecycle boundary") {
		t.Fatalf("stdout = %q, want lifecycle boundary explanation", stdout.String())
	}
}
