package sqlite

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/duck8823/traceary/domain/model"
	"github.com/duck8823/traceary/domain/types"
)

func TestCompactSummaryPreviewSkipsLeadingWhitespaceLegacyMarkersAcrossPages(t *testing.T) {
	ctx := context.Background()
	database := NewDatabase(filepath.Join(t.TempDir(), "traceary.db"), os.DirFS(filepath.Join("..", "..", "schema", "sqlite", "migrations")))
	if err := NewStoreManagementDatasource(database).Initialize(ctx); err != nil {
		t.Fatal(err)
	}
	events := NewEventDatasource(database)
	base := time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC)
	post := model.EventOfWithSourceHook("post", types.EventKindCompactSummary, "cli", "codex", "session", "workspace", "usable summary", base, "post_compact")
	if err := events.Save(ctx, post); err != nil {
		t.Fatal(err)
	}
	// More than one candidate page proves continuation cannot false-select a
	// whitespace-prefixed legacy pre-compact snapshot.
	for index := 0; index < 33; index++ {
		body := " \n\t" + types.EventBodyMarkerCompactPreSnapshot + " private snapshot"
		event := model.EventOf(types.EventID(fmt.Sprintf("legacy-pre-%02d", index)), types.EventKindCompactSummary, "cli", "codex", "session", "workspace", body, base.Add(time.Duration(index+1)*time.Second))
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
		t.Fatalf("preview did not continue to the latest usable post-compact summary")
	}
}

func TestCompactSummaryPreviewSelectsMetadataBeforeHydration(t *testing.T) {
	candidate := strings.Join(strings.Fields(strings.ToLower(selectLatestPostCompactSummaryQuery)), " ")
	if !strings.Contains(candidate, "from event_metadata_projection m") || strings.Contains(candidate, " from events ") || strings.Contains(candidate, ".body") {
		t.Fatalf("candidate query is not body-free: %s", candidate)
	}
	if !strings.Contains(candidate, "m.created_at_norm < ?") || !strings.Contains(candidate, "m.id < ?") || !strings.Contains(candidate, "limit ?") {
		t.Fatalf("candidate query lacks bounded keyset continuation: %s", candidate)
	}
	hydration := strings.Join(strings.Fields(strings.ToLower(selectEventByIDQuery)), " ")
	if !strings.Contains(hydration, "from events e where e.id = ?") {
		t.Fatalf("candidate hydration is not an identity lookup: %s", hydration)
	}
}
