package usecase_test

import (
	"context"
	"strings"
	"testing"

	"github.com/duck8823/traceary/application/usecase"
	"github.com/duck8823/traceary/domain/types"
)

func TestRecordSessionBoundaryUsecase_Run(t *testing.T) {
	t.Parallel()

	t.Run("session start で session ID を生成して保存できる", func(t *testing.T) {
		t.Parallel()

		stub := &eventRepositoryStub{}
		sut := usecase.NewRecordSessionBoundaryUsecase(stub)

		got, err := sut.Run(context.Background(), usecase.RecordSessionBoundaryInput{
			DBPath: "/tmp/traceary.db",
			Client: "cli",
			Agent:  "codex",
			Repo:   "duck8823/traceary",
			Kind:   types.EventKindSessionStarted,
		})
		if err != nil {
			t.Fatalf("Run() error = %v", err)
		}
		if got == nil {
			t.Fatalf("Run() event is nil")
		}
		if !strings.HasPrefix(got.SessionID().String(), "session-") {
			t.Fatalf("SessionID() = %q, want prefix %q", got.SessionID(), "session-")
		}
		if got.Kind() != types.EventKindSessionStarted {
			t.Fatalf("Kind() = %q, want %q", got.Kind(), types.EventKindSessionStarted)
		}
		if got.Body() != "session started" {
			t.Fatalf("Body() = %q, want %q", got.Body(), "session started")
		}
	})

	t.Run("session end は session ID 未指定だとエラー", func(t *testing.T) {
		t.Parallel()

		stub := &eventRepositoryStub{}
		sut := usecase.NewRecordSessionBoundaryUsecase(stub)

		_, err := sut.Run(context.Background(), usecase.RecordSessionBoundaryInput{
			DBPath: "/tmp/traceary.db",
			Agent:  "codex",
			Kind:   types.EventKindSessionEnded,
		})
		if err == nil {
			t.Fatalf("Run() error = nil, want error")
		}
	})

	t.Run("session end を保存できる", func(t *testing.T) {
		t.Parallel()

		stub := &eventRepositoryStub{}
		sut := usecase.NewRecordSessionBoundaryUsecase(stub)

		got, err := sut.Run(context.Background(), usecase.RecordSessionBoundaryInput{
			DBPath:    "/tmp/traceary.db",
			Client:    "cli",
			Agent:     "codex",
			SessionID: "session-1",
			Repo:      "duck8823/traceary",
			Kind:      types.EventKindSessionEnded,
		})
		if err != nil {
			t.Fatalf("Run() error = %v", err)
		}
		if got.Kind() != types.EventKindSessionEnded {
			t.Fatalf("Kind() = %q, want %q", got.Kind(), types.EventKindSessionEnded)
		}
		if got.SessionID().String() != "session-1" {
			t.Fatalf("SessionID() = %q, want %q", got.SessionID(), "session-1")
		}
		if got.Body() != "session ended" {
			t.Fatalf("Body() = %q, want %q", got.Body(), "session ended")
		}
	})
}
