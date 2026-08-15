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

// TestSearchProjectionStatusKeepsGenerationScopedFieldsOnOneSnapshot is
// #1839: a cutover committed between two generation-scoped reads must not
// mix counters from two generations.
func TestSearchProjectionStatusKeepsGenerationScopedFieldsOnOneSnapshot(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "store.db")
	migrations, err := fs.Sub(os.DirFS("../.."), "schema/sqlite/migrations")
	if err != nil {
		t.Fatal(err)
	}
	store := NewDatabase(path, migrations)
	if err := store.initialize(ctx); err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite", sqliteDSN(path))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	if _, err = db.Exec(`
		INSERT INTO search_projection_generation_lifecycle(generation_id,state,config_hash,source_revision,high_water)
		VALUES('gen-old','complete','hash-old',0,1),('gen-new','complete','hash-new',0,3);
		UPDATE search_projection_state
		   SET generation_id='gen-old',active_generation_id='gen-old',state='complete',phase='complete',
		       config_hash='hash-old',recent_source_bytes=10
		 WHERE singleton=1;
		INSERT INTO search_projection_source_sequence(event_id) VALUES('old-1'),('new-1'),('new-2'),('new-3');
		INSERT INTO search_projection_recent_documents(generation_id,event_rowid,event_id,created_at_norm,body_text,decoded_bytes)
		VALUES('gen-old',1,'old-1','2026-01-01T00:00:00Z','old',10),
		      ('gen-new',2,'new-1','2026-02-01T00:00:00Z','n1',10),
		      ('gen-new',3,'new-2','2026-02-02T00:00:00Z','n2',10),
		      ('gen-new',4,'new-3','2026-02-03T00:00:00Z','n3',10);
		INSERT INTO search_projection_session_summaries(generation_id,session_id,event_count,summary_text,projection_version,summary_version)
		VALUES('gen-old','sess-old',1,'old summary',1,1),
		      ('gen-new','sess-n1',1,'n1',1,1),
		      ('gen-new','sess-n2',1,'n2',1,1),
		      ('gen-new','sess-n3',1,'n3',1,1);
		INSERT INTO search_projection_session_keywords(generation_id,session_id,keyword,occurrences,keyword_version)
		VALUES('gen-old','sess-old','old',1,1),
		      ('gen-new','sess-n1','n1',1,1),
		      ('gen-new','sess-n2','n2',1,1),
		      ('gen-new','sess-n3','n3',1,1);
		INSERT INTO literal_search_fingerprints(generation_id,source_sequence,event_id,fingerprint,fingerprint_version)
		SELECT 'gen-old',sequence,'old-1',randomblob(16),1 FROM search_projection_source_sequence WHERE event_id='old-1';
		INSERT INTO literal_search_fingerprints(generation_id,source_sequence,event_id,fingerprint,fingerprint_version)
		SELECT 'gen-new',sequence,event_id,randomblob(16),1 FROM search_projection_source_sequence WHERE event_id IN('new-1','new-2','new-3');
		INSERT INTO search_projection_exclusions(generation_id,source_sequence,event_id,class,measured_bytes,byte_limit)
		SELECT 'gen-old',sequence,'old-1','stored_bytes',10,1 FROM search_projection_source_sequence WHERE event_id='old-1';
		INSERT INTO search_projection_exclusions(generation_id,source_sequence,event_id,class,measured_bytes,byte_limit)
		SELECT 'gen-new',sequence,event_id,'stored_bytes',10,1 FROM search_projection_source_sequence WHERE event_id IN('new-1','new-2');
	`); err != nil {
		t.Fatalf("seed generations: %v", err)
	}

	store.SetStatusGenerationReadHookForTest(func() {
		if _, hookErr := db.Exec(`
			UPDATE search_projection_state
			   SET generation_id='gen-new',active_generation_id='gen-new',config_hash='hash-new',recent_source_bytes=30
			 WHERE singleton=1`); hookErr != nil {
			t.Errorf("cutover between reads: %v", hookErr)
		}
	})
	defer store.SetStatusGenerationReadHookForTest(nil)

	status, err := store.SearchProjectionStatus(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if diff := cmp.Diff(int64(1), status.RecentDocuments); diff != "" {
		t.Fatalf("recent_documents (-want old gen +got):\n%s", diff)
	}
	if diff := cmp.Diff(int64(1), status.SummarySessions); diff != "" {
		t.Fatalf("summary_sessions (-want old gen +got):\n%s", diff)
	}
	if diff := cmp.Diff(int64(1), status.KeywordRows); diff != "" {
		t.Fatalf("keyword_rows (-want old gen +got):\n%s", diff)
	}
	if diff := cmp.Diff(int64(1), status.FingerprintRows); diff != "" {
		t.Fatalf("fingerprint_rows (-want old gen +got):\n%s", diff)
	}
	if diff := cmp.Diff("gen-old", status.ExclusionGenerationID); diff != "" {
		t.Fatalf("exclusion generation (-want old gen +got):\n%s", diff)
	}
	if diff := cmp.Diff(int64(1), status.ExclusionCount); diff != "" {
		t.Fatalf("exclusion_count (-want old gen +got):\n%s", diff)
	}
	if diff := cmp.Diff(int64(10), status.RecentSourceBytesMeasured); diff != "" {
		t.Fatalf("recent_source_bytes_measured (-want old gen +got):\n%s", diff)
	}
	var published string
	if err := db.QueryRow(`SELECT active_generation_id FROM search_projection_state WHERE singleton=1`).Scan(&published); err != nil {
		t.Fatal(err)
	}
	if diff := cmp.Diff("gen-new", published); diff != "" {
		t.Fatalf("cutover must have published gen-new (-want +got):\n%s", diff)
	}
}
