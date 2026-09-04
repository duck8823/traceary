package sqlite_test

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"

	apptypes "github.com/duck8823/traceary/application/types"
	"github.com/duck8823/traceary/domain/model"
	"github.com/duck8823/traceary/domain/types"
	"github.com/duck8823/traceary/infrastructure/sqlite"
)

type twoTierFixture struct {
	dbPath      string
	events      *sqlite.EventDatasource
	sessions    *sqlite.SessionDatasource
	refinements *sqlite.SessionRefinementDatasource
}

func newTwoTierFixture(t *testing.T) twoTierFixture {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "two-tier.db")
	database := sqlite.NewDatabase(dbPath, onDiskSQLiteMigrations(t))
	store := sqlite.NewStoreManagementDatasource(database)
	if err := store.Initialize(context.Background()); err != nil {
		t.Fatalf("Initialize() error = %v", err)
	}
	return twoTierFixture{
		dbPath:      dbPath,
		events:      sqlite.NewEventDatasource(database),
		sessions:    sqlite.NewSessionDatasource(database),
		refinements: sqlite.NewSessionRefinementDatasource(database),
	}
}

func twoTierCompressible(tag string) string {
	return tag + " " + strings.Repeat("redacted synthetic payload ", 128)
}

func (fx twoTierFixture) seedSession(
	t *testing.T,
	sessionID, workspace, client, agent string,
	started time.Time,
) {
	t.Helper()
	ctx := context.Background()
	session := model.NewSession(
		types.SessionID(sessionID),
		started,
		types.Client(client),
		types.Agent(agent),
		types.Workspace(workspace),
	)
	boundary, err := model.NewEventWithClock(
		types.EventID(sessionID+"-start"),
		types.EventKindSessionStarted,
		types.Client(client),
		types.Agent(agent),
		types.SessionID(sessionID),
		types.Workspace(workspace),
		"session started",
		fixedEventClock{at: started},
	)
	if err != nil {
		t.Fatalf("NewEventWithClock(%s start): %v", sessionID, err)
	}
	if err := fx.sessions.SaveBoundary(ctx, session, boundary); err != nil {
		t.Fatalf("SaveBoundary(%s): %v", sessionID, err)
	}
}

func (fx twoTierFixture) seedEvent(
	t *testing.T,
	eventID, sessionID, workspace, client, agent, body string,
	kind types.EventKind,
	at time.Time,
) {
	t.Helper()
	event, err := model.NewEventWithClock(
		types.EventID(eventID),
		kind,
		types.Client(client),
		types.Agent(agent),
		types.SessionID(sessionID),
		types.Workspace(workspace),
		body,
		fixedEventClock{at: at},
	)
	if err != nil {
		t.Fatalf("NewEventWithClock(%s): %v", eventID, err)
	}
	if err := fx.events.Save(context.Background(), event); err != nil {
		t.Fatalf("Save(%s): %v", eventID, err)
	}
}

func (fx twoTierFixture) seedAuditEvent(
	t *testing.T,
	eventID, sessionID, workspace, client, agent, command, output string,
	failed bool,
	at time.Time,
) {
	t.Helper()
	event, err := model.NewEventWithClock(
		types.EventID(eventID),
		types.EventKindCommandExecuted,
		types.Client(client),
		types.Agent(agent),
		types.SessionID(sessionID),
		types.Workspace(workspace),
		"",
		fixedEventClock{at: at},
	)
	if err != nil {
		t.Fatalf("NewEventWithClock(%s): %v", eventID, err)
	}
	audit := model.CommandAuditOf(
		types.EventID(eventID),
		command,
		"",
		output,
		false,
		false,
		types.None[int](),
		failed,
	)
	if err := fx.events.SaveWithAudit(context.Background(), event, audit); err != nil {
		t.Fatalf("SaveWithAudit(%s): %v", eventID, err)
	}
}

func (fx twoTierFixture) seedRefinement(t *testing.T, sessionID, summary string, producedAt time.Time, coversTo string) {
	t.Helper()
	row, err := model.NewSessionRefinement(
		types.SessionID(sessionID),
		1,
		types.EventID(sessionID+"-start"),
		types.EventID(coversTo),
		summary,
		"",
		"agent",
		producedAt,
		false,
	)
	if err != nil {
		t.Fatalf("NewSessionRefinement(%s): %v", sessionID, err)
	}
	written, err := fx.refinements.SaveIfAdvances(context.Background(), row, 0)
	if err != nil {
		t.Fatalf("SaveIfAdvances(%s): %v", sessionID, err)
	}
	if !written {
		t.Fatalf("SaveIfAdvances(%s) wrote nothing", sessionID)
	}
}

func (fx twoTierFixture) search(t *testing.T, criteria apptypes.EventSearchCriteria) apptypes.TwoTierSearchPage {
	t.Helper()
	page, err := fx.events.SearchTwoTier(context.Background(), criteria)
	if err != nil {
		t.Fatalf("SearchTwoTier() error = %v", err)
	}
	return page
}

func twoTierEventIDs(page apptypes.TwoTierSearchPage) []string {
	ids := make([]string, 0, len(page.Events()))
	for _, hit := range page.Events() {
		ids = append(ids, hit.Event().EventID().String())
	}
	return ids
}

func twoTierSessionIDs(page apptypes.TwoTierSearchPage) []string {
	ids := make([]string, 0, len(page.Sessions()))
	for _, hit := range page.Sessions() {
		ids = append(ids, hit.SessionID().String())
	}
	return ids
}

func TestTwoTierSearch_PhraseInRefinementIsFound(t *testing.T) {
	t.Parallel()
	fx := newTwoTierFixture(t)
	started := time.Date(2026, 8, 1, 10, 0, 0, 0, time.UTC)
	fx.seedSession(t, "sess-phrase", "ws", "cli", "codex", started)
	fx.seedEvent(t, "evt-phrase", "sess-phrase", "ws", "cli", "codex", "unrelated body", types.EventKindNote, started.Add(time.Minute))
	fx.seedRefinement(t, "sess-phrase", "rotate the keep-alive window weekly", started.Add(2*time.Minute), "evt-phrase")

	page := fx.search(t, apptypes.NewEventSearchCriteriaBuilder(20).Query("keep-alive window").Build())
	if diff := cmp.Diff([]string{"sess-phrase"}, twoTierSessionIDs(page)); diff != "" {
		t.Fatalf("session IDs mismatch (-want +got):\n%s", diff)
	}
	if len(page.Sessions()) != 1 || page.Sessions()[0].Tier() != apptypes.SearchHitTierRefinement {
		t.Fatalf("want 1 refinement session hit, got %#v", page.Sessions())
	}
}

func TestTwoTierSearch_HyphenatedTermMatchesRefinementAndBody(t *testing.T) {
	t.Parallel()
	fx := newTwoTierFixture(t)
	base := time.Date(2026, 8, 1, 10, 0, 0, 0, time.UTC)
	for i, id := range []string{"sess-h1", "sess-h2", "sess-h3"} {
		fx.seedSession(t, id, "ws", "cli", "codex", base.Add(time.Duration(i)*time.Minute))
		eventID := "evt-" + id
		fx.seedEvent(t, eventID, id, "ws", "cli", "codex", "no hyphen here", types.EventKindNote, base.Add(time.Duration(i)*time.Minute+time.Second))
		fx.seedRefinement(t, id, "raise keep-days for "+id, base.Add(time.Duration(i)*time.Minute+2*time.Second), eventID)
	}
	for i, id := range []string{"sess-b1", "sess-b2", "sess-b3"} {
		fx.seedSession(t, id, "ws", "cli", "codex", base.Add(time.Duration(10+i)*time.Minute))
		fx.seedEvent(t, "evt-"+id, id, "ws", "cli", "codex", "set keep-days later in "+id, types.EventKindNote, base.Add(time.Duration(10+i)*time.Minute+time.Second))
	}

	page := fx.search(t, apptypes.NewEventSearchCriteriaBuilder(50).Query("keep-days").Build())
	wantSessions := []string{"sess-h1", "sess-h2", "sess-h3", "sess-b1", "sess-b2", "sess-b3"}
	gotSessions := twoTierSessionIDs(page)
	for _, want := range wantSessions[:3] {
		if !containsString(gotSessions, want) {
			t.Fatalf("missing refinement session %s in %v", want, gotSessions)
		}
	}
	gotEvents := twoTierEventIDs(page)
	for _, id := range []string{"evt-sess-b1", "evt-sess-b2", "evt-sess-b3"} {
		if !containsString(gotEvents, id) {
			t.Fatalf("missing body event %s in %v", id, gotEvents)
		}
	}
	if len(gotEvents)+len(gotSessions) < 6 {
		t.Fatalf("reachable hits = events %v sessions %v, want all 6 source rows", gotEvents, gotSessions)
	}
}

func TestTwoTierSearch_SessionWithoutRefinementReachableViaFallback(t *testing.T) {
	t.Parallel()
	fx := newTwoTierFixture(t)
	started := time.Date(2026, 8, 1, 10, 0, 0, 0, time.UTC)
	fx.seedSession(t, "sess-raw", "ws", "cli", "codex", started)
	fx.seedEvent(t, "evt-raw", "sess-raw", "ws", "cli", "codex", "raw body contains fallback-only-term", types.EventKindNote, started.Add(time.Minute))

	page := fx.search(t, apptypes.NewEventSearchCriteriaBuilder(20).Query("fallback-only-term").Build())
	if diff := cmp.Diff([]string{"evt-raw"}, twoTierEventIDs(page)); diff != "" {
		t.Fatalf("events mismatch (-want +got):\n%s", diff)
	}
	if diff := cmp.Diff([]string{"sess-raw"}, twoTierSessionIDs(page)); diff != "" {
		t.Fatalf("sessions mismatch (-want +got):\n%s", diff)
	}
	if page.Sessions()[0].Tier() != apptypes.SearchHitTierFallback {
		t.Fatalf("session tier = %q, want fallback", page.Sessions()[0].Tier())
	}
	if page.Events()[0].Tier() != apptypes.SearchHitTierFallback {
		t.Fatalf("event tier = %q, want fallback", page.Events()[0].Tier())
	}
}

func TestTwoTierSearch_FallbackFindsPlaintextBody(t *testing.T) {
	t.Parallel()
	fx := newTwoTierFixture(t)
	started := time.Date(2026, 8, 1, 10, 0, 0, 0, time.UTC)
	fx.seedSession(t, "sess-plain-body", "ws", "cli", "codex", started)
	fx.seedSession(t, "sess-plain-audit", "ws", "cli", "codex", started.Add(time.Minute))
	fx.seedEvent(
		t, "evt-plain-body", "sess-plain-body", "ws", "cli", "codex",
		twoTierCompressible("plain-two-tier-needle in the event body"),
		types.EventKindNote, started.Add(2*time.Second),
	)
	fx.seedAuditEvent(
		t, "evt-plain-audit", "sess-plain-audit", "ws", "cli", "codex",
		"echo ok",
		twoTierCompressible("plain-two-tier-needle in the audit output"),
		false,
		started.Add(3*time.Second),
	)

	page := fx.search(t, apptypes.NewEventSearchCriteriaBuilder(20).Query("plain-two-tier-needle").Build())
	got := twoTierEventIDs(page)
	for _, id := range []string{"evt-plain-body", "evt-plain-audit"} {
		if !containsString(got, id) {
			t.Fatalf("missing plaintext hit %s in %v", id, got)
		}
	}
}

func TestTwoTierSearch_MixedEncodedPlaintextTrailingHit(t *testing.T) {
	t.Parallel()
	fx := newTwoTierFixture(t)
	base := time.Date(2026, 8, 1, 10, 0, 0, 0, time.UTC)
	fx.seedSession(t, "sess-mix", "ws", "cli", "codex", base)
	fx.seedEvent(t, "evt-plain", "sess-mix", "ws", "cli", "codex", "short mix-term", types.EventKindNote, base.Add(time.Second))
	fx.seedEvent(t, "evt-noise", "sess-mix", "ws", "cli", "codex", "no match here", types.EventKindNote, base.Add(2*time.Second))
	fx.seedEvent(
		t, "evt-trail", "sess-mix", "ws", "cli", "codex",
		twoTierCompressible("trailing mix-term"),
		types.EventKindNote, base.Add(3*time.Second),
	)
	page := fx.search(t, apptypes.NewEventSearchCriteriaBuilder(20).Query("mix-term").Build())
	got := twoTierEventIDs(page)
	for _, id := range []string{"evt-plain", "evt-trail"} {
		if !containsString(got, id) {
			t.Fatalf("missing mixed-corpus hit %s in %v", id, got)
		}
	}
	if containsString(got, "evt-noise") {
		t.Fatalf("noise row matched: %v", got)
	}
}

func TestTwoTierSearch_EachHitStatesItsTier(t *testing.T) {
	t.Parallel()
	fx := newTwoTierFixture(t)
	base := time.Date(2026, 8, 1, 10, 0, 0, 0, time.UTC)
	fx.seedSession(t, "sess-r", "ws", "cli", "codex", base)
	fx.seedEvent(t, "evt-r", "sess-r", "ws", "cli", "codex", "unrelated", types.EventKindNote, base.Add(time.Second))
	fx.seedRefinement(t, "sess-r", "tier-label-term in the summary", base.Add(2*time.Second), "evt-r")
	fx.seedSession(t, "sess-f", "ws", "cli", "codex", base.Add(time.Minute))
	fx.seedEvent(t, "evt-f", "sess-f", "ws", "cli", "codex", "tier-label-term in the body", types.EventKindNote, base.Add(time.Minute+time.Second))

	page := fx.search(t, apptypes.NewEventSearchCriteriaBuilder(20).Query("tier-label-term").Build())
	if len(page.Events()) != 1 || page.Events()[0].Tier() != apptypes.SearchHitTierFallback {
		t.Fatalf("events = %#v, want one fallback event", page.Events())
	}
	var sawRefinement, sawFallback bool
	for _, hit := range page.Sessions() {
		switch hit.Tier() {
		case apptypes.SearchHitTierRefinement:
			sawRefinement = true
		case apptypes.SearchHitTierFallback:
			sawFallback = true
		default:
			t.Fatalf("session %s missing tier: %q", hit.SessionID(), hit.Tier())
		}
	}
	if !sawRefinement || !sawFallback {
		t.Fatalf("sessions missing a tier label: refinement=%v fallback=%v ids=%v", sawRefinement, sawFallback, twoTierSessionIDs(page))
	}
}

func TestTwoTierSearch_NonMatchingRefinementDoesNotHideRawBodyMatch(t *testing.T) {
	t.Parallel()
	fx := newTwoTierFixture(t)
	started := time.Date(2026, 8, 1, 10, 0, 0, 0, time.UTC)
	fx.seedSession(t, "sess-hidden", "ws", "cli", "codex", started)
	fx.seedEvent(t, "evt-hidden", "sess-hidden", "ws", "cli", "codex", "body has recall-term", types.EventKindNote, started.Add(time.Minute))
	fx.seedRefinement(t, "sess-hidden", "unrelated summary", started.Add(2*time.Minute), "evt-hidden")

	page := fx.search(t, apptypes.NewEventSearchCriteriaBuilder(20).Query("recall-term").Build())
	if !containsString(twoTierSessionIDs(page), "sess-hidden") {
		t.Fatalf("session missing: %v", twoTierSessionIDs(page))
	}
	if page.Sessions()[0].Tier() != apptypes.SearchHitTierFallback {
		t.Fatalf("session tier = %q, want fallback (de-dupe keys on hits, not existence)", page.Sessions()[0].Tier())
	}
}

func containsString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

func TestTwoTierSearch_FallbackLatencyEvidence(t *testing.T) {
	if os.Getenv("TRACEARY_SEARCH_LATENCY") == "" {
		t.Skip("set TRACEARY_SEARCH_LATENCY=1 to record item 7 evidence")
	}
	fx := newTwoTierFixture(t)
	base := time.Date(2026, 8, 1, 10, 0, 0, 0, time.UTC)
	const n = 2000
	fx.seedSession(t, "sess-lat", "ws", "cli", "codex", base)
	for i := 0; i < n; i++ {
		body := "noise row"
		if i%7 == 0 {
			body = twoTierCompressible("latency-term packed into a compressible body")
		} else if i == n-1 {
			body = "trailing latency-term hit"
		}
		fx.seedEvent(
			t,
			fmt.Sprintf("evt-lat-%04d", i),
			"sess-lat", "ws", "cli", "codex", body, types.EventKindNote,
			base.Add(time.Duration(i)*time.Second),
		)
	}
	criteria := apptypes.NewEventSearchCriteriaBuilder(20).Query("latency-term").Build()
	start := time.Now()
	page := fx.search(t, criteria)
	elapsed := time.Since(start)
	t.Logf("fallback scan over %d events (mixed long/short plaintext, trailing hit) took %s; hits events=%d sessions=%d",
		n, elapsed, len(page.Events()), len(page.Sessions()))
}
