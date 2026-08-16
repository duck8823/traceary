package types_test

import (
	"errors"
	"strings"
	"testing"

	apptypes "github.com/duck8823/traceary/application/types"
)

func TestContentEventDedupeApplyError_ErrorIncludesRunID(t *testing.T) {
	t.Parallel()
	err := &apptypes.ContentEventDedupeApplyError{RunID: "dedupe-abc123", Err: errors.New("boom")}

	if !strings.Contains(err.Error(), "dedupe-abc123") {
		t.Fatalf("Error() = %q, want it to contain the run id", err.Error())
	}
	if !strings.Contains(err.Error(), "boom") {
		t.Fatalf("Error() = %q, want it to contain the underlying cause", err.Error())
	}
}

func TestContentEventDedupeApplyError_UnwrapAndAs(t *testing.T) {
	t.Parallel()
	cause := errors.New("boom")
	wrapped := error(&apptypes.ContentEventDedupeApplyError{RunID: "dedupe-abc123", Err: cause})

	if !errors.Is(wrapped, cause) {
		t.Fatalf("errors.Is() = false, want true via Unwrap()")
	}

	var applyErr *apptypes.ContentEventDedupeApplyError
	if !errors.As(wrapped, &applyErr) {
		t.Fatalf("errors.As() = false, want true")
	}
	if applyErr.RunID != "dedupe-abc123" {
		t.Fatalf("RunID = %q, want dedupe-abc123", applyErr.RunID)
	}
}
