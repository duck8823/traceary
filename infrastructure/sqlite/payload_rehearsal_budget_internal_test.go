package sqlite

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	apptypes "github.com/duck8823/traceary/application/types"
	"github.com/duck8823/traceary/application/usecase"
)

func TestFileDigestReturnsCtxErrOnCancelledContext(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "dummy.bin")
	if err := os.WriteFile(path, make([]byte, 1<<20), 0600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := fileDigest(ctx, path)
	if err == nil {
		t.Fatal("expected error from cancelled context, got nil")
	}
	if err != context.Canceled {
		t.Fatalf("expected context.Canceled, got %v", err)
	}
}

func TestFileDigestReturnsCtxErrOnExpiredDeadline(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "dummy.bin")
	if err := os.WriteFile(path, make([]byte, 1<<20), 0600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
	defer cancel()
	_, err := fileDigest(ctx, path)
	if err == nil {
		t.Fatal("expected error from expired deadline, got nil")
	}
	if err != context.DeadlineExceeded {
		t.Fatalf("expected context.DeadlineExceeded, got %v", err)
	}
}

func TestEnsurePhysicalBackupPropagatesCanceledDigest(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	source := filepath.Join(dir, "source.db")
	dest := filepath.Join(dir, "backup.db")
	if err := os.WriteFile(source, make([]byte, 4096), 0o600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := ensurePhysicalBackupWithHook(ctx, source, dest, nil)
	if err == nil {
		t.Fatal("expected canceled digest to fail")
	}
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled digest classified as %v, want context.Canceled", err)
	}
}

func TestRollbackBudgetExceededAbortsBeforeMutation(t *testing.T) {
	t.Parallel()
	adapter, config, _ := newSwapRehearsalFixture(t)
	ctx := context.Background()

	if _, err := adapter.Run(ctx, config, apptypes.PayloadRehearsalRunCommand{Mode: apptypes.PayloadRehearsalStart}); err != nil {
		t.Fatalf("setup run failed: %v", err)
	}

	targetInfoBefore, err := os.Stat(config.TargetPath)
	if err != nil {
		t.Fatal(err)
	}

	u := usecase.NewPayloadRehearsalUsecase(adapter, adapter, adapter, adapter)
	tinyConfig := config
	tinyConfig.WallTimeLimit = time.Nanosecond

	if _, err = u.Rollback(ctx, tinyConfig); err == nil {
		t.Fatal("expected error from budget-exceeded rollback, got nil")
	}

	targetInfoAfter, err := os.Stat(config.TargetPath)
	if err != nil {
		t.Fatal(err)
	}
	if targetInfoBefore.ModTime() != targetInfoAfter.ModTime() || targetInfoBefore.Size() != targetInfoAfter.Size() {
		t.Fatal("target was mutated despite budget expiry before verification completed")
	}
}
