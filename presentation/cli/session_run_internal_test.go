package cli

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"strconv"
	"testing"
	"time"

	"golang.org/x/xerrors"

	"github.com/duck8823/traceary/domain/types"
)

type alwaysFailWriter struct{}

func (alwaysFailWriter) Write([]byte) (int, error) {
	return 0, errors.New("simulated broken output stream")
}

func TestRunOneShotProcess_TimeoutKillsProcessGroupPromptly(t *testing.T) {
	startedAt := time.Now()
	reason, exitCode, err := runOneShotProcess(
		context.Background(), bytes.NewReader(nil), &bytes.Buffer{}, &bytes.Buffer{},
		[]string{"sh", "-c", "(sleep 10) & wait"}, 20*time.Millisecond,
		oneShotProcessEnvironment("/tmp/test.db", "session", "", "", "", "", ""),
	)
	if err == nil || reason != types.TerminalReasonTimeout || exitCode != oneShotTimeoutExitCode {
		t.Fatalf("runOneShotProcess() = (%q, %d, %v), want timeout/%d/error", reason, exitCode, err, oneShotTimeoutExitCode)
	}
	if elapsed := time.Since(startedAt); elapsed > time.Second {
		t.Fatalf("timeout process group took %s, want <= 1s", elapsed)
	}
}

func TestRunOneShotProcess_ClassifiesAbortedStream(t *testing.T) {
	reason, exitCode, err := runOneShotProcess(
		context.Background(),
		bytes.NewReader(nil),
		alwaysFailWriter{},
		&bytes.Buffer{},
		[]string{"sh", "-c", "printf output"},
		0,
		oneShotProcessEnvironment("/tmp/test.db", "session", "", "", "", "", ""),
	)
	if err == nil {
		t.Fatal("runOneShotProcess() error = nil, want stream error")
	}
	if reason != types.TerminalReasonAbortedStream || exitCode != oneShotStreamExitCode {
		t.Fatalf("runOneShotProcess() = (%q, %d), want (aborted_stream, %d)", reason, exitCode, oneShotStreamExitCode)
	}
}

func TestRunOneShotProcess_ClassifiesStartFailure(t *testing.T) {
	reason, exitCode, err := runOneShotProcess(
		context.Background(), bytes.NewReader(nil), &bytes.Buffer{}, &bytes.Buffer{},
		[]string{"/path/that/does/not/exist"}, 0,
		oneShotProcessEnvironment("/tmp/test.db", "session", "", "", "", "", ""),
	)
	if err == nil {
		t.Fatal("runOneShotProcess() error = nil, want start error")
	}
	if reason != types.TerminalReasonFailure || exitCode != oneShotStartExitCode {
		t.Fatalf("runOneShotProcess() = (%q, %d), want (failure, %d)", reason, exitCode, oneShotStartExitCode)
	}
}

func TestClassifyOneShotOutcome(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		ctxErr   error
		runErr   error
		reason   types.TerminalReason
		exitCode int
		wantErr  bool
	}{
		{name: "clean exit", reason: types.TerminalReasonSuccess, exitCode: 0},
		{
			name:   "clean exit wins over expired deadline",
			ctxErr: context.DeadlineExceeded,
			reason: types.TerminalReasonSuccess, exitCode: 0,
		},
		{
			name:   "clean exit wins over canceled context",
			ctxErr: context.Canceled,
			reason: types.TerminalReasonSuccess, exitCode: 0,
		},
		{
			name:   "deadline classifies unfinished process as timeout",
			ctxErr: context.DeadlineExceeded,
			runErr: errors.New("signal: killed"),
			reason: types.TerminalReasonTimeout, exitCode: oneShotTimeoutExitCode, wantErr: true,
		},
		{
			name:   "canceled context classifies unfinished process as aborted stream",
			ctxErr: context.Canceled,
			runErr: errors.New("signal: killed"),
			reason: types.TerminalReasonAbortedStream, exitCode: oneShotStreamExitCode, wantErr: true,
		},
		{
			name:   "start failure",
			runErr: &os.PathError{Op: "fork/exec", Path: "/missing", Err: errors.New("no such file or directory")},
			reason: types.TerminalReasonFailure, exitCode: oneShotStartExitCode, wantErr: true,
		},
		{
			name:   "stream error",
			runErr: errors.New("broken pipe"),
			reason: types.TerminalReasonAbortedStream, exitCode: oneShotStreamExitCode, wantErr: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			reason, exitCode, err := classifyOneShotOutcome(tt.ctxErr, tt.runErr)
			if reason != tt.reason || exitCode != tt.exitCode {
				t.Fatalf("classifyOneShotOutcome() = (%q, %d), want (%q, %d)", reason, exitCode, tt.reason, tt.exitCode)
			}
			if (err != nil) != tt.wantErr {
				t.Fatalf("classifyOneShotOutcome() error = %v, wantErr %t", err, tt.wantErr)
			}
		})
	}
}

// TestOneShotHelperProcess is not a test: it runs as the child of
// oneShotTestExitError and exits with the requested code so tests can
// classify a real *exec.ExitError on any platform.
func TestOneShotHelperProcess(_ *testing.T) {
	code, err := strconv.Atoi(os.Getenv("TRACEARY_TEST_ONESHOT_HELPER_EXIT"))
	if err != nil {
		return
	}
	os.Exit(code)
}

// oneShotTestExitError re-executes the test binary as a failing helper
// process, producing a real *exec.ExitError with the given exit code.
func oneShotTestExitError(t *testing.T, exitCode int) error {
	t.Helper()
	cmd := exec.Command(os.Args[0], "-test.run=^TestOneShotHelperProcess$")
	cmd.Env = append(os.Environ(), "TRACEARY_TEST_ONESHOT_HELPER_EXIT="+strconv.Itoa(exitCode))
	err := cmd.Run()
	if err == nil {
		t.Fatalf("helper process error = nil, want exit code %d", exitCode)
	}
	var exitErr *exec.ExitError
	if !errors.As(err, &exitErr) {
		t.Fatalf("helper process error = %T, want *exec.ExitError", err)
	}
	return xerrors.Errorf("test command failed: %w", err)
}

func TestIsClaudeHeadlessUsageCommand_RequiresPrintAndJSONOutput(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		command []string
		want    bool
	}{
		{name: "stream JSON", command: []string{"claude", "-p", "--output-format", "stream-json"}, want: true},
		{name: "single JSON", command: []string{"/usr/local/bin/claude", "--print", "--output-format=json"}, want: true},
		{name: "plain print", command: []string{"claude", "-p"}, want: false},
		{name: "interactive JSON option", command: []string{"claude", "--output-format", "json"}, want: false},
		{name: "prompt option after separator", command: []string{"claude", "-p", "--", "--output-format=json"}, want: false},
		{name: "JSON option before separator", command: []string{"claude", "-p", "--output-format=json", "--", "prompt"}, want: true},
		{name: "other host", command: []string{"codex", "-p", "--output-format", "stream-json"}, want: false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			if got := isClaudeHeadlessUsageCommand(test.command); got != test.want {
				t.Fatalf("isClaudeHeadlessUsageCommand(%q) = %t, want %t", test.command, got, test.want)
			}
		})
	}
}

func TestIsGrokHeadlessUsageCommand_RequiresSingleAndStreamingJSON(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		command []string
		want    bool
	}{
		{name: "short single", command: []string{"grok", "-p", "prompt", "--output-format", "streaming-json"}, want: true},
		{name: "live flags prompt last", command: []string{"grok", "--cwd", "/private/tmp", "--permission-mode", "plan", "--no-memory", "--no-subagents", "--disable-web-search", "--output-format", "streaming-json", "-p", "prompt"}, want: true},
		{name: "live flags prompt first", command: []string{"grok", "-p", "prompt", "--output-format", "streaming-json", "--cwd", "/private/tmp", "--permission-mode", "plan", "--no-memory", "--no-subagents", "--disable-web-search"}, want: true},
		{name: "attached short single", command: []string{"grok", "-pprompt", "--output-format", "streaming-json"}, want: true},
		{name: "long single", command: []string{"/usr/local/bin/grok", "--single=prompt", "--output-format=streaming-json"}, want: true},
		{name: "prompt file", command: []string{"grok", "--prompt-file", "prompt.md", "--output-format=streaming-json"}, want: true},
		{name: "prompt JSON", command: []string{"grok", "--prompt-json={\"type\":\"text\",\"text\":\"prompt\"}", "--output-format=streaming-json"}, want: true},
		{name: "plain single", command: []string{"grok", "-p", "prompt"}, want: false},
		{name: "interactive stream", command: []string{"grok", "--output-format", "streaming-json"}, want: false},
		{name: "unrelated attached short option", command: []string{"grok", "-xprompt", "--output-format", "streaming-json"}, want: false},
		{name: "option after separator", command: []string{"grok", "-p", "prompt", "--", "--output-format=streaming-json"}, want: false},
		{name: "attached prompt after separator", command: []string{"grok", "--output-format=streaming-json", "--", "-pprompt"}, want: false},
		{name: "other host", command: []string{"gemini", "-p", "prompt", "--output-format", "streaming-json"}, want: false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			if got := isGrokHeadlessUsageCommand(test.command); got != test.want {
				t.Fatalf("isGrokHeadlessUsageCommand(%q) = %t, want %t", test.command, got, test.want)
			}
		})
	}
}

func TestIsGeminiHeadlessUsageCommand_RequiresPromptAndStreamJSON(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		command []string
		want    bool
	}{
		{name: "separate options", command: []string{"gemini", "-p", "prompt", "--output-format", "stream-json"}, want: true},
		{name: "equals options", command: []string{"/usr/local/bin/gemini", "--prompt=prompt", "--output-format=stream-json"}, want: true},
		{name: "plain prompt", command: []string{"gemini", "-p", "prompt"}, want: false},
		{name: "interactive stream", command: []string{"gemini", "--output-format", "stream-json"}, want: false},
		{name: "options after separator", command: []string{"gemini", "--", "-p", "prompt", "--output-format=stream-json"}, want: false},
		{name: "stream before separator", command: []string{"gemini", "--prompt", "prompt", "--output-format=stream-json", "--", "argument"}, want: true},
		{name: "missing prompt value", command: []string{"gemini", "--prompt=", "--output-format=stream-json"}, want: false},
		{name: "other host", command: []string{"claude", "-p", "prompt", "--output-format", "stream-json"}, want: false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			if got := isGeminiHeadlessUsageCommand(test.command); got != test.want {
				t.Fatalf("isGeminiHeadlessUsageCommand(%q) = %t, want %t", test.command, got, test.want)
			}
		})
	}
}

func TestIsCodexHeadlessUsageCommand_RecognizesOnlyExecOptionsBeforeDelimiter(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		command []string
		want    bool
	}{
		{name: "exec json", command: []string{"codex", "exec", "--json"}, want: true},
		{name: "absolute executable", command: []string{"/usr/local/bin/codex", "exec", "--json", "prompt"}, want: true},
		{name: "exec json before delimiter", command: []string{"codex", "exec", "--json", "--", "prompt"}, want: true},
		{name: "json after delimiter", command: []string{"codex", "exec", "--", "--json"}, want: false},
		{name: "different subcommand", command: []string{"codex", "app-server", "--json"}, want: false},
		{name: "missing json", command: []string{"codex", "exec", "--full-auto"}, want: false},
		{name: "json without exec", command: []string{"codex", "--json"}, want: false},
		{name: "different executable", command: []string{"codex-wrapper", "exec", "--json"}, want: false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			if got := isCodexHeadlessUsageCommand(test.command); got != test.want {
				t.Fatalf("isCodexHeadlessUsageCommand(%q) = %t, want %t", test.command, got, test.want)
			}
		})
	}
}
