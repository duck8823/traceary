package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"

	apptypes "github.com/duck8823/traceary/application/types"
	"github.com/duck8823/traceary/application/usecase"
)

// newCapacityTestStore builds a migrated store parked at idle with optional
// seed events. Tests own the generation lifecycle from Start.
func newCapacityTestStore(t *testing.T, events []struct {
	id, body, created string
}) (*Database, *sql.DB) {
	t.Helper()
	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "store.db")
	store := NewDatabase(dbPath, os.DirFS(filepath.Join("..", "..", "schema", "sqlite", "migrations")))
	if err := NewStoreManagementDatasource(store).Initialize(ctx); err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite", sqliteDSN(dbPath))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if _, err = db.ExecContext(ctx, `
		INSERT OR IGNORE INTO sessions(session_id,started_at,client,agent,workspace)
		VALUES('sess-cap','2026-01-01T00:00:00Z','cli','agent','ws');
		UPDATE search_projection_state
		   SET generation_id=NULL,active_generation_id=NULL,config_hash='',source_revision=0,
		       high_water=0,checkpoint=0,phase='source',cleanup_scope='old',failure_class='',
		       state='idle',capacity_semantics_version=1,index_family_within_budget=-1
		 WHERE singleton=1;
		DELETE FROM search_projection_generation_lifecycle;
		DELETE FROM search_projection_recent_documents;
		DELETE FROM search_projection_session_summaries;
		DELETE FROM search_projection_session_keywords;
		DELETE FROM search_projection_command_aggregates;
		DELETE FROM literal_search_fingerprints;
		UPDATE search_projection_inventory_state SET generation_id='',cursor='',cursor_started=0,state='idle' WHERE singleton=1;
		UPDATE literal_search_projection_state SET generation_id='',high_water=0,state='missing' WHERE singleton=1;
	`); err != nil {
		t.Fatal(err)
	}
	for _, e := range events {
		if _, err = db.ExecContext(ctx,
			`INSERT INTO events(id,kind,agent,session_id,body,created_at,client,workspace) VALUES(?,'note','agent','sess-cap',?,?,'cli','ws')`,
			e.id, e.body, e.created,
		); err != nil {
			t.Fatal(err)
		}
	}
	return store, db
}

func capacityBudget(indexFamily int64) apptypes.SearchProjectionBudget {
	return apptypes.SearchProjectionBudget{
		Rows: 64, WallTime: time.Minute, LockTime: 5 * time.Second,
		StoredBytes: 8 << 20, DecodedBytes: 8 << 20, WriteBytes: 8 << 20,
		RecentAge: 365 * 24 * time.Hour, IndexFamilyBytes: indexFamily,
	}
}

func driveToCompletion(t *testing.T, store *Database, b apptypes.SearchProjectionBudget, now time.Time) {
	t.Helper()
	ctx := context.Background()
	if _, err := store.Start(ctx, b, now); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 200; i++ {
		p, err := resumeProjection(ctx, store, b, now)
		if err != nil {
			t.Fatalf("resume %d: %v", i, err)
		}
		if p.Completed {
			return
		}
	}
	t.Fatal("generation did not complete")
}

// searchProjectionScopedTblNames is the explicit generation-scoped tbl_name set
// mirrored from measure_search_projection_family_split.sql. Kept as a test
// fixture so mutating the production set to include a shared table is a red
// that this exhaustiveness check catches.
var searchProjectionScopedTblNames = map[string]struct{}{
	"search_projection_session_summaries":  {},
	"search_projection_session_keywords":   {},
	"search_projection_command_aggregates": {},
	"literal_search_fingerprints":          {},
}

// TestSearchProjectionFamilySplitIsExhaustiveAndClassifiesRecentSide asserts
// the three-way split against sqlite_schema (not a hand-written name list) and
// that recent + nonRecentScoped + nonRecentShared equals the total family
// figure. Shared permanently-resident objects and all four scoped tables are
// named explicitly so a silent reclassification fails.
func TestSearchProjectionFamilySplitIsExhaustiveAndClassifiesRecentSide(t *testing.T) {
	store, db := newCapacityTestStore(t, []struct{ id, body, created string }{
		{"e1", strings.Repeat("alpha beta gamma ", 200), "2026-06-01T12:00:00Z"},
		{"e2", strings.Repeat("delta epsilon zeta ", 200), "2026-06-02T12:00:00Z"},
	})
	now := time.Date(2026, 6, 3, 0, 0, 0, 0, time.UTC)
	driveToCompletion(t, store, capacityBudget(64<<20), now)

	ctx := context.Background()
	recent, nonRecentScoped, nonRecentShared, evidence := store.measureSearchProjectionFamilySplit(ctx, db)
	if evidence.Status != searchProjectionEvidenceMeasured {
		t.Fatalf("evidence=%+v", evidence)
	}
	total, totalEvidence := store.measureSearchProjectionFamilyBytes(ctx, db)
	if totalEvidence.Status != searchProjectionEvidenceMeasured {
		t.Fatalf("total evidence=%+v", totalEvidence)
	}
	if recent+nonRecentScoped+nonRecentShared != total {
		t.Fatalf("recent(%d)+scoped(%d)+shared(%d)=%d, total=%d",
			recent, nonRecentScoped, nonRecentShared, recent+nonRecentScoped+nonRecentShared, total)
	}
	if recent <= 0 {
		t.Fatalf("recent bytes = %d, want > 0 (documents+fts+indexes)", recent)
	}
	if nonRecentShared <= 0 {
		t.Fatalf("shared bytes = %d, want > 0 (state/source_sequence/etc.)", nonRecentShared)
	}

	// Classify every family object from sqlite_schema with the same predicates
	// the SQL uses and require every object is classified into exactly one bucket.
	rows, err := db.QueryContext(ctx, `
		SELECT name, type, tbl_name FROM sqlite_schema
		 WHERE name GLOB 'search_projection_*' OR tbl_name GLOB 'search_projection_*'
		    OR name GLOB 'literal_search_*' OR tbl_name GLOB 'literal_search_*'
		 ORDER BY name`)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = rows.Close() }()
	var recentNames, scopedNames, sharedNames, allNames []string
	for rows.Next() {
		var name, typ, tbl string
		if err = rows.Scan(&name, &typ, &tbl); err != nil {
			t.Fatal(err)
		}
		_ = typ // classification is name/tbl_name only; type is not a branch.
		allNames = append(allNames, name)
		isRecent := strings.HasPrefix(name, "search_projection_recent") ||
			strings.HasPrefix(tbl, "search_projection_recent")
		_, isScoped := searchProjectionScopedTblNames[tbl]
		switch {
		case isRecent:
			recentNames = append(recentNames, name)
		case isScoped:
			scopedNames = append(scopedNames, name)
		default:
			sharedNames = append(sharedNames, name)
		}
	}
	if err = rows.Err(); err != nil {
		t.Fatal(err)
	}
	if len(allNames) == 0 {
		t.Fatal("sqlite_schema reported no family objects")
	}
	if len(recentNames)+len(scopedNames)+len(sharedNames) != len(allNames) {
		t.Fatalf("classification incomplete: recent=%d scoped=%d shared=%d total=%d names=%v",
			len(recentNames), len(scopedNames), len(sharedNames), len(allNames), allNames)
	}
	// The Go mirror must agree with the SQL object-for-object, not merely be
	// complementary to it. Without this, dropping the tbl_name clause from the
	// SQL misclassifies every index and FTS shadow table onto the non-recent
	// side — inflating the reserve and shrinking the ceiling — and the
	// recent+scoped+shared==total assertion above still passes.
	var mirrorRecent, mirrorScoped, mirrorShared int64
	for _, name := range recentNames {
		var pages int64
		if err = db.QueryRowContext(ctx, `SELECT COALESCE(SUM(pgsize),0) FROM dbstat WHERE name = ?`, name).Scan(&pages); err != nil {
			t.Fatal(err)
		}
		mirrorRecent += pages
	}
	for _, name := range scopedNames {
		var pages int64
		if err = db.QueryRowContext(ctx, `SELECT COALESCE(SUM(pgsize),0) FROM dbstat WHERE name = ?`, name).Scan(&pages); err != nil {
			t.Fatal(err)
		}
		mirrorScoped += pages
	}
	for _, name := range sharedNames {
		var pages int64
		if err = db.QueryRowContext(ctx, `SELECT COALESCE(SUM(pgsize),0) FROM dbstat WHERE name = ?`, name).Scan(&pages); err != nil {
			t.Fatal(err)
		}
		mirrorShared += pages
	}
	if mirrorRecent != recent {
		t.Fatalf("SQL recent=%d, mirror over %v = %d; classification predicates diverged", recent, recentNames, mirrorRecent)
	}
	if mirrorScoped != nonRecentScoped {
		t.Fatalf("SQL scoped=%d, mirror over %v = %d; scoped set diverged", nonRecentScoped, scopedNames, mirrorScoped)
	}
	if mirrorShared != nonRecentShared {
		t.Fatalf("SQL shared=%d, mirror over %v = %d; shared set diverged", nonRecentShared, sharedNames, mirrorShared)
	}
	// Must include the documents table, FTS virtual table, and the eviction index
	// via tbl_name/name prefix alone (proves the removed clause is unnecessary).
	joinedRecent := strings.Join(recentNames, ",")
	for _, must := range []string{
		"search_projection_recent_documents",
		"search_projection_recent_fts",
		"idx_search_projection_recent_eviction",
	} {
		if !strings.Contains(joinedRecent, must) {
			t.Fatalf("recent side missing %q; recent=%v scoped=%v shared=%v", must, recentNames, scopedNames, sharedNames)
		}
	}
	// Session tier must not be classified as recent.
	for _, name := range recentNames {
		if strings.Contains(name, "session_summar") || strings.Contains(name, "session_keyword") {
			t.Fatalf("session tier object %q classified as recent", name)
		}
	}
	// Permanent shared objects — by name, not by exclusion — must land in shared.
	// search_projection_source_sequence and its UNIQUE index grow forever and
	// must never be discounted by a generation ratio.
	joinedShared := strings.Join(sharedNames, ",")
	if !strings.Contains(joinedShared, "search_projection_source_sequence") {
		t.Fatalf("shared missing search_projection_source_sequence; shared=%v scoped=%v", sharedNames, scopedNames)
	}
	// All four generation-scoped tables land in scoped (tbl_name match).
	joinedScoped := strings.Join(scopedNames, ",")
	for _, must := range []string{
		"search_projection_session_summaries",
		"search_projection_session_keywords",
		"search_projection_command_aggregates",
		"literal_search_fingerprints",
	} {
		if !strings.Contains(joinedScoped, must) {
			t.Fatalf("scoped missing %q; scoped=%v shared=%v", must, scopedNames, sharedNames)
		}
	}
	// Red when the production scoped set is mutated to include a shared table:
	// source_sequence would appear in scopedNames and fail the shared-by-name
	// assertion above (and the mirrorScoped/SQL comparison).
}

// TestSearchProjectionEvictionHonoursPersistedCeiling pins that eviction reads
// the persisted recent_source_ceiling_bytes column, not the caller's budget.
func TestSearchProjectionEvictionHonoursPersistedCeiling(t *testing.T) {
	body := strings.Repeat("0123456789", 5) // 50 bytes
	store, db := newCapacityTestStore(t, []struct{ id, body, created string }{
		{"old", body, "2026-06-01T10:00:00Z"},
		{"new", body, "2026-06-01T12:00:00Z"},
	})
	now := time.Date(2026, 6, 1, 13, 0, 0, 0, time.UTC)
	// Large physical budget so derivation does not exhaust; ceiling is pinned.
	b := capacityBudget(64 << 20)
	b.Rows = 10
	ctx := context.Background()
	if _, err := store.Start(ctx, b, now); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 20; i++ {
		p, err := resumeProjection(ctx, store, b, now)
		if err != nil {
			t.Fatal(err)
		}
		// Keep the ceiling at one document so the older row is always the
		// eviction target, even after source→eviction re-derives.
		if _, err = db.Exec(`UPDATE search_projection_state SET recent_source_ceiling_bytes=50 WHERE singleton=1`); err != nil {
			t.Fatal(err)
		}
		if p.Completed {
			break
		}
	}
	var ids string
	if err := db.QueryRow(`SELECT group_concat(event_id) FROM search_projection_recent_documents ORDER BY created_at_norm`).Scan(&ids); err != nil {
		t.Fatal(err)
	}
	if ids != "new" {
		t.Fatalf("retained=%q, want only newest under persisted ceiling 50", ids)
	}
	// Mutating the caller's budget must not change what is retained: re-run
	// status/eviction path with a different IndexFamilyBytes is blocked by
	// ConfigHash, so pin a different ceiling and resume is N/A once complete.
	// Instead assert the column used by selectProjectionCleanup.
	var ceiling int64
	if err := db.QueryRow(`SELECT recent_source_ceiling_bytes FROM search_projection_state`).Scan(&ceiling); err != nil {
		t.Fatal(err)
	}
	if ceiling != 50 {
		t.Fatalf("ceiling=%d, want pinned 50", ceiling)
	}
}

func TestSearchProjectionEvictionUnderCeilingRetainsRows(t *testing.T) {
	store, db := newCapacityTestStore(t, []struct{ id, body, created string }{
		{"retained", "body that is not expired", "2026-06-01T12:00:00Z"},
	})
	now := time.Date(2026, 6, 1, 13, 0, 0, 0, time.UTC)
	b := capacityBudget(64 << 20)
	b.RecentAge = 24 * time.Hour
	ctx := context.Background()
	if _, err := store.Start(ctx, b, now); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`UPDATE search_projection_state SET recent_source_ceiling_bytes=1048576 WHERE singleton=1`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`UPDATE search_projection_state SET recent_source_bytes=0 WHERE singleton=1`); err != nil {
		t.Fatal(err)
	}
	if _, err := resumeProjection(ctx, store, b, now); err != nil {
		t.Fatal(err)
	}
	if _, err := resumeProjection(ctx, store, b, now); err != nil {
		t.Fatal(err)
	}
	var count int
	if err := db.QueryRow(`SELECT COUNT(*) FROM search_projection_recent_documents`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("recent document count=%d, want 1", count)
	}
}

func TestSearchProjectionRecentResidencyAndCounterStayBoundedEachBatch(t *testing.T) {
	body := strings.Repeat("x", 40)
	var events []struct{ id, body, created string }
	for i := 0; i < 8; i++ {
		events = append(events, struct{ id, body, created string }{fmt.Sprintf("e%d", i), body, fmt.Sprintf("2026-06-01T12:%02d:00Z", i)})
	}
	store, db := newCapacityTestStore(t, events)
	now := time.Date(2026, 6, 2, 0, 0, 0, 0, time.UTC)
	b := capacityBudget(64 << 20)
	b.Rows = 1
	ctx := context.Background()
	if _, err := store.Start(ctx, b, now); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`UPDATE search_projection_state SET recent_source_ceiling_bytes=100 WHERE singleton=1`); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 100; i++ {
		p, err := resumeProjection(ctx, store, b, now)
		if err != nil {
			t.Fatalf("batch %d: %v", i, err)
		}
		var sum, counter int64
		if err = db.QueryRow(`SELECT COALESCE(SUM(length(CAST(body_text AS BLOB))),0) FROM search_projection_recent_documents`).Scan(&sum); err != nil {
			t.Fatal(err)
		}
		if err = db.QueryRow(`SELECT recent_source_bytes FROM search_projection_state`).Scan(&counter); err != nil {
			t.Fatal(err)
		}
		if sum > 140 || counter != sum {
			t.Fatalf("batch %d residency=%d counter=%d, want residency <= 140 and exact counter", i, sum, counter)
		}
		if p.Completed {
			return
		}
	}
	t.Fatal("projection did not complete")
}

// TestSearchProjectionCeilingRederivedAtSourceEvictionTransition asserts the
// source→eviction transition rewrites recent_source_ceiling_bytes from this
// generation's own sample.
func TestSearchProjectionCeilingRederivedAtSourceEvictionTransition(t *testing.T) {
	store, db := newCapacityTestStore(t, []struct{ id, body, created string }{
		{"e1", strings.Repeat("word ", 100), "2026-06-01T12:00:00Z"},
	})
	now := time.Date(2026, 6, 2, 0, 0, 0, 0, time.UTC)
	b := capacityBudget(64 << 20)
	ctx := context.Background()
	if _, err := store.Start(ctx, b, now); err != nil {
		t.Fatal(err)
	}
	// Force a distinctive Start-time ceiling so the re-derive is observable.
	if _, err := db.Exec(`UPDATE search_projection_state SET recent_source_ceiling_bytes=999999 WHERE singleton=1`); err != nil {
		t.Fatal(err)
	}
	var phase string
	for i := 0; i < 50; i++ {
		p, err := resumeProjection(ctx, store, b, now)
		if err != nil {
			t.Fatal(err)
		}
		if err = db.QueryRow(`SELECT phase FROM search_projection_state`).Scan(&phase); err != nil {
			t.Fatal(err)
		}
		if phase != "source" {
			break
		}
		if p.Completed {
			break
		}
	}
	if phase == "source" {
		t.Fatal("still in source phase")
	}
	var ceiling int64
	if err := db.QueryRow(`SELECT recent_source_ceiling_bytes FROM search_projection_state`).Scan(&ceiling); err != nil {
		t.Fatal(err)
	}
	if ceiling == 999999 {
		t.Fatalf("ceiling still Start-time pin %d; source→eviction did not re-derive", ceiling)
	}
	if ceiling <= 0 {
		t.Fatalf("ceiling=%d, want positive re-derived value", ceiling)
	}
}

// TestSearchProjectionCutoffPrefilterBoundsInsertion pins that a corpus far
// larger than the budget inserts roughly the retained set, not the whole age
// window, during the source phase.
func TestSearchProjectionCutoffPrefilterBoundsInsertion(t *testing.T) {
	var events []struct{ id, body, created string }
	// Large documents spanning many days; age window admits all, byte budget does not.
	for i := 0; i < 30; i++ {
		events = append(events, struct{ id, body, created string }{
			id:      "evt-" + itoa(i),
			body:    strings.Repeat("payload-body-content-", 800), // ~16 KiB each
			created: time.Date(2026, 5, 1, i, 0, 0, 0, time.UTC).Format(time.RFC3339Nano),
		})
	}
	store, db := newCapacityTestStore(t, events)
	now := time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC)
	// ~480 KiB of source text total. A 256 KiB family budget with the 2.16x
	// fallback admits ~118 KiB of source (~7 docs). Non-recent session-tier
	// pages stay well under 256 KiB so Start does not exhaust.
	b := capacityBudget(256 << 10)
	b.Rows = 64
	ctx := context.Background()
	if _, err := store.Start(ctx, b, now); err != nil {
		t.Fatal(err)
	}
	var cutoff string
	if err := db.QueryRow(`SELECT recent_cutoff_norm FROM search_projection_state`).Scan(&cutoff); err != nil {
		t.Fatal(err)
	}
	if cutoff == "" {
		t.Fatal("expected a byte cutoff for an oversized corpus")
	}
	// Drive source phase fully so every age-eligible document is considered.
	for i := 0; i < 200; i++ {
		p, err := resumeProjection(ctx, store, b, now)
		if err != nil {
			t.Fatal(err)
		}
		var phase string
		_ = db.QueryRow(`SELECT phase FROM search_projection_state`).Scan(&phase)
		if phase != "source" || p.Completed {
			break
		}
	}
	var inserted int
	if err := db.QueryRow(`SELECT COUNT(*) FROM search_projection_recent_documents`).Scan(&inserted); err != nil {
		t.Fatal(err)
	}
	// Without the prefilter all 30 would be retained under the age window.
	if inserted >= 30 {
		t.Fatalf("inserted %d recent rows (whole age window); cutoff prefilter did not bound insertion (cutoff=%q)", inserted, cutoff)
	}
	if inserted == 0 {
		t.Fatal("inserted 0 rows; prefilter over-restricted")
	}
}

// TestSearchProjectionCutoffSurvivesThinkingHeavyEnvelopes pins the slack
// factor. The prefilter counts body_plaintext_bytes — the whole stored
// envelope — while ExtractPlainBody keeps only text blocks, so a newer
// thinking-only envelope contributes its full size to the running sum and
// nothing at all to the projection. Without slack it alone pushes the walk
// past the ceiling and every older text event is excluded irreversibly:
// eviction cannot re-project what the source phase never inserted.
func TestSearchProjectionCutoffSurvivesThinkingHeavyEnvelopes(t *testing.T) {
	// ~513 KiB of reasoning that projects to nothing, on top of ~6 KiB of text.
	thinking, err := apptypes.MarshalEventBodyBlocks([]apptypes.EventBodyBlock{
		{Type: apptypes.EventBodyBlockTypeThinking, Text: strings.Repeat("internal reasoning ", 27000)},
	})
	if err != nil {
		t.Fatal(err)
	}
	events := []struct{ id, body, created string }{
		{"text-1", strings.Repeat("visible corpus text ", 100), "2026-05-01T10:00:00Z"},
		{"text-2", strings.Repeat("visible corpus text ", 100), "2026-05-01T11:00:00Z"},
		{"text-3", strings.Repeat("visible corpus text ", 100), "2026-05-01T12:00:00Z"},
		// Newest, and far larger than the whole visible corpus, but projects to "".
		{"thinking-only", thinking, "2026-05-01T13:00:00Z"},
	}
	store, db := newCapacityTestStore(t, events)
	now := time.Date(2026, 5, 2, 0, 0, 0, 0, time.UTC)
	// A 512 KiB budget derives a source ceiling near 220 KiB under the 2.16x
	// fallback: comfortably above the 6 KiB of visible text, and below the
	// thinking envelope the prefilter counts. Slack is what closes that gap.
	b := capacityBudget(512 << 10)
	b.Rows = 64
	ctx := context.Background()
	if _, err = store.Start(ctx, b, now); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 200; i++ {
		p, resumeErr := resumeProjection(ctx, store, b, now)
		if resumeErr != nil {
			t.Fatal(resumeErr)
		}
		var phase string
		_ = db.QueryRow(`SELECT phase FROM search_projection_state`).Scan(&phase)
		if phase != "source" || p.Completed {
			break
		}
	}
	var visible int
	if err = db.QueryRow(`SELECT COUNT(*) FROM search_projection_recent_documents WHERE event_id LIKE 'text-%'`).Scan(&visible); err != nil {
		t.Fatal(err)
	}
	if visible == 0 {
		var cutoff string
		_ = db.QueryRow(`SELECT recent_cutoff_norm FROM search_projection_state`).Scan(&cutoff)
		t.Fatalf("0 visible text documents projected (cutoff=%q); a thinking-only envelope excluded the whole visible corpus", cutoff)
	}
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b []byte
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	return string(b)
}

// TestSearchProjectionCutoffFailureDegradesNotBreaks pins that a cutoff walk
// failure leaves evidence unavailable with a visible reason and the generation
// still completes under the eviction ceiling.
func TestSearchProjectionCutoffFailureDegradesNotBreaks(t *testing.T) {
	store, db := newCapacityTestStore(t, []struct{ id, body, created string }{
		// Corpus large enough that a positive ceiling would normally walk for
		// a cutoff; we force the walk to time out via a cancelled context on
		// the cutoff helper and assert the reason is folded into evidence.
		{"e1", strings.Repeat("cutoff degrade body ", 500), "2026-06-01T12:00:00Z"},
		{"e2", strings.Repeat("cutoff degrade body ", 500), "2026-06-01T13:00:00Z"},
		{"e3", strings.Repeat("cutoff degrade body ", 500), "2026-06-01T14:00:00Z"},
	})
	now := time.Date(2026, 6, 2, 0, 0, 0, 0, time.UTC)
	ctx := context.Background()
	// Direct unit of the degrade path: a cancelled parent context must stop
	// the walk (WithTimeout inherits cancel) and return a non-empty reason,
	// distinct from ErrNoRows (corpus fits → empty reason).
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	cutoff, reason := store.deriveSearchProjectionRecentCutoff(cancelled, db, 1024)
	if cutoff != "" {
		t.Fatalf("cutoff=%q on cancelled context, want empty", cutoff)
	}
	if reason == "" {
		t.Fatal("cutoff reason empty on cancelled context; ErrNoRows and error are not distinguished")
	}
	if !strings.Contains(reason, "recent cutoff:") {
		t.Fatalf("reason=%q, want 'recent cutoff:' prefix", reason)
	}
	// Generation still completes when Start-time evidence is forced unavailable
	// for the same class of failure.
	b := capacityBudget(64 << 20)
	if _, err := store.Start(ctx, b, now); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`UPDATE search_projection_state SET recent_cutoff_norm='',capacity_evidence_status='unavailable',capacity_evidence_reason=? WHERE singleton=1`, reason); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 50; i++ {
		p, err := resumeProjection(ctx, store, b, now)
		if err != nil {
			t.Fatal(err)
		}
		if p.Completed {
			status, err := store.SearchProjectionStatus(ctx)
			if err != nil {
				t.Fatal(err)
			}
			if !status.Completed {
				t.Fatalf("state=%q, want complete", status.State)
			}
			// Contract: degrade never fails the generation. Evidence may be
			// re-derived at source→eviction to measured; the Start-time reason
			// is what this test pinned above via the helper.
			return
		}
	}
	t.Fatal("did not complete")
}

// TestSearchProjectionCutoffUsesBlobByteLength pins CAST-as-BLOB accounting
// for legacy NULL plaintext_bytes on non-ASCII text (#1749).
func TestSearchProjectionCutoffUsesBlobByteLength(t *testing.T) {
	// Three-byte UTF-8 runes: character length != byte length.
	nonASCII := strings.Repeat("日本語", 100) // 300 runes, 900 bytes
	_, db := newCapacityTestStore(t, []struct{ id, body, created string }{
		{"old", nonASCII, "2026-06-01T10:00:00Z"},
		{"mid", nonASCII, "2026-06-01T11:00:00Z"},
		{"new", nonASCII, "2026-06-01T12:00:00Z"},
	})
	// Force legacy NULL plaintext_bytes so the CAST fallback is the only path.
	if _, err := db.Exec(`UPDATE events SET body_plaintext_bytes=NULL, body_stored_bytes=NULL`); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	// Ceiling of ~1500 bytes: with byte-accurate counting keeps ~1 doc;
	// with character counting (300/doc) would keep more.
	var cutoff string
	err := db.QueryRowContext(ctx, selectSearchProjectionRecentCutoffSQL, int64(1500)).Scan(&cutoff)
	if err != nil {
		t.Fatal(err)
	}
	// Newest-first running sum exceeds 1500 at the second document → cutoff
	// is the mid timestamp (first row where running > ceiling).
	if cutoff != "2026-06-01T11:00:00.000000000Z" && cutoff != "2026-06-01T11:00:00Z" {
		// Accept either normalized form.
		if !strings.HasPrefix(cutoff, "2026-06-01T11:00:00") {
			t.Fatalf("cutoff=%q, want mid document timestamp under byte-accurate accounting", cutoff)
		}
	}
	// Character-count path would see 300+300=600 < 1500 and only cut at the
	// third document (900 chars). Prove the difference:
	var charCutoff string
	charErr := db.QueryRowContext(ctx, `
SELECT created_at_norm FROM (
  SELECT e.created_at_norm AS created_at_norm,
         SUM(CASE WHEN e.body_availability='available'
                  THEN COALESCE(e.body_plaintext_bytes,e.body_stored_bytes,length(e.body),0)
                  ELSE 0 END) OVER (ORDER BY e.created_at_norm DESC, e.id DESC) AS running
    FROM events e
) WHERE running > ? ORDER BY created_at_norm DESC LIMIT 1`, int64(1500)).Scan(&charCutoff)
	if charErr == sql.ErrNoRows {
		// Character path fits all three — distinct from byte path.
		return
	}
	if charErr != nil {
		t.Fatal(charErr)
	}
	if charCutoff == cutoff {
		t.Fatalf("byte cutoff and character cutoff both %q; CAST fallback not differentiating", cutoff)
	}
}

// TestSearchProjectionReserveAtBudgetYieldsZeroCeilingNotFailure pins that a
// non-recent reserve at or above the budget yields SourceCeiling 0 and does
// not hard-fail Start (index_family_exhausted is gone — it deadlocked).
func TestSearchProjectionReserveAtBudgetYieldsZeroCeilingNotFailure(t *testing.T) {
	store, db := newCapacityTestStore(t, []struct{ id, body, created string }{
		{"e1", "body for zero ceiling", "2026-06-01T12:00:00Z"},
	})
	now := time.Date(2026, 6, 2, 0, 0, 0, 0, time.UTC)
	driveToCompletion(t, store, capacityBudget(64<<20), now)
	ctx := context.Background()
	// Seed lastNonRecent above a 1-byte budget and clear the recent sample so
	// derivation falls back to the persisted non-recent figure.
	if _, err := db.Exec(`
		DELETE FROM search_projection_recent_documents;
		UPDATE search_projection_state
		   SET state='idle',generation_id=NULL,active_generation_id=NULL,
		       non_recent_family_bytes=1000000
		 WHERE singleton=1`); err != nil {
		t.Fatal(err)
	}
	b := capacityBudget(1)
	if _, err := store.Start(ctx, b, now); err != nil {
		t.Fatalf("Start failed: %v; want success with zero ceiling (no index_family_exhausted)", err)
	}
	var ceiling int64
	var evidenceStatus, evidenceReason string
	if err := db.QueryRow(`SELECT recent_source_ceiling_bytes,capacity_evidence_status,capacity_evidence_reason FROM search_projection_state`).Scan(&ceiling, &evidenceStatus, &evidenceReason); err != nil {
		t.Fatal(err)
	}
	if ceiling != 0 {
		t.Fatalf("recent_source_ceiling_bytes=%d, want 0 when reserve >= budget", ceiling)
	}
	if evidenceStatus != searchProjectionEvidenceUnavailable {
		t.Fatalf("capacity_evidence_status=%q, want unavailable", evidenceStatus)
	}
	if !strings.Contains(evidenceReason, "non-recent reserve") {
		t.Fatalf("capacity_evidence_reason=%q, want non-recent reserve reason", evidenceReason)
	}
}

// TestSearchProjectionZeroCeilingBuildsNothingRatherThanEvictingEverything
// pins the prefilter side of a zero ceiling. Emptying the recent tier via
// eviction is the right end state but the wrong route: an empty cutoff means
// "everything qualifies", so the source phase would build the whole age window
// into the trigram index at full FTS5 cost and then delete every row of it —
// on exactly the stores already at their budget. Nothing must be inserted.
func TestSearchProjectionZeroCeilingBuildsNothingRatherThanEvictingEverything(t *testing.T) {
	store, db := newCapacityTestStore(t, []struct{ id, body, created string }{
		{"e1", strings.Repeat("would be indexed ", 200), "2026-06-01T10:00:00Z"},
		{"e2", strings.Repeat("would be indexed ", 200), "2026-06-01T11:00:00Z"},
		{"e3", strings.Repeat("would be indexed ", 200), "2026-06-01T12:00:00Z"},
	})
	now := time.Date(2026, 6, 2, 0, 0, 0, 0, time.UTC)
	// Complete once so the session tier exists, then park at idle with a
	// persisted non-recent figure that exhausts a 1-byte budget.
	driveToCompletion(t, store, capacityBudget(64<<20), now)
	ctx := context.Background()
	if _, err := db.Exec(`
		DELETE FROM search_projection_recent_documents;
		UPDATE search_projection_state
		   SET state='idle',generation_id=NULL,active_generation_id=NULL,
		       non_recent_family_bytes=1000000
		 WHERE singleton=1`); err != nil {
		t.Fatal(err)
	}
	b := capacityBudget(1)
	b.Rows = 64
	if _, err := store.Start(ctx, b, now); err != nil {
		t.Fatal(err)
	}
	var cutoff string
	if err := db.QueryRow(`SELECT recent_cutoff_norm FROM search_projection_state`).Scan(&cutoff); err != nil {
		t.Fatal(err)
	}
	if cutoff != searchProjectionCutoffRetainNothing {
		t.Fatalf("recent_cutoff_norm=%q, want the retain-nothing sentinel at ceiling 0", cutoff)
	}
	// Watch every batch: a row inserted and later evicted is the failure this
	// pins, so a count taken only at the end would not see it.
	for i := 0; i < 200; i++ {
		p, err := resumeProjection(ctx, store, b, now)
		if err != nil {
			t.Fatal(err)
		}
		var inserted int
		if err = db.QueryRow(`SELECT COUNT(*) FROM search_projection_recent_documents`).Scan(&inserted); err != nil {
			t.Fatal(err)
		}
		if inserted != 0 {
			t.Fatalf("batch %d inserted %d recent rows under a zero ceiling; the whole age window is being built and then evicted", i, inserted)
		}
		var phase string
		_ = db.QueryRow(`SELECT phase FROM search_projection_state`).Scan(&phase)
		if phase != "source" || p.Completed {
			return
		}
	}
	t.Fatal("source phase did not finish")
}

// TestSearchProjectionZeroCeilingEmptiesRecentTier pins that a persisted
// ceiling of 0 empties the recent tier while leaving the session tier intact.
// Zero is not "age only": the eviction predicate is true for every row.
func TestSearchProjectionZeroCeilingEmptiesRecentTier(t *testing.T) {
	body := strings.Repeat("0123456789", 5) // 50 bytes
	store, db := newCapacityTestStore(t, []struct{ id, body, created string }{
		{"old", body, "2026-06-01T10:00:00Z"},
		{"new", body, "2026-06-01T12:00:00Z"},
	})
	now := time.Date(2026, 6, 1, 13, 0, 0, 0, time.UTC)
	b := capacityBudget(64 << 20)
	b.Rows = 10
	ctx := context.Background()
	if _, err := store.Start(ctx, b, now); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 40; i++ {
		// Keep ceiling at 0 through source→eviction re-derive so the contract
		// is about the eviction predicate, not Start-time derivation.
		if _, err := db.Exec(`UPDATE search_projection_state SET recent_source_ceiling_bytes=0 WHERE singleton=1`); err != nil {
			t.Fatal(err)
		}
		p, err := resumeProjection(ctx, store, b, now)
		if err != nil {
			t.Fatal(err)
		}
		if p.Completed {
			break
		}
	}
	var recentCount, summaryCount int
	if err := db.QueryRow(`SELECT COUNT(*) FROM search_projection_recent_documents`).Scan(&recentCount); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(`SELECT COUNT(*) FROM search_projection_session_summaries`).Scan(&summaryCount); err != nil {
		t.Fatal(err)
	}
	if recentCount != 0 {
		t.Fatalf("recent rows=%d, want 0 under ceiling 0 (empties whole recent tier)", recentCount)
	}
	if summaryCount == 0 {
		t.Fatal("session summaries empty; zero ceiling must not touch the session tier")
	}
}

// TestMulDivOverflowSafe pins the exact overflow-safe helper used by reserve
// and ceiling arithmetic. The wrapping case (4 GiB * 3 GiB) overflows int64
// when multiply-first; mulDiv must not.
func TestMulDivOverflowSafe(t *testing.T) {
	const gib int64 = 1 << 30
	tests := []struct {
		name    string
		a, b, c int64
		want    int64
	}{
		{name: "simple", a: 10, b: 20, c: 5, want: 40},
		{name: "floor division", a: 10, b: 3, c: 4, want: 7},
		{name: "zero numerator", a: 0, b: 99, c: 3, want: 0},
		{name: "zero divisor", a: 10, b: 10, c: 0, want: 0},
		{name: "negative inputs clamped", a: -4, b: 10, c: 2, want: 0},
		{name: "negative product factor clamped", a: 10, b: -3, c: 2, want: 0},
		// 4 GiB * 3 GiB overflows int64 (product ~1.38e19 > MaxInt64 ~9.22e18).
		// Divide by 4 GiB → expect 3 GiB.
		{name: "wraps int64 multiply-first (4GiB*3GiB/4GiB)", a: 4 * gib, b: 3 * gib, c: 4 * gib, want: 3 * gib},
		// Same product, divide by 1 → saturates (hi >= c when c=1 and product needs 2 limbs).
		{name: "saturates when quotient exceeds 64 bits", a: math.MaxInt64, b: math.MaxInt64, c: 1, want: math.MaxInt64},
		{name: "amplification-style ppm", a: 2_160_000, b: 1_000_000, c: 1_000_000, want: 2_160_000},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := mulDiv(tt.a, tt.b, tt.c)
			if diff := cmp.Diff(tt.want, got); diff != "" {
				t.Fatalf("mulDiv(%d,%d,%d) mismatch (-want +got):\n%s", tt.a, tt.b, tt.c, diff)
			}
			// The wrapping case must differ from the overflowing expression.
			if tt.name == "wraps int64 multiply-first (4GiB*3GiB/4GiB)" {
				//nolint:gosec // intentional overflow demonstration
				overflowed := tt.a * tt.b / tt.c
				if overflowed == got {
					t.Fatalf("overflowing expression accidentally equals mulDiv (%d); test is not discriminating", got)
				}
			}
		})
	}
}

// TestSearchProjectionReserveScopedToSurvivingGeneration pins that the
// non-recent reserve is shared-in-full plus scoped pages apportioned by
// logical share of the target generation — not the full dbstat non-recent
// figure (which holds both generations' scoped pages during a rebuild).
func TestSearchProjectionReserveScopedToSurvivingGeneration(t *testing.T) {
	store, db := newCapacityTestStore(t, []struct{ id, body, created string }{
		{"e1", "scoped reserve body", "2026-06-01T12:00:00Z"},
	})
	now := time.Date(2026, 6, 2, 0, 0, 0, 0, time.UTC)
	driveToCompletion(t, store, capacityBudget(64<<20), now)
	ctx := context.Background()
	var activeID string
	if err := db.QueryRow(`SELECT active_generation_id FROM search_projection_state`).Scan(&activeID); err != nil {
		t.Fatal(err)
	}
	// Plant a second generation with a large logical non-recent footprint so
	// unscoped physical would overstate the reserve for the active generation.
	const otherGen = "gen-other-large"
	huge := strings.Repeat("X", 50_000)
	if _, err := db.Exec(`INSERT INTO search_projection_session_summaries(generation_id,session_id,event_count,summary_text,projection_version,summary_version) VALUES(?,?,1,?,?,?)`,
		otherGen, "sess-other", huge, 1, 1); err != nil {
		t.Fatalf("seed other-gen summary: %v", err)
	}
	// Measure scoped vs unscoped logical shares.
	var logicalActive, logicalAll int64
	if err := db.QueryRowContext(ctx, selectSearchProjectionLogicalNonRecentBytesSQL, activeID).Scan(&logicalActive); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRowContext(ctx, selectSearchProjectionLogicalNonRecentBytesSQL, "").Scan(&logicalAll); err != nil {
		t.Fatal(err)
	}
	if logicalAll <= logicalActive {
		t.Fatalf("logicalAll=%d logicalActive=%d; other-gen seed did not inflate the family", logicalAll, logicalActive)
	}
	_, nonRecentScoped, nonRecentShared, evidence := store.measureSearchProjectionFamilySplit(ctx, db)
	if evidence.Status != searchProjectionEvidenceMeasured {
		t.Fatalf("split evidence=%+v", evidence)
	}
	if nonRecentScoped <= 0 {
		t.Fatalf("nonRecentScoped=%d, want > 0", nonRecentScoped)
	}
	if nonRecentShared <= 0 {
		t.Fatalf("nonRecentShared=%d, want > 0 (permanent state objects)", nonRecentShared)
	}
	got := store.scopedNonRecentReserve(ctx, db, nonRecentScoped, nonRecentShared, activeID)
	// Shared is never discounted; only scoped is apportioned via mulDiv.
	want := nonRecentShared + mulDiv(nonRecentScoped, logicalActive, logicalAll)
	if got != want {
		t.Fatalf("reserve=%d, want %d (shared=%d + mulDiv(scoped=%d, %d, %d))",
			got, want, nonRecentShared, nonRecentScoped, logicalActive, logicalAll)
	}
	// Apportioned reserve must be strictly less than full scoped+shared once
	// the other generation exists and owns a positive logical share.
	full := nonRecentShared + nonRecentScoped
	if logicalActive < logicalAll && got >= full {
		t.Fatalf("reserve %d == full non-recent %d despite logicalActive=%d < logicalAll=%d",
			got, full, logicalActive, logicalAll)
	}
}

// TestSearchProjectionOverBudgetCompletionReported pins
// index_family_within_budget=0 when the measured family exceeds the budget.
func TestSearchProjectionOverBudgetCompletionReported(t *testing.T) {
	store, db := newCapacityTestStore(t, []struct{ id, body, created string }{
		{"e1", strings.Repeat("indexed text content ", 100), "2026-06-01T12:00:00Z"},
	})
	now := time.Date(2026, 6, 2, 0, 0, 0, 0, time.UTC)
	// Generous budget so generation completes, then force a tiny budget figure
	// for the cutover comparison by rewriting the limit before completion
	// evidence is recorded — recordSearchProjectionCutoverEvidence reads the
	// column at measurement time. Complete first, then re-run evidence with a
	// lowered limit.
	b := capacityBudget(64 << 20)
	driveToCompletion(t, store, b, now)
	ctx := context.Background()
	if _, err := db.Exec(`UPDATE search_projection_state SET index_family_byte_limit=1 WHERE singleton=1`); err != nil {
		t.Fatal(err)
	}
	var generationID string
	if err := db.QueryRow(`SELECT generation_id FROM search_projection_state`).Scan(&generationID); err != nil {
		t.Fatal(err)
	}
	store.recordSearchProjectionCutoverEvidence(ctx, db, generationID, now)
	var within int
	if err := db.QueryRow(`SELECT index_family_within_budget FROM search_projection_state`).Scan(&within); err != nil {
		t.Fatal(err)
	}
	if within != 0 {
		t.Fatalf("index_family_within_budget=%d, want 0 (over budget)", within)
	}
	status, err := store.SearchProjectionStatus(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if status.IndexFamilyWithinBudget != 0 {
		t.Fatalf("status.IndexFamilyWithinBudget=%d, want 0", status.IndexFamilyWithinBudget)
	}
	if status.IndexFamilyByteLimit != 1 {
		t.Fatalf("status.IndexFamilyByteLimit=%d, want 1", status.IndexFamilyByteLimit)
	}
}

// TestSearchProjectionStatusReportsConfiguredUnit pins the JSON field rename.
func TestSearchProjectionStatusReportsConfiguredUnit(t *testing.T) {
	store, _ := newCapacityTestStore(t, []struct{ id, body, created string }{
		{"e1", "status unit body", "2026-06-01T12:00:00Z"},
	})
	now := time.Date(2026, 6, 2, 0, 0, 0, 0, time.UTC)
	driveToCompletion(t, store, capacityBudget(64<<20), now)
	status, err := store.SearchProjectionStatus(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if status.IndexFamilyByteLimit <= 0 {
		t.Fatalf("index_family_byte_limit=%d, want configured budget", status.IndexFamilyByteLimit)
	}
	if status.CapacitySemanticsVersion != apptypes.SearchProjectionCapacitySemanticsVersion {
		t.Fatalf("capacity_semantics_version=%d, want %d", status.CapacitySemanticsVersion, apptypes.SearchProjectionCapacitySemanticsVersion)
	}
	// recent_bytes remains the *other* unit (source text retained).
	if status.RecentBytes <= 0 {
		t.Fatalf("recent_bytes=%d, want retained source text > 0", status.RecentBytes)
	}
}

// TestSearchProjectionBudgetMeansIndexBytes pins that retained source text is
// approximately budget/amplification, not the budget itself, when the trigram
// index amplifies.
func TestSearchProjectionBudgetMeansIndexBytes(t *testing.T) {
	// Enough text that a completed generation has a measurable family.
	var events []struct{ id, body, created string }
	for i := 0; i < 8; i++ {
		events = append(events, struct{ id, body, created string }{
			id:      "doc-" + itoa(i),
			body:    strings.Repeat("trigram amplification corpus word ", 80),
			created: time.Date(2026, 6, 1, i, 0, 0, 0, time.UTC).Format(time.RFC3339Nano),
		})
	}
	store, db := newCapacityTestStore(t, events)
	now := time.Date(2026, 6, 10, 0, 0, 0, 0, time.UTC)
	const budget int64 = 256 << 10 // 256 KiB physical
	driveToCompletion(t, store, capacityBudget(budget), now)
	ctx := context.Background()
	status, err := store.SearchProjectionStatus(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var sourceBytes int64
	if err = db.QueryRow(`SELECT COALESCE(SUM(decoded_bytes),0) FROM search_projection_recent_documents`).Scan(&sourceBytes); err != nil {
		t.Fatal(err)
	}
	// Retained source must be well below the physical budget (amplification > 1).
	if sourceBytes > budget {
		t.Fatalf("retained source %d >= physical budget %d; budget still treated as source text", sourceBytes, budget)
	}
	if status.RecentSourceCeilingBytes <= 0 {
		t.Fatalf("recent_source_ceiling_bytes=%d, want derived positive ceiling", status.RecentSourceCeilingBytes)
	}
	if status.RecentSourceCeilingBytes > budget {
		t.Fatalf("source ceiling %d > physical budget %d", status.RecentSourceCeilingBytes, budget)
	}
}

// TestSearchProjectionObsoleteCompletedGenerationIsReplaced is the usecase-level
// behaviour for capacity_semantics_version < current on a complete generation.
func TestSearchProjectionObsoleteCompletedGenerationIsReplaced(t *testing.T) {
	store, db := newCapacityTestStore(t, []struct{ id, body, created string }{
		{"e1", "obsolete complete body", "2026-06-01T12:00:00Z"},
	})
	now := time.Date(2026, 6, 2, 0, 0, 0, 0, time.UTC)
	driveToCompletion(t, store, capacityBudget(64<<20), now)
	// Downgrade semantics version to simulate a pre-#1679 generation.
	if _, err := db.Exec(`UPDATE search_projection_state SET capacity_semantics_version=1 WHERE singleton=1`); err != nil {
		t.Fatal(err)
	}
	var beforeID string
	if err := db.QueryRow(`SELECT generation_id FROM search_projection_state`).Scan(&beforeID); err != nil {
		t.Fatal(err)
	}
	result, err := usecase.NewSearchProjectionUsecase(store).CatchUp(context.Background(), defaultSearchProjectionCatchUpBudget(), now)
	if err != nil {
		t.Fatal(err)
	}
	if result.Action != "start" {
		t.Fatalf("action=%q, want start (replace obsolete complete)", result.Action)
	}
	var afterID string
	var version int
	if err := db.QueryRow(`SELECT generation_id,capacity_semantics_version FROM search_projection_state`).Scan(&afterID, &version); err != nil {
		t.Fatal(err)
	}
	if afterID == beforeID {
		t.Fatalf("generation_id unchanged %q; obsolete complete was not replaced", beforeID)
	}
	if version != apptypes.SearchProjectionCapacitySemanticsVersion {
		t.Fatalf("capacity_semantics_version=%d, want %d", version, apptypes.SearchProjectionCapacitySemanticsVersion)
	}
}

// TestSearchProjectionObsoleteInFlightGenerationIsReplaced abandons a
// rebuilding v1 generation and starts a replacement.
func TestSearchProjectionObsoleteInFlightGenerationIsReplaced(t *testing.T) {
	store, db := newCapacityTestStore(t, []struct{ id, body, created string }{
		{"e1", "obsolete inflight body", "2026-06-01T12:00:00Z"},
	})
	now := time.Date(2026, 6, 2, 0, 0, 0, 0, time.UTC)
	b := capacityBudget(64 << 20)
	ctx := context.Background()
	gen, err := store.Start(ctx, b, now)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(`UPDATE search_projection_state SET capacity_semantics_version=1 WHERE singleton=1`); err != nil {
		t.Fatal(err)
	}
	result, err := usecase.NewSearchProjectionUsecase(store).CatchUp(ctx, defaultSearchProjectionCatchUpBudget(), now)
	if err != nil {
		t.Fatal(err)
	}
	if result.Action != "start" {
		t.Fatalf("action=%q, want start after abandon", result.Action)
	}
	var lifecycle string
	if err = db.QueryRow(`SELECT state FROM search_projection_generation_lifecycle WHERE generation_id=?`, gen.GenerationID).Scan(&lifecycle); err != nil {
		t.Fatal(err)
	}
	if lifecycle != "abandoned" {
		t.Fatalf("old lifecycle=%q, want abandoned", lifecycle)
	}
	var newID string
	if err = db.QueryRow(`SELECT generation_id FROM search_projection_state`).Scan(&newID); err != nil {
		t.Fatal(err)
	}
	if newID == gen.GenerationID {
		t.Fatalf("still on %q after obsolete in-flight replace", newID)
	}
}

// TestSearchProjectionObsoleteReplaceRecoversAbandonedCorpse is the #1752
// crash injection: Abandon without Start is the two-step failure between
// those calls. The next CatchUp must replace the parked corpse without an
// operator start. Mutating CatchUp back to Abandon+Start without the
// failed+abandoned+obsolete gate makes this red.
func TestSearchProjectionObsoleteReplaceRecoversAbandonedCorpse(t *testing.T) {
	store, db := newCapacityTestStore(t, []struct{ id, body, created string }{
		{"e1", "obsolete crash body", "2026-06-01T12:00:00Z"},
	})
	now := time.Date(2026, 6, 2, 0, 0, 0, 0, time.UTC)
	ctx := context.Background()
	gen, err := store.Start(ctx, capacityBudget(64<<20), now)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(`UPDATE search_projection_state SET capacity_semantics_version=1 WHERE singleton=1`); err != nil {
		t.Fatal(err)
	}
	if _, err = store.AbandonSearchProjection(ctx, now); err != nil {
		t.Fatalf("Abandon (injected crash after abandon): %v", err)
	}
	var parkedState, failureClass string
	if err = db.QueryRow(`SELECT state,failure_class FROM search_projection_state WHERE singleton=1`).Scan(&parkedState, &failureClass); err != nil {
		t.Fatal(err)
	}
	if parkedState != "failed" || failureClass != "abandoned" {
		t.Fatalf("injected corpse state=%q class=%q, want failed/abandoned", parkedState, failureClass)
	}

	result, err := usecase.NewSearchProjectionUsecase(store).CatchUp(ctx, defaultSearchProjectionCatchUpBudget(), now)
	if err != nil {
		t.Fatal(err)
	}
	if result.Action != "start" {
		t.Fatalf("action=%q reason=%q, want start (recovered corpse)", result.Action, result.SkippedReason)
	}
	var afterID string
	var version int
	if err = db.QueryRow(`SELECT generation_id,capacity_semantics_version FROM search_projection_state`).Scan(&afterID, &version); err != nil {
		t.Fatal(err)
	}
	if afterID == "" || afterID == gen.GenerationID {
		t.Fatalf("generation_id=%q, want a replacement of %q", afterID, gen.GenerationID)
	}
	if version != apptypes.SearchProjectionCapacitySemanticsVersion {
		t.Fatalf("capacity_semantics_version=%d, want %d", version, apptypes.SearchProjectionCapacitySemanticsVersion)
	}
}

// TestSearchProjectionObsoleteReplaceConcurrentCatchUpKeepsOneGeneration pins
// that two CatchUp calls against the same obsolete generation produce one
// replacement. The loser observes the winner instead of abandoning it.
func TestSearchProjectionObsoleteReplaceConcurrentCatchUpKeepsOneGeneration(t *testing.T) {
	store, db := newCapacityTestStore(t, []struct{ id, body, created string }{
		{"e1", "obsolete race body", "2026-06-01T12:00:00Z"},
	})
	now := time.Date(2026, 6, 2, 0, 0, 0, 0, time.UTC)
	ctx := context.Background()
	old, err := store.Start(ctx, capacityBudget(64<<20), now)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(`UPDATE search_projection_state SET capacity_semantics_version=1 WHERE singleton=1`); err != nil {
		t.Fatal(err)
	}

	budget := defaultSearchProjectionCatchUpBudget()
	budget.LockTime = 5 * time.Second
	results := make([]apptypes.SearchProjectionCatchUpResult, 2)
	errs := make([]error, 2)
	var wg sync.WaitGroup
	wg.Add(2)
	for i := 0; i < 2; i++ {
		i := i
		go func() {
			defer wg.Done()
			results[i], errs[i] = catchUpRetryingBusy(ctx, store, budget, now)
		}()
	}
	wg.Wait()
	for i, catchErr := range errs {
		var drift *apptypes.SearchProjectionDriftError
		if catchErr != nil && !errors.As(catchErr, &drift) {
			t.Fatalf("CatchUp[%d]: %v", i, catchErr)
		}
	}

	var liveID string
	if err = db.QueryRow(`SELECT generation_id FROM search_projection_state`).Scan(&liveID); err != nil {
		t.Fatal(err)
	}
	if liveID == "" || liveID == old.GenerationID {
		t.Fatalf("live generation %q still the obsolete %q", liveID, old.GenerationID)
	}
	var newCount int
	if err = db.QueryRow(`SELECT COUNT(*) FROM search_projection_generation_lifecycle WHERE generation_id<>?`, old.GenerationID).Scan(&newCount); err != nil {
		t.Fatal(err)
	}
	if newCount != 1 {
		t.Fatalf("replacement lifecycle rows=%d, want 1 (loser must not start a second generation)", newCount)
	}
	var liveLifecycle string
	if err = db.QueryRow(`SELECT state FROM search_projection_generation_lifecycle WHERE generation_id=?`, liveID).Scan(&liveLifecycle); err != nil {
		t.Fatal(err)
	}
	if liveLifecycle == "abandoned" {
		t.Fatalf("winner generation %q was abandoned by the loser", liveID)
	}
	seen := map[string]struct{}{}
	for i, result := range results {
		switch result.Action {
		case "start":
			if result.GenerationID != liveID {
				t.Fatalf("CatchUp[%d] generation=%q, want live %q", i, result.GenerationID, liveID)
			}
			seen[result.GenerationID] = struct{}{}
		case "resume", "already_complete":
			// Loser retried after the winner committed the replacement.
		default:
			t.Fatalf("CatchUp[%d] action=%q, want start or observe-after-win", i, result.Action)
		}
	}
}

func catchUpRetryingBusy(ctx context.Context, store *Database, budget apptypes.SearchProjectionBudget, now time.Time) (apptypes.SearchProjectionCatchUpResult, error) {
	var last apptypes.SearchProjectionCatchUpResult
	var err error
	for attempt := 0; attempt < 20; attempt++ {
		last, err = usecase.NewSearchProjectionUsecase(store).CatchUp(ctx, budget, now)
		if err == nil {
			return last, nil
		}
		if !strings.Contains(err.Error(), "database is locked") {
			return last, fmt.Errorf("catch-up: %w", err)
		}
		time.Sleep(25 * time.Millisecond)
	}
	return last, fmt.Errorf("catch-up still locked: %w", err)
}

// TestSearchProjectionOperatorTuningNotHijacked pins that a version-matched
// rebuild with a different ConfigHash is still skipped.
func TestSearchProjectionOperatorTuningNotHijacked(t *testing.T) {
	store, _ := newCapacityTestStore(t, []struct{ id, body, created string }{
		{"e1", "operator tuning body", "2026-06-01T12:00:00Z"},
	})
	now := time.Date(2026, 6, 2, 0, 0, 0, 0, time.UTC)
	operator := capacityBudget(32 << 20) // deliberate non-default
	ctx := context.Background()
	if _, err := store.Start(ctx, operator, now); err != nil {
		t.Fatal(err)
	}
	result, err := usecase.NewSearchProjectionUsecase(store).CatchUp(ctx, defaultSearchProjectionCatchUpBudget(), now)
	if err != nil {
		t.Fatal(err)
	}
	if diff := cmp.Diff("skipped", result.Action); diff != "" {
		t.Fatalf("action (-want +got):\n%s", diff)
	}
	if !strings.Contains(result.SkippedReason, "budget does not match") {
		t.Fatalf("reason=%q, want budget mismatch", result.SkippedReason)
	}
}
