package types_test

import (
	"testing"

	"github.com/duck8823/traceary/domain/types"
)

func TestTerminalReasonFrom(t *testing.T) {
	t.Parallel()

	for _, want := range []types.TerminalReason{
		types.TerminalReasonSuccess,
		types.TerminalReasonFailure,
		types.TerminalReasonTimeout,
		types.TerminalReasonSignal,
		types.TerminalReasonAbortedStream,
		types.TerminalReasonLegacyUnknown,
	} {
		want := want
		t.Run(want.String(), func(t *testing.T) {
			t.Parallel()
			got, err := types.TerminalReasonFrom(want.String())
			if err != nil {
				t.Fatalf("TerminalReasonFrom(%q) error = %v", want, err)
			}
			if got != want {
				t.Fatalf("TerminalReasonFrom(%q) = %q, want %q", want, got, want)
			}
		})
	}

	for _, invalid := range []string{"", "aborted", "unknown"} {
		invalid := invalid
		t.Run("reject_"+invalid, func(t *testing.T) {
			t.Parallel()
			if _, err := types.TerminalReasonFrom(invalid); err == nil {
				t.Fatalf("TerminalReasonFrom(%q) error = nil, want validation error", invalid)
			}
		})
	}
}
