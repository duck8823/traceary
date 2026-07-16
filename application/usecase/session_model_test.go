package usecase_test

import (
	"context"
	"testing"

	"github.com/duck8823/traceary/application/usecase"
	"github.com/duck8823/traceary/domain/model"
	"github.com/duck8823/traceary/domain/types"
)

func TestSessionUsecase_SetModelIfEmpty(t *testing.T) {
	t.Parallel()

	t.Run("stores host-reported model when empty", func(t *testing.T) {
		t.Parallel()
		session := model.NewSession(types.SessionID("session-1"), mustTime(t), types.Client("hook"), types.Agent("codex"), types.Workspace("ws"))
		stub := &sessionRepositoryStub{session: session}
		sut := usecase.NewSessionUsecase(nil, stub, nil, nil)
		updated, err := sut.SetModelIfEmpty(context.Background(), types.SessionID("session-1"), "gpt-5.6")
		if err != nil {
			t.Fatalf("SetModelIfEmpty error = %v", err)
		}
		if !updated {
			t.Fatal("expected update")
		}
		if session.Model() != "gpt-5.6" {
			t.Fatalf("model = %q, want gpt-5.6", session.Model())
		}
	})

	t.Run("empty model is no-op", func(t *testing.T) {
		t.Parallel()
		session := model.NewSession(types.SessionID("session-1"), mustTime(t), types.Client("hook"), types.Agent("codex"), types.Workspace("ws"))
		stub := &sessionRepositoryStub{session: session}
		sut := usecase.NewSessionUsecase(nil, stub, nil, nil)
		updated, err := sut.SetModelIfEmpty(context.Background(), types.SessionID("session-1"), "  ")
		if err != nil {
			t.Fatalf("SetModelIfEmpty error = %v", err)
		}
		if updated {
			t.Fatal("empty model should not update")
		}
	})

	t.Run("does not overwrite existing model", func(t *testing.T) {
		t.Parallel()
		session := model.NewSession(types.SessionID("session-1"), mustTime(t), types.Client("hook"), types.Agent("codex"), types.Workspace("ws"))
		session.SetModel("claude-sonnet-5")
		stub := &sessionRepositoryStub{session: session}
		sut := usecase.NewSessionUsecase(nil, stub, nil, nil)
		updated, err := sut.SetModelIfEmpty(context.Background(), types.SessionID("session-1"), "gpt-5.6")
		if err != nil {
			t.Fatalf("SetModelIfEmpty error = %v", err)
		}
		if updated {
			t.Fatal("should not overwrite existing model")
		}
		if session.Model() != "claude-sonnet-5" {
			t.Fatalf("model = %q, want claude-sonnet-5", session.Model())
		}
	})
}
