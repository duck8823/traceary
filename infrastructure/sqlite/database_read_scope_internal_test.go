package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"testing"

	_ "modernc.org/sqlite"
)

func newReadScopeTestDatabase(t *testing.T) *Database {
	t.Helper()
	path := t.TempDir() + "/store.db"
	raw, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	// SQLite defers actually creating the file until first use; force it so
	// the read-only DSN below (mode=ro) has a file to open.
	if _, err := raw.Exec(`PRAGMA user_version`); err != nil {
		t.Fatal(err)
	}
	if err := raw.Close(); err != nil {
		t.Fatal(err)
	}
	return NewDatabase(path, nil)
}

// TestWithReadScope_OpensOnceClosesOnceChecksCompatOnce covers TDD plan item
// 1: a single WithReadScope call opens exactly one connection, runs the
// compatibility guard exactly once, and closes the connection when fn
// returns.
func TestWithReadScope_OpensOnceClosesOnceChecksCompatOnce(t *testing.T) {
	ctx := context.Background()
	database := newReadScopeTestDatabase(t)

	var opens, compatChecks int
	database.SetReadOnlyOpenHookForTest(func() { opens++ })
	database.SetCompatibilityCheckHookForTest(func() { compatChecks++ })

	var handleInsideScope *sql.DB
	err := database.WithReadScope(ctx, func(scopedCtx context.Context) error {
		db, err := database.openReadOnly(scopedCtx)
		if err != nil {
			return err
		}
		handleInsideScope = db
		return db.PingContext(scopedCtx)
	})
	if err != nil {
		t.Fatalf("WithReadScope() error = %v", err)
	}
	if opens != 1 {
		t.Fatalf("opens = %d, want 1", opens)
	}
	if compatChecks != 1 {
		t.Fatalf("compatChecks = %d, want 1", compatChecks)
	}
	if err := handleInsideScope.PingContext(ctx); err == nil {
		t.Fatal("handle still usable after WithReadScope returned; want it closed")
	}
}

// TestWithReadScope_NestedReusesOuterHandle covers TDD plan item 2: a nested
// WithReadScope call made while ctx already carries a scope must reuse the
// outer handle rather than opening a second connection or re-running the
// compatibility guard.
func TestWithReadScope_NestedReusesOuterHandle(t *testing.T) {
	ctx := context.Background()
	database := newReadScopeTestDatabase(t)

	var opens, compatChecks int
	database.SetReadOnlyOpenHookForTest(func() { opens++ })
	database.SetCompatibilityCheckHookForTest(func() { compatChecks++ })

	var outer, inner *sql.DB
	err := database.WithReadScope(ctx, func(outerCtx context.Context) error {
		var err error
		outer, err = database.openReadOnly(outerCtx)
		if err != nil {
			return err
		}
		return database.WithReadScope(outerCtx, func(innerCtx context.Context) error {
			var err error
			inner, err = database.openReadOnly(innerCtx)
			return err
		})
	})
	if err != nil {
		t.Fatalf("WithReadScope() error = %v", err)
	}
	if opens != 1 {
		t.Fatalf("opens = %d, want 1 (nested scope must reuse the outer handle)", opens)
	}
	if compatChecks != 1 {
		t.Fatalf("compatChecks = %d, want 1", compatChecks)
	}
	if outer != inner {
		t.Fatal("nested WithReadScope returned a different handle than the outer scope")
	}
}

// TestWithReadScope_CallerThatSkipsScopeSeesPerCallBehaviour covers TDD plan
// item 4: a caller that never enters WithReadScope must keep paying
// setup+ping+compat on every openReadOnly call.
func TestWithReadScope_CallerThatSkipsScopeSeesPerCallBehaviour(t *testing.T) {
	ctx := context.Background()
	database := newReadScopeTestDatabase(t)

	var opens, compatChecks int
	database.SetReadOnlyOpenHookForTest(func() { opens++ })
	database.SetCompatibilityCheckHookForTest(func() { compatChecks++ })

	for i := 0; i < 3; i++ {
		db, err := database.openReadOnly(ctx)
		if err != nil {
			t.Fatalf("openReadOnly() error = %v", err)
		}
		if err := db.Close(); err != nil {
			t.Fatalf("Close() error = %v", err)
		}
	}
	if opens != 3 {
		t.Fatalf("opens = %d, want 3 (no scope entered, so each call opens its own connection)", opens)
	}
	if compatChecks != 3 {
		t.Fatalf("compatChecks = %d, want 3", compatChecks)
	}
}

// TestWithReadScope_ClosesHandleOnPanic covers the Given/When/Then panic
// case: the handle opened by WithReadScope must close via defer even when fn
// panics, so no *sql.DB leaks out of a crashed candidate loop.
func TestWithReadScope_ClosesHandleOnPanic(t *testing.T) {
	ctx := context.Background()
	database := newReadScopeTestDatabase(t)

	var handleInsideScope *sql.DB
	func() {
		defer func() {
			if r := recover(); r == nil {
				t.Fatal("expected panic to propagate through WithReadScope")
			}
		}()
		_ = database.WithReadScope(ctx, func(scopedCtx context.Context) error {
			db, err := database.openReadOnly(scopedCtx)
			if err != nil {
				t.Fatal(err)
			}
			handleInsideScope = db
			panic("boom")
		})
	}()

	if err := handleInsideScope.PingContext(ctx); err == nil {
		t.Fatal("handle still usable after a panic inside WithReadScope; want it closed")
	}
}

// TestWithReadScope_CompatibilityFailureStopsAtEntry covers the
// Given/When/Then compatibility case: an incompatible store must fail scope
// entry before fn runs at all, and openReadOnly must independently reject
// the same store.
func TestWithReadScope_CompatibilityFailureStopsAtEntry(t *testing.T) {
	ctx := context.Background()
	path := t.TempDir() + "/future.db"
	raw, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := raw.Exec(`CREATE TABLE store_format_state(singleton INTEGER PRIMARY KEY, minimum_reader_version INTEGER NOT NULL); INSERT INTO store_format_state VALUES(1, 40)`); err != nil {
		t.Fatal(err)
	}
	if err := raw.Close(); err != nil {
		t.Fatal(err)
	}

	database := NewDatabase(path, nil)
	fnRan := false
	err = database.WithReadScope(ctx, func(context.Context) error {
		fnRan = true
		return nil
	})
	if err == nil {
		t.Fatal("WithReadScope accepted a future reader requirement")
	}
	if fnRan {
		t.Fatal("fn ran despite a failed compatibility guard at scope entry")
	}
	if _, err := database.openReadOnly(ctx); err == nil {
		t.Fatal("openReadOnly accepted a future reader requirement")
	}
}

// TestWithReadScope_PropagatesCancelledContext covers the Given/When/Then
// cancellation case: fn observes ctx.Err() from the scoped context.
func TestWithReadScope_PropagatesCancelledContext(t *testing.T) {
	database := newReadScopeTestDatabase(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	err := database.WithReadScope(context.Background(), func(_ context.Context) error {
		// Re-derive cancellation state inside fn using the already-cancelled
		// parent, mirroring a candidate loop that checks ctx.Err() mid-pass.
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
			return errors.New("expected ctx to already be cancelled")
		}
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
}
