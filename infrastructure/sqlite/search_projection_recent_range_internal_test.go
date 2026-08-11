package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
	_ "modernc.org/sqlite"
)

func TestSearchProjectionStatusReportsRecentRangeForStatusGeneration(t *testing.T) {
	t.Parallel()

	// bothGenerations holds documents for the published generation and for one
	// being built, with deliberately wider timestamps on the latter: any case
	// that reports the building generation's range is reporting coverage
	// searchProjectionReadReady refuses to serve.
	const bothGenerations = `INSERT INTO search_projection_recent_documents(generation_id,event_rowid,event_id,created_at_norm,body_text,decoded_bytes) VALUES('current',1,'active-old','2026-08-01T00:00:00Z','active',1),('current',2,'active-new','2026-08-03T00:00:00Z','active',1),('next',3,'building-old','2020-01-01T00:00:00Z','building',1),('next',4,'building-new','2099-01-01T00:00:00Z','building',1);`

	for _, test := range []struct {
		name string
		// building is the generation_id the status row carries, and state is
		// the projection state. They differ from the active generation exactly
		// while a rebuild is in flight.
		building   string
		state      string
		active     string
		rows       string
		wantOldest string
		wantNewest string
		wantJSON   map[string]any
	}{
		{
			name:       "reports the active generation's range when the projection is read-ready",
			building:   "current",
			state:      "complete",
			active:     "current",
			rows:       bothGenerations,
			wantOldest: "2026-08-01T00:00:00Z",
			wantNewest: "2026-08-03T00:00:00Z",
		},
		{
			// searchProjectionReadReady (event_search_query.go:49) refuses the
			// projection unless state is complete, so during a rebuild the
			// previous generation's rows answer nothing. Reporting their range
			// would claim full-text coverage for the period in which broad
			// queries decode every candidate and can return index_incomplete.
			name:     "omits the range while a rebuild is in flight",
			building: "next",
			state:    "rebuilding",
			active:   "current",
			rows:     bothGenerations,
			wantJSON: map[string]any{},
		},
		{
			// A canonical mutation flips the bounded side to drifted and
			// clears the active generation (search_projection_rebuild.go:839).
			name:     "omits the range when the projection has drifted",
			building: "current",
			state:    "drifted",
			active:   "",
			rows:     bothGenerations,
			wantJSON: map[string]any{},
		},
		{
			name:     "omits the range when the active tier is empty",
			building: "current",
			state:    "complete",
			active:   "current",
			wantJSON: map[string]any{},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			store, db := newRecentRangeStatusTestStore(t)
			if _, err := db.Exec(`INSERT INTO search_projection_generation_lifecycle(generation_id,state,config_hash,source_revision,high_water) VALUES('current','complete','hash',0,0),('next','rebuilding','hash',0,0);`); err != nil {
				t.Fatal(err)
			}
			if _, err := db.Exec(`UPDATE search_projection_state SET generation_id=?,active_generation_id=NULLIF(?,''),state=?,config_hash='hash' WHERE singleton=1`, test.building, test.active, test.state); err != nil {
				t.Fatal(err)
			}
			if test.rows != "" {
				if _, err := db.Exec(test.rows); err != nil {
					t.Fatal(err)
				}
			}

			status, err := store.SearchProjectionStatus(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			if test.wantJSON == nil {
				if diff := cmp.Diff(test.wantOldest, status.RecentOldestNorm); diff != "" {
					t.Fatalf("recent oldest (-want +got):\n%s", diff)
				}
				if diff := cmp.Diff(test.wantNewest, status.RecentNewestNorm); diff != "" {
					t.Fatalf("recent newest (-want +got):\n%s", diff)
				}
				return
			}

			encoded, err := json.Marshal(status)
			if err != nil {
				t.Fatal(err)
			}
			var payload map[string]any
			if err := json.Unmarshal(encoded, &payload); err != nil {
				t.Fatal(err)
			}
			gotFields := make(map[string]any)
			for _, field := range []string{"recent_oldest_norm", "recent_newest_norm"} {
				if _, ok := payload[field]; ok {
					gotFields[field] = payload[field]
				}
			}
			if diff := cmp.Diff(test.wantJSON, gotFields); diff != "" {
				t.Fatalf("empty recent range fields (-want +got):\n%s\nJSON: %s", diff, encoded)
			}
		})
	}
}

func TestSelectSearchProjectionRecentRangeUsesEvictionIndex(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "projection.db")
	db, err := sql.Open("sqlite", sqliteDSN(path))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if _, err := db.Exec(`CREATE TABLE search_projection_state(singleton INTEGER PRIMARY KEY,generation_id TEXT,active_generation_id TEXT,state TEXT NOT NULL); INSERT INTO search_projection_state VALUES(1,'next','current','complete'); CREATE TABLE search_projection_recent_documents(document_id INTEGER PRIMARY KEY,generation_id TEXT NOT NULL,event_rowid INTEGER NOT NULL,created_at_norm TEXT NOT NULL); CREATE INDEX idx_search_projection_recent_eviction ON search_projection_recent_documents(generation_id,created_at_norm,event_rowid);`); err != nil {
		t.Fatal(err)
	}

	rows, err := db.QueryContext(context.Background(), "EXPLAIN QUERY PLAN "+selectSearchProjectionRecentRangeSQL)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = rows.Close() })
	var plan []string
	for rows.Next() {
		var id, parent, unused int
		var detail string
		if err := rows.Scan(&id, &parent, &unused, &detail); err != nil {
			t.Fatal(err)
		}
		plan = append(plan, detail)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(plan, "\n")
	t.Log(joined)
	if !strings.Contains(joined, "SEARCH search_projection_recent_documents USING COVERING INDEX idx_search_projection_recent_eviction (generation_id=?)") {
		t.Fatalf("plan does not use the generation-scoped eviction index: %s", joined)
	}
	if strings.Contains(joined, "SCAN search_projection_recent_documents") || strings.Contains(joined, "TEMP B-TREE") {
		t.Fatalf("plan scans recent documents or uses a temp b-tree: %s", joined)
	}
}

func newRecentRangeStatusTestStore(t *testing.T) (*Database, *sql.DB) {
	t.Helper()
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "store.db")
	migrations := os.DirFS(filepath.Join("..", "..", "schema", "sqlite", "migrations"))
	store := NewDatabase(path, migrations)
	if err := NewStoreManagementDatasource(store).Initialize(ctx); err != nil {
		t.Fatalf("Initialize() error = %v", err)
	}
	db, err := sql.Open("sqlite", sqliteDSN(path))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return store, db
}
