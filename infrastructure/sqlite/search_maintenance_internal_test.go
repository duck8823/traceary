package sqlite

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	apptypes "github.com/duck8823/traceary/application/types"
	"github.com/duck8823/traceary/domain/model"
)

func TestSearchMaintenanceRetireRestoreIsBoundedAndResumable(t *testing.T) {
	ctx := context.Background()
	database := NewDatabase(filepath.Join(t.TempDir(), "store.db"), os.DirFS(filepath.Join("..", "..", "schema", "sqlite", "migrations")))
	if err := NewStoreManagementDatasource(database).Initialize(ctx); err != nil {
		t.Fatal(err)
	}
	raw, err := database.open(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"e1", "e2", "e3"} {
		if _, err = raw.ExecContext(ctx, `INSERT INTO events(id,kind,client,agent,session_id,workspace,body,created_at) VALUES(?,'note','codex','codex','s','w',?,?)`, id, "body "+id, "2026-01-01T00:00:00Z"); err != nil {
			t.Fatal(err)
		}
	}
	if _, err = raw.ExecContext(ctx, `UPDATE literal_search_projection_state SET generation_id='g',high_water=(SELECT MAX(sequence) FROM search_projection_source_sequence),state='complete'; UPDATE search_projection_state SET generation_id='g',active_generation_id='g',high_water=(SELECT MAX(sequence) FROM search_projection_source_sequence),state='complete',phase='complete'; UPDATE search_maintenance_control SET target_adopted=1`); err != nil {
		t.Fatal(err)
	}
	_ = raw.Close()
	snapshot, err := database.SearchRetirementSnapshot(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = database.BeginSearchRetirement(ctx, "opaque", snapshot); err != nil {
		t.Fatal(err)
	}
	first, err := database.RetireLegacySearchBatch(ctx, 1)
	if err != nil {
		t.Fatal(err)
	}
	if first.RowsProcessed != 1 || first.Phase != model.SearchMaintenanceRetiring {
		t.Fatalf("first=%+v", first)
	}
	// Reopening the adapter resumes the persisted cursor rather than guessing
	// from table shape.
	second, err := NewDatabase(database.Path(), database.migrations).RetireLegacySearchBatch(ctx, 10)
	if err != nil {
		t.Fatal(err)
	}
	if second.Phase != model.SearchMaintenanceRetired {
		t.Fatalf("second=%+v", second)
	}
	if second.LogicalBytesBefore <= 0 || second.LogicalBytesAfter != 0 || second.PhysicalBytesBefore <= 0 || second.PhysicalBytesAfter <= 0 {
		t.Fatalf("missing before/after byte attribution: %+v", second)
	}
	raw, err = database.open(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = raw.ExecContext(ctx, `INSERT INTO events(id,kind,client,agent,session_id,workspace,body,created_at) VALUES('e4','note','codex','codex','s','w','new body','2026-01-02T00:00:00Z')`); err != nil {
		t.Fatal(err)
	}
	var docs int
	if err = raw.QueryRowContext(ctx, `SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name IN('event_search_documents','event_search_fts')`).Scan(&docs); err != nil {
		t.Fatal(err)
	}
	_ = raw.Close()
	if docs != 0 {
		t.Fatalf("retired legacy structures=%d want=0", docs)
	}
	if _, err = database.BeginSearchRestore(ctx); err != nil {
		t.Fatal(err)
	}
	var restored apptypes.SearchMaintenanceReport
	for {
		report, restoreErr := database.RestoreLegacySearchBatch(ctx, 2)
		if restoreErr != nil {
			t.Fatal(restoreErr)
		}
		restored = report
		if report.Complete {
			break
		}
	}
	if restored.LogicalBytesAfter <= 0 || restored.PhysicalBytesAfter <= 0 {
		t.Fatalf("missing restore byte attribution: %+v", restored)
	}
	raw, err = database.open(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = raw.Close() }()
	if err = raw.QueryRowContext(ctx, `SELECT COUNT(*) FROM event_search_documents`).Scan(&docs); err != nil {
		t.Fatal(err)
	}
	if docs != 4 {
		t.Fatalf("restored documents=%d want=4", docs)
	}
	if _, err = raw.ExecContext(ctx, `INSERT INTO events(id,kind,client,agent,session_id,workspace,body,created_at) VALUES('e5','note','codex','codex','s','w','later','2026-01-03T00:00:00Z')`); err != nil {
		t.Fatal(err)
	}
	if err = raw.QueryRowContext(ctx, `SELECT COUNT(*) FROM event_search_documents`).Scan(&docs); err != nil {
		t.Fatal(err)
	}
	if docs != 5 {
		t.Fatalf("restored writer documents=%d", docs)
	}
}

func TestPersistedTieredAuthorityPreservesDescendingContinuationAndFailsClosed(t *testing.T) {
	ctx := context.Background()
	database := NewDatabase(filepath.Join(t.TempDir(), "store.db"), os.DirFS(filepath.Join("..", "..", "schema", "sqlite", "migrations")))
	if err := NewStoreManagementDatasource(database).Initialize(ctx); err != nil {
		t.Fatal(err)
	}
	raw, err := database.open(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for i, id := range []string{"older", "newer"} {
		at := time.Date(2026, 1, 1, 0, 0, i, 0, time.UTC).Format(time.RFC3339Nano)
		if _, err = raw.ExecContext(ctx, `INSERT INTO events(id,kind,client,agent,session_id,workspace,body,created_at) VALUES(?,'note','codex','codex','s','w','find needle',?)`, id, at); err != nil {
			t.Fatal(err)
		}
	}
	_, err = raw.ExecContext(ctx, `UPDATE literal_search_projection_state SET generation_id='g',high_water=(SELECT MAX(sequence) FROM search_projection_source_sequence),state='complete'; UPDATE search_projection_state SET generation_id='g',active_generation_id='g',high_water=(SELECT MAX(sequence) FROM search_projection_source_sequence),state='complete',phase='complete'; UPDATE search_maintenance_control SET authority='tiered',phase='retired'`)
	if err != nil {
		t.Fatal(err)
	}
	_ = raw.Close()
	datasource := NewEventDatasource(database)
	criteria := apptypes.NewEventSearchCriteriaBuilder(1).Query("needle").Build()
	page, err := datasource.SearchPage(ctx, criteria)
	if err != nil {
		t.Fatal(err)
	}
	if len(page) != 1 || page[0].EventID().String() != "newer" {
		t.Fatalf("first page=%v", eventIDs(page))
	}
	anchor, err := apptypes.EventPageAnchorOf(page[0].CreatedAt(), page[0].EventID())
	if err != nil {
		t.Fatal(err)
	}
	page, err = datasource.SearchPage(ctx, apptypes.NewEventSearchCriteriaBuilder(1).Query("needle").PageAnchor(anchor).Build())
	if err != nil {
		t.Fatal(err)
	}
	if len(page) != 1 || page[0].EventID().String() != "older" {
		t.Fatalf("continuation=%v", eventIDs(page))
	}
	raw, err = database.open(ctx)
	if err != nil {
		t.Fatal(err)
	}
	_, err = raw.ExecContext(ctx, `UPDATE literal_search_projection_state SET state='stale'`)
	_ = raw.Close()
	if err != nil {
		t.Fatal(err)
	}
	if _, err = datasource.SearchPage(ctx, criteria); err == nil {
		t.Fatal("stale tiered projection did not fail closed")
	}
}

func eventIDs(events []*model.Event) []string {
	ids := make([]string, 0, len(events))
	for _, event := range events {
		ids = append(ids, event.EventID().String())
	}
	return ids
}

func TestSearchMaintenanceRestoreFailureDoesNotSwitchAuthority(t *testing.T) {
	ctx := context.Background()
	database := NewDatabase(filepath.Join(t.TempDir(), "store.db"), os.DirFS(filepath.Join("..", "..", "schema", "sqlite", "migrations")))
	if err := NewStoreManagementDatasource(database).Initialize(ctx); err != nil {
		t.Fatal(err)
	}
	raw, err := database.open(ctx)
	if err != nil {
		t.Fatal(err)
	}
	_, err = raw.ExecContext(ctx, `INSERT INTO events(id,kind,client,agent,session_id,workspace,body,created_at,body_codec,body_plaintext_bytes,body_encoded_bytes) VALUES('bad','note','codex','codex','s','w',X'00','2026-01-01T00:00:00Z','zstd',1,1); UPDATE literal_search_projection_state SET generation_id='g',high_water=(SELECT MAX(sequence) FROM search_projection_source_sequence),state='complete'; UPDATE search_maintenance_control SET target_adopted=1`)
	if err != nil {
		t.Fatal(err)
	}
	_ = raw.Close()
	snapshot, err := database.SearchRetirementSnapshot(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = database.BeginSearchRetirement(ctx, "opaque", snapshot); err != nil {
		t.Fatal(err)
	}
	if _, err = database.RetireLegacySearchBatch(ctx, 10); err != nil {
		t.Fatal(err)
	}
	if _, err = database.BeginSearchRestore(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err = database.RestoreLegacySearchBatch(ctx, 10); err == nil {
		t.Fatal("corrupt canonical payload restored")
	}
	status, err := database.SearchMaintenanceStatus(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if status.Authority != model.SearchAuthorityTiered || status.Phase != model.SearchMaintenanceRestoring {
		t.Fatalf("failure switched authority: %+v", status)
	}
}

func TestMigration40ContainsOnlyAdditiveControlDDL(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("..", "..", "schema", "sqlite", "migrations", "000040_add_search_maintenance_control.sql"))
	if err != nil {
		t.Fatal(err)
	}
	normalized := string(data)
	for _, forbidden := range []string{"DROP ", "DELETE ", "VACUUM"} {
		if containsFold(normalized, forbidden) {
			t.Fatalf("migration contains %q", forbidden)
		}
	}
}
func containsFold(value, needle string) bool {
	return len(value) >= len(needle) && indexFold(value, needle) >= 0
}
func indexFold(value, needle string) int {
	for i := 0; i+len(needle) <= len(value); i++ {
		match := true
		for j := range needle {
			a, b := value[i+j], needle[j]
			if a >= 'a' && a <= 'z' {
				a -= 32
			}
			if b >= 'a' && b <= 'z' {
				b -= 32
			}
			if a != b {
				match = false
				break
			}
		}
		if match {
			return i
		}
	}
	return -1
}

func TestAdoptSearchRetirementTargetRotatesKeyAndInvalidatesProjection(t *testing.T) {
	ctx := context.Background()
	database := NewDatabase(filepath.Join(t.TempDir(), "store.db"), os.DirFS(filepath.Join("..", "..", "schema", "sqlite", "migrations")))
	if err := NewStoreManagementDatasource(database).Initialize(ctx); err != nil {
		t.Fatal(err)
	}
	before, err := database.SearchRetirementSnapshot(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = database.AdoptSearchRetirementTarget(ctx); err != nil {
		t.Fatal(err)
	}
	after, err := database.SearchRetirementSnapshot(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !after.TargetAdopted || string(before.CursorKey) == string(after.CursorKey) || after.ProjectionState != "stale" {
		t.Fatalf("target adoption did not rotate and invalidate")
	}
}
