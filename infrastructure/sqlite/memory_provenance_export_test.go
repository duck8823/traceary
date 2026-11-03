package sqlite_test

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	apptypes "github.com/duck8823/traceary/application/types"
	"github.com/duck8823/traceary/application/usecase"
	"github.com/duck8823/traceary/domain/types"
)

// Exercise the real Export -> query -> SQLite boundary, not a filter fake.
func TestMemoryProvenanceExport_ExcludesForeignStaleAndUnreviewedRows(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	datasource, store := newMemoryDatasource(t, filepath.Join(t.TempDir(), "synthetic.db"), memoryDatasourceTestMigrations())
	if err := store.Initialize(ctx); err != nil {
		t.Fatal(err)
	}
	scope := mustWorkspaceScope(t, "github.com/example/synthetic-a")
	for _, tc := range []struct {
		id     string
		scope  types.MemoryScope
		status types.MemoryStatus
		stale  bool
	}{
		{"current", scope, types.MemoryStatusAccepted, false},
		{"foreign", mustWorkspaceScope(t, "github.com/example/synthetic-b"), types.MemoryStatusAccepted, false},
		{"candidate", scope, types.MemoryStatusCandidate, false},
		{"superseded", scope, types.MemoryStatusSuperseded, false},
		{"rejected", scope, types.MemoryStatusRejected, false},
		{"stale", scope, types.MemoryStatusAccepted, true},
	} {
		created := time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)
		memory := memoryOf(t, tc.id, types.MemoryTypePreference, tc.scope, "synthetic-"+tc.id+"-marker", tc.status,
			types.ConfidenceHigh, types.MemorySourceImported, nil, nil, types.None[types.MemoryID](), types.None[time.Time](), created, created)
		if tc.stale {
			memory.SetValidity(types.Some(created), types.Some(created.Add(time.Hour)))
		}
		if err := datasource.Save(ctx, memory); err != nil {
			t.Fatal(err)
		}
	}
	sut := usecase.NewMemoryUsecase(datasource, datasource, nil)
	result, err := sut.Export(ctx, apptypes.MemoryExportCriteria{Target: apptypes.MemoryBridgeTargetClaude, Scopes: []types.MemoryScope{scope}})
	if err != nil {
		t.Fatal(err)
	}
	if result.ExportedCount != 1 || !strings.Contains(result.Markdown, "synthetic-current-marker") {
		t.Fatalf("unexpected export: %+v", result)
	}
	for _, excluded := range []string{"foreign", "candidate", "superseded", "rejected", "stale"} {
		if strings.Contains(result.Markdown, "synthetic-"+excluded+"-marker") {
			t.Fatalf("export leaked %s", excluded)
		}
	}
}
