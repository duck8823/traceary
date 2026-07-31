//go:build !aix && !darwin && !dragonfly && !freebsd && !linux && !netbsd && !openbsd && !solaris

package cli

import (
	"context"
	"testing"

	"github.com/duck8823/traceary/domain/types"
)

// On platforms without wait-status signal inspection a supervisor-initiated
// kill is indistinguishable from a child-owned exit (see oneShotOwnExitCode),
// so an expired or canceled context keeps classification priority. A plain
// non-zero exit without a context error still records the child's failure.
func TestClassifyOneShotOutcome_ContextKeepsPriorityOverExitError(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		ctxErr   error
		runErr   error
		reason   types.TerminalReason
		exitCode int
	}{
		{
			name:   "expired deadline outranks ambiguous exit error",
			ctxErr: context.DeadlineExceeded,
			runErr: oneShotTestExitError(t, 7),
			reason: types.TerminalReasonTimeout, exitCode: oneShotTimeoutExitCode,
		},
		{
			name:   "canceled context outranks ambiguous exit error",
			ctxErr: context.Canceled,
			runErr: oneShotTestExitError(t, 7),
			reason: types.TerminalReasonAbortedStream, exitCode: oneShotStreamExitCode,
		},
		{
			name:   "plain failure without context error",
			runErr: oneShotTestExitError(t, 7),
			reason: types.TerminalReasonFailure, exitCode: 7,
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
