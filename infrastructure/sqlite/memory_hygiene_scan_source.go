package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"log/slog"
	"strings"

	"golang.org/x/xerrors"

	"github.com/duck8823/traceary/application/queryservice"
	apptypes "github.com/duck8823/traceary/application/types"
	domtypes "github.com/duck8823/traceary/domain/types"
)

const selectMemoryHygieneRevisionQuery = `
SELECT revision
FROM memory_hygiene_revision
WHERE singleton = 1`

const memoryHygieneSourceBytesExpression = `(
    length(CAST(m.id AS BLOB)) +
    length(CAST(m.type AS BLOB)) +
    length(CAST(m.scope_kind AS BLOB)) +
    length(CAST(m.scope_value AS BLOB)) +
    length(CAST(m.fact AS BLOB)) +
    length(CAST(m.status AS BLOB)) +
    length(CAST(m.confidence AS BLOB)) +
    length(CAST(m.source AS BLOB)) +
    COALESCE(length(CAST(m.supersedes_memory_id AS BLOB)), 0) +
    COALESCE(length(CAST(m.expires_at AS BLOB)), 0) +
    COALESCE(length(CAST(m.valid_from AS BLOB)), 0) +
    COALESCE(length(CAST(m.valid_to AS BLOB)), 0) +
    length(CAST(m.created_at AS BLOB)) +
    length(CAST(m.updated_at AS BLOB))
)`

const selectMemoryHygieneSummaryProbeQuery = `
SELECT
    CASE WHEN ` + memoryHygieneSourceBytesExpression + ` <= ?1 THEN m.id ELSE NULL END,
    ` + memoryHygieneSourceBytesExpression + `
FROM memories m
WHERE 1 = 1`

// ScanMemoryHygienePage returns one bounded, revision-consistent source page.
// Pair traversal is exhaustive across continuations: an anchor is retained in
// the keyset until every greater same-scope partner has been visited.
func (d *MemoryDatasource) ScanMemoryHygienePage(
	ctx context.Context,
	criteria apptypes.MemoryHygieneScanPageCriteria,
) (result apptypes.MemoryHygieneScanSourcePage, err error) {
	if !criteria.Phase.IsKnown() {
		return result, xerrors.Errorf("unknown memory hygiene scan phase")
	}
	if criteria.MaxRows < 1 || criteria.MaxScanBytes < 1 || criteria.MaxComparisons < 1 {
		return result, xerrors.Errorf("memory hygiene source limits must be positive")
	}

	db, err := d.db.open(ctx)
	if err != nil {
		return result, xerrors.Errorf("failed to open DB for memory hygiene scan")
	}
	defer func() {
		if closeErr := db.Close(); closeErr != nil {
			slog.Debug("failed to close resource", "error", closeErr)
		}
	}()

	tx, err := db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return result, xerrors.Errorf("failed to begin memory hygiene scan transaction: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	revision, err := memoryHygieneRevision(ctx, tx)
	if err != nil {
		return result, err
	}
	if expected, ok := criteria.ExpectedRevision.Value(); ok && expected != revision {
		return result, xerrors.Errorf("%w", queryservice.ErrMemoryHygieneRevisionChanged)
	}
	result.Revision = revision
	result.ProgressKeyset = criteria.Keyset

	switch criteria.Phase {
	case apptypes.MemoryHygieneScanPhaseAcceptedRows:
		err = scanMemoryHygieneRows(ctx, tx, criteria, []domtypes.MemoryStatus{domtypes.MemoryStatusAccepted}, nil, &result)
	case apptypes.MemoryHygieneScanPhaseCandidateRows:
		sources := []domtypes.MemorySource{domtypes.MemorySourceExtracted}
		if criteria.IncludeHiddenCandidates {
			sources = append(sources, domtypes.MemorySourceExtractedHidden)
		}
		err = scanMemoryHygieneRows(ctx, tx, criteria, []domtypes.MemoryStatus{domtypes.MemoryStatusCandidate}, sources, &result)
	case apptypes.MemoryHygieneScanPhaseExactDuplicates:
		err = scanMemoryHygieneDuplicates(ctx, tx, criteria, &result)
	case apptypes.MemoryHygieneScanPhaseSimilarityPairs:
		err = scanMemoryHygienePairs(ctx, tx, criteria, &result)
	default:
		err = xerrors.Errorf("unknown memory hygiene scan phase")
	}
	if err != nil {
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			result.Done = false
			result.StopReason = apptypes.MemoryHygieneStopReasonTimeLimit
			return result, nil
		}
		return apptypes.MemoryHygieneScanSourcePage{}, err
	}
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		result.Done = false
		result.StopReason = apptypes.MemoryHygieneStopReasonTimeLimit
		return result, nil
	}
	if err = tx.Commit(); err != nil {
		return apptypes.MemoryHygieneScanSourcePage{}, xerrors.Errorf("failed to commit memory hygiene scan transaction: %w", err)
	}
	return result, nil
}

// RevalidateMemoryHygiene loads only the requested target and its same-scope
// peers. It fails closed through Complete=false when the peer set cannot be
// exhausted inside the explicit byte/comparison bounds.
func (d *MemoryDatasource) RevalidateMemoryHygiene(
	ctx context.Context,
	criteria apptypes.MemoryHygieneRevalidationCriteria,
) (result apptypes.MemoryHygieneRevalidationSourceResult, err error) {
	if criteria.MaxRows < 1 || criteria.MaxScanBytes < 1 || criteria.MaxComparisons < 1 {
		return result, xerrors.Errorf("memory hygiene revalidation limits must be positive")
	}
	db, err := d.db.open(ctx)
	if err != nil {
		return result, xerrors.Errorf("failed to open DB for memory hygiene revalidation")
	}
	defer func() {
		if closeErr := db.Close(); closeErr != nil {
			slog.Debug("failed to close resource", "error", closeErr)
		}
	}()
	tx, err := db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return result, xerrors.Errorf("failed to begin memory hygiene revalidation transaction: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	result.Revision, err = memoryHygieneRevision(ctx, tx)
	if err != nil {
		return result, err
	}
	target, found, oversized, targetBytes, err := loadMemoryHygieneSummaryByID(
		ctx,
		tx,
		criteria.MemoryID.String(),
		criteria.MaxScanBytes,
	)
	if err != nil {
		return result, err
	}
	if !found {
		return result, xerrors.Errorf("memory not found")
	}
	if oversized {
		result.StopReason = apptypes.MemoryHygieneStopReasonScanByteLimit
		return result, nil
	}
	if targetBytes > criteria.MaxScanBytes {
		result.StopReason = apptypes.MemoryHygieneStopReasonScanByteLimit
		return result, nil
	}
	result.Target = target
	result.ScannedRows = 1
	result.ScannedBytes = targetBytes

	if target.Status() == domtypes.MemoryStatusCandidate {
		result.Complete = true
		result.StopReason = apptypes.MemoryHygieneStopReasonComplete
		if err = tx.Commit(); err != nil {
			return apptypes.MemoryHygieneRevalidationSourceResult{}, xerrors.Errorf("failed to commit memory hygiene revalidation transaction: %w", err)
		}
		return result, nil
	}
	if target.Status() != domtypes.MemoryStatusAccepted {
		result.Complete = true
		result.StopReason = apptypes.MemoryHygieneStopReasonComplete
		if err = tx.Commit(); err != nil {
			return apptypes.MemoryHygieneRevalidationSourceResult{}, xerrors.Errorf("failed to commit memory hygiene revalidation transaction: %w", err)
		}
		return result, nil
	}

	afterID := ""
	for {
		if result.ScannedRows >= criteria.MaxRows {
			more, moreErr := hasMemoryHygienePeer(ctx, tx, target, afterID)
			if moreErr != nil {
				return result, moreErr
			}
			if more {
				result.StopReason = apptypes.MemoryHygieneStopReasonRowLimit
				return result, nil
			}
			break
		}
		if result.Comparisons >= criteria.MaxComparisons {
			more, moreErr := hasMemoryHygienePeer(ctx, tx, target, afterID)
			if moreErr != nil {
				return result, moreErr
			}
			if more {
				result.StopReason = apptypes.MemoryHygieneStopReasonComparisonLimit
				return result, nil
			}
			break
		}
		remainingBytes := criteria.MaxScanBytes - result.ScannedBytes
		peer, found, oversized, peerBytes, loadErr := nextMemoryHygienePeer(
			ctx,
			tx,
			target,
			afterID,
			false,
			remainingBytes,
		)
		if loadErr != nil {
			return result, loadErr
		}
		if !found {
			break
		}
		if oversized {
			result.StopReason = apptypes.MemoryHygieneStopReasonScanByteLimit
			return result, nil
		}
		if result.ScannedBytes+peerBytes > criteria.MaxScanBytes {
			result.StopReason = apptypes.MemoryHygieneStopReasonScanByteLimit
			return result, nil
		}
		result.Peers = append(result.Peers, peer)
		result.ScannedRows++
		result.ScannedBytes += peerBytes
		result.Comparisons++
		afterID = peer.MemoryID().String()
		if target.Fact() == peer.Fact() {
			existing, hasDuplicate := result.ExactDuplicateMemoryID.Value()
			if !hasDuplicate ||
				(existing.String() < target.MemoryID().String() && peer.MemoryID().String() > target.MemoryID().String()) {
				result.ExactDuplicateMemoryID = domtypes.Some(peer.MemoryID())
			}
		}
	}
	result.Complete = true
	result.StopReason = apptypes.MemoryHygieneStopReasonComplete
	if err = tx.Commit(); err != nil {
		return apptypes.MemoryHygieneRevalidationSourceResult{}, xerrors.Errorf("failed to commit memory hygiene revalidation transaction: %w", err)
	}
	return result, nil
}

func memoryHygieneRevision(ctx context.Context, tx *sql.Tx) (int64, error) {
	var revision int64
	if err := tx.QueryRowContext(ctx, selectMemoryHygieneRevisionQuery).Scan(&revision); err != nil {
		return 0, xerrors.Errorf("failed to read memory hygiene revision: %w", err)
	}
	return revision, nil
}

func scanMemoryHygieneRows(
	ctx context.Context,
	tx *sql.Tx,
	criteria apptypes.MemoryHygieneScanPageCriteria,
	statuses []domtypes.MemoryStatus,
	sources []domtypes.MemorySource,
	result *apptypes.MemoryHygieneScanSourcePage,
) error {
	afterID := criteria.Keyset.AfterMemoryID
	for {
		if result.ScannedRows >= criteria.MaxRows {
			result.StopReason = apptypes.MemoryHygieneStopReasonRowLimit
			return nil
		}
		remainingBytes := criteria.MaxScanBytes - result.ScannedBytes
		summary, found, oversized, sourceBytes, err := nextMemoryHygieneSummary(
			ctx,
			tx,
			afterID,
			criteria.Scopes,
			statuses,
			sources,
			remainingBytes,
		)
		if err != nil {
			return err
		}
		if !found {
			result.Done = true
			return nil
		}
		if oversized {
			result.StopReason = apptypes.MemoryHygieneStopReasonScanByteLimit
			return nil
		}
		if result.ScannedBytes+sourceBytes > criteria.MaxScanBytes {
			result.StopReason = apptypes.MemoryHygieneStopReasonScanByteLimit
			return nil
		}
		next := apptypes.MemoryHygieneScanKeyset{AfterMemoryID: summary.MemoryID().String()}
		result.Units = append(result.Units, apptypes.MemoryHygieneScanUnit{
			Row:             summary,
			Peer:            domtypes.None[apptypes.MemorySummary](),
			RelatedMemoryID: domtypes.None[domtypes.MemoryID](),
			NextKeyset:      next,
		})
		result.ScannedRows++
		result.ScannedBytes += sourceBytes
		result.ProgressKeyset = next
		afterID = summary.MemoryID().String()
	}
}

func scanMemoryHygieneDuplicates(
	ctx context.Context,
	tx *sql.Tx,
	criteria apptypes.MemoryHygieneScanPageCriteria,
	result *apptypes.MemoryHygieneScanSourcePage,
) error {
	afterID := criteria.Keyset.AfterMemoryID
	for {
		if result.ScannedRows >= criteria.MaxRows {
			result.StopReason = apptypes.MemoryHygieneStopReasonRowLimit
			return nil
		}
		if result.Comparisons >= criteria.MaxComparisons {
			result.StopReason = apptypes.MemoryHygieneStopReasonComparisonLimit
			return nil
		}
		remainingBytes := criteria.MaxScanBytes - result.ScannedBytes
		summary, found, oversized, sourceBytes, err := nextMemoryHygieneSummary(
			ctx,
			tx,
			afterID,
			criteria.Scopes,
			[]domtypes.MemoryStatus{domtypes.MemoryStatusAccepted},
			nil,
			remainingBytes,
		)
		if err != nil {
			return err
		}
		if !found {
			result.Done = true
			return nil
		}
		if oversized {
			result.StopReason = apptypes.MemoryHygieneStopReasonScanByteLimit
			return nil
		}
		if result.ScannedBytes+sourceBytes > criteria.MaxScanBytes {
			result.StopReason = apptypes.MemoryHygieneStopReasonScanByteLimit
			return nil
		}
		peerID, hasPeer, err := exactMemoryHygienePeerID(ctx, tx, summary)
		if err != nil {
			return err
		}
		next := apptypes.MemoryHygieneScanKeyset{AfterMemoryID: summary.MemoryID().String()}
		related := domtypes.None[domtypes.MemoryID]()
		if hasPeer {
			related = domtypes.Some(peerID)
		}
		result.Units = append(result.Units, apptypes.MemoryHygieneScanUnit{
			Row:             summary,
			Peer:            domtypes.None[apptypes.MemorySummary](),
			RelatedMemoryID: related,
			NextKeyset:      next,
		})
		result.ScannedRows++
		result.ScannedBytes += sourceBytes
		result.Comparisons++
		result.ProgressKeyset = next
		afterID = summary.MemoryID().String()
	}
}

func scanMemoryHygienePairs(
	ctx context.Context,
	tx *sql.Tx,
	criteria apptypes.MemoryHygieneScanPageCriteria,
	result *apptypes.MemoryHygieneScanSourcePage,
) error {
	keyset := criteria.Keyset
	for {
		if result.ScannedRows >= criteria.MaxRows {
			result.StopReason = apptypes.MemoryHygieneStopReasonRowLimit
			return nil
		}
		if result.Comparisons >= criteria.MaxComparisons {
			result.StopReason = apptypes.MemoryHygieneStopReasonComparisonLimit
			return nil
		}

		var anchor apptypes.MemorySummary
		var found bool
		var oversized bool
		var anchorBytes int64
		var err error
		remainingBytes := criteria.MaxScanBytes - result.ScannedBytes
		if keyset.AnchorMemoryID != "" {
			anchor, found, oversized, anchorBytes, err = loadMemoryHygieneSummaryByID(
				ctx,
				tx,
				keyset.AnchorMemoryID,
				remainingBytes,
			)
			if err != nil {
				return err
			}
			if oversized {
				result.StopReason = apptypes.MemoryHygieneStopReasonScanByteLimit
				return nil
			}
			if !found || anchor.Status() != domtypes.MemoryStatusAccepted || !memoryHygieneScopeIncluded(anchor.Scope(), criteria.Scopes) {
				return xerrors.Errorf("%w", queryservice.ErrMemoryHygieneRevisionChanged)
			}
		} else {
			anchor, found, oversized, anchorBytes, err = nextMemoryHygieneSummary(
				ctx,
				tx,
				keyset.AfterMemoryID,
				criteria.Scopes,
				[]domtypes.MemoryStatus{domtypes.MemoryStatusAccepted},
				nil,
				remainingBytes,
			)
			if err != nil {
				return err
			}
			if !found {
				result.Done = true
				result.ProgressKeyset = keyset
				return nil
			}
			if oversized {
				result.StopReason = apptypes.MemoryHygieneStopReasonScanByteLimit
				return nil
			}
			keyset.AnchorMemoryID = anchor.MemoryID().String()
			keyset.AfterPartnerID = ""
		}

		partnerBudget := remainingBytes - anchorBytes
		if partnerBudget < 0 {
			partnerBudget = 0
		}
		partner, hasPartner, oversized, partnerBytes, err := nextMemoryHygienePeer(
			ctx,
			tx,
			anchor,
			keyset.AfterPartnerID,
			true,
			partnerBudget,
		)
		if err != nil {
			return err
		}
		if !hasPartner {
			if result.ScannedBytes+anchorBytes > criteria.MaxScanBytes {
				result.StopReason = apptypes.MemoryHygieneStopReasonScanByteLimit
				return nil
			}
			next := apptypes.MemoryHygieneScanKeyset{AfterMemoryID: anchor.MemoryID().String()}
			result.Units = append(result.Units, apptypes.MemoryHygieneScanUnit{
				Row:             anchor,
				Peer:            domtypes.None[apptypes.MemorySummary](),
				RelatedMemoryID: domtypes.None[domtypes.MemoryID](),
				NextKeyset:      next,
			})
			result.ScannedRows++
			result.ScannedBytes += anchorBytes
			result.ProgressKeyset = next
			keyset = next
			continue
		}
		if oversized {
			result.StopReason = apptypes.MemoryHygieneStopReasonScanByteLimit
			return nil
		}
		if result.ScannedRows+2 > criteria.MaxRows {
			result.StopReason = apptypes.MemoryHygieneStopReasonRowLimit
			return nil
		}
		pairBytes := anchorBytes + partnerBytes
		if result.ScannedBytes+pairBytes > criteria.MaxScanBytes {
			result.StopReason = apptypes.MemoryHygieneStopReasonScanByteLimit
			return nil
		}
		next := apptypes.MemoryHygieneScanKeyset{
			AfterMemoryID:  keyset.AfterMemoryID,
			AnchorMemoryID: anchor.MemoryID().String(),
			AfterPartnerID: partner.MemoryID().String(),
		}
		result.Units = append(result.Units, apptypes.MemoryHygieneScanUnit{
			Row:             anchor,
			Peer:            domtypes.Some(partner),
			RelatedMemoryID: domtypes.None[domtypes.MemoryID](),
			NextKeyset:      next,
		})
		result.ScannedRows += 2
		result.ScannedBytes += pairBytes
		result.Comparisons++
		result.ProgressKeyset = next
		keyset = next
	}
}

func nextMemoryHygieneSummary(
	ctx context.Context,
	tx *sql.Tx,
	afterID string,
	scopes []domtypes.MemoryScope,
	statuses []domtypes.MemoryStatus,
	sources []domtypes.MemorySource,
	maxSourceBytes int64,
) (apptypes.MemorySummary, bool, bool, int64, error) {
	var builder strings.Builder
	builder.WriteString(selectMemoryHygieneSummaryProbeQuery)
	builder.WriteString(" AND m.id > ?")
	args := []any{maxSourceBytes, afterID}
	var err error
	args, err = appendMemoryFilters(&builder, args, scopes, statuses, nil, sources)
	if err != nil {
		return apptypes.MemorySummary{}, false, false, 0, err
	}
	builder.WriteString(" ORDER BY m.id ASC LIMIT 1")
	memoryID, oversized, sourceBytes, err := scanMemoryHygieneSummaryProbe(tx.QueryRowContext(ctx, builder.String(), args...))
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return apptypes.MemorySummary{}, false, false, 0, nil
		}
		return apptypes.MemorySummary{}, false, false, 0, xerrors.Errorf("failed to scan memory hygiene row: %w", err)
	}
	if oversized {
		return apptypes.MemorySummary{}, true, true, sourceBytes, nil
	}
	summary, err := loadProbedMemoryHygieneSummary(ctx, tx, memoryID)
	if err != nil {
		return apptypes.MemorySummary{}, false, false, 0, err
	}
	return summary, true, oversized, sourceBytes, nil
}

func loadMemoryHygieneSummaryByID(
	ctx context.Context,
	tx *sql.Tx,
	memoryID string,
	maxSourceBytes int64,
) (apptypes.MemorySummary, bool, bool, int64, error) {
	query := selectMemoryHygieneSummaryProbeQuery + " AND m.id = ?"
	probedID, oversized, sourceBytes, err := scanMemoryHygieneSummaryProbe(tx.QueryRowContext(ctx, query, maxSourceBytes, memoryID))
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return apptypes.MemorySummary{}, false, false, 0, nil
		}
		return apptypes.MemorySummary{}, false, false, 0, xerrors.Errorf("failed to scan memory hygiene target: %w", err)
	}
	if oversized {
		return apptypes.MemorySummary{}, true, true, sourceBytes, nil
	}
	summary, err := loadProbedMemoryHygieneSummary(ctx, tx, probedID)
	if err != nil {
		return apptypes.MemorySummary{}, false, false, 0, err
	}
	return summary, true, oversized, sourceBytes, nil
}

func exactMemoryHygienePeerID(
	ctx context.Context,
	tx *sql.Tx,
	summary apptypes.MemorySummary,
) (domtypes.MemoryID, bool, error) {
	const greaterQuery = `
SELECT id
FROM memories
WHERE status = ?
  AND scope_kind = ?
  AND scope_value = ?
  AND fact = ?
  AND id > ?
ORDER BY id ASC
LIMIT 1`
	const lowerQuery = `
SELECT id
FROM memories
WHERE status = ?
  AND scope_kind = ?
  AND scope_value = ?
  AND fact = ?
  AND id < ?
ORDER BY id ASC
LIMIT 1`
	args := []any{
		domtypes.MemoryStatusAccepted.String(),
		summary.Scope().Kind().String(),
		summary.Scope().Key(),
		summary.Fact(),
		summary.MemoryID().String(),
	}
	var peerID string
	err := tx.QueryRowContext(ctx, greaterQuery, args...).Scan(&peerID)
	if errors.Is(err, sql.ErrNoRows) {
		err = tx.QueryRowContext(ctx, lowerQuery, args...).Scan(&peerID)
	}
	if errors.Is(err, sql.ErrNoRows) {
		return "", false, nil
	}
	if err != nil {
		return "", false, xerrors.Errorf("failed to query exact memory hygiene peer: %w", err)
	}
	return domtypes.MemoryID(peerID), true, nil
}

func nextMemoryHygienePeer(
	ctx context.Context,
	tx *sql.Tx,
	anchor apptypes.MemorySummary,
	afterID string,
	greaterThanAnchor bool,
	maxSourceBytes int64,
) (apptypes.MemorySummary, bool, bool, int64, error) {
	var builder strings.Builder
	builder.WriteString(selectMemoryHygieneSummaryProbeQuery)
	builder.WriteString(" AND m.status = ? AND m.scope_kind = ? AND m.scope_value = ? AND m.id > ?")
	args := []any{
		maxSourceBytes,
		domtypes.MemoryStatusAccepted.String(),
		anchor.Scope().Kind().String(),
		anchor.Scope().Key(),
		afterID,
	}
	if greaterThanAnchor {
		builder.WriteString(" AND m.id > ?")
		args = append(args, anchor.MemoryID().String())
	} else {
		builder.WriteString(" AND m.id <> ?")
		args = append(args, anchor.MemoryID().String())
	}
	builder.WriteString(" ORDER BY m.id ASC LIMIT 1")
	memoryID, oversized, sourceBytes, err := scanMemoryHygieneSummaryProbe(tx.QueryRowContext(ctx, builder.String(), args...))
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return apptypes.MemorySummary{}, false, false, 0, nil
		}
		return apptypes.MemorySummary{}, false, false, 0, xerrors.Errorf("failed to scan memory hygiene peer: %w", err)
	}
	if oversized {
		return apptypes.MemorySummary{}, true, true, sourceBytes, nil
	}
	summary, err := loadProbedMemoryHygieneSummary(ctx, tx, memoryID)
	if err != nil {
		return apptypes.MemorySummary{}, false, false, 0, err
	}
	return summary, true, oversized, sourceBytes, nil
}

func scanMemoryHygieneSummaryProbe(rowScanner interface {
	Scan(dest ...any) error
}) (string, bool, int64, error) {
	var memoryID sql.NullString
	var sourceBytes int64
	if err := rowScanner.Scan(&memoryID, &sourceBytes); err != nil {
		return "", false, 0, xerrors.Errorf("failed to scan bounded memory hygiene row probe: %w", err)
	}
	if !memoryID.Valid {
		return "", true, sourceBytes, nil
	}
	return memoryID.String, false, sourceBytes, nil
}

func loadProbedMemoryHygieneSummary(
	ctx context.Context,
	tx *sql.Tx,
	memoryID string,
) (apptypes.MemorySummary, error) {
	query := selectMemorySummaryColumnsQuery + " AND m.id = ?"
	summary, err := scanMemorySummary(tx.QueryRowContext(ctx, query, memoryID))
	if err != nil {
		return apptypes.MemorySummary{}, xerrors.Errorf("failed to load probed memory hygiene row: %w", err)
	}
	return summary, nil
}

func hasMemoryHygienePeer(ctx context.Context, tx *sql.Tx, target apptypes.MemorySummary, afterID string) (bool, error) {
	const query = `
SELECT 1
FROM memories
WHERE status = ?
  AND scope_kind = ?
  AND scope_value = ?
  AND id <> ?
  AND id > ?
LIMIT 1`
	var one int
	err := tx.QueryRowContext(
		ctx,
		query,
		domtypes.MemoryStatusAccepted.String(),
		target.Scope().Kind().String(),
		target.Scope().Key(),
		target.MemoryID().String(),
		afterID,
	).Scan(&one)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, xerrors.Errorf("failed to check memory hygiene peers: %w", err)
	}
	return true, nil
}

func memoryHygieneScopeIncluded(scope domtypes.MemoryScope, scopes []domtypes.MemoryScope) bool {
	if len(scopes) == 0 {
		return true
	}
	if scope == nil {
		return false
	}
	for _, candidate := range scopes {
		if candidate != nil && candidate.Kind() == scope.Kind() && candidate.Key() == scope.Key() {
			return true
		}
	}
	return false
}
