package sqlite

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"strings"
	"testing"

	apptypes "github.com/duck8823/traceary/application/types"
)

func TestShippedEventPlansDoNotUseDroppedCreatedAtIndexes(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "traceary.db")
	database := NewDatabase(dbPath, os.DirFS(filepath.Join("..", "..", "schema", "sqlite", "migrations")))
	if err := NewStoreManagementDatasource(database).Initialize(ctx); err != nil {
		t.Fatalf("Initialize() error = %v", err)
	}
	db, err := sql.Open("sqlite", sqliteDSN(dbPath))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })

	from := "2026-01-01T00:00:00.000000000Z"
	to := "2026-01-02T00:00:00.000000000Z"
	searchSQL, searchArgs := buildTieredSearchCandidateQuery(
		apptypes.NewEventSearchCriteriaBuilder(10).Query("needle").Build(),
		"generation",
		false,
	)
	plans := []struct {
		name  string
		query string
		args  []any
	}{
		{
			name:  "list recent events",
			query: selectRecentEventsQuery,
			args:  []any{"", "", "", "", "", "", "", "", "", "", 0, "", "", "", "", 10, 0},
		},
		{
			name:  "search fingerprint-first",
			query: searchSQL,
			args:  searchArgs,
		},
		{
			name:  "report sessions",
			query: listReportSessionsQuery,
			args: []any{
				"", "", "", "", from, from, to, to, "", "", "", 10,
				"", "", "", "", from, from, to, to,
			},
		},
	}
	dropped := []string{
		"idx_events_created_at",
		"idx_events_session_created_at",
		"idx_events_session_created_at_id_desc",
		"idx_events_workspace_created_at",
		"idx_events_source_hook_time",
	}
	for _, plan := range plans {
		rows, err := db.QueryContext(ctx, "EXPLAIN QUERY PLAN "+plan.query, plan.args...)
		if err != nil {
			t.Fatalf("%s EXPLAIN: %v", plan.name, err)
		}
		joined := strings.ToLower(joinQueryPlan(t, rows))
		_ = rows.Close()
		masked := joined
		for _, keep := range []string{
			"idx_events_created_at_norm",
			"idx_events_session_created_at_norm",
			"idx_events_workspace_created_at_norm",
			"idx_events_source_hook_created_at_norm",
		} {
			masked = strings.ReplaceAll(masked, keep, "kept_norm")
		}
		for _, name := range dropped {
			if strings.Contains(masked, strings.ToLower(name)) {
				t.Fatalf("%s plan still names %s:\n%s", plan.name, name, joined)
			}
		}
	}
}
