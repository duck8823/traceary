package sqlite_test

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	_ "modernc.org/sqlite"

	apptypes "github.com/duck8823/traceary/application/types"
	"github.com/duck8823/traceary/domain/model"
	"github.com/duck8823/traceary/domain/types"
)

func TestSearchProjectionReadPath_CompleteUsesProjectionAndMatchesLegacy(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "traceary.db")
	sut, store := newEventDatasource(t, dbPath, onDiskSQLiteMigrations(t))
	if err := store.Initialize(ctx); err != nil {
		t.Fatalf("Initialize() error = %v", err)
	}

	workspace := types.Workspace("github.com/duck8823/traceary")
	base := time.Date(2026, 8, 6, 14, 0, 0, 0, time.UTC)
	events := []*model.Event{
		newSearchEventWithSession(t, "evt-recent-1", "session-recent", workspace.String(), "visible needle alpha", base),
		newSearchEventWithSession(t, "evt-recent-2", "session-recent", workspace.String(), "visible needle beta", base.Add(time.Minute)),
	}
	for _, event := range events {
		if err := sut.Save(ctx, event); err != nil {
			t.Fatalf("Save(%s) error = %v", event.EventID(), err)
		}
	}

	// Seed an active projection generation that mirrors the recent window.
	seedCompleteProjection(t, dbPath, "gen-complete", []projectionRecentSeed{
		{eventID: "evt-recent-1", sessionID: "session-recent", body: "visible needle alpha", createdAt: base},
		{eventID: "evt-recent-2", sessionID: "session-recent", body: "visible needle beta", createdAt: base.Add(time.Minute)},
	}, nil, nil)

	criteria := apptypes.NewEventSearchCriteriaBuilder(20).
		Query("visible needle").
		Workspace(workspace).
		Build()

	got, err := sut.Search(ctx, criteria.Query(), workspace, "", "", "", "", time.Time{}, time.Time{}, 20, 0, false)
	if err != nil {
		t.Fatalf("Search() error = %v", err)
	}
	legacy, err := sut.SearchLegacyPage(ctx, criteria)
	if err != nil {
		t.Fatalf("SearchLegacyPage() error = %v", err)
	}
	if diff := cmp.Diff(eventIDs(legacy), eventIDs(got)); diff != "" {
		t.Fatalf("projection/legacy recent-window mismatch (-legacy +projection):\n%s", diff)
	}
	if diff := cmp.Diff([]string{"evt-recent-2", "evt-recent-1"}, eventIDs(got)); diff != "" {
		t.Fatalf("Search() IDs mismatch (-want +got):\n%s", diff)
	}
}

func TestSearchProjectionReadPath_IncompleteFallsBackToLegacySilently(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "traceary.db")
	sut, store := newEventDatasource(t, dbPath, onDiskSQLiteMigrations(t))
	if err := store.Initialize(ctx); err != nil {
		t.Fatalf("Initialize() error = %v", err)
	}

	workspace := types.Workspace("github.com/duck8823/traceary")
	base := time.Date(2026, 8, 6, 14, 0, 0, 0, time.UTC)
	event := newSearchEventWithSession(t, "evt-legacy-only", "session-1", workspace.String(), "legacy only needle", base)
	if err := sut.Save(ctx, event); err != nil {
		t.Fatalf("Save() error = %v", err)
	}

	// Projection state is idle / incomplete; no active generation.
	openRawDB(t, dbPath, func(db *sql.DB) {
		if _, err := db.Exec(`UPDATE search_projection_state SET state='idle', active_generation_id=NULL WHERE singleton=1`); err != nil {
			t.Fatalf("clear projection state: %v", err)
		}
	})

	criteria := apptypes.NewEventSearchCriteriaBuilder(20).
		Query("legacy only needle").
		Workspace(workspace).
		Build()
	got, err := sut.Search(ctx, criteria.Query(), workspace, "", "", "", "", time.Time{}, time.Time{}, 20, 0, false)
	if err != nil {
		t.Fatalf("Search() error = %v", err)
	}
	legacy, err := sut.SearchLegacyPage(ctx, criteria)
	if err != nil {
		t.Fatalf("SearchLegacyPage() error = %v", err)
	}
	if diff := cmp.Diff(eventIDs(legacy), eventIDs(got)); diff != "" {
		t.Fatalf("idle projection should match legacy (-legacy +got):\n%s", diff)
	}
	if diff := cmp.Diff([]string{"evt-legacy-only"}, eventIDs(got)); diff != "" {
		t.Fatalf("Search() IDs mismatch (-want +got):\n%s", diff)
	}
}

func TestSearchProjectionReadPath_SessionTierSummaryOnly(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "traceary.db")
	sut, store := newEventDatasource(t, dbPath, onDiskSQLiteMigrations(t))
	if err := store.Initialize(ctx); err != nil {
		t.Fatalf("Initialize() error = %v", err)
	}

	workspace := types.Workspace("github.com/duck8823/traceary")
	// No recent events match; only an older session summary does.
	seedCompleteProjection(t, dbPath, "gen-session", nil, []projectionSessionSeed{
		{
			sessionID:  "sess-old-summary",
			summary:    "discussed unique-session-marker during planning",
			eventCount: 12,
			startedAt:  time.Date(2026, 6, 19, 10, 0, 0, 0, time.UTC),
			workspace:  workspace.String(),
			client:     "cli",
			agent:      "codex",
		},
	}, nil)

	criteria := apptypes.NewEventSearchCriteriaBuilder(20).
		Query("unique-session-marker").
		Workspace(workspace).
		Build()
	events, err := sut.Search(ctx, criteria.Query(), workspace, "", "", "", "", time.Time{}, time.Time{}, 20, 0, false)
	if err != nil {
		t.Fatalf("Search() error = %v", err)
	}
	if len(events) != 0 {
		t.Fatalf("len(events) = %d, want 0", len(events))
	}
	sessions, err := sut.SearchSessionHits(ctx, criteria, nil)
	if err != nil {
		t.Fatalf("SearchSessionHits() error = %v", err)
	}
	if len(sessions) != 1 {
		t.Fatalf("len(sessions) = %d, want 1", len(sessions))
	}
	if diff := cmp.Diff("sess-old-summary", sessions[0].SessionID().String()); diff != "" {
		t.Fatalf("session id mismatch (-want +got):\n%s", diff)
	}
}

func TestSearchProjectionReadPath_SessionExcludedWhenEventHitExists(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "traceary.db")
	sut, store := newEventDatasource(t, dbPath, onDiskSQLiteMigrations(t))
	if err := store.Initialize(ctx); err != nil {
		t.Fatalf("Initialize() error = %v", err)
	}

	workspace := types.Workspace("github.com/duck8823/traceary")
	base := time.Date(2026, 8, 6, 14, 0, 0, 0, time.UTC)
	event := newSearchEventWithSession(t, "evt-shared", "sess-shared", workspace.String(), "shared-marker event body", base)
	if err := sut.Save(ctx, event); err != nil {
		t.Fatalf("Save() error = %v", err)
	}
	seedCompleteProjection(t, dbPath, "gen-exclude", []projectionRecentSeed{
		{eventID: "evt-shared", sessionID: "sess-shared", body: "shared-marker event body", createdAt: base},
	}, []projectionSessionSeed{
		{
			sessionID:  "sess-shared",
			summary:    "shared-marker also appears in the summary",
			eventCount: 3,
			startedAt:  base.Add(-24 * time.Hour),
			workspace:  workspace.String(),
			client:     "cli",
			agent:      "codex",
		},
		{
			sessionID:  "sess-only-summary",
			summary:    "shared-marker in another older session",
			eventCount: 2,
			startedAt:  base.Add(-48 * time.Hour),
			workspace:  workspace.String(),
			client:     "cli",
			agent:      "codex",
		},
	}, nil)

	criteria := apptypes.NewEventSearchCriteriaBuilder(20).
		Query("shared-marker").
		Workspace(workspace).
		Build()
	events, err := sut.Search(ctx, criteria.Query(), workspace, "", "", "", "", time.Time{}, time.Time{}, 20, 0, false)
	if err != nil {
		t.Fatalf("Search() error = %v", err)
	}
	if diff := cmp.Diff([]string{"evt-shared"}, eventIDs(events)); diff != "" {
		t.Fatalf("event IDs mismatch (-want +got):\n%s", diff)
	}
	exclude := []types.SessionID{types.SessionID("sess-shared")}
	sessions, err := sut.SearchSessionHits(ctx, criteria, exclude)
	if err != nil {
		t.Fatalf("SearchSessionHits() error = %v", err)
	}
	gotIDs := make([]string, 0, len(sessions))
	for _, hit := range sessions {
		gotIDs = append(gotIDs, hit.SessionID().String())
	}
	if diff := cmp.Diff([]string{"sess-only-summary"}, gotIDs); diff != "" {
		t.Fatalf("session IDs mismatch (-want +got):\n%s", diff)
	}
}

func TestSearchProjectionReadPath_FiltersApplyToBothTiers(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "traceary.db")
	sut, store := newEventDatasource(t, dbPath, onDiskSQLiteMigrations(t))
	if err := store.Initialize(ctx); err != nil {
		t.Fatalf("Initialize() error = %v", err)
	}

	wsA := types.Workspace("github.com/duck8823/traceary")
	wsB := types.Workspace("github.com/other/repo")
	base := time.Date(2026, 8, 6, 14, 0, 0, 0, time.UTC)
	for _, event := range []*model.Event{
		newSearchEventWithSession(t, "evt-a", "sess-a", wsA.String(), "filter-needle note", base),
		newSearchEventWithSession(t, "evt-b", "sess-b", wsB.String(), "filter-needle note", base.Add(time.Minute)),
	} {
		if err := sut.Save(ctx, event); err != nil {
			t.Fatalf("Save(%s) error = %v", event.EventID(), err)
		}
	}
	seedCompleteProjection(t, dbPath, "gen-filter", []projectionRecentSeed{
		{eventID: "evt-a", sessionID: "sess-a", body: "filter-needle note", createdAt: base},
		{eventID: "evt-b", sessionID: "sess-b", body: "filter-needle note", createdAt: base.Add(time.Minute)},
	}, []projectionSessionSeed{
		{
			sessionID:  "sess-old-a",
			summary:    "filter-needle older summary",
			eventCount: 4,
			startedAt:  time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC),
			workspace:  wsA.String(),
			client:     "cli",
			agent:      "codex",
		},
		{
			sessionID:  "sess-old-b",
			summary:    "filter-needle older summary",
			eventCount: 4,
			startedAt:  time.Date(2026, 6, 2, 0, 0, 0, 0, time.UTC),
			workspace:  wsB.String(),
			client:     "cli",
			agent:      "codex",
		},
	}, nil)

	t.Run("workspace filter", func(t *testing.T) {
		criteria := apptypes.NewEventSearchCriteriaBuilder(20).Query("filter-needle").Workspace(wsA).Build()
		events, err := sut.Search(ctx, criteria.Query(), wsA, "", "", "", "", time.Time{}, time.Time{}, 20, 0, false)
		if err != nil {
			t.Fatalf("Search() error = %v", err)
		}
		if diff := cmp.Diff([]string{"evt-a"}, eventIDs(events)); diff != "" {
			t.Fatalf("events (-want +got):\n%s", diff)
		}
		sessions, err := sut.SearchSessionHits(ctx, criteria, nil)
		if err != nil {
			t.Fatalf("SearchSessionHits() error = %v", err)
		}
		if diff := cmp.Diff([]string{"sess-old-a"}, sessionHitIDs(sessions)); diff != "" {
			t.Fatalf("sessions (-want +got):\n%s", diff)
		}
	})

	t.Run("session-id filter", func(t *testing.T) {
		criteria := apptypes.NewEventSearchCriteriaBuilder(20).
			Query("filter-needle").
			SessionID(types.SessionID("sess-a")).
			Build()
		events, err := sut.Search(ctx, criteria.Query(), "", types.SessionID("sess-a"), "", "", "", time.Time{}, time.Time{}, 20, 0, false)
		if err != nil {
			t.Fatalf("Search() error = %v", err)
		}
		if diff := cmp.Diff([]string{"evt-a"}, eventIDs(events)); diff != "" {
			t.Fatalf("events (-want +got):\n%s", diff)
		}
		sessions, err := sut.SearchSessionHits(ctx, criteria, nil)
		if err != nil {
			t.Fatalf("SearchSessionHits() error = %v", err)
		}
		if len(sessions) != 0 {
			t.Fatalf("session-id filter matched event session should not invent extra sessions: %+v", sessions)
		}
	})

	t.Run("kind filter returns no sessions", func(t *testing.T) {
		criteria := apptypes.NewEventSearchCriteriaBuilder(20).
			Query("filter-needle").
			Workspace(wsA).
			Kind(types.EventKindNote).
			Build()
		sessions, err := sut.SearchSessionHits(ctx, criteria, nil)
		if err != nil {
			t.Fatalf("SearchSessionHits() error = %v", err)
		}
		if len(sessions) != 0 {
			t.Fatalf("kind filter must fail closed for sessions, got %d", len(sessions))
		}
	})

	t.Run("session group belongs to the first page only", func(t *testing.T) {
		criteria := apptypes.NewEventSearchCriteriaBuilder(20).
			Query("filter-needle").
			Workspace(wsA).
			Offset(10).
			Build()
		sessions, err := sut.SearchSessionHits(ctx, criteria, nil)
		if err != nil {
			t.Fatalf("SearchSessionHits() error = %v", err)
		}
		if len(sessions) != 0 {
			t.Fatalf("offset page repeated the session group: %+v", sessions)
		}
	})

	t.Run("from/to filter", func(t *testing.T) {
		from := time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)
		to := time.Date(2026, 6, 3, 0, 0, 0, 0, time.UTC)
		criteria := apptypes.NewEventSearchCriteriaBuilder(20).
			Query("filter-needle").
			From(from).
			To(to).
			Build()
		// Recent events fall outside the older window.
		events, err := sut.Search(ctx, criteria.Query(), "", "", "", "", "", from, to, 20, 0, false)
		if err != nil {
			t.Fatalf("Search() error = %v", err)
		}
		if len(events) != 0 {
			t.Fatalf("len(events) = %d, want 0", len(events))
		}
		sessions, err := sut.SearchSessionHits(ctx, criteria, nil)
		if err != nil {
			t.Fatalf("SearchSessionHits() error = %v", err)
		}
		// sess-old-a starts 2026-06-01 00:00 which is before from; only sess-old-b.
		if diff := cmp.Diff([]string{"sess-old-b"}, sessionHitIDs(sessions)); diff != "" {
			t.Fatalf("sessions (-want +got):\n%s", diff)
		}
	})
}

// Trigram FTS cannot index a term shorter than three characters, so a short
// query routed into the projection would return nothing while the same query
// answered before the projection existed. The result must not depend on which
// tier happens to be authoritative.
func TestSearchProjectionReadPath_ShortQueryAnswersUnderBothTiers(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "traceary.db")
	sut, store := newEventDatasource(t, dbPath, onDiskSQLiteMigrations(t))
	if err := store.Initialize(ctx); err != nil {
		t.Fatalf("Initialize() error = %v", err)
	}

	workspace := types.Workspace("github.com/duck8823/traceary")
	base := time.Date(2026, 8, 6, 14, 0, 0, 0, time.UTC)
	event := newSearchEventWithSession(t, "evt-short", "sess-short", workspace.String(), "rewrote the go build script", base)
	if err := sut.Save(ctx, event); err != nil {
		t.Fatalf("Save() error = %v", err)
	}

	search := func() []string {
		t.Helper()
		got, err := sut.Search(ctx, "go", workspace, "", "", "", "", time.Time{}, time.Time{}, 20, 0, false)
		if err != nil {
			t.Fatalf("Search() error = %v", err)
		}
		return eventIDs(got)
	}

	before := search()
	if diff := cmp.Diff([]string{"evt-short"}, before); diff != "" {
		t.Fatalf("short query before projection (-want +got):\n%s", diff)
	}

	seedCompleteProjection(t, dbPath, "gen-short", []projectionRecentSeed{
		{eventID: "evt-short", sessionID: "sess-short", body: "rewrote the go build script", createdAt: base},
	}, nil, nil)

	after := search()
	if diff := cmp.Diff(before, after); diff != "" {
		t.Fatalf("short query changed once the projection became authoritative (-before +after):\n%s", diff)
	}
}

// A complete generation is a snapshot. Migration 038's insert trigger records
// the source sequence of a new event but never adds it to the projection, and
// an insert does not drift a complete generation — so reading the projection
// alone would stop returning anything recorded after the last rebuild.
func TestSearchProjectionReadPath_EventsAppendedAfterRebuildStayFindable(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "traceary.db")
	sut, store := newEventDatasource(t, dbPath, onDiskSQLiteMigrations(t))
	if err := store.Initialize(ctx); err != nil {
		t.Fatalf("Initialize() error = %v", err)
	}

	workspace := types.Workspace("github.com/duck8823/traceary")
	base := time.Date(2026, 8, 6, 14, 0, 0, 0, time.UTC)
	indexed := newSearchEventWithSession(t, "evt-indexed", "sess-tail", workspace.String(), "freshness-marker in the projection", base)
	if err := sut.Save(ctx, indexed); err != nil {
		t.Fatalf("Save(indexed) error = %v", err)
	}
	seedCompleteProjection(t, dbPath, "gen-tail", []projectionRecentSeed{
		{eventID: "evt-indexed", sessionID: "sess-tail", body: "freshness-marker in the projection", createdAt: base},
	}, nil, nil)

	// Recorded after the generation completed: present in events, absent from
	// the projection, and the generation is still `complete`.
	appended := newSearchEventWithSession(t, "evt-appended", "sess-tail", workspace.String(), "freshness-marker recorded after the rebuild", base.Add(time.Hour))
	if err := sut.Save(ctx, appended); err != nil {
		t.Fatalf("Save(appended) error = %v", err)
	}

	got, err := sut.Search(ctx, "freshness-marker", workspace, "", "", "", "", time.Time{}, time.Time{}, 20, 0, false)
	if err != nil {
		t.Fatalf("Search() error = %v", err)
	}
	if diff := cmp.Diff([]string{"evt-appended", "evt-indexed"}, eventIDs(got)); diff != "" {
		t.Fatalf("Search() must include events appended after the rebuild (-want +got):\n%s", diff)
	}
}

// sessions carries no persisted _norm column, so started_at comparisons must go
// through ts_norm on both sides. Raw RFC3339Nano comparison drops a sub-second
// timestamp out of range because '.' 0x2E sorts below 'Z' 0x5A (#1185).
func TestSearchProjectionReadPath_SessionFromFilterHandlesSubSecondStartedAt(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "traceary.db")
	sut, store := newEventDatasource(t, dbPath, onDiskSQLiteMigrations(t))
	if err := store.Initialize(ctx); err != nil {
		t.Fatalf("Initialize() error = %v", err)
	}

	workspace := types.Workspace("github.com/duck8823/traceary")
	from := time.Date(2026, 6, 19, 10, 0, 0, 0, time.UTC)
	seedCompleteProjection(t, dbPath, "gen-subsecond", nil, []projectionSessionSeed{
		{
			sessionID:  "sess-subsecond",
			summary:    "planning notes with subsecond-marker",
			eventCount: 3,
			// 500ms after `from`, so it is inside the range in real time.
			startedAt: from.Add(500 * time.Millisecond),
			workspace: workspace.String(),
			client:    "cli",
			agent:     "codex",
		},
	}, nil)

	criteria := apptypes.NewEventSearchCriteriaBuilder(20).
		Query("subsecond-marker").
		Workspace(workspace).
		From(from).
		Build()
	sessions, err := sut.SearchSessionHits(ctx, criteria, nil)
	if err != nil {
		t.Fatalf("SearchSessionHits() error = %v", err)
	}
	if diff := cmp.Diff([]string{"sess-subsecond"}, sessionHitIDs(sessions)); diff != "" {
		t.Fatalf("sessions (-want +got):\n%s", diff)
	}
}

type projectionRecentSeed struct {
	eventID   string
	sessionID string
	body      string
	createdAt time.Time
}

type projectionSessionSeed struct {
	sessionID  string
	summary    string
	eventCount int
	startedAt  time.Time
	workspace  string
	client     string
	agent      string
	keywords   []string
}

func seedCompleteProjection(
	t *testing.T,
	dbPath string,
	generationID string,
	recent []projectionRecentSeed,
	sessions []projectionSessionSeed,
	_ any,
) {
	t.Helper()
	openRawDB(t, dbPath, func(db *sql.DB) {
		// high_water is the source sequence reached when the generation
		// completed, so events saved afterwards fall in the tail.
		if _, err := db.Exec(`
			UPDATE search_projection_state
			   SET generation_id = ?,
			       active_generation_id = ?,
			       state = 'complete',
			       phase = 'complete',
			       high_water = (SELECT COALESCE(MAX(sequence), 0) FROM search_projection_source_sequence)
			 WHERE singleton = 1`,
			generationID, generationID,
		); err != nil {
			t.Fatalf("activate projection: %v", err)
		}
		if _, err := db.Exec(`
			INSERT OR REPLACE INTO search_projection_generation_lifecycle(
				generation_id, state, config_hash, source_revision, high_water
			) VALUES (?, 'complete', 'test', 0, 100)`,
			generationID,
		); err != nil {
			t.Fatalf("lifecycle: %v", err)
		}
		for i, doc := range recent {
			// Store lowercased body so ASCII folding matches eventSearchFTSPhrase.
			body := foldASCIIForTest(doc.body)
			if _, err := db.Exec(`
				INSERT INTO search_projection_recent_documents(
					generation_id, event_rowid, event_id, created_at_norm, body_text, decoded_bytes
				) VALUES (?, ?, ?, ?, ?, ?)`,
				generationID,
				i+1,
				doc.eventID,
				doc.createdAt.UTC().Format(time.RFC3339Nano),
				body,
				len(body),
			); err != nil {
				t.Fatalf("insert recent doc %s: %v", doc.eventID, err)
			}
		}
		for _, session := range sessions {
			if _, err := db.Exec(`
				INSERT OR REPLACE INTO sessions(
					session_id, started_at, client, agent, workspace
				) VALUES (?, ?, ?, ?, ?)`,
				session.sessionID,
				session.startedAt.UTC().Format(time.RFC3339Nano),
				session.client,
				session.agent,
				session.workspace,
			); err != nil {
				t.Fatalf("insert session %s: %v", session.sessionID, err)
			}
			if _, err := db.Exec(`
				INSERT INTO search_projection_session_summaries(
					generation_id, session_id, event_count, summary_text, projection_version, summary_version
				) VALUES (?, ?, ?, ?, 1, 1)`,
				generationID, session.sessionID, session.eventCount, session.summary,
			); err != nil {
				t.Fatalf("insert summary %s: %v", session.sessionID, err)
			}
			for _, keyword := range session.keywords {
				if _, err := db.Exec(`
					INSERT INTO search_projection_session_keywords(
						generation_id, session_id, keyword, occurrences, keyword_version
					) VALUES (?, ?, ?, 1, 1)`,
					generationID, session.sessionID, foldASCIIForTest(keyword),
				); err != nil {
					t.Fatalf("insert keyword %s/%s: %v", session.sessionID, keyword, err)
				}
			}
		}
	})
}

func openRawDB(t *testing.T, dbPath string, fn func(*sql.DB)) {
	t.Helper()
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("sql.Open: %v", err)
	}
	defer func() {
		if err := db.Close(); err != nil {
			t.Fatalf("db.Close: %v", err)
		}
	}()
	if _, err := db.Exec(`PRAGMA foreign_keys = ON`); err != nil {
		t.Fatalf("foreign_keys: %v", err)
	}
	fn(db)
}

func newSearchEventWithSession(
	t *testing.T,
	eventIDValue string,
	sessionIDValue string,
	workspace string,
	body string,
	createdAt time.Time,
) *model.Event {
	t.Helper()
	eventID, err := types.EventIDFrom(eventIDValue)
	if err != nil {
		t.Fatalf("EventIDFrom() error = %v", err)
	}
	agent, err := types.AgentFrom("codex")
	if err != nil {
		t.Fatalf("AgentFrom() error = %v", err)
	}
	sessionID, err := types.SessionIDFrom(sessionIDValue)
	if err != nil {
		t.Fatalf("SessionIDFrom() error = %v", err)
	}
	return model.EventOf(
		eventID,
		types.EventKindNote,
		types.Client("cli"),
		agent,
		sessionID,
		types.Workspace(workspace),
		body,
		createdAt,
	)
}

func sessionHitIDs(hits []apptypes.SearchSessionHit) []string {
	out := make([]string, 0, len(hits))
	for _, hit := range hits {
		out = append(out, hit.SessionID().String())
	}
	return out
}

func foldASCIIForTest(value string) string {
	out := make([]rune, 0, len(value))
	for _, r := range value {
		if r >= 'A' && r <= 'Z' {
			out = append(out, r+('a'-'A'))
			continue
		}
		out = append(out, r)
	}
	return string(out)
}
