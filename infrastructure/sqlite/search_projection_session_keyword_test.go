package sqlite_test

import (
	"context"
	"database/sql"
	"strings"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"

	apptypes "github.com/duck8823/traceary/application/types"
	"github.com/duck8823/traceary/domain/types"
)

func TestSearchSessionPageFilterableMatchesKeywordNotSummarySubstring(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	workspace := types.Workspace("ws")
	dbPath := t.TempDir() + "/store.db"
	sut, store := newEventDatasource(t, dbPath, onDiskSQLiteMigrations(t))
	if err := store.Initialize(ctx); err != nil {
		t.Fatalf("Initialize() error = %v", err)
	}
	base := time.Date(2026, 6, 19, 10, 0, 0, 0, time.UTC)
	seedCompleteProjection(t, dbPath, "gen-2170", nil, []projectionSessionSeed{
		{
			sessionID:  "sess-keyword",
			summary:    "unrelated summary text",
			eventCount: 2,
			startedAt:  base,
			workspace:  workspace.String(),
			client:     "cli",
			agent:      "codex",
			keywords:   []string{"needle2170"},
		},
		{
			sessionID:  "sess-substring",
			summary:    "the needle2170token is only in the summary",
			eventCount: 1,
			startedAt:  base.Add(time.Minute),
			workspace:  workspace.String(),
			client:     "cli",
			agent:      "codex",
		},
	}, nil)

	got, err := sut.SearchSessionPage(ctx, apptypes.NewEventSearchCriteriaBuilder(10).Query("needle2170").Workspace(workspace).Build(), nil)
	if err != nil {
		t.Fatalf("SearchSessionPage() error = %v", err)
	}
	if diff := cmp.Diff([]string{"sess-keyword"}, sessionHitIDs(got.Hits())); diff != "" {
		t.Fatalf("SearchSessionPage() IDs mismatch (-want +got):\n%s", diff)
	}

	empty, err := sut.SearchSessionPage(ctx, apptypes.NewEventSearchCriteriaBuilder(10).Query("xyzzy-nomatch-2170").Workspace(workspace).Build(), nil)
	if err != nil {
		t.Fatalf("SearchSessionPage(no-match) error = %v", err)
	}
	if len(empty.Hits()) != 0 {
		t.Fatalf("no-match hits = %v, want empty", sessionHitIDs(empty.Hits()))
	}

	openRawDB(t, dbPath, func(db *sql.DB) {
		rows, err := db.Query(`EXPLAIN QUERY PLAN
			SELECT sum.session_id, sum.summary_text, sum.event_count, s.started_at
			  FROM search_projection_session_keywords k INDEXED BY idx_search_projection_session_keywords_by_kw
			  JOIN search_projection_session_summaries sum
			    ON sum.generation_id = k.generation_id AND sum.session_id = k.session_id
			  JOIN sessions s ON s.session_id = sum.session_id
			 WHERE k.generation_id = (SELECT active_generation_id FROM search_projection_state WHERE singleton = 1)
			   AND k.keyword = ?
			   AND s.workspace = ?
			 ORDER BY s.started_at_norm DESC, s.session_id DESC LIMIT ?`,
			"xyzzy-nomatch-2170", workspace.String(), 10)
		if err != nil {
			t.Fatalf("EXPLAIN QUERY PLAN error = %v", err)
		}
		defer func() { _ = rows.Close() }()
		var plan []string
		for rows.Next() {
			var id, parent, unused int
			var detail string
			if err := rows.Scan(&id, &parent, &unused, &detail); err != nil {
				t.Fatal(err)
			}
			plan = append(plan, strings.ToLower(detail))
		}
		if err := rows.Err(); err != nil {
			t.Fatal(err)
		}
		joined := strings.Join(plan, "\n")
		if !strings.Contains(joined, "idx_search_projection_session_keywords_by_kw") {
			t.Fatalf("filterable plan does not use idx_search_projection_session_keywords_by_kw:\n%s", joined)
		}
		if strings.Contains(joined, "like") {
			t.Fatalf("filterable plan still LIKE-scans summaries:\n%s", joined)
		}
	})
}
