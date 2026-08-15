package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"

	apptypes "github.com/duck8823/traceary/application/types"
)

func TestSearchProjectionCleanupDeletesCompositePrimaryKeys(t *testing.T) {
	store, db := newCapacityTestStore(t, nil)
	now := time.Date(2026, 6, 2, 0, 0, 0, 0, time.UTC)
	ctx := context.Background()
	generation, err := store.Start(ctx, defaultSearchProjectionCatchUpBudget(), now)
	if err != nil {
		t.Fatal(err)
	}
	fingerprint := []byte{1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15, 16}
	if _, err = db.Exec(`INSERT INTO search_projection_source_sequence(event_id) VALUES('e-fp')`); err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(`INSERT INTO search_projection_session_summaries(generation_id,session_id,event_count,summary_text,projection_version,summary_version) VALUES(?, 'sess-a', 1, 'summary', 1, 1)`, generation.GenerationID); err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(`INSERT INTO search_projection_command_aggregates(generation_id,session_id,command_count,failure_count) VALUES(?, 'sess-a', 1, 0)`, generation.GenerationID); err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(`INSERT INTO search_projection_session_keywords(generation_id,session_id,keyword,occurrences,keyword_version) VALUES(?, 'sess-a', 'needle', 1, 1)`, generation.GenerationID); err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(`INSERT INTO literal_search_fingerprints(generation_id,source_sequence,event_id,fingerprint,fingerprint_version) SELECT ?, sequence, 'e-fp', ?, 1 FROM search_projection_source_sequence WHERE event_id='e-fp'`, generation.GenerationID, fingerprint); err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(`INSERT INTO search_projection_exclusions(generation_id,source_sequence,event_id,class,measured_bytes,byte_limit) SELECT ?, sequence, 'e-fp', 'stored_bytes', 10, 1 FROM search_projection_source_sequence WHERE event_id='e-fp'`, generation.GenerationID); err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(`UPDATE search_projection_state SET phase='cleanup',checkpoint=0 WHERE generation_id=?`, generation.GenerationID); err != nil {
		t.Fatal(err)
	}
	var exclusionSeq int64
	if err = db.QueryRow(`SELECT source_sequence FROM search_projection_exclusions WHERE generation_id=?`, generation.GenerationID).Scan(&exclusionSeq); err != nil {
		t.Fatal(err)
	}

	plan := apptypes.ProjectionBatchPlan{
		GenerationID:       generation.GenerationID,
		Phase:              "cleanup",
		ExpectedRevision:   generation.SourceRevision,
		ExpectedCheckpoint: 0,
		NextCheckpoint:     1,
		Cleanup: []apptypes.ProjectionCleanupCandidate{
			{Class: "summary", Address1: generation.GenerationID, Address2: "sess-a", LogicalBytes: 1},
			{Class: "aggregate", Address1: generation.GenerationID, Address2: "sess-a", LogicalBytes: 1},
			{Class: "keyword", Address1: generation.GenerationID, Address2: "sess-a", Address3: "needle", LogicalBytes: 1},
			{Class: "fingerprint", Address1: generation.GenerationID, Address2: "e-fp", AddressBlob: fingerprint, LogicalBytes: 1},
			{Class: "exclusion", Address1: generation.GenerationID, RowID: exclusionSeq, LogicalBytes: 1},
		},
	}
	progress, err := store.CleanupBatch(ctx, plan, 5*time.Second, now)
	if err != nil {
		t.Fatalf("CleanupBatch: %v", err)
	}
	if progress.Cleaned != 5 {
		t.Fatalf("cleaned=%d, want 5", progress.Cleaned)
	}

	var leftover int
	if err = db.QueryRow(`
		SELECT
			(SELECT COUNT(*) FROM search_projection_session_summaries)+
			(SELECT COUNT(*) FROM search_projection_command_aggregates)+
			(SELECT COUNT(*) FROM search_projection_session_keywords)+
			(SELECT COUNT(*) FROM literal_search_fingerprints)+
			(SELECT COUNT(*) FROM search_projection_exclusions)
	`).Scan(&leftover); err != nil {
		t.Fatal(err)
	}
	if leftover != 0 {
		t.Fatalf("leftover rows=%d, want 0", leftover)
	}
}

func TestSearchProjectionCleanupWrongCompositeKeyIsDrift(t *testing.T) {
	store, db := newCapacityTestStore(t, nil)
	now := time.Date(2026, 6, 2, 0, 0, 0, 0, time.UTC)
	ctx := context.Background()
	generation, err := store.Start(ctx, defaultSearchProjectionCatchUpBudget(), now)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(`
		INSERT INTO search_projection_session_keywords(generation_id,session_id,keyword,occurrences,keyword_version)
		VALUES(?, 'sess-a', 'keep', 1, 1);
		UPDATE search_projection_state SET phase='cleanup',checkpoint=0 WHERE generation_id=?;
	`, generation.GenerationID, generation.GenerationID); err != nil {
		t.Fatal(err)
	}

	_, err = store.CleanupBatch(ctx, apptypes.ProjectionBatchPlan{
		GenerationID:     generation.GenerationID,
		Phase:            "cleanup",
		ExpectedRevision: generation.SourceRevision,
		Cleanup: []apptypes.ProjectionCleanupCandidate{{
			Class: "keyword", Address1: generation.GenerationID, Address2: "sess-a", Address3: "missing", LogicalBytes: 1,
		}},
	}, 5*time.Second, now)
	var drift *apptypes.SearchProjectionDriftError
	if !errors.As(err, &drift) {
		t.Fatalf("CleanupBatch() error = %v, want drift", err)
	}
	var kept int
	if err = db.QueryRow(`SELECT COUNT(*) FROM search_projection_session_keywords`).Scan(&kept); err != nil {
		t.Fatal(err)
	}
	if kept != 1 {
		t.Fatalf("kept=%d, want the unmatched keyword row", kept)
	}
}

func TestSearchProjectionCleanupSelectsPrimaryKeysNotRowID(t *testing.T) {
	store, db := newCapacityTestStore(t, nil)
	now := time.Date(2026, 6, 2, 0, 0, 0, 0, time.UTC)
	ctx := context.Background()
	generation, err := store.Start(ctx, defaultSearchProjectionCatchUpBudget(), now)
	if err != nil {
		t.Fatal(err)
	}
	other := "other-generation"
	fingerprint := []byte{9, 9, 9, 9, 9, 9, 9, 9, 9, 9, 9, 9, 9, 9, 9, 9}
	if _, err = db.Exec(`INSERT INTO search_projection_source_sequence(event_id) VALUES('e-old')`); err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(`INSERT INTO search_projection_session_keywords(generation_id,session_id,keyword,occurrences,keyword_version) VALUES(?, 'sess-old', 'stale', 1, 1)`, other); err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(`INSERT INTO literal_search_fingerprints(generation_id,source_sequence,event_id,fingerprint,fingerprint_version) SELECT ?, sequence, 'e-old', ?, 1 FROM search_projection_source_sequence WHERE event_id='e-old'`, other, fingerprint); err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(`UPDATE search_projection_state SET phase='cleanup',checkpoint=0,cleanup_scope='old' WHERE generation_id=?`, generation.GenerationID); err != nil {
		t.Fatal(err)
	}

	snapshot, err := store.SelectSnapshot(ctx, defaultSearchProjectionCatchUpBudget(), now)
	if err != nil {
		t.Fatal(err)
	}
	var sawKeyword, sawFingerprint bool
	for _, c := range snapshot.Cleanup {
		switch c.Class {
		case "keyword":
			sawKeyword = true
			if diff := cmp.Diff([]string{other, "sess-old", "stale"}, []string{c.Address1, c.Address2, c.Address3}); diff != "" {
				t.Fatalf("keyword address (-want +got):\n%s", diff)
			}
			if c.RowID != 0 {
				t.Fatalf("keyword RowID=%d, want 0 (not rowid)", c.RowID)
			}
		case "fingerprint":
			sawFingerprint = true
			if c.Address1 != other || c.Address2 != "e-old" {
				t.Fatalf("fingerprint address = %q/%q", c.Address1, c.Address2)
			}
			if diff := cmp.Diff(fingerprint, c.AddressBlob); diff != "" {
				t.Fatalf("fingerprint blob (-want +got):\n%s", diff)
			}
		}
	}
	if !sawKeyword || !sawFingerprint {
		t.Fatalf("cleanup candidates=%+v, want keyword and fingerprint", snapshot.Cleanup)
	}
}

func TestSearchProjectionKeywordAutoindexDuplicatesTheKey(t *testing.T) {
	// #1825 measurement: the implicit UNIQUE/PK autoindex is still a large
	// fraction of the keyword table. Conversion is not done here — COPY is
	// store-sized and DROP+reset discards a complete generation. Addressing
	// is what this issue ships.
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "store.db")
	store := NewDatabase(path, os.DirFS(filepath.Join("..", "..", "schema", "sqlite", "migrations")))
	if err := NewStoreManagementDatasource(store).Initialize(ctx); err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite", sqliteDSN(path))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	tx, err := db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 400; i++ {
		if _, err = tx.Exec(
			`INSERT INTO search_projection_session_keywords(generation_id,session_id,keyword,occurrences,keyword_version) VALUES(?,?,?,1,1)`,
			"gen-measure", "sess-measure", "kw-"+strconv.Itoa(i),
		); err != nil {
			t.Fatal(err)
		}
	}
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}
	var tableBytes, indexBytes int64
	if err = db.QueryRow(`SELECT COALESCE(SUM(pgsize),0) FROM dbstat WHERE name='search_projection_session_keywords'`).Scan(&tableBytes); err != nil {
		t.Fatal(err)
	}
	if err = db.QueryRow(`SELECT COALESCE(SUM(pgsize),0) FROM dbstat WHERE name LIKE 'sqlite_autoindex_search_projection_session_keywords%'`).Scan(&indexBytes); err != nil {
		t.Fatal(err)
	}
	if tableBytes == 0 || indexBytes == 0 {
		t.Fatalf("table=%d autoindex=%d, want both present (rowid table)", tableBytes, indexBytes)
	}
	if indexBytes*2 < tableBytes {
		t.Fatalf("autoindex=%d table=%d, want autoindex to be a large fraction of the table", indexBytes, tableBytes)
	}
}
