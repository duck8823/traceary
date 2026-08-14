package sqlite

import (
	"context"
	"database/sql"
	"io/fs"
	"os"
	"path/filepath"
	"testing"

	"github.com/google/go-cmp/cmp"
)

func TestSearchProjectionStatusReportsRecentSourceBytesDrift(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "store.db")
	migrations, err := fs.Sub(os.DirFS("../.."), "schema/sqlite/migrations")
	if err != nil {
		t.Fatal(err)
	}
	store := NewDatabase(path, migrations)
	if err = store.initialize(ctx); err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite", sqliteDSN(path))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	if _, err = db.Exec(`
		INSERT INTO search_projection_generation_lifecycle(generation_id,state,config_hash,source_revision,high_water)
		VALUES('gen-new','rebuilding','hash-new',0,2);
		UPDATE search_projection_state
		SET generation_id='gen-new',active_generation_id='gen-old',state='rebuilding',phase='source',
		    config_hash='hash-new',recent_source_bytes=7
		WHERE singleton=1;
		INSERT INTO search_projection_recent_documents(generation_id,event_rowid,event_id,created_at_norm,body_text,decoded_bytes)
		VALUES('gen-old',1,'old','2026-01-01T00:00:00Z','old-body',20),
		      ('gen-new',2,'a','2026-01-01T00:00:01Z','abcdefg',7);
	`); err != nil {
		t.Fatal(err)
	}

	matched, err := store.SearchProjectionStatus(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if diff := cmp.Diff(int64(7), matched.RecentSourceBytes); diff != "" {
		t.Fatalf("cached (-want +got):\n%s", diff)
	}
	if diff := cmp.Diff(int64(7), matched.RecentSourceBytesMeasured); diff != "" {
		t.Fatalf("measured (-want +got):\n%s", diff)
	}
	if matched.RecentSourceBytesDelta != 0 || matched.RecentSourceBytesEvidence.Status != "complete" {
		t.Fatalf("matched status=%+v evidence=%+v", matched.RecentSourceBytesDelta, matched.RecentSourceBytesEvidence)
	}
	if matched.RecentBytes != 20 {
		t.Fatalf("RecentBytes=%d, want active generation 20, not the cache generation", matched.RecentBytes)
	}

	if _, err = db.Exec(`UPDATE search_projection_state SET recent_source_bytes=99 WHERE singleton=1`); err != nil {
		t.Fatal(err)
	}
	high, err := store.SearchProjectionStatus(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if high.RecentSourceBytes != 99 || high.RecentSourceBytesMeasured != 7 || high.RecentSourceBytesDelta != 92 {
		t.Fatalf("high cache status=%+v", high)
	}

	if _, err = db.Exec(`
		UPDATE search_projection_state SET recent_source_bytes=7 WHERE singleton=1;
		DELETE FROM search_projection_recent_documents WHERE event_id='a';
	`); err != nil {
		t.Fatal(err)
	}
	missing, err := store.SearchProjectionStatus(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if missing.RecentSourceBytes != 7 || missing.RecentSourceBytesMeasured != 0 || missing.RecentSourceBytesDelta != 7 {
		t.Fatalf("deleted-row status=%+v", missing)
	}
}
