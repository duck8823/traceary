package types_test

import (
	"testing"

	"github.com/duck8823/traceary/domain/types"
)

func TestCommandFailureReasonFrom(t *testing.T) {
	t.Parallel()
	for _, value := range types.KnownCommandFailureReasonStrings() {
		value := value
		t.Run(value, func(t *testing.T) {
			t.Parallel()
			got, err := types.CommandFailureReasonFrom(value)
			if err != nil {
				t.Fatalf("CommandFailureReasonFrom() error = %v", err)
			}
			if got.String() != value {
				t.Fatalf("CommandFailureReasonFrom() = %q, want %q", got, value)
			}
		})
	}
	if _, err := types.CommandFailureReasonFrom("quoted_failure_text"); err == nil {
		t.Fatal("CommandFailureReasonFrom(quoted_failure_text) error = nil, want error")
	}
}
