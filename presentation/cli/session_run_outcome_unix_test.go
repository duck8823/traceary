//go:build aix || darwin || dragonfly || freebsd || linux || netbsd || openbsd || solaris

package cli

import (
	"context"
	"errors"
	"os/exec"
	"testing"

	"golang.org/x/xerrors"

	"github.com/duck8823/traceary/domain/types"
)

func TestClassifyOneShotOutcome_ChildOwnedExitWinsOverContext(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		ctxErr   error
		runErr   error
		reason   types.TerminalReason
		exitCode int
	}{
		{
			name:   "child failure wins over expired deadline",
			ctxErr: context.DeadlineExceeded,
			runErr: oneShotTestExitError(t, 7),
			reason: types.TerminalReasonFailure, exitCode: 7,
		},
		{
			name:   "child failure wins over canceled context",
			ctxErr: context.Canceled,
			runErr: oneShotTestExitError(t, 3),
			reason: types.TerminalReasonFailure, exitCode: 3,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			reason, exitCode, err := classifyOneShotOutcome(tt.ctxErr, tt.runErr)
			if reason != tt.reason || exitCode != tt.exitCode || err == nil {
				t.Fatalf("classifyOneShotOutcome() = (%q, %d, %v), want (%q, %d, error)", reason, exitCode, err, tt.reason, tt.exitCode)
			}
		})
	}
}

func TestClassifyOneShotOutcome_SignalClassification(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		ctxErr   error
		runErr   error
		reason   types.TerminalReason
		exitCode int
	}{
		{
			name:   "deadline classifies signal-killed process as timeout",
			ctxErr: context.DeadlineExceeded,
			runErr: oneShotTestShellExitError(t, "kill -KILL $$"),
			reason: types.TerminalReasonTimeout, exitCode: oneShotTimeoutExitCode,
		},
		{
			name:   "canceled context classifies signal-killed process as aborted stream",
			ctxErr: context.Canceled,
			runErr: oneShotTestShellExitError(t, "kill -KILL $$"),
			reason: types.TerminalReasonAbortedStream, exitCode: oneShotStreamExitCode,
		},
		{
			name:   "signal without context error",
			runErr: oneShotTestShellExitError(t, "kill -TERM $$"),
			reason: types.TerminalReasonSignal, exitCode: 143,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			reason, exitCode, err := classifyOneShotOutcome(tt.ctxErr, tt.runErr)
			if reason != tt.reason || exitCode != tt.exitCode || err == nil {
				t.Fatalf("classifyOneShotOutcome() = (%q, %d, %v), want (%q, %d, error)", reason, exitCode, err, tt.reason, tt.exitCode)
			}
		})
	}
}

// oneShotTestShellExitError runs a shell command that fails so signal-based
// classification tests can use a real *exec.ExitError wait status.
func oneShotTestShellExitError(t *testing.T, command string) error {
	t.Helper()
	err := exec.Command("sh", "-c", command).Run()
	if err == nil {
		t.Fatalf("sh -c %q error = nil, want exit error", command)
	}
	var exitErr *exec.ExitError
	if !errors.As(err, &exitErr) {
		t.Fatalf("sh -c %q error = %T, want *exec.ExitError", command, err)
	}
	return xerrors.Errorf("test command failed: %w", err)
}
