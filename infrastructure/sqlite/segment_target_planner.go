package sqlite

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/duck8823/traceary/application"
	apptypes "github.com/duck8823/traceary/application/types"
	"github.com/duck8823/traceary/domain"
)

const segmentTargetMaxValueBytes int64 = 64 << 20

var _ application.CatalogTargetPlanner = (*Database)(nil)

type segmentTargetUnitEvidence struct {
	sequence int64
	bytes    int64
	digest   string
}

// PlanAndReserveCatalogTarget selects and reserves one deterministic prefix.
// A no-op head write obtains the SQLite writer slot before the snapshot, so a
// competing writer cannot invalidate a selected source between read and freeze.
//
//nolint:wrapcheck // Typed Catalog and selection failures cross this adapter unchanged.
func (d *Database) PlanAndReserveCatalogTarget(ctx context.Context, request apptypes.CatalogTargetPlanRequest) (apptypes.CatalogTargetPlan, error) {
	request.ReservationID = strings.TrimSpace(request.ReservationID)
	request.Retry.PreviousReservationID = strings.TrimSpace(request.Retry.PreviousReservationID)
	if !request.Budget.Valid() || !request.Policy.Valid() || request.ReservationID == "" || len(request.ReservationID) > 255 || request.CapturedHighWater <= 0 {
		return apptypes.CatalogTargetPlan{}, apptypes.ErrCatalogLimit
	}
	opCtx, cancel := context.WithTimeout(ctx, request.Budget.WallTime)
	defer cancel()
	db, err := d.open(opCtx)
	if err != nil {
		return apptypes.CatalogTargetPlan{}, targetSelectionContextError(opCtx, err)
	}
	defer d.release(db)
	lockMillis := max64(1, request.Budget.LockTime.Milliseconds())
	if _, err = db.ExecContext(opCtx, fmt.Sprintf("PRAGMA busy_timeout=%d", lockMillis)); err != nil {
		return apptypes.CatalogTargetPlan{}, targetSelectionContextError(opCtx, err)
	}
	tx, err := db.BeginTx(opCtx, nil)
	if err != nil {
		return apptypes.CatalogTargetPlan{}, targetSelectionContextError(opCtx, err)
	}
	defer func() { _ = tx.Rollback() }()
	if _, err = tx.ExecContext(opCtx, `UPDATE archive_catalog_head SET current_epoch=current_epoch WHERE singleton=1`); err != nil {
		return apptypes.CatalogTargetPlan{}, targetSelectionContextError(opCtx, err)
	}
	highWater, err := checkCatalogInventoryGate(opCtx, tx)
	if err != nil {
		return apptypes.CatalogTargetPlan{}, targetSelectionContextError(opCtx, err)
	}
	if request.CapturedHighWater > highWater {
		return apptypes.CatalogTargetPlan{}, apptypes.ErrCatalogGap
	}
	actual, err := readCatalogHead(opCtx, tx)
	if err != nil {
		return apptypes.CatalogTargetPlan{}, targetSelectionContextError(opCtx, err)
	}
	if actual != request.ExpectedHead {
		return apptypes.CatalogTargetPlan{}, apptypes.ErrCatalogStaleHead
	}
	if err = verifyCatalogHeadIncremental(opCtx, tx, actual); err != nil {
		return apptypes.CatalogTargetPlan{}, targetSelectionContextError(opCtx, err)
	}
	if err = validateCatalogTargetRetryRelease(opCtx, tx, request.Retry); err != nil {
		return apptypes.CatalogTargetPlan{}, targetSelectionContextError(opCtx, err)
	}
	ranges, err := replayCatalogRanges(opCtx, tx, actual.Epoch, highWater, request.Budget.Ranges)
	if err != nil {
		return apptypes.CatalogTargetPlan{}, targetSelectionContextError(opCtx, err)
	}
	start, end, found := smallestUnplacedTargetWindow(ranges, request.CapturedHighWater)
	if !found {
		return apptypes.CatalogTargetPlan{}, apptypes.ErrSegmentTargetNotFound
	}
	if err = d.runSegmentTargetPlannerHook("snapshot-pinned"); err != nil {
		return apptypes.CatalogTargetPlan{}, targetSelectionContextError(opCtx, err)
	}
	selection, units, sourceDigest, err := planSegmentTargetSelection(opCtx, tx, start, end, request.Policy)
	if err != nil {
		return apptypes.CatalogTargetPlan{}, targetSelectionContextError(opCtx, err)
	}
	if err = d.runSegmentTargetPlannerHook("selection-complete"); err != nil {
		return apptypes.CatalogTargetPlan{}, targetSelectionContextError(opCtx, err)
	}
	if err = validateCatalogTargetRetry(opCtx, tx, request, selection, units); err != nil {
		return apptypes.CatalogTargetPlan{}, targetSelectionContextError(opCtx, err)
	}
	var storeID string
	if err = tx.QueryRowContext(opCtx, `SELECT store_id FROM archive_store_lineage WHERE singleton=1`).Scan(&storeID); err != nil {
		return apptypes.CatalogTargetPlan{}, targetSelectionContextError(opCtx, err)
	}
	planDigest, err := domain.SegmentTargetPlanDigest(storeID, request.ReservationID, actual.Epoch, actual.LedgerDigest, actual.CurrentRangesDigest, sourceDigest, request.CapturedHighWater, request.Policy, selection)
	if err != nil {
		return apptypes.CatalogTargetPlan{}, apptypes.ErrCatalogLimit
	}
	evidenceDigest := catalogTargetEvidenceDigest(planDigest, request.Retry)
	transition, err := domain.ReservationTransition(selection.Range, request.ReservationID)
	if err != nil {
		return apptypes.CatalogTargetPlan{}, apptypes.ErrCatalogIllegalTransition
	}
	if err = d.runSegmentTargetPlannerHook("before-reserve"); err != nil {
		return apptypes.CatalogTargetPlan{}, targetSelectionContextError(opCtx, err)
	}
	head, err := commitCatalogEpoch(opCtx, tx, catalogEpochCommit{expected: actual, highWater: highWater, transitions: []domain.CatalogTransition{transition}, evidenceDigest: evidenceDigest, reservationID: request.ReservationID, delta: "reserve"}, request.Budget.Ranges)
	if err != nil {
		return apptypes.CatalogTargetPlan{}, targetSelectionContextError(opCtx, err)
	}
	if err = insertCatalogTargetPlan(opCtx, tx, head.Epoch, storeID, actual, request, selection, units, sourceDigest, planDigest, evidenceDigest, d.runSegmentTargetPlannerHook); err != nil {
		return apptypes.CatalogTargetPlan{}, targetSelectionContextError(opCtx, err)
	}
	if err = tx.Commit(); err != nil {
		return apptypes.CatalogTargetPlan{}, targetSelectionContextError(opCtx, err)
	}
	return apptypes.CatalogTargetPlan{Head: head, ReservationID: request.ReservationID, Range: selection.Range, Rows: selection.Rows, CanonicalPlainBytes: selection.CanonicalPlainBytes, DecodedBytes: selection.DecodedBytes, StoredUpperBytes: selection.StoredUpperBytes, CapturedHighWater: request.CapturedHighWater, PlanDigest: planDigest, ReservationEvidenceDigest: evidenceDigest}, nil
}

//nolint:wrapcheck // Internal append-only release proof lookup preserves typed errors.
func validateCatalogTargetRetryRelease(ctx context.Context, q interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
}, retry apptypes.CatalogTargetRetry) error {
	if retry.PreviousReservationID == "" {
		return nil
	}
	historyRange, reserved, released, _, releaseEvidence, err := catalogReservationHistory(ctx, q, retry.PreviousReservationID)
	if err != nil {
		return err
	}
	if !reserved || !released || historyRange != retry.PreviousRange || releaseEvidence != retry.EvidenceDigest {
		return apptypes.ErrSegmentTargetRetryProof
	}
	return nil
}

//nolint:wrapcheck // Operation cancellation and typed adapter errors are part of this port contract.
func targetSelectionContextError(operation context.Context, err error) error {
	if operation.Err() != nil || errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
		return apptypes.ErrSegmentTargetSelectionIncomplete
	}
	return err
}

func smallestUnplacedTargetWindow(ranges []apptypes.CatalogCurrentRange, highWater int64) (int64, int64, bool) {
	for _, current := range ranges {
		if current.Range.Start > highWater {
			break
		}
		if current.Placement == domain.CatalogPlacementHot {
			end := min64(current.Range.End, highWater)
			return current.Range.Start, end, current.Range.Start <= end
		}
	}
	return 0, 0, false
}

//nolint:wrapcheck // Internal SQLite hydration preserves errors for the public boundary.
func planSegmentTargetSelection(ctx context.Context, q interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}, start, end int64, policy domain.SegmentTargetPolicy) (domain.SegmentTargetSelection, []segmentTargetUnitEvidence, string, error) {
	if start <= 0 || end < start || !policy.Valid() {
		return domain.SegmentTargetSelection{}, nil, "", apptypes.ErrCatalogLimit
	}
	h := sha256.New()
	_, _ = h.Write([]byte("traceary-segment-target-source-v1\x00"))
	selection := domain.SegmentTargetSelection{}
	units := make([]segmentTargetUnitEvidence, 0, min(policy.MaxRows, 1024))
	for sequence := start; sequence <= end && !selection.Complete(policy); sequence++ {
		if err := ctx.Err(); err != nil {
			return domain.SegmentTargetSelection{}, nil, "", err
		}
		createdAt, timestampValid, err := readSegmentTargetTimestamp(ctx, q, sequence)
		if err != nil {
			return domain.SegmentTargetSelection{}, nil, "", err
		}
		if !timestampValid {
			return domain.SegmentTargetSelection{}, nil, "", apptypes.ErrSegmentTargetMalformedTimestamp
		}
		if createdAt.After(policy.CapturedAt.Add(-policy.HotHorizon)) {
			_, boundaryErr := selection.Consider(domain.SegmentTargetCandidate{Sequence: sequence, CreatedAt: createdAt, TimestampValid: true, CanonicalPlainBytes: 1}, policy)
			if boundaryErr != nil {
				return domain.SegmentTargetSelection{}, nil, "", boundaryErr
			}
			break
		}
		meta, err := readSegmentTargetMetadata(ctx, q, sequence, policy)
		if err != nil {
			if errors.Is(err, apptypes.ErrSegmentTargetOversizeFirst) && selection.Rows > 0 {
				break
			}
			return domain.SegmentTargetSelection{}, nil, "", err
		}
		admitted, err := selection.Consider(domain.SegmentTargetCandidate{Sequence: sequence, CreatedAt: createdAt, TimestampValid: true, CanonicalPlainBytes: meta.canonicalBytes, DecodedBytes: meta.decodedBytes}, policy)
		if err != nil {
			return domain.SegmentTargetSelection{}, nil, "", err
		}
		if !admitted {
			break
		}
		unit, err := hydrateSegmentTargetUnit(ctx, q, sequence)
		if err != nil {
			return domain.SegmentTargetSelection{}, nil, "", err
		}
		encoded, err := unit.CanonicalBytes()
		if err != nil || int64(len(encoded)) != meta.canonicalBytes {
			return domain.SegmentTargetSelection{}, nil, "", apptypes.ErrCatalogDrift
		}
		digest := sha256.Sum256(encoded)
		var frame [8]byte
		binary.BigEndian.PutUint64(frame[:], uint64(len(encoded)))
		_, _ = h.Write(frame[:])
		_, _ = h.Write(encoded)
		units = append(units, segmentTargetUnitEvidence{sequence: sequence, bytes: int64(len(encoded)), digest: hex.EncodeToString(digest[:])})
	}
	if selection.Rows == 0 {
		return domain.SegmentTargetSelection{}, nil, "", apptypes.ErrSegmentTargetNotFound
	}
	return selection, units, hex.EncodeToString(h.Sum(nil)), nil
}

type segmentTargetMetadata struct {
	canonicalBytes int64
	decodedBytes   int64
}

//nolint:wrapcheck // Internal timestamp scan preserves errors for its boundary.
func readSegmentTargetTimestamp(ctx context.Context, q interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}, sequence int64) (time.Time, bool, error) {
	var storageClass string
	var length sql.NullInt64
	var value sql.NullString
	err := q.QueryRowContext(ctx, `SELECT typeof(e.created_at),length(CAST(e.created_at AS BLOB)),CASE WHEN typeof(e.created_at)='text' AND length(CAST(e.created_at AS BLOB))<=64 THEN e.created_at ELSE NULL END FROM archive_event_sequences s JOIN events e ON e.id=s.event_id WHERE s.sequence=?`, sequence).Scan(&storageClass, &length, &value)
	if errors.Is(err, sql.ErrNoRows) {
		return time.Time{}, false, apptypes.ErrCatalogDrift
	}
	if err != nil {
		return time.Time{}, false, err
	}
	if storageClass != "text" || !length.Valid || length.Int64 > 64 || !value.Valid {
		return time.Time{}, false, nil
	}
	parsed, err := time.Parse(time.RFC3339Nano, value.String)
	if err != nil {
		return time.Time{}, false, nil
	}
	return parsed.UTC(), true, nil
}

//nolint:wrapcheck // Internal SQLite metadata scan preserves errors for its boundary.
func readSegmentTargetMetadata(ctx context.Context, q interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}, sequence int64, policy domain.SegmentTargetPolicy) (segmentTargetMetadata, error) {
	eventColumns, auditColumns := domain.ArchiveEventV1Columns(), domain.ArchiveAuditV1Columns()
	query := `SELECT (a.event_id IS NOT NULL),COALESCE(e.body_plaintext_bytes,length(CAST(e.body AS BLOB)),0),COALESCE(a.command_plaintext_bytes,length(CAST(a.command_text AS BLOB)),0),COALESCE(a.input_plaintext_bytes,length(CAST(a.input_text AS BLOB)),0),COALESCE(a.output_plaintext_bytes,length(CAST(a.output_text AS BLOB)),0),` +
		targetMetadataColumns("e", eventColumns) + `,` + targetMetadataColumns("a", auditColumns) +
		` FROM archive_event_sequences s JOIN events e ON e.id=s.event_id LEFT JOIN command_audits a ON a.event_id=e.id WHERE s.sequence=?`
	var hasAudit int
	var decoded [4]int64
	eventTypes, eventLengths := make([]string, len(eventColumns)), make([]sql.NullInt64, len(eventColumns))
	auditTypes, auditLengths := make([]string, len(auditColumns)), make([]sql.NullInt64, len(auditColumns))
	dest := []any{&hasAudit, &decoded[0], &decoded[1], &decoded[2], &decoded[3]}
	for index := range eventTypes {
		dest = append(dest, &eventTypes[index], &eventLengths[index])
	}
	for index := range auditTypes {
		dest = append(dest, &auditTypes[index], &auditLengths[index])
	}
	if err := q.QueryRowContext(ctx, query, sequence).Scan(dest...); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return segmentTargetMetadata{}, apptypes.ErrCatalogDrift
		}
		return segmentTargetMetadata{}, err
	}
	meta := segmentTargetMetadata{}
	for _, bytes := range decoded {
		if bytes < 0 || bytes > maxDecodedPayloadBytes {
			return segmentTargetMetadata{}, apptypes.ErrSegmentTargetOversizeFirst
		}
		if bytes > policy.MaxDecodedBytes-meta.decodedBytes {
			return segmentTargetMetadata{}, apptypes.ErrSegmentTargetOversizeFirst
		}
		meta.decodedBytes += bytes
	}
	size, err := canonicalTargetSize(sequence, eventTypes, eventLengths, hasAudit != 0, auditTypes, auditLengths, policy)
	if err != nil {
		return segmentTargetMetadata{}, err
	}
	meta.canonicalBytes = size
	return meta, nil
}

func targetMetadataColumns(alias string, columns []string) string {
	items := make([]string, 0, len(columns)*2)
	for _, column := range columns {
		items = append(items, "typeof("+alias+"."+column+")", "length(CAST("+alias+"."+column+" AS BLOB))")
	}
	return strings.Join(items, ",")
}

func canonicalTargetSize(sequence int64, eventTypes []string, eventLengths []sql.NullInt64, hasAudit bool, auditTypes []string, auditLengths []sql.NullInt64, policy domain.SegmentTargetPolicy) (int64, error) {
	if len(eventTypes) == 0 || eventTypes[0] != "text" || !eventLengths[0].Valid || eventLengths[0].Int64 <= 0 {
		return 0, apptypes.ErrCatalogDrift
	}
	size := int64(8+targetUvarintSize(uint64(sequence))+targetUvarintSize(uint64(eventLengths[0].Int64))+targetUvarintSize(uint64(len(eventTypes)))+1) + eventLengths[0].Int64
	var err error
	size, err = addTargetValueSizes(size, eventTypes, eventLengths, policy)
	if err != nil {
		return 0, err
	}
	if hasAudit {
		size += int64(targetUvarintSize(uint64(len(auditTypes))))
		size, err = addTargetValueSizes(size, auditTypes, auditLengths, policy)
		if err != nil {
			return 0, err
		}
	}
	return size, nil
}

func addTargetValueSizes(size int64, types []string, lengths []sql.NullInt64, policy domain.SegmentTargetPolicy) (int64, error) {
	for index, storageClass := range types {
		add := int64(1)
		switch storageClass {
		case "null":
		case "integer", "real":
			add += 8
		case "text", "blob":
			if !lengths[index].Valid || lengths[index].Int64 < 0 || lengths[index].Int64 > segmentTargetMaxValueBytes || lengths[index].Int64 > policy.MaxCanonicalPlainBytes-size {
				return 0, apptypes.ErrSegmentTargetOversizeFirst
			}
			add += int64(targetUvarintSize(uint64(lengths[index].Int64))) + lengths[index].Int64
		default:
			return 0, apptypes.ErrCatalogDrift
		}
		if add > policy.MaxCanonicalPlainBytes-size {
			return 0, apptypes.ErrSegmentTargetOversizeFirst
		}
		size += add
	}
	return size, nil
}

func targetUvarintSize(value uint64) int {
	var buffer [binary.MaxVarintLen64]byte
	return binary.PutUvarint(buffer[:], value)
}

//nolint:wrapcheck // Internal SQLite hydration preserves errors for its boundary.
func hydrateSegmentTargetUnit(ctx context.Context, q interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}, sequence int64) (domain.HistoryUnit, error) {
	eventColumns, auditColumns := domain.ArchiveEventV1Columns(), domain.ArchiveAuditV1Columns()
	query := `SELECT a.event_id,` + prefixedTargetColumns("e", eventColumns) + `,` + prefixedTargetColumns("a", auditColumns) +
		` FROM archive_event_sequences s JOIN events e ON e.id=s.event_id LEFT JOIN command_audits a ON a.event_id=e.id WHERE s.sequence=?`
	var auditID any
	eventRaw, auditRaw := make([]any, len(eventColumns)), make([]any, len(auditColumns))
	dest := []any{&auditID}
	for index := range eventRaw {
		dest = append(dest, &eventRaw[index])
	}
	for index := range auditRaw {
		dest = append(dest, &auditRaw[index])
	}
	if err := q.QueryRowContext(ctx, query, sequence).Scan(dest...); err != nil {
		return domain.HistoryUnit{}, err
	}
	eventValues, err := targetSQLiteValues(eventRaw)
	if err != nil {
		return domain.HistoryUnit{}, apptypes.ErrCatalogDrift
	}
	event, err := domain.NewArchiveEventV1(eventValues)
	if err != nil {
		return domain.HistoryUnit{}, apptypes.ErrCatalogDrift
	}
	unit := domain.HistoryUnit{Sequence: uint64(sequence), Event: event}
	if auditID != nil {
		auditValues, conversionErr := targetSQLiteValues(auditRaw)
		if conversionErr != nil {
			return domain.HistoryUnit{}, apptypes.ErrCatalogDrift
		}
		audit, constructionErr := domain.NewArchiveAuditV1(auditValues)
		if constructionErr != nil {
			return domain.HistoryUnit{}, apptypes.ErrCatalogDrift
		}
		unit.Audit = &audit
	}
	return unit, nil
}

func prefixedTargetColumns(alias string, columns []string) string {
	qualified := make([]string, len(columns))
	for index, column := range columns {
		qualified[index] = alias + "." + column
	}
	return strings.Join(qualified, ",")
}

func targetSQLiteValues(raw []any) ([]domain.SQLiteValue, error) {
	values := make([]domain.SQLiteValue, len(raw))
	for index, value := range raw {
		switch typed := value.(type) {
		case nil:
			values[index] = domain.NullValue()
		case int64:
			values[index] = domain.IntegerValue(typed)
		case float64:
			values[index] = domain.RealValue(typed)
		case string:
			values[index] = domain.TextValue([]byte(typed))
		case []byte:
			values[index] = domain.BlobValue(typed)
		default:
			return nil, fmt.Errorf("unsupported SQLite storage class %T", value)
		}
	}
	return values, nil
}

//nolint:wrapcheck // Internal SQLite proof lookup preserves errors for its boundary.
func validateCatalogTargetRetry(ctx context.Context, q interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
}, request apptypes.CatalogTargetPlanRequest, selection domain.SegmentTargetSelection, units []segmentTargetUnitEvidence) error {
	retry := request.Retry
	priorID, priorRange, hasLargerPrior, err := latestReleasedLargerTarget(ctx, q, selection.Range)
	if err != nil {
		return err
	}
	if retry.PreviousReservationID == "" {
		if retry.PreviousRange != (domain.CatalogRange{}) || retry.FailureClass != "" || retry.MeasuredBytes != 0 || retry.FailedCapBytes != 0 || retry.EvidenceDigest != "" || hasLargerPrior {
			return apptypes.ErrSegmentTargetRetryProof
		}
		return nil
	}
	if !hasLargerPrior || priorID != retry.PreviousReservationID || priorRange != retry.PreviousRange || retry.PreviousReservationID == request.ReservationID || selection.Range.Start != priorRange.Start || selection.Range.End >= priorRange.End {
		return apptypes.ErrSegmentTargetRetryProof
	}
	historyRange, reserved, released, _, releaseEvidence, historyErr := catalogReservationHistory(ctx, q, retry.PreviousReservationID)
	if historyErr != nil || !reserved || !released || historyRange != retry.PreviousRange || releaseEvidence != retry.EvidenceDigest {
		return apptypes.ErrSegmentTargetRetryProof
	}
	var oldPlanDigest, oldCapturedAt string
	var oldHighWater, oldHorizon, oldMaxRows, oldPlainCap, oldDecodedCap, oldStoredCap, oldFileCap int64
	var oldBoundVersion int64
	err = q.QueryRowContext(ctx, `SELECT plan_digest,captured_high_water,captured_at,hot_horizon_ns,max_rows,max_canonical_plain_bytes,max_decoded_bytes,max_stored_upper_bytes,max_file_bytes,stored_bound_version FROM archive_segment_target_plans WHERE reservation_id=?`, retry.PreviousReservationID).Scan(&oldPlanDigest, &oldHighWater, &oldCapturedAt, &oldHorizon, &oldMaxRows, &oldPlainCap, &oldDecodedCap, &oldStoredCap, &oldFileCap, &oldBoundVersion)
	if err != nil {
		return apptypes.ErrSegmentTargetRetryProof
	}
	if oldHighWater != request.CapturedHighWater || oldCapturedAt != request.Policy.CapturedAt.UTC().Format(time.RFC3339Nano) || oldHorizon != int64(request.Policy.HotHorizon) || oldPlainCap != request.Policy.MaxCanonicalPlainBytes || oldDecodedCap != request.Policy.MaxDecodedBytes || oldStoredCap != request.Policy.MaxStoredUpperBytes || oldFileCap != request.Policy.MaxFileBytes || oldBoundVersion != int64(request.Policy.StoredBoundVersion) || int64(request.Policy.MaxRows) >= oldMaxRows {
		return apptypes.ErrSegmentTargetRetryProof
	}
	relevantCap := oldStoredCap
	if retry.FailureClass == apptypes.SegmentTargetFailureFileCap {
		relevantCap = oldFileCap
	} else if retry.FailureClass != apptypes.SegmentTargetFailureStoredCap {
		return apptypes.ErrSegmentTargetRetryProof
	}
	wantEvidence, digestErr := domain.SegmentTargetRetryEvidenceDigest(oldPlanDigest, retry.FailureClass, retry.MeasuredBytes, retry.FailedCapBytes)
	if digestErr != nil || retry.FailedCapBytes != relevantCap || retry.EvidenceDigest != wantEvidence {
		return apptypes.ErrSegmentTargetRetryProof
	}
	for _, unit := range units {
		var oldBytes int64
		var oldDigest string
		if err = q.QueryRowContext(ctx, `SELECT canonical_bytes,canonical_digest FROM archive_segment_target_plan_units WHERE reservation_id=? AND sequence=?`, retry.PreviousReservationID, unit.sequence).Scan(&oldBytes, &oldDigest); err != nil || oldBytes != unit.bytes || oldDigest != unit.digest {
			return apptypes.ErrSegmentTargetRetryProof
		}
	}
	return nil
}

//nolint:wrapcheck // Internal SQLite helper preserves errors for its boundary.
func latestReleasedLargerTarget(ctx context.Context, q interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
}, selection domain.CatalogRange) (string, domain.CatalogRange, bool, error) {
	rows, err := q.QueryContext(ctx, `SELECT rs.reservation_id,rs.start_sequence,rs.end_sequence FROM archive_catalog_reservation_deltas rs JOIN archive_catalog_reservation_deltas rl ON rl.reservation_id=rs.reservation_id AND rl.delta='release' WHERE rs.delta='reserve' AND rs.start_sequence=? AND rs.end_sequence>? ORDER BY rl.epoch DESC LIMIT 1`, selection.Start, selection.End)
	if err != nil {
		return "", domain.CatalogRange{}, false, err
	}
	defer func() { _ = rows.Close() }()
	if !rows.Next() {
		return "", domain.CatalogRange{}, false, rows.Err()
	}
	var reservationID string
	var target domain.CatalogRange
	if err = rows.Scan(&reservationID, &target.Start, &target.End); err != nil {
		return "", domain.CatalogRange{}, false, err
	}
	return reservationID, target, true, rows.Err()
}

//nolint:wrapcheck // Transaction-local durable plan writes preserve SQLite errors.
func insertCatalogTargetPlan(ctx context.Context, tx *sql.Tx, boundEpoch int64, storeID string, parent apptypes.CatalogHead, request apptypes.CatalogTargetPlanRequest, selection domain.SegmentTargetSelection, units []segmentTargetUnitEvidence, sourceDigest, planDigest, evidenceDigest string, afterUnit func(string) error) error {
	retry := request.Retry
	_, err := tx.ExecContext(ctx, `INSERT INTO archive_segment_target_plans(reservation_id,bound_epoch,store_id,catalog_parent_epoch,catalog_parent_ledger_digest,catalog_parent_ranges_digest,captured_high_water,captured_at,hot_horizon_ns,max_rows,max_canonical_plain_bytes,max_decoded_bytes,max_stored_upper_bytes,max_file_bytes,stored_bound_version,start_sequence,end_sequence,selected_rows,canonical_plain_bytes,decoded_bytes,stored_upper_bytes,source_digest,plan_digest,reservation_evidence_digest,retry_previous_reservation_id,retry_failure_class,retry_measured_bytes,retry_failed_cap_bytes,retry_evidence_digest) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`, request.ReservationID, boundEpoch, storeID, parent.Epoch, parent.LedgerDigest, parent.CurrentRangesDigest, request.CapturedHighWater, request.Policy.CapturedAt.UTC().Format(time.RFC3339Nano), int64(request.Policy.HotHorizon), request.Policy.MaxRows, request.Policy.MaxCanonicalPlainBytes, request.Policy.MaxDecodedBytes, request.Policy.MaxStoredUpperBytes, request.Policy.MaxFileBytes, request.Policy.StoredBoundVersion, selection.Range.Start, selection.Range.End, selection.Rows, selection.CanonicalPlainBytes, selection.DecodedBytes, selection.StoredUpperBytes, sourceDigest, planDigest, evidenceDigest, retry.PreviousReservationID, retry.FailureClass, retry.MeasuredBytes, retry.FailedCapBytes, retry.EvidenceDigest)
	if err != nil {
		return err
	}
	for _, unit := range units {
		if _, err = tx.ExecContext(ctx, `INSERT INTO archive_segment_target_plan_units(reservation_id,sequence,canonical_bytes,canonical_digest) VALUES(?,?,?,?)`, request.ReservationID, unit.sequence, unit.bytes, unit.digest); err != nil {
			return err
		}
		if afterUnit != nil {
			if err = afterUnit("plan-unit-inserted"); err != nil {
				return err
			}
		}
	}
	return nil
}

func catalogTargetEvidenceDigest(planDigest string, retry apptypes.CatalogTargetRetry) string {
	if retry.PreviousReservationID == "" {
		return planDigest
	}
	h := sha256.New()
	_, _ = h.Write([]byte("traceary-segment-target-retry-v2\x00"))
	for _, value := range []string{planDigest, retry.PreviousReservationID, retry.FailureClass, retry.EvidenceDigest, fmt.Sprint(retry.PreviousRange.Start), fmt.Sprint(retry.PreviousRange.End), fmt.Sprint(retry.MeasuredBytes), fmt.Sprint(retry.FailedCapBytes)} {
		_, _ = h.Write([]byte(value))
		_, _ = h.Write([]byte{0})
	}
	return hex.EncodeToString(h.Sum(nil))
}
