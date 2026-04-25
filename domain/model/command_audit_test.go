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

	audit := model.CommandAuditOf(eventID, "go build", "input-data", "output-data", false, true, types.None[int]())

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
