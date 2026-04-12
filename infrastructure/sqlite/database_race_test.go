package sqlite_test

import (
	"context"
	"path/filepath"
	"sync"
	"testing"

	infra "github.com/duck8823/traceary/infrastructure/sqlite"
)

// TestDatabase_SetPath_ConcurrentWithInitialize exercises the race
// scenario Codex flagged in the --db-path review: a goroutine calling
// SetPath while another goroutine runs Initialize. With the mutex +
// path-snapshot guard the race detector should not fire.
func TestDatabase_SetPath_ConcurrentWithInitialize(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	dbA := filepath.Join(dir, "a.db")
	dbB := filepath.Join(dir, "b.db")

	db := infra.NewDatabase(dbA, listSessionsTestMigrations())
	store := infra.NewStoreManagementDatasource(db)

	var wg sync.WaitGroup
	const swaps = 500
	ctx := context.Background()

	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < swaps; i++ {
			if i%2 == 0 {
				db.SetPath(dbA)
			} else {
				db.SetPath(dbB)
			}
		}
	}()

	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < swaps; i++ {
			// Initialize is idempotent and must never observe a torn
			// path snapshot. A data race would fail this test under
			// `go test -race`.
			if err := store.Initialize(ctx); err != nil {
				t.Errorf("Initialize() error = %v", err)
				return
			}
		}
	}()

	wg.Wait()
}

// TestDatabase_SetPath_ConcurrentPathReaders asserts that SetPath and
// Path can run concurrently without a data race.
func TestDatabase_SetPath_ConcurrentPathReaders(t *testing.T) {
	t.Parallel()

	db := infra.NewDatabase("/tmp/initial.db", listSessionsTestMigrations())

	var wg sync.WaitGroup
	const iterations = 1000

	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < iterations; i++ {
			db.SetPath("/tmp/a.db")
			db.SetPath("/tmp/b.db")
		}
	}()

	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < iterations; i++ {
			_ = db.Path()
		}
	}()

	wg.Wait()
}
