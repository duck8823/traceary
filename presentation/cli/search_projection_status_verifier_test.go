package cli

import (
	"context"
	"database/sql"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"github.com/duck8823/traceary/application/usecase"
	infra "github.com/duck8823/traceary/infrastructure/sqlite"
	sqliteschema "github.com/duck8823/traceary/schema/sqlite"
	_ "modernc.org/sqlite"
)

func TestStoreSearchProjectionStatusReportsRecentSourceBytesVerifier(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "store.db")
	migrations, err := sqliteschema.Migrations()
	if err != nil {
		t.Fatal(err)
	}
	database := infra.NewDatabase(path, migrations)
	if err := infra.NewStoreManagementDatasource(database).Initialize(ctx); err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	if _, err = db.Exec(`
		INSERT INTO search_projection_generation_lifecycle(generation_id,state,config_hash,source_revision,high_water)
		VALUES('gen-v','rebuilding','hash-v',0,1);
		UPDATE search_projection_state
		SET generation_id='gen-v',state='rebuilding',phase='source',config_hash='hash-v',recent_source_bytes=40
		WHERE singleton=1;
		INSERT INTO search_projection_recent_documents(generation_id,event_rowid,event_id,created_at_norm,body_text,decoded_bytes)
		VALUES('gen-v',1,'only','2026-01-01T00:00:00Z','xxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxx',40);
	`); err != nil {
		t.Fatal(err)
	}

	root := NewRootCLI(WithSearchProjection(usecase.NewSearchProjectionUsecase(database))).Command()
	root.SetArgs([]string{"store", "search-projection", "status"})
	var stdout strings.Builder
	root.SetOut(&stdout)
	if err := root.Execute(); err != nil {
		t.Fatal(err)
	}
	var payload map[string]any
	if err := json.Unmarshal([]byte(stdout.String()), &payload); err != nil {
		t.Fatalf("stdout %q is not JSON: %v", stdout.String(), err)
	}
	if payload["recent_source_bytes"] != float64(40) {
		t.Fatalf("recent_source_bytes=%v", payload["recent_source_bytes"])
	}
	if payload["recent_source_bytes_measured"] != float64(40) {
		t.Fatalf("recent_source_bytes_measured=%v", payload["recent_source_bytes_measured"])
	}
	if payload["recent_source_bytes_delta"] != float64(0) {
		t.Fatalf("recent_source_bytes_delta=%v", payload["recent_source_bytes_delta"])
	}

	if _, err = db.Exec(`UPDATE search_projection_state SET recent_source_bytes=1 WHERE singleton=1`); err != nil {
		t.Fatal(err)
	}
	root = NewRootCLI(WithSearchProjection(usecase.NewSearchProjectionUsecase(database))).Command()
	root.SetArgs([]string{"store", "search-projection", "status"})
	stdout.Reset()
	root.SetOut(&stdout)
	if err := root.Execute(); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal([]byte(stdout.String()), &payload); err != nil {
		t.Fatal(err)
	}
	if payload["recent_source_bytes_delta"] != float64(-39) {
		t.Fatalf("after tamper delta=%v stdout=%s", payload["recent_source_bytes_delta"], stdout.String())
	}
}
