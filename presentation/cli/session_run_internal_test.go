package cli

import (
	"bytes"
	"context"
	"errors"
	"testing"
	"time"

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
		oneShotProcessEnvironment("/tmp/test.db", "session", "", "", "", ""),
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
		oneShotProcessEnvironment("/tmp/test.db", "session", "", "", "", ""),
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
		oneShotProcessEnvironment("/tmp/test.db", "session", "", "", "", ""),
	)
	if err == nil {
		t.Fatal("runOneShotProcess() error = nil, want start error")
	}
	if reason != types.TerminalReasonFailure || exitCode != oneShotStartExitCode {
		t.Fatalf("runOneShotProcess() = (%q, %d), want (failure, %d)", reason, exitCode, oneShotStartExitCode)
	}
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
