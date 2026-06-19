package model_test

import (
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/duck8823/traceary/domain/model"
	"github.com/duck8823/traceary/domain/types"
)

func TestNewCommandAudit(t *testing.T) {
	t.Parallel()

	eventID, err := types.EventIDFrom("event-1")
	if err != nil {
		t.Fatalf("EventIDFrom() error = %v", err)
	}

	t.Run("creates command audit successfully", func(t *testing.T) {
		t.Parallel()

		got, err := model.NewCommandAudit(
			eventID,
			"  go test ./...  ",
			"stdin",
			"stdout",
			true,
			false,
		)
		if err != nil {
			t.Fatalf("NewCommandAudit() error = %v", err)
		}
		if diff := cmp.Diff("go test ./...", got.Command()); diff != "" {
			t.Fatalf("Command() mismatch (-want +got):\n%s", diff)
		}
		if !got.InputTruncated() {
			t.Fatalf("InputTruncated() = false, want true")
		}
		if got.OutputTruncated() {
			t.Fatalf("OutputTruncated() = true, want false")
		}
		got.SetRedaction(true, false)
		if !got.InputRedacted() {
			t.Fatalf("InputRedacted() = false, want true")
		}
		if got.OutputRedacted() {
			t.Fatalf("OutputRedacted() = true, want false")
		}
	})

	t.Run("restores original byte metadata from truncation marker", func(t *testing.T) {
		t.Parallel()

		got, err := model.NewCommandAudit(
			eventID,
			"go test ./...",
			"head\n...[truncated original_bytes=12345]...\ntail",
			"stdout",
			true,
			false,
		)
		if err != nil {
			t.Fatalf("NewCommandAudit() error = %v", err)
		}
		if got.InputOriginalBytes() != 12345 {
			t.Fatalf("InputOriginalBytes() = %d, want 12345", got.InputOriginalBytes())
		}
	})

	t.Run("ignores original byte text outside truncation marker", func(t *testing.T) {
		t.Parallel()

		got, err := model.NewCommandAudit(
			eventID,
			"go test ./...",
			"user text ...[truncated original_bytes=999]...\n...[truncated original_bytes=12345]...\ntail",
			"stdout",
			true,
			false,
		)
		if err != nil {
			t.Fatalf("NewCommandAudit() error = %v", err)
		}
		if got.InputOriginalBytes() != 12345 {
			t.Fatalf("InputOriginalBytes() = %d, want 12345", got.InputOriginalBytes())
		}
	})

	t.Run("ignores original byte metadata for untruncated payloads", func(t *testing.T) {
		t.Parallel()

		got, err := model.NewCommandAudit(
			eventID,
			"go test ./...",
			"stdin",
			"stdout",
			false,
			false,
		)
		if err != nil {
			t.Fatalf("NewCommandAudit() error = %v", err)
		}
		got.SetOriginalPayloadBytes(5, 6)
		if got.InputOriginalBytes() != 0 || got.OutputOriginalBytes() != 0 {
			t.Fatalf("original bytes = (%d, %d), want zero", got.InputOriginalBytes(), got.OutputOriginalBytes())
		}
	})

	t.Run("returns error for empty command", func(t *testing.T) {
		t.Parallel()

		_, err := model.NewCommandAudit(eventID, "   ", "", "", false, false)
		if err == nil {
			t.Fatalf("NewCommandAudit() error = nil, want error")
		}
	})
}

func TestCommandAuditOf(t *testing.T) {
	t.Parallel()

	eventID, _ := types.EventIDFrom("event-2")

	audit := model.CommandAuditOf(eventID, "go build", "input-data", "output-data", false, true, types.None[int](), false)

	if diff := cmp.Diff(eventID, audit.EventID()); diff != "" {
		t.Errorf("EventID() mismatch (-want +got):\n%s", diff)
	}
	if diff := cmp.Diff("go build", audit.Command()); diff != "" {
		t.Errorf("Command() mismatch (-want +got):\n%s", diff)
	}
	if diff := cmp.Diff("input-data", audit.Input()); diff != "" {
		t.Errorf("Input() mismatch (-want +got):\n%s", diff)
	}
	if diff := cmp.Diff("output-data", audit.Output()); diff != "" {
		t.Errorf("Output() mismatch (-want +got):\n%s", diff)
	}
	if audit.InputTruncated() {
		t.Errorf("InputTruncated() = true, want false")
	}
	if !audit.OutputTruncated() {
		t.Errorf("OutputTruncated() = false, want true")
	}
}
