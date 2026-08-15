package sqlite_test

import (
	"context"
	"database/sql"
	"fmt"
	"math"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	_ "modernc.org/sqlite"

	apptypes "github.com/duck8823/traceary/application/types"
	"github.com/duck8823/traceary/domain/types"
)

const sessionTierCompareFTS = "search_projection_session_compare_fts"

const sessionTierFamilyBytesSQL = `
SELECT COALESCE(SUM(pgsize), 0)
  FROM dbstat
 WHERE name IN (
         SELECT name
           FROM sqlite_schema
          WHERE name GLOB 'search_projection_*'
             OR tbl_name GLOB 'search_projection_*'
             OR name GLOB 'literal_search_*'
             OR tbl_name GLOB 'literal_search_*'
       )`

// TestSessionTierKeepsLikePathAndMeasuresPorterFTS is the #1756 scratch
// measurement. SearchSessionPage stays on exact-keyword + LIKE. The porter
// FTS is created only for the comparison, then dropped.
func TestSessionTierKeepsLikePathAndMeasuresPorterFTS(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	workspace := types.Workspace("github.com/duck8823/traceary")
	dbPath := filepath.Join(t.TempDir(), "traceary.db")
	sut, store := newEventDatasource(t, dbPath, onDiskSQLiteMigrations(t))
	if err := store.Initialize(ctx); err != nil {
		t.Fatalf("Initialize() error = %v", err)
	}

	base := time.Date(2026, 6, 19, 10, 0, 0, 0, time.UTC)
	labeled := []projectionSessionSeed{
		{sessionID: "sess-unique", summary: "discussed unique-session-marker during planning", eventCount: 12, startedAt: base, workspace: workspace.String(), client: "cli", agent: "codex"},
		{sessionID: "sess-filter", summary: "filter-needle older summary", eventCount: 4, startedAt: base.Add(time.Minute), workspace: workspace.String(), client: "cli", agent: "codex"},
		{sessionID: "sess-subsecond", summary: "planning notes with subsecond-marker", eventCount: 3, startedAt: base.Add(2 * time.Second), workspace: workspace.String(), client: "cli", agent: "codex"},
		{sessionID: "sess-deployed", summary: "the service was deployed to staging", eventCount: 8, startedAt: base.Add(3 * time.Minute), workspace: workspace.String(), client: "cli", agent: "codex"},
		{sessionID: "sess-deploy", summary: "deploy the service tonight", eventCount: 5, startedAt: base.Add(4 * time.Minute), workspace: workspace.String(), client: "cli", agent: "codex"},
		{sessionID: "sess-case", summary: "Discussed DEPLOY pipeline", eventCount: 2, startedAt: base.Add(5 * time.Minute), workspace: workspace.String(), client: "cli", agent: "codex"},
		{sessionID: "sess-undeploy", summary: "we will undeploy later", eventCount: 1, startedAt: base.Add(6 * time.Minute), workspace: workspace.String(), client: "cli", agent: "codex"},
		{sessionID: "sess-cafe-ascii", summary: "cafe rollout notes", eventCount: 2, startedAt: base.Add(7 * time.Minute), workspace: workspace.String(), client: "cli", agent: "codex"},
		{sessionID: "sess-cafe-accent", summary: "café rollout notes", eventCount: 2, startedAt: base.Add(8 * time.Minute), workspace: workspace.String(), client: "cli", agent: "codex"},
	}
	seedCompleteProjection(t, dbPath, "gen-1756", nil, labeled, nil)

	const fillers = 2000
	openRawDB(t, dbPath, func(db *sql.DB) {
		tx, err := db.Begin()
		if err != nil {
			t.Fatalf("begin filler insert: %v", err)
		}
		defer func() { _ = tx.Rollback() }()
		for i := 0; i < fillers; i++ {
			id := fmt.Sprintf("sess-fill-%04d", i)
			started := base.Add(time.Duration(i+10) * time.Minute).UTC().Format(time.RFC3339Nano)
			if _, err := tx.Exec(`
				INSERT OR REPLACE INTO sessions(session_id, started_at, client, agent, workspace)
				VALUES (?, ?, 'cli', 'codex', ?)`, id, started, workspace.String()); err != nil {
				t.Fatalf("insert filler session %s: %v", id, err)
			}
			summary := fmt.Sprintf("session filler notes %d about planning and review", i)
			if _, err := tx.Exec(`
				INSERT INTO search_projection_session_summaries(
					generation_id, session_id, event_count, summary_text, projection_version, summary_version
				) VALUES ('gen-1756', ?, 1, ?, 1, 1)`, id, summary); err != nil {
				t.Fatalf("insert filler summary %s: %v", id, err)
			}
		}
		if err := tx.Commit(); err != nil {
			t.Fatalf("commit fillers: %v", err)
		}
	})

	queries := []struct {
		name     string
		query    string
		relevant []string
	}{
		{name: "unique-session-marker", query: "unique-session-marker", relevant: []string{"sess-unique"}},
		{name: "filter-needle", query: "filter-needle", relevant: []string{"sess-filter"}},
		{name: "subsecond-marker", query: "subsecond-marker", relevant: []string{"sess-subsecond"}},
		{name: "deploy-stem", query: "deploy", relevant: []string{"sess-deployed", "sess-deploy", "sess-case"}},
		{name: "deploy-case", query: "Deploy", relevant: []string{"sess-deployed", "sess-deploy", "sess-case"}},
		{name: "cafe-ascii", query: "cafe", relevant: []string{"sess-cafe-ascii"}},
		{name: "cafe-accent", query: "café", relevant: []string{"sess-cafe-accent"}},
	}

	likeByQuery := map[string]sessionTierPathStats{}
	for _, q := range queries {
		criteria := apptypes.NewEventSearchCriteriaBuilder(50).
			Query(q.query).
			Workspace(workspace).
			Build()
		hits, err := sut.SearchSessionPage(ctx, criteria, nil)
		if err != nil {
			t.Fatalf("SearchSessionPage(%q) error = %v", q.query, err)
		}
		if hits.State() != apptypes.SearchSessionTierReady {
			t.Fatalf("SearchSessionPage(%q) state = %q, want ready", q.query, hits.State())
		}
		got := sessionHitIDs(hits.Hits())
		samples := make([]time.Duration, 0, 20)
		for i := 0; i < 20; i++ {
			start := time.Now()
			page, err := sut.SearchSessionPage(ctx, criteria, nil)
			elapsed := time.Since(start)
			if err != nil {
				t.Fatalf("SearchSessionPage(%q) timed error = %v", q.query, err)
			}
			if page.State() != apptypes.SearchSessionTierReady {
				t.Fatalf("SearchSessionPage(%q) timed state = %q", q.query, page.State())
			}
			samples = append(samples, elapsed)
		}
		likeByQuery[q.name] = measurePath(got, q.relevant, samples)
	}

	deployLike := likeByQuery["deploy-stem"]
	if !containsAll(deployLike.hits, []string{"sess-deployed", "sess-deploy", "sess-case"}) {
		t.Fatalf("LIKE deploy hits = %v, want stemming-as-substring of deployed/deploy/DEPLOY", deployLike.hits)
	}
	if deployLike.recall != 1 {
		t.Fatalf("LIKE deploy recall = %v, want 1 (stemming is not a LIKE miss)", deployLike.recall)
	}

	var familyBefore, compareFTS, familyAfterDrop int64
	ftsByQuery := map[string]sessionTierPathStats{}
	openRawDB(t, dbPath, func(db *sql.DB) {
		if err := db.QueryRow(sessionTierFamilyBytesSQL).Scan(&familyBefore); err != nil {
			t.Fatalf("family before: %v", err)
		}
		if _, err := db.Exec(`
			CREATE VIRTUAL TABLE ` + sessionTierCompareFTS + ` USING fts5(
				session_id UNINDEXED,
				summary_text,
				tokenize = 'porter unicode61'
			)`); err != nil {
			t.Fatalf("create compare FTS: %v", err)
		}
		if _, err := db.Exec(`
			INSERT INTO ` + sessionTierCompareFTS + `(session_id, summary_text)
			SELECT session_id, summary_text
			  FROM search_projection_session_summaries
			 WHERE generation_id = 'gen-1756'`); err != nil {
			t.Fatalf("populate compare FTS: %v", err)
		}
		if err := db.QueryRow(`
			SELECT COALESCE(SUM(pgsize),0)
			  FROM dbstat
			 WHERE name IN (
			         SELECT name FROM sqlite_schema
			          WHERE name GLOB '` + sessionTierCompareFTS + `*'
			             OR tbl_name GLOB '` + sessionTierCompareFTS + `*'
			       )`).Scan(&compareFTS); err != nil {
			t.Fatalf("compare FTS dbstat: %v", err)
		}
		if compareFTS <= 0 {
			t.Fatalf("compare FTS dbstat = %d, want a positive family charge", compareFTS)
		}

		for _, q := range queries {
			phrase := sessionTierFTSPhrase(q.query)
			got := queryCompareFTS(t, db, phrase)
			samples := make([]time.Duration, 0, 20)
			for i := 0; i < 20; i++ {
				start := time.Now()
				_ = queryCompareFTS(t, db, phrase)
				samples = append(samples, time.Since(start))
			}
			ftsByQuery[q.name] = measurePath(got, q.relevant, samples)
		}

		if _, err := db.Exec(`DROP TABLE ` + sessionTierCompareFTS); err != nil {
			t.Fatalf("drop compare FTS: %v", err)
		}
		if err := db.QueryRow(sessionTierFamilyBytesSQL).Scan(&familyAfterDrop); err != nil {
			t.Fatalf("family after drop: %v", err)
		}
		var leftover int
		if err := db.QueryRow(`
			SELECT COUNT(*) FROM sqlite_master
			 WHERE name LIKE 'search_projection_session_%fts%'
			    OR name LIKE 'search_projection_session%fts%'`).Scan(&leftover); err != nil {
			t.Fatalf("schema leftover: %v", err)
		}
		if leftover != 0 {
			t.Fatalf("schema grew a session-tier FTS (%d leftover names)", leftover)
		}
	})
	if familyAfterDrop != familyBefore {
		t.Fatalf("family after drop = %d, before = %d", familyAfterDrop, familyBefore)
	}

	const ceiling = 1464 << 20
	t.Logf("session-tier #1756 family_before=%d compare_fts=%d family_after_drop=%d ceiling=%d recent_bytes_displaced=%d",
		familyBefore, compareFTS, familyAfterDrop, ceiling, compareFTS)
	for _, q := range queries {
		like := likeByQuery[q.name]
		fts := ftsByQuery[q.name]
		t.Logf("query=%s like_hits=%v like_recall=%.2f like_p50=%s like_p95=%s like_extra=%v fts_hits=%v fts_recall=%.2f fts_p50=%s fts_p95=%s fts_extra=%v fts_missing=%v",
			q.name, like.hits, like.recall, like.p50, like.p95, like.extra,
			fts.hits, fts.recall, fts.p50, fts.p95, fts.extra, fts.missing)
	}

	// Cafe ASCII is a LIKE hit; accented café is a LIKE hit for the accented
	// query. Searching "cafe" is not required to match "café" — ASCII fold
	// only, same as the keyword path.
	if !containsAll(likeByQuery["cafe-ascii"].hits, []string{"sess-cafe-ascii"}) {
		t.Fatalf("LIKE cafe hits = %v, want sess-cafe-ascii", likeByQuery["cafe-ascii"].hits)
	}
	if !containsAll(likeByQuery["cafe-accent"].hits, []string{"sess-cafe-accent"}) {
		t.Fatalf("LIKE café hits = %v, want sess-cafe-accent", likeByQuery["cafe-accent"].hits)
	}
}

func sessionTierFTSPhrase(query string) string {
	folded := strings.Map(func(r rune) rune {
		if r >= 'A' && r <= 'Z' {
			return r + ('a' - 'A')
		}
		return r
	}, strings.TrimSpace(query))
	return `"` + strings.ReplaceAll(folded, `"`, `""`) + `"`
}

func queryCompareFTS(t *testing.T, db *sql.DB, phrase string) []string {
	t.Helper()
	rows, err := db.Query(`SELECT session_id FROM `+sessionTierCompareFTS+` WHERE `+sessionTierCompareFTS+` MATCH ?`, phrase)
	if err != nil {
		t.Fatalf("compare FTS MATCH %s: %v", phrase, err)
	}
	defer func() { _ = rows.Close() }()
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			t.Fatalf("scan compare FTS: %v", err)
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate compare FTS: %v", err)
	}
	sort.Strings(ids)
	return ids
}

type sessionTierPathStats struct {
	hits    []string
	recall  float64
	p50     time.Duration
	p95     time.Duration
	extra   []string
	missing []string
}

func measurePath(got, relevant []string, samples []time.Duration) sessionTierPathStats {
	sorted := append([]string(nil), got...)
	sort.Strings(sorted)
	relevantSet := map[string]struct{}{}
	for _, id := range relevant {
		relevantSet[id] = struct{}{}
	}
	gotSet := map[string]struct{}{}
	for _, id := range sorted {
		gotSet[id] = struct{}{}
	}
	var extra, missing []string
	hitRelevant := 0
	for _, id := range relevant {
		if _, ok := gotSet[id]; ok {
			hitRelevant++
		} else {
			missing = append(missing, id)
		}
	}
	for _, id := range sorted {
		if _, ok := relevantSet[id]; !ok {
			extra = append(extra, id)
		}
	}
	recall := 0.0
	if len(relevant) > 0 {
		recall = float64(hitRelevant) / float64(len(relevant))
	}
	return sessionTierPathStats{
		hits:    sorted,
		recall:  recall,
		p50:     durationPercentile(samples, 0.50),
		p95:     durationPercentile(samples, 0.95),
		extra:   extra,
		missing: missing,
	}
}

func durationPercentile(samples []time.Duration, p float64) time.Duration {
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

func containsAll(got, want []string) bool {
	set := map[string]struct{}{}
	for _, id := range got {
		set[id] = struct{}{}
	}
	for _, id := range want {
		if _, ok := set[id]; !ok {
			return false
		}
	}
	return true
}
