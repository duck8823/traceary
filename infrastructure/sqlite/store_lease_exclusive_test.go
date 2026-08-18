package sqlite

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestAcquireExclusive_SucceedsAfterIdlePoolReleased(t *testing.T) {
	path := filepath.Join(t.TempDir(), "store.db")
	db := openCoordinatedDB(path, sqliteDSN(path))
	defer func() { _ = db.Close() }()
	if err := db.PingContext(context.Background()); err != nil {
		t.Fatal(err)
	}
	db.SetMaxIdleConns(0)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	release, err := (StoreLeaseCoordinator{}).AcquireExclusive(ctx, path)
	if err != nil {
		t.Fatalf("released idle pool must not block exclusive: %v", err)
	}
	release()
}

func TestAcquireExclusive_TimesOutWhileSharedConnHeld(t *testing.T) {
	path := filepath.Join(t.TempDir(), "store.db")
	db := openCoordinatedDB(path, sqliteDSN(path))
	defer func() { _ = db.Close() }()
	conn, err := db.Conn(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.Close() }()
	ctx, cancel := context.WithTimeout(context.Background(), 80*time.Millisecond)
	defer cancel()
	if _, err := (StoreLeaseCoordinator{}).AcquireExclusive(ctx, path); err == nil {
		t.Fatal("exclusive lease acquired while a shared conn was held")
	} else if !strings.Contains(err.Error(), "lsof") || !strings.Contains(err.Error(), storeLeaseSuffix) {
		t.Fatalf("error should name lock path and lsof, got %v", err)
	}
}

func TestCoordinatedOpen_FailsFastWhenThisProcessHoldsExclusive(t *testing.T) {
	path := filepath.Join(t.TempDir(), "store.db")
	release, err := (StoreLeaseCoordinator{}).AcquireExclusive(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	started := time.Now()
	db := openCoordinatedDB(path, sqliteDSN(path))
	defer func() { _ = db.Close() }()
	err = db.PingContext(context.Background())
	if err == nil {
		t.Fatal("coordinated ping succeeded while this process holds exclusive")
	}
	if !strings.Contains(err.Error(), "self-deadlock") || !strings.Contains(err.Error(), storeLeaseSuffix) {
		t.Fatalf("error should name self-deadlock and lock path, got %v", err)
	}
	if time.Since(started) > time.Second {
		t.Fatalf("self-deadlock took %s, want fail-fast", time.Since(started))
	}
}

func TestSharedLease_TimesOutWhileExclusiveHeldOnOtherFd(t *testing.T) {
	path := filepath.Join(t.TempDir(), "store.db")
	lockPath, err := canonicalLeasePath(path)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := acquireAdvisoryLease(context.Background(), lockPath, true)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = raw.Close() }()

	var reports int
	SetSharedLeaseWaitReporter(func(time.Duration, string) { reports++ })
	t.Cleanup(func() { SetSharedLeaseWaitReporter(nil) })
	orig := exclusiveLeaseWaitInterval
	exclusiveLeaseWaitInterval = 15 * time.Millisecond
	t.Cleanup(func() { exclusiveLeaseWaitInterval = orig })

	ctx, cancel := context.WithTimeout(context.Background(), 80*time.Millisecond)
	defer cancel()
	db := openCoordinatedDB(path, sqliteDSN(path))
	defer func() { _ = db.Close() }()
	err = db.PingContext(ctx)
	if err == nil {
		t.Fatal("shared lease acquired while exclusive flock was held")
	}
	if !strings.Contains(err.Error(), "lsof") || !strings.Contains(err.Error(), storeLeaseSuffix) {
		t.Fatalf("error should name lock path and lsof, got %v", err)
	}
	if reports == 0 {
		t.Fatal("expected shared wait reporter while blocked")
	}
}

func TestAcquireExclusive_WaitReporterFires(t *testing.T) {
	orig := exclusiveLeaseWaitInterval
	exclusiveLeaseWaitInterval = 15 * time.Millisecond
	t.Cleanup(func() { exclusiveLeaseWaitInterval = orig })
	var reports int
	SetExclusiveLeaseWaitReporter(func(time.Duration, string) { reports++ })
	t.Cleanup(func() { SetExclusiveLeaseWaitReporter(nil) })

	path := filepath.Join(t.TempDir(), "store.db")
	db := openCoordinatedDB(path, sqliteDSN(path))
	defer func() { _ = db.Close() }()
	conn, err := db.Conn(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.Close() }()
	ctx, cancel := context.WithTimeout(context.Background(), 80*time.Millisecond)
	defer cancel()
	_, _ = (StoreLeaseCoordinator{}).AcquireExclusive(ctx, path)
	if reports == 0 {
		t.Fatal("expected wait reporter while exclusive acquire is blocked")
	}
}
