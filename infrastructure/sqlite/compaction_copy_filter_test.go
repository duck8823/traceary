package sqlite_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"database/sql"

	"github.com/duck8823/traceary/application"
	"github.com/duck8823/traceary/application/usecase"
	"github.com/duck8823/traceary/domain/model"
	"github.com/duck8823/traceary/domain/types"
	"github.com/duck8823/traceary/infrastructure/sqlite"
)

func TestVerifyPairRejectsEmptyEventsCandidateWhenSourceHasUniqueEvents(t *testing.T) {
	t.Parallel()
	dbPath, events, store := prepareDiscardGCFixture(t)
	_ = store
	unique := newGCEventFixture(t, "event-unique", types.EventKindTranscript, "unique-why", time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC))
	if err := events.Save(context.Background(), unique); err != nil {
		t.Fatal(err)
	}
	candidate := filepath.Join(filepath.Dir(dbPath), "candidate.db")
	data, err := os.ReadFile(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(candidate, data, 0o600); err != nil {
		t.Fatal(err)
	}
	db := openRetentionDB(t, candidate)
	if _, err := db.Exec(`DELETE FROM events`); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	if err := (sqlite.SQLiteCompactionBuilder{}).VerifyPair(context.Background(), dbPath, candidate); err == nil {
		t.Fatal("VerifyPair() error = nil, want rejection of emptied unique events")
	}
}

func TestVerifyPairRejectsRewrittenAvailableBody(t *testing.T) {
	t.Parallel()
	dbPath, events, store := prepareDiscardGCFixture(t)
	_ = store
	body := newGCEventFixture(t, "event-body", types.EventKindTranscript, "original-why", time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC))
	if err := events.Save(context.Background(), body); err != nil {
		t.Fatal(err)
	}
	candidate := filepath.Join(filepath.Dir(dbPath), "candidate.db")
	data, err := os.ReadFile(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(candidate, data, 0o600); err != nil {
		t.Fatal(err)
	}
	db := openRetentionDB(t, candidate)
	if _, err := db.Exec(`UPDATE events SET body = 'tampered-why' WHERE id = 'event-body'`); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	if err := (sqlite.SQLiteCompactionBuilder{}).VerifyPair(context.Background(), dbPath, candidate); err == nil {
		t.Fatal("VerifyPair() error = nil, want rejection of rewritten body")
	}
}

func TestVerifyPairRejectsCrossIdentitySameBodyDeletion(t *testing.T) {
	t.Parallel()
	dbPath, events, store := prepareDiscardGCFixture(t)
	_ = store
	first := newGCEventFixture(t, "event-a", types.EventKindTranscript, "shared-body", time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC))
	second := newGCEventFixture(t, "event-b", types.EventKindTranscript, "shared-body", time.Date(2026, 6, 1, 0, 0, 1, 0, time.UTC))
	if err := events.Save(context.Background(), first); err != nil {
		t.Fatal(err)
	}
	if err := events.Save(context.Background(), second); err != nil {
		t.Fatal(err)
	}
	db := openRetentionDB(t, dbPath)
	if _, err := db.Exec(`UPDATE events SET session_id = 'session-other' WHERE id = 'event-b'`); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	candidate := filepath.Join(filepath.Dir(dbPath), "candidate.db")
	data, err := os.ReadFile(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(candidate, data, 0o600); err != nil {
		t.Fatal(err)
	}
	db = openRetentionDB(t, candidate)
	if _, err := db.Exec(`DELETE FROM events WHERE id = 'event-b'`); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	if err := (sqlite.SQLiteCompactionBuilder{}).VerifyPair(context.Background(), dbPath, candidate); err == nil {
		t.Fatal("VerifyPair() error = nil, want rejection of a same-body drop from another session")
	}
}

func TestVerifyPairRejectsSourceHookAndCreatedAtNormDrift(t *testing.T) {
	t.Parallel()
	dbPath, events, store := prepareDiscardGCFixture(t)
	_ = store
	body := newGCEventFixture(t, "event-ident", types.EventKindTranscript, "identity-why", time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC))
	if err := events.Save(context.Background(), body); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name string
		sql  string
	}{
		{name: "source_hook", sql: `UPDATE events SET source_hook = 'tampered-hook' WHERE id = 'event-ident'`},
		{name: "created_at_norm", sql: `UPDATE events SET created_at_norm = '2099-01-01T00:00:00.000000000Z' WHERE id = 'event-ident'`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			candidate := filepath.Join(filepath.Dir(dbPath), "candidate-"+tc.name+".db")
			if err := os.WriteFile(candidate, data, 0o600); err != nil {
				t.Fatal(err)
			}
			db := openRetentionDB(t, candidate)
			if _, err := db.Exec(tc.sql); err != nil {
				t.Fatal(err)
			}
			if err := db.Close(); err != nil {
				t.Fatal(err)
			}
			if err := (sqlite.SQLiteCompactionBuilder{}).VerifyPair(context.Background(), dbPath, candidate); err == nil {
				t.Fatalf("VerifyPair() error = nil, want rejection of %s drift", tc.name)
			}
		})
	}
}

func TestVerifyPairRejectsRewrittenAvailableCandidate(t *testing.T) {
	t.Parallel()
	dbPath, events, store := prepareDiscardGCFixture(t)
	_ = store
	body := newGCEventFixture(t, "event-codec", types.EventKindTranscript, "readable-why", time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC))
	if err := events.Save(context.Background(), body); err != nil {
		t.Fatal(err)
	}
	candidate := filepath.Join(filepath.Dir(dbPath), "candidate.db")
	data, err := os.ReadFile(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(candidate, data, 0o600); err != nil {
		t.Fatal(err)
	}
	db := openRetentionDB(t, candidate)
	if _, err := db.Exec(`UPDATE events SET body = 'rewritten-available-body' WHERE id = 'event-codec'`); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	if err := (sqlite.SQLiteCompactionBuilder{}).VerifyPair(context.Background(), dbPath, candidate); err == nil {
		t.Fatal("VerifyPair() error = nil, want rejection of a rewritten available body")
	}
}

func TestCompactClearsDuplicatedCommandExecutedBodiesAndKeepsLogOnlyBodies(t *testing.T) {
	t.Parallel()
	dbPath, events, store := prepareDiscardGCFixture(t)
	_ = store
	composed := strings.Repeat("command + INPUT + OUTPUT\n", 80)
	saveHistoricalCommandExecuted(t, events, "cmd-dup", composed, true)
	saveHistoricalCommandExecuted(t, events, "cmd-log", "log-only body", false)

	svc := newTestCompactionUsecase(t, dbPath)
	got, err := svc.Compact(context.Background(), application.CompactInput{
		Source: dbPath,
	})
	if err != nil {
		t.Fatalf("Compact() error = %v", err)
	}
	if got.ReleasedCommandBodyRows != 1 {
		t.Fatalf("ReleasedCommandBodyRows = %d, want 1", got.ReleasedCommandBodyRows)
	}
	if got.ReleasedCommandBodyBytes <= 0 {
		t.Fatalf("ReleasedCommandBodyBytes = %d, want measured stored bytes", got.ReleasedCommandBodyBytes)
	}

	db := openRetentionDB(t, dbPath)
	defer func() { _ = db.Close() }()
	assertClearedCommandBody(t, db, "cmd-dup")
	var logBody string
	if err := db.QueryRow(`SELECT CAST(body AS TEXT) FROM events WHERE id = 'cmd-log'`).Scan(&logBody); err != nil {
		t.Fatal(err)
	}
	if logBody != "log-only body" {
		t.Fatalf("log-only body = %q, want preserved", logBody)
	}
	var auditCommand string
	if err := db.QueryRow(`SELECT command_text FROM command_audits WHERE event_id = 'cmd-dup'`).Scan(&auditCommand); err != nil {
		t.Fatal(err)
	}
	if auditCommand != "echo duplicated" {
		t.Fatalf("audit command = %q, want echo duplicated", auditCommand)
	}
}

func TestCompactReportsStoredBlobBytes(t *testing.T) {
	t.Parallel()
	dbPath, events, store := prepareDiscardGCFixture(t)
	_ = store
	plain := strings.Repeat("aaaaaaaaaa", 400)
	saveHistoricalCommandExecuted(t, events, "cmd-plain", plain, true)

	db := openRetentionDB(t, dbPath)
	var stored int64
	if err := db.QueryRow(`SELECT length(CAST(body AS BLOB)) FROM events WHERE id = 'cmd-plain'`).Scan(&stored); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	if stored != int64(len(plain)) {
		t.Fatalf("fixture stored=%d, want plaintext length %d", stored, len(plain))
	}

	got, err := newTestCompactionUsecase(t, dbPath).Compact(context.Background(), application.CompactInput{
		Source: dbPath,
	})
	if err != nil {
		t.Fatalf("Compact() error = %v", err)
	}
	if got.ReleasedCommandBodyBytes != stored {
		t.Fatalf("ReleasedCommandBodyBytes = %d, want stored blob %d", got.ReleasedCommandBodyBytes, stored)
	}
}

func TestVerifyPairAllowsClearedCommandExecutedBodyWithAudit(t *testing.T) {
	t.Parallel()
	dbPath, events, store := prepareDiscardGCFixture(t)
	_ = store
	saveHistoricalCommandExecuted(t, events, "cmd-dup", "duplicated body", true)
	candidate := filepath.Join(filepath.Dir(dbPath), "candidate.db")
	data, err := os.ReadFile(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(candidate, data, 0o600); err != nil {
		t.Fatal(err)
	}
	db := openRetentionDB(t, candidate)
	dropRetiredSearchFamily(t, db)
	emptyCommandExecutedBody(t, db, "cmd-dup")
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	if err := (sqlite.SQLiteCompactionBuilder{}).VerifyPair(context.Background(), dbPath, candidate); err != nil {
		t.Fatalf("VerifyPair() error = %v, want permitted empty command_executed body", err)
	}
}

func TestVerifyPairRejectsClearedCommandExecutedBodyWithoutAudit(t *testing.T) {
	t.Parallel()
	dbPath, events, store := prepareDiscardGCFixture(t)
	_ = store
	saveHistoricalCommandExecuted(t, events, "cmd-log", "log-only body", false)
	candidate := filepath.Join(filepath.Dir(dbPath), "candidate.db")
	data, err := os.ReadFile(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(candidate, data, 0o600); err != nil {
		t.Fatal(err)
	}
	db := openRetentionDB(t, candidate)
	dropRetiredSearchFamily(t, db)
	emptyCommandExecutedBody(t, db, "cmd-log")
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	err = (sqlite.SQLiteCompactionBuilder{}).VerifyPair(context.Background(), dbPath, candidate)
	if err == nil {
		t.Fatal("VerifyPair() error = nil, want rejection of a log-only body clear")
	}
	if !strings.Contains(err.Error(), "rewrote body") {
		t.Fatalf("VerifyPair() error = %v, want rewritten-body rejection", err)
	}
}

func newTestCompactionUsecase(t *testing.T, dbPath string) application.StoreCompactionUsecase {
	t.Helper()
	return usecase.NewStoreCompactionUsecase(
		dbPath,
		&sqlite.CompactionFileJournal{Dir: filepath.Join(t.TempDir(), "journal")},
		&sqlite.SQLiteCompactionBuilder{},
		sqlite.StoreReplacementFiles{CallerHoldsExclusiveLease: true},
		sqlite.StoreLeaseCoordinator{},
	)
}

func saveHistoricalCommandExecuted(t *testing.T, events *sqlite.EventDatasource, id, body string, withAudit bool) {
	t.Helper()
	event := newGCEventFixture(t, id, types.EventKindCommandExecuted, body, time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC))
	if !withAudit {
		if err := events.Save(context.Background(), event); err != nil {
			t.Fatalf("Save(%s) error = %v", id, err)
		}
		return
	}
	audit, err := model.NewCommandAudit(event.EventID(), "echo duplicated", "in", "out", false, false)
	if err != nil {
		t.Fatalf("NewCommandAudit(%s) error = %v", id, err)
	}
	if err := events.SaveWithAudit(context.Background(), event, audit); err != nil {
		t.Fatalf("SaveWithAudit(%s) error = %v", id, err)
	}
}

func TestInspectReclaimableBytesUsesMetadataProjection(t *testing.T) {
	t.Parallel()
	dbPath, events, _ := prepareDiscardGCFixture(t)
	old := newGCEventFixture(t, "event-old-transcript", types.EventKindTranscript, strings.Repeat("x", 4000), time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC))
	if err := events.Save(context.Background(), old); err != nil {
		t.Fatal(err)
	}
	got, err := (sqlite.SQLiteCompactionBuilder{}).InspectReclaimableBytes(
		context.Background(),
		dbPath,
	)
	if err != nil {
		t.Fatalf("InspectReclaimableBytes: %v", err)
	}
	if got < 0 {
		t.Fatalf("InspectReclaimableBytes = %d, want non-negative freelist estimate", got)
	}
}

func TestCompactInPlaceDropsDuplicatedCommandBodies(t *testing.T) {
	t.Parallel()
	dbPath, events, _ := prepareDiscardGCFixture(t)
	saveHistoricalCommandExecuted(t, events, "cmd-a", strings.Repeat("same-body", 200), true)
	saveHistoricalCommandExecuted(t, events, "cmd-b", strings.Repeat("same-body", 200), true)
	before, err := os.Stat(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := (sqlite.SQLiteCompactionBuilder{}).CompactInPlace(context.Background(), dbPath, application.CompactFilter{}); err != nil {
		t.Fatalf("CompactInPlace: %v", err)
	}
	after, err := os.Stat(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	if after.Size() <= 0 || before.Size() <= 0 {
		t.Fatalf("unexpected sizes before=%d after=%d", before.Size(), after.Size())
	}
	reclaim, err := (sqlite.SQLiteCompactionBuilder{}).InspectCommandBodyReclaim(context.Background(), dbPath)
	if err != nil {
		t.Fatalf("InspectCommandBodyReclaim: %v", err)
	}
	if reclaim.Rows != 0 {
		t.Fatalf("duplicated command bodies remaining: %+v", reclaim)
	}
}

func dropRetiredSearchFamily(t *testing.T, db *sql.DB) {
	t.Helper()
	for _, name := range []string{"event_search_documents", "event_search_fts", "event_search_backfill_state"} {
		if _, err := db.Exec(`DROP TABLE IF EXISTS ` + name); err != nil {
			t.Fatalf("drop %s: %v", name, err)
		}
	}
}

func emptyCommandExecutedBody(t *testing.T, db *sql.DB, id string) {
	t.Helper()
	if _, err := db.Exec(`UPDATE events SET body = '' WHERE id = ?`, id); err != nil {
		t.Fatal(err)
	}
}

func assertClearedCommandBody(t *testing.T, db *sql.DB, id string) {
	t.Helper()
	var body string
	if err := db.QueryRow(`SELECT CAST(body AS TEXT) FROM events WHERE id = ?`, id).Scan(&body); err != nil {
		t.Fatal(err)
	}
	if body != "" {
		t.Fatalf("cleared body = %q, want empty", body)
	}
}
