package sqlite_test

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"

	infra "github.com/duck8823/traceary/infrastructure/sqlite"
)

func TestOperatorCostInspectorMeasuresProjectionAggregates(t *testing.T) {
	path := filepath.Join(t.TempDir(), "operator-cost.db")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 8, 15, 12, 0, 0, 0, time.UTC)
	recent := now.Add(-24 * time.Hour).Format(time.RFC3339Nano)
	old := now.AddDate(0, 0, -120).Format(time.RFC3339Nano)
	statements := []string{
		`CREATE TABLE event_metadata_projection (
			id TEXT PRIMARY KEY,
			kind TEXT NOT NULL,
			session_id TEXT NOT NULL,
			created_at_norm TEXT,
			body_stored_bytes INTEGER
		)`,
		`CREATE TABLE session_refinements (session_id TEXT PRIMARY KEY)`,
		`INSERT INTO event_metadata_projection VALUES ('e1', 'transcript', 's1', '` + recent + `', 2000)`,
		`INSERT INTO event_metadata_projection VALUES ('e2', 'transcript', 's1', '` + old + `', 4000)`,
		`INSERT INTO event_metadata_projection VALUES ('e3', 'prompt', 's2', '` + recent + `', 500)`,
		`INSERT INTO session_refinements VALUES ('s1')`,
	}
	for i, statement := range statements {
		if _, execErr := db.Exec(statement); execErr != nil {
			t.Fatalf("exec fixture %d: %v", i, execErr)
		}
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	report, err := infra.NewOperatorCostInspector(infra.NewDatabase(path, nil)).InspectOperatorCost(context.Background(), now, 16500)
	if err != nil {
		t.Fatalf("InspectOperatorCost() error = %v", err)
	}
	if report.EventCount != 3 || report.SessionCount != 2 {
		t.Fatalf("counts event=%d session=%d", report.EventCount, report.SessionCount)
	}
	if report.RetainedSourceBytes != 6500 || report.FoldableSourceBytes != 4000 || report.UndiscardableSourceBytes != 2500 {
		t.Fatalf("bytes retained=%d foldable=%d undiscardable=%d", report.RetainedSourceBytes, report.FoldableSourceBytes, report.UndiscardableSourceBytes)
	}
	if report.WindowEventCount != 2 || report.WindowSessionCount != 2 {
		t.Fatalf("window events=%d sessions=%d", report.WindowEventCount, report.WindowSessionCount)
	}
	if report.Evidence.Status != "complete" {
		t.Fatalf("evidence = %+v", report.Evidence)
	}
	if report.ProjectedUndiscardableBytesPerMonth <= 0 {
		t.Fatalf("projected = %d", report.ProjectedUndiscardableBytesPerMonth)
	}
	if diff := cmp.Diff("traceary.operator_cost/v1", report.SchemaVersion); diff != "" {
		t.Fatalf("schema %s", diff)
	}
}

func TestOperatorCostInspectorSkipsWithoutProjection(t *testing.T) {
	path := filepath.Join(t.TempDir(), "empty.db")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`CREATE TABLE sample (value TEXT)`); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	report, err := infra.NewOperatorCostInspector(infra.NewDatabase(path, nil)).InspectOperatorCost(context.Background(), time.Now().UTC(), 100)
	if err != nil {
		t.Fatal(err)
	}
	if report.Evidence.Status != "skipped" {
		t.Fatalf("evidence = %+v", report.Evidence)
	}
}
