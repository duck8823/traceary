package sqlite_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"

	apptypes "github.com/duck8823/traceary/application/types"
	"github.com/duck8823/traceary/domain/types"
	"github.com/duck8823/traceary/infrastructure/sqlite"
)

// The discard allowlist is the only thing standing between an ordinary gc run
// and irreversible loss, so each of its clauses needs a case that fails when
// the clause is removed. TestDatasource_CollectGarbage_discardsOnlyFoldedEndedTranscripts
// covers kind, session existence and session end; these cover the three
// remaining ones — the cutoff, the availability state, and the fold range —
// plus the two ways a fold can look like coverage without being coverage.

var discardBoundaryCutoff = time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC)

func TestDatasource_CollectGarbage_discardsOnlyBodiesOlderThanTheCutoff(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name      string
		createdAt time.Time
		want      string
	}{
		{name: "well before the cutoff", createdAt: time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC), want: "unavailable_retention"},
		{name: "one nanosecond before the cutoff", createdAt: discardBoundaryCutoff.Add(-time.Nanosecond), want: "unavailable_retention"},
		{name: "exactly at the cutoff", createdAt: discardBoundaryCutoff, want: "available"},
		{name: "after the cutoff", createdAt: time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC), want: "available"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dbPath, events, store := prepareDiscardGCFixture(t)
			event := newGCEventFixture(t, "event-1", types.EventKindTranscript, "body", tc.createdAt)
			if err := events.Save(context.Background(), event); err != nil {
				t.Fatalf("Save() error = %v", err)
			}
			db := openRetentionDB(t, dbPath)
			defer func() { _ = db.Close() }()
			insertGCSession(t, db, "session-1", true)
			insertGCFold(t, db, "session-1", "event-1", "event-1")

			if _, err := store.CollectGarbage(context.Background(), discardBoundaryCutoff, apptypes.GarbageCollectionTargetEvents, false); err != nil {
				t.Fatalf("CollectGarbage() error = %v", err)
			}
			if diff := cmp.Diff(tc.want, gcEventAvailability(t, db, "event-1")); diff != "" {
				t.Fatalf("availability mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

// A body that is already discarded must not be counted again. Without the
// availability clause every later run would re-report the same rows as
// candidates, so --dry-run would keep promising space that no longer exists.
func TestDatasource_CollectGarbage_doesNotRecountAlreadyDiscardedBodies(t *testing.T) {
	t.Parallel()

	dbPath, events, store := prepareDiscardGCFixture(t)
	event := newGCEventFixture(t, "event-1", types.EventKindTranscript, "body", time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC))
	if err := events.Save(context.Background(), event); err != nil {
		t.Fatalf("Save() error = %v", err)
	}
	db := openRetentionDB(t, dbPath)
	defer func() { _ = db.Close() }()
	insertGCSession(t, db, "session-1", true)
	insertGCFold(t, db, "session-1", "event-1", "event-1")

	first, err := store.CollectGarbage(context.Background(), discardBoundaryCutoff, apptypes.GarbageCollectionTargetEvents, false)
	if err != nil {
		t.Fatalf("CollectGarbage() first error = %v", err)
	}
	if diff := cmp.Diff(1, first); diff != "" {
		t.Fatalf("first discard count mismatch (-want +got):\n%s", diff)
	}
	second, err := store.CollectGarbage(context.Background(), discardBoundaryCutoff, apptypes.GarbageCollectionTargetEvents, false)
	if err != nil {
		t.Fatalf("CollectGarbage() second error = %v", err)
	}
	if diff := cmp.Diff(0, second); diff != "" {
		t.Fatalf("second discard count mismatch (-want +got):\n%s", diff)
	}
}

// Coverage is a range, not a session-wide flag: a session can hold a fold that
// stops short of the event in question, and that event is still unsummarised.
func TestDatasource_CollectGarbage_requiresTheFoldRangeToReachTheEvent(t *testing.T) {
	t.Parallel()

	target := time.Date(2026, 6, 10, 0, 0, 0, 0, time.UTC)
	cases := []struct {
		name    string
		lowerAt time.Time
		upperAt time.Time
		want    string
	}{
		{name: "range ends before the event", lowerAt: time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC), upperAt: time.Date(2026, 6, 5, 0, 0, 0, 0, time.UTC), want: "available"},
		{name: "range starts after the event", lowerAt: time.Date(2026, 6, 15, 0, 0, 0, 0, time.UTC), upperAt: time.Date(2026, 6, 20, 0, 0, 0, 0, time.UTC), want: "available"},
		{name: "event sits inside the range", lowerAt: time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC), upperAt: time.Date(2026, 6, 20, 0, 0, 0, 0, time.UTC), want: "unavailable_retention"},
		{name: "event is the lower bound", lowerAt: target, upperAt: time.Date(2026, 6, 20, 0, 0, 0, 0, time.UTC), want: "unavailable_retention"},
		{name: "event is the upper bound", lowerAt: time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC), upperAt: target, want: "unavailable_retention"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dbPath, events, store := prepareDiscardGCFixture(t)
			db := openRetentionDB(t, dbPath)
			defer func() { _ = db.Close() }()
			insertGCSession(t, db, "session-1", true)

			lower, upper := "event-lower", "event-upper"
			saveDiscardBoundaryEvent(t, events, "event-1", target)
			if !tc.lowerAt.Equal(target) {
				saveDiscardBoundaryEvent(t, events, lower, tc.lowerAt)
			} else {
				lower = "event-1"
			}
			if !tc.upperAt.Equal(target) {
				saveDiscardBoundaryEvent(t, events, upper, tc.upperAt)
			} else {
				upper = "event-1"
			}
			insertGCFold(t, db, "session-1", lower, upper)

			if _, err := store.CollectGarbage(context.Background(), discardBoundaryCutoff, apptypes.GarbageCollectionTargetEvents, false); err != nil {
				t.Fatalf("CollectGarbage() error = %v", err)
			}
			if diff := cmp.Diff(tc.want, gcEventAvailability(t, db, "event-1")); diff != "" {
				t.Fatalf("availability mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

// Nothing in the schema requires a refinement's boundary ids to name events of
// the session that refinement belongs to. If they name another session's
// events, global event order can place an uncovered event between them.
func TestDatasource_CollectGarbage_ignoresFoldBoundariesFromAnotherSession(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name            string
		boundarySession string
		want            string
	}{
		{name: "boundaries belong to the folded session", boundarySession: "session-1", want: "unavailable_retention"},
		{name: "boundaries belong to another session", boundarySession: "session-2", want: "available"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dbPath, events, store := prepareDiscardGCFixture(t)
			db := openRetentionDB(t, dbPath)
			defer func() { _ = db.Close() }()
			insertGCSession(t, db, "session-1", true)
			insertGCSession(t, db, "session-2", true)

			saveDiscardBoundaryEvent(t, events, "event-1", time.Date(2026, 6, 10, 0, 0, 0, 0, time.UTC))
			saveDiscardBoundaryEvent(t, events, "event-lower", time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC))
			saveDiscardBoundaryEvent(t, events, "event-upper", time.Date(2026, 6, 20, 0, 0, 0, 0, time.UTC))
			execRetentionSQL(t, db, `UPDATE events SET session_id = ? WHERE id IN ('event-lower', 'event-upper')`, tc.boundarySession)
			insertGCFold(t, db, "session-1", "event-lower", "event-upper")

			if _, err := store.CollectGarbage(context.Background(), discardBoundaryCutoff, apptypes.GarbageCollectionTargetEvents, false); err != nil {
				t.Fatalf("CollectGarbage() error = %v", err)
			}
			if diff := cmp.Diff(tc.want, gcEventAvailability(t, db, "event-1")); diff != "" {
				t.Fatalf("availability mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

// ts_norm returns an unparseable timestamp unchanged so ordinary reads survive
// historical rows. Under that fallback "0" compares less than every real
// cutoff, so an event whose age is unknowable would look old enough to
// discard. Discard must fail closed on it instead — including when the
// undatable timestamp is the one a fold's boundary carries.
func TestDatasource_CollectGarbage_refusesUndatableTimestamps(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name      string
		undatable string
		// An undatable boundary disqualifies the fold, so nothing in the
		// session is discardable. An undatable event only disqualifies itself,
		// and its two datable neighbours are still discarded.
		wantDiscarded int
	}{
		{name: "the event itself", undatable: "event-1", wantDiscarded: 2},
		{name: "the fold lower bound", undatable: "event-lower", wantDiscarded: 0},
		{name: "the fold upper bound", undatable: "event-upper", wantDiscarded: 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dbPath, events, store := prepareDiscardGCFixture(t)
			db := openRetentionDB(t, dbPath)
			defer func() { _ = db.Close() }()
			insertGCSession(t, db, "session-1", true)

			saveDiscardBoundaryEvent(t, events, "event-1", time.Date(2026, 6, 10, 0, 0, 0, 0, time.UTC))
			saveDiscardBoundaryEvent(t, events, "event-lower", time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC))
			saveDiscardBoundaryEvent(t, events, "event-upper", time.Date(2026, 6, 20, 0, 0, 0, 0, time.UTC))
			insertGCFold(t, db, "session-1", "event-lower", "event-upper")
			execRetentionSQL(t, db, `UPDATE events SET created_at = '0' WHERE id = ?`, tc.undatable)

			got, err := store.CollectGarbage(context.Background(), discardBoundaryCutoff, apptypes.GarbageCollectionTargetEvents, false)
			if err != nil {
				t.Fatalf("CollectGarbage() error = %v", err)
			}
			if diff := cmp.Diff(tc.wantDiscarded, got); diff != "" {
				t.Fatalf("discard count mismatch (-want +got):\n%s", diff)
			}
			if diff := cmp.Diff("available", gcEventAvailability(t, db, "event-1")); diff != "" {
				t.Fatalf("availability mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

func saveDiscardBoundaryEvent(t *testing.T, events *sqlite.EventDatasource, id string, createdAt time.Time) {
	t.Helper()
	event := newGCEventFixture(t, id, types.EventKindTranscript, "body", createdAt)
	if err := events.Save(context.Background(), event); err != nil {
		t.Fatalf("Save(%s) error = %v", id, err)
	}
}
