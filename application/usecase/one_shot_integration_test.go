package usecase_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"testing"

	"github.com/duck8823/traceary/application/usecase"
	"github.com/duck8823/traceary/domain/model"
	"github.com/duck8823/traceary/domain/types"
	sqliteinfra "github.com/duck8823/traceary/infrastructure/sqlite"
)

func TestSessionUsecase_FinalizeOneShot_ConcurrentSameReasonPersistsOneBoundary(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	_, sourceFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller() failed")
	}
	migrations := os.DirFS(filepath.Join(filepath.Dir(sourceFile), "..", "..", "schema", "sqlite", "migrations"))
	database := sqliteinfra.NewDatabase(filepath.Join(t.TempDir(), "traceary.db"), migrations)
	if err := sqliteinfra.NewStoreManagementDatasource(database).Initialize(ctx); err != nil {
		t.Fatalf("Initialize() error = %v", err)
	}
	sessions := sqliteinfra.NewSessionDatasource(database)
	sut := usecase.NewSessionUsecase(nil, sessions, nil, nil)
	if _, err := sut.StartWithRuntimeMode(ctx, "cli", "codex", "concurrent-one-shot", "workspace", "", types.RuntimeModeOneShot); err != nil {
		t.Fatalf("StartWithRuntimeMode() error = %v", err)
	}

	type result struct {
		transition model.SessionTerminalTransition
		err        error
	}
	start := make(chan struct{})
	results := make(chan result, 2)
	var workers sync.WaitGroup
	for range 2 {
		workers.Add(1)
		go func() {
			defer workers.Done()
			<-start
			transition, _, err := sut.FinalizeOneShot(ctx, "cli", "codex", "concurrent-one-shot", "workspace", types.TerminalReasonSuccess, "done")
			results <- result{transition: transition, err: err}
		}()
	}
	close(start)
	workers.Wait()
	close(results)

	applied, alreadyApplied := 0, 0
	for got := range results {
		if got.err != nil {
			t.Fatalf("FinalizeOneShot() error = %v", got.err)
		}
		switch got.transition {
		case model.SessionTerminalTransitionApplied:
			applied++
		case model.SessionTerminalTransitionAlreadyApplied:
			alreadyApplied++
		default:
			t.Fatalf("FinalizeOneShot() transition = %q", got.transition)
		}
	}
	if applied != 1 || alreadyApplied != 1 {
		t.Fatalf("transitions applied/already_applied = %d/%d, want 1/1", applied, alreadyApplied)
	}
	stored, err := sessions.FindByID(ctx, "concurrent-one-shot")
	if err != nil {
		t.Fatalf("FindByID() error = %v", err)
	}
	session, ok := stored.Value()
	if !ok {
		t.Fatal("FindByID() returned no session")
	}
	if reason, ok := session.TerminalReason().Value(); !ok || reason != types.TerminalReasonSuccess {
		t.Fatalf("stored terminal reason = %q/%v, want success/present", reason, ok)
	}
}

func TestSessionUsecase_FinalizeOneShot_ConcurrentDifferentReasonsRejectsLoser(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	_, sourceFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller() failed")
	}
	migrations := os.DirFS(filepath.Join(filepath.Dir(sourceFile), "..", "..", "schema", "sqlite", "migrations"))
	database := sqliteinfra.NewDatabase(filepath.Join(t.TempDir(), "traceary.db"), migrations)
	if err := sqliteinfra.NewStoreManagementDatasource(database).Initialize(ctx); err != nil {
		t.Fatalf("Initialize() error = %v", err)
	}
	sessions := sqliteinfra.NewSessionDatasource(database)
	sut := usecase.NewSessionUsecase(nil, sessions, nil, nil)
	if _, err := sut.StartWithRuntimeMode(ctx, "cli", "codex", "conflicting-one-shot", "workspace", "", types.RuntimeModeOneShot); err != nil {
		t.Fatalf("StartWithRuntimeMode() error = %v", err)
	}

	type result struct {
		transition model.SessionTerminalTransition
		err        error
	}
	start := make(chan struct{})
	results := make(chan result, 2)
	var workers sync.WaitGroup
	for _, reason := range []types.TerminalReason{types.TerminalReasonSuccess, types.TerminalReasonFailure} {
		reason := reason
		workers.Add(1)
		go func() {
			defer workers.Done()
			<-start
			transition, _, err := sut.FinalizeOneShot(ctx, "cli", "codex", "conflicting-one-shot", "workspace", reason, reason.String())
			results <- result{transition: transition, err: err}
		}()
	}
	close(start)
	workers.Wait()
	close(results)

	applied, conflicts := 0, 0
	for got := range results {
		if got.err == nil && got.transition == model.SessionTerminalTransitionApplied {
			applied++
			continue
		}
		if errors.Is(got.err, model.ErrConflictingTerminalState) {
			conflicts++
			continue
		}
		t.Fatalf("FinalizeOneShot() = (%q, %v), want applied or terminal conflict", got.transition, got.err)
	}
	if applied != 1 || conflicts != 1 {
		t.Fatalf("results applied/conflict = %d/%d, want 1/1", applied, conflicts)
	}
}
