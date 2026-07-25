package sqlite

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	apptypes "github.com/duck8823/traceary/application/types"
	"github.com/duck8823/traceary/domain/types"
)

func TestBoundedLegacySearchScope_RequiresWorkspaceSessionOrClosedInterval(t *testing.T) {
	t.Parallel()

	now := time.Now()
	tests := []struct {
		name     string
		criteria apptypes.EventSearchCriteria
		want     bool
	}{
		{
			name:     "workspace",
			criteria: apptypes.NewEventSearchCriteriaBuilder(1).Workspace("workspace").Build(),
			want:     true,
		},
		{
			name:     "session",
			criteria: apptypes.NewEventSearchCriteriaBuilder(1).SessionID("session").Build(),
			want:     true,
		},
		{
			name:     "closed interval",
			criteria: apptypes.NewEventSearchCriteriaBuilder(1).From(now).To(now.Add(time.Hour)).Build(),
			want:     true,
		},
		{
			name:     "from only",
			criteria: apptypes.NewEventSearchCriteriaBuilder(1).From(now).Build(),
		},
		{
			name:     "to only",
			criteria: apptypes.NewEventSearchCriteriaBuilder(1).To(now).Build(),
		},
	}
	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := hasBoundedLegacySearchScope(tc.criteria); got != tc.want {
				t.Fatalf("hasBoundedLegacySearchScope() = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestEventSearchFTSQuery_UsesVirtualTableIndex(t *testing.T) {
	t.Parallel()

	database := NewDatabase(
		filepath.Join(t.TempDir(), "traceary.db"),
		os.DirFS(filepath.Join("..", "..", "schema", "sqlite", "migrations")),
	)
	if err := NewStoreManagementDatasource(database).Initialize(context.Background()); err != nil {
		t.Fatalf("Initialize() error = %v", err)
	}
	db, err := database.open(context.Background())
	if err != nil {
		t.Fatalf("open() error = %v", err)
	}
	defer func() { _ = db.Close() }()

	criteria := apptypes.NewEventSearchCriteriaBuilder(20).
		Query("literal needle").
		Workspace(types.Workspace("github.com/duck8823/traceary")).
		Build()
	query, args := buildFTSEventIDsQuery(criteria, criteria.Query())
	rows, err := db.QueryContext(context.Background(), "EXPLAIN QUERY PLAN "+query, args...)
	if err != nil {
		t.Fatalf("EXPLAIN QUERY PLAN error = %v", err)
	}
	defer func() { _ = rows.Close() }()

	var plan strings.Builder
	for rows.Next() {
		var id, parent, notUsed int
		var detail string
		if err := rows.Scan(&id, &parent, &notUsed, &detail); err != nil {
			t.Fatalf("scan query plan: %v", err)
		}
		plan.WriteString(detail)
		plan.WriteByte('\n')
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate query plan: %v", err)
	}
	normalized := strings.ToLower(plan.String())
	if !strings.Contains(normalized, "event_search_fts") ||
		!strings.Contains(normalized, "virtual table index") {
		t.Fatalf("query plan does not use the FTS virtual-table index:\n%s", plan.String())
	}
}
