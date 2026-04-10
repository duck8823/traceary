package model_test

import (
	"testing"

	"github.com/duck8823/traceary/domain/model"
	"github.com/duck8823/traceary/domain/types"
)

func TestNewCommandAudit(t *testing.T) {
	t.Parallel()

	eventID, err := types.EventIDOf("event-1")
	if err != nil {
		t.Fatalf("EventIDOf() error = %v", err)
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
		if got.Command() != "go test ./..." {
			t.Fatalf("Command() = %q, want %q", got.Command(), "go test ./...")
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

	eventID, _ := types.EventIDOf("event-2")

	audit := model.CommandAuditOf(eventID, "go build", "input-data", "output-data", false, true, nil)

	if audit.EventID() != eventID {
		t.Errorf("EventID() = %v, want %v", audit.EventID(), eventID)
	}
	if audit.Command() != "go build" {
		t.Errorf("Command() = %q, want %q", audit.Command(), "go build")
	}
	if audit.Input() != "input-data" {
		t.Errorf("Input() = %q, want %q", audit.Input(), "input-data")
	}
	if audit.Output() != "output-data" {
		t.Errorf("Output() = %q, want %q", audit.Output(), "output-data")
	}
	if audit.InputTruncated() {
		t.Errorf("InputTruncated() = true, want false")
	}
	if !audit.OutputTruncated() {
		t.Errorf("OutputTruncated() = false, want true")
	}
}
