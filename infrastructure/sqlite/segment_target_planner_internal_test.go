package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	apptypes "github.com/duck8823/traceary/application/types"
	"github.com/duck8823/traceary/domain"
)

func plannerPolicy(maxRows int) apptypes.SegmentTargetPolicy {
	return apptypes.SegmentTargetPolicy{CapturedAt: time.Date(2026, 8, 5, 12, 0, 0, 0, time.UTC), HotHorizon: 24 * time.Hour, MaxRows: maxRows, MaxCanonicalPlainBytes: 1 << 20, MaxDecodedBytes: 64 << 20, MaxStoredUpperBytes: 1 << 20, MaxFileBytes: 2 << 20, StoredBoundVersion: apptypes.SegmentStoredBoundV1}
}

func TestSegmentTargetSerializesTwoConnectionRacesAfterSnapshot(t *testing.T) {
	tests := []struct {
		name       string
		compete    func(context.Context, *Database, *sql.DB, apptypes.CatalogTargetPlanRequest) error
		wantWriter bool
	}{
		{name: "backdated event waits then remains hot", wantWriter: true, compete: func(_ context.Context, _ *Database, raw *sql.DB, _ apptypes.CatalogTargetPlanRequest) error {
			_, err := raw.Exec(`INSERT INTO events(id,kind,agent,session_id,body,created_at,client,workspace) VALUES('race-backdated','note','a','s','body','2020-01-01T00:00:00Z','c','w')`)
			if err != nil {
				return fmt.Errorf("insert racing event: %w", err)
			}
			return nil
		}},
		{name: "late audit waits then freeze rejects it", compete: func(_ context.Context, _ *Database, raw *sql.DB, _ apptypes.CatalogTargetPlanRequest) error {
			_, err := raw.Exec(`INSERT INTO command_audits(event_id,command_text,input_text,output_text,input_truncated,output_truncated,exit_code,failed,input_original_bytes,output_original_bytes,command_wrapper,command_name,failure_reason) VALUES('catalog-event-b','echo','','',0,0,0,0,0,0,'direct','echo','none')`)
			if err != nil {
				return fmt.Errorf("insert racing audit: %w", err)
			}
			return nil
		}},
		{name: "competing planner loses expected head", compete: func(ctx context.Context, other *Database, _ *sql.DB, request apptypes.CatalogTargetPlanRequest) error {
			request.ReservationID = "race-loser"
			_, err := other.PlanAndReserveCatalogTarget(ctx, request)
			return err
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			ctx := context.Background()
			database, raw := newActivatedCatalogStore(t, 1)
			defer func() { _ = raw.Close() }()
			initial, _ := database.CurrentCatalogRanges(ctx, catalogTestBudget())
			request := plannerRequest(initial.Head, 1, "race-winner", 1)
			other := NewDatabase(database.Path(), preparedMigrations(t))
			started := make(chan struct{})
			result := make(chan error, 1)
			var once sync.Once
			database.segmentTargetPlannerHook = func(point string) error {
				if point != "snapshot-pinned" {
					return nil
				}
				once.Do(func() {
					go func() {
						close(started)
						result <- test.compete(ctx, other, raw, request)
					}()
				})
				<-started
				select {
				case err := <-result:
					t.Fatalf("competitor escaped writer fence before publication: %v", err)
				default:
				}
				return nil
			}
			plan, err := database.PlanAndReserveCatalogTarget(ctx, request)
			if err != nil {
				t.Fatal(err)
			}
			competitorErr := <-result
			if test.wantWriter {
				if competitorErr != nil {
					t.Fatal(competitorErr)
				}
			} else if competitorErr == nil {
				t.Fatal("competing mutation/planner unexpectedly succeeded")
			}
			var reservations int
			if err = raw.QueryRow(`SELECT count(*) FROM archive_catalog_reservation_deltas WHERE delta='reserve'`).Scan(&reservations); err != nil || reservations != 1 {
				t.Fatalf("reservations=%d err=%v", reservations, err)
			}
			current, err := database.CurrentCatalogRanges(ctx, catalogTestBudget())
			if err != nil || current.Head != plan.Head || current.Ranges[0].Placement != domain.CatalogPlacementReserved {
				t.Fatalf("current=%+v err=%v", current, err)
			}
			if test.wantWriter {
				var sequence int64
				var covered int
				if err = raw.QueryRow(`SELECT sequence FROM archive_event_sequences WHERE event_id='race-backdated'`).Scan(&sequence); err != nil {
					t.Fatal(err)
				}
				if err = raw.QueryRow(`SELECT count(*) FROM archive_catalog_current_ranges WHERE placement_state<>'hot' AND ? BETWEEN start_sequence AND end_sequence`, sequence).Scan(&covered); err != nil || sequence != 2 || covered != 0 {
					t.Fatalf("backdated sequence=%d non-hot coverage=%d err=%v", sequence, covered, err)
				}
			}
		})
	}
}

func TestReservedTargetFreezesSameLengthEventMutationAndLateAudit(t *testing.T) {
	ctx := context.Background()
	database, raw := newActivatedCatalogStore(t, 2)
	defer func() { _ = raw.Close() }()
	initial, _ := database.CurrentCatalogRanges(ctx, catalogTestBudget())
	plan, err := database.PlanAndReserveCatalogTarget(ctx, plannerRequest(initial.Head, 2, "freeze-target", 2))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = raw.Exec(`UPDATE events SET body='same' WHERE id='catalog-event-b'`); err == nil || !strings.Contains(err.Error(), "reserved archive history") {
		t.Fatalf("same-length update error = %v", err)
	}
	if _, err = raw.Exec(`INSERT INTO command_audits(event_id,command_text,input_text,output_text,input_truncated,output_truncated,exit_code,failed,input_original_bytes,output_original_bytes,command_wrapper,command_name,failure_reason) VALUES('catalog-event-b','echo','','',0,0,0,0,0,0,'direct','echo','none')`); err == nil || !strings.Contains(err.Error(), "reserved archive history") {
		t.Fatalf("late audit error = %v", err)
	}
	var plans, units int
	if err = raw.QueryRow(`SELECT count(*) FROM archive_segment_target_plans WHERE reservation_id=?`, plan.ReservationID).Scan(&plans); err != nil {
		t.Fatal(err)
	}
	if err = raw.QueryRow(`SELECT count(*) FROM archive_segment_target_plan_units WHERE reservation_id=?`, plan.ReservationID).Scan(&units); err != nil {
		t.Fatal(err)
	}
	if plans != 1 || units != 2 {
		t.Fatalf("durable evidence plans=%d units=%d", plans, units)
	}
}

func TestPlanAndReserveCatalogTargetNormalizesTimestampStorageFailures(t *testing.T) {
	mutations := []string{
		`UPDATE events SET created_at='not-a-time'`,
		`UPDATE events SET created_at=CAST('2020-01-01T00:00:00Z' AS BLOB)`,
		`UPDATE events SET created_at=42`,
	}
	for index, mutation := range mutations {
		t.Run(strings.ReplaceAll(mutation, " ", "_"), func(t *testing.T) {
			ctx := context.Background()
			database, raw := newActivatedCatalogStore(t, 1)
			defer func() { _ = raw.Close() }()
			if _, err := raw.Exec(mutation); err != nil {
				t.Fatal(err)
			}
			initial, _ := database.CurrentCatalogRanges(ctx, catalogTestBudget())
			_, err := database.PlanAndReserveCatalogTarget(ctx, plannerRequest(initial.Head, 1, fmt.Sprintf("malformed-%d", index), 1))
			if !errors.Is(err, apptypes.ErrSegmentTargetMalformedTimestamp) {
				t.Fatalf("error = %v", err)
			}
			after, readErr := database.CurrentCatalogRanges(ctx, catalogTestBudget())
			if readErr != nil || after.Head != initial.Head {
				t.Fatalf("head changed: %+v %v", after.Head, readErr)
			}
		})
	}
}

func TestSegmentTargetStopsBeforeHydratingRecentOrOversizeBoundary(t *testing.T) {
	ctx := context.Background()
	t.Run("recent", func(t *testing.T) {
		database, raw := newActivatedCatalogStore(t, 2)
		defer func() { _ = raw.Close() }()
		if _, err := raw.Exec(`UPDATE events SET created_at='2026-08-05T11:30:00Z',body_plaintext_bytes=999999999 WHERE id='catalog-event-c'`); err != nil {
			t.Fatal(err)
		}
		initial, _ := database.CurrentCatalogRanges(ctx, catalogTestBudget())
		plan, err := database.PlanAndReserveCatalogTarget(ctx, plannerRequest(initial.Head, 2, "stop-recent", 2))
		if err != nil || plan.Range.End != 1 {
			t.Fatalf("plan=%+v error=%v", plan, err)
		}
	})
	t.Run("oversize", func(t *testing.T) {
		database, raw := newActivatedCatalogStore(t, 2)
		defer func() { _ = raw.Close() }()
		if _, err := raw.Exec(`UPDATE events SET body=zeroblob(?) WHERE id='catalog-event-c'`, segmentTargetMaxValueBytes+1); err != nil {
			t.Fatal(err)
		}
		initial, _ := database.CurrentCatalogRanges(ctx, catalogTestBudget())
		plan, err := database.PlanAndReserveCatalogTarget(ctx, plannerRequest(initial.Head, 2, "stop-oversize", 2))
		if err != nil || plan.Range.End != 1 {
			t.Fatalf("plan=%+v error=%v", plan, err)
		}
	})
}

func TestSegmentTargetByteAccountingMatchesCanonicalEncoding(t *testing.T) {
	values := []struct {
		name  string
		value any
		bytes int64
	}{
		{name: "japanese", value: "日本語", bytes: int64(len([]byte("日本語")))},
		{name: "combining", value: "e\u0301", bytes: int64(len([]byte("e\u0301")))},
		{name: "emoji", value: "🙂", bytes: int64(len([]byte("🙂")))},
		{name: "nul", value: "a\x00b", bytes: 3},
		{name: "invalid utf8 equivalent blob", value: []byte{0xff, 0xfe, 0x80}, bytes: 3},
	}
	for _, test := range values {
		t.Run(test.name, func(t *testing.T) {
			ctx := context.Background()
			database, raw := newActivatedCatalogStore(t, 1)
			defer func() { _ = raw.Close() }()
			if _, err := raw.Exec(`UPDATE events SET body=?`, test.value); err != nil {
				t.Fatal(err)
			}
			policy := plannerPolicy(1)
			meta, err := readSegmentTargetMetadata(ctx, raw, 1, policy)
			if err != nil {
				t.Fatal(err)
			}
			unit, err := hydrateSegmentTargetUnit(ctx, raw, 1)
			if err != nil {
				t.Fatal(err)
			}
			canonical, err := unit.CanonicalBytes()
			if err != nil {
				t.Fatal(err)
			}
			if meta.canonicalBytes != int64(len(canonical)) || meta.decodedBytes != test.bytes {
				t.Fatalf("metadata=%+v canonical=%d decoded-want=%d", meta, len(canonical), test.bytes)
			}
			_ = database
		})
	}
}

func TestSegmentTargetCanonicalByteCapBoundaryIsExact(t *testing.T) {
	const body = "日本語e\u0301🙂\x00"
	measure := func(t *testing.T) int64 {
		t.Helper()
		database, raw := newActivatedCatalogStore(t, 1)
		defer func() { _ = raw.Close() }()
		if _, err := raw.Exec(`UPDATE events SET body=?`, body); err != nil {
			t.Fatal(err)
		}
		meta, err := readSegmentTargetMetadata(context.Background(), raw, 1, plannerPolicy(1))
		if err != nil {
			t.Fatal(err)
		}
		_ = database
		return meta.canonicalBytes
	}
	wantBytes := measure(t)
	for _, test := range []struct {
		name    string
		cap     int64
		wantErr error
	}{
		{name: "exact", cap: wantBytes},
		{name: "one byte below", cap: wantBytes - 1, wantErr: apptypes.ErrSegmentTargetOversizeFirst},
	} {
		t.Run(test.name, func(t *testing.T) {
			ctx := context.Background()
			database, raw := newActivatedCatalogStore(t, 1)
			defer func() { _ = raw.Close() }()
			if _, err := raw.Exec(`UPDATE events SET body=?`, body); err != nil {
				t.Fatal(err)
			}
			initial, _ := database.CurrentCatalogRanges(ctx, catalogTestBudget())
			request := plannerRequest(initial.Head, 1, "byte-cap-"+strings.ReplaceAll(test.name, " ", "-"), 1)
			request.Policy.MaxCanonicalPlainBytes = test.cap
			request.Policy.MaxStoredUpperBytes = test.cap
			plan, err := database.PlanAndReserveCatalogTarget(ctx, request)
			if test.wantErr != nil {
				if !errors.Is(err, test.wantErr) {
					t.Fatalf("error = %v", err)
				}
				after, readErr := database.CurrentCatalogRanges(ctx, catalogTestBudget())
				if readErr != nil || after.Head != initial.Head {
					t.Fatalf("head changed: %+v %v", after.Head, readErr)
				}
				return
			}
			if err != nil || plan.CanonicalPlainBytes != wantBytes {
				t.Fatalf("plan=%+v error=%v want=%d", plan, err, wantBytes)
			}
		})
	}
}

func plannerRequest(head apptypes.CatalogHead, highWater int64, reservationID string, maxRows int) apptypes.CatalogTargetPlanRequest {
	return apptypes.CatalogTargetPlanRequest{ExpectedHead: head, ReservationID: reservationID, CapturedHighWater: highWater, Policy: plannerPolicy(maxRows), Budget: catalogTestBudget()}
}

func TestPlanAndReserveCatalogTargetExcludesConcurrentBackdatedAppend(t *testing.T) {
	ctx := context.Background()
	database, raw := newActivatedCatalogStore(t, 3)
	defer func() { _ = raw.Close() }()
	initial, err := database.CurrentCatalogRanges(ctx, catalogTestBudget())
	if err != nil {
		t.Fatal(err)
	}
	if _, err = raw.Exec(`INSERT INTO command_audits(event_id,command_text,input_text,output_text,input_truncated,output_truncated,exit_code,failed,input_original_bytes,output_original_bytes,command_wrapper,command_name,failure_reason) VALUES('catalog-event-b','echo test','','test',0,0,0,0,0,4,'direct','echo','none')`); err != nil {
		t.Fatal(err)
	}
	// A backdated write is newer in archive sequence and therefore remains Hot.
	if _, err = raw.Exec(`INSERT INTO events(id,kind,agent,session_id,body,created_at,client,workspace) VALUES('planner-backdated','note','a','s','body','2020-01-01T00:00:00Z','c','w')`); err != nil {
		t.Fatal(err)
	}
	plan, err := database.PlanAndReserveCatalogTarget(ctx, plannerRequest(initial.Head, 3, "target-three", 10))
	if err != nil {
		t.Fatal(err)
	}
	if plan.Range != (domain.CatalogRange{Start: 1, End: 3}) || plan.Rows != 3 || plan.CapturedHighWater != 3 || !domain.ValidCatalogDigest(plan.PlanDigest) {
		t.Fatalf("plan = %+v", plan)
	}
	snapshot, err := database.CurrentCatalogRanges(ctx, catalogTestBudget())
	if err != nil {
		t.Fatal(err)
	}
	if len(snapshot.Ranges) != 2 || snapshot.Ranges[0].Placement != domain.CatalogPlacementReserved || snapshot.Ranges[1].Range != (domain.CatalogRange{Start: 4, End: 4}) || snapshot.Ranges[1].Placement != domain.CatalogPlacementHot {
		t.Fatalf("ranges = %+v", snapshot.Ranges)
	}
}

func TestPlanAndReserveCatalogTargetTypedFailuresDoNotPublish(t *testing.T) {
	tests := []struct {
		name      string
		mutate    func(*apptypes.CatalogTargetPlanRequest)
		createdAt string
		want      error
	}{
		{name: "recent first", createdAt: "2026-08-05T11:30:00Z", want: apptypes.ErrSegmentTargetRecentFirst},
		{name: "malformed timestamp", createdAt: "not-a-time", want: apptypes.ErrSegmentTargetMalformedTimestamp},
		{name: "oversize first", createdAt: "2020-01-01T00:00:00Z", mutate: func(r *apptypes.CatalogTargetPlanRequest) {
			r.Policy.MaxCanonicalPlainBytes = 1
			r.Policy.MaxStoredUpperBytes = 1
		}, want: apptypes.ErrSegmentTargetOversizeFirst},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			ctx := context.Background()
			database, raw := newActivatedCatalogStore(t, 1)
			defer func() { _ = raw.Close() }()
			if _, err := raw.Exec(`UPDATE events SET created_at=?`, test.createdAt); err != nil {
				t.Fatal(err)
			}
			initial, err := database.CurrentCatalogRanges(ctx, catalogTestBudget())
			if err != nil {
				t.Fatal(err)
			}
			request := plannerRequest(initial.Head, 1, "typed-failure", 10)
			if test.mutate != nil {
				test.mutate(&request)
			}
			_, err = database.PlanAndReserveCatalogTarget(ctx, request)
			if !errors.Is(err, test.want) {
				t.Fatalf("error = %v", err)
			}
			after, err := database.CurrentCatalogRanges(ctx, catalogTestBudget())
			if err != nil || after.Head != initial.Head {
				t.Fatalf("head changed: before=%+v after=%+v error=%v", initial.Head, after.Head, err)
			}
		})
	}
}

func TestPlanAndReserveCatalogTargetDeadlineIsIncompleteAndDoesNotReserve(t *testing.T) {
	ctx := context.Background()
	database, raw := newActivatedCatalogStore(t, 1)
	defer func() { _ = raw.Close() }()
	initial, _ := database.CurrentCatalogRanges(ctx, catalogTestBudget())
	request := plannerRequest(initial.Head, 1, "deadline", 10)
	request.Budget.WallTime = time.Nanosecond
	_, err := database.PlanAndReserveCatalogTarget(ctx, request)
	if !errors.Is(err, apptypes.ErrSegmentTargetSelectionIncomplete) {
		t.Fatalf("error = %v", err)
	}
	after, readErr := database.CurrentCatalogRanges(ctx, catalogTestBudget())
	if readErr != nil || after.Head != initial.Head {
		t.Fatalf("head changed: %+v %v", after.Head, readErr)
	}
}

func TestSegmentTargetDeadlineAtTransactionalBoundariesRollsBackEverything(t *testing.T) {
	for _, point := range []string{"selection-complete", "plan-unit-inserted"} {
		t.Run(point, func(t *testing.T) {
			ctx := context.Background()
			database, raw := newActivatedCatalogStore(t, 2)
			defer func() { _ = raw.Close() }()
			initial, _ := database.CurrentCatalogRanges(ctx, catalogTestBudget())
			fired := make(chan struct{})
			var once sync.Once
			database.segmentTargetPlannerHook = func(observed string) error {
				if observed != point {
					return nil
				}
				once.Do(func() { close(fired) })
				return context.DeadlineExceeded
			}
			_, err := database.PlanAndReserveCatalogTarget(ctx, plannerRequest(initial.Head, 2, "deadline-"+point, 2))
			if !errors.Is(err, apptypes.ErrSegmentTargetSelectionIncomplete) {
				t.Fatalf("error = %v", err)
			}
			select {
			case <-fired:
			default:
				t.Fatal("deadline seam was not reached")
			}
			after, readErr := database.CurrentCatalogRanges(ctx, catalogTestBudget())
			if readErr != nil || after.Head != initial.Head {
				t.Fatalf("head changed: before=%+v after=%+v err=%v", initial.Head, after.Head, readErr)
			}
			var epochs, deltas, plans, units int
			if err = raw.QueryRow(`SELECT (SELECT count(*) FROM archive_catalog_epochs),(SELECT count(*) FROM archive_catalog_reservation_deltas),(SELECT count(*) FROM archive_segment_target_plans),(SELECT count(*) FROM archive_segment_target_plan_units)`).Scan(&epochs, &deltas, &plans, &units); err != nil {
				t.Fatal(err)
			}
			if epochs != 0 || deltas != 0 || plans != 0 || units != 0 {
				t.Fatalf("partial publication epochs=%d deltas=%d plans=%d units=%d", epochs, deltas, plans, units)
			}
		})
	}
}

func TestPlanAndReserveCatalogTargetShorteningRequiresReleasedMeasuredProofAndNewID(t *testing.T) {
	ctx := context.Background()
	database, raw := newActivatedCatalogStore(t, 3)
	defer func() { _ = raw.Close() }()
	initial, _ := database.CurrentCatalogRanges(ctx, catalogTestBudget())
	first, err := database.PlanAndReserveCatalogTarget(ctx, plannerRequest(initial.Head, 3, "original-target", 3))
	if err != nil {
		t.Fatal(err)
	}
	retryDigest, err := domain.SegmentTargetRetryEvidenceDigest(first.PlanDigest, apptypes.SegmentTargetFailureFileCap, 3<<20, 2<<20)
	if err != nil {
		t.Fatal(err)
	}
	preRelease := plannerRequest(first.Head, 3, "pre-release-shorter", 2)
	preRelease.Retry = apptypes.CatalogTargetRetry{PreviousReservationID: first.ReservationID, PreviousRange: first.Range, FailureClass: apptypes.SegmentTargetFailureFileCap, MeasuredBytes: 3 << 20, FailedCapBytes: 2 << 20, EvidenceDigest: retryDigest}
	if _, err = database.PlanAndReserveCatalogTarget(ctx, preRelease); !errors.Is(err, apptypes.ErrSegmentTargetRetryProof) {
		t.Fatalf("pre-release retry error = %v", err)
	}
	released, err := database.ReleaseCatalogReservation(ctx, apptypes.CatalogRelease{ExpectedHead: first.Head, ReservationID: first.ReservationID, EvidenceDigest: retryDigest, Budget: catalogTestBudget()})
	if err != nil {
		t.Fatal(err)
	}
	unproven := plannerRequest(released, 3, "silent-shorter-target", 2)
	if _, err = database.PlanAndReserveCatalogTarget(ctx, unproven); !errors.Is(err, apptypes.ErrSegmentTargetRetryProof) {
		t.Fatalf("unproven shortening error = %v", err)
	}
	retry := plannerRequest(released, 3, "shorter-target", 2)
	retry.Retry = apptypes.CatalogTargetRetry{PreviousReservationID: first.ReservationID, PreviousRange: first.Range, FailureClass: apptypes.SegmentTargetFailureFileCap, MeasuredBytes: 3 << 20, FailedCapBytes: 2 << 20, EvidenceDigest: retryDigest}
	invalid := []struct {
		name   string
		mutate func(*apptypes.CatalogTargetPlanRequest)
	}{
		{name: "forged digest", mutate: func(r *apptypes.CatalogTargetPlanRequest) { r.Retry.EvidenceDigest = strings.Repeat("a", 64) }},
		{name: "below cap", mutate: func(r *apptypes.CatalogTargetPlanRequest) {
			r.Retry.MeasuredBytes = r.Retry.FailedCapBytes
			r.Retry.EvidenceDigest = strings.Repeat("b", 64)
		}},
		{name: "different high water", mutate: func(r *apptypes.CatalogTargetPlanRequest) { r.CapturedHighWater = 2 }},
		{name: "different horizon", mutate: func(r *apptypes.CatalogTargetPlanRequest) { r.Policy.HotHorizon += time.Hour }},
		{name: "unrelated plain cap", mutate: func(r *apptypes.CatalogTargetPlanRequest) { r.Policy.MaxCanonicalPlainBytes-- }},
	}
	for _, test := range invalid {
		t.Run(test.name, func(t *testing.T) {
			candidate := retry
			test.mutate(&candidate)
			if _, retryErr := database.PlanAndReserveCatalogTarget(ctx, candidate); !errors.Is(retryErr, apptypes.ErrSegmentTargetRetryProof) {
				t.Fatalf("error = %v", retryErr)
			}
			after, readErr := database.CurrentCatalogRanges(ctx, catalogTestBudget())
			if readErr != nil || after.Head != released {
				t.Fatalf("head changed: %+v %v", after.Head, readErr)
			}
		})
	}
	shorter, err := database.PlanAndReserveCatalogTarget(ctx, retry)
	if err != nil {
		t.Fatal(err)
	}
	if shorter.Range != (domain.CatalogRange{Start: 1, End: 2}) {
		t.Fatalf("shorter = %+v", shorter)
	}
}

func TestShorteningRejectsSameLengthSourceChangeAfterRelease(t *testing.T) {
	ctx := context.Background()
	database, raw := newActivatedCatalogStore(t, 3)
	defer func() { _ = raw.Close() }()
	initial, _ := database.CurrentCatalogRanges(ctx, catalogTestBudget())
	first, err := database.PlanAndReserveCatalogTarget(ctx, plannerRequest(initial.Head, 3, "changed-source-original", 3))
	if err != nil {
		t.Fatal(err)
	}
	digest, _ := domain.SegmentTargetRetryEvidenceDigest(first.PlanDigest, apptypes.SegmentTargetFailureFileCap, 3<<20, 2<<20)
	released, err := database.ReleaseCatalogReservation(ctx, apptypes.CatalogRelease{ExpectedHead: first.Head, ReservationID: first.ReservationID, EvidenceDigest: digest, Budget: catalogTestBudget()})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = raw.Exec(`UPDATE events SET body='same' WHERE id='catalog-event-b'`); err != nil {
		t.Fatal(err)
	}
	retry := plannerRequest(released, 3, "changed-source-shorter", 2)
	retry.Retry = apptypes.CatalogTargetRetry{PreviousReservationID: first.ReservationID, PreviousRange: first.Range, FailureClass: apptypes.SegmentTargetFailureFileCap, MeasuredBytes: 3 << 20, FailedCapBytes: 2 << 20, EvidenceDigest: digest}
	if _, err = database.PlanAndReserveCatalogTarget(ctx, retry); !errors.Is(err, apptypes.ErrSegmentTargetRetryProof) {
		t.Fatalf("changed source retry error = %v", err)
	}
}

func TestShorteningRejectsReleaseThatWasNotBoundToFailureProof(t *testing.T) {
	ctx := context.Background()
	database, raw := newActivatedCatalogStore(t, 3)
	defer func() { _ = raw.Close() }()
	initial, _ := database.CurrentCatalogRanges(ctx, catalogTestBudget())
	first, err := database.PlanAndReserveCatalogTarget(ctx, plannerRequest(initial.Head, 3, "wrong-release-original", 3))
	if err != nil {
		t.Fatal(err)
	}
	released, err := database.ReleaseCatalogReservation(ctx, apptypes.CatalogRelease{ExpectedHead: first.Head, ReservationID: first.ReservationID, EvidenceDigest: first.PlanDigest, Budget: catalogTestBudget()})
	if err != nil {
		t.Fatal(err)
	}
	digest, _ := domain.SegmentTargetRetryEvidenceDigest(first.PlanDigest, apptypes.SegmentTargetFailureFileCap, 3<<20, 2<<20)
	retry := plannerRequest(released, 3, "wrong-release-shorter", 2)
	retry.Retry = apptypes.CatalogTargetRetry{PreviousReservationID: first.ReservationID, PreviousRange: first.Range, FailureClass: apptypes.SegmentTargetFailureFileCap, MeasuredBytes: 3 << 20, FailedCapBytes: 2 << 20, EvidenceDigest: digest}
	if _, err = database.PlanAndReserveCatalogTarget(ctx, retry); !errors.Is(err, apptypes.ErrSegmentTargetRetryProof) {
		t.Fatalf("wrong release proof retry error = %v", err)
	}
}
