package sqlite

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestAcquireExclusive_SucceedsAgainstIdleCoordinatedPool(t *testing.T) {
	path := filepath.Join(t.TempDir(), "store.db")
	db := openCoordinatedDB(path, sqliteDSN(path))
	defer func() { _ = db.Close() }()
	if err := db.PingContext(context.Background()); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	release, err := (StoreLeaseCoordinator{}).AcquireExclusive(ctx, path)
	if err != nil {
		t.Fatalf("idle coordinated pool must not block exclusive: %v", err)
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
