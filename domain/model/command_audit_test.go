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

	t.Run("コマンド監査情報を生成できる", func(t *testing.T) {
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
	})

	t.Run("空コマンドはエラー", func(t *testing.T) {
		t.Parallel()

		_, err := model.NewCommandAudit(eventID, "   ", "", "", false, false)
		if err == nil {
			t.Fatalf("NewCommandAudit() error = nil, want error")
		}
	})
}
