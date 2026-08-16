package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	sqliteschema "github.com/duck8823/traceary/schema/sqlite"
)

// newAutocheckpointTestSession creates a minimal walBudgetedMutationSession
// backed by a real WAL-mode SQLite database. The session is valid for the
// guard check so tests can exercise the autocheckpoint seam directly.
func newAutocheckpointTestSession(t *testing.T) walBudgetedMutationSession {
	t.Helper()
	ctx := context.Background()
	dir := t.TempDir()
	path := filepath.Join(dir, "test.db")

	migrations, err := sqliteschema.Migrations()
	if err != nil {
		t.Fatal(err)
	}
	if err = NewStoreManagementDatasource(NewDatabase(path, migrations)).Initialize(ctx); err != nil {
		t.Fatal(err)
	}

	db, err := sql.Open("sqlite", writableRehearsalDSN(path, time.Second))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })

	fingerprint, err := rehearsalSchemaFingerprint(ctx, db)
	if err != nil {
		t.Fatal(err)
	}

	peak := int64(0)
	return walBudgetedMutationSession{
		db:                db,
		path:              path,
		expectedSchemaSHA: fingerprint,
		frameBytes:        4096,
		maximum:           1 << 20, // 1 MiB — well above any test WAL
		peak:              &peak,
		lockLimit:         time.Second,
	}
}

func TestWALBudgetedMutationSessionAutocheckpointSeams(t *testing.T) {
	ctx := context.Background()

	t.Run("happy path one conn", func(t *testing.T) {
		session := newAutocheckpointTestSession(t)
		connCount := 0
		session.acquireConn = func(c context.Context) (*sql.Conn, error) {
			connCount++
			return session.db.Conn(c)
		}
		// readAutocheckpoint left nil → uses real disableRehearsalAutocheckpoint

		err := session.run(ctx, 1, func(_ *sql.Conn) error { return nil })
		if err != nil {
			t.Fatalf("run error: %v", err)
		}
		if connCount != 1 {
			t.Fatalf("conn count = %d, want 1", connCount)
		}
	})

	t.Run("transient read retries on fresh conn (conn=2)", func(t *testing.T) {
		session := newAutocheckpointTestSession(t)
		connCount := 0
		session.acquireConn = func(c context.Context) (*sql.Conn, error) {
			connCount++
			return session.db.Conn(c)
		}
		call := 0
		readErr := errors.New("simulated read error")
		session.readAutocheckpoint = func(_ context.Context, _ *sql.Conn) (int, error) {
			call++
			if call == 1 {
				return 0, &rehearsalAutocheckpointError{Stage: rehearsalAutocheckpointStageRead, Cause: readErr}
			}
			return 0, nil // second call succeeds
		}

		err := session.run(ctx, 1, func(_ *sql.Conn) error { return nil })
		if err != nil {
			t.Fatalf("run error after retry: %v", err)
		}
		if connCount != 2 {
			t.Fatalf("conn count = %d, want 2", connCount)
		}
	})

	t.Run("persistent non-zero aborts with value in message", func(t *testing.T) {
		session := newAutocheckpointTestSession(t)
		const observed = 1000
		session.readAutocheckpoint = func(_ context.Context, _ *sql.Conn) (int, error) {
			return observed, &rehearsalAutocheckpointError{Stage: rehearsalAutocheckpointStageValue, Observed: observed}
		}

		err := session.run(ctx, 1, func(_ *sql.Conn) error { return nil })
		if err == nil {
			t.Fatal("want error, got nil")
		}
		var cpErr *rehearsalAutocheckpointError
		if !errors.As(err, &cpErr) {
			t.Fatalf("want *rehearsalAutocheckpointError, got %T: %v", err, err)
		}
		if cpErr.Stage != rehearsalAutocheckpointStageValue {
			t.Fatalf("stage = %q, want %q", cpErr.Stage, rehearsalAutocheckpointStageValue)
		}
		if !strings.Contains(err.Error(), fmt.Sprint(observed)) {
			t.Fatalf("error message %q does not contain observed value %d", err.Error(), observed)
		}
	})

	t.Run("exec failure twice wraps driver error", func(t *testing.T) {
		session := newAutocheckpointTestSession(t)
		driverErr := errors.New("simulated driver error")
		call := 0
		session.readAutocheckpoint = func(_ context.Context, _ *sql.Conn) (int, error) {
			call++
			return 0, &rehearsalAutocheckpointError{Stage: rehearsalAutocheckpointStageExec, Cause: driverErr}
		}

		err := session.run(ctx, 1, func(_ *sql.Conn) error { return nil })
		if err == nil {
			t.Fatal("want error, got nil")
		}
		if call != 2 {
			t.Fatalf("readAutocheckpoint called %d times, want 2", call)
		}
		// must wrap the original driver error
		if !errors.Is(err, driverErr) {
			t.Fatalf("error does not wrap driver error: %v", err)
		}
		// must be distinguishable from the value case
		var cpErr *rehearsalAutocheckpointError
		if !errors.As(err, &cpErr) {
			t.Fatalf("want *rehearsalAutocheckpointError, got %T: %v", err, err)
		}
		if cpErr.Stage == rehearsalAutocheckpointStageValue {
			t.Fatalf("stage must not be %q for an exec failure", rehearsalAutocheckpointStageValue)
		}
	})
}
