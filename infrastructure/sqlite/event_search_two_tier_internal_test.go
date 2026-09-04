package sqlite

import (
	"context"
	"database/sql"
	"path/filepath"
	"strings"
	"testing"
	"time"

	apptypes "github.com/duck8823/traceary/application/types"
	"github.com/duck8823/traceary/domain/types"
)

func TestBuildTwoTierQueries_DoNotReadProjectionOrFingerprintTables(t *testing.T) {
	t.Parallel()
	criteria := apptypes.NewEventSearchCriteriaBuilder(5).
		Query("keep-days").
		Workspace(types.Workspace("ws")).
		Build()
	phrase := apptypes.SearchPhraseOf("keep-days")
	refinementSQL, _ := buildTwoTierRefinementQuery(criteria, phrase)
	fallbackSQL, _ := buildTwoTierFallbackScanQuery(criteria)
	for _, tc := range []struct {
		name, sql string
	}{
		{"refinement", refinementSQL},
		{"fallback", fallbackSQL},
	} {
		t.Run(tc.name, func(t *testing.T) {
			lower := strings.ToLower(tc.sql)
			for _, banned := range []string{
				"search_projection_",
				"literal_search_fingerprints",
				"event_search_",
			} {
				if strings.Contains(lower, banned) {
					t.Fatalf("%s SQL mentions banned %q:\n%s", tc.name, banned, tc.sql)
				}
			}
		})
	}

	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "two-tier-plan.db")
	if err := NewStoreManagementDatasource(NewDatabase(path, preparedMigrations(t))).Initialize(ctx); err != nil {
		t.Fatalf("Initialize: %v", err)
	}
	db, err := sql.Open("sqlite", directSQLiteRWDSN(path))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })

	for _, tc := range []struct {
		name, sql string
		args      []any
	}{
		func() struct {
			name, sql string
			args      []any
		} {
			sqlText, args := buildTwoTierRefinementQuery(criteria, phrase)
			return struct {
				name, sql string
				args      []any
			}{"refinement", sqlText, args}
		}(),
		func() struct {
			name, sql string
			args      []any
		} {
			sqlText, args := buildTwoTierFallbackScanQuery(criteria)
			return struct {
				name, sql string
				args      []any
			}{"fallback", sqlText, args}
		}(),
	} {
		t.Run("explain "+tc.name, func(t *testing.T) {
			rows, err := db.Query("EXPLAIN QUERY PLAN "+tc.sql, tc.args...)
			if err != nil {
				t.Fatalf("EXPLAIN QUERY PLAN: %v\n%s", err, tc.sql)
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
			for _, banned := range []string{
				"search_projection_",
				"literal_search_fingerprints",
				"event_search_",
			} {
				if strings.Contains(joined, banned) {
					t.Fatalf("%s plan mentions banned %q:\n%s", tc.name, banned, joined)
				}
			}
		})
	}
}

func TestCompareTwoTierHits_TotalOrder(t *testing.T) {
	t.Parallel()
	whole := time.Date(2026, 8, 1, 10, 0, 0, 0, time.UTC)
	frac := time.Date(2026, 8, 1, 10, 0, 0, 500_000_000, time.UTC)

	t.Run("fractional later instant sorts before whole second", func(t *testing.T) {
		later := twoTierHit{resultTime: frac, eventID: "evt-frac"}
		earlier := twoTierHit{resultTime: whole, eventID: "evt-whole"}
		if compareTwoTierHits(later, earlier) >= 0 {
			t.Fatal("later fractional instant must precede the whole second")
		}
	})

	t.Run("refinement precedes fallback at the same instant", func(t *testing.T) {
		refinement := twoTierHit{
			resultTime: whole,
			tier:       apptypes.SearchHitTierRefinement,
			kind:       twoTierResultKindSession,
			sessionID:  "sess-z",
		}
		fallback := twoTierHit{
			resultTime: whole,
			tier:       apptypes.SearchHitTierFallback,
			kind:       twoTierResultKindEvent,
			sessionID:  "sess-a",
			eventID:    "evt-a",
		}
		if compareTwoTierHits(refinement, fallback) >= 0 {
			t.Fatal("refinement must precede fallback at the same instant")
		}
	})

	t.Run("event precedes session within a tier", func(t *testing.T) {
		eventHit := twoTierHit{
			resultTime: whole,
			tier:       apptypes.SearchHitTierFallback,
			kind:       twoTierResultKindEvent,
			sessionID:  "sess-z",
			eventID:    "evt-z",
		}
		sessionHit := twoTierHit{
			resultTime: whole,
			tier:       apptypes.SearchHitTierFallback,
			kind:       twoTierResultKindSession,
			sessionID:  "sess-a",
		}
		if compareTwoTierHits(eventHit, sessionHit) >= 0 {
			t.Fatal("event must precede session within a tier")
		}
	})

	t.Run("binary id order breaks remaining ties", func(t *testing.T) {
		a := twoTierHit{
			resultTime: whole,
			tier:       apptypes.SearchHitTierFallback,
			kind:       twoTierResultKindEvent,
			sessionID:  "sess-tie",
			eventID:    "evt-tie-a",
		}
		z := twoTierHit{
			resultTime: whole,
			tier:       apptypes.SearchHitTierFallback,
			kind:       twoTierResultKindEvent,
			sessionID:  "sess-tie",
			eventID:    "evt-tie-z",
		}
		if compareTwoTierHits(a, z) >= 0 {
			t.Fatal("binary event_id order must put evt-tie-a before evt-tie-z")
		}
	})
}
