package sqlite

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
	_ "modernc.org/sqlite"
)

func TestSearchProjectionControlStatusQueryReadsOnlySingletonState(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "traceary.db")
	database := NewDatabase(dbPath, os.DirFS(filepath.Join("..", "..", "schema", "sqlite", "migrations")))
	if err := NewStoreManagementDatasource(database).Initialize(ctx); err != nil {
		t.Fatalf("Initialize() error = %v", err)
	}
	db, err := sql.Open("sqlite", sqliteDSN(dbPath))
	if err != nil {
		t.Fatalf("sql.Open() error = %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	rows, err := db.QueryContext(ctx, "EXPLAIN QUERY PLAN "+selectSearchProjectionControlStatusSQL)
	if err != nil {
		t.Fatalf("EXPLAIN QUERY PLAN error = %v", err)
	}
	t.Cleanup(func() { _ = rows.Close() })
	var plan []string
	for rows.Next() {
		var id, parent, unused int
		var detail string
		if err := rows.Scan(&id, &parent, &unused, &detail); err != nil {
			t.Fatalf("scan query plan: %v", err)
		}
		plan = append(plan, detail)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate query plan: %v", err)
	}

	for _, test := range []struct {
		name string
		want []string
	}{
		{
			name: "referenced objects",
			want: []string{"search_projection_inventory_state", "search_projection_state"},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			got := referencedSearchProjectionControlStatusObjects(selectSearchProjectionControlStatusSQL, strings.Join(plan, "\n"))
			if diff := cmp.Diff(test.want, got); diff != "" {
				t.Fatalf("query plan referenced unexpected objects (-want +got):\n%s\nplan:\n%s", diff, strings.Join(plan, "\n"))
			}
		})
	}
}

func referencedSearchProjectionControlStatusObjects(query, plan string) []string {
	queryTokens := strings.FieldsFunc(query, func(r rune) bool {
		return (r < 'a' || r > 'z') && (r < 'A' || r > 'Z') && (r < '0' || r > '9') && r != '_'
	})
	planTokens := make(map[string]struct{})
	for _, token := range strings.FieldsFunc(plan, func(r rune) bool {
		return (r < 'a' || r > 'z') && (r < 'A' || r > 'Z') && (r < '0' || r > '9') && r != '_'
	}) {
		planTokens[strings.ToLower(token)] = struct{}{}
	}

	objects := make([]string, 0)
	for index := 0; index < len(queryTokens); index++ {
		if !strings.EqualFold(queryTokens[index], "from") && !strings.EqualFold(queryTokens[index], "join") {
			continue
		}
		if index+1 >= len(queryTokens) {
			continue
		}
		object := queryTokens[index+1]
		aliases := []string{object}
		if index+3 < len(queryTokens) && strings.EqualFold(queryTokens[index+2], "as") {
			aliases = append(aliases, queryTokens[index+3])
		} else if index+2 < len(queryTokens) && !isSearchProjectionControlStatusSQLKeyword(queryTokens[index+2]) {
			aliases = append(aliases, queryTokens[index+2])
		}
		for _, alias := range aliases {
			if _, ok := planTokens[strings.ToLower(alias)]; ok {
				objects = append(objects, object)
				break
			}
		}
	}
	sort.Strings(objects)
	return objects
}

func isSearchProjectionControlStatusSQLKeyword(token string) bool {
	switch strings.ToLower(token) {
	case "where", "join", "left", "right", "inner", "outer", "on", "group", "order", "limit", "union":
		return true
	default:
		return false
	}
}
