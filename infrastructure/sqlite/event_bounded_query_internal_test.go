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

// The bounded query used to derive the visible-text prefix, its rune count and
// the canonical-envelope flag in SQL. Every one of those results was scanned
// and then overwritten by hydrateBoundedEvents from loadEventPlaintext, so the
// derivation was dead work even before the payload codec — and an encoded body
// is a BLOB that json_valid() reports as invalid, which would have turned the
// dead work into a silent wrong answer. #1685 D6 deleted it. This test pins the
// replacement contract: the query resolves identity and availability, and asks
// SQLite nothing about what the body contains.
func TestBoundedEventBodyQuery_DoesNotInterpretBodyContent(t *testing.T) {
	t.Parallel()

	normalized := strings.ToLower(strings.Join(strings.Fields(selectBoundedEventBodiesQuery), " "))
	for _, interpretation := range []string{
		"json_valid(", "json_type(", "json_extract(e.body", "visible_body", "substr(", "like ",
	} {
		if strings.Contains(normalized, interpretation) {
			t.Fatalf("bounded query interprets body content via %q: %s", interpretation, normalized)
		}
	}
	for _, projected := range []string{"e.body,", "e.body ", ", body,"} {
		if strings.Contains(normalized, projected) {
			t.Fatalf("bounded query transfers the stored body %q: %s", projected, normalized)
		}
	}
	if !strings.Contains(normalized, "e.body_availability") {
		t.Fatalf("bounded query must still resolve body availability: %s", normalized)
	}
	for _, forbidden := range []string{"command_text", "input_text", "output_text", "event_search_documents.body_text"} {
		if strings.Contains(normalized, forbidden) {
			t.Fatalf("bounded hydration selects forbidden payload source %q: %s", forbidden, normalized)
		}
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
