package sqlite

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"reflect"
	"strings"
	"testing"

	apptypes "github.com/duck8823/traceary/application/types"
	"github.com/duck8823/traceary/domain"
)

func TestCatalogSummaryRebuildIsResumableIdempotentAndAggregateOnly(t *testing.T) {
	ctx := context.Background()
	database, raw := newActivatedCatalogStore(t, 11)
	defer func() { _ = raw.Close() }()
	initial, err := database.CurrentCatalogRanges(ctx, catalogTestBudget())
	if err != nil {
		t.Fatal(err)
	}
	head, err := database.ReserveCatalogTarget(ctx, apptypes.CatalogReservation{ExpectedHead: initial.Head, ReservationID: "summary-fixture", Range: domain.CatalogRange{Start: 1, End: 1}, EvidenceDigest: strings.Repeat("a", 64), Budget: catalogTestBudget()})
	if err != nil {
		t.Fatal(err)
	}
	var storeID string
	if err = raw.QueryRow(`SELECT store_id FROM archive_store_lineage WHERE singleton=1`).Scan(&storeID); err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	manifest, err := BuildArchiveSegmentV1(ctx, root, testArchiveUnits(), ArchiveSegmentConfig{StoreID: storeID, CompressionFloor: 32, Limits: testSegmentLimits(), Summary: testSegmentSummary()})
	if err != nil {
		t.Fatal(err)
	}
	manifestDigest, err := ArchiveSegmentManifestDigest(manifest)
	if err != nil {
		t.Fatal(err)
	}
	fixtureBinding := catalogSummaryBinding{segmentID: manifest.Basename, storeID: storeID, basename: manifest.Basename, logicalDigest: manifest.LogicalDigest, fileDigest: manifest.FileDigest, manifestDigest: strings.Repeat("9", 64), summaryDigest: manifest.SummaryDigest, storageClass: "sealed_sqlite_zstd_v1", start: int64(manifest.StartSequence), end: int64(manifest.EndSequence), boundEpoch: head.Epoch, formatVersion: 1, manifestVersion: 1, summaryVersion: 1}
	if _, _, err = loadBoundSegmentSummary(ctx, root, fixtureBinding, testSegmentLimits()); !errors.Is(err, apptypes.ErrCatalogBindingMismatch) {
		t.Fatalf("manifest mutation error = %v", err)
	}
	if _, err = raw.Exec(`INSERT INTO archive_catalog_segment_bindings(segment_id,store_id,start_sequence,end_sequence,format_version,manifest_version,summary_version,logical_digest,file_digest,manifest_digest,summary_digest,relative_basename,storage_class,bound_epoch) VALUES(?,?,?,?,1,1,1,?,?,?,?,?,'sealed_sqlite_zstd_v1',?)`, manifest.Basename, storeID, manifest.StartSequence, manifest.EndSequence, manifest.LogicalDigest, manifest.FileDigest, manifestDigest, manifest.SummaryDigest, manifest.Basename, head.Epoch); err != nil {
		t.Fatal(err)
	}

	first, err := database.RebuildCatalogSummaries(ctx, root, head, 1, testSegmentLimits(), catalogTestBudget())
	if err != nil {
		t.Fatal(err)
	}
	if first.Processed != 1 || first.Inserted != 1 || !first.Done || first.Remaining != 0 {
		t.Fatalf("first = %+v", first)
	}
	second, err := database.RebuildCatalogSummaries(ctx, root, head, 1, testSegmentLimits(), catalogTestBudget())
	if err != nil {
		t.Fatal(err)
	}
	if second.Processed != 1 || second.Existing != 1 || !second.Done {
		t.Fatalf("idempotent = %+v", second)
	}
	diagnostics, err := database.CatalogSummaryDiagnostics(ctx, head, catalogTestBudget())
	if err != nil {
		t.Fatal(err)
	}
	if diagnostics.BoundSegments != 1 || diagnostics.ReconciledSegments != 1 || diagnostics.DriftCount != 0 || diagnostics.StoredBytes <= 0 {
		t.Fatalf("diagnostics = %+v", diagnostics)
	}
	for i := 0; i < reflect.TypeOf(diagnostics).NumField(); i++ {
		name := strings.ToLower(reflect.TypeOf(diagnostics).Field(i).Name)
		for _, forbidden := range []string{"id", "token", "digest", "basename", "payload", "key", "time"} {
			if strings.Contains(name, forbidden) {
				t.Fatalf("diagnostic field %q exposes %q", name, forbidden)
			}
		}
	}
	if _, err = raw.Exec(`UPDATE archive_catalog_summary_segments SET unit_count=unit_count+1 WHERE segment_id=?`, manifest.Basename); err != nil {
		t.Fatal(err)
	}
	if _, err = database.CatalogSummaryDiagnostics(ctx, head, catalogTestBudget()); !errors.Is(err, apptypes.ErrCatalogDrift) {
		t.Fatalf("parent mutation error = %v", err)
	}
	if _, err = raw.Exec(`UPDATE archive_catalog_summary_segments SET unit_count=? WHERE segment_id=?`, manifest.UnitCount, manifest.Basename); err != nil {
		t.Fatal(err)
	}
	if _, err = raw.Exec(`UPDATE archive_catalog_summary_segments SET summary_version=2 WHERE segment_id=?`, manifest.Basename); err != nil {
		t.Fatal(err)
	}
	if _, err = database.CatalogSummaryDiagnostics(ctx, head, catalogTestBudget()); !errors.Is(err, apptypes.ErrCatalogDrift) {
		t.Fatalf("parent summary-version mutation error = %v", err)
	}
	if _, err = raw.Exec(`UPDATE archive_catalog_summary_segments SET summary_version=? WHERE segment_id=?`, domain.SegmentSummaryV1, manifest.Basename); err != nil {
		t.Fatal(err)
	}
	tx, err := raw.Begin()
	if err != nil {
		t.Fatal(err)
	}
	statement, err := tx.Prepare(`INSERT INTO archive_catalog_summary_exact(segment_id,kind,token) VALUES(?,5,?)`)
	if err != nil {
		t.Fatal(err)
	}
	for index := 0; index < 1001; index++ {
		var number [8]byte
		binary.BigEndian.PutUint64(number[:], uint64(index))
		token := sha256.Sum256(number[:])
		if _, err = statement.Exec(manifest.Basename, token[:]); err != nil {
			_ = statement.Close()
			_ = tx.Rollback()
			t.Fatal(err)
		}
	}
	_ = statement.Close()
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}
	if _, err = database.RebuildCatalogSummaries(ctx, root, head, 1, testSegmentLimits(), catalogTestBudget()); !errors.Is(err, apptypes.ErrCatalogDrift) {
		t.Fatalf("over-cap child error = %v", err)
	}
	if _, err = raw.Exec(`DELETE FROM archive_catalog_summary_exact WHERE segment_id=? AND kind=5`, manifest.Basename); err != nil {
		t.Fatal(err)
	}
	if result, err := database.RebuildCatalogSummaries(ctx, root, head, 1, testSegmentLimits(), catalogTestBudget()); err != nil || !result.Done {
		t.Fatalf("post-cleanup rebuild=%+v err=%v", result, err)
	}
	if _, err = raw.Exec(`DELETE FROM archive_catalog_summary_exact WHERE segment_id=?`, manifest.Basename); err != nil {
		t.Fatal(err)
	}
	if _, err = database.RebuildCatalogSummaries(ctx, root, head, 1, testSegmentLimits(), catalogTestBudget()); !errors.Is(err, apptypes.ErrCatalogDrift) {
		t.Fatalf("deleted child error = %v", err)
	}
	if _, err = database.CatalogSummaryDiagnostics(ctx, head, catalogTestBudget()); !errors.Is(err, apptypes.ErrCatalogDrift) {
		t.Fatalf("diagnostic deleted-child error = %v", err)
	}
	if _, err = raw.Exec(`DELETE FROM archive_catalog_summary_segments WHERE segment_id=?`, manifest.Basename); err != nil {
		t.Fatal(err)
	}
	coverage, err := database.CatalogSummaryDiagnostics(ctx, head, catalogTestBudget())
	if err != nil || coverage.BoundSegments != 1 || coverage.ReconciledSegments != 0 {
		t.Fatalf("partial coverage=%+v err=%v", coverage, err)
	}
	if result, err := database.RebuildCatalogSummaries(ctx, root, head, 1, testSegmentLimits(), catalogTestBudget()); err != nil || !result.Done {
		t.Fatalf("coverage rebuild=%+v err=%v", result, err)
	}
	cloneLogical := strings.Repeat("2", 64)
	cloneBase := "segment-v1-" + cloneLogical + ".sqlite"
	cloneManifest := manifest
	cloneManifest.LogicalDigest = cloneLogical
	cloneManifest.Basename = cloneBase
	cloneManifest.FileDigest = strings.Repeat("3", 64)
	cloneManifestDigest, err := ArchiveSegmentManifestDigest(cloneManifest)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = raw.Exec(`INSERT INTO archive_catalog_segment_bindings(segment_id,store_id,start_sequence,end_sequence,format_version,manifest_version,summary_version,logical_digest,file_digest,manifest_digest,summary_digest,relative_basename,storage_class,bound_epoch) VALUES(?,?,?,?,1,1,1,?,?,?,?,?,'sealed_sqlite_zstd_v1',?)`, cloneBase, storeID, cloneManifest.StartSequence, cloneManifest.EndSequence, cloneLogical, cloneManifest.FileDigest, cloneManifestDigest, cloneManifest.SummaryDigest, cloneBase, head.Epoch); err != nil {
		t.Fatal(err)
	}
	if _, err = raw.Exec(`INSERT INTO archive_catalog_summary_segments SELECT ?,bound_epoch,summary_version,filter_key_id,time_summary_complete,min_created_at,max_created_at,unit_count,audit_count,plain_value_count,zstd_value_count,total_plain_bytes,total_stored_bytes,summary_row_count,summary_byte_count,summary_digest,reconciled_at FROM archive_catalog_summary_segments WHERE segment_id=?`, cloneBase, manifest.Basename); err != nil {
		t.Fatal(err)
	}
	if _, err = raw.Exec(`INSERT INTO archive_catalog_summary_exact SELECT ?,kind,token FROM archive_catalog_summary_exact WHERE segment_id=?`, cloneBase, manifest.Basename); err != nil {
		t.Fatal(err)
	}
	if _, err = raw.Exec(`INSERT INTO archive_catalog_summary_blooms SELECT ?,kind,bit_count,hash_count,bits FROM archive_catalog_summary_blooms WHERE segment_id=?`, cloneBase, manifest.Basename); err != nil {
		t.Fatal(err)
	}
	if _, err = raw.Exec(`INSERT INTO archive_catalog_summary_sessions SELECT ?,session_token,unit_count,audit_count FROM archive_catalog_summary_sessions WHERE segment_id=?`, cloneBase, manifest.Basename); err != nil {
		t.Fatal(err)
	}
	pageBudget := catalogTestBudget()
	pageBudget.Ranges = 1
	if _, err = database.CatalogSummaryDiagnostics(ctx, head, pageBudget); !errors.Is(err, apptypes.ErrCatalogAuditIncomplete) {
		t.Fatalf("diagnostic cache page error = %v", err)
	}
	var cacheCursor string
	if err = raw.QueryRow(`SELECT cache_segment_id FROM archive_catalog_summary_diagnostics_state WHERE singleton=1`).Scan(&cacheCursor); err != nil || cacheCursor == "" {
		t.Fatalf("diagnostic cache cursor=%q err=%v", cacheCursor, err)
	}
	finalDiagnostics, err := database.CatalogSummaryDiagnostics(ctx, head, pageBudget)
	if err != nil || finalDiagnostics.BoundSegments != 2 || finalDiagnostics.ReconciledSegments != 2 {
		t.Fatalf("diagnostic resume=%+v err=%v", finalDiagnostics, err)
	}
}

func TestCatalogSummaryAuditRejectsHistoricalReservationLoss(t *testing.T) {
	ctx := context.Background()
	database, raw := newActivatedCatalogStore(t, 1)
	defer func() { _ = raw.Close() }()
	initial, _ := database.CurrentCatalogRanges(ctx, catalogTestBudget())
	head, err := database.ReserveCatalogTarget(ctx, apptypes.CatalogReservation{ExpectedHead: initial.Head, ReservationID: "lost-delta", Range: domain.CatalogRange{Start: 1, End: 1}, EvidenceDigest: strings.Repeat("e", 64), Budget: catalogTestBudget()})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = raw.Exec(`DROP TRIGGER archive_catalog_reservations_no_delete`); err != nil {
		t.Fatal(err)
	}
	if _, err = raw.Exec(`DELETE FROM archive_catalog_reservation_deltas WHERE reservation_id='lost-delta'`); err != nil {
		t.Fatal(err)
	}
	_, err = database.RebuildCatalogSummaries(ctx, t.TempDir(), head, 1, testSegmentLimits(), catalogTestBudget())
	if !errors.Is(err, apptypes.ErrCatalogDrift) {
		t.Fatalf("historical reservation loss error = %v", err)
	}
}

func TestCatalogSummaryAuditRejectsHistoricalTransitionLoss(t *testing.T) {
	ctx := context.Background()
	database, raw := newActivatedCatalogStore(t, 1)
	defer func() { _ = raw.Close() }()
	initial, _ := database.CurrentCatalogRanges(ctx, catalogTestBudget())
	reserved, err := database.ReserveCatalogTarget(ctx, apptypes.CatalogReservation{ExpectedHead: initial.Head, ReservationID: "lost-transition", Range: domain.CatalogRange{Start: 1, End: 1}, EvidenceDigest: strings.Repeat("f", 64), Budget: catalogTestBudget()})
	if err != nil {
		t.Fatal(err)
	}
	head, err := database.ReleaseCatalogReservation(ctx, apptypes.CatalogRelease{ExpectedHead: reserved, ReservationID: "lost-transition", EvidenceDigest: strings.Repeat("f", 64), Budget: catalogTestBudget()})
	if err != nil {
		t.Fatal(err)
	}
	if result, completeErr := database.RebuildCatalogSummaries(ctx, t.TempDir(), head, 2, testSegmentLimits(), catalogTestBudget()); completeErr != nil || !result.Done {
		t.Fatalf("initial complete = %+v, %v", result, completeErr)
	}
	if _, err = raw.Exec(`DROP TRIGGER archive_catalog_transitions_no_delete`); err != nil {
		t.Fatal(err)
	}
	if _, err = raw.Exec(`DELETE FROM archive_catalog_range_transitions WHERE epoch=1`); err != nil {
		t.Fatal(err)
	}
	_, err = database.RebuildCatalogSummaries(ctx, t.TempDir(), head, 2, testSegmentLimits(), catalogTestBudget())
	if !errors.Is(err, apptypes.ErrCatalogDrift) {
		t.Fatalf("historical transition loss error = %v", err)
	}
}

func TestCatalogSummaryFullAuditResumesDurablyBeforeRebuild(t *testing.T) {
	ctx := context.Background()
	database, raw := newActivatedCatalogStore(t, 1)
	defer func() { _ = raw.Close() }()
	initial, _ := database.CurrentCatalogRanges(ctx, catalogTestBudget())
	reserved, err := database.ReserveCatalogTarget(ctx, apptypes.CatalogReservation{ExpectedHead: initial.Head, ReservationID: "paged-audit", Range: domain.CatalogRange{Start: 1, End: 1}, EvidenceDigest: strings.Repeat("1", 64), Budget: catalogTestBudget()})
	if err != nil {
		t.Fatal(err)
	}
	head, err := database.ReleaseCatalogReservation(ctx, apptypes.CatalogRelease{ExpectedHead: reserved, ReservationID: "paged-audit", EvidenceDigest: strings.Repeat("2", 64), Budget: catalogTestBudget()})
	if err != nil {
		t.Fatal(err)
	}
	budget := catalogTestBudget()
	budget.Ranges = 1
	if _, err = database.RebuildCatalogSummaries(ctx, t.TempDir(), head, 1, testSegmentLimits(), budget); !errors.Is(err, apptypes.ErrCatalogAuditIncomplete) {
		t.Fatalf("first page error = %v", err)
	}
	var cursor int64
	if err = raw.QueryRow(`SELECT audit_epoch FROM archive_catalog_summary_rebuild_state WHERE singleton=1`).Scan(&cursor); err != nil || cursor != 1 {
		t.Fatalf("durable cursor=%d err=%v", cursor, err)
	}
	result, err := database.RebuildCatalogSummaries(ctx, t.TempDir(), head, 1, testSegmentLimits(), budget)
	if err != nil || !result.Done {
		t.Fatalf("resumed result=%+v err=%v", result, err)
	}
}

func TestCatalogSummaryRebuildFailsClosedWhenLedgerIsMissing(t *testing.T) {
	ctx := context.Background()
	database, raw := newActivatedCatalogStore(t, 1)
	defer func() { _ = raw.Close() }()
	initial, _ := database.CurrentCatalogRanges(ctx, catalogTestBudget())
	head, err := database.ReserveCatalogTarget(ctx, apptypes.CatalogReservation{ExpectedHead: initial.Head, ReservationID: "ledger-loss", Range: domain.CatalogRange{Start: 1, End: 1}, EvidenceDigest: strings.Repeat("c", 64), Budget: catalogTestBudget()})
	if err != nil {
		t.Fatal(err)
	}
	// Point the mutable head at a missing append-only epoch. Rebuild must not
	// infer authority from bindings or segment files.
	head.Epoch++
	head.LedgerDigest = strings.Repeat("d", 64)
	if _, err = raw.Exec(`UPDATE archive_catalog_head SET current_epoch=?,ledger_digest=? WHERE singleton=1`, head.Epoch, head.LedgerDigest); err != nil {
		t.Fatal(err)
	}
	_, err = database.RebuildCatalogSummaries(ctx, t.TempDir(), head, 1, testSegmentLimits(), catalogTestBudget())
	if !errors.Is(err, apptypes.ErrCatalogDrift) {
		t.Fatalf("ledger loss error = %v", err)
	}
}

func TestCatalogSummaryMigrationDoesNotCreateAuthority(t *testing.T) {
	_, raw := newActivatedCatalogStore(t, 1)
	defer func() { _ = raw.Close() }()
	var bindings, summaries int
	if err := raw.QueryRow(`SELECT (SELECT count(*) FROM archive_catalog_segment_bindings),(SELECT count(*) FROM archive_catalog_summary_segments)`).Scan(&bindings, &summaries); err != nil {
		t.Fatal(err)
	}
	if bindings != 0 || summaries != 0 {
		t.Fatalf("migration invented authority: bindings=%d summaries=%d", bindings, summaries)
	}
}

func TestCatalogSummaryBindingRejectsNoncanonicalVersionsBeforeFileAccess(t *testing.T) {
	binding := catalogSummaryBinding{segmentID: "segment-v1-" + strings.Repeat("a", 64) + ".sqlite", basename: "segment-v1-" + strings.Repeat("a", 64) + ".sqlite", formatVersion: 2, manifestVersion: 1, summaryVersion: 1, storageClass: "sealed_sqlite_zstd_v1", logicalDigest: strings.Repeat("a", 64), fileDigest: strings.Repeat("b", 64), manifestDigest: strings.Repeat("c", 64), summaryDigest: strings.Repeat("d", 64)}
	if _, _, err := loadBoundSegmentSummary(context.Background(), t.TempDir(), binding, testSegmentLimits()); !errors.Is(err, apptypes.ErrCatalogBindingMismatch) {
		t.Fatalf("version error = %v", err)
	}
}
