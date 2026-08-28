package sqlite

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"

	apptypes "github.com/duck8823/traceary/application/types"
	"github.com/duck8823/traceary/domain/model"
	"github.com/duck8823/traceary/domain/types"
)

// Tests for #1685 D6: SQL that interprets body content or plaintext size must
// stay correct after store payload-backfill encodes bodies as zstd BLOBs.
// R1/R2 must fail against a compressed corpus before the SQL is repaired.
// D1/D2 pin behaviour the deleted SQL was not contributing.

func TestSourceHookLegacyList_MatchesCompressedCorpus(t *testing.T) {
	t.Parallel()

	t.Run("legacy subagent_stop still matches after backfill", func(t *testing.T) {
		t.Parallel()
		ctx := context.Background()
		db, path := openPayloadEncodeFixture(t)
		defer closePayloadEncodeFixture(t, db)
		events := NewEventDatasource(NewDatabase(path, preparedMigrations(t)))

		const id = "legacy-subagent"
		body := "[phase:subagent] " + compressibleBody("subagent-stop")
		insertPlaintextEvent(t, db, eventSeed{
			ID: id, Kind: "session_ended", Body: body,
		})
		assertLegacySourceHook(t, db, id, "subagent_stop")
		encodeAllPayloadLanes(ctx, t, db)
		assertBodyCodec(t, db, id, payloadCodecZstd)

		got, err := events.ListRecent(
			ctx, 10, 0,
			types.EventKind(""), types.Client(""), types.Agent(""), types.SessionID(""), types.Workspace(""),
			false, time.Time{}, time.Time{},
			"subagent_stop",
		)
		if err != nil {
			t.Fatalf("ListRecent(subagent_stop): %v", err)
		}
		if diff := cmp.Diff([]string{id}, listedEventIDs(got)); diff != "" {
			t.Fatalf("legacy subagent_stop IDs mismatch (-want +got):\n%s", diff)
		}
	})

	t.Run("legacy pre_compact still matches after backfill", func(t *testing.T) {
		t.Parallel()
		ctx := context.Background()
		db, path := openPayloadEncodeFixture(t)
		defer closePayloadEncodeFixture(t, db)
		events := NewEventDatasource(NewDatabase(path, preparedMigrations(t)))

		const id = "legacy-precompact"
		body := "[phase:pre-compact] " + compressibleBody("pre-compact")
		insertPlaintextEvent(t, db, eventSeed{
			ID: id, Kind: "compact_summary", Body: body,
		})
		assertLegacySourceHook(t, db, id, "pre_compact")
		encodeAllPayloadLanes(ctx, t, db)
		assertBodyCodec(t, db, id, payloadCodecZstd)

		got, err := events.ListRecent(
			ctx, 10, 0,
			types.EventKind(""), types.Client(""), types.Agent(""), types.SessionID(""), types.Workspace(""),
			false, time.Time{}, time.Time{},
			"pre_compact",
		)
		if err != nil {
			t.Fatalf("ListRecent(pre_compact): %v", err)
		}
		if diff := cmp.Diff([]string{id}, listedEventIDs(got)); diff != "" {
			t.Fatalf("legacy pre_compact IDs mismatch (-want +got):\n%s", diff)
		}
	})

	t.Run("incompressible legacy hook survives the identity rewrite", func(t *testing.T) {
		t.Parallel()
		ctx := context.Background()
		db, path := openPayloadEncodeFixture(t)
		defer closePayloadEncodeFixture(t, db)
		events := NewEventDatasource(NewDatabase(path, preparedMigrations(t)))

		// zstd expands a short unique body, so the recipe keeps identity — but
		// it still rewrites the row to stamp the five codec columns. If that
		// rewrite changes the column affinity from TEXT to BLOB, migration
		// 053 re-derives legacy_source_hook (identity is a derivable codec)
		// and every LIKE misses, dropping the event from --source-hook.
		const id = "legacy-incompressible"
		insertPlaintextEvent(t, db, eventSeed{
			ID: id, Kind: "session_ended", Body: "[phase:subagent] short",
		})
		assertLegacySourceHook(t, db, id, "subagent_stop")
		encodeAllPayloadLanes(ctx, t, db)
		assertBodyCodec(t, db, id, payloadCodecIdentity)
		assertLegacySourceHook(t, db, id, "subagent_stop")

		got, err := events.ListRecent(
			ctx, 10, 0,
			types.EventKind(""), types.Client(""), types.Agent(""), types.SessionID(""), types.Workspace(""),
			false, time.Time{}, time.Time{},
			"subagent_stop",
		)
		if err != nil {
			t.Fatalf("ListRecent(subagent_stop): %v", err)
		}
		if diff := cmp.Diff([]string{id}, listedEventIDs(got)); diff != "" {
			t.Fatalf("incompressible legacy subagent_stop IDs mismatch (-want +got):\n%s", diff)
		}
	})

	t.Run("body-bearing and metadata paths agree on mixed corpus", func(t *testing.T) {
		t.Parallel()
		ctx := context.Background()
		db, path := openPayloadEncodeFixture(t)
		defer closePayloadEncodeFixture(t, db)
		events := NewEventDatasource(NewDatabase(path, preparedMigrations(t)))

		// Stamped primary row (source_hook set) stays on the primary branch.
		insertPlaintextEvent(t, db, eventSeed{
			ID: "hook-stamped", Kind: "session_ended",
			Body: compressibleBody("stamped"), SourceHook: "subagent_stop",
		})
		// Legacy rows that backfill encodes to zstd.
		insertPlaintextEvent(t, db, eventSeed{
			ID: "legacy-zstd", Kind: "session_ended",
			Body: "[phase:subagent] " + compressibleBody("legacy-zstd"),
		})
		insertPlaintextEvent(t, db, eventSeed{
			ID: "legacy-plain", Kind: "session_ended",
			Body: "[phase:subagent] " + compressibleBody("legacy-plain"),
		})
		// Unrelated kind/hook must not appear.
		insertPlaintextEvent(t, db, eventSeed{
			ID: "other-stop", Kind: "note",
			Body: compressibleBody("other"), SourceHook: "stop",
		})

		encodeAllPayloadLanes(ctx, t, db)
		assertBodyCodec(t, db, "legacy-zstd", payloadCodecZstd)
		assertBodyCodec(t, db, "legacy-plain", payloadCodecZstd)

		// Re-insert a plaintext legacy row after the backfill so the corpus is
		// mixed: some zstd, some still all-NULL codec metadata.
		insertPlaintextEvent(t, db, eventSeed{
			ID: "legacy-still-plain", Kind: "session_ended",
			Body: "[phase:subagent] still plaintext phase marker",
		})
		var stillCodec sql.NullString
		if err := db.QueryRow(`SELECT body_codec FROM events WHERE id = ?`, "legacy-still-plain").Scan(&stillCodec); err != nil {
			t.Fatalf("read still-plain codec: %v", err)
		}
		if stillCodec.Valid {
			t.Fatalf("legacy-still-plain must remain plaintext (codec NULL), got %q", stillCodec.String)
		}

		bodyIDs, err := events.ListRecent(
			ctx, 20, 0,
			types.EventKind(""), types.Client(""), types.Agent(""), types.SessionID(""), types.Workspace(""),
			false, time.Time{}, time.Time{},
			"subagent_stop",
		)
		if err != nil {
			t.Fatalf("ListRecent(subagent_stop): %v", err)
		}
		meta, err := events.ListRecentMetadata(
			ctx,
			apptypes.NewEventListCriteriaBuilder(20).SourceHook("subagent_stop").Build(),
		)
		if err != nil {
			t.Fatalf("ListRecentMetadata(subagent_stop): %v", err)
		}
		if diff := cmp.Diff(listedEventIDs(bodyIDs), metadataEventIDs(meta)); diff != "" {
			t.Fatalf("body vs metadata ID sequences disagree (-body +metadata):\n%s", diff)
		}
		// Same created_at → order by id DESC.
		want := []string{"legacy-zstd", "legacy-still-plain", "legacy-plain", "hook-stamped"}
		if diff := cmp.Diff(want, listedEventIDs(bodyIDs)); diff != "" {
			t.Fatalf("subagent_stop ID set mismatch (-want +got):\n%s", diff)
		}
	})

	t.Run("session_ended without phase marker is not returned", func(t *testing.T) {
		t.Parallel()
		ctx := context.Background()
		db, path := openPayloadEncodeFixture(t)
		defer closePayloadEncodeFixture(t, db)
		events := NewEventDatasource(NewDatabase(path, preparedMigrations(t)))

		const id = "no-phase"
		insertPlaintextEvent(t, db, eventSeed{
			ID: id, Kind: "session_ended",
			Body: compressibleBody("no-phase-marker"),
		})
		assertLegacySourceHook(t, db, id, "")
		encodeAllPayloadLanes(ctx, t, db)
		assertBodyCodec(t, db, id, payloadCodecZstd)

		got, err := events.ListRecent(
			ctx, 10, 0,
			types.EventKind(""), types.Client(""), types.Agent(""), types.SessionID(""), types.Workspace(""),
			false, time.Time{}, time.Time{},
			"subagent_stop",
		)
		if err != nil {
			t.Fatalf("ListRecent(subagent_stop): %v", err)
		}
		if len(got) != 0 {
			t.Fatalf("false positive: got %v, want empty", listedEventIDs(got))
		}
	})
}

func TestSessionBodyBytes_PressureIsLogicalAfterBackfill(t *testing.T) {
	t.Parallel()

	t.Run("whole-session pressure is unchanged by compression", func(t *testing.T) {
		t.Parallel()
		ctx := context.Background()
		db, path := openPayloadEncodeFixture(t)
		defer closePayloadEncodeFixture(t, db)
		events := NewEventDatasource(NewDatabase(path, preparedMigrations(t)))

		bodies := []struct {
			id   string
			body string
		}{
			{"pressure-a", compressibleBody("pressure-a")},
			{"pressure-b", compressibleBody("pressure-b")},
			{"pressure-c", compressibleBody("pressure-c")},
		}
		var want int64
		for _, seed := range bodies {
			insertPlaintextEvent(t, db, eventSeed{
				ID: seed.id, Kind: "note", Body: seed.body,
			})
			want += int64(len(seed.body))
		}

		before, err := events.SumBodyBytesAfter(ctx, types.SessionID("session-codec"), types.None[types.EventID]())
		if err != nil {
			t.Fatalf("SumBodyBytesAfter before: %v", err)
		}
		if diff := cmp.Diff(want, before); diff != "" {
			t.Fatalf("pre-backfill pressure mismatch (-want +got):\n%s", diff)
		}

		encodeAllPayloadLanes(ctx, t, db)
		for _, seed := range bodies {
			assertBodyCodec(t, db, seed.id, payloadCodecZstd)
		}

		after, err := events.SumBodyBytesAfter(ctx, types.SessionID("session-codec"), types.None[types.EventID]())
		if err != nil {
			t.Fatalf("SumBodyBytesAfter after: %v", err)
		}
		if diff := cmp.Diff(before, after); diff != "" {
			t.Fatalf("pressure changed across backfill (-before +after):\n%s", diff)
		}
	})

	t.Run("pressure after covers_to boundary is unchanged by compression", func(t *testing.T) {
		t.Parallel()
		ctx := context.Background()
		db, path := openPayloadEncodeFixture(t)
		defer closePayloadEncodeFixture(t, db)
		events := NewEventDatasource(NewDatabase(path, preparedMigrations(t)))

		// Distinct created_at so the covers_to boundary is meaningful under
		// (ts_norm(created_at), id) order.
		seeds := []struct {
			id   string
			at   string
			body string
		}{
			{"bound-a", "2026-08-09T00:00:00Z", compressibleBody("bound-a")},
			{"bound-b", "2026-08-09T00:00:01Z", compressibleBody("bound-b")},
			{"bound-c", "2026-08-09T00:00:02Z", compressibleBody("bound-c")},
		}
		for _, seed := range seeds {
			insertPlaintextEventAt(t, db, eventSeed{
				ID: seed.id, Kind: "note", Body: seed.body,
			}, seed.at)
		}
		wantAfterA := int64(len(seeds[1].body) + len(seeds[2].body))

		before, err := events.SumBodyBytesAfter(
			ctx,
			types.SessionID("session-codec"),
			types.Some(types.EventID("bound-a")),
		)
		if err != nil {
			t.Fatalf("SumBodyBytesAfter before: %v", err)
		}
		if diff := cmp.Diff(wantAfterA, before); diff != "" {
			t.Fatalf("pre-backfill after-boundary pressure mismatch (-want +got):\n%s", diff)
		}

		encodeAllPayloadLanes(ctx, t, db)
		for _, seed := range seeds {
			assertBodyCodec(t, db, seed.id, payloadCodecZstd)
		}

		after, err := events.SumBodyBytesAfter(
			ctx,
			types.SessionID("session-codec"),
			types.Some(types.EventID("bound-a")),
		)
		if err != nil {
			t.Fatalf("SumBodyBytesAfter after: %v", err)
		}
		if diff := cmp.Diff(before, after); diff != "" {
			t.Fatalf("after-boundary pressure changed across backfill (-before +after):\n%s", diff)
		}
	})
}

func TestBoundedHydration_PinAfterBodySQLDeletion(t *testing.T) {
	t.Parallel()
	// Pin: D1 deletes SQL json/substr derivation; Go already owns visible text.
	// This test is expected to pass both before and after the SQL deletion.

	t.Run("canonical and plain bodies hydrate after backfill", func(t *testing.T) {
		t.Parallel()
		ctx := context.Background()
		db, path := openPayloadEncodeFixture(t)
		defer closePayloadEncodeFixture(t, db)
		events := NewEventDatasource(NewDatabase(path, preparedMigrations(t)))

		plainBody := compressibleBody("plain-bounded")
		canonicalBody, err := apptypes.MarshalEventBodyBlocks([]apptypes.EventBodyBlock{
			{Type: apptypes.EventBodyBlockTypeThinking, Text: "hidden reasoning " + compressibleBody("think")},
			{Type: apptypes.EventBodyBlockTypeText, Text: "visible one"},
			{Type: apptypes.EventBodyBlockTypeText, Text: "visible two " + compressibleBody("text2")},
		})
		if err != nil {
			t.Fatalf("MarshalEventBodyBlocks: %v", err)
		}
		insertPlaintextEvent(t, db, eventSeed{
			ID: "bounded-plain", Kind: "note", Body: plainBody,
		})
		insertPlaintextEvent(t, db, eventSeed{
			ID: "bounded-canonical", Kind: "transcript", Body: canonicalBody,
		})

		encodeAllPayloadLanes(ctx, t, db)
		assertBodyCodec(t, db, "bounded-plain", payloadCodecZstd)
		assertBodyCodec(t, db, "bounded-canonical", payloadCodecZstd)

		wantPlain, wantPlainCanon := visibleEventBody(plainBody, types.BodyAvailabilityAvailable)
		wantCanon, wantCanonFlag := visibleEventBody(canonicalBody, types.BodyAvailabilityAvailable)
		if !wantCanonFlag {
			t.Fatalf("fixture body must be a canonical envelope")
		}

		const runeLimit = 1 << 20
		got, err := events.ListRecentBounded(
			ctx,
			apptypes.NewEventListCriteriaBuilder(10).Build(),
			runeLimit,
		)
		if err != nil {
			t.Fatalf("ListRecentBounded: %v", err)
		}
		byID := boundedByID(got)
		assertBoundedBody(t, byID, "bounded-plain", wantPlain, len([]rune(wantPlain)), wantPlainCanon)
		assertBoundedBody(t, byID, "bounded-canonical", wantCanon, len([]rune(wantCanon)), true)

		meta := make([]apptypes.EventMetadata, 0, len(got))
		for _, event := range got {
			meta = append(meta, event.Metadata())
		}
		hydrated, err := events.HydrateBounded(ctx, meta, runeLimit)
		if err != nil {
			t.Fatalf("HydrateBounded: %v", err)
		}
		if diff := cmp.Diff(boundedSummaries(got), boundedSummaries(hydrated)); diff != "" {
			t.Fatalf("HydrateBounded vs ListRecentBounded (-list +hydrate):\n%s", diff)
		}
	})

	t.Run("rune limit truncates prefix and reports full visible count", func(t *testing.T) {
		t.Parallel()
		ctx := context.Background()
		db, path := openPayloadEncodeFixture(t)
		defer closePayloadEncodeFixture(t, db)
		events := NewEventDatasource(NewDatabase(path, preparedMigrations(t)))

		body := compressibleBody("truncate-me")
		insertPlaintextEvent(t, db, eventSeed{
			ID: "bounded-trunc", Kind: "note", Body: body,
		})
		encodeAllPayloadLanes(ctx, t, db)
		assertBodyCodec(t, db, "bounded-trunc", payloadCodecZstd)

		const runeLimit = 12
		got, err := events.ListRecentBounded(
			ctx,
			apptypes.NewEventListCriteriaBuilder(5).Build(),
			runeLimit,
		)
		if err != nil {
			t.Fatalf("ListRecentBounded: %v", err)
		}
		byID := boundedByID(got)
		event, ok := byID["bounded-trunc"]
		if !ok {
			t.Fatalf("bounded-trunc missing from results")
		}
		wantPrefix := string([]rune(body)[:runeLimit])
		if diff := cmp.Diff(wantPrefix, event.Body()); diff != "" {
			t.Fatalf("truncated body mismatch (-want +got):\n%s", diff)
		}
		if event.VisibleBodyRunes() != len([]rune(body)) {
			t.Fatalf("VisibleBodyRunes = %d, want %d", event.VisibleBodyRunes(), len([]rune(body)))
		}
		if !event.BodyResponseTruncated() {
			t.Fatalf("BodyResponseTruncated = false, want true")
		}
	})
}

func TestLoadCanonicalBodies_PinAfterBodyColumnDrop(t *testing.T) {
	t.Parallel()
	// Pin: D2 drops e.body from the SQL projection; loadEventPlaintext owns the
	// bytes. Expected to pass both before and after the column drop.

	ctx := context.Background()
	db, path := openPayloadEncodeFixture(t)
	defer closePayloadEncodeFixture(t, db)
	events := NewEventDatasource(NewDatabase(path, preparedMigrations(t)))

	canonicalBody, err := apptypes.MarshalEventBodyBlocks([]apptypes.EventBodyBlock{
		{Type: apptypes.EventBodyBlockTypeText, Text: "canonical " + compressibleBody("canon-load")},
	})
	if err != nil {
		t.Fatalf("MarshalEventBodyBlocks: %v", err)
	}
	plainBody := compressibleBody("plain-canon-load")
	insertPlaintextEvent(t, db, eventSeed{
		ID: "canon-load-1", Kind: "transcript", Body: canonicalBody,
	})
	insertPlaintextEvent(t, db, eventSeed{
		ID: "canon-load-2", Kind: "note", Body: plainBody,
	})
	encodeAllPayloadLanes(ctx, t, db)
	assertBodyCodec(t, db, "canon-load-1", payloadCodecZstd)
	assertBodyCodec(t, db, "canon-load-2", payloadCodecZstd)

	ids := []types.EventID{
		types.EventID("canon-load-1"),
		types.EventID("canon-load-2"),
	}
	got, err := events.LoadCanonicalBodies(ctx, ids)
	if err != nil {
		t.Fatalf("LoadCanonicalBodies: %v", err)
	}
	want := map[types.EventID]string{
		types.EventID("canon-load-1"): canonicalBody,
		types.EventID("canon-load-2"): plainBody,
	}
	if diff := cmp.Diff(want, got); diff != "" {
		t.Fatalf("LoadCanonicalBodies mismatch (-want +got):\n%s", diff)
	}
}

func TestTimelineSummary_BlankCandidateDoesNotStealTheSummary(t *testing.T) {
	t.Parallel()

	// The timeline ranked summary candidates with TRIM(body) != '' so a blank
	// prompt could not take rn = 1 away from the real one. That predicate reads
	// the stored bytes: it cannot see through an encoded body, and SQLite's
	// TRIM does not strip tabs or newlines. Either way the blank row took rn = 1
	// and the block silently lost its summary, because Go only ever saw the one
	// candidate SQL had already chosen.
	tests := []struct {
		name      string
		blankBody string
		wantCodec string
	}{
		{
			name:      "blank prompt compressed into a BLOB TRIM cannot read",
			blankBody: strings.Repeat(" ", 4096),
			wantCodec: payloadCodecZstd,
		},
		{
			name:      "blank prompt of tabs and newlines TRIM does not strip",
			blankBody: "\n\t\n\t",
			wantCodec: payloadCodecIdentity,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			ctx := context.Background()
			db, path := openPayloadEncodeFixture(t)
			defer closePayloadEncodeFixture(t, db)
			events := NewEventDatasource(NewDatabase(path, preparedMigrations(t)))

			insertPlaintextEventAt(t, db, eventSeed{
				ID: "timeline-blank", Kind: "prompt", Body: tt.blankBody,
			}, "2026-08-09T00:00:00Z")
			insertPlaintextEventAt(t, db, eventSeed{
				ID: "timeline-real", Kind: "prompt", Body: "the real first prompt",
			}, "2026-08-09T00:00:01Z")

			encodeAllPayloadLanes(ctx, t, db)
			assertBodyCodec(t, db, "timeline-blank", tt.wantCodec)

			blocks, err := events.ListTimelineBlocks(ctx, types.Workspace(""), time.Time{}, time.Time{}, 900, 10)
			if err != nil {
				t.Fatalf("ListTimelineBlocks: %v", err)
			}
			if len(blocks) != 1 || len(blocks[0].WorkspaceBreakdown()) != 1 {
				t.Fatalf("want one block with one workspace, got %d blocks", len(blocks))
			}
			got := blocks[0].WorkspaceBreakdown()[0]
			if diff := cmp.Diff("the real first prompt", got.Summary()); diff != "" {
				t.Fatalf("timeline summary mismatch (-want +got):\n%s", diff)
			}
			if diff := cmp.Diff(apptypes.TimelineSummarySourcePrompt, got.SummarySource()); diff != "" {
				t.Fatalf("timeline summary source mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

func TestTimelineSummary_WalksPastTheFormerCandidateCap(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	db, path := openPayloadEncodeFixture(t)
	defer closePayloadEncodeFixture(t, db)
	events := NewEventDatasource(NewDatabase(path, preparedMigrations(t)))

	// Depth 3 used to fall through to the next kind. Four compressed blanks
	// ahead of the real prompt is past that cap.
	for i := 0; i < 4; i++ {
		insertPlaintextEventAt(t, db, eventSeed{
			ID: fmt.Sprintf("timeline-blank-%d", i), Kind: "prompt", Body: strings.Repeat(" ", 4096),
		}, fmt.Sprintf("2026-08-09T00:00:0%dZ", i))
	}
	insertPlaintextEventAt(t, db, eventSeed{
		ID: "timeline-real-beyond-cap", Kind: "prompt", Body: "fourth blank then this",
	}, "2026-08-09T00:00:04Z")
	encodeAllPayloadLanes(ctx, t, db)
	for i := 0; i < 4; i++ {
		assertBodyCodec(t, db, fmt.Sprintf("timeline-blank-%d", i), payloadCodecZstd)
	}

	blocks, err := events.ListTimelineBlocks(ctx, types.Workspace(""), time.Time{}, time.Time{}, 900, 10)
	if err != nil {
		t.Fatalf("ListTimelineBlocks: %v", err)
	}
	if len(blocks) != 1 || len(blocks[0].WorkspaceBreakdown()) != 1 {
		t.Fatalf("want one block with one workspace, got %d blocks", len(blocks))
	}
	got := blocks[0].WorkspaceBreakdown()[0]
	if diff := cmp.Diff("fourth blank then this", got.Summary()); diff != "" {
		t.Fatalf("timeline summary mismatch (-want +got):\n%s", diff)
	}
	if diff := cmp.Diff(apptypes.TimelineSummarySourcePrompt, got.SummarySource()); diff != "" {
		t.Fatalf("timeline summary source mismatch (-want +got):\n%s", diff)
	}
}

func TestTimelineSummary_QueryCountPerRowDoesNotRegress(t *testing.T) {
	ctx := context.Background()
	db, path := openPayloadEncodeFixture(t)
	defer closePayloadEncodeFixture(t, db)
	events := NewEventDatasource(NewDatabase(path, preparedMigrations(t)))
	insertPlaintextEventAt(t, db, eventSeed{
		ID: "timeline-only-real", Kind: "prompt", Body: "the only prompt",
	}, "2026-08-09T00:00:00Z")
	encodeAllPayloadLanes(ctx, t, db)

	// Main hydrated one candidate as schema+body (2). The walk still decodes
	// that one body; the schema check is once per ListTimelineBlocks.
	counts := map[string]int{}
	events.SetTimelinePayloadQueryHookForTest(func(kind string) { counts[kind]++ })
	defer events.SetTimelinePayloadQueryHookForTest(nil)

	blocks, err := events.ListTimelineBlocks(ctx, types.Workspace(""), time.Time{}, time.Time{}, 900, 10)
	if err != nil {
		t.Fatalf("ListTimelineBlocks: %v", err)
	}
	if len(blocks) != 1 || len(blocks[0].WorkspaceBreakdown()) != 1 {
		t.Fatalf("want one block with one workspace, got %d blocks", len(blocks))
	}
	if diff := cmp.Diff("the only prompt", blocks[0].WorkspaceBreakdown()[0].Summary()); diff != "" {
		t.Fatalf("timeline summary mismatch (-want +got):\n%s", diff)
	}
	if diff := cmp.Diff(1, counts["schema"]); diff != "" {
		t.Fatalf("schema checks per list (-want +got):\n%s", diff)
	}
	if diff := cmp.Diff(1, counts["body"]); diff != "" {
		t.Fatalf("body reads per row (-want +got):\n%s", diff)
	}
}

func insertPlaintextEventAt(t *testing.T, db *sql.DB, seed eventSeed, createdAt string) {
	t.Helper()
	var sourceHook any
	if seed.SourceHook != "" {
		sourceHook = seed.SourceHook
	}
	if _, err := db.Exec(`
		INSERT INTO events(
			id, kind, client, agent, session_id, workspace, body, created_at, source_hook
		) VALUES (?, ?, 'cli', 'codex', 'session-codec', 'ws-codec', ?, ?, ?)
	`, seed.ID, seed.Kind, seed.Body, createdAt, sourceHook); err != nil {
		t.Fatalf("insert plaintext event %s at %s: %v", seed.ID, createdAt, err)
	}
}

func listedEventIDs(events []*model.Event) []string {
	ids := make([]string, 0, len(events))
	for _, event := range events {
		ids = append(ids, event.EventID().String())
	}
	return ids
}

func metadataEventIDs(metadata []apptypes.EventMetadata) []string {
	ids := make([]string, 0, len(metadata))
	for _, item := range metadata {
		ids = append(ids, item.EventID().String())
	}
	return ids
}

func boundedByID(events []apptypes.BoundedEvent) map[string]apptypes.BoundedEvent {
	out := make(map[string]apptypes.BoundedEvent, len(events))
	for _, event := range events {
		out[event.Metadata().EventID().String()] = event
	}
	return out
}

type boundedSummary struct {
	ID                string
	Body              string
	VisibleBodyRunes  int
	CanonicalEnvelope bool
}

func boundedSummaries(events []apptypes.BoundedEvent) []boundedSummary {
	out := make([]boundedSummary, 0, len(events))
	for _, event := range events {
		out = append(out, boundedSummary{
			ID:                event.Metadata().EventID().String(),
			Body:              event.Body(),
			VisibleBodyRunes:  event.VisibleBodyRunes(),
			CanonicalEnvelope: event.CanonicalEnvelope(),
		})
	}
	return out
}

func assertBoundedBody(
	t *testing.T,
	byID map[string]apptypes.BoundedEvent,
	id, wantBody string,
	wantRunes int,
	wantCanonical bool,
) {
	t.Helper()
	event, ok := byID[id]
	if !ok {
		t.Fatalf("event %s missing from bounded results", id)
	}
	if diff := cmp.Diff(wantBody, event.Body()); diff != "" {
		t.Fatalf("%s body mismatch (-want +got):\n%s", id, diff)
	}
	if event.VisibleBodyRunes() != wantRunes {
		t.Fatalf("%s VisibleBodyRunes = %d, want %d", id, event.VisibleBodyRunes(), wantRunes)
	}
	if event.CanonicalEnvelope() != wantCanonical {
		t.Fatalf("%s CanonicalEnvelope = %v, want %v", id, event.CanonicalEnvelope(), wantCanonical)
	}
}
