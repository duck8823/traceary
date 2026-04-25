package sqlite_test

import (
	"context"
	"fmt"
	"path/filepath"
	"sync"
	"testing"
	"time"

	apptypes "github.com/duck8823/traceary/application/types"
	"github.com/duck8823/traceary/domain/model"
	"github.com/duck8823/traceary/domain/types"
)

// TestDatabase_ConcurrentWritersAndReaders_NoSQLITE_BUSY simulates the
// tail polling scenario that previously surfaced
// `database is locked (5) SQLITE_BUSY`: several hook-like goroutines
// writing events while a tail-like goroutine polls recent events.
//
// With WAL + busy_timeout enabled on the DSN, none of the calls should
// fail with SQLITE_BUSY; the driver retries internally within the
// configured busy timeout window.
func TestDatabase_ConcurrentWritersAndReaders_NoSQLITE_BUSY(t *testing.T) {
	t.Parallel()

	dbPath := filepath.Join(t.TempDir(), "traceary.db")
	eventDS, _, storeManager := newFullDatasources(t, dbPath, listSessionsTestMigrations())
	ctx := context.Background()
	if err := storeManager.Initialize(ctx); err != nil {
		t.Fatalf("Initialize() error = %v", err)
	}

	const writers = 4
	const writesPerWriter = 40
	const readers = 2
	const readsPerReader = 80

	errCh := make(chan error, writers+readers)
	var wg sync.WaitGroup

	for w := 0; w < writers; w++ {
		wg.Add(1)
		go func(writer int) {
			defer wg.Done()
			for i := 0; i < writesPerWriter; i++ {
				event := newConcurrencyTestEvent(t,
					fmt.Sprintf("event-%d-%d", writer, i),
					fmt.Sprintf("session-%d", writer),
					time.Now().UTC(),
				)
				if err := eventDS.Save(ctx, event); err != nil {
					errCh <- fmt.Errorf("writer %d iter %d: Save(): %w", writer, i, err)
					return
				}
			}
		}(w)
	}

	for r := 0; r < readers; r++ {
		wg.Add(1)
		go func(reader int) {
			defer wg.Done()
			criteria := apptypes.NewEventListCriteriaBuilder(50).Build()
			for i := 0; i < readsPerReader; i++ {
				if _, err := eventDS.ListWindow(ctx, criteria); err != nil {
					errCh <- fmt.Errorf("reader %d iter %d: ListWindow(): %w", reader, i, err)
					return
				}
			}
		}(r)
	}

	wg.Wait()
	close(errCh)

	for err := range errCh {
		t.Error(err)
	}
}

func newConcurrencyTestEvent(t *testing.T, eventIDValue, sessionIDValue string, createdAt time.Time) *model.Event {
	t.Helper()

	eventID, err := types.EventIDFrom(eventIDValue)
	if err != nil {
		t.Fatalf("EventIDFrom() error = %v", err)
	}
	agent, err := types.AgentFrom("codex")
	if err != nil {
		t.Fatalf("AgentFrom() error = %v", err)
	}
	sessionID, err := types.SessionIDFrom(sessionIDValue)
	if err != nil {
		t.Fatalf("SessionIDFrom() error = %v", err)
	}

	return model.EventOf(
		eventID,
		types.EventKindCommandExecuted,
		types.Client("hook"),
		agent,
		sessionID,
		types.Workspace("concurrency-test"),
		"concurrent write",
		createdAt,
	)
}
