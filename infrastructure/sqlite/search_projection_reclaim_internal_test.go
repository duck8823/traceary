package sqlite

import (
	"context"
	"database/sql"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
)

func TestSearchProjectionFTSReclaimKeepsConstantLargeCorpusBounded(t *testing.T) {
	tests := []struct {
		name        string
		generations int
	}{
		{name: "three churn generations", generations: 3},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "store.db")
			migrations, err := fs.Sub(os.DirFS("../.."), "schema/sqlite/migrations")
			if err != nil {
				t.Fatal(err)
			}
			store := NewDatabase(path, migrations)
			if err = store.initialize(context.Background()); err != nil {
				t.Fatal(err)
			}
			db, err := sql.Open("sqlite", sqliteDSN(path))
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = db.Close() }()
			body := "Traceary projection payload with searchable trigrams. " + strings.Repeat("representative indexed text ", 1800)
			insertCorpus := func(generation int) {
				t.Helper()
				tx, txErr := db.Begin()
				if txErr != nil {
					t.Fatal(txErr)
				}
				for i := 0; i < 220; i++ {
					id := generation*220 + i + 1
					if _, txErr = tx.Exec(`INSERT INTO search_projection_recent_documents(generation_id,event_rowid,event_id,created_at_norm,body_text,decoded_bytes) VALUES(?,?,?,?,?,?)`, "fixture", id, fmt.Sprintf("event-%d", id), "2026-08-10T00:00:00Z", body, len(body)); txErr != nil {
						_ = tx.Rollback()
						t.Fatal(txErr)
					}
				}
				if txErr = tx.Commit(); txErr != nil {
					t.Fatal(txErr)
				}
			}
			shadowBytes := func() int64 {
				t.Helper()
				var bytes int64
				if err = db.QueryRow(`SELECT COALESCE(SUM(pgsize),0) FROM dbstat WHERE name LIKE 'search_projection_recent_fts_%'`).Scan(&bytes); err != nil {
					t.Fatal(err)
				}
				return bytes
			}
			insertCorpus(0)
			sizes := []int64{shadowBytes()}
			for generation := 1; generation <= tt.generations; generation++ {
				if _, err = db.Exec(`DELETE FROM search_projection_recent_documents WHERE generation_id='fixture'`); err != nil {
					t.Fatal(err)
				}
				insertCorpus(generation)
				tx, txErr := db.Begin()
				if txErr != nil {
					t.Fatal(txErr)
				}
				if txErr = reclaimSearchProjectionFTS(context.Background(), tx); txErr != nil {
					_ = tx.Rollback()
					t.Fatal(txErr)
				}
				if txErr = tx.Commit(); txErr != nil {
					t.Fatal(txErr)
				}
				sizes = append(sizes, shadowBytes())
			}
			increasing := true
			for i := 1; i < len(sizes); i++ {
				if sizes[i] <= sizes[i-1] {
					increasing = false
				}
			}
			if diff := cmp.Diff(false, increasing); diff != "" {
				t.Fatalf("shadow sizes grew monotonically (-want +got):\n%s\nsizes=%v", diff, sizes)
			}
			t.Logf("shadow-table sizes before/after churn: %v", sizes)
		})
	}
}

func TestSearchProjectionStatusReportsFTSShadowLogicalBytes(t *testing.T) {
	path := filepath.Join(t.TempDir(), "store.db")
	migrations, err := fs.Sub(os.DirFS("../.."), "schema/sqlite/migrations")
	if err != nil {
		t.Fatal(err)
	}
	store := NewDatabase(path, migrations)
	if err = store.initialize(context.Background()); err != nil {
		t.Fatal(err)
	}
	budget := projectionBudget()
	generation, err := store.Start(context.Background(), budget, time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite", sqliteDSN(path))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	if _, err = db.Exec(`UPDATE search_projection_state SET active_generation_id=? WHERE singleton=1`, generation.GenerationID); err != nil {
		t.Fatal(err)
	}
	body := strings.Repeat("representative indexed text ", 1800)
	for i := 0; i < 220; i++ {
		if _, err = db.Exec(`INSERT INTO search_projection_recent_documents(generation_id,event_rowid,event_id,created_at_norm,body_text,decoded_bytes) VALUES(?,?,?,?,?,?)`, generation.GenerationID, i+1, fmt.Sprintf("event-%d", i), "2026-08-10T00:00:00Z", body, len(body)); err != nil {
			t.Fatal(err)
		}
	}
	status, err := store.SearchProjectionStatus(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if diff := cmp.Diff(true, status.FTSLogicalBytes > status.RecentBytes); diff != "" {
		t.Fatalf("FTS logical bytes must describe shadow tables, not source text (-want +got):\n%s", diff)
	}
}
