package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"math"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	apptypes "github.com/duck8823/traceary/application/types"
	"github.com/duck8823/traceary/domain"
	"github.com/duck8823/traceary/domain/model"
	domaintypes "github.com/duck8823/traceary/domain/types"
)

func TestMigration44InstallsConstantTimeArchiveSequenceFoundation(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "store.db")
	legacy := NewDatabase(path, preparedMigrationsBefore(t, 44))
	if err := NewStoreManagementDatasource(legacy).Initialize(ctx); err != nil {
		t.Fatal(err)
	}
	raw, err := sql.Open("sqlite", sqliteDSN(path))
	if err != nil {
		t.Fatal(err)
	}
	for index := 0; index < 20; index++ {
		if _, err = raw.Exec(`INSERT INTO events(id,kind,agent,session_id,body,created_at,client,workspace) VALUES(?,'note','a','s','body','2026-08-01T00:00:00Z','c','w')`, fmt.Sprintf("legacy-%02d", index)); err != nil {
			t.Fatal(err)
		}
	}
	_ = raw.Close()

	database := NewDatabase(path, preparedMigrations(t))
	if err = NewStoreManagementDatasource(database).Initialize(ctx); err != nil {
		t.Fatal(err)
	}
	raw, err = sql.Open("sqlite", sqliteDSN(path))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = raw.Close() }()
	var mapped int
	if err = raw.QueryRow(`SELECT COUNT(*) FROM archive_event_sequences`).Scan(&mapped); err != nil {
		t.Fatal(err)
	}
	if mapped != 0 {
		t.Fatalf("migration backfilled %d events, want zero", mapped)
	}
	var storeID string
	var key []byte
	if err = raw.QueryRow(`SELECT store_id,filter_key FROM archive_store_lineage WHERE singleton=1`).Scan(&storeID, &key); err != nil {
		t.Fatal(err)
	}
	if len(storeID) != 32 || len(key) != 32 {
		t.Fatalf("lineage lengths = %d/%d", len(storeID), len(key))
	}
	if err = NewStoreManagementDatasource(database).Initialize(ctx); err != nil {
		t.Fatal(err)
	}
	var secondID string
	var secondKey []byte
	if err = raw.QueryRow(`SELECT store_id,filter_key FROM archive_store_lineage WHERE singleton=1`).Scan(&secondID, &secondKey); err != nil {
		t.Fatal(err)
	}
	if secondID != storeID || string(secondKey) != string(key) {
		t.Fatal("store lineage changed across initialize")
	}
}

func TestArchiveSequenceTriggerIsTransactionalMonotonicAndOverflowSafe(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "store.db")
	database := NewDatabase(path, preparedMigrations(t))
	if err := NewStoreManagementDatasource(database).Initialize(ctx); err != nil {
		t.Fatal(err)
	}
	raw, err := sql.Open("sqlite", sqliteDSN(path))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = raw.Close() }()
	insert := func(exec interface {
		ExecContext(context.Context, string, ...any) (sql.Result, error)
	}, id string) error {
		_, execErr := exec.ExecContext(ctx, `INSERT INTO events(id,kind,agent,session_id,body,created_at,client,workspace) VALUES(?,'note','a','s','body','2026-08-01T00:00:00Z','c','w')`, id)
		if execErr != nil {
			return fmt.Errorf("insert event: %w", execErr)
		}
		return nil
	}
	if err = insert(raw, "committed-1"); err != nil {
		t.Fatal(err)
	}
	tx, err := raw.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err = insert(tx, "rolled-back"); err != nil {
		t.Fatal(err)
	}
	if err = tx.Rollback(); err != nil {
		t.Fatal(err)
	}
	if err = insert(raw, "committed-2"); err != nil {
		t.Fatal(err)
	}
	var first, second, next int64
	if err = raw.QueryRow(`SELECT sequence FROM archive_event_sequences WHERE event_id='committed-1'`).Scan(&first); err != nil {
		t.Fatal(err)
	}
	if err = raw.QueryRow(`SELECT sequence FROM archive_event_sequences WHERE event_id='committed-2'`).Scan(&second); err != nil {
		t.Fatal(err)
	}
	if err = raw.QueryRow(`SELECT next_sequence FROM archive_sequence_allocator WHERE singleton=1`).Scan(&next); err != nil {
		t.Fatal(err)
	}
	if first != 1 || second != 2 || next != 3 {
		t.Fatalf("sequence state = %d/%d next=%d", first, second, next)
	}
	if _, err = raw.Exec(`UPDATE archive_sequence_allocator SET next_sequence=? WHERE singleton=1`, int64(math.MaxInt64)); err != nil {
		t.Fatal(err)
	}
	if err = insert(raw, "overflow"); err == nil {
		t.Fatal("overflow insert succeeded")
	}
	var overflowRows int
	if err = raw.QueryRow(`SELECT COUNT(*) FROM events WHERE id='overflow'`).Scan(&overflowRows); err != nil || overflowRows != 0 {
		t.Fatalf("overflow event count = %d, err=%v", overflowRows, err)
	}
}

func TestArchiveSequenceOverflowIsTypedThroughEventWriteBoundary(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "store.db")
	database := NewDatabase(path, preparedMigrations(t))
	if err := NewStoreManagementDatasource(database).Initialize(ctx); err != nil {
		t.Fatal(err)
	}
	raw, err := sql.Open("sqlite", sqliteDSN(path))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = raw.Exec(`UPDATE archive_sequence_allocator SET next_sequence=? WHERE singleton=1`, int64(math.MaxInt64)); err != nil {
		_ = raw.Close()
		t.Fatal(err)
	}
	_ = raw.Close()
	eventID, _ := domaintypes.EventIDFrom("typed-overflow")
	agent, _ := domaintypes.AgentFrom("codex")
	sessionID, _ := domaintypes.SessionIDFrom("session-overflow")
	event := model.EventOf(eventID, domaintypes.EventKindNote, domaintypes.Client("cli"), agent, sessionID, domaintypes.Workspace("workspace"), "body", time.Now().UTC())
	if err = NewEventDatasource(database).Save(ctx, event); !errors.Is(err, apptypes.ErrArchiveSequenceOverflow) {
		t.Fatalf("Save() error = %v, want ErrArchiveSequenceOverflow", err)
	}
}

func TestArchiveSequenceTriggerSerializesConcurrentInsertPaths(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "store.db")
	if err := NewStoreManagementDatasource(NewDatabase(path, preparedMigrations(t))).Initialize(ctx); err != nil {
		t.Fatal(err)
	}
	const count = 16
	var wg sync.WaitGroup
	errorsByWriter := make(chan error, count)
	for index := 0; index < count; index++ {
		wg.Add(1)
		go func(index int) {
			defer wg.Done()
			db, openErr := sql.Open("sqlite", sqliteDSN(path))
			if openErr != nil {
				errorsByWriter <- openErr
				return
			}
			defer func() { _ = db.Close() }()
			_, execErr := db.ExecContext(ctx, `INSERT INTO events(id,kind,agent,session_id,body,created_at,client,workspace) VALUES(?,'note','a','s','body','2026-08-01T00:00:00Z','c','w')`, fmt.Sprintf("event-%02d", index))
			errorsByWriter <- execErr
		}(index)
	}
	wg.Wait()
	close(errorsByWriter)
	for err := range errorsByWriter {
		if err != nil {
			t.Fatal(err)
		}
	}
	raw, _ := sql.Open("sqlite", sqliteDSN(path))
	defer func() { _ = raw.Close() }()
	var rows, distinct, minimum, maximum int
	if err := raw.QueryRow(`SELECT COUNT(*),COUNT(DISTINCT sequence),MIN(sequence),MAX(sequence) FROM archive_event_sequences`).Scan(&rows, &distinct, &minimum, &maximum); err != nil {
		t.Fatal(err)
	}
	if rows != count || distinct != count || minimum != 1 || maximum != count {
		t.Fatalf("mapping aggregate = rows:%d distinct:%d range:%d..%d", rows, distinct, minimum, maximum)
	}
}

func TestArchiveSequenceInventoryResumesAndActivatesOnlyAfterBoundedVerification(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	database := newPre44ArchiveInventoryStore(t, 7)
	budget := apptypes.ArchiveSequenceBudget{Rows: 2, StoredBytes: 128, WriteBytes: 128, WallTime: 2 * time.Second, LockTime: time.Second}
	_, err := database.StartArchiveSequenceInventory(ctx, budget)
	if err != nil {
		t.Fatal(err)
	}
	first, err := database.SelectArchiveSequenceInventory(ctx, budget)
	if err != nil {
		t.Fatal(err)
	}
	tampered := first
	tampered.Items = nil
	tampered.Done = !first.Done
	progress, err := database.ApplyArchiveSequenceInventory(ctx, budget, tampered)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = database.ApplyArchiveSequenceInventory(ctx, budget, first); !errors.Is(err, apptypes.ErrArchiveSequenceStaleGeneration) {
		t.Fatal("stale inventory page applied twice")
	}
	generation := progress.Generation
	for generation.Phase == domain.ArchiveInventoryScanning {
		page, selectErr := database.SelectArchiveSequenceInventory(ctx, budget)
		if selectErr != nil {
			t.Fatal(selectErr)
		}
		progress, err = database.ApplyArchiveSequenceInventory(ctx, budget, page)
		if err != nil {
			t.Fatal(err)
		}
		generation = progress.Generation
	}
	status, err := database.ArchiveSequenceStatus(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if status.ActiveGenerationID != "" || generation.HighWater != 7 {
		t.Fatalf("pre-verification status = %+v generation=%+v", status, generation)
	}
	verificationCalls := 0
	for generation.Phase == domain.ArchiveInventoryVerifying {
		progress, err = database.VerifyArchiveSequenceInventory(ctx, budget, generation)
		if err != nil {
			t.Fatal(err)
		}
		verificationCalls++
		generation = progress.Generation
	}
	if verificationCalls < 4 || generation.Phase != domain.ArchiveInventoryComplete {
		t.Fatalf("verification calls=%d generation=%+v", verificationCalls, generation)
	}
	status, err = database.ArchiveSequenceStatus(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if status.ActiveGenerationID != generation.ID || status.VerifiedHighWater != 7 || status.MappedEvents != 7 {
		t.Fatalf("active status = %+v", status)
	}
}

func TestArchiveSequenceInventoryHonorsCapsCancellationAndFailsClosedOnGap(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	database := newPre44ArchiveInventoryStore(t, 2)
	tiny := apptypes.ArchiveSequenceBudget{Rows: 1, StoredBytes: 1, WriteBytes: 1, WallTime: time.Second, LockTime: time.Second}
	if _, err := database.StartArchiveSequenceInventory(ctx, tiny); err != nil {
		t.Fatal(err)
	}
	if _, err := database.SelectArchiveSequenceInventory(ctx, tiny); err == nil {
		t.Fatal("identity exceeding caps was selected")
	}
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := database.SelectArchiveSequenceInventory(cancelled, tiny); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled select error = %v", err)
	}

	budget := apptypes.ArchiveSequenceBudget{Rows: 10, StoredBytes: 1024, WriteBytes: 1024, WallTime: time.Second, LockTime: time.Second}
	// A new generation may replace the non-mutating generation.
	raw, err := sql.Open("sqlite", sqliteDSN(database.Path()))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = raw.Exec(`UPDATE archive_sequence_inventory_state SET phase='failed' WHERE singleton=1`); err != nil {
		t.Fatal(err)
	}
	_ = raw.Close()
	_, err = database.StartArchiveSequenceInventory(ctx, budget)
	if err != nil {
		t.Fatal(err)
	}
	page, err := database.SelectArchiveSequenceInventory(ctx, budget)
	if err != nil {
		t.Fatal(err)
	}
	progress, err := database.ApplyArchiveSequenceInventory(ctx, budget, page)
	if err != nil {
		t.Fatal(err)
	}
	generation := progress.Generation
	raw, _ = sql.Open("sqlite", sqliteDSN(database.Path()))
	if _, err = raw.Exec(`DELETE FROM archive_event_sequences WHERE sequence=1`); err != nil {
		t.Fatal(err)
	}
	_ = raw.Close()
	if _, err = database.VerifyArchiveSequenceInventory(ctx, budget, generation); !errors.Is(err, apptypes.ErrArchiveSequenceActivation) || !errors.Is(err, apptypes.ErrArchiveSequenceGap) {
		t.Fatal("gap verification succeeded")
	}
	status, err := database.ArchiveSequenceStatus(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if status.ActiveGenerationID != "" || status.Generation.Phase != domain.ArchiveInventoryFailed {
		t.Fatalf("failed verification activated: %+v", status)
	}
}

func TestArchiveSequenceInventoryRejectsHugeIdentityBeforeMaterializingIt(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "store.db")
	if err := NewStoreManagementDatasource(NewDatabase(path, preparedMigrationsBefore(t, 44))).Initialize(ctx); err != nil {
		t.Fatal(err)
	}
	raw, err := sql.Open("sqlite", sqliteDSN(path))
	if err != nil {
		t.Fatal(err)
	}
	huge := strings.Repeat("x", 1<<20)
	if _, err = raw.Exec(`INSERT INTO events(id,kind,agent,session_id,body,created_at,client,workspace) VALUES(?,'note','a','s','b','2026-08-01T00:00:00Z','c','w')`, huge); err != nil {
		t.Fatal(err)
	}
	_ = raw.Close()
	database := NewDatabase(path, preparedMigrations(t))
	if err = NewStoreManagementDatasource(database).Initialize(ctx); err != nil {
		t.Fatal(err)
	}
	budget := apptypes.ArchiveSequenceBudget{Rows: 1, StoredBytes: 64, WriteBytes: 128, WallTime: time.Second, LockTime: time.Second}
	if _, err = database.StartArchiveSequenceInventory(ctx, budget); err != nil {
		t.Fatal(err)
	}
	if _, err = database.SelectArchiveSequenceInventory(ctx, budget); !errors.Is(err, apptypes.ErrArchiveSequenceLimit) {
		t.Fatalf("huge identity error = %v", err)
	}
}

func TestArchiveSequenceApplyRejectsPageDigestDriftAndTypedOverflow(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	database := newPre44ArchiveInventoryStore(t, 1)
	budget := apptypes.ArchiveSequenceBudget{Rows: 1, StoredBytes: 128, WriteBytes: 128, WallTime: time.Second, LockTime: time.Second}
	if _, err := database.StartArchiveSequenceInventory(ctx, budget); err != nil {
		t.Fatal(err)
	}
	page, err := database.SelectArchiveSequenceInventory(ctx, budget)
	if err != nil {
		t.Fatal(err)
	}
	page.PageDigest = strings.Repeat("0", 64)
	if _, err = database.ApplyArchiveSequenceInventory(ctx, budget, page); !errors.Is(err, apptypes.ErrArchiveSequenceDrift) {
		t.Fatalf("digest drift error = %v", err)
	}
	raw, err := sql.Open("sqlite", sqliteDSN(database.Path()))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = raw.Close() }()
	if _, err = raw.Exec(`UPDATE archive_sequence_allocator SET next_sequence=? WHERE singleton=1`, int64(math.MaxInt64)); err != nil {
		t.Fatal(err)
	}
	tx, err := raw.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback() }()
	if err = allocateArchiveSequence(ctx, tx, "historical-000"); !errors.Is(err, apptypes.ErrArchiveSequenceOverflow) {
		t.Fatalf("overflow error = %v", err)
	}
}

func newPre44ArchiveInventoryStore(t *testing.T, eventCount int) *Database {
	t.Helper()
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "store.db")
	if err := NewStoreManagementDatasource(NewDatabase(path, preparedMigrationsBefore(t, 44))).Initialize(ctx); err != nil {
		t.Fatal(err)
	}
	raw, err := sql.Open("sqlite", sqliteDSN(path))
	if err != nil {
		t.Fatal(err)
	}
	for index := 0; index < eventCount; index++ {
		if _, err = raw.Exec(`INSERT INTO events(id,kind,agent,session_id,body,created_at,client,workspace) VALUES(?,'note','a','s','body','2026-08-01T00:00:00Z','c','w')`, fmt.Sprintf("historical-%03d", index)); err != nil {
			t.Fatal(err)
		}
	}
	_ = raw.Close()
	database := NewDatabase(path, preparedMigrations(t))
	if err = NewStoreManagementDatasource(database).Initialize(ctx); err != nil {
		t.Fatal(err)
	}
	return database
}
