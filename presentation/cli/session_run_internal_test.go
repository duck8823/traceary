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
		oneShotProcessEnvironment("/tmp/test.db", "session", ""),
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
		oneShotProcessEnvironment("/tmp/test.db", "session", ""),
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
		oneShotProcessEnvironment("/tmp/test.db", "session", ""),
	)
	if err == nil {
		t.Fatal("runOneShotProcess() error = nil, want start error")
	}
	if reason != types.TerminalReasonFailure || exitCode != oneShotStartExitCode {
		t.Fatalf("runOneShotProcess() = (%q, %d), want (failure, %d)", reason, exitCode, oneShotStartExitCode)
	}
}
