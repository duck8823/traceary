package sqlite_test

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	apptypes "github.com/duck8823/traceary/application/types"
	"github.com/duck8823/traceary/domain/model"
	"github.com/duck8823/traceary/domain/types"
)

func TestLiteralSearchPageReturnsPartialCoverageAndAdvancingZeroMatchCursor(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	sut, store := newEventDatasource(t, filepath.Join(t.TempDir(), "traceary.db"), onDiskSQLiteMigrations(t))
	if err := store.Initialize(ctx); err != nil {
		t.Fatal(err)
	}
	for i, body := range []string{"alpha", "beta", "Unicode 障害/経路 ERR/Path-01"} {
		event := model.EventOf(mustEventIDForSQLite(t, "literal-event-"+string(rune('a'+i))), types.EventKindNote, types.Client("hook"), mustAgentForSQLite(t, "codex"), mustSessionIDForSQLite(t, "literal-session"), types.Workspace("literal-workspace"), body, time.Date(2026, 8, 1, 0, 0, i, 0, time.UTC))
		if err := sut.Save(ctx, event); err != nil {
			t.Fatal(err)
		}
	}
	criteria := apptypes.NewEventSearchCriteriaBuilder(1).Query("not-present").Workspace(types.Workspace("literal-workspace")).Build()
	page, err := sut.SearchLiteralPage(ctx, apptypes.LiteralSearchRequest{Criteria: criteria, Budget: apptypes.LiteralSearchBudget{SourceRows: 1, StoredBytes: 1024, DecodedBytes: 1024}})
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Events) != 0 || page.Coverage.Complete || page.Continuation == "" || page.PartialReason != "source_rows" {
		t.Fatalf("page = %+v", page)
	}
	cursor, err := apptypes.DecodeLiteralSearchCursor(page.Continuation)
	if err != nil {
		t.Fatal(err)
	}
	if cursor.LastSequence == 0 {
		t.Fatal("zero-match continuation did not advance")
	}
	page2, err := sut.SearchLiteralPage(ctx, apptypes.LiteralSearchRequest{Criteria: criteria, Budget: apptypes.LiteralSearchBudget{SourceRows: 10, StoredBytes: 4096, DecodedBytes: 4096}, Continuation: page.Continuation})
	if err != nil {
		t.Fatal(err)
	}
	if !page2.Coverage.Complete || page2.Continuation != "" {
		t.Fatalf("resumed page = %+v", page2)
	}
}

func TestLiteralSearchPageVerifiesUnicodeCaseAgainstCanonicalVisibleText(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	sut, store := newEventDatasource(t, filepath.Join(t.TempDir(), "traceary.db"), onDiskSQLiteMigrations(t))
	if err := store.Initialize(ctx); err != nil {
		t.Fatal(err)
	}
	event := model.EventOf(mustEventIDForSQLite(t, "literal-unicode"), types.EventKindTranscript, types.Client("hook"), mustAgentForSQLite(t, "codex"), mustSessionIDForSQLite(t, "literal-session"), types.Workspace("literal-workspace"), `{"blocks":[{"type":"thinking","text":"ERR/PATH-01 hidden"},{"type":"text","text":"障害/経路 err/path-01 visible"}]}`, time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC))
	if err := sut.Save(ctx, event); err != nil {
		t.Fatal(err)
	}
	criteria := apptypes.NewEventSearchCriteriaBuilder(2).Query("ERR/PATH-01").Workspace(types.Workspace("literal-workspace")).Build()
	page, err := sut.SearchLiteralPage(ctx, apptypes.LiteralSearchRequest{Criteria: criteria, Budget: apptypes.LiteralSearchBudget{SourceRows: 10, StoredBytes: 4096, DecodedBytes: 4096}})
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Events) != 1 || page.Events[0].Body() != "障害/経路 err/path-01 visible" {
		t.Fatalf("events = %+v", page.Events)
	}
	if page.Tier != apptypes.LiteralSearchTierBoundedVerification || !page.Coverage.Complete {
		t.Fatalf("page = %+v", page)
	}
}
