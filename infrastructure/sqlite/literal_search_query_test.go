package sqlite_test

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"testing"
	"time"

	apptypes "github.com/duck8823/traceary/application/types"
	"github.com/duck8823/traceary/domain/model"
	"github.com/duck8823/traceary/domain/types"
	infra "github.com/duck8823/traceary/infrastructure/sqlite"
)

func TestLiteralSearchPageValidatesBeforeDatabaseAndAllocation(t *testing.T) {
	t.Parallel()
	sut := infra.NewEventDatasource(nil)
	budget := apptypes.LiteralSearchBudget{SourceRows: 1, StoredBytes: 1, DecodedBytes: 1}
	now := time.Now().UTC()
	tests := []struct {
		name    string
		request apptypes.LiteralSearchRequest
	}{{"zero limit", apptypes.LiteralSearchRequest{Criteria: apptypes.NewEventSearchCriteriaBuilder(0).Query("needle").Build(), Budget: budget}}, {"negative limit", apptypes.LiteralSearchRequest{Criteria: apptypes.NewEventSearchCriteriaBuilder(-1).Query("needle").Build(), Budget: budget}}, {"negative budget", apptypes.LiteralSearchRequest{Criteria: apptypes.NewEventSearchCriteriaBuilder(1).Query("needle").Build(), Budget: apptypes.LiteralSearchBudget{SourceRows: -1, StoredBytes: 1, DecodedBytes: 1}}}, {"reversed interval", apptypes.LiteralSearchRequest{Criteria: apptypes.NewEventSearchCriteriaBuilder(1).Query("needle").From(now).To(now.Add(-time.Second)).Build(), Budget: budget}}, {"offset", apptypes.LiteralSearchRequest{Criteria: apptypes.NewEventSearchCriteriaBuilder(1).Query("needle").Offset(1).Build(), Budget: budget}}}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := sut.SearchLiteralPage(context.Background(), tc.request); !errors.Is(err, apptypes.ErrLiteralSearchInvalidRequest) {
				t.Fatalf("error=%v", err)
			}
		})
	}
}

func TestLiteralSearchPageFingerprintCandidatesRemainInclusiveForMissingRows(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "traceary.db")
	sut, store := newEventDatasource(t, dbPath, onDiskSQLiteMigrations(t))
	if err := store.Initialize(ctx); err != nil {
		t.Fatal(err)
	}
	for _, fixture := range []struct{ id, body string }{{"literal-fp-match", "path ERR/Path-01"}, {"literal-fp-missing", "also err/path-01"}, {"literal-fp-other", "unrelated body"}} {
		event := model.EventOf(mustEventIDForSQLite(t, fixture.id), types.EventKindNote, types.Client("hook"), mustAgentForSQLite(t, "codex"), mustSessionIDForSQLite(t, "literal-session"), types.Workspace("literal-workspace"), fixture.body, time.Now().UTC())
		if err := sut.Save(ctx, event); err != nil {
			t.Fatal(err)
		}
	}
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	query := apptypes.CharacterizeLiteralQuery("ERR/Path-01")
	for _, id := range []string{"literal-fp-match", "literal-fp-other"} {
		body := query
		if id == "literal-fp-other" {
			body = apptypes.CharacterizeLiteralQuery("unrelated body")
		}
		for _, fp := range body.Fingerprints() {
			if _, err := db.Exec(`INSERT INTO literal_search_fingerprints(generation_id,source_sequence,event_id,fingerprint,fingerprint_version) SELECT 'g1',sequence,?, ?,1 FROM search_projection_source_sequence WHERE event_id=?`, id, []byte(fp), id); err != nil {
				t.Fatal(err)
			}
		}
	}
	if _, err := db.Exec(`UPDATE literal_search_projection_state SET generation_id='g1',state='complete'`); err != nil {
		t.Fatal(err)
	}
	criteria := apptypes.NewEventSearchCriteriaBuilder(10).Query("err/path-01").Workspace(types.Workspace("literal-workspace")).Build()
	page, err := sut.SearchLiteralPage(ctx, apptypes.LiteralSearchRequest{Criteria: criteria, Budget: apptypes.LiteralSearchBudget{SourceRows: 20, StoredBytes: 4096, DecodedBytes: 4096}})
	if err != nil {
		t.Fatal(err)
	}
	if page.Tier != apptypes.LiteralSearchTierFingerprint || len(page.Events) != 2 {
		t.Fatalf("page = %+v", page)
	}
	if page.Events[0].Metadata().EventID().String() != "literal-fp-match" || page.Events[1].Metadata().EventID().String() != "literal-fp-missing" || len(page.Matches) != 2 {
		t.Fatalf("source order/continuations=%v/%d", []string{page.Events[0].Metadata().EventID().String(), page.Events[1].Metadata().EventID().String()}, len(page.Matches))
	}
}

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
	if page.Coverage.ProcessedSources == 0 {
		t.Fatal("zero-match continuation did not advance")
	}
	if page.Coverage.ExaminedSources != 1 {
		t.Fatalf("source sentinel was examined: coverage=%+v", page.Coverage)
	}
	page2, err := sut.SearchLiteralPage(ctx, apptypes.LiteralSearchRequest{Criteria: criteria, Budget: apptypes.LiteralSearchBudget{SourceRows: 10, StoredBytes: 4096, DecodedBytes: 4096}, Continuation: page.Continuation})
	if err != nil {
		t.Fatal(err)
	}
	if page2.Coverage.ProcessedSources <= page.Coverage.ProcessedSources || !page2.Coverage.Complete {
		t.Fatalf("resumed page = %+v", page2)
	}
}

func TestLiteralSearchPageResultLimitAcceptsMatchWithResumeBefore(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	sut, store := newEventDatasource(t, filepath.Join(t.TempDir(), "traceary.db"), onDiskSQLiteMigrations(t))
	if err := store.Initialize(ctx); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"literal-result-a", "literal-result-b"} {
		event := model.EventOf(mustEventIDForSQLite(t, id), types.EventKindNote, types.Client("hook"), mustAgentForSQLite(t, "codex"), mustSessionIDForSQLite(t, "literal-session"), types.Workspace("literal-workspace"), "needle", time.Now().UTC())
		if err := sut.Save(ctx, event); err != nil {
			t.Fatal(err)
		}
	}
	criteria := apptypes.NewEventSearchCriteriaBuilder(1).Query("needle").Workspace(types.Workspace("literal-workspace")).Build()
	request := apptypes.LiteralSearchRequest{Criteria: criteria, Budget: apptypes.LiteralSearchBudget{SourceRows: 10, StoredBytes: 4096, DecodedBytes: 4096}}
	page, err := sut.SearchLiteralPage(ctx, request)
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Events) != 1 || len(page.Matches) != 1 || page.Matches[0].ResumeBefore == "" || page.PartialReason != "result_limit" {
		t.Fatalf("page=%+v matches=%+v", page, page.Matches)
	}
	request.Continuation = page.Matches[0].ResumeBefore
	retry, err := sut.SearchLiteralPage(ctx, request)
	if err != nil {
		t.Fatal(err)
	}
	if len(retry.Events) != 1 || retry.Events[0].Metadata().EventID() != page.Events[0].Metadata().EventID() {
		t.Fatalf("resume-before did not retry accepted match: first=%+v retry=%+v", page.Events, retry.Events)
	}
}

func TestLiteralSearchPageExcludesHydrationOversizeAfterProgress(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "traceary.db")
	sut, store := newEventDatasource(t, dbPath, onDiskSQLiteMigrations(t))
	if err := store.Initialize(ctx); err != nil {
		t.Fatal(err)
	}
	fixtures := []struct{ id, workspace, body string }{{"literal-skipped", "other", "ignored"}, {"literal-oversize", "target", "needle payload for hydration"}}
	for _, fixture := range fixtures {
		event := model.EventOf(mustEventIDForSQLite(t, fixture.id), types.EventKindNote, types.Client("hook"), mustAgentForSQLite(t, "codex"), mustSessionIDForSQLite(t, "literal-session"), types.Workspace(fixture.workspace), fixture.body, time.Now().UTC())
		if err := sut.Save(ctx, event); err != nil {
			t.Fatal(err)
		}
	}
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	var stored, decoded int64
	if err := db.QueryRow(`SELECT COALESCE(body_encoded_bytes,length(body),0),COALESCE(body_plaintext_bytes,length(body),0) FROM events WHERE id='literal-oversize'`).Scan(&stored, &decoded); err != nil {
		t.Fatal(err)
	}
	criteria := apptypes.NewEventSearchCriteriaBuilder(2).Query("needle").Workspace(types.Workspace("target")).Build()
	page, err := sut.SearchLiteralPage(ctx, apptypes.LiteralSearchRequest{Criteria: criteria, Budget: apptypes.LiteralSearchBudget{SourceRows: 10, StoredBytes: stored, DecodedBytes: decoded}})
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Events) != 0 || page.PartialReason != "verified_hydration_bytes" || page.Coverage.ProcessedSources != 1 || page.Coverage.ExaminedSources != 2 {
		t.Fatalf("page=%+v", page)
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

func TestLiteralSearchContinuationRejectsMembershipRevisionAndUnsupportedOffset(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "traceary.db")
	sut, store := newEventDatasource(t, dbPath, onDiskSQLiteMigrations(t))
	if err := store.Initialize(ctx); err != nil {
		t.Fatal(err)
	}
	event := model.EventOf(mustEventIDForSQLite(t, "literal-revision"), types.EventKindNote, types.Client("hook"), mustAgentForSQLite(t, "codex"), mustSessionIDForSQLite(t, "literal-session"), types.Workspace("workspace-a"), "needle body", time.Now().UTC())
	if err := sut.Save(ctx, event); err != nil {
		t.Fatal(err)
	}
	second := model.EventOf(mustEventIDForSQLite(t, "literal-revision-second"), types.EventKindNote, types.Client("hook"), mustAgentForSQLite(t, "codex"), mustSessionIDForSQLite(t, "literal-session"), types.Workspace("workspace-b"), "other body", time.Now().UTC())
	if err := sut.Save(ctx, second); err != nil {
		t.Fatal(err)
	}
	criteria := apptypes.NewEventSearchCriteriaBuilder(1).Query("absent").Workspace(types.Workspace("workspace-a")).Build()
	request := apptypes.LiteralSearchRequest{Criteria: criteria, Budget: apptypes.LiteralSearchBudget{SourceRows: 1, StoredBytes: 1024, DecodedBytes: 1024}, BodyRuneLimit: 20}
	page, err := sut.SearchLiteralPage(ctx, request)
	if err != nil {
		t.Fatal(err)
	}
	if page.Continuation == "" {
		t.Fatal("missing continuation")
	}
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(`UPDATE events SET workspace='workspace-b' WHERE id='literal-revision'`); err != nil {
		t.Fatal(err)
	}
	_ = db.Close()
	request.Continuation = page.Continuation
	if _, err = sut.SearchLiteralPage(ctx, request); !errors.Is(err, apptypes.ErrLiteralSearchCursorMismatch) {
		t.Fatalf("revision resume error=%v", err)
	}
	offset := apptypes.NewEventSearchCriteriaBuilder(1).Query("needle").Offset(1).Build()
	if _, err = sut.SearchLiteralPage(ctx, apptypes.LiteralSearchRequest{Criteria: offset, Budget: request.Budget}); err == nil {
		t.Fatal("offset accepted")
	}
}
