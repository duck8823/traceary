//nolint:wrapcheck // This dedicated SQLite adapter preserves typed Catalog failures.
package sqlite

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"time"

	"github.com/duck8823/traceary/application"
	apptypes "github.com/duck8823/traceary/application/types"
	"github.com/duck8823/traceary/domain"
)

var _ application.CatalogSummaryDiagnosticsReader = (*Database)(nil)

type catalogSummaryBinding struct {
	segmentID, storeID, basename, logicalDigest, fileDigest, manifestDigest, summaryDigest, storageClass string
	start, end, boundEpoch                                                                               int64
	formatVersion, manifestVersion, summaryVersion                                                       int
}

// RebuildCatalogSummaries reconciles at most maxSegments previously
// unreconciled bindings. Each segment is its own durable checkpoint, so a
// cancelled invocation resumes without replaying completed files.
func (d *Database) RebuildCatalogSummaries(ctx context.Context, archiveRoot string, expected apptypes.CatalogHead, maxSegments int, limits ArchiveSegmentLimits, budget apptypes.CatalogBudget) (apptypes.CatalogSummaryRebuildResult, error) {
	if maxSegments <= 0 || maxSegments > 1000 || !budget.Valid() || limits.validate() != nil {
		return apptypes.CatalogSummaryRebuildResult{}, apptypes.ErrCatalogLimit
	}
	if err := validateArchiveRoot(archiveRoot); err != nil {
		return apptypes.CatalogSummaryRebuildResult{}, err
	}
	opCtx, cancel := context.WithTimeout(ctx, budget.WallTime)
	defer cancel()
	state, err := d.ensureCatalogSummaryAudit(opCtx, expected, budget)
	if err != nil {
		return apptypes.CatalogSummaryRebuildResult{}, err
	}
	db, err := d.openReadOnly(opCtx)
	if err != nil {
		return apptypes.CatalogSummaryRebuildResult{}, err
	}
	head, err := readCatalogHead(opCtx, db)
	if err != nil || head != expected || verifyCatalogLedgerPresence(opCtx, db, head) != nil || verifyCatalogHeadIncremental(opCtx, db, head) != nil {
		d.release(db)
		return apptypes.CatalogSummaryRebuildResult{}, apptypes.ErrCatalogDrift
	}
	rows, err := db.QueryContext(opCtx, `SELECT segment_id,store_id,start_sequence,end_sequence,format_version,manifest_version,summary_version,logical_digest,file_digest,manifest_digest,summary_digest,relative_basename,storage_class,bound_epoch FROM archive_catalog_segment_bindings WHERE bound_epoch>? OR (bound_epoch=? AND segment_id>?) ORDER BY bound_epoch,segment_id LIMIT ?`, state.cacheBoundEpoch, state.cacheBoundEpoch, state.cacheSegmentID, maxSegments)
	if err != nil {
		d.release(db)
		return apptypes.CatalogSummaryRebuildResult{}, err
	}
	var bindings []catalogSummaryBinding
	for rows.Next() {
		var b catalogSummaryBinding
		if err = rows.Scan(&b.segmentID, &b.storeID, &b.start, &b.end, &b.formatVersion, &b.manifestVersion, &b.summaryVersion, &b.logicalDigest, &b.fileDigest, &b.manifestDigest, &b.summaryDigest, &b.basename, &b.storageClass, &b.boundEpoch); err != nil {
			_ = rows.Close()
			d.release(db)
			return apptypes.CatalogSummaryRebuildResult{}, err
		}
		bindings = append(bindings, b)
	}
	err = rows.Err()
	_ = rows.Close()
	d.release(db)
	if err != nil {
		return apptypes.CatalogSummaryRebuildResult{}, err
	}
	result := apptypes.CatalogSummaryRebuildResult{}
	for _, binding := range bindings {
		if err = opCtx.Err(); err != nil {
			return result, err
		}
		manifest, summary, loadErr := loadBoundSegmentSummary(opCtx, archiveRoot, binding, limits)
		if loadErr != nil {
			return result, loadErr
		}
		inserted, reconcileErr := d.reconcileCatalogSummary(opCtx, expected, binding, manifest, summary, limits, budget)
		if reconcileErr != nil {
			return result, reconcileErr
		}
		result.Processed++
		if inserted {
			result.Inserted++
		} else {
			result.Existing++
		}
		if err = d.advanceCatalogSummaryCacheCursor(opCtx, expected, binding.boundEpoch, binding.segmentID, false, budget); err != nil {
			return result, err
		}
		state.cacheBoundEpoch, state.cacheSegmentID = binding.boundEpoch, binding.segmentID
	}
	db, err = d.openReadOnly(opCtx)
	if err != nil {
		return result, err
	}
	defer d.release(db)
	if err = db.QueryRowContext(opCtx, `SELECT count(*) FROM archive_catalog_segment_bindings WHERE bound_epoch>? OR (bound_epoch=? AND segment_id>?)`, state.cacheBoundEpoch, state.cacheBoundEpoch, state.cacheSegmentID).Scan(&result.Remaining); err != nil {
		return result, err
	}
	result.Done = result.Remaining == 0
	if result.Done {
		if err = d.advanceCatalogSummaryCacheCursor(opCtx, expected, state.cacheBoundEpoch, state.cacheSegmentID, true, budget); err != nil {
			return result, err
		}
	}
	return result, nil
}

type catalogSummaryRebuildState struct {
	expectedEpoch, auditEpoch, cacheBoundEpoch  int64
	expectedDigest, auditDigest, cacheSegmentID string
	auditComplete, cacheComplete                bool
}

func (d *Database) ensureCatalogSummaryAudit(ctx context.Context, expected apptypes.CatalogHead, budget apptypes.CatalogBudget) (catalogSummaryRebuildState, error) {
	db, err := d.open(ctx)
	if err != nil {
		return catalogSummaryRebuildState{}, err
	}
	defer d.release(db)
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return catalogSummaryRebuildState{}, err
	}
	defer func() { _ = tx.Rollback() }()
	head, err := readCatalogHead(ctx, tx)
	if err != nil || head != expected {
		return catalogSummaryRebuildState{}, apptypes.ErrCatalogDrift
	}
	var s catalogSummaryRebuildState
	if err = tx.QueryRowContext(ctx, `SELECT expected_epoch,expected_ledger_digest,audit_epoch,audit_ledger_digest,cache_bound_epoch,cache_segment_id,audit_complete,cache_complete FROM archive_catalog_summary_rebuild_state WHERE singleton=1`).Scan(&s.expectedEpoch, &s.expectedDigest, &s.auditEpoch, &s.auditDigest, &s.cacheBoundEpoch, &s.cacheSegmentID, &s.auditComplete, &s.cacheComplete); err != nil {
		return s, err
	}
	if s.expectedEpoch != expected.Epoch || s.expectedDigest != expected.LedgerDigest {
		s = catalogSummaryRebuildState{expectedEpoch: expected.Epoch, expectedDigest: expected.LedgerDigest, auditDigest: emptyCatalogDigestValue}
		if _, err = tx.ExecContext(ctx, `UPDATE archive_catalog_summary_rebuild_state SET expected_epoch=?,expected_ledger_digest=?,audit_epoch=0,audit_ledger_digest=?,cache_bound_epoch=0,cache_segment_id='',audit_complete=0,cache_complete=0 WHERE singleton=1`, expected.Epoch, expected.LedgerDigest, emptyCatalogDigestValue); err != nil {
			return s, err
		}
	} else if s.auditComplete && s.cacheComplete {
		// Completion authenticates only the finished cycle. Every explicit
		// subsequent validation starts from genesis so offline historical damage
		// cannot hide behind a permanent complete bit.
		s = catalogSummaryRebuildState{expectedEpoch: expected.Epoch, expectedDigest: expected.LedgerDigest, auditDigest: emptyCatalogDigestValue}
		if _, err = tx.ExecContext(ctx, `UPDATE archive_catalog_summary_rebuild_state SET audit_epoch=0,audit_ledger_digest=?,cache_bound_epoch=0,cache_segment_id='',audit_complete=0,cache_complete=0 WHERE singleton=1`, emptyCatalogDigestValue); err != nil {
			return s, err
		}
	}
	if err = tx.Commit(); err != nil {
		return s, err
	}
	if s.auditComplete {
		return s, nil
	}
	page, err := d.AuditCatalogLedgerPage(ctx, apptypes.CatalogAuditCursor{Epoch: s.auditEpoch, LedgerDigest: s.auditDigest}, budget)
	if err != nil {
		return s, err
	}
	if !page.Done && page.Verified < budget.Ranges {
		return s, apptypes.ErrCatalogDrift
	}
	if err = d.verifyCatalogReservationAuditPage(ctx, s.auditEpoch, page.Next.Epoch, budget); err != nil {
		return s, err
	}
	db, err = d.open(ctx)
	if err != nil {
		return s, err
	}
	defer d.release(db)
	tx, err = db.BeginTx(ctx, nil)
	if err != nil {
		return s, err
	}
	defer func() { _ = tx.Rollback() }()
	head, err = readCatalogHead(ctx, tx)
	if err != nil || head != expected {
		return s, apptypes.ErrCatalogDrift
	}
	result, err := tx.ExecContext(ctx, `UPDATE archive_catalog_summary_rebuild_state SET audit_epoch=?,audit_ledger_digest=?,audit_complete=? WHERE singleton=1 AND expected_epoch=? AND expected_ledger_digest=? AND audit_epoch=? AND audit_ledger_digest=?`, page.Next.Epoch, page.Next.LedgerDigest, page.Done, expected.Epoch, expected.LedgerDigest, s.auditEpoch, s.auditDigest)
	if err != nil {
		return s, err
	}
	n, _ := result.RowsAffected()
	if n != 1 {
		return s, apptypes.ErrCatalogDrift
	}
	if err = tx.Commit(); err != nil {
		return s, err
	}
	s.auditEpoch, s.auditDigest, s.auditComplete = page.Next.Epoch, page.Next.LedgerDigest, page.Done
	if !page.Done {
		return s, apptypes.ErrCatalogAuditIncomplete
	}
	return s, nil
}

func (d *Database) verifyCatalogReservationAuditPage(ctx context.Context, after, through int64, budget apptypes.CatalogBudget) error {
	if through <= after {
		return nil
	}
	opCtx, cancel := context.WithTimeout(ctx, budget.WallTime)
	defer cancel()
	db, err := d.openReadOnly(opCtx)
	if err != nil {
		return err
	}
	defer d.release(db)
	var invalid int64
	query := `SELECT count(*) FROM (SELECT t.epoch,t.transition_index FROM archive_catalog_range_transitions t LEFT JOIN archive_catalog_reservation_deltas d ON d.epoch=t.epoch AND d.reservation_id=t.reservation_id AND d.start_sequence=t.start_sequence AND d.end_sequence=t.end_sequence AND d.delta=CASE WHEN t.to_state='reserved' THEN 'reserve' ELSE 'release' END WHERE t.epoch>? AND t.epoch<=? AND (t.from_state='reserved' OR t.to_state='reserved') AND d.epoch IS NULL UNION ALL SELECT d.epoch,0 FROM archive_catalog_reservation_deltas d LEFT JOIN archive_catalog_range_transitions t ON t.epoch=d.epoch AND t.reservation_id=d.reservation_id AND t.start_sequence=d.start_sequence AND t.end_sequence=d.end_sequence AND ((d.delta='reserve' AND t.to_state='reserved') OR (d.delta='release' AND t.from_state='reserved')) WHERE d.epoch>? AND d.epoch<=? AND t.epoch IS NULL)`
	if err = db.QueryRowContext(opCtx, query, after, through, after, through).Scan(&invalid); err != nil || invalid != 0 {
		return apptypes.ErrCatalogDrift
	}
	return nil
}

func (d *Database) advanceCatalogSummaryCacheCursor(ctx context.Context, expected apptypes.CatalogHead, epoch int64, segmentID string, complete bool, budget apptypes.CatalogBudget) error {
	opCtx, cancel := boundedCatalogContext(ctx, budget)
	defer cancel()
	db, err := d.open(opCtx)
	if err != nil {
		return err
	}
	defer d.release(db)
	result, err := db.ExecContext(opCtx, `UPDATE archive_catalog_summary_rebuild_state SET cache_bound_epoch=?,cache_segment_id=?,cache_complete=? WHERE singleton=1 AND expected_epoch=? AND expected_ledger_digest=? AND audit_complete=1`, epoch, segmentID, complete, expected.Epoch, expected.LedgerDigest)
	if err != nil {
		return err
	}
	n, _ := result.RowsAffected()
	if n != 1 {
		return apptypes.ErrCatalogDrift
	}
	return nil
}

func loadBoundSegmentSummary(ctx context.Context, root string, binding catalogSummaryBinding, limits ArchiveSegmentLimits) (ArchiveSegmentManifest, domain.SegmentCatalogSummaryV1, error) {
	if binding.formatVersion != int(domain.SegmentFormatV1) || binding.manifestVersion != 1 || binding.summaryVersion != int(domain.SegmentSummaryV1) || binding.storageClass != "sealed_sqlite_zstd_v1" || binding.segmentID != binding.basename ||
		!domain.ValidCatalogDigest(binding.logicalDigest) || !domain.ValidCatalogDigest(binding.fileDigest) || !domain.ValidCatalogDigest(binding.manifestDigest) || !domain.ValidCatalogDigest(binding.summaryDigest) {
		return ArchiveSegmentManifest{}, domain.SegmentCatalogSummaryV1{}, apptypes.ErrCatalogBindingMismatch
	}
	path, err := safeArchivePath(root, binding.basename, true)
	if err != nil {
		return ArchiveSegmentManifest{}, domain.SegmentCatalogSummaryV1{}, err
	}
	db, pinned, err := openPinnedImmutableSegment(ctx, path, true)
	if err != nil {
		return ArchiveSegmentManifest{}, domain.SegmentCatalogSummaryV1{}, err
	}
	defer func() { _ = db.Close(); _ = pinned.Close() }()
	manifest, err := inspectArchiveSegmentOpen(ctx, db, pinned, binding.basename, limits.MaxFileBytes)
	if err != nil {
		return manifest, domain.SegmentCatalogSummaryV1{}, err
	}
	if manifest.StoreID != binding.storeID || int64(manifest.StartSequence) != binding.start || int64(manifest.EndSequence) != binding.end || manifest.LogicalDigest != binding.logicalDigest || manifest.FileDigest != "" || manifest.SummaryDigest != binding.summaryDigest || int(manifest.SummaryVersion) != binding.summaryVersion || manifest.Basename != binding.segmentID || manifest.Basename != binding.basename {
		return manifest, domain.SegmentCatalogSummaryV1{}, apptypes.ErrCatalogBindingMismatch
	}
	manifest.FileDigest = binding.fileDigest
	if _, err = verifyArchiveSegmentMetadataOpen(ctx, db, pinned, manifest, limits); err != nil {
		return manifest, domain.SegmentCatalogSummaryV1{}, err
	}
	manifestDigest, digestErr := ArchiveSegmentManifestDigest(manifest)
	if digestErr != nil || manifestDigest != binding.manifestDigest {
		return manifest, domain.SegmentCatalogSummaryV1{}, apptypes.ErrCatalogBindingMismatch
	}
	summary, err := readNormalizedSegmentSummary(ctx, db, manifest.FilterKeyID, manifest.TimeSummaryComplete, limits)
	return manifest, summary, err
}

func readNormalizedSegmentSummary(ctx context.Context, db interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
}, keyID string, timeComplete bool, limits ArchiveSegmentLimits) (domain.SegmentCatalogSummaryV1, error) {
	result := domain.SegmentCatalogSummaryV1{FilterKeyID: keyID, TimeComplete: timeComplete}
	rows, err := db.QueryContext(ctx, `SELECT kind,token FROM segment_exact_filters ORDER BY kind,token`)
	if err != nil {
		return result, err
	}
	for rows.Next() {
		var kind uint8
		var raw []byte
		if err = rows.Scan(&kind, &raw); err != nil || len(raw) != sha256.Size {
			_ = rows.Close()
			return result, ErrSegmentCorrupt
		}
		var token [sha256.Size]byte
		copy(token[:], raw)
		result.ExactTokens = append(result.ExactTokens, domain.SegmentSummaryToken{Kind: domain.SummaryTokenKind(kind), Value: token})
	}
	if err = rows.Close(); err != nil {
		return result, err
	}
	rows, err = db.QueryContext(ctx, `SELECT kind,bit_count,hash_count,bits FROM segment_bloom_filters ORDER BY kind`)
	if err != nil {
		return result, err
	}
	for rows.Next() {
		var kind uint8
		var count uint32
		var hashes uint8
		var raw []byte
		if err = rows.Scan(&kind, &count, &hashes, &raw); err != nil {
			_ = rows.Close()
			return result, err
		}
		result.Blooms = append(result.Blooms, domain.SegmentBloomV1{Kind: domain.SummaryTokenKind(kind), BitCount: count, HashCount: hashes, Bits: raw})
	}
	if err = rows.Close(); err != nil {
		return result, err
	}
	rows, err = db.QueryContext(ctx, `SELECT session_token,unit_count,audit_count FROM segment_session_aggregates ORDER BY session_token`)
	if err != nil {
		return result, err
	}
	for rows.Next() {
		var raw []byte
		var units, audits uint64
		if err = rows.Scan(&raw, &units, &audits); err != nil || len(raw) != sha256.Size {
			_ = rows.Close()
			return result, ErrSegmentCorrupt
		}
		var token [sha256.Size]byte
		copy(token[:], raw)
		result.Sessions = append(result.Sessions, domain.SegmentSessionAggregateV1{SessionToken: token, UnitCount: units, AuditCount: audits})
	}
	if err = rows.Close(); err != nil {
		return result, err
	}
	if _, err = result.CanonicalBytes(limits.MaxSummaryBytes); err != nil {
		return result, ErrSegmentCorrupt
	}
	return result, nil
}

func (d *Database) reconcileCatalogSummary(ctx context.Context, expected apptypes.CatalogHead, binding catalogSummaryBinding, manifest ArchiveSegmentManifest, summary domain.SegmentCatalogSummaryV1, limits ArchiveSegmentLimits, budget apptypes.CatalogBudget) (bool, error) {
	opCtx, cancel := boundedCatalogContext(ctx, budget)
	defer cancel()
	db, err := d.open(opCtx)
	if err != nil {
		return false, err
	}
	defer d.release(db)
	tx, err := db.BeginTx(opCtx, nil)
	if err != nil {
		return false, err
	}
	defer func() { _ = tx.Rollback() }()
	head, err := readCatalogHead(opCtx, tx)
	if err != nil || head != expected || verifyCatalogLedgerPresence(opCtx, tx, head) != nil || verifyCatalogHeadIncremental(opCtx, tx, head) != nil {
		return false, apptypes.ErrCatalogDrift
	}
	var actual catalogSummaryBinding
	err = tx.QueryRowContext(opCtx, `SELECT segment_id,store_id,start_sequence,end_sequence,format_version,manifest_version,summary_version,logical_digest,file_digest,manifest_digest,summary_digest,relative_basename,storage_class,bound_epoch FROM archive_catalog_segment_bindings WHERE segment_id=?`, binding.segmentID).Scan(&actual.segmentID, &actual.storeID, &actual.start, &actual.end, &actual.formatVersion, &actual.manifestVersion, &actual.summaryVersion, &actual.logicalDigest, &actual.fileDigest, &actual.manifestDigest, &actual.summaryDigest, &actual.basename, &actual.storageClass, &actual.boundEpoch)
	if err != nil || actual != binding {
		return false, apptypes.ErrCatalogBindingMismatch
	}
	canonical, err := summary.CanonicalBytes(manifest.SummaryByteCount)
	if err != nil || hex.EncodeToString(digestBytes(canonical)) != binding.summaryDigest {
		return false, apptypes.ErrCatalogBindingMismatch
	}
	var existingDigest, keyID, minCreated, maxCreated string
	var existingEpoch int64
	var version int
	var complete bool
	var units, audits uint64
	var plainValues, zstdValues uint64
	var totalPlain, stored, rowsCount, byteCount int64
	err = tx.QueryRowContext(opCtx, `SELECT bound_epoch,summary_version,filter_key_id,time_summary_complete,min_created_at,max_created_at,unit_count,audit_count,plain_value_count,zstd_value_count,total_plain_bytes,total_stored_bytes,summary_row_count,summary_byte_count,summary_digest FROM archive_catalog_summary_segments WHERE segment_id=?`, binding.segmentID).Scan(&existingEpoch, &version, &keyID, &complete, &minCreated, &maxCreated, &units, &audits, &plainValues, &zstdValues, &totalPlain, &stored, &rowsCount, &byteCount, &existingDigest)
	if err == nil {
		if existingDigest != binding.summaryDigest || existingEpoch != binding.boundEpoch || version != binding.summaryVersion || keyID != manifest.FilterKeyID || complete != manifest.TimeSummaryComplete || minCreated != manifest.MinCreatedAt || maxCreated != manifest.MaxCreatedAt || units != manifest.UnitCount || audits != manifest.AuditCount || plainValues != manifest.PlainValueCount || zstdValues != manifest.ZstdValueCount || totalPlain != manifest.TotalPlainBytes || stored != manifest.TotalStoredBytes || uint64(rowsCount) != manifest.SummaryRowCount || byteCount != manifest.SummaryByteCount {
			return false, apptypes.ErrCatalogDrift
		}
		cached, cacheErr := readCatalogNormalizedSummary(opCtx, tx, binding.segmentID, keyID, complete, manifest.SummaryRowCount, limits.MaxSummaryRows, manifest.SummaryByteCount)
		if cacheErr != nil {
			return false, apptypes.ErrCatalogDrift
		}
		cachedBytes, cacheErr := cached.CanonicalBytes(manifest.SummaryByteCount)
		if cacheErr != nil || !bytes.Equal(cachedBytes, canonical) {
			return false, apptypes.ErrCatalogDrift
		}
		return false, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return false, err
	}
	_, err = tx.ExecContext(opCtx, `INSERT INTO archive_catalog_summary_segments(segment_id,bound_epoch,summary_version,filter_key_id,time_summary_complete,min_created_at,max_created_at,unit_count,audit_count,plain_value_count,zstd_value_count,total_plain_bytes,total_stored_bytes,summary_row_count,summary_byte_count,summary_digest,reconciled_at) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`, binding.segmentID, binding.boundEpoch, manifest.SummaryVersion, manifest.FilterKeyID, manifest.TimeSummaryComplete, manifest.MinCreatedAt, manifest.MaxCreatedAt, manifest.UnitCount, manifest.AuditCount, manifest.PlainValueCount, manifest.ZstdValueCount, manifest.TotalPlainBytes, manifest.TotalStoredBytes, manifest.SummaryRowCount, manifest.SummaryByteCount, binding.summaryDigest, time.Now().UTC().Format(time.RFC3339Nano))
	if err != nil {
		return false, err
	}
	for _, v := range summary.ExactTokens {
		if _, err = tx.ExecContext(opCtx, `INSERT INTO archive_catalog_summary_exact(segment_id,kind,token) VALUES(?,?,?)`, binding.segmentID, v.Kind, v.Value[:]); err != nil {
			return false, err
		}
	}
	for _, v := range summary.Blooms {
		if _, err = tx.ExecContext(opCtx, `INSERT INTO archive_catalog_summary_blooms(segment_id,kind,bit_count,hash_count,bits) VALUES(?,?,?,?,?)`, binding.segmentID, v.Kind, v.BitCount, v.HashCount, v.Bits); err != nil {
			return false, err
		}
	}
	for _, v := range summary.Sessions {
		if _, err = tx.ExecContext(opCtx, `INSERT INTO archive_catalog_summary_sessions(segment_id,session_token,unit_count,audit_count) VALUES(?,?,?,?)`, binding.segmentID, v.SessionToken[:], v.UnitCount, v.AuditCount); err != nil {
			return false, err
		}
	}
	if err = tx.Commit(); err != nil {
		return false, err
	}
	return true, nil
}

func readCatalogNormalizedSummary(ctx context.Context, q interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
	QueryRowContext(context.Context, string, ...any) *sql.Row
}, segmentID, keyID string, complete bool, expectedRows, maxRows uint64, maxBytes int64) (domain.SegmentCatalogSummaryV1, error) {
	result := domain.SegmentCatalogSummaryV1{FilterKeyID: keyID, TimeComplete: complete}
	if maxRows == 0 || expectedRows > maxRows || maxBytes <= 0 {
		return result, apptypes.ErrCatalogDrift
	}
	var exactCount, bloomCount, sessionCount uint64
	var exactBytes, bloomBytes, sessionBytes int64
	var maxBloomBits uint32
	var maxBloomHashes uint8
	if err := q.QueryRowContext(ctx, `SELECT count(*),COALESCE(sum(length(token)),0) FROM archive_catalog_summary_exact WHERE segment_id=?`, segmentID).Scan(&exactCount, &exactBytes); err != nil {
		return result, err
	}
	if err := q.QueryRowContext(ctx, `SELECT count(*),COALESCE(sum(length(bits)),0),COALESCE(max(bit_count),0),COALESCE(max(hash_count),0) FROM archive_catalog_summary_blooms WHERE segment_id=?`, segmentID).Scan(&bloomCount, &bloomBytes, &maxBloomBits, &maxBloomHashes); err != nil {
		return result, err
	}
	if err := q.QueryRowContext(ctx, `SELECT count(*),COALESCE(sum(length(session_token)),0) FROM archive_catalog_summary_sessions WHERE segment_id=?`, segmentID).Scan(&sessionCount, &sessionBytes); err != nil {
		return result, err
	}
	total := exactCount + bloomCount + sessionCount
	if total != expectedRows || total > maxRows || exactBytes < 0 || bloomBytes < 0 || sessionBytes < 0 || exactBytes > maxBytes-bloomBytes || exactBytes+bloomBytes > maxBytes-sessionBytes || maxBloomBits > domain.SegmentSummaryBloomMaxBitsV1 || maxBloomHashes > 16 {
		return result, apptypes.ErrCatalogDrift
	}
	limit := int64(maxRows) + 1
	rows, err := q.QueryContext(ctx, `SELECT kind,token FROM archive_catalog_summary_exact WHERE segment_id=? ORDER BY kind,token LIMIT ?`, segmentID, limit)
	if err != nil {
		return result, err
	}
	for rows.Next() {
		if uint64(len(result.ExactTokens)) >= maxRows {
			_ = rows.Close()
			return result, apptypes.ErrCatalogDrift
		}
		var kind uint8
		var raw []byte
		if err = rows.Scan(&kind, &raw); err != nil || len(raw) != sha256.Size {
			_ = rows.Close()
			return result, apptypes.ErrCatalogDrift
		}
		var token [sha256.Size]byte
		copy(token[:], raw)
		result.ExactTokens = append(result.ExactTokens, domain.SegmentSummaryToken{Kind: domain.SummaryTokenKind(kind), Value: token})
	}
	if err = rows.Close(); err != nil {
		return result, err
	}
	rows, err = q.QueryContext(ctx, `SELECT kind,bit_count,hash_count,bits FROM archive_catalog_summary_blooms WHERE segment_id=? ORDER BY kind LIMIT ?`, segmentID, limit)
	if err != nil {
		return result, err
	}
	for rows.Next() {
		if uint64(len(result.ExactTokens)+len(result.Blooms)) >= maxRows {
			_ = rows.Close()
			return result, apptypes.ErrCatalogDrift
		}
		var kind uint8
		var bitCount uint32
		var hashCount uint8
		var bits []byte
		if err = rows.Scan(&kind, &bitCount, &hashCount, &bits); err != nil {
			_ = rows.Close()
			return result, err
		}
		result.Blooms = append(result.Blooms, domain.SegmentBloomV1{Kind: domain.SummaryTokenKind(kind), BitCount: bitCount, HashCount: hashCount, Bits: bits})
	}
	if err = rows.Close(); err != nil {
		return result, err
	}
	rows, err = q.QueryContext(ctx, `SELECT session_token,unit_count,audit_count FROM archive_catalog_summary_sessions WHERE segment_id=? ORDER BY session_token LIMIT ?`, segmentID, limit)
	if err != nil {
		return result, err
	}
	for rows.Next() {
		if uint64(len(result.ExactTokens)+len(result.Blooms)+len(result.Sessions)) >= maxRows {
			_ = rows.Close()
			return result, apptypes.ErrCatalogDrift
		}
		var raw []byte
		var units, audits uint64
		if err = rows.Scan(&raw, &units, &audits); err != nil || len(raw) != sha256.Size {
			_ = rows.Close()
			return result, apptypes.ErrCatalogDrift
		}
		var token [sha256.Size]byte
		copy(token[:], raw)
		result.Sessions = append(result.Sessions, domain.SegmentSessionAggregateV1{SessionToken: token, UnitCount: units, AuditCount: audits})
	}
	if err = rows.Close(); err != nil {
		return result, err
	}
	if _, err = result.CanonicalBytes(maxBytes); err != nil {
		return result, apptypes.ErrCatalogDrift
	}
	return result, nil
}

// CatalogSummaryDiagnostics returns aggregates only and fails closed on a
// missing or inconsistent ledger.
func (d *Database) CatalogSummaryDiagnostics(ctx context.Context, expected apptypes.CatalogHead, budget apptypes.CatalogBudget) (apptypes.CatalogSummaryDiagnostics, error) {
	if !budget.Valid() {
		return apptypes.CatalogSummaryDiagnostics{}, apptypes.ErrCatalogLimit
	}
	opCtx, cancel := context.WithTimeout(ctx, budget.WallTime)
	defer cancel()
	state, err := d.ensureCatalogDiagnosticsAudit(opCtx, expected, budget)
	if err != nil {
		return apptypes.CatalogSummaryDiagnostics{}, err
	}
	if err = d.validateCatalogDiagnosticsCachePage(opCtx, expected, state, budget); err != nil {
		return apptypes.CatalogSummaryDiagnostics{}, err
	}
	db, err := d.openReadOnly(opCtx)
	if err != nil {
		return apptypes.CatalogSummaryDiagnostics{}, err
	}
	defer d.release(db)
	head, err := readCatalogHead(opCtx, db)
	if err != nil || head != expected || verifyCatalogLedgerPresence(opCtx, db, head) != nil || verifyCatalogHeadIncremental(opCtx, db, head) != nil {
		return apptypes.CatalogSummaryDiagnostics{}, apptypes.ErrCatalogDrift
	}
	var r apptypes.CatalogSummaryDiagnostics
	queries := []struct {
		q   string
		dst *int64
	}{
		{`SELECT count(*) FROM archive_catalog_segment_bindings`, &r.BoundSegments}, {`SELECT count(*) FROM archive_catalog_summary_segments`, &r.ReconciledSegments}, {`SELECT count(*) FROM archive_catalog_summary_exact`, &r.ExactKindRows}, {`SELECT count(*) FROM archive_catalog_summary_blooms`, &r.BloomKindRows}, {`SELECT count(*) FROM archive_catalog_summary_sessions`, &r.SessionRows}, {`SELECT COALESCE(sum(unit_count),0) FROM archive_catalog_summary_segments`, &r.UnitCount}, {`SELECT COALESCE(sum(audit_count),0) FROM archive_catalog_summary_segments`, &r.AuditCount}, {`SELECT COALESCE(sum(total_stored_bytes),0) FROM archive_catalog_summary_segments`, &r.StoredBytes}, {`SELECT COALESCE(sum(summary_byte_count),0) FROM archive_catalog_summary_segments`, &r.SummaryBytes}, {`SELECT count(*) FROM archive_catalog_current_ranges WHERE placement_state='hot'`, &r.HotRanges}, {`SELECT count(*) FROM archive_catalog_current_ranges WHERE placement_state='reserved'`, &r.ReservedRanges}, {`SELECT count(*) FROM archive_catalog_current_ranges WHERE placement_state IN ('sealed','verified_shadow','segment_authoritative','evicting','cold')`, &r.SegmentRanges}, {`SELECT count(*) FROM archive_catalog_summary_segments s LEFT JOIN archive_catalog_segment_bindings b ON b.segment_id=s.segment_id WHERE b.segment_id IS NULL OR b.summary_digest<>s.summary_digest OR b.bound_epoch<>s.bound_epoch`, &r.DriftCount}}
	for _, query := range queries {
		if err = db.QueryRowContext(opCtx, query.q).Scan(query.dst); err != nil {
			return r, err
		}
	}
	r.UnknownKindSlots = r.ReconciledSegments*int64(summaryTokenKindCount) - r.BloomKindRows
	if r.UnknownKindSlots < 0 {
		r.DriftCount++
		r.UnknownKindSlots = 0
	}
	return r, nil
}

const summaryTokenKindCount = 5
const catalogSummaryMaxRows uint64 = 100_000
const catalogSummaryMaxBytes int64 = 64 << 20

type catalogDiagnosticsState struct {
	expectedEpoch, auditEpoch, cacheEpoch       int64
	expectedDigest, auditDigest, cacheID        string
	auditComplete, cacheComplete, cycleComplete bool
}

func (d *Database) ensureCatalogDiagnosticsAudit(ctx context.Context, expected apptypes.CatalogHead, budget apptypes.CatalogBudget) (catalogDiagnosticsState, error) {
	db, err := d.open(ctx)
	if err != nil {
		return catalogDiagnosticsState{}, err
	}
	defer d.release(db)
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return catalogDiagnosticsState{}, err
	}
	defer func() { _ = tx.Rollback() }()
	head, err := readCatalogHead(ctx, tx)
	if err != nil || head != expected {
		return catalogDiagnosticsState{}, apptypes.ErrCatalogDrift
	}
	var s catalogDiagnosticsState
	if err = tx.QueryRowContext(ctx, `SELECT expected_epoch,expected_ledger_digest,audit_epoch,audit_ledger_digest,cache_bound_epoch,cache_segment_id,audit_complete,cache_complete,cycle_complete FROM archive_catalog_summary_diagnostics_state WHERE singleton=1`).Scan(&s.expectedEpoch, &s.expectedDigest, &s.auditEpoch, &s.auditDigest, &s.cacheEpoch, &s.cacheID, &s.auditComplete, &s.cacheComplete, &s.cycleComplete); err != nil {
		return s, err
	}
	if s.expectedEpoch != expected.Epoch || s.expectedDigest != expected.LedgerDigest || s.cycleComplete {
		s = catalogDiagnosticsState{expectedEpoch: expected.Epoch, expectedDigest: expected.LedgerDigest, auditDigest: emptyCatalogDigestValue}
		if _, err = tx.ExecContext(ctx, `UPDATE archive_catalog_summary_diagnostics_state SET expected_epoch=?,expected_ledger_digest=?,audit_epoch=0,audit_ledger_digest=?,cache_bound_epoch=0,cache_segment_id='',audit_complete=0,cache_complete=0,cycle_complete=0 WHERE singleton=1`, expected.Epoch, expected.LedgerDigest, emptyCatalogDigestValue); err != nil {
			return s, err
		}
	}
	if err = tx.Commit(); err != nil {
		return s, err
	}
	if s.auditComplete {
		return s, nil
	}
	page, err := d.AuditCatalogLedgerPage(ctx, apptypes.CatalogAuditCursor{Epoch: s.auditEpoch, LedgerDigest: s.auditDigest}, budget)
	if err != nil {
		return s, err
	}
	if !page.Done && page.Verified < budget.Ranges {
		return s, apptypes.ErrCatalogDrift
	}
	if err = d.verifyCatalogReservationAuditPage(ctx, s.auditEpoch, page.Next.Epoch, budget); err != nil {
		return s, err
	}
	db, err = d.open(ctx)
	if err != nil {
		return s, err
	}
	defer d.release(db)
	result, err := db.ExecContext(ctx, `UPDATE archive_catalog_summary_diagnostics_state SET audit_epoch=?,audit_ledger_digest=?,audit_complete=? WHERE singleton=1 AND expected_epoch=? AND expected_ledger_digest=? AND audit_epoch=? AND audit_ledger_digest=?`, page.Next.Epoch, page.Next.LedgerDigest, page.Done, expected.Epoch, expected.LedgerDigest, s.auditEpoch, s.auditDigest)
	if err != nil {
		return s, err
	}
	n, _ := result.RowsAffected()
	if n != 1 {
		return s, apptypes.ErrCatalogDrift
	}
	s.auditEpoch, s.auditDigest, s.auditComplete = page.Next.Epoch, page.Next.LedgerDigest, page.Done
	if !page.Done {
		return s, apptypes.ErrCatalogAuditIncomplete
	}
	return s, nil
}

type diagnosticCacheRow struct {
	binding                                     catalogSummaryBinding
	parentEpoch                                 int64
	parentSummaryVersion                        int
	parentDigest, keyID, minCreated, maxCreated string
	complete                                    bool
	units, audits, plainValues, zstdValues      uint64
	totalPlain, totalStored                     int64
	rowCount                                    uint64
	byteCount                                   int64
}

func (d *Database) validateCatalogDiagnosticsCachePage(ctx context.Context, expected apptypes.CatalogHead, state catalogDiagnosticsState, budget apptypes.CatalogBudget) error {
	if state.cacheComplete {
		return nil
	}
	db, err := d.openReadOnly(ctx)
	if err != nil {
		return err
	}
	defer d.release(db)
	rows, err := db.QueryContext(ctx, `SELECT b.segment_id,b.store_id,b.start_sequence,b.end_sequence,b.format_version,b.manifest_version,b.summary_version,b.logical_digest,b.file_digest,b.manifest_digest,b.summary_digest,b.relative_basename,b.storage_class,b.bound_epoch,s.bound_epoch,s.summary_version,s.summary_digest,s.filter_key_id,s.time_summary_complete,s.min_created_at,s.max_created_at,s.unit_count,s.audit_count,s.plain_value_count,s.zstd_value_count,s.total_plain_bytes,s.total_stored_bytes,s.summary_row_count,s.summary_byte_count FROM archive_catalog_summary_segments s JOIN archive_catalog_segment_bindings b ON b.segment_id=s.segment_id WHERE s.bound_epoch>? OR (s.bound_epoch=? AND s.segment_id>?) ORDER BY s.bound_epoch,s.segment_id LIMIT ?`, state.cacheEpoch, state.cacheEpoch, state.cacheID, budget.Ranges+1)
	if err != nil {
		return err
	}
	page := make([]diagnosticCacheRow, 0, budget.Ranges)
	more := false
	for rows.Next() {
		if len(page) >= budget.Ranges {
			more = true
			break
		}
		var r diagnosticCacheRow
		if err = rows.Scan(&r.binding.segmentID, &r.binding.storeID, &r.binding.start, &r.binding.end, &r.binding.formatVersion, &r.binding.manifestVersion, &r.binding.summaryVersion, &r.binding.logicalDigest, &r.binding.fileDigest, &r.binding.manifestDigest, &r.binding.summaryDigest, &r.binding.basename, &r.binding.storageClass, &r.binding.boundEpoch, &r.parentEpoch, &r.parentSummaryVersion, &r.parentDigest, &r.keyID, &r.complete, &r.minCreated, &r.maxCreated, &r.units, &r.audits, &r.plainValues, &r.zstdValues, &r.totalPlain, &r.totalStored, &r.rowCount, &r.byteCount); err != nil {
			_ = rows.Close()
			return err
		}
		page = append(page, r)
	}
	_ = rows.Close()
	for _, r := range page {
		b := r.binding
		if r.parentEpoch != b.boundEpoch || r.parentSummaryVersion != int(domain.SegmentSummaryV1) || r.parentSummaryVersion != b.summaryVersion || r.parentDigest != b.summaryDigest || b.formatVersion != 1 || b.manifestVersion != 1 || b.summaryVersion != 1 || b.storageClass != "sealed_sqlite_zstd_v1" || r.rowCount > catalogSummaryMaxRows || r.byteCount <= 0 || r.byteCount > catalogSummaryMaxBytes {
			return apptypes.ErrCatalogDrift
		}
		manifest := ArchiveSegmentManifest{StoreID: b.storeID, FormatVersion: uint32(b.formatVersion), StartSequence: uint64(b.start), EndSequence: uint64(b.end), UnitCount: r.units, AuditCount: r.audits, MinCreatedAt: r.minCreated, MaxCreatedAt: r.maxCreated, PlainValueCount: r.plainValues, ZstdValueCount: r.zstdValues, TotalPlainBytes: r.totalPlain, TotalStoredBytes: r.totalStored, LogicalDigest: b.logicalDigest, FileDigest: b.fileDigest, Basename: b.basename, SummaryVersion: uint32(r.parentSummaryVersion), SummaryDigest: b.summaryDigest, FilterKeyID: r.keyID, TimeSummaryComplete: r.complete, SummaryRowCount: r.rowCount, SummaryByteCount: r.byteCount}
		digest, digestErr := ArchiveSegmentManifestDigest(manifest)
		if digestErr != nil || digest != b.manifestDigest {
			return apptypes.ErrCatalogDrift
		}
		summary, readErr := readCatalogNormalizedSummary(ctx, db, b.segmentID, r.keyID, r.complete, r.rowCount, catalogSummaryMaxRows, r.byteCount)
		if readErr != nil {
			return apptypes.ErrCatalogDrift
		}
		canonical, readErr := summary.CanonicalBytes(r.byteCount)
		if readErr != nil || hex.EncodeToString(digestBytes(canonical)) != b.summaryDigest {
			return apptypes.ErrCatalogDrift
		}
	}
	lastEpoch, lastID := state.cacheEpoch, state.cacheID
	if len(page) > 0 {
		lastEpoch, lastID = page[len(page)-1].binding.boundEpoch, page[len(page)-1].binding.segmentID
	}
	complete := !more
	writer, err := d.open(ctx)
	if err != nil {
		return err
	}
	defer d.release(writer)
	result, err := writer.ExecContext(ctx, `UPDATE archive_catalog_summary_diagnostics_state SET cache_bound_epoch=?,cache_segment_id=?,cache_complete=?,cycle_complete=? WHERE singleton=1 AND expected_epoch=? AND expected_ledger_digest=? AND audit_complete=1 AND cache_bound_epoch=? AND cache_segment_id=?`, lastEpoch, lastID, complete, complete, expected.Epoch, expected.LedgerDigest, state.cacheEpoch, state.cacheID)
	if err != nil {
		return err
	}
	n, _ := result.RowsAffected()
	if n != 1 {
		return apptypes.ErrCatalogDrift
	}
	if !complete {
		return apptypes.ErrCatalogAuditIncomplete
	}
	return nil
}

func verifyCatalogLedgerPresence(ctx context.Context, q interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}, head apptypes.CatalogHead) error {
	var count, minimum, maximum, broken int64
	if err := q.QueryRowContext(ctx, `SELECT count(*),COALESCE(min(epoch),0),COALESCE(max(epoch),0),COALESCE(sum(CASE WHEN parent_epoch<>epoch-1 THEN 1 ELSE 0 END),0) FROM archive_catalog_epochs`).Scan(&count, &minimum, &maximum, &broken); err != nil {
		return apptypes.ErrCatalogDrift
	}
	if head.Epoch == 0 {
		if count != 0 {
			return apptypes.ErrCatalogDrift
		}
		return nil
	}
	if count != head.Epoch || minimum != 1 || maximum != head.Epoch || broken != 0 {
		return apptypes.ErrCatalogDrift
	}
	if err := q.QueryRowContext(ctx, `SELECT count(*) FROM archive_catalog_epochs child LEFT JOIN archive_catalog_epochs parent ON parent.epoch=child.parent_epoch WHERE child.parent_epoch>0 AND (parent.epoch IS NULL OR parent.ledger_digest<>child.parent_ledger_digest)`).Scan(&broken); err != nil || broken != 0 {
		return apptypes.ErrCatalogDrift
	}
	return nil
}
