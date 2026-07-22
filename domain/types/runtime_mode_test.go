package types_test

import (
	"testing"

	"github.com/duck8823/traceary/domain/types"
)

func TestRuntimeModeFrom(t *testing.T) {
	t.Parallel()

	for _, want := range []types.RuntimeMode{
		types.RuntimeModeInteractive,
		types.RuntimeModeOneShot,
		types.RuntimeModeResumed,
		types.RuntimeModeBackground,
	} {
		want := want
		t.Run(want.String(), func(t *testing.T) {
			t.Parallel()
			got, err := types.RuntimeModeFrom(want.String())
			if err != nil {
				t.Fatalf("RuntimeModeFrom(%q) error = %v", want, err)
			}
			if got != want {
				t.Fatalf("RuntimeModeFrom(%q) = %q, want %q", want, got, want)
			}
		})
	}

	for _, invalid := range []string{"", "one-shot", "unknown"} {
		invalid := invalid
		t.Run("reject_"+invalid, func(t *testing.T) {
			t.Parallel()
			if _, err := types.RuntimeModeFrom(invalid); err == nil {
				t.Fatalf("RuntimeModeFrom(%q) error = nil, want validation error", invalid)
			}
		})
	}
}
