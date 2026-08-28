package sqlite_test

import (
	"context"
	"database/sql"
	"path/filepath"
	"regexp"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"golang.org/x/xerrors"
	_ "modernc.org/sqlite"

	"github.com/duck8823/traceary/domain/model"
	"github.com/duck8823/traceary/domain/types"
	"github.com/duck8823/traceary/infrastructure/sqlite"
)

type consolidationLedgerFixture struct {
	dbPath string
	ledger *sqlite.ConsolidationRequestDatasource
	events *sqlite.EventDatasource
}

func newConsolidationLedgerFixture(t *testing.T) consolidationLedgerFixture {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "traceary.db")
	database := sqlite.NewDatabase(dbPath, onDiskSQLiteMigrations(t))
	store := sqlite.NewStoreManagementDatasource(database)
	if err := store.Initialize(context.Background()); err != nil {
		t.Fatalf("Initialize() error = %v", err)
	}
	return consolidationLedgerFixture{
		dbPath: dbPath,
		ledger: sqlite.NewConsolidationRequestDatasource(database),
		events: sqlite.NewEventDatasource(database),
	}
}

func mustConsolidationRequest(
	t *testing.T,
	sessionID, client string,
	at time.Time,
	eventID string,
	pressure, threshold int64,
	reRequest bool,
) *model.ConsolidationRequest {
	t.Helper()
	request, err := model.NewConsolidationRequest(
		types.SessionID(sessionID),
		client,
		at,
		types.EventID(eventID),
		"body_bytes",
		pressure,
		threshold,
		reRequest,
		types.ConsolidationDeliveryStopExit2,
	)
	if err != nil {
		t.Fatalf("NewConsolidationRequest() error = %v", err)
	}
	return request
}

func TestConsolidationRequestDatasource_SaveIdempotentOnSessionAndEvent(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	fx := newConsolidationLedgerFixture(t)
	at := time.Date(2026, 8, 29, 12, 0, 0, 0, time.UTC)

	first, err := fx.ledger.Save(ctx, mustConsolidationRequest(t, "sess-1", "claude", at, "evt-1", 10, 100, false))
	if err != nil || !first {
		t.Fatalf("first Save() recorded=%v err=%v", first, err)
	}
	dup, err := fx.ledger.Save(ctx, mustConsolidationRequest(t, "sess-1", "claude", at.Add(time.Second), "evt-1", 20, 100, true))
	if err != nil {
		t.Fatalf("duplicate Save() error = %v", err)
	}
	if dup {
		t.Fatal("duplicate Save() recorded=true, want false")
	}
	second, err := fx.ledger.Save(ctx, mustConsolidationRequest(t, "sess-1", "claude", at.Add(2*time.Second), "evt-2", 30, 100, true))
	if err != nil || !second {
		t.Fatalf("different at_event_id Save() recorded=%v err=%v", second, err)
	}
	if got := countConsolidationRequestsAt(t, fx.dbPath); got != 2 {
		t.Fatalf("rows = %d, want 2", got)
	}
}

func TestConsolidationRequestDatasource_FindLatestOpenSkipsStampedRows(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	fx := newConsolidationLedgerFixture(t)
	at := time.Date(2026, 8, 29, 12, 0, 0, 0, time.UTC)
	if _, err := fx.ledger.Save(ctx, mustConsolidationRequest(t, "sess-1", "claude", at, "evt-1", 10, 100, false)); err != nil {
		t.Fatal(err)
	}
	if _, err := fx.ledger.Save(ctx, mustConsolidationRequest(t, "sess-1", "claude", at.Add(time.Minute), "evt-2", 20, 100, true)); err != nil {
		t.Fatal(err)
	}

	open, err := fx.ledger.FindLatestOpen(ctx, "sess-1")
	if err != nil {
		t.Fatal(err)
	}
	request, ok := open.Value()
	if !ok {
		t.Fatal("FindLatestOpen() = None, want newest open row")
	}
	if request.AtEventID().String() != "evt-2" {
		t.Fatalf("AtEventID() = %s, want evt-2", request.AtEventID())
	}

	stamped, err := fx.ledger.MarkRefineOutcome(ctx, model.ConsolidationRefineStamp{
		SessionID:  "sess-1",
		Outcome:    types.ConsolidationRefineAccepted,
		Reason:     "created",
		ProducedBy: "agent",
		Generation: types.Some(1),
		At:         at.Add(2 * time.Minute),
	})
	if err != nil || !stamped {
		t.Fatalf("MarkRefineOutcome() stamped=%v err=%v", stamped, err)
	}

	stillOpen, err := fx.ledger.FindLatestOpen(ctx, "sess-1")
	if err != nil {
		t.Fatal(err)
	}
	older, ok := stillOpen.Value()
	if !ok || older.AtEventID().String() != "evt-1" {
		t.Fatalf("remaining open = %v ok=%v, want evt-1", older, ok)
	}

	if _, err := fx.ledger.MarkRefineOutcome(ctx, model.ConsolidationRefineStamp{
		SessionID: "sess-1", Outcome: types.ConsolidationRefineRejected, Reason: "unchanged", At: at.Add(3 * time.Minute),
	}); err != nil {
		t.Fatal(err)
	}
	none, err := fx.ledger.FindLatestOpen(ctx, "sess-1")
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := none.Value(); ok {
		t.Fatal("FindLatestOpen() after all stamped = Some, want None")
	}
	noop, err := fx.ledger.MarkRefineOutcome(ctx, model.ConsolidationRefineStamp{
		SessionID: "sess-1", Outcome: types.ConsolidationRefineAccepted, Reason: "created", At: at,
	})
	if err != nil || noop {
		t.Fatalf("MarkRefineOutcome() with no open row = %v err=%v, want false", noop, err)
	}
}

func TestConsolidationRequestDatasource_RequestedAtUsesFixedWidthCanonicalForm(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	fx := newConsolidationLedgerFixture(t)
	whole := time.Date(2026, 8, 29, 12, 0, 0, 0, time.UTC)
	frac := time.Date(2026, 8, 29, 12, 0, 0, 500000000, time.UTC)
	if _, err := fx.ledger.Save(ctx, mustConsolidationRequest(t, "sess-whole", "claude", whole, "evt-whole", 1, 100, false)); err != nil {
		t.Fatal(err)
	}
	if _, err := fx.ledger.Save(ctx, mustConsolidationRequest(t, "sess-frac", "claude", frac, "evt-frac", 1, 100, false)); err != nil {
		t.Fatal(err)
	}

	stored := map[string]string{
		"sess-whole": storedRequestedAt(t, fx.dbPath, "sess-whole"),
		"sess-frac":  storedRequestedAt(t, fx.dbPath, "sess-frac"),
	}
	canonical := regexp.MustCompile(`^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}\.\d{9}Z$`)
	for session, value := range stored {
		if !canonical.MatchString(value) {
			t.Fatalf("%s requested_at = %q, want fixed-width canonical", session, value)
		}
	}

	between := time.Date(2026, 8, 29, 12, 0, 0, 250000000, time.UTC)
	rows, err := fx.ledger.ConversionSince(ctx, between)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || rows[0].Requests != 1 || rows[0].SessionsRequested != 1 {
		t.Fatalf("range between whole and frac = %+v, want only the later row", rows)
	}

	windowStart := time.Date(2026, 8, 29, 11, 59, 59, 0, time.UTC)
	all, err := fx.ledger.ConversionSince(ctx, windowStart)
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 1 || all[0].Requests != 2 {
		t.Fatalf("window covering both = %+v, want 2 requests", all)
	}
}

func TestConsolidationRequestDatasource_ConversionSinceGroupsAndWindows(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	fx := newConsolidationLedgerFixture(t)
	now := time.Date(2026, 8, 29, 12, 0, 0, 0, time.UTC)
	old := now.Add(-8 * 24 * time.Hour)
	if _, err := fx.ledger.Save(ctx, mustConsolidationRequest(t, "sess-old", "claude", old, "evt-old", 1, 100, false)); err != nil {
		t.Fatal(err)
	}
	if _, err := fx.ledger.Save(ctx, mustConsolidationRequest(t, "sess-a", "claude", now, "evt-a", 1, 100, false)); err != nil {
		t.Fatal(err)
	}
	if _, err := fx.ledger.Save(ctx, mustConsolidationRequest(t, "sess-a", "claude", now.Add(time.Minute), "evt-a2", 1, 100, true)); err != nil {
		t.Fatal(err)
	}
	if _, err := fx.ledger.Save(ctx, mustConsolidationRequest(t, "sess-b", "codex", now, "evt-b", 1, 100, false)); err != nil {
		t.Fatal(err)
	}
	if _, err := fx.ledger.MarkRefineOutcome(ctx, model.ConsolidationRefineStamp{
		SessionID: "sess-b", Outcome: types.ConsolidationRefineAccepted, Reason: "created", ProducedBy: "agent", Generation: types.Some(1), At: now,
	}); err != nil {
		t.Fatal(err)
	}

	rows, err := fx.ledger.ConversionSince(ctx, now.Add(-time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	want := []struct {
		client            string
		requests          int
		sessionsRequested int
		sessionsRefined   int
	}{
		{client: "claude", requests: 2, sessionsRequested: 1},
		{client: "codex", requests: 1, sessionsRequested: 1, sessionsRefined: 1},
	}
	if len(rows) != len(want) {
		t.Fatalf("rows = %+v, want %d", rows, len(want))
	}
	for i, row := range rows {
		if row.Client != want[i].client || row.Requests != want[i].requests ||
			row.SessionsRequested != want[i].sessionsRequested || row.SessionsRefined != want[i].sessionsRefined {
			t.Fatalf("row[%d] = %+v, want %+v", i, row, want[i])
		}
	}
}

func TestConsolidationRequestDatasource_RefinementAuthorshipSince(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	fx := newConsolidationLedgerFixture(t)
	sessions := sqlite.NewSessionDatasource(sqlite.NewDatabase(fx.dbPath, onDiskSQLiteMigrations(t)))
	refinementDS := sqlite.NewSessionRefinementDatasource(sqlite.NewDatabase(fx.dbPath, onDiskSQLiteMigrations(t)))
	askAt := time.Date(2026, 8, 29, 12, 0, 0, 0, time.UTC)

	seedRefinementSession(ctx, t, sessions, fx.events, "sess-agent", []eventSeed{
		{id: "evt-agent", at: askAt.Add(-time.Minute)},
	})
	seedRefinementSession(ctx, t, sessions, fx.events, "sess-gc", []eventSeed{
		{id: "evt-gc", at: askAt.Add(-time.Minute)},
	})
	seedRefinementSession(ctx, t, sessions, fx.events, "sess-none", []eventSeed{
		{id: "evt-none", at: askAt.Add(-time.Minute)},
	})
	seedRefinementSession(ctx, t, sessions, fx.events, "sess-early", []eventSeed{
		{id: "evt-early", at: askAt.Add(-2 * time.Hour)},
	})
	seedRefinementSession(ctx, t, sessions, fx.events, "sess-badts", []eventSeed{
		{id: "evt-badts", at: askAt.Add(-time.Minute)},
	})

	mustSaveRefinement(ctx, t, refinementDS, "sess-agent", "evt-agent", "agent", askAt.Add(time.Minute))
	mustSaveRefinement(ctx, t, refinementDS, "sess-gc", "evt-gc", "gc:orphan-consolidation", askAt.Add(time.Minute))
	mustSaveRefinement(ctx, t, refinementDS, "sess-early", "evt-early", "agent", askAt.Add(-time.Hour))
	mustSaveRefinement(ctx, t, refinementDS, "sess-badts", "evt-badts", "agent", askAt.Add(time.Minute))
	if err := rewriteProducedAt(fx.dbPath, "sess-badts", "not-a-timestamp"); err != nil {
		t.Fatalf("rewrite produced_at: %v", err)
	}

	for _, sessionID := range []string{"sess-agent", "sess-gc", "sess-none", "sess-early", "sess-badts"} {
		if _, err := fx.ledger.Save(ctx, mustConsolidationRequest(t, sessionID, "claude", askAt, "evt-"+sessionID, 1, 100, false)); err != nil {
			t.Fatal(err)
		}
	}

	rows, err := fx.ledger.RefinementAuthorshipSince(ctx, askAt.Add(-time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]int{}
	for _, row := range rows {
		if row.Client != "claude" {
			t.Fatalf("unexpected client %q", row.Client)
		}
		got[row.ProducedBy] += row.Sessions
	}
	want := map[string]int{
		"agent":                   1,
		"gc:orphan-consolidation": 1,
		"":                        3, // none + early + unparseable
	}
	if diff := cmp.Diff(want, got); diff != "" {
		t.Fatalf("authorship mismatch (-want +got):\n%s", diff)
	}
}

func TestEventDatasource_LatestEventIDUsesCanonicalOrder(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	fx := newSessionRefinementFixture(t)
	sessionID := seedRefinementSession(ctx, t, fx.sessions, fx.events, "sess-latest", []eventSeed{
		{id: "evt-early", at: time.Date(2026, 8, 1, 10, 0, 0, 0, time.UTC)},
		{id: "evt-late", at: time.Date(2026, 8, 1, 11, 0, 0, 0, time.UTC)},
	})
	got, err := fx.events.LatestEventID(ctx, sessionID)
	if err != nil {
		t.Fatal(err)
	}
	id, ok := got.Value()
	if !ok || id.String() != "evt-late" {
		t.Fatalf("LatestEventID() = %v ok=%v, want evt-late", id, ok)
	}
	missing, err := fx.events.LatestEventID(ctx, "sess-missing")
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := missing.Value(); ok {
		t.Fatal("LatestEventID(missing) = Some, want None")
	}
}

func mustSaveRefinement(
	ctx context.Context,
	t *testing.T,
	repo *sqlite.SessionRefinementDatasource,
	sessionID, eventID, producedBy string,
	at time.Time,
) {
	t.Helper()
	row, err := model.NewSessionRefinement(
		types.SessionID(sessionID), 1, types.EventID(sessionID+"-start"), types.EventID(eventID),
		"summary", "", producedBy, at, false,
	)
	if err != nil {
		t.Fatal(err)
	}
	ok, err := repo.SaveIfAdvances(ctx, row, 0)
	if err != nil || !ok {
		t.Fatalf("SaveIfAdvances(%s) ok=%v err=%v", sessionID, ok, err)
	}
}

func countConsolidationRequestsAt(t *testing.T, dbPath string) int {
	t.Helper()
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("sql.Open() error = %v", err)
	}
	defer func() { _ = db.Close() }()
	var count int
	if err := db.QueryRow(`SELECT COUNT(*) FROM consolidation_requests`).Scan(&count); err != nil {
		t.Fatalf("COUNT consolidation_requests error = %v", err)
	}
	return count
}

func storedRequestedAt(t *testing.T, dbPath, sessionID string) string {
	t.Helper()
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("sql.Open() error = %v", err)
	}
	defer func() { _ = db.Close() }()
	var value string
	if err := db.QueryRow(`SELECT requested_at FROM consolidation_requests WHERE session_id = ?`, sessionID).Scan(&value); err != nil {
		t.Fatalf("requested_at lookup: %v", err)
	}
	return value
}

func rewriteProducedAt(dbPath, sessionID, producedAt string) error {
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		return xerrors.Errorf("open store: %w", err)
	}
	defer func() { _ = db.Close() }()
	if _, err = db.Exec(`UPDATE session_refinements SET produced_at = ? WHERE session_id = ?`, producedAt, sessionID); err != nil {
		return xerrors.Errorf("rewrite produced_at: %w", err)
	}
	return nil
}
