package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	apptypes "github.com/duck8823/traceary/application/types"
	"golang.org/x/xerrors"
)

func TestSharedLeaseAcquireIsMarkerAwareFailFast(t *testing.T) {
	if runtime.GOOS != "darwin" && runtime.GOOS != "linux" {
		t.Skip("advisory lease unsupported")
	}
	path := filepath.Join(t.TempDir(), "store.db")
	if err := os.WriteFile(path, []byte("store"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := writeCompactPendingMarker(path); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { clearCompactPendingMarker(path) })

	started := time.Now()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	db := openCoordinatedDB(path, sqliteDSN(path))
	defer func() { _ = db.Close() }()
	err := db.PingContext(ctx)
	if elapsed := time.Since(started); elapsed > time.Second {
		t.Fatalf("RW open under marker took %s, want fail-fast <1s", elapsed)
	}
	var pending *apptypes.StoreMaintenancePendingError
	if !errors.As(err, &pending) {
		t.Fatalf("Ping error = %v, want StoreMaintenancePendingError", err)
	}

	t.Run("pause between consult and shared acquire", func(t *testing.T) {
		clearCompactPendingMarker(path)
		entered := make(chan struct{})
		gate := make(chan struct{})
		afterMaintenanceMarkerConsult = func() {
			select {
			case <-entered:
			default:
				close(entered)
			}
			<-gate
		}
		t.Cleanup(func() { afterMaintenanceMarkerConsult = nil })

		errCh := make(chan error, 1)
		go func() {
			paused := openCoordinatedDB(path, sqliteDSN(path))
			defer func() { _ = paused.Close() }()
			pingCtx, pingCancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer pingCancel()
			errCh <- paused.PingContext(pingCtx)
		}()

		<-entered
		if err := writeCompactPendingMarker(path); err != nil {
			t.Fatal(err)
		}
		started := time.Now()
		close(gate)
		err := <-errCh
		if elapsed := time.Since(started); elapsed > time.Second {
			t.Fatalf("paused RW open took %s, want fail-fast", elapsed)
		}
		var pending *apptypes.StoreMaintenancePendingError
		if !errors.As(err, &pending) {
			t.Fatalf("paused Ping error = %v, want StoreMaintenancePendingError", err)
		}
	})

	t.Run("mid-poll re-consult", func(t *testing.T) {
		clearCompactPendingMarker(path)
		lockPath, err := canonicalLeasePath(path)
		if err != nil {
			t.Fatal(err)
		}
		held, err := acquireAdvisoryLease(context.Background(), lockPath, true)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = held.Close() })

		var blocked atomic.Bool
		afterSharedLeaseWouldBlock = func() {
			if blocked.CompareAndSwap(false, true) {
				if writeErr := writeCompactPendingMarker(path); writeErr != nil {
					t.Errorf("write marker mid-poll: %v", writeErr)
				}
			}
		}
		t.Cleanup(func() { afterSharedLeaseWouldBlock = nil })

		started := time.Now()
		pollDB := openCoordinatedDB(path, sqliteDSN(path))
		defer func() { _ = pollDB.Close() }()
		pingCtx, pingCancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer pingCancel()
		err = pollDB.PingContext(pingCtx)
		if elapsed := time.Since(started); elapsed > time.Second {
			t.Fatalf("mid-poll RW open took %s, want in-poll fail-fast", elapsed)
		}
		var pending *apptypes.StoreMaintenancePendingError
		if !errors.As(err, &pending) {
			t.Fatalf("mid-poll Ping error = %v, want StoreMaintenancePendingError", err)
		}
	})
}

func TestReadOnlyOpenersWaitThenReportMaintenance(t *testing.T) {
	if runtime.GOOS != "darwin" && runtime.GOOS != "linux" {
		t.Skip("advisory lease unsupported")
	}
	t.Run("RW fail-fast under live marker", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "rw.db")
		mustSQLiteFile(t, path)
		if err := writeCompactPendingMarker(path); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { clearCompactPendingMarker(path) })
		started := time.Now()
		db := openCoordinatedDB(path, sqliteDSN(path))
		defer func() { _ = db.Close() }()
		err := db.PingContext(context.Background())
		if time.Since(started) > time.Second {
			t.Fatalf("RW opener waited %s", time.Since(started))
		}
		var pending *apptypes.StoreMaintenancePendingError
		if !errors.As(err, &pending) {
			t.Fatalf("RW Ping error = %v, want StoreMaintenancePendingError", err)
		}
	})

	t.Run("RO succeeds when hold is brief", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "brief.db")
		mustSQLiteFile(t, path)
		release, err := (StoreLeaseCoordinator{}).AcquireExclusive(context.Background(), path)
		if err != nil {
			t.Fatal(err)
		}
		errCh := make(chan error, 1)
		go func() {
			db := openCoordinatedReadOnlyDB(path, sqliteReadOnlyDSN(path))
			defer func() { _ = db.Close() }()
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			errCh <- db.PingContext(ctx)
		}()
		release()
		if err := <-errCh; err != nil {
			t.Fatalf("brief-hold RO Ping error = %v", err)
		}
	})

	t.Run("RO timeout names maintenance", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "held.db")
		mustSQLiteFile(t, path)
		if err := writeCompactPendingMarker(path); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { clearCompactPendingMarker(path) })
		lockPath, err := canonicalLeasePath(path)
		if err != nil {
			t.Fatal(err)
		}
		held, err := acquireAdvisoryLease(context.Background(), lockPath, true)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = held.Close() })
		db := openCoordinatedReadOnlyDB(path, sqliteReadOnlyDSN(path))
		defer func() { _ = db.Close() }()
		ctx, cancel := context.WithTimeout(context.Background(), 80*time.Millisecond)
		defer cancel()
		err = db.PingContext(ctx)
		if err == nil {
			t.Fatal("RO Ping succeeded against uninterruptible exclusive hold")
		}
		var pending *apptypes.StoreMaintenancePendingError
		if errors.As(err, &pending) {
			t.Fatal("RO opener must not return the RW fail-fast typed error")
		}
		if !errors.Is(err, context.DeadlineExceeded) && !containsMaintenanceWording(err) {
			t.Fatalf("RO timeout error = %v, want maintenance wording", err)
		}
		if !containsMaintenanceWording(err) {
			t.Fatalf("RO timeout error = %v, want %q", err, storeMaintenanceInProgressMessage)
		}
	})
}

func containsMaintenanceWording(err error) bool {
	return err != nil && strings.Contains(err.Error(), storeMaintenanceInProgressMessage)
}

func mustSQLiteFile(t *testing.T, path string) {
	t.Helper()
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`CREATE TABLE t(x INTEGER)`); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestOpenAtPropagatesMaintenancePendingThroughWrap(t *testing.T) {
	if runtime.GOOS != "darwin" && runtime.GOOS != "linux" {
		t.Skip("advisory lease unsupported")
	}
	path := filepath.Join(t.TempDir(), "store.db")
	if err := os.WriteFile(path, []byte("store"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := writeCompactPendingMarker(path); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { clearCompactPendingMarker(path) })
	db := NewDatabase(path, nil)
	_, err := db.openAt(context.Background(), path)
	wrapped := xerrors.Errorf("failed to ping SQLite DB: %w", err)
	var pending *apptypes.StoreMaintenancePendingError
	if !errors.As(err, &pending) || !errors.As(wrapped, &pending) {
		t.Fatalf("openAt error = %v, want typed maintenance-pending through wrap", err)
	}
}

func TestHoldsExclusiveReflectsProcessMap(t *testing.T) {
	if runtime.GOOS != "darwin" && runtime.GOOS != "linux" {
		t.Skip("advisory lease unsupported")
	}
	path := filepath.Join(t.TempDir(), "store.db")
	if err := os.WriteFile(path, []byte("store"), 0o600); err != nil {
		t.Fatal(err)
	}
	coord := StoreLeaseCoordinator{}
	if coord.HoldsExclusive(path) {
		t.Fatal("HoldsExclusive before acquire")
	}
	release, err := coord.AcquireExclusive(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	if !coord.HoldsExclusive(path) {
		t.Fatal("HoldsExclusive after acquire")
	}
	release()
	if coord.HoldsExclusive(path) {
		t.Fatal("HoldsExclusive after release")
	}
}

func TestMaintenanceMarkerConsultIsDeterministicWithoutSleepInHook(t *testing.T) {
	if runtime.GOOS != "darwin" && runtime.GOOS != "linux" {
		t.Skip("advisory lease unsupported")
	}
	path := filepath.Join(t.TempDir(), "store.db")
	if err := os.WriteFile(path, []byte("store"), 0o600); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	wg.Add(1)
	gate := make(chan struct{})
	afterMaintenanceMarkerConsult = func() {
		wg.Done()
		<-gate
	}
	t.Cleanup(func() { afterMaintenanceMarkerConsult = nil })
	errCh := make(chan error, 1)
	go func() {
		db := openCoordinatedDB(path, sqliteDSN(path))
		defer func() { _ = db.Close() }()
		errCh <- db.PingContext(context.Background())
	}()
	wg.Wait()
	if err := writeCompactPendingMarker(path); err != nil {
		t.Fatal(err)
	}
	close(gate)
	err := <-errCh
	var pending *apptypes.StoreMaintenancePendingError
	if !errors.As(err, &pending) {
		t.Fatalf("error = %v, want StoreMaintenancePendingError", err)
	}
}
