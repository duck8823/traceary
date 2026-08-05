package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	apptypes "github.com/duck8823/traceary/application/types"
	"github.com/duck8823/traceary/domain"
)

func catalogTestBudget() apptypes.CatalogBudget {
	return apptypes.CatalogBudget{Ranges: 100, WallTime: 10 * time.Second, LockTime: 5 * time.Second}
}

func TestCatalogProspectiveBoundaryCapRejectsBeforePublishingHead(t *testing.T) {
	ctx := context.Background()
	database, raw := newActivatedCatalogStore(t, 3)
	defer func() { _ = raw.Close() }()
	initial, err := database.CurrentCatalogRanges(ctx, catalogTestBudget())
	if err != nil {
		t.Fatal(err)
	}
	transition, err := domain.ReservationTransition(domain.CatalogRange{Start: 2, End: 2}, "cap-cross")
	if err != nil {
		t.Fatal(err)
	}
	tx, err := raw.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	_, err = commitCatalogEpoch(ctx, tx, catalogEpochCommit{expected: initial.Head, highWater: 3, transitions: []domain.CatalogTransition{transition}, evidenceDigest: "acacacacacacacacacacacacacacacacacacacacacacacacacacacacacacacac", reservationID: "cap-cross", delta: "reserve", boundaryPointLimit: 2}, 10)
	if !errors.Is(err, apptypes.ErrCatalogLimit) {
		_ = tx.Rollback()
		t.Fatalf("cap error = %v", err)
	}
	_ = tx.Rollback()
	after, err := database.CurrentCatalogRanges(ctx, catalogTestBudget())
	if err != nil {
		t.Fatal(err)
	}
	if after.Head != initial.Head {
		t.Fatalf("head published after cap failure: before=%+v after=%+v", initial.Head, after.Head)
	}
	var epochs int
	if err = raw.QueryRow(`SELECT count(*) FROM archive_catalog_epochs`).Scan(&epochs); err != nil || epochs != 0 {
		t.Fatalf("epochs=%d err=%v", epochs, err)
	}
}

func TestCatalogReservationIDIsGloballyUniqueAndRequestsAreIdempotent(t *testing.T) {
	ctx := context.Background()
	database, raw := newActivatedCatalogStore(t, 3)
	defer func() { _ = raw.Close() }()
	initial, _ := database.CurrentCatalogRanges(ctx, catalogTestBudget())
	evidence := "7777777777777777777777777777777777777777777777777777777777777777"
	command := apptypes.CatalogReservation{ExpectedHead: initial.Head, ReservationID: "  stable-id  ", Range: domain.CatalogRange{Start: 1, End: 2}, EvidenceDigest: evidence, Budget: catalogTestBudget()}
	reserved, err := database.ReserveCatalogTarget(ctx, command)
	if err != nil {
		t.Fatal(err)
	}
	again, err := database.ReserveCatalogTarget(ctx, command)
	if err != nil || again != reserved {
		t.Fatalf("idempotent reserve = %+v, %v", again, err)
	}
	conflict := command
	conflict.Range = domain.CatalogRange{Start: 2, End: 3}
	if _, err = database.ReserveCatalogTarget(ctx, conflict); !errors.Is(err, apptypes.ErrCatalogReservationConflict) {
		t.Fatalf("conflict error = %v", err)
	}
	released, err := database.ReleaseCatalogReservation(ctx, apptypes.CatalogRelease{ExpectedHead: reserved, ReservationID: "stable-id ", EvidenceDigest: evidence, Budget: catalogTestBudget()})
	if err != nil {
		t.Fatal(err)
	}
	retried, err := database.ReleaseCatalogReservation(ctx, apptypes.CatalogRelease{ExpectedHead: reserved, ReservationID: " stable-id", EvidenceDigest: evidence, Budget: catalogTestBudget()})
	if err != nil || retried != released {
		t.Fatalf("idempotent release = %+v, %v", retried, err)
	}
	var distinct int
	if err = raw.QueryRow(`SELECT count(DISTINCT reservation_id) FROM archive_catalog_reservation_deltas WHERE reservation_id='stable-id'`).Scan(&distinct); err != nil || distinct != 1 {
		t.Fatalf("canonical reservation id count=%d err=%v", distinct, err)
	}
}

func TestCatalogBoundaryScanDoesNotConsumeReturnedRangeCap(t *testing.T) {
	ctx := context.Background()
	database, raw := newActivatedCatalogStore(t, 1)
	defer func() { _ = raw.Close() }()
	budget := catalogTestBudget()
	budget.Ranges = 1
	initial, err := database.CurrentCatalogRanges(ctx, budget)
	if err != nil {
		t.Fatal(err)
	}
	evidence := "efefefefefefefefefefefefefefefefefefefefefefefefefefefefefefefef"
	reserved, err := database.ReserveCatalogTarget(ctx, apptypes.CatalogReservation{ExpectedHead: initial.Head, ReservationID: "cap-one", Range: domain.CatalogRange{Start: 1, End: 1}, EvidenceDigest: evidence, Budget: budget})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = raw.Exec(`INSERT INTO events(id,kind,agent,session_id,body,created_at,client,workspace) VALUES('cap-extension','note','a','s','body','2026-08-01T00:00:01Z','c','w')`); err != nil {
		t.Fatal(err)
	}
	released, err := database.ReleaseCatalogReservation(ctx, apptypes.CatalogRelease{ExpectedHead: reserved, ReservationID: "cap-one", EvidenceDigest: evidence, Budget: budget})
	if err != nil {
		t.Fatal(err)
	}
	current, err := database.CurrentCatalogRanges(ctx, budget)
	if err != nil {
		t.Fatal(err)
	}
	if current.Head != released || len(current.Ranges) != 1 || current.Ranges[0].Range.End != 2 || current.Ranges[0].Placement != domain.CatalogPlacementHot {
		t.Fatalf("current = %+v", current)
	}
	old, err := database.CatalogRangesAtEpoch(ctx, reserved.Epoch, budget)
	if err != nil {
		t.Fatal(err)
	}
	if len(old.Ranges) != 1 || old.Ranges[0].Range.End != 1 || old.Ranges[0].Placement != domain.CatalogPlacementReserved {
		t.Fatalf("old = %+v", old)
	}
	var boundaries int
	if err = raw.QueryRow(`SELECT count(*) FROM archive_catalog_boundaries`).Scan(&boundaries); err != nil || boundaries != 2 {
		t.Fatalf("boundaries=%d err=%v", boundaries, err)
	}
}

func TestCatalogHeadPathDoesNotScanAllEpochs(t *testing.T) {
	ctx := context.Background()
	database, raw := newActivatedCatalogStore(t, 1)
	defer func() { _ = raw.Close() }()
	tx, err := raw.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	parent := emptyCatalogDigestValue
	evidence := "8888888888888888888888888888888888888888888888888888888888888888"
	boundaryDigest, _ := domain.CanonicalCatalogBoundaryDigest([]int64{1})
	const epochs = 10_001
	transitionDigest, _ := domain.CanonicalCatalogTransitionDigest(nil)
	for epoch := int64(1); epoch <= epochs; epoch++ {
		ledger, _ := domain.CatalogLedgerDigest(parent, epoch, epoch-1, epoch, 1, transitionDigest, boundaryDigest, evidence)
		if _, err = tx.Exec(`INSERT INTO archive_catalog_epochs(epoch,parent_epoch,transition_digest,evidence_digest,parent_ledger_digest,source_high_water,boundary_count,boundary_digest,ledger_digest,committed_at) VALUES(?,?,?,?,?,?,1,?,?,'2026-08-01T00:00:00Z')`, epoch, epoch-1, transitionDigest, evidence, parent, epoch, boundaryDigest, ledger); err != nil {
			t.Fatal(err)
		}
		parent = ledger
	}
	current := []apptypes.CatalogCurrentRange{{Range: domain.CatalogRange{Start: 1, End: epochs}, Placement: domain.CatalogPlacementHot}}
	digest := digestCatalogRanges(current)
	if _, err = tx.Exec(`DELETE FROM archive_catalog_current_ranges`); err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(`INSERT INTO archive_catalog_current_ranges VALUES(1,?,'hot','','',0)`, epochs); err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(`UPDATE archive_catalog_head SET current_epoch=?,ledger_digest=?,current_ranges_digest=? WHERE singleton=1`, epochs, parent, digest); err != nil {
		t.Fatal(err)
	}
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}
	snapshot, err := database.CurrentCatalogRanges(ctx, apptypes.CatalogBudget{Ranges: 10, WallTime: 10 * time.Second, LockTime: time.Second})
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.Head.Epoch != epochs {
		t.Fatalf("head = %+v", snapshot.Head)
	}
	historical, err := database.CatalogRangesAtEpoch(ctx, 9_999, apptypes.CatalogBudget{Ranges: 2, WallTime: 10 * time.Second, LockTime: time.Second})
	if err != nil {
		t.Fatal(err)
	}
	if len(historical.Ranges) != 1 || historical.Ranges[0].Range.End != 9_999 {
		t.Fatalf("historical ranges = %+v", historical.Ranges)
	}
	var boundaryRows int
	if err = raw.QueryRow(`SELECT count(*) FROM archive_catalog_boundaries`).Scan(&boundaryRows); err != nil || boundaryRows != 1 {
		t.Fatalf("boundary rows=%d err=%v", boundaryRows, err)
	}
	planRows, err := raw.Query(`EXPLAIN QUERY PLAN SELECT epoch,from_state,to_state,reservation_id,segment_id FROM archive_catalog_range_transitions WHERE epoch<=? AND start_sequence<=? AND end_sequence>=? ORDER BY epoch DESC,transition_index DESC LIMIT 1`, epochs, 1, 1)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = planRows.Close() }()
	var plan strings.Builder
	for planRows.Next() {
		var id, parentID, unused int
		var detail string
		if err = planRows.Scan(&id, &parentID, &unused, &detail); err != nil {
			t.Fatal(err)
		}
		plan.WriteString(detail)
	}
	if !strings.Contains(plan.String(), "INDEX") {
		t.Fatalf("query plan = %s", plan.String())
	}
}

func newActivatedCatalogStore(t *testing.T, events int) (*Database, *sql.DB) {
	t.Helper()
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "store.db")
	database := NewDatabase(path, preparedMigrations(t))
	if err := NewStoreManagementDatasource(database).Initialize(ctx); err != nil {
		t.Fatal(err)
	}
	raw, err := sql.Open("sqlite", sqliteDSN(path))
	if err != nil {
		t.Fatal(err)
	}
	for i := 1; i <= events; i++ {
		if _, err = raw.Exec(`INSERT INTO events(id,kind,agent,session_id,body,created_at,client,workspace) VALUES(?, 'note','a','s','body','2026-08-01T00:00:00Z','c','w')`, "catalog-event-"+string(rune('a'+i))); err != nil {
			_ = raw.Close()
			t.Fatal(err)
		}
	}
	if _, err = raw.Exec(`UPDATE archive_sequence_inventory_state SET generation_id='verified',phase='complete',high_water=?,revision=1 WHERE singleton=1`, events); err != nil {
		t.Fatal(err)
	}
	if _, err = raw.Exec(`UPDATE archive_sequence_activation SET active_generation_id='verified',verified_high_water=? WHERE singleton=1`, events); err != nil {
		t.Fatal(err)
	}
	return database, raw
}

func TestCatalogReservationReleaseAndOldEpochReplay(t *testing.T) {
	ctx := context.Background()
	database, raw := newActivatedCatalogStore(t, 6)
	defer func() { _ = raw.Close() }()
	initial, err := database.CurrentCatalogRanges(ctx, catalogTestBudget())
	if err != nil {
		t.Fatal(err)
	}
	evidence := "1111111111111111111111111111111111111111111111111111111111111111"
	reserved, err := database.ReserveCatalogTarget(ctx, apptypes.CatalogReservation{ExpectedHead: initial.Head, ReservationID: "target-1", Range: domain.CatalogRange{Start: 2, End: 4}, EvidenceDigest: evidence, Budget: catalogTestBudget()})
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := database.CurrentCatalogRanges(ctx, catalogTestBudget())
	if err != nil {
		t.Fatal(err)
	}
	if len(snapshot.Ranges) != 3 || snapshot.Ranges[1].Placement != domain.CatalogPlacementReserved {
		t.Fatalf("ranges = %+v", snapshot.Ranges)
	}
	if _, err = database.ReserveCatalogTarget(ctx, apptypes.CatalogReservation{ExpectedHead: reserved, ReservationID: "overlap", Range: domain.CatalogRange{Start: 3, End: 5}, EvidenceDigest: evidence, Budget: catalogTestBudget()}); !errors.Is(err, apptypes.ErrCatalogOverlap) {
		t.Fatalf("overlap error = %v", err)
	}
	released, err := database.ReleaseCatalogReservation(ctx, apptypes.CatalogRelease{ExpectedHead: reserved, ReservationID: "target-1", EvidenceDigest: evidence, Budget: catalogTestBudget()})
	if err != nil {
		t.Fatal(err)
	}
	current, err := database.CurrentCatalogRanges(ctx, catalogTestBudget())
	if err != nil {
		t.Fatal(err)
	}
	if len(current.Ranges) != 1 || current.Ranges[0].Placement != domain.CatalogPlacementHot {
		t.Fatalf("released ranges = %+v", current.Ranges)
	}
	old, err := database.CatalogRangesAtEpoch(ctx, reserved.Epoch, catalogTestBudget())
	if err != nil {
		t.Fatal(err)
	}
	if len(old.Ranges) != 3 || old.Ranges[1].Placement != domain.CatalogPlacementReserved {
		t.Fatalf("old ranges = %+v", old.Ranges)
	}
	if released.Epoch != 2 {
		t.Fatalf("released head = %+v", released)
	}
	auditBudget := catalogTestBudget()
	auditBudget.Ranges = 1
	firstAudit, err := database.AuditCatalogLedgerPage(ctx, apptypes.InitialCatalogAuditCursor(), auditBudget)
	if err != nil || firstAudit.Done || firstAudit.Verified != 1 {
		t.Fatalf("first audit = %+v, %v", firstAudit, err)
	}
	secondAudit, err := database.AuditCatalogLedgerPage(ctx, firstAudit.Next, auditBudget)
	if err != nil || !secondAudit.Done || secondAudit.Next.Epoch != 2 {
		t.Fatalf("second audit = %+v, %v", secondAudit, err)
	}
	if _, err = raw.Exec(`UPDATE archive_catalog_head SET current_epoch=3,ledger_digest=? WHERE singleton=1`, "abababababababababababababababababababababababababababababababab"); err != nil {
		t.Fatal(err)
	}
	if _, err = database.AuditCatalogLedgerPage(ctx, secondAudit.Next, auditBudget); !errors.Is(err, apptypes.ErrCatalogDrift) {
		t.Fatalf("missing audit tail error = %v", err)
	}
	var reserve, release, abandoned int
	if err = raw.QueryRow(`SELECT sum(delta='reserve'),sum(delta='release'),sum(delta='abandoned') FROM archive_catalog_reservation_deltas`).Scan(&reserve, &release, &abandoned); err != nil {
		t.Fatal(err)
	}
	if reserve != 1 || release != 1 || abandoned != 0 {
		t.Fatalf("deltas = %d/%d/%d", reserve, release, abandoned)
	}
}

func TestCatalogSQLRejectsProofBearingGenericTransition(t *testing.T) {
	ctx := context.Background()
	database, raw := newActivatedCatalogStore(t, 1)
	defer func() { _ = raw.Close() }()
	initial, _ := database.CurrentCatalogRanges(ctx, catalogTestBudget())
	evidence := "9999999999999999999999999999999999999999999999999999999999999999"
	head, err := database.ReserveCatalogTarget(ctx, apptypes.CatalogReservation{ExpectedHead: initial.Head, ReservationID: "proof", Range: domain.CatalogRange{Start: 1, End: 1}, EvidenceDigest: evidence, Budget: catalogTestBudget()})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = raw.Exec(`INSERT INTO archive_catalog_range_transitions(epoch,transition_index,start_sequence,end_sequence,from_state,to_state,reservation_id,segment_id) VALUES(?,1,1,1,'reserved','sealed','proof','segment')`, head.Epoch); err == nil {
		t.Fatal("proof-bearing generic transition succeeded")
	}
}

func TestCatalogExpectedHeadCASAndGapFailClosed(t *testing.T) {
	ctx := context.Background()
	database, raw := newActivatedCatalogStore(t, 3)
	defer func() { _ = raw.Close() }()
	initial, _ := database.CurrentCatalogRanges(ctx, catalogTestBudget())
	evidence := "2222222222222222222222222222222222222222222222222222222222222222"
	head, err := database.ReserveCatalogTarget(ctx, apptypes.CatalogReservation{ExpectedHead: initial.Head, ReservationID: "one", Range: domain.CatalogRange{Start: 1, End: 1}, EvidenceDigest: evidence, Budget: catalogTestBudget()})
	if err != nil {
		t.Fatal(err)
	}
	_, err = database.ReserveCatalogTarget(ctx, apptypes.CatalogReservation{ExpectedHead: initial.Head, ReservationID: "stale", Range: domain.CatalogRange{Start: 2, End: 2}, EvidenceDigest: evidence, Budget: catalogTestBudget()})
	if !errors.Is(err, apptypes.ErrCatalogStaleHead) {
		t.Fatalf("stale error = %v", err)
	}
	_, err = database.ReserveCatalogTarget(ctx, apptypes.CatalogReservation{ExpectedHead: head, ReservationID: "gap", Range: domain.CatalogRange{Start: 4, End: 4}, EvidenceDigest: evidence, Budget: catalogTestBudget()})
	if !errors.Is(err, apptypes.ErrCatalogGap) {
		t.Fatalf("gap error = %v", err)
	}
}

func TestCatalogCurrentCacheRebuildIsDeterministicAndDoesNotReadBindings(t *testing.T) {
	ctx := context.Background()
	database, raw := newActivatedCatalogStore(t, 4)
	defer func() { _ = raw.Close() }()
	initial, _ := database.CurrentCatalogRanges(ctx, catalogTestBudget())
	evidence := "3333333333333333333333333333333333333333333333333333333333333333"
	head, err := database.ReserveCatalogTarget(ctx, apptypes.CatalogReservation{ExpectedHead: initial.Head, ReservationID: "one", Range: domain.CatalogRange{Start: 1, End: 2}, EvidenceDigest: evidence, Budget: catalogTestBudget()})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = raw.Exec(`UPDATE archive_catalog_current_ranges SET placement_state='hot',reservation_id='' WHERE start_sequence=1`); err != nil {
		t.Fatal(err)
	}
	if _, err = database.CurrentCatalogRanges(ctx, catalogTestBudget()); !errors.Is(err, apptypes.ErrCatalogDrift) {
		t.Fatalf("drift error = %v", err)
	}
	result, err := database.RebuildCatalogCurrentRanges(ctx, head, catalogTestBudget())
	if err != nil {
		t.Fatal(err)
	}
	if !result.Changed {
		t.Fatal("rebuild did not report drift repair")
	}
	first, err := database.CurrentCatalogRanges(ctx, catalogTestBudget())
	if err != nil {
		t.Fatal(err)
	}
	second, err := database.RebuildCatalogCurrentRanges(ctx, first.Head, catalogTestBudget())
	if err != nil {
		t.Fatal(err)
	}
	if second.Changed || second.Head.CurrentRangesDigest != first.Head.CurrentRangesDigest {
		t.Fatalf("second rebuild = %+v", second)
	}
}

func TestCatalogLedgerDriftFailsClosed(t *testing.T) {
	ctx := context.Background()
	database, raw := newActivatedCatalogStore(t, 2)
	defer func() { _ = raw.Close() }()
	initial, _ := database.CurrentCatalogRanges(ctx, catalogTestBudget())
	evidence := "5555555555555555555555555555555555555555555555555555555555555555"
	if _, err := database.ReserveCatalogTarget(ctx, apptypes.CatalogReservation{ExpectedHead: initial.Head, ReservationID: "one", Range: domain.CatalogRange{Start: 1, End: 1}, EvidenceDigest: evidence, Budget: catalogTestBudget()}); err != nil {
		t.Fatal(err)
	}
	if _, err := raw.Exec(`DROP TRIGGER archive_catalog_epochs_no_update`); err != nil {
		t.Fatal(err)
	}
	if _, err := raw.Exec(`UPDATE archive_catalog_epochs SET evidence_digest=? WHERE epoch=1`, "6666666666666666666666666666666666666666666666666666666666666666"); err != nil {
		t.Fatal(err)
	}
	if _, err := database.CurrentCatalogRanges(ctx, catalogTestBudget()); !errors.Is(err, apptypes.ErrCatalogDrift) {
		t.Fatalf("drift error = %v", err)
	}
}

func TestCatalogBoundaryCacheLossFailsClosed(t *testing.T) {
	ctx := context.Background()
	database, raw := newActivatedCatalogStore(t, 4)
	defer func() { _ = raw.Close() }()
	initial, _ := database.CurrentCatalogRanges(ctx, catalogTestBudget())
	evidence := "cdcdcdcdcdcdcdcdcdcdcdcdcdcdcdcdcdcdcdcdcdcdcdcdcdcdcdcdcdcdcdcd"
	if _, err := database.ReserveCatalogTarget(ctx, apptypes.CatalogReservation{ExpectedHead: initial.Head, ReservationID: "boundary", Range: domain.CatalogRange{Start: 2, End: 3}, EvidenceDigest: evidence, Budget: catalogTestBudget()}); err != nil {
		t.Fatal(err)
	}
	if _, err := raw.Exec(`DROP TRIGGER archive_catalog_boundaries_no_delete`); err != nil {
		t.Fatal(err)
	}
	if _, err := raw.Exec(`DELETE FROM archive_catalog_boundaries WHERE sequence=2`); err != nil {
		t.Fatal(err)
	}
	if _, err := database.CurrentCatalogRanges(ctx, catalogTestBudget()); !errors.Is(err, apptypes.ErrCatalogDrift) {
		t.Fatalf("boundary drift error = %v", err)
	}
}

func TestCatalogSegmentBindingIsImmutableAndCannotCreateAuthority(t *testing.T) {
	ctx := context.Background()
	database, raw := newActivatedCatalogStore(t, 2)
	defer func() { _ = raw.Close() }()
	initial, _ := database.CurrentCatalogRanges(ctx, catalogTestBudget())
	evidence := "4444444444444444444444444444444444444444444444444444444444444444"
	head, err := database.ReserveCatalogTarget(ctx, apptypes.CatalogReservation{ExpectedHead: initial.Head, ReservationID: "one", Range: domain.CatalogRange{Start: 1, End: 1}, EvidenceDigest: evidence, Budget: catalogTestBudget()})
	if err != nil {
		t.Fatal(err)
	}
	var storeID string
	if err = raw.QueryRow(`SELECT store_id FROM archive_store_lineage WHERE singleton=1`).Scan(&storeID); err != nil {
		t.Fatal(err)
	}
	tx, err := raw.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	logical, fileDigest, manifestDigest, summaryDigest := [32]byte{0xaa}, [32]byte{0xbb}, [32]byte{0xcc}, [32]byte{0xdd}
	identity, err := domain.NewSegmentIdentity(storeID, 1, 1, logical)
	if err != nil {
		t.Fatal(err)
	}
	binding, err := domain.NewCatalogSegmentBinding(identity, fileDigest, manifestDigest, summaryDigest)
	if err != nil {
		t.Fatal(err)
	}
	if err = bindCatalogSegment(ctx, tx, head.Epoch, binding); err != nil {
		t.Fatal(err)
	}
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}
	mismatchTx, err := raw.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	mismatchedSummary := [32]byte{0xee}
	mismatched, err := domain.NewCatalogSegmentBinding(identity, fileDigest, manifestDigest, mismatchedSummary)
	if err != nil {
		t.Fatal(err)
	}
	if err = bindCatalogSegment(ctx, mismatchTx, head.Epoch, mismatched); !errors.Is(err, apptypes.ErrCatalogBindingMismatch) {
		_ = mismatchTx.Rollback()
		t.Fatalf("binding mismatch error = %v", err)
	}
	_ = mismatchTx.Rollback()
	if _, err = raw.Exec(`UPDATE archive_catalog_segment_bindings SET summary_digest=? WHERE segment_id=?`, evidence, binding.SegmentID()); err == nil {
		t.Fatal("immutable binding update succeeded")
	}
	snapshot, err := database.CurrentCatalogRanges(ctx, catalogTestBudget())
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.Ranges[0].Placement != domain.CatalogPlacementReserved {
		t.Fatalf("binding invented authority: %+v", snapshot.Ranges)
	}
}
