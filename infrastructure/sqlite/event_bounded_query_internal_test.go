package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	_ "modernc.org/sqlite"
)

func TestBoundedEventBodyQuery_SelectsOnlyVisiblePrefix(t *testing.T) {
	t.Parallel()

	normalized := strings.ToLower(strings.Join(strings.Fields(selectBoundedEventBodiesQuery), " "))
	if !strings.Contains(normalized, "substr(visible_body, 1, ?)") {
		t.Fatalf("bounded query must select a visible-text prefix: %s", normalized)
	}
	if !strings.Contains(normalized, "length(visible_body)") {
		t.Fatalf("bounded query must retain response truncation provenance: %s", normalized)
	}
	finalSelectIndex := strings.LastIndex(normalized, ") select ")
	if finalSelectIndex < 0 {
		t.Fatalf("bounded query final projection not found: %s", normalized)
	}
	finalProjection := normalized[finalSelectIndex+2:]
	for _, unboundedProjection := range []string{
		"select e.body,",
		", e.body,",
		"select visible_body,",
	} {
		if strings.Contains(finalProjection, unboundedProjection) {
			t.Fatalf("bounded query selects an unbounded result column %q: %s", unboundedProjection, finalProjection)
		}
	}
	for _, forbidden := range []string{"command_text", "input_text", "output_text", "event_search_documents.body_text"} {
		if strings.Contains(normalized, forbidden) {
			t.Fatalf("bounded hydration selects forbidden payload source %q: %s", forbidden, normalized)
		}
	}
	if got := strings.Count(normalized, "json_valid("); got != 1 {
		t.Fatalf("canonical envelope predicate count = %d, want one shared classification: %s", got, normalized)
	}
	if !strings.Contains(
		normalized,
		"when classified.body_availability <> 'available' then ''",
	) {
		t.Fatalf("bounded query does not fail closed for unavailable bodies: %s", normalized)
	}
	if !strings.Contains(
		normalized,
		"char(10) || char(10) order by cast(key as integer)",
	) {
		t.Fatalf("canonical text aggregation does not own array-index order: %s", normalized)
	}
}

func TestBoundedEventBodyQuery_UsesEventIdentityIndex(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "traceary.db")
	database := NewDatabase(
		dbPath,
		os.DirFS(filepath.Join("..", "..", "schema", "sqlite", "migrations")),
	)
	if err := NewStoreManagementDatasource(database).Initialize(ctx); err != nil {
		t.Fatalf("Initialize() error = %v", err)
	}
	db, err := sql.Open("sqlite", sqliteDSN(dbPath))
	if err != nil {
		t.Fatalf("sql.Open() error = %v", err)
	}
	defer func() { _ = db.Close() }()

	encodedIDs, err := json.Marshal([]string{"event-1"})
	if err != nil {
		t.Fatalf("json.Marshal() error = %v", err)
	}
	rows, err := db.QueryContext(
		ctx,
		"EXPLAIN QUERY PLAN "+selectBoundedEventBodiesQuery,
		string(encodedIDs),
		500,
	)
	if err != nil {
		t.Fatalf("EXPLAIN QUERY PLAN error = %v", err)
	}
	defer func() { _ = rows.Close() }()
	var plan strings.Builder
	for rows.Next() {
		var id, parent, unused int
		var detail string
		if err := rows.Scan(&id, &parent, &unused, &detail); err != nil {
			t.Fatalf("scan query plan: %v", err)
		}
		plan.WriteString(detail)
		plan.WriteByte('\n')
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate query plan: %v", err)
	}
	normalized := strings.ToLower(plan.String())
	if !strings.Contains(normalized, "events") ||
		(!strings.Contains(normalized, "sqlite_autoindex_events_1") &&
			!strings.Contains(normalized, "primary key")) {
		t.Fatalf("bounded hydration does not seek events by identity:\n%s", plan.String())
	}
	if strings.Contains(normalized, "event_search_documents") ||
		strings.Contains(normalized, "event_search_fts") {
		t.Fatalf("bounded hydration reads search-document content:\n%s", plan.String())
	}
}
