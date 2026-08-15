package sqlite

import (
	"context"
	"fmt"
	"math"
	"sort"
	"testing"
	"time"

	apptypes "github.com/duck8823/traceary/application/types"
	"github.com/duck8823/traceary/domain/types"
)

// TestRecentFTSDoesNotEarnDecodeWalk is the #1842 scratch measurement.
// Shipped search is the decode walk. A comparison trigram FTS is built only
// for the timing table, then dropped. Decision: delete the write-only tier.
func TestRecentFTSDoesNotEarnDecodeWalk(t *testing.T) {
	var events []struct{ id, body, created string }
	now := time.Date(2026, 6, 30, 0, 0, 0, 0, time.UTC)
	for i := 0; i < 120; i++ {
		body := fmt.Sprintf("session filler notes %d about planning and review", i)
		if i == 7 {
			body = "unique-recent-marker during planning"
		}
		if i%10 == 0 {
			body += " shared-token"
		}
		events = append(events, struct{ id, body, created string }{
			id:      fmt.Sprintf("evt-%03d", i),
			body:    body,
			created: now.Add(-time.Duration(120-i) * time.Minute).Format(time.RFC3339Nano),
		})
	}
	store, db := newCapacityTestStore(t, events)
	driveToCompletion(t, store, capacityBudget(64<<20), now)
	ctx := context.Background()

	// External-content FTS COUNT(*) follows the documents table. Writers
	// gone means MATCH is empty even while documents exist.
	var ftsHits int
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM search_projection_recent_fts WHERE search_projection_recent_fts MATCH ?`, eventSearchFTSPhrase("unique-recent-marker")).Scan(&ftsHits); err != nil {
		t.Fatalf("shipped FTS inspect: %v", err)
	}
	if ftsHits != 0 {
		t.Fatalf("shipped recent FTS MATCH = %d, want 0 after writer drop", ftsHits)
	}
	var leftover int
	if err := db.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM sqlite_master
		 WHERE type='trigger' AND name IN ('search_projection_recent_ai','search_projection_recent_ad')`).Scan(&leftover); err != nil {
		t.Fatalf("trigger leftover: %v", err)
	}
	if leftover != 0 {
		t.Fatalf("recent FTS writer triggers still present (%d)", leftover)
	}

	sut := NewEventDatasource(store)
	ws := types.Workspace("ws")
	queries := []struct {
		name  string
		query string
		want  string
	}{
		{name: "narrow", query: "unique-recent-marker", want: "evt-007"},
		{name: "broad", query: "shared-token", want: "evt-110"},
	}

	if _, err := db.ExecContext(ctx, `
		CREATE VIRTUAL TABLE recent_fts_compare USING fts5(
			event_id UNINDEXED,
			body_text,
			tokenize = 'trigram case_sensitive 1'
		)`); err != nil {
		t.Fatalf("create compare FTS: %v", err)
	}
	if _, err := db.ExecContext(ctx, `
		INSERT INTO recent_fts_compare(event_id, body_text)
		SELECT event_id, body_text FROM search_projection_recent_documents`); err != nil {
		t.Fatalf("populate compare FTS: %v", err)
	}

	for _, q := range queries {
		criteria := apptypes.NewEventSearchCriteriaBuilder(20).Query(q.query).Workspace(ws).Build()
		got, err := sut.Search(ctx, criteria.Query(), ws, "", "", "", "", time.Time{}, time.Time{}, 20, 0, false)
		if err != nil {
			t.Fatalf("Search(%s) error = %v", q.name, err)
		}
		ids := eventIDs(got)
		if len(ids) == 0 || ids[0] != q.want {
			t.Fatalf("Search(%s) first = %v, want %s (fingerprints must still work)", q.name, ids, q.want)
		}
		walkSamples := make([]time.Duration, 0, 20)
		for i := 0; i < 20; i++ {
			start := time.Now()
			_, err := sut.Search(ctx, criteria.Query(), ws, "", "", "", "", time.Time{}, time.Time{}, 20, 0, false)
			if err != nil {
				t.Fatalf("Search(%s) timed: %v", q.name, err)
			}
			walkSamples = append(walkSamples, time.Since(start))
		}
		phrase := eventSearchFTSPhrase(q.query)
		ftsSamples := make([]time.Duration, 0, 20)
		var ftsCount int
		for i := 0; i < 20; i++ {
			start := time.Now()
			if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM recent_fts_compare WHERE recent_fts_compare MATCH ?`, phrase).Scan(&ftsCount); err != nil {
				t.Fatalf("compare FTS %s: %v", q.name, err)
			}
			ftsSamples = append(ftsSamples, time.Since(start))
		}
		if ftsCount == 0 {
			t.Fatalf("compare FTS %s hit 0 rows", q.name)
		}
		t.Logf("query=%s walk_hits=%d walk_p50=%s walk_p95=%s fts_hits=%d fts_p50=%s fts_p95=%s",
			q.name, len(got), durationAt(walkSamples, 0.50), durationAt(walkSamples, 0.95),
			ftsCount, durationAt(ftsSamples, 0.50), durationAt(ftsSamples, 0.95))
	}

	if _, err := db.ExecContext(ctx, `DROP TABLE recent_fts_compare`); err != nil {
		t.Fatalf("drop compare FTS: %v", err)
	}
}

func TestCompactionDropsUnreadRecentFTSOnTheWorkCopy(t *testing.T) {
	store, db := newCapacityTestStore(t, nil)
	_ = store
	ctx := context.Background()
	var present int
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM sqlite_master WHERE name='search_projection_recent_fts'`).Scan(&present); err != nil {
		t.Fatal(err)
	}
	if present == 0 {
		t.Fatal("precondition: recent FTS table missing before compact reclaim")
	}
	if err := dropUnreadRecentFTSOn(ctx, db); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM sqlite_master WHERE name='search_projection_recent_fts'`).Scan(&present); err != nil {
		t.Fatal(err)
	}
	if present != 0 {
		t.Fatal("work copy still has search_projection_recent_fts")
	}
}

func durationAt(samples []time.Duration, p float64) time.Duration {
	if len(samples) == 0 {
		return 0
	}
	sorted := append([]time.Duration(nil), samples...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i] < sorted[j] })
	idx := int(math.Ceil(p*float64(len(sorted)))) - 1
	if idx < 0 {
		idx = 0
	}
	if idx >= len(sorted) {
		idx = len(sorted) - 1
	}
	return sorted[idx]
}
