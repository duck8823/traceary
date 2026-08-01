package sqlite

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/duck8823/traceary/domain/model"
	"github.com/duck8823/traceary/domain/types"
)

func TestCompactSummaryPreviewSkipsNewerPreCompactSnapshot(t *testing.T) {
	ctx := context.Background()
	database := NewDatabase(filepath.Join(t.TempDir(), "traceary.db"), os.DirFS(filepath.Join("..", "..", "schema", "sqlite", "migrations")))
	if err := NewStoreManagementDatasource(database).Initialize(ctx); err != nil {
		t.Fatal(err)
	}
	events := NewEventDatasource(database)
	base := time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC)
	for _, event := range []*model.Event{
		model.EventOfWithSourceHook("post", types.EventKindCompactSummary, "cli", "codex", "session", "workspace", "usable summary", base, "post_compact"),
		model.EventOfWithSourceHook("pre", types.EventKindCompactSummary, "cli", "codex", "session", "workspace", "newer snapshot", base.Add(time.Second), "pre_compact"),
	} {
		if err := events.Save(ctx, event); err != nil {
			t.Fatal(err)
		}
	}
	result, err := events.FindLatestPostCompactSummary(ctx, "session", "workspace")
	if err != nil {
		t.Fatal(err)
	}
	event, ok := result.Value()
	if !ok || event.EventID() != "post" || event.Body() != "usable summary" {
		t.Fatalf("preview did not return the latest usable post-compact summary")
	}
}

func TestCompactSummaryPreviewSelectsMetadataBeforeHydration(t *testing.T) {
	normalized := strings.Join(strings.Fields(strings.ToLower(selectLatestPostCompactSummaryQuery)), " ")
	selectedEnd := strings.Index(normalized, ") select e.id")
	if selectedEnd < 0 {
		t.Fatalf("query has no bounded candidate phase: %s", normalized)
	}
	candidate := normalized[:selectedEnd]
	if !strings.Contains(candidate, "from event_metadata_projection m") || strings.Contains(candidate, " from events ") || strings.Contains(candidate, ".body") {
		t.Fatalf("candidate phase is not body-free: %s", candidate)
	}
	if !strings.Contains(normalized, "limit 1") || !strings.Contains(normalized, "join events e on e.id = selected.id") {
		t.Fatalf("query does not hydrate exactly one selected identity: %s", normalized)
	}
}
