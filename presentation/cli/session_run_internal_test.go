package cli

import (
	"bytes"
	"context"
	"errors"
	"testing"

	"github.com/duck8823/traceary/domain/types"
)

type alwaysFailWriter struct{}

func (alwaysFailWriter) Write([]byte) (int, error) {
	return 0, errors.New("simulated broken output stream")
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
