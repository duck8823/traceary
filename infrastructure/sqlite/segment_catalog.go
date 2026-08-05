package sqlite

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/duck8823/traceary/application"
	apptypes "github.com/duck8823/traceary/application/types"
	"github.com/duck8823/traceary/domain"
)

const emptyCatalogDigestValue = "0000000000000000000000000000000000000000000000000000000000000000"

var (
	_ application.CatalogInventoryGateReader    = (*Database)(nil)
	_ application.CatalogTargetReservationStore = (*Database)(nil)
	_ application.CatalogRangeReader            = (*Database)(nil)
	_ application.CatalogCurrentRebuilder       = (*Database)(nil)
)

type catalogEpochCommit struct {
	expected       apptypes.CatalogHead
	highWater      int64
	transitions    []domain.CatalogTransition
	evidenceDigest string
	reservationID  string
	delta          string
	// boundaryPointLimit is a test-only lower hard-cap seam. Zero selects the
	// production CatalogMaxBoundaryPoints constant.
	boundaryPointLimit int
}

func boundedCatalogContext(parent context.Context, budget apptypes.CatalogBudget) (context.Context, context.CancelFunc) {
	timeout := budget.WallTime
	if budget.LockTime < timeout {
		timeout = budget.LockTime
	}
	return context.WithTimeout(parent, timeout)
}

//nolint:wrapcheck // This dedicated SQLite adapter preserves typed SQL failures.
func readCatalogHead(ctx context.Context, q interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}) (apptypes.CatalogHead, error) {
	var head apptypes.CatalogHead
	err := q.QueryRowContext(ctx, `SELECT current_epoch,ledger_digest,current_ranges_digest FROM archive_catalog_head WHERE singleton=1`).Scan(&head.Epoch, &head.LedgerDigest, &head.CurrentRangesDigest)
	return head, err
}

// ReserveCatalogTarget appends a Hot-to-reserved transition after proving the
// sequence inventory is active and the expected Catalog head is current.
//
//nolint:wrapcheck // This dedicated SQLite adapter preserves typed SQL failures.
func (d *Database) ReserveCatalogTarget(ctx context.Context, command apptypes.CatalogReservation) (apptypes.CatalogHead, error) {
	command.ReservationID = strings.TrimSpace(command.ReservationID)
	if !command.Budget.Valid() || command.ReservationID == "" || len(command.ReservationID) > 255 || !domain.ValidCatalogDigest(command.EvidenceDigest) {
		return apptypes.CatalogHead{}, apptypes.ErrCatalogLimit
	}
	transition, err := domain.ReservationTransition(command.Range, command.ReservationID)
	if err != nil {
		return apptypes.CatalogHead{}, apptypes.ErrCatalogIllegalTransition
	}
	opCtx, cancel := boundedCatalogContext(ctx, command.Budget)
	defer cancel()
	db, err := d.open(opCtx)
	if err != nil {
		return apptypes.CatalogHead{}, err
	}
	defer d.release(db)
	tx, err := db.BeginTx(opCtx, nil)
	if err != nil {
		return apptypes.CatalogHead{}, err
	}
	defer func() { _ = tx.Rollback() }()
	highWater, err := checkCatalogInventoryGate(opCtx, tx)
	if err != nil {
		return apptypes.CatalogHead{}, err
	}
	if command.Range.End > highWater {
		return apptypes.CatalogHead{}, apptypes.ErrCatalogGap
	}
	actual, err := readCatalogHead(opCtx, tx)
	if err != nil {
		return apptypes.CatalogHead{}, err
	}
	reserveRange, reserved, released, reserveEvidence, _, err := catalogReservationHistory(opCtx, tx, command.ReservationID)
	if err != nil {
		return apptypes.CatalogHead{}, err
	}
	if reserved {
		if reserveRange != command.Range || released || reserveEvidence != command.EvidenceDigest {
			return apptypes.CatalogHead{}, apptypes.ErrCatalogReservationConflict
		}
		if err = verifyCatalogHeadIncremental(opCtx, tx, actual); err != nil {
			return apptypes.CatalogHead{}, err
		}
		return actual, nil
	}
	head, err := commitCatalogEpoch(opCtx, tx, catalogEpochCommit{expected: command.ExpectedHead, highWater: highWater, transitions: []domain.CatalogTransition{transition}, evidenceDigest: command.EvidenceDigest, reservationID: command.ReservationID, delta: "reserve"}, command.Budget.Ranges)
	if err != nil {
		return apptypes.CatalogHead{}, err
	}
	if err = tx.Commit(); err != nil {
		return apptypes.CatalogHead{}, err
	}
	return head, nil
}

// ReleaseCatalogReservation appends the inverse delta for the exact active
// reservation. History is retained; placement returns to Hot.
//
//nolint:wrapcheck // This dedicated SQLite adapter preserves typed SQL failures.
func (d *Database) ReleaseCatalogReservation(ctx context.Context, command apptypes.CatalogRelease) (apptypes.CatalogHead, error) {
	command.ReservationID = strings.TrimSpace(command.ReservationID)
	if !command.Budget.Valid() || command.ReservationID == "" || !domain.ValidCatalogDigest(command.EvidenceDigest) {
		return apptypes.CatalogHead{}, apptypes.ErrCatalogLimit
	}
	opCtx, cancel := boundedCatalogContext(ctx, command.Budget)
	defer cancel()
	db, err := d.open(opCtx)
	if err != nil {
		return apptypes.CatalogHead{}, err
	}
	defer d.release(db)
	tx, err := db.BeginTx(opCtx, nil)
	if err != nil {
		return apptypes.CatalogHead{}, err
	}
	defer func() { _ = tx.Rollback() }()
	highWater, err := checkCatalogInventoryGate(opCtx, tx)
	if err != nil {
		return apptypes.CatalogHead{}, err
	}
	actual, err := readCatalogHead(opCtx, tx)
	if err != nil {
		return apptypes.CatalogHead{}, err
	}
	reserveRange, reserved, released, _, releaseEvidence, err := catalogReservationHistory(opCtx, tx, command.ReservationID)
	if err != nil {
		return apptypes.CatalogHead{}, err
	}
	if !reserved {
		return apptypes.CatalogHead{}, apptypes.ErrCatalogNotFound
	}
	if released {
		if releaseEvidence != command.EvidenceDigest {
			return apptypes.CatalogHead{}, apptypes.ErrCatalogReservationConflict
		}
		if err = verifyCatalogHeadIncremental(opCtx, tx, actual); err != nil {
			return apptypes.CatalogHead{}, err
		}
		return actual, nil
	}
	replayHighWater := highWater
	if actual.Epoch > 0 {
		if err = tx.QueryRowContext(opCtx, `SELECT source_high_water FROM archive_catalog_epochs WHERE epoch=?`, actual.Epoch).Scan(&replayHighWater); err != nil {
			return apptypes.CatalogHead{}, apptypes.ErrCatalogDrift
		}
	}
	ranges, err := replayCatalogRanges(opCtx, tx, actual.Epoch, replayHighWater, command.Budget.Ranges)
	if err != nil {
		return apptypes.CatalogHead{}, err
	}
	var found *apptypes.CatalogCurrentRange
	for i := range ranges {
		if ranges[i].Placement == domain.CatalogPlacementReserved && ranges[i].ReservationID == command.ReservationID {
			if found != nil {
				return apptypes.CatalogHead{}, apptypes.ErrCatalogDrift
			}
			found = &ranges[i]
		}
	}
	if found == nil {
		return apptypes.CatalogHead{}, apptypes.ErrCatalogDrift
	}
	if found.Range != reserveRange {
		return apptypes.CatalogHead{}, apptypes.ErrCatalogDrift
	}
	transition, err := domain.ReleaseReservationTransition(found.Range, command.ReservationID)
	if err != nil {
		return apptypes.CatalogHead{}, apptypes.ErrCatalogIllegalTransition
	}
	head, err := commitCatalogEpoch(opCtx, tx, catalogEpochCommit{expected: command.ExpectedHead, highWater: highWater, transitions: []domain.CatalogTransition{transition}, evidenceDigest: command.EvidenceDigest, reservationID: command.ReservationID, delta: "release"}, command.Budget.Ranges)
	if err != nil {
		return apptypes.CatalogHead{}, err
	}
	if err = tx.Commit(); err != nil {
		return apptypes.CatalogHead{}, err
	}
	return head, nil
}

//nolint:wrapcheck // This dedicated SQLite adapter preserves typed SQL failures.
func catalogReservationHistory(ctx context.Context, q interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
}, reservationID string) (domain.CatalogRange, bool, bool, string, string, error) {
	rows, err := q.QueryContext(ctx, `SELECT d.delta,d.start_sequence,d.end_sequence,e.evidence_digest FROM archive_catalog_reservation_deltas d JOIN archive_catalog_epochs e ON e.epoch=d.epoch WHERE d.reservation_id=? ORDER BY d.epoch`, reservationID)
	if err != nil {
		return domain.CatalogRange{}, false, false, "", "", err
	}
	defer func() { _ = rows.Close() }()
	var result domain.CatalogRange
	var reserved, released bool
	var reserveEvidence, releaseEvidence string
	for rows.Next() {
		var delta, evidence string
		var current domain.CatalogRange
		if err = rows.Scan(&delta, &current.Start, &current.End, &evidence); err != nil {
			return domain.CatalogRange{}, false, false, "", "", err
		}
		switch delta {
		case "reserve":
			if reserved {
				return domain.CatalogRange{}, false, false, "", "", apptypes.ErrCatalogDrift
			}
			result = current
			reserveEvidence = evidence
			reserved = true
		case "release":
			if !reserved || released || current != result {
				return domain.CatalogRange{}, false, false, "", "", apptypes.ErrCatalogDrift
			}
			releaseEvidence = evidence
			released = true
		default:
			return domain.CatalogRange{}, false, false, "", "", apptypes.ErrCatalogDrift
		}
	}
	return result, reserved, released, reserveEvidence, releaseEvidence, rows.Err()
}

//nolint:wrapcheck // This dedicated SQLite adapter preserves typed SQL failures.
func checkCatalogInventoryGate(ctx context.Context, q interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}) (int64, error) {
	var activeID, generationID, phase string
	var verified, next int64
	err := q.QueryRowContext(ctx, `SELECT a.active_generation_id,a.verified_high_water,s.generation_id,s.phase,x.next_sequence FROM archive_sequence_activation a JOIN archive_sequence_inventory_state s ON s.singleton=1 JOIN archive_sequence_allocator x ON x.singleton=1 WHERE a.singleton=1`).Scan(&activeID, &verified, &generationID, &phase, &next)
	if err != nil {
		return 0, err
	}
	if activeID == "" || activeID != generationID || phase != "complete" || verified <= 0 || next <= verified || next <= 1 {
		return 0, apptypes.ErrCatalogInventoryGate
	}
	var mapped, events int64
	if err = q.QueryRowContext(ctx, `SELECT (SELECT count(*) FROM archive_event_sequences),(SELECT count(*) FROM events)`).Scan(&mapped, &events); err != nil {
		return 0, err
	}
	if mapped != events || mapped != next-1 {
		return 0, apptypes.ErrCatalogInventoryGate
	}
	return next - 1, nil
}

// CatalogInventoryGate returns aggregate sequence readiness and current head.
//
//nolint:wrapcheck // This dedicated SQLite adapter preserves typed SQL failures.
func (d *Database) CatalogInventoryGate(ctx context.Context, budget apptypes.CatalogBudget) (apptypes.CatalogInventoryGate, error) {
	if !budget.Valid() {
		return apptypes.CatalogInventoryGate{}, apptypes.ErrCatalogLimit
	}
	opCtx, cancel := context.WithTimeout(ctx, budget.WallTime)
	defer cancel()
	db, err := d.openReadOnly(opCtx)
	if err != nil {
		return apptypes.CatalogInventoryGate{}, err
	}
	defer d.release(db)
	highWater, err := checkCatalogInventoryGate(opCtx, db)
	if err != nil {
		return apptypes.CatalogInventoryGate{}, err
	}
	head, err := readCatalogHead(opCtx, db)
	if err != nil {
		return apptypes.CatalogInventoryGate{}, err
	}
	var storeID string
	if err = db.QueryRowContext(opCtx, `SELECT store_id FROM archive_store_lineage WHERE singleton=1`).Scan(&storeID); err != nil {
		return apptypes.CatalogInventoryGate{}, err
	}
	return apptypes.CatalogInventoryGate{StoreID: storeID, HighWater: highWater, Head: head}, nil
}

//nolint:wrapcheck // This dedicated SQLite adapter preserves typed SQL failures.
func commitCatalogEpoch(ctx context.Context, tx *sql.Tx, commit catalogEpochCommit, maxRanges int) (apptypes.CatalogHead, error) {
	actual, err := readCatalogHead(ctx, tx)
	if err != nil {
		return apptypes.CatalogHead{}, err
	}
	if actual != commit.expected {
		return apptypes.CatalogHead{}, apptypes.ErrCatalogStaleHead
	}
	if err = verifyCatalogHeadIncremental(ctx, tx, actual); err != nil {
		return apptypes.CatalogHead{}, err
	}
	if !domain.ValidCatalogDigest(commit.evidenceDigest) || commit.highWater <= 0 {
		return apptypes.CatalogHead{}, apptypes.ErrCatalogDrift
	}
	transitionDigest, err := domain.CanonicalCatalogTransitionDigest(commit.transitions)
	if err != nil {
		return apptypes.CatalogHead{}, apptypes.ErrCatalogOverlap
	}
	previousHighWater := commit.highWater
	if actual.Epoch > 0 {
		if err = tx.QueryRowContext(ctx, `SELECT source_high_water FROM archive_catalog_epochs WHERE epoch=?`, actual.Epoch).Scan(&previousHighWater); err != nil {
			return apptypes.CatalogHead{}, apptypes.ErrCatalogDrift
		}
		if previousHighWater > commit.highWater {
			return apptypes.CatalogHead{}, apptypes.ErrCatalogDrift
		}
	}
	current, err := replayCatalogRanges(ctx, tx, actual.Epoch, previousHighWater, maxRanges)
	if err != nil {
		return apptypes.CatalogHead{}, err
	}
	tailWasHot := current[len(current)-1].Placement == domain.CatalogPlacementHot
	if commit.highWater > previousHighWater {
		tail := &current[len(current)-1]
		if tail.Placement == domain.CatalogPlacementHot {
			tail.Range.End = commit.highWater
		} else {
			current = append(current, apptypes.CatalogCurrentRange{Range: domain.CatalogRange{Start: previousHighWater + 1, End: commit.highWater}, Placement: domain.CatalogPlacementHot})
		}
	}
	for _, transition := range commit.transitions {
		current, err = applyCatalogTransition(current, transition)
		if err != nil {
			return apptypes.CatalogHead{}, err
		}
	}
	current = coalesceCatalogRanges(current)
	if len(current) > maxRanges {
		return apptypes.CatalogHead{}, apptypes.ErrCatalogLimit
	}
	newEpoch := actual.Epoch + 1
	boundaries, err := catalogBoundarySet(ctx, tx, actual.Epoch, previousHighWater)
	if err != nil {
		return apptypes.CatalogHead{}, err
	}
	if commit.highWater > previousHighWater && !tailWasHot {
		boundaries = append(boundaries, previousHighWater+1)
	}
	for _, transition := range commit.transitions {
		boundaries = append(boundaries, transition.Range.Start)
		if transition.Range.End < commit.highWater {
			boundaries = append(boundaries, transition.Range.End+1)
		}
	}
	boundaries = uniqueSortedBoundaries(boundaries)
	boundaryPointLimit := commit.boundaryPointLimit
	if boundaryPointLimit == 0 {
		boundaryPointLimit = apptypes.CatalogMaxBoundaryPoints
	}
	if len(boundaries) > boundaryPointLimit {
		return apptypes.CatalogHead{}, apptypes.ErrCatalogLimit
	}
	boundaryDigest, err := domain.CanonicalCatalogBoundaryDigest(boundaries)
	if err != nil {
		return apptypes.CatalogHead{}, apptypes.ErrCatalogDrift
	}
	ledgerDigest, err := domain.CatalogLedgerDigest(actual.LedgerDigest, newEpoch, actual.Epoch, commit.highWater, int64(len(boundaries)), transitionDigest, boundaryDigest, commit.evidenceDigest)
	if err != nil {
		return apptypes.CatalogHead{}, apptypes.ErrCatalogDrift
	}
	currentDigest := digestCatalogRanges(current)
	result, err := tx.ExecContext(ctx, `UPDATE archive_catalog_head SET current_epoch=?,ledger_digest=?,current_ranges_digest=? WHERE singleton=1 AND current_epoch=? AND ledger_digest=? AND current_ranges_digest=?`, newEpoch, ledgerDigest, currentDigest, actual.Epoch, actual.LedgerDigest, actual.CurrentRangesDigest)
	if err != nil {
		return apptypes.CatalogHead{}, err
	}
	changed, err := result.RowsAffected()
	if err != nil || changed != 1 {
		return apptypes.CatalogHead{}, apptypes.ErrCatalogStaleHead
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	if _, err = tx.ExecContext(ctx, `INSERT INTO archive_catalog_epochs(epoch,parent_epoch,transition_digest,evidence_digest,parent_ledger_digest,source_high_water,boundary_count,boundary_digest,ledger_digest,committed_at) VALUES(?,?,?,?,?,?,?,?,?,?)`, newEpoch, actual.Epoch, transitionDigest, commit.evidenceDigest, actual.LedgerDigest, commit.highWater, len(boundaries), boundaryDigest, ledgerDigest, now); err != nil {
		return apptypes.CatalogHead{}, err
	}
	if commit.highWater > previousHighWater && !tailWasHot {
		if _, err = tx.ExecContext(ctx, `INSERT INTO archive_catalog_boundaries(sequence,first_epoch) VALUES(?,?) ON CONFLICT(sequence) DO NOTHING`, previousHighWater+1, newEpoch); err != nil {
			return apptypes.CatalogHead{}, err
		}
	}
	for index, transition := range commit.transitions {
		if _, err = tx.ExecContext(ctx, `INSERT INTO archive_catalog_range_transitions(epoch,transition_index,start_sequence,end_sequence,from_state,to_state,reservation_id,segment_id) VALUES(?,?,?,?,?,?,?,?)`, newEpoch, index, transition.Range.Start, transition.Range.End, transition.From, transition.To, transition.ReservationID, transition.SegmentID); err != nil {
			return apptypes.CatalogHead{}, err
		}
		if _, err = tx.ExecContext(ctx, `INSERT INTO archive_catalog_boundaries(sequence,first_epoch) VALUES(?,?) ON CONFLICT(sequence) DO NOTHING`, transition.Range.Start, newEpoch); err != nil {
			return apptypes.CatalogHead{}, err
		}
		if transition.Range.End < commit.highWater {
			if _, err = tx.ExecContext(ctx, `INSERT INTO archive_catalog_boundaries(sequence,first_epoch) VALUES(?,?) ON CONFLICT(sequence) DO NOTHING`, transition.Range.End+1, newEpoch); err != nil {
				return apptypes.CatalogHead{}, err
			}
		}
	}
	if commit.delta != "reserve" && commit.delta != "release" {
		return apptypes.CatalogHead{}, apptypes.ErrCatalogIllegalTransition
	}
	transition := commit.transitions[0]
	if _, err = tx.ExecContext(ctx, `INSERT INTO archive_catalog_reservation_deltas(epoch,reservation_id,delta,start_sequence,end_sequence) VALUES(?,?,?,?,?)`, newEpoch, commit.reservationID, commit.delta, transition.Range.Start, transition.Range.End); err != nil {
		return apptypes.CatalogHead{}, err
	}
	if err = replaceCatalogCurrent(ctx, tx, current); err != nil {
		return apptypes.CatalogHead{}, err
	}
	return apptypes.CatalogHead{Epoch: newEpoch, LedgerDigest: ledgerDigest, CurrentRangesDigest: currentDigest}, nil
}

// verifyCatalogHeadIncremental checks only the immutable head epoch, its
// parent link, and that epoch's bounded canonical transition set. It does not
// make ordinary reads or commits proportional to the lifetime epoch count.
//
//nolint:wrapcheck // This dedicated SQLite adapter preserves typed SQL failures.
func verifyCatalogHeadIncremental(ctx context.Context, q interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
}, head apptypes.CatalogHead) error {
	if head.Epoch == 0 {
		if head.LedgerDigest != emptyCatalogDigestValue {
			return apptypes.ErrCatalogDrift
		}
		return nil
	}
	var parent, highWater, boundaryCount int64
	var transitionDigest, boundaryDigest, evidenceDigest, parentDigest, ledgerDigest string
	if err := q.QueryRowContext(ctx, `SELECT parent_epoch,source_high_water,boundary_count,boundary_digest,transition_digest,evidence_digest,parent_ledger_digest,ledger_digest FROM archive_catalog_epochs WHERE epoch=?`, head.Epoch).Scan(&parent, &highWater, &boundaryCount, &boundaryDigest, &transitionDigest, &evidenceDigest, &parentDigest, &ledgerDigest); err != nil {
		return apptypes.ErrCatalogDrift
	}
	if ledgerDigest != head.LedgerDigest || parent != head.Epoch-1 {
		return apptypes.ErrCatalogDrift
	}
	if parent == 0 {
		if parentDigest != emptyCatalogDigestValue {
			return apptypes.ErrCatalogDrift
		}
	} else {
		var storedParent string
		if err := q.QueryRowContext(ctx, `SELECT ledger_digest FROM archive_catalog_epochs WHERE epoch=?`, parent).Scan(&storedParent); err != nil || storedParent != parentDigest {
			return apptypes.ErrCatalogDrift
		}
	}
	rows, err := q.QueryContext(ctx, `SELECT start_sequence,end_sequence,from_state,to_state,reservation_id,segment_id FROM archive_catalog_range_transitions WHERE epoch=? ORDER BY transition_index LIMIT ?`, head.Epoch, apptypes.CatalogMaxTransitionsPerEpoch+1)
	if err != nil {
		return err
	}
	defer func() { _ = rows.Close() }()
	var transitions []domain.CatalogTransition
	for rows.Next() {
		if len(transitions) >= apptypes.CatalogMaxTransitionsPerEpoch {
			return apptypes.ErrCatalogLimit
		}
		var start, end int64
		var from, to domain.CatalogPlacement
		var reservationID, segmentID string
		if err = rows.Scan(&start, &end, &from, &to, &reservationID, &segmentID); err != nil {
			return err
		}
		transition, validationErr := domain.NewCatalogTransition(domain.CatalogRange{Start: start, End: end}, from, to, reservationID, segmentID)
		if validationErr != nil {
			return apptypes.ErrCatalogDrift
		}
		transitions = append(transitions, transition)
	}
	if err = rows.Err(); err != nil {
		return err
	}
	digest, digestErr := domain.CanonicalCatalogTransitionDigest(transitions)
	if digestErr != nil || digest != transitionDigest {
		return apptypes.ErrCatalogDrift
	}
	boundaries, boundaryErr := catalogBoundarySet(ctx, q, head.Epoch, highWater)
	if boundaryErr != nil {
		return boundaryErr
	}
	observedBoundaryDigest, digestBoundaryErr := domain.CanonicalCatalogBoundaryDigest(boundaries)
	if digestBoundaryErr != nil || boundaryCount != int64(len(boundaries)) || boundaryDigest != observedBoundaryDigest {
		return apptypes.ErrCatalogDrift
	}
	want, chainErr := domain.CatalogLedgerDigest(parentDigest, head.Epoch, parent, highWater, boundaryCount, transitionDigest, boundaryDigest, evidenceDigest)
	if chainErr != nil || want != ledgerDigest {
		return apptypes.ErrCatalogDrift
	}
	return nil
}

func applyCatalogTransition(ranges []apptypes.CatalogCurrentRange, transition domain.CatalogTransition) ([]apptypes.CatalogCurrentRange, error) {
	result := make([]apptypes.CatalogCurrentRange, 0, len(ranges)+2)
	covered := int64(0)
	for _, current := range ranges {
		if !current.Range.Overlaps(transition.Range) {
			result = append(result, current)
			continue
		}
		start := max64(current.Range.Start, transition.Range.Start)
		end := min64(current.Range.End, transition.Range.End)
		covered += end - start + 1
		if current.Placement != transition.From {
			if current.Placement == domain.CatalogPlacementReserved {
				return nil, apptypes.ErrCatalogOverlap
			}
			return nil, apptypes.ErrCatalogIllegalTransition
		}
		if current.Range.Start < start {
			left := current
			left.Range.End = start - 1
			result = append(result, left)
		}
		changed := apptypes.CatalogCurrentRange{Range: domain.CatalogRange{Start: start, End: end}, Placement: transition.To, ReservationID: transition.ReservationID, SegmentID: transition.SegmentID}
		if transition.To == domain.CatalogPlacementHot {
			changed.ReservationID = ""
			changed.SegmentID = ""
		}
		result = append(result, changed)
		if end < current.Range.End {
			right := current
			right.Range.Start = end + 1
			result = append(result, right)
		}
	}
	if covered != transition.Range.End-transition.Range.Start+1 {
		return nil, apptypes.ErrCatalogGap
	}
	sort.Slice(result, func(i, j int) bool { return result[i].Range.Start < result[j].Range.Start })
	return result, nil
}

//nolint:wrapcheck // This dedicated SQLite adapter preserves typed SQL failures.
func replayCatalogRanges(ctx context.Context, q interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
	QueryRowContext(context.Context, string, ...any) *sql.Row
}, epoch, highWater int64, maxRanges int) ([]apptypes.CatalogCurrentRange, error) {
	if highWater <= 0 || maxRanges <= 0 {
		return nil, apptypes.ErrCatalogLimit
	}
	boundaries, err := catalogBoundarySet(ctx, q, epoch, highWater)
	if err != nil {
		return nil, err
	}
	if epoch > 0 {
		var expectedCount int64
		var expectedDigest string
		if err = q.QueryRowContext(ctx, `SELECT boundary_count,boundary_digest FROM archive_catalog_epochs WHERE epoch=?`, epoch).Scan(&expectedCount, &expectedDigest); err != nil {
			return nil, apptypes.ErrCatalogDrift
		}
		digest, digestErr := domain.CanonicalCatalogBoundaryDigest(boundaries)
		if digestErr != nil || expectedCount != int64(len(boundaries)) || expectedDigest != digest {
			return nil, apptypes.ErrCatalogDrift
		}
	}
	boundaries = append(boundaries, highWater+1)
	result := make([]apptypes.CatalogCurrentRange, 0, len(boundaries)-1)
	for index := 0; index < len(boundaries)-1; index++ {
		start, end := boundaries[index], boundaries[index+1]-1
		if start > highWater {
			break
		}
		if end > highWater {
			end = highWater
		}
		item := apptypes.CatalogCurrentRange{Range: domain.CatalogRange{Start: start, End: end}, Placement: domain.CatalogPlacementHot}
		var from domain.CatalogPlacement
		err = q.QueryRowContext(ctx, `SELECT epoch,from_state,to_state,reservation_id,segment_id FROM archive_catalog_range_transitions WHERE epoch<=? AND start_sequence<=? AND end_sequence>=? ORDER BY epoch DESC,transition_index DESC LIMIT 1`, epoch, start, start).Scan(&item.SourceEpoch, &from, &item.Placement, &item.ReservationID, &item.SegmentID)
		if errors.Is(err, sql.ErrNoRows) {
			item.SourceEpoch = 0
			item.Placement = domain.CatalogPlacementHot
			item.ReservationID = ""
			item.SegmentID = ""
		} else if err != nil {
			return nil, err
		}
		if item.Placement == domain.CatalogPlacementHot {
			item.ReservationID = ""
			item.SegmentID = ""
		}
		result = append(result, item)
	}
	result = coalesceCatalogRanges(result)
	if len(result) > maxRanges {
		return nil, apptypes.ErrCatalogLimit
	}
	return result, nil
}

//nolint:wrapcheck // This dedicated SQLite adapter preserves typed SQL failures.
func catalogBoundarySet(ctx context.Context, q interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
}, epoch, highWater int64) ([]int64, error) {
	rows, err := q.QueryContext(ctx, `SELECT sequence FROM archive_catalog_boundaries WHERE first_epoch<=? AND sequence<=? ORDER BY sequence LIMIT ?`, epoch, highWater, apptypes.CatalogMaxBoundaryPoints+1)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	boundaries := make([]int64, 0, min(apptypes.CatalogMaxBoundaryPoints, 1024))
	for rows.Next() {
		var boundary int64
		if err = rows.Scan(&boundary); err != nil {
			return nil, err
		}
		boundaries = append(boundaries, boundary)
	}
	if err = rows.Err(); err != nil {
		return nil, err
	}
	if len(boundaries) == 0 || boundaries[0] != 1 {
		return nil, apptypes.ErrCatalogDrift
	}
	if len(boundaries) > apptypes.CatalogMaxBoundaryPoints {
		return nil, apptypes.ErrCatalogLimit
	}
	return boundaries, nil
}

func uniqueSortedBoundaries(values []int64) []int64 {
	sort.Slice(values, func(i, j int) bool { return values[i] < values[j] })
	out := values[:0]
	for _, value := range values {
		if len(out) == 0 || out[len(out)-1] != value {
			out = append(out, value)
		}
	}
	return out
}

func coalesceCatalogRanges(ranges []apptypes.CatalogCurrentRange) []apptypes.CatalogCurrentRange {
	if len(ranges) < 2 {
		return ranges
	}
	out := []apptypes.CatalogCurrentRange{ranges[0]}
	for _, next := range ranges[1:] {
		last := &out[len(out)-1]
		if last.Range.End+1 == next.Range.Start && last.Placement == next.Placement && last.ReservationID == next.ReservationID && last.SegmentID == next.SegmentID {
			last.Range.End = next.Range.End
			if next.SourceEpoch > last.SourceEpoch {
				last.SourceEpoch = next.SourceEpoch
			}
		} else {
			out = append(out, next)
		}
	}
	return out
}

func digestCatalogRanges(ranges []apptypes.CatalogCurrentRange) string {
	h := sha256.New()
	write := func(v []byte) {
		var n [8]byte
		binary.BigEndian.PutUint64(n[:], uint64(len(v)))
		_, _ = h.Write(n[:])
		_, _ = h.Write(v)
	}
	write([]byte("traceary/catalog-current-ranges/v1"))
	for _, current := range ranges {
		var n [8]byte
		binary.BigEndian.PutUint64(n[:], uint64(current.Range.Start))
		write(n[:])
		binary.BigEndian.PutUint64(n[:], uint64(current.Range.End))
		write(n[:])
		write([]byte(current.Placement))
		write([]byte(current.ReservationID))
		write([]byte(current.SegmentID))
	}
	return hex.EncodeToString(h.Sum(nil))
}

//nolint:wrapcheck // This dedicated SQLite adapter preserves typed SQL failures.
func replaceCatalogCurrent(ctx context.Context, tx *sql.Tx, ranges []apptypes.CatalogCurrentRange) error {
	if _, err := tx.ExecContext(ctx, `DELETE FROM archive_catalog_current_ranges`); err != nil {
		return err
	}
	for _, current := range ranges {
		if _, err := tx.ExecContext(ctx, `INSERT INTO archive_catalog_current_ranges(start_sequence,end_sequence,placement_state,reservation_id,segment_id,source_epoch) VALUES(?,?,?,?,?,?)`, current.Range.Start, current.Range.End, current.Placement, current.ReservationID, current.SegmentID, current.SourceEpoch); err != nil {
			return err
		}
	}
	return nil
}

// CatalogRangesAtEpoch reconstructs a bounded, immutable source set. Its
// source high-water is read from that epoch, not from mutable allocator state.
//
//nolint:wrapcheck // This dedicated SQLite adapter preserves typed SQL failures.
func (d *Database) CatalogRangesAtEpoch(ctx context.Context, epoch int64, budget apptypes.CatalogBudget) (apptypes.CatalogSnapshot, error) {
	if !budget.Valid() || epoch <= 0 {
		return apptypes.CatalogSnapshot{}, apptypes.ErrCatalogLimit
	}
	opCtx, cancel := context.WithTimeout(ctx, budget.WallTime)
	defer cancel()
	db, err := d.openReadOnly(opCtx)
	if err != nil {
		return apptypes.CatalogSnapshot{}, err
	}
	defer d.release(db)
	head, err := readCatalogHead(opCtx, db)
	if err != nil {
		return apptypes.CatalogSnapshot{}, err
	}
	if epoch > head.Epoch {
		return apptypes.CatalogSnapshot{}, apptypes.ErrCatalogStaleHead
	}
	var highWater int64
	var ledger string
	if err = db.QueryRowContext(opCtx, `SELECT source_high_water,ledger_digest FROM archive_catalog_epochs WHERE epoch=?`, epoch).Scan(&highWater, &ledger); err != nil {
		return apptypes.CatalogSnapshot{}, err
	}
	historicalHead := apptypes.CatalogHead{Epoch: epoch, LedgerDigest: ledger}
	if err = verifyCatalogHeadIncremental(opCtx, db, historicalHead); err != nil {
		return apptypes.CatalogSnapshot{}, err
	}
	ranges, err := replayCatalogRanges(opCtx, db, epoch, highWater, budget.Ranges)
	if err != nil {
		return apptypes.CatalogSnapshot{}, err
	}
	return apptypes.CatalogSnapshot{Head: apptypes.CatalogHead{Epoch: epoch, LedgerDigest: ledger, CurrentRangesDigest: digestCatalogRanges(ranges)}, Ranges: ranges}, nil
}

// CurrentCatalogRanges fails closed when the derived cache differs from a
// deterministic ledger replay.
//
//nolint:wrapcheck // This dedicated SQLite adapter preserves typed SQL failures.
func (d *Database) CurrentCatalogRanges(ctx context.Context, budget apptypes.CatalogBudget) (apptypes.CatalogSnapshot, error) {
	if !budget.Valid() {
		return apptypes.CatalogSnapshot{}, apptypes.ErrCatalogLimit
	}
	opCtx, cancel := context.WithTimeout(ctx, budget.WallTime)
	defer cancel()
	db, err := d.openReadOnly(opCtx)
	if err != nil {
		return apptypes.CatalogSnapshot{}, err
	}
	defer d.release(db)
	head, err := readCatalogHead(opCtx, db)
	if err != nil {
		return apptypes.CatalogSnapshot{}, err
	}
	if head.Epoch == 0 {
		return apptypes.CatalogSnapshot{Head: head}, nil
	}
	if err = verifyCatalogHeadIncremental(opCtx, db, head); err != nil {
		return apptypes.CatalogSnapshot{}, err
	}
	var highWater int64
	if err = db.QueryRowContext(opCtx, `SELECT source_high_water FROM archive_catalog_epochs WHERE epoch=?`, head.Epoch).Scan(&highWater); err != nil {
		return apptypes.CatalogSnapshot{}, apptypes.ErrCatalogDrift
	}
	ranges, err := readCurrentCatalogRanges(opCtx, db, budget.Ranges)
	if err != nil {
		return apptypes.CatalogSnapshot{}, err
	}
	if digestCatalogRanges(ranges) != head.CurrentRangesDigest {
		return apptypes.CatalogSnapshot{}, apptypes.ErrCatalogDrift
	}
	replayed, err := replayCatalogRanges(opCtx, db, head.Epoch, highWater, budget.Ranges)
	if err != nil {
		return apptypes.CatalogSnapshot{}, err
	}
	if digestCatalogRanges(replayed) != head.CurrentRangesDigest {
		return apptypes.CatalogSnapshot{}, apptypes.ErrCatalogDrift
	}
	return apptypes.CatalogSnapshot{Head: head, Ranges: ranges}, nil
}

//nolint:wrapcheck // This dedicated SQLite adapter preserves typed SQL failures.
func readCurrentCatalogRanges(ctx context.Context, q interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
}, maxRanges int) ([]apptypes.CatalogCurrentRange, error) {
	rows, err := q.QueryContext(ctx, `SELECT start_sequence,end_sequence,placement_state,reservation_id,segment_id,source_epoch FROM archive_catalog_current_ranges ORDER BY start_sequence`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []apptypes.CatalogCurrentRange
	for rows.Next() {
		if len(out) >= maxRanges {
			return nil, apptypes.ErrCatalogLimit
		}
		var item apptypes.CatalogCurrentRange
		if err = rows.Scan(&item.Range.Start, &item.Range.End, &item.Placement, &item.ReservationID, &item.SegmentID, &item.SourceEpoch); err != nil {
			return nil, err
		}
		out = append(out, item)
	}
	return out, rows.Err()
}

// RebuildCatalogCurrentRanges deterministically replaces only the derived
// cache. It never reads manifests or segment bindings and therefore cannot
// invent authority.
//
//nolint:wrapcheck // This dedicated SQLite adapter preserves typed SQL failures.
func (d *Database) RebuildCatalogCurrentRanges(ctx context.Context, expected apptypes.CatalogHead, budget apptypes.CatalogBudget) (apptypes.CatalogRebuildResult, error) {
	if !budget.Valid() {
		return apptypes.CatalogRebuildResult{}, apptypes.ErrCatalogLimit
	}
	opCtx, cancel := boundedCatalogContext(ctx, budget)
	defer cancel()
	db, err := d.open(opCtx)
	if err != nil {
		return apptypes.CatalogRebuildResult{}, err
	}
	defer d.release(db)
	tx, err := db.BeginTx(opCtx, nil)
	if err != nil {
		return apptypes.CatalogRebuildResult{}, err
	}
	defer func() { _ = tx.Rollback() }()
	head, err := readCatalogHead(opCtx, tx)
	if err != nil {
		return apptypes.CatalogRebuildResult{}, err
	}
	if head.Epoch != expected.Epoch || head.LedgerDigest != expected.LedgerDigest {
		return apptypes.CatalogRebuildResult{}, apptypes.ErrCatalogStaleHead
	}
	if head.Epoch == 0 {
		return apptypes.CatalogRebuildResult{Head: head}, nil
	}
	if err = verifyCatalogHeadIncremental(opCtx, tx, head); err != nil {
		return apptypes.CatalogRebuildResult{}, err
	}
	var highWater int64
	if err = tx.QueryRowContext(opCtx, `SELECT source_high_water FROM archive_catalog_epochs WHERE epoch=?`, head.Epoch).Scan(&highWater); err != nil {
		return apptypes.CatalogRebuildResult{}, apptypes.ErrCatalogDrift
	}
	ranges, err := replayCatalogRanges(opCtx, tx, head.Epoch, highWater, budget.Ranges)
	if err != nil {
		return apptypes.CatalogRebuildResult{}, err
	}
	digest := digestCatalogRanges(ranges)
	cached, cacheErr := readCurrentCatalogRanges(opCtx, tx, budget.Ranges)
	if cacheErr != nil {
		return apptypes.CatalogRebuildResult{}, cacheErr
	}
	changed := digestCatalogRanges(cached) != digest || head.CurrentRangesDigest != digest
	if err = replaceCatalogCurrent(opCtx, tx, ranges); err != nil {
		return apptypes.CatalogRebuildResult{}, err
	}
	result, err := tx.ExecContext(opCtx, `UPDATE archive_catalog_head SET current_ranges_digest=? WHERE singleton=1 AND current_epoch=? AND ledger_digest=?`, digest, head.Epoch, head.LedgerDigest)
	if err != nil {
		return apptypes.CatalogRebuildResult{}, err
	}
	n, _ := result.RowsAffected()
	if n != 1 {
		return apptypes.CatalogRebuildResult{}, apptypes.ErrCatalogStaleHead
	}
	head.CurrentRangesDigest = digest
	if err = tx.Commit(); err != nil {
		return apptypes.CatalogRebuildResult{}, err
	}
	return apptypes.CatalogRebuildResult{Head: head, Ranges: len(ranges), Changed: changed}, nil
}

// AuditCatalogLedgerPage verifies one bounded epoch page and returns a durable
// value checkpoint suitable for caller-controlled resume.
//
//nolint:wrapcheck // This dedicated SQLite adapter preserves typed SQL failures.
func (d *Database) AuditCatalogLedgerPage(ctx context.Context, cursor apptypes.CatalogAuditCursor, budget apptypes.CatalogBudget) (apptypes.CatalogAuditPage, error) {
	if !budget.Valid() || cursor.Epoch < 0 || (cursor.Epoch == 0 && cursor.LedgerDigest != emptyCatalogDigestValue) || (cursor.Epoch > 0 && !domain.ValidCatalogDigest(cursor.LedgerDigest)) {
		return apptypes.CatalogAuditPage{}, apptypes.ErrCatalogLimit
	}
	opCtx, cancel := context.WithTimeout(ctx, budget.WallTime)
	defer cancel()
	db, err := d.openReadOnly(opCtx)
	if err != nil {
		return apptypes.CatalogAuditPage{}, err
	}
	defer d.release(db)
	head, err := readCatalogHead(opCtx, db)
	if err != nil {
		return apptypes.CatalogAuditPage{}, err
	}
	if cursor.Epoch > head.Epoch {
		return apptypes.CatalogAuditPage{}, apptypes.ErrCatalogStaleHead
	}
	if cursor.Epoch > 0 {
		var stored string
		if err = db.QueryRowContext(opCtx, `SELECT ledger_digest FROM archive_catalog_epochs WHERE epoch=?`, cursor.Epoch).Scan(&stored); err != nil || stored != cursor.LedgerDigest {
			return apptypes.CatalogAuditPage{}, apptypes.ErrCatalogDrift
		}
	}
	rows, err := db.QueryContext(opCtx, `SELECT epoch,ledger_digest,parent_ledger_digest FROM archive_catalog_epochs WHERE epoch>? ORDER BY epoch LIMIT ?`, cursor.Epoch, budget.Ranges)
	if err != nil {
		return apptypes.CatalogAuditPage{}, err
	}
	var checkpoints []apptypes.CatalogAuditCursor
	var parents []string
	for rows.Next() {
		var item apptypes.CatalogAuditCursor
		var parent string
		if err = rows.Scan(&item.Epoch, &item.LedgerDigest, &parent); err != nil {
			_ = rows.Close()
			return apptypes.CatalogAuditPage{}, err
		}
		checkpoints = append(checkpoints, item)
		parents = append(parents, parent)
	}
	if err = rows.Err(); err != nil {
		_ = rows.Close()
		return apptypes.CatalogAuditPage{}, err
	}
	_ = rows.Close()
	previous := cursor
	if len(checkpoints) == 0 && cursor.Epoch < head.Epoch {
		return apptypes.CatalogAuditPage{}, apptypes.ErrCatalogDrift
	}
	for index, item := range checkpoints {
		if item.Epoch != previous.Epoch+1 || parents[index] != previous.LedgerDigest {
			return apptypes.CatalogAuditPage{}, apptypes.ErrCatalogDrift
		}
		if err = verifyCatalogHeadIncremental(opCtx, db, apptypes.CatalogHead{Epoch: item.Epoch, LedgerDigest: item.LedgerDigest}); err != nil {
			return apptypes.CatalogAuditPage{}, err
		}
		previous = item
	}
	return apptypes.CatalogAuditPage{Next: previous, Verified: len(checkpoints), Done: previous.Epoch == head.Epoch}, nil
}

// bindCatalogSegment records metadata only. It is intentionally unexported:
// #1651 must wrap it in a proof-specific port before production use.
//
//nolint:wrapcheck // This dedicated SQLite adapter preserves typed SQL failures.
func bindCatalogSegment(ctx context.Context, tx *sql.Tx, epoch int64, binding domain.CatalogSegmentBinding) error {
	var storeID string
	if err := tx.QueryRowContext(ctx, `SELECT store_id FROM archive_store_lineage WHERE singleton=1`).Scan(&storeID); err != nil {
		return err
	}
	if err := binding.Validate(storeID); err != nil {
		return apptypes.ErrCatalogLineageMismatch
	}
	var segmentID, existingStore, logical, file, manifest, summary, basename, storage string
	var start, end int64
	var formatVersion, manifestVersion, summaryVersion int
	err := tx.QueryRowContext(ctx, `SELECT segment_id,store_id,start_sequence,end_sequence,format_version,manifest_version,summary_version,logical_digest,file_digest,manifest_digest,summary_digest,relative_basename,storage_class FROM archive_catalog_segment_bindings WHERE segment_id=?`, binding.SegmentID()).Scan(&segmentID, &existingStore, &start, &end, &formatVersion, &manifestVersion, &summaryVersion, &logical, &file, &manifest, &summary, &basename, &storage)
	if err == nil {
		r := binding.Range()
		if segmentID == binding.SegmentID() && existingStore == binding.StoreID() && start == r.Start && end == r.End && formatVersion == binding.FormatVersion() && manifestVersion == binding.ManifestVersion() && summaryVersion == binding.SummaryVersion() && logical == binding.LogicalDigest() && file == binding.FileDigest() && manifest == binding.ManifestDigest() && summary == binding.SummaryDigest() && basename == binding.RelativeBasename() && storage == binding.StorageClass() {
			return nil
		}
		return apptypes.ErrCatalogBindingMismatch
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	r := binding.Range()
	_, err = tx.ExecContext(ctx, `INSERT INTO archive_catalog_segment_bindings(segment_id,store_id,start_sequence,end_sequence,format_version,manifest_version,summary_version,logical_digest,file_digest,manifest_digest,summary_digest,relative_basename,storage_class,bound_epoch) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?)`, binding.SegmentID(), binding.StoreID(), r.Start, r.End, binding.FormatVersion(), binding.ManifestVersion(), binding.SummaryVersion(), binding.LogicalDigest(), binding.FileDigest(), binding.ManifestDigest(), binding.SummaryDigest(), binding.RelativeBasename(), binding.StorageClass(), epoch)
	if err != nil {
		return fmt.Errorf("bind segment metadata: %w", err)
	}
	return nil
}

func min64(a, b int64) int64 {
	if a < b {
		return a
	}
	return b
}
func max64(a, b int64) int64 {
	if a > b {
		return a
	}
	return b
}
