//nolint:wrapcheck // This adapter intentionally preserves typed Catalog and context failures.
package sqlite

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"golang.org/x/sys/unix"

	"github.com/duck8823/traceary/application"
	apptypes "github.com/duck8823/traceary/application/types"
	"github.com/duck8823/traceary/domain"
)

var _ application.SegmentMigrationRepository = (*Database)(nil)

const (
	segmentMigrationBootstrapMaxWAL = 64 << 20
	segmentMigrationBootstrapTime   = 5 * time.Second
)

func segmentMigrationLimitError() error {
	// Preserve the historical infrastructure sentinel while exposing the
	// application-owned contract to callers of the migration use case.
	return errors.Join(apptypes.ErrSegmentMigrationLimit, ErrSegmentLimit)
}

type segmentMigrationConfig struct {
	candidateRoot, archiveRoot      string
	candidateDevice, candidateInode string
	archiveDevice, archiveInode     string
	compressionFloor                int
	softwareCommit, configDigest    string
	envelope                        apptypes.SegmentMigrationBudget
}

func segmentMigrationConfigDigest(command apptypes.SegmentMigrationStart, candidateDevice, candidateInode, archiveDevice, archiveInode string) string {
	h := sha256.New()
	// The start budget is the immutable authorized resource envelope. Resumes
	// may tighten individual caps; actual consumption is reported separately.
	_, _ = fmt.Fprintf(h, "traceary/segment-migration-config/v1\x00%s\x00%s\x00%s\x00%s\x00%d\x00%+v", candidateDevice, candidateInode, archiveDevice, archiveInode, command.CompressionFloor, command.Budget)
	return hex.EncodeToString(h.Sum(nil))
}

func canonicalSegmentRoot(path string) (string, error) {
	absolute, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	resolved, err := filepath.EvalSymlinks(absolute)
	if err != nil {
		return "", err
	}
	if err = validateArchiveRoot(resolved); err != nil {
		return "", err
	}
	return resolved, nil
}
func segmentRootIdentity(path string) (string, string, error) {
	info, err := os.Stat(path)
	if err != nil {
		return "", "", err
	}
	device, inode, ok := physicalFileIdentity(info)
	if !ok {
		return "", "", apptypes.ErrSegmentMigrationOrientation
	}
	return device, inode, nil
}
func verifySegmentRootIdentity(cfg segmentMigrationConfig) error {
	candidateDevice, candidateInode, err := segmentRootIdentity(cfg.candidateRoot)
	if err != nil {
		return err
	}
	if candidateDevice != cfg.candidateDevice || candidateInode != cfg.candidateInode {
		return apptypes.ErrSegmentMigrationOrientation
	}
	device, inode, err := segmentRootIdentity(cfg.archiveRoot)
	if err != nil {
		return err
	}
	if device != cfg.archiveDevice || inode != cfg.archiveInode {
		return apptypes.ErrSegmentMigrationOrientation
	}
	return nil
}

func migrationOperationContext(parent context.Context, budget apptypes.SegmentMigrationBudget) (context.Context, context.CancelFunc) {
	return context.WithTimeout(parent, budget.WallTime)
}

func (d *Database) acquireSegmentMigrationLease(ctx context.Context, lockTime time.Duration) (func(), error) {
	leaseCtx, cancel := context.WithTimeout(ctx, lockTime)
	defer cancel()
	lease, err := acquireAdvisoryLease(leaseCtx, d.Path()+".segment-migration.lock", true)
	if err != nil {
		return nil, err
	}
	return func() { _ = lease.Close() }, nil
}

// StartSegmentMigration binds a run to an existing immutable #1662 target.
func (d *Database) StartSegmentMigration(ctx context.Context, command apptypes.SegmentMigrationStart) (domain.SegmentMigrationRun, error) {
	command.RunID, command.StoreID, command.ReservationID, command.PlanDigest, command.SoftwareCommit = strings.TrimSpace(command.RunID), strings.TrimSpace(command.StoreID), strings.TrimSpace(command.ReservationID), strings.TrimSpace(command.PlanDigest), strings.TrimSpace(command.SoftwareCommit)
	if !command.Budget.Valid() || command.RunID == "" || len(command.RunID) > 255 || len(command.StoreID) != 32 || command.ReservationID == "" || !domain.ValidCatalogDigest(command.PlanDigest) || command.SoftwareCommit == "" || len(command.SoftwareCommit) > 128 || command.Range.Start <= 0 || command.Range.End < command.Range.Start || command.CompressionFloor < 0 {
		return domain.SegmentMigrationRun{}, apptypes.ErrCatalogLimit
	}
	var err error
	if command.CandidateRoot, err = canonicalSegmentRoot(command.CandidateRoot); err != nil {
		return domain.SegmentMigrationRun{}, err
	}
	if command.ArchiveRoot, err = canonicalSegmentRoot(command.ArchiveRoot); err != nil {
		return domain.SegmentMigrationRun{}, err
	}
	archiveDevice, archiveInode, err := segmentRootIdentity(command.ArchiveRoot)
	if err != nil {
		return domain.SegmentMigrationRun{}, err
	}
	candidateDevice, candidateInode, err := segmentRootIdentity(command.CandidateRoot)
	if err != nil {
		return domain.SegmentMigrationRun{}, err
	}
	configDigest := segmentMigrationConfigDigest(command, candidateDevice, candidateInode, archiveDevice, archiveInode)
	opCtx, cancel := migrationOperationContext(ctx, command.Budget)
	defer cancel()
	if info, statErr := os.Stat(d.Path() + "-wal"); statErr == nil && info.Size() > segmentMigrationBootstrapMaxWAL {
		return domain.SegmentMigrationRun{}, segmentMigrationLimitError()
	} else if statErr != nil && !errors.Is(statErr, os.ErrNotExist) {
		return domain.SegmentMigrationRun{}, statErr
	}
	release, err := d.acquireSegmentMigrationLease(opCtx, command.Budget.LockTime)
	if err != nil {
		return domain.SegmentMigrationRun{}, err
	}
	defer release()
	db, err := d.open(opCtx)
	if err != nil {
		return domain.SegmentMigrationRun{}, err
	}
	defer d.release(db)
	tx, err := db.BeginTx(opCtx, nil)
	if err != nil {
		return domain.SegmentMigrationRun{}, err
	}
	defer func() { _ = tx.Rollback() }()
	var storeID, planDigest string
	var start, end int64
	var planRows int
	var planPlain, planStored, planFile int64
	if err = tx.QueryRowContext(opCtx, `SELECT store_id,plan_digest,start_sequence,end_sequence,max_rows,max_canonical_plain_bytes,max_stored_upper_bytes,max_file_bytes FROM archive_segment_target_plans WHERE reservation_id=?`, command.ReservationID).Scan(&storeID, &planDigest, &start, &end, &planRows, &planPlain, &planStored, &planFile); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return domain.SegmentMigrationRun{}, apptypes.ErrSegmentMigrationStaleSource
		}
		return domain.SegmentMigrationRun{}, err
	}
	if storeID != command.StoreID || planDigest != command.PlanDigest || start != command.Range.Start || end != command.Range.End {
		return domain.SegmentMigrationRun{}, apptypes.ErrSegmentMigrationStaleSource
	}
	if command.Budget.PageRows > planRows || command.Budget.MaxPlainBytes > planPlain || command.Budget.MaxStoredBytes > planStored || command.Budget.MaxFileBytes > planFile {
		return domain.SegmentMigrationRun{}, apptypes.ErrCatalogLimit
	}
	var placement domain.CatalogPlacement
	var reservation string
	if err = tx.QueryRowContext(opCtx, `SELECT placement_state,reservation_id FROM archive_catalog_current_ranges WHERE start_sequence<=? AND end_sequence>=?`, start, end).Scan(&placement, &reservation); err != nil || placement != domain.CatalogPlacementReserved || reservation != command.ReservationID {
		return domain.SegmentMigrationRun{}, apptypes.ErrSegmentMigrationStaleSource
	}
	run := domain.SegmentMigrationRun{ID: command.RunID, StoreID: storeID, ReservationID: command.ReservationID, PlanDigest: planDigest, Range: domain.CatalogRange{Start: start, End: end}, Phase: domain.SegmentMigrationPlanned, Revision: 1, NextSequence: start}
	if err = run.Validate(); err != nil {
		return domain.SegmentMigrationRun{}, err
	}
	existing, existingCfg, existingErr := scanSegmentMigration(tx.QueryRowContext(opCtx, loadSegmentMigrationSQL, run.ID))
	if existingErr == nil {
		if existing.StoreID == run.StoreID && existing.ReservationID == run.ReservationID && existing.PlanDigest == run.PlanDigest && existing.Range == run.Range && existingCfg.softwareCommit == command.SoftwareCommit && existingCfg.configDigest == configDigest && existingCfg.candidateRoot == command.CandidateRoot && existingCfg.candidateDevice == candidateDevice && existingCfg.candidateInode == candidateInode && existingCfg.archiveRoot == command.ArchiveRoot && existingCfg.archiveDevice == archiveDevice && existingCfg.archiveInode == archiveInode && existingCfg.compressionFloor == command.CompressionFloor {
			return existing, nil
		}
		return domain.SegmentMigrationRun{}, apptypes.ErrSegmentMigrationConflict
	}
	if !errors.Is(existingErr, apptypes.ErrSegmentMigrationNotFound) {
		return domain.SegmentMigrationRun{}, existingErr
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	_, err = tx.ExecContext(opCtx, `INSERT INTO archive_segment_migration_runs(run_id,revision,store_id,reservation_id,plan_digest,software_commit,config_digest,envelope_page_rows,envelope_max_steps,envelope_wall_time_ns,envelope_lock_time_ns,envelope_max_plain_bytes,envelope_max_stored_bytes,envelope_max_value_plain_bytes,envelope_max_value_stored_bytes,envelope_max_file_bytes,envelope_max_summary_bytes,envelope_max_wal_bytes,envelope_min_free_disk_bytes,envelope_max_summary_rows,candidate_root,candidate_root_device,candidate_root_inode,archive_root,archive_root_device,archive_root_inode,compression_floor,start_sequence,end_sequence,phase,next_sequence,copied_rows,copied_plain_bytes,recorded_at) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`, run.ID, run.Revision, run.StoreID, run.ReservationID, run.PlanDigest, command.SoftwareCommit, configDigest,
		command.Budget.PageRows, command.Budget.MaxSteps, int64(command.Budget.WallTime), int64(command.Budget.LockTime), command.Budget.MaxPlainBytes, command.Budget.MaxStoredBytes, command.Budget.MaxValuePlainBytes, command.Budget.MaxValueStoredBytes, command.Budget.MaxFileBytes, command.Budget.MaxSummaryBytes, command.Budget.MaxWALBytes, command.Budget.MinFreeDiskBytes, command.Budget.MaxSummaryRows,
		command.CandidateRoot, candidateDevice, candidateInode, command.ArchiveRoot, archiveDevice, archiveInode, command.CompressionFloor, start, end, run.Phase, start, 0, 0, now)
	if err != nil {
		if strings.Contains(err.Error(), "UNIQUE") {
			return domain.SegmentMigrationRun{}, apptypes.ErrSegmentMigrationConflict
		}
		return domain.SegmentMigrationRun{}, err
	}
	if _, err = tx.ExecContext(opCtx, `INSERT INTO archive_segment_migration_active(store_id,run_id,revision) VALUES(?,?,?)`, storeID, run.ID, run.Revision); err != nil {
		return domain.SegmentMigrationRun{}, apptypes.ErrSegmentMigrationConflict
	}
	if err = tx.Commit(); err != nil {
		return domain.SegmentMigrationRun{}, err
	}
	return run, nil
}

func scanSegmentMigration(row *sql.Row) (domain.SegmentMigrationRun, segmentMigrationConfig, error) {
	var r domain.SegmentMigrationRun
	var cfg segmentMigrationConfig
	err := row.Scan(&r.ID, &r.StoreID, &r.ReservationID, &r.PlanDigest, &cfg.softwareCommit, &cfg.configDigest,
		&cfg.envelope.PageRows, &cfg.envelope.MaxSteps, &cfg.envelope.WallTime, &cfg.envelope.LockTime, &cfg.envelope.MaxPlainBytes, &cfg.envelope.MaxStoredBytes, &cfg.envelope.MaxValuePlainBytes, &cfg.envelope.MaxValueStoredBytes, &cfg.envelope.MaxFileBytes, &cfg.envelope.MaxSummaryBytes, &cfg.envelope.MaxWALBytes, &cfg.envelope.MinFreeDiskBytes, &cfg.envelope.MaxSummaryRows,
		&cfg.candidateRoot, &cfg.candidateDevice, &cfg.candidateInode, &cfg.archiveRoot, &cfg.archiveDevice, &cfg.archiveInode, &cfg.compressionFloor, &r.Range.Start, &r.Range.End, &r.Phase, &r.Revision, &r.NextSequence, &r.CopiedRows, &r.CopiedPlainBytes, &r.SourceDigest, &r.CandidateBasename, &r.SegmentID, &r.ManifestDigest, &r.FileDigest, &r.CatalogEpoch)
	if errors.Is(err, sql.ErrNoRows) {
		return r, cfg, apptypes.ErrSegmentMigrationNotFound
	}
	if err != nil {
		return r, cfg, err
	}
	if err = r.Validate(); err != nil {
		return r, cfg, apptypes.ErrCatalogDrift
	}
	if !cfg.envelope.Valid() {
		return r, cfg, apptypes.ErrCatalogDrift
	}
	return r, cfg, nil
}

const loadSegmentMigrationSQL = `SELECT run_id,store_id,reservation_id,plan_digest,software_commit,config_digest,envelope_page_rows,envelope_max_steps,envelope_wall_time_ns,envelope_lock_time_ns,envelope_max_plain_bytes,envelope_max_stored_bytes,envelope_max_value_plain_bytes,envelope_max_value_stored_bytes,envelope_max_file_bytes,envelope_max_summary_bytes,envelope_max_wal_bytes,envelope_min_free_disk_bytes,envelope_max_summary_rows,candidate_root,candidate_root_device,candidate_root_inode,archive_root,archive_root_device,archive_root_inode,compression_floor,start_sequence,end_sequence,phase,revision,next_sequence,copied_rows,copied_plain_bytes,source_digest,candidate_basename,segment_id,manifest_digest,file_digest,catalog_epoch FROM archive_segment_migration_runs WHERE run_id=? ORDER BY revision DESC LIMIT 1`

// LoadSegmentMigration returns the newest append-only revision.
func (d *Database) LoadSegmentMigration(ctx context.Context, id string) (domain.SegmentMigrationRun, error) {
	if info, err := os.Stat(d.Path() + "-wal"); err == nil && info.Size() > segmentMigrationBootstrapMaxWAL {
		return domain.SegmentMigrationRun{}, segmentMigrationLimitError()
	} else if err != nil && !errors.Is(err, os.ErrNotExist) {
		return domain.SegmentMigrationRun{}, err
	}
	bounded, cancel := context.WithTimeout(ctx, segmentMigrationBootstrapTime)
	defer cancel()
	db, err := d.openReadOnly(bounded)
	if err != nil {
		return domain.SegmentMigrationRun{}, err
	}
	defer d.release(db)
	r, _, err := scanSegmentMigration(db.QueryRowContext(bounded, loadSegmentMigrationSQL, strings.TrimSpace(id)))
	return r, err
}

func appendSegmentMigration(ctx context.Context, tx *sql.Tx, from, to domain.SegmentMigrationRun) error {
	if err := from.ValidateRevision(to); err != nil {
		return err
	}
	if from.ID != to.ID || from.StoreID != to.StoreID || from.ReservationID != to.ReservationID || from.PlanDigest != to.PlanDigest || from.Range != to.Range || to.Revision != from.Revision+1 {
		return domain.ErrSegmentMigrationInvalid
	}
	result, err := tx.ExecContext(ctx, `INSERT INTO archive_segment_migration_runs(run_id,revision,store_id,reservation_id,plan_digest,software_commit,config_digest,envelope_page_rows,envelope_max_steps,envelope_wall_time_ns,envelope_lock_time_ns,envelope_max_plain_bytes,envelope_max_stored_bytes,envelope_max_value_plain_bytes,envelope_max_value_stored_bytes,envelope_max_file_bytes,envelope_max_summary_bytes,envelope_max_wal_bytes,envelope_min_free_disk_bytes,envelope_max_summary_rows,candidate_root,candidate_root_device,candidate_root_inode,archive_root,archive_root_device,archive_root_inode,compression_floor,start_sequence,end_sequence,phase,next_sequence,copied_rows,copied_plain_bytes,source_digest,candidate_basename,segment_id,manifest_digest,file_digest,catalog_epoch,recorded_at) SELECT run_id,?,store_id,reservation_id,plan_digest,software_commit,config_digest,envelope_page_rows,envelope_max_steps,envelope_wall_time_ns,envelope_lock_time_ns,envelope_max_plain_bytes,envelope_max_stored_bytes,envelope_max_value_plain_bytes,envelope_max_value_stored_bytes,envelope_max_file_bytes,envelope_max_summary_bytes,envelope_max_wal_bytes,envelope_min_free_disk_bytes,envelope_max_summary_rows,candidate_root,candidate_root_device,candidate_root_inode,archive_root,archive_root_device,archive_root_inode,compression_floor,start_sequence,end_sequence,?,?,?,?,?,?,?,?,?,?,? FROM archive_segment_migration_runs WHERE run_id=? AND revision=?`, to.Revision, to.Phase, to.NextSequence, to.CopiedRows, to.CopiedPlainBytes, to.SourceDigest, to.CandidateBasename, to.SegmentID, to.ManifestDigest, to.FileDigest, to.CatalogEpoch, time.Now().UTC().Format(time.RFC3339Nano), from.ID, from.Revision)
	if err != nil {
		return err
	}
	changed, _ := result.RowsAffected()
	if changed != 1 {
		return apptypes.ErrSegmentMigrationConflict
	}
	result, err = tx.ExecContext(ctx, `UPDATE archive_segment_migration_active SET revision=? WHERE store_id=? AND run_id=? AND revision=?`, to.Revision, to.StoreID, to.ID, from.Revision)
	if err != nil {
		return err
	}
	changed, _ = result.RowsAffected()
	if changed != 1 {
		return apptypes.ErrSegmentMigrationConflict
	}
	return nil
}

func migrationLimits(b apptypes.SegmentMigrationBudget) ArchiveSegmentLimits {
	return ArchiveSegmentLimits{MaxValuePlainBytes: b.MaxValuePlainBytes, MaxValueStoredBytes: b.MaxValueStoredBytes, MaxTotalPlainBytes: b.MaxPlainBytes, MaxTotalStoredBytes: b.MaxStoredBytes, MaxFileBytes: b.MaxFileBytes, MaxSummaryBytes: b.MaxSummaryBytes, MaxSummaryRows: b.MaxSummaryRows}
}

func checkSegmentMigrationResources(storePath, outputRoot string, budget apptypes.SegmentMigrationBudget) error {
	if err := checkSegmentMigrationWAL(storePath, budget); err != nil {
		return err
	}
	var stat unix.Statfs_t
	if err := unix.Statfs(outputRoot, &stat); err != nil {
		return err
	}
	free := uint64(stat.Bavail) * uint64(stat.Bsize)
	reserve, output := uint64(budget.MinFreeDiskBytes), uint64(budget.MaxFileBytes)
	if free < reserve || free-reserve < output {
		return segmentMigrationLimitError()
	}
	return nil
}

func checkSegmentMigrationWAL(storePath string, budget apptypes.SegmentMigrationBudget) error {
	if info, err := os.Stat(storePath + "-wal"); err == nil && info.Size() > budget.MaxWALBytes {
		return segmentMigrationLimitError()
	} else if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}

func (d *Database) loadMigrationConfigBounded(ctx context.Context, id string, wallTime time.Duration) (domain.SegmentMigrationRun, segmentMigrationConfig, error) {
	// The immutable envelope is stored in SQLite. Authenticate it through a
	// small code-fixed bootstrap envelope, never through caller-provided caps.
	if info, err := os.Stat(d.Path() + "-wal"); err == nil && info.Size() > segmentMigrationBootstrapMaxWAL {
		return domain.SegmentMigrationRun{}, segmentMigrationConfig{}, segmentMigrationLimitError()
	} else if err != nil && !errors.Is(err, os.ErrNotExist) {
		return domain.SegmentMigrationRun{}, segmentMigrationConfig{}, err
	}
	bootstrapCtx, cancel := context.WithTimeout(ctx, min(wallTime, segmentMigrationBootstrapTime))
	defer cancel()
	return d.loadMigrationConfig(bootstrapCtx, id)
}

// AdvanceSegmentMigration advances one durable boundary (or one source page).
func (d *Database) AdvanceSegmentMigration(ctx context.Context, id string, b apptypes.SegmentMigrationBudget) (domain.SegmentMigrationRun, error) {
	if !b.Valid() {
		return domain.SegmentMigrationRun{}, apptypes.ErrCatalogLimit
	}
	opCtx, cancel := migrationOperationContext(ctx, b)
	defer cancel()
	if err := checkSegmentMigrationWAL(d.Path(), b); err != nil {
		return domain.SegmentMigrationRun{}, err
	}
	run, _, err := d.loadMigrationConfigBounded(opCtx, id, b.WallTime)
	if err != nil {
		return run, err
	}
	action, ok := run.NextAction()
	if !ok {
		if run.Phase == domain.SegmentMigrationVerifiedShadow {
			return run, nil
		}
		return run, domain.ErrSegmentMigrationTransition
	}
	return d.ExecuteSegmentMigrationAction(opCtx, id, action, b)
}

// ExecuteSegmentMigrationAction executes the application-selected bounded action.
func (d *Database) ExecuteSegmentMigrationAction(ctx context.Context, id string, action domain.SegmentMigrationAction, b apptypes.SegmentMigrationBudget) (domain.SegmentMigrationRun, error) {
	if !b.Valid() {
		return domain.SegmentMigrationRun{}, apptypes.ErrCatalogLimit
	}
	opCtx, cancel := migrationOperationContext(ctx, b)
	defer cancel()
	// WAL must be bounded before opening SQLite; an oversized WAL can make even
	// the envelope metadata read unsafe or fail with an untyped I/O error.
	if err := checkSegmentMigrationWAL(d.Path(), b); err != nil {
		return domain.SegmentMigrationRun{}, err
	}
	bootstrapCtx, bootstrapCancel := context.WithTimeout(opCtx, min(b.WallTime, segmentMigrationBootstrapTime))
	defer bootstrapCancel()
	if info, statErr := os.Stat(d.Path() + "-wal"); statErr == nil && info.Size() > segmentMigrationBootstrapMaxWAL {
		return domain.SegmentMigrationRun{}, segmentMigrationLimitError()
	} else if statErr != nil && !errors.Is(statErr, os.ErrNotExist) {
		return domain.SegmentMigrationRun{}, statErr
	}
	run, cfg, err := d.loadMigrationConfig(bootstrapCtx, id)
	if err != nil {
		return run, err
	}
	if !b.Tightens(cfg.envelope) {
		return run, apptypes.ErrSegmentMigrationEnvelopeExpansion
	}
	if err = checkSegmentMigrationWAL(d.Path(), b); err != nil {
		return run, err
	}
	release, err := d.acquireSegmentMigrationLease(opCtx, b.LockTime)
	if err != nil {
		return run, err
	}
	defer release()
	run, cfg, err = d.loadMigrationConfig(opCtx, id)
	if err != nil {
		return run, err
	}
	if !b.Tightens(cfg.envelope) {
		return run, apptypes.ErrSegmentMigrationEnvelopeExpansion
	}
	if err = d.validateMigrationBudgetAgainstPlan(opCtx, run, cfg, b); err != nil {
		return run, err
	}
	want, ok := run.NextAction()
	if !ok || want != action {
		return run, domain.ErrSegmentMigrationTransition
	}
	switch action {
	case domain.SegmentMigrationActionBeginCopy:
		return d.advanceMigrationPhase(opCtx, run, domain.SegmentMigrationCopying, nil)
	case domain.SegmentMigrationActionCopyPage:
		return d.copyMigrationPage(opCtx, run, b)
	case domain.SegmentMigrationActionBuildCandidate:
		return d.buildMigrationCandidate(opCtx, run, cfg, b)
	case domain.SegmentMigrationActionRecordInstallIntent:
		return d.recordInstallIntent(opCtx, run)
	case domain.SegmentMigrationActionInstall:
		return d.installMigrationCandidate(opCtx, run, cfg, b)
	case domain.SegmentMigrationActionRecordSealIntent:
		return d.advanceMigrationPhase(opCtx, run, domain.SegmentMigrationSealIntent, nil)
	case domain.SegmentMigrationActionSeal:
		return d.sealMigrationCatalog(opCtx, run, b)
	case domain.SegmentMigrationActionRecordVerifyIntent:
		return d.advanceMigrationPhase(opCtx, run, domain.SegmentMigrationVerifyIntent, nil)
	case domain.SegmentMigrationActionVerify:
		return d.verifyMigrationShadow(opCtx, run, cfg, b)
	default:
		return run, domain.ErrSegmentMigrationTransition
	}
}

func (d *Database) validateMigrationBudgetAgainstPlan(ctx context.Context, run domain.SegmentMigrationRun, cfg segmentMigrationConfig, b apptypes.SegmentMigrationBudget) error {
	if !b.Tightens(cfg.envelope) {
		return apptypes.ErrSegmentMigrationEnvelopeExpansion
	}
	db, err := d.openReadOnly(ctx)
	if err != nil {
		return err
	}
	defer d.release(db)
	var maxRows int
	var maxPlain, maxStored, maxFile int64
	if err = db.QueryRowContext(ctx, `SELECT max_rows,max_canonical_plain_bytes,max_stored_upper_bytes,max_file_bytes FROM archive_segment_target_plans WHERE reservation_id=? AND plan_digest=?`, run.ReservationID, run.PlanDigest).Scan(&maxRows, &maxPlain, &maxStored, &maxFile); err != nil {
		return apptypes.ErrSegmentMigrationStaleSource
	}
	if b.PageRows > maxRows || b.MaxPlainBytes > maxPlain || b.MaxStoredBytes > maxStored || b.MaxFileBytes > maxFile {
		return apptypes.ErrCatalogLimit
	}
	return nil
}

func (d *Database) loadMigrationConfig(ctx context.Context, id string) (domain.SegmentMigrationRun, segmentMigrationConfig, error) {
	db, err := d.openReadOnly(ctx)
	if err != nil {
		return domain.SegmentMigrationRun{}, segmentMigrationConfig{}, err
	}
	defer d.release(db)
	return scanSegmentMigration(db.QueryRowContext(ctx, loadSegmentMigrationSQL, strings.TrimSpace(id)))
}

func (d *Database) advanceMigrationPhase(ctx context.Context, run domain.SegmentMigrationRun, to domain.SegmentMigrationPhase, mutate func(*domain.SegmentMigrationRun)) (domain.SegmentMigrationRun, error) {
	next, err := run.Advance(to)
	if err != nil {
		return run, err
	}
	if mutate != nil {
		mutate(&next)
	}
	db, err := d.open(ctx)
	if err != nil {
		return run, err
	}
	defer d.release(db)
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return run, err
	}
	defer func() { _ = tx.Rollback() }()
	if err = appendSegmentMigration(ctx, tx, run, next); err != nil {
		return run, err
	}
	if err = tx.Commit(); err != nil {
		return run, err
	}
	return next, nil
}

func (d *Database) copyMigrationPage(ctx context.Context, run domain.SegmentMigrationRun, b apptypes.SegmentMigrationBudget) (domain.SegmentMigrationRun, error) {
	if err := checkSegmentMigrationResources(d.Path(), filepath.Dir(d.Path()), b); err != nil {
		return run, err
	}
	db, err := d.open(ctx)
	if err != nil {
		return run, err
	}
	defer d.release(db)
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return run, err
	}
	defer func() { _ = tx.Rollback() }()
	end := min64(run.Range.End, run.NextSequence+int64(b.PageRows)-1)
	h := sha256.New()
	plain := int64(0)
	pageUnits := make([]domain.HistoryUnit, 0, end-run.NextSequence+1)
	for seq := run.NextSequence; seq <= end; seq++ {
		if err = ctx.Err(); err != nil {
			return run, err
		}
		unit, e := hydrateSegmentTargetUnit(ctx, tx, seq)
		if e != nil {
			return run, e
		}
		encoded, e := unit.CanonicalBytes()
		if e != nil {
			return run, e
		}
		sum := sha256.Sum256(encoded)
		var want string
		var bytes int64
		if e = tx.QueryRowContext(ctx, `SELECT canonical_digest,canonical_bytes FROM archive_segment_target_plan_units WHERE reservation_id=? AND sequence=?`, run.ReservationID, seq).Scan(&want, &bytes); e != nil || want != hex.EncodeToString(sum[:]) || bytes != int64(len(encoded)) {
			return run, apptypes.ErrSegmentMigrationStaleSource
		}
		plain += int64(len(encoded))
		pageUnits = append(pageUnits, unit)
		if plain > b.MaxPlainBytes-run.CopiedPlainBytes {
			return run, segmentMigrationLimitError()
		}
		_, _ = h.Write(encoded)
	}
	candidateRoot, candidateDevice, candidateInode, rootErr := cfgCandidateRootFromTx(ctx, tx, run.ID)
	if rootErr != nil {
		return run, rootErr
	}
	device, inode, rootErr := segmentRootIdentity(candidateRoot)
	if rootErr != nil || device != candidateDevice || inode != candidateInode {
		return run, apptypes.ErrSegmentMigrationOrientation
	}
	if err = checkSegmentMigrationResources(d.Path(), candidateRoot, b); err != nil {
		return run, err
	}
	candidateDir := migrationRunDir(candidateRoot, run.ID)
	if err = checkMigrationCandidateFiles(candidateDir, b); err != nil {
		return run, err
	}
	next, err := run.CheckpointPage(domain.SegmentMigrationPageProof{NextSequence: end + 1, Rows: end + 1 - run.Range.Start, PlainBytes: run.CopiedPlainBytes + plain})
	if err != nil {
		return run, err
	}
	pageDigest := hex.EncodeToString(h.Sum(nil))
	var pageNumber int64
	if err = tx.QueryRowContext(ctx, `SELECT COALESCE(max(page_number)+1,0) FROM archive_segment_migration_pages WHERE run_id=?`, run.ID).Scan(&pageNumber); err != nil {
		return run, err
	}
	durableJournal := func() error {
		if checkErr := checkMigrationCandidateFiles(candidateDir, b); checkErr != nil {
			return checkErr
		}
		if _, execErr := tx.ExecContext(ctx, `INSERT INTO archive_segment_migration_pages(run_id,page_number,start_sequence,end_sequence,rows_copied,plain_bytes,page_digest) VALUES(?,?,?,?,?,?,?)`, run.ID, pageNumber, run.NextSequence, end, end-run.NextSequence+1, plain, pageDigest); execErr != nil {
			return execErr
		}
		if appendErr := appendSegmentMigration(ctx, tx, run, next); appendErr != nil {
			return appendErr
		}
		return tx.Commit()
	}
	if err = appendMigrationCandidatePage(ctx, candidateDir, candidateDevice, candidateInode, pageUnits, b, d.runSegmentMigrationHook, durableJournal); err != nil {
		return run, err
	}
	return next, nil
}

func checkMigrationCandidateFiles(dir string, b apptypes.SegmentMigrationBudget) error {
	for _, item := range []struct {
		name string
		cap  int64
	}{{migrationCandidateWorkPath(dir), b.MaxFileBytes}, {migrationCandidateWorkPath(dir) + "-wal", b.MaxWALBytes}} {
		info, err := os.Lstat(item.name)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
			return apptypes.ErrSegmentMigrationOrientation
		}
		if info.Size() > item.cap {
			return segmentMigrationLimitError()
		}
	}
	return nil
}

func cfgCandidateRootFromTx(ctx context.Context, tx *sql.Tx, runID string) (string, string, string, error) {
	var root, device, inode string
	err := tx.QueryRowContext(ctx, `SELECT candidate_root,candidate_root_device,candidate_root_inode FROM archive_segment_migration_runs WHERE run_id=? ORDER BY revision DESC LIMIT 1`, runID).Scan(&root, &device, &inode)
	return root, device, inode, err
}

const migrationCandidatePageSchema = `CREATE TABLE IF NOT EXISTS candidate_units(sequence INTEGER PRIMARY KEY, canonical_bytes BLOB NOT NULL, canonical_digest BLOB NOT NULL);`

func migrationCandidateWorkPath(dir string) string {
	return filepath.Join(dir, ".paged-candidate.sqlite")
}

func appendMigrationCandidatePage(ctx context.Context, dir, expectedRootDevice, expectedRootInode string, units []domain.HistoryUnit, b apptypes.SegmentMigrationBudget, hook func(string) error, durableJournal func() error) error {
	if dir == "" || len(units) == 0 {
		return apptypes.ErrSegmentMigrationOrientation
	}
	if hook != nil {
		if err := hook("before_candidate_root_pin"); err != nil {
			return err
		}
	}
	parentFD, err := unix.Open(filepath.Dir(dir), unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0)
	if err != nil {
		return err
	}
	defer func() { _ = unix.Close(parentFD) }()
	var pinnedParent unix.Stat_t
	if err = unix.Fstat(parentFD, &pinnedParent); err != nil {
		return err
	}
	if fmt.Sprint(pinnedParent.Dev) != expectedRootDevice || fmt.Sprint(pinnedParent.Ino) != expectedRootInode {
		return fmt.Errorf("%w: candidate root held=%d/%d expected=%s/%s", apptypes.ErrSegmentMigrationOrientation, pinnedParent.Dev, pinnedParent.Ino, expectedRootDevice, expectedRootInode)
	}
	childName := filepath.Base(dir)
	childFD, err := unix.Openat(parentFD, childName, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0)
	if errors.Is(err, unix.ENOENT) {
		if err = unix.Mkdirat(parentFD, childName, 0o700); err != nil {
			return err
		}
		if err = unix.Fsync(parentFD); err != nil {
			return err
		}
		childFD, err = unix.Openat(parentFD, childName, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0)
	}
	if err != nil {
		return apptypes.ErrSegmentMigrationOrientation
	}
	defer func() { _ = unix.Close(childFD) }()
	var pinnedChild unix.Stat_t
	if err = unix.Fstat(childFD, &pinnedChild); err != nil {
		return err
	}
	path := migrationCandidateWorkPath(dir)
	workName := filepath.Base(path)
	workFD, err := unix.Openat(childFD, workName, unix.O_RDWR|unix.O_CREAT|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0o600)
	if err != nil {
		return err
	}
	defer func() { _ = unix.Close(workFD) }()
	var workStat unix.Stat_t
	if err = unix.Fstat(workFD, &workStat); err != nil || workStat.Mode&unix.S_IFMT != unix.S_IFREG {
		return apptypes.ErrSegmentMigrationOrientation
	}
	if hook != nil {
		if err = hook("before_candidate_sqlite_io"); err != nil {
			return err
		}
	}
	if workStat.Size < 0 || workStat.Size > b.MaxFileBytes {
		return segmentMigrationLimitError()
	}
	initial, err := readBoundedFD(workFD, b.MaxFileBytes)
	if err != nil {
		return err
	}
	// Candidate pages are edited in-memory and atomically serialized through
	// openat beneath the pinned run directory. SQLite never receives a writable
	// pathname, eliminating the root/work-file exchange window and WAL sidecars.
	db, err := openSerializedSQLite(ctx, initial)
	if err != nil {
		return err
	}
	defer func() { _ = db.Close() }()
	if len(initial) == 0 {
		_, err = db.ExecContext(ctx, migrationCandidatePageSchema)
	}
	if err != nil {
		return err
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	for _, unit := range units {
		encoded, encodeErr := unit.CanonicalBytes()
		if encodeErr != nil {
			return encodeErr
		}
		if int64(len(encoded)) > b.MaxValuePlainBytes {
			return segmentMigrationLimitError()
		}
		digest := sha256.Sum256(encoded)
		result, insertErr := tx.ExecContext(ctx, `INSERT INTO candidate_units(sequence,canonical_bytes,canonical_digest) VALUES(?,?,?) ON CONFLICT(sequence) DO UPDATE SET canonical_bytes=excluded.canonical_bytes,canonical_digest=excluded.canonical_digest WHERE candidate_units.canonical_digest=excluded.canonical_digest`, unit.Sequence, encoded, digest[:])
		if insertErr != nil {
			return insertErr
		}
		if affected, affectedErr := result.RowsAffected(); affectedErr != nil || affected != 1 {
			return apptypes.ErrSegmentMigrationOrientation
		}
	}
	if err = tx.Commit(); err != nil {
		return err
	}
	serialized, err := serializeSQLite(ctx, db)
	if err != nil {
		return err
	}
	if int64(len(serialized)) > b.MaxFileBytes {
		return segmentMigrationLimitError()
	}
	if err = db.Close(); err != nil {
		return err
	}
	newWorkFD, err := publishSerializedCandidateAt(childFD, workName, serialized, hook)
	if err != nil {
		return err
	}
	defer func() { _ = unix.Close(newWorkFD) }()
	if hook != nil {
		if err = hook("before_candidate_fd_verify"); err != nil {
			return err
		}
	}
	if err = verifyMigrationCandidatePageFD(ctx, newWorkFD, units, b); err != nil {
		return err
	}
	var currentChild, candidateFile unix.Stat_t
	if err = unix.Fstatat(parentFD, childName, &currentChild, unix.AT_SYMLINK_NOFOLLOW); err != nil || currentChild.Dev != pinnedChild.Dev || currentChild.Ino != pinnedChild.Ino {
		return apptypes.ErrSegmentMigrationOrientation
	}
	var newWorkStat unix.Stat_t
	if err = unix.Fstat(newWorkFD, &newWorkStat); err != nil {
		return err
	}
	if err = unix.Fstatat(childFD, workName, &candidateFile, unix.AT_SYMLINK_NOFOLLOW); err != nil || candidateFile.Mode&unix.S_IFMT != unix.S_IFREG || candidateFile.Dev != newWorkStat.Dev || candidateFile.Ino != newWorkStat.Ino {
		return apptypes.ErrSegmentMigrationOrientation
	}
	err = unix.Fsync(childFD)
	if err == nil && hook != nil {
		err = hook("candidate_page_durable")
	}
	if err != nil {
		return err
	}
	currentParentFD, openErr := unix.Open(filepath.Dir(dir), unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0)
	if openErr != nil {
		return apptypes.ErrSegmentMigrationOrientation
	}
	defer func() { _ = unix.Close(currentParentFD) }()
	var currentParent unix.Stat_t
	if err = unix.Fstat(currentParentFD, &currentParent); err != nil || currentParent.Dev != pinnedParent.Dev || currentParent.Ino != pinnedParent.Ino {
		return apptypes.ErrSegmentMigrationOrientation
	}
	if durableJournal == nil {
		return apptypes.ErrSegmentMigrationOrientation
	}
	return durableJournal()
}

func readBoundedFD(fd int, capBytes int64) ([]byte, error) {
	if capBytes <= 0 {
		return nil, segmentMigrationLimitError()
	}
	dup, err := unix.Dup(fd)
	if err != nil {
		return nil, err
	}
	file := os.NewFile(uintptr(dup), "candidate-snapshot")
	defer func() { _ = file.Close() }()
	if _, err = file.Seek(0, 0); err != nil {
		return nil, err
	}
	data, err := io.ReadAll(io.LimitReader(file, capBytes+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > capBytes {
		return nil, segmentMigrationLimitError()
	}
	return data, nil
}

func publishSerializedCandidateAt(dirFD int, name string, serialized []byte, hook func(string) error) (int, error) {
	const tempSuffix = ".serializing"
	tempName := name + tempSuffix
	tempFD, err := unix.Openat(dirFD, tempName, unix.O_RDWR|unix.O_CREAT|unix.O_EXCL|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0o600)
	if errors.Is(err, unix.EEXIST) {
		var stat unix.Stat_t
		if statErr := unix.Fstatat(dirFD, tempName, &stat, unix.AT_SYMLINK_NOFOLLOW); statErr != nil || stat.Mode&unix.S_IFMT != unix.S_IFREG {
			return -1, apptypes.ErrSegmentMigrationOrientation
		}
		if unlinkErr := unix.Unlinkat(dirFD, tempName, 0); unlinkErr != nil {
			return -1, unlinkErr
		}
		tempFD, err = unix.Openat(dirFD, tempName, unix.O_RDWR|unix.O_CREAT|unix.O_EXCL|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0o600)
	}
	if err != nil {
		return -1, err
	}
	keep := false
	defer func() {
		if !keep {
			_ = unix.Close(tempFD)
			_ = unix.Unlinkat(dirFD, tempName, 0)
		}
	}()
	for written := 0; written < len(serialized); {
		n, writeErr := unix.Write(tempFD, serialized[written:])
		if writeErr != nil {
			return -1, writeErr
		}
		if n <= 0 {
			return -1, io.ErrShortWrite
		}
		written += n
	}
	if err = unix.Fsync(tempFD); err != nil {
		return -1, err
	}
	if hook != nil {
		if err = hook("after_candidate_serialize_fsync"); err != nil {
			return -1, err
		}
	}
	var openedTemp, currentTemp unix.Stat_t
	if err = unix.Fstat(tempFD, &openedTemp); err != nil {
		return -1, err
	}
	if err = unix.Fstatat(dirFD, tempName, &currentTemp, unix.AT_SYMLINK_NOFOLLOW); err != nil || openedTemp.Mode&unix.S_IFMT != unix.S_IFREG || currentTemp.Mode&unix.S_IFMT != unix.S_IFREG || openedTemp.Dev != currentTemp.Dev || openedTemp.Ino != currentTemp.Ino {
		return -1, apptypes.ErrSegmentMigrationOrientation
	}
	if err = unix.Renameat(dirFD, tempName, dirFD, name); err != nil {
		return -1, err
	}
	if err = unix.Fsync(dirFD); err != nil {
		return -1, err
	}
	keep = true
	return tempFD, nil
}

func verifyMigrationCandidatePageFD(ctx context.Context, workFD int, units []domain.HistoryUnit, b apptypes.SegmentMigrationBudget) error {
	var stat unix.Stat_t
	if err := unix.Fstat(workFD, &stat); err != nil {
		return err
	}
	if stat.Size < 0 || stat.Size > b.MaxFileBytes {
		return segmentMigrationLimitError()
	}
	db, err := sql.Open("sqlite", segmentSQLiteDSN(filepath.Join("/dev/fd", fmt.Sprint(workFD)), "ro", true))
	if err != nil {
		return err
	}
	defer func() { _ = db.Close() }()
	for _, unit := range units {
		encoded, encodeErr := unit.CanonicalBytes()
		if encodeErr != nil {
			return encodeErr
		}
		digest := sha256.Sum256(encoded)
		var storedBytesLen, storedDigestLen int64
		if err = db.QueryRowContext(ctx, `SELECT length(canonical_bytes),length(canonical_digest) FROM candidate_units WHERE sequence=?`, unit.Sequence).Scan(&storedBytesLen, &storedDigestLen); err != nil {
			return apptypes.ErrSegmentMigrationOrientation
		}
		if storedBytesLen != int64(len(encoded)) || storedBytesLen > b.MaxValuePlainBytes || storedDigestLen != sha256.Size {
			return segmentMigrationLimitError()
		}
		var storedBytes, storedDigest []byte
		if err = db.QueryRowContext(ctx, `SELECT canonical_bytes,canonical_digest FROM candidate_units WHERE sequence=?`, unit.Sequence).Scan(&storedBytes, &storedDigest); err != nil {
			return apptypes.ErrSegmentMigrationOrientation
		}
		storedSum := sha256.Sum256(storedBytes)
		if !equalBytes(storedBytes, encoded) || !equalBytes(storedDigest, storedSum[:]) || !equalBytes(storedSum[:], digest[:]) {
			return apptypes.ErrSegmentMigrationOrientation
		}
	}
	return nil
}

func migrationRunDir(root, id string) string {
	sum := sha256.Sum256([]byte(id))
	return filepath.Join(root, ".migration-"+hex.EncodeToString(sum[:16]))
}

func recoverOwnedSegmentCandidateFD(ctx context.Context, dirFD int, limits ArchiveSegmentLimits) (ArchiveSegmentManifest, bool, error) {
	readFD, err := unix.Dup(dirFD)
	if err != nil {
		return ArchiveSegmentManifest{}, false, err
	}
	directory := os.NewFile(uintptr(readFD), "candidate-run")
	entries, err := directory.ReadDir(-1)
	_ = directory.Close()
	if err != nil {
		return ArchiveSegmentManifest{}, false, err
	}
	var sealed string
	for _, entry := range entries {
		name := entry.Name()
		if name == filepath.Base(migrationCandidateWorkPath("")) {
			var stat unix.Stat_t
			if err = unix.Fstatat(dirFD, name, &stat, unix.AT_SYMLINK_NOFOLLOW); err != nil || stat.Mode&unix.S_IFMT != unix.S_IFREG {
				return ArchiveSegmentManifest{}, false, apptypes.ErrSegmentMigrationOrientation
			}
			continue
		}
		if segmentBasenameRE.MatchString(name) {
			if sealed != "" {
				return ArchiveSegmentManifest{}, false, apptypes.ErrSegmentMigrationOrientation
			}
			sealed = name
			continue
		}
		if strings.HasPrefix(name, ".segment-v1-") && strings.HasSuffix(name, ".candidate") {
			var stat unix.Stat_t
			if err = unix.Fstatat(dirFD, name, &stat, unix.AT_SYMLINK_NOFOLLOW); err != nil || stat.Mode&unix.S_IFMT != unix.S_IFREG {
				return ArchiveSegmentManifest{}, false, apptypes.ErrSegmentMigrationOrientation
			}
			if err = unix.Unlinkat(dirFD, name, 0); err != nil {
				return ArchiveSegmentManifest{}, false, err
			}
			continue
		}
		return ArchiveSegmentManifest{}, false, apptypes.ErrSegmentMigrationOrientation
	}
	if err = unix.Fsync(dirFD); err != nil {
		return ArchiveSegmentManifest{}, false, err
	}
	if sealed == "" {
		return ArchiveSegmentManifest{}, false, nil
	}
	sealedFD, err := unix.Openat(dirFD, sealed, unix.O_RDONLY|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0)
	if err != nil {
		return ArchiveSegmentManifest{}, false, apptypes.ErrSegmentMigrationOrientation
	}
	defer func() { _ = unix.Close(sealedFD) }()
	manifest, err := inspectArchiveSegmentFD(ctx, sealedFD, sealed, limits.MaxFileBytes)
	if err != nil {
		return ArchiveSegmentManifest{}, false, err
	}
	dupFD, err := unix.Dup(sealedFD)
	if err != nil {
		return ArchiveSegmentManifest{}, false, err
	}
	file := os.NewFile(uintptr(dupFD), sealed)
	manifest.FileDigest, err = digestOpenFile(ctx, file, limits.MaxFileBytes)
	_ = file.Close()
	return manifest, err == nil, err
}

type migrationCandidateSource struct {
	db    *sql.DB
	start int64
	count int
	hook  func(string) error
}

func (s migrationCandidateSource) Len() int { return s.count }
func (s migrationCandidateSource) At(ctx context.Context, index int) (ArchiveHistoryUnit, error) {
	if index < 0 || index >= s.count {
		return ArchiveHistoryUnit{}, ErrSegmentCorrupt
	}
	var encoded, digest []byte
	if err := s.db.QueryRowContext(ctx, `SELECT canonical_bytes,canonical_digest FROM candidate_units WHERE sequence=?`, s.start+int64(index)).Scan(&encoded, &digest); err != nil {
		return ArchiveHistoryUnit{}, fmt.Errorf("%w: candidate sequence %d: %v", apptypes.ErrSegmentMigrationOrientation, s.start+int64(index), err)
	}
	sum := sha256.Sum256(encoded)
	if !equalBytes(sum[:], digest) {
		return ArchiveHistoryUnit{}, fmt.Errorf("%w: candidate digest at %d", apptypes.ErrSegmentMigrationOrientation, s.start+int64(index))
	}
	unit, err := domain.DecodeHistoryUnitCanonical(encoded)
	if err != nil || unit.Sequence != uint64(s.start+int64(index)) {
		return ArchiveHistoryUnit{}, fmt.Errorf("%w: candidate decode at %d: %v", apptypes.ErrSegmentMigrationOrientation, s.start+int64(index), err)
	}
	if s.hook != nil {
		if err = s.hook("candidate_final_reread"); err != nil {
			return ArchiveHistoryUnit{}, err
		}
	}
	return ArchiveHistoryUnit{Unit: unit}, nil
}

func equalBytes(a, b []byte) bool {
	if len(a) != len(b) {
		return false
	}
	var diff byte
	for i := range a {
		diff |= a[i] ^ b[i]
	}
	return diff == 0
}

func (d *Database) buildMigrationCandidate(ctx context.Context, run domain.SegmentMigrationRun, cfg segmentMigrationConfig, b apptypes.SegmentMigrationBudget) (domain.SegmentMigrationRun, error) {
	if err := verifySegmentRootIdentity(cfg); err != nil {
		return run, err
	}
	if err := checkSegmentMigrationResources(d.Path(), cfg.candidateRoot, b); err != nil {
		return run, err
	}
	db, err := d.openReadOnly(ctx)
	if err != nil {
		return run, err
	}
	defer d.release(db)
	candidateRootFD, err := unix.Open(cfg.candidateRoot, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0)
	if err != nil {
		return run, err
	}
	defer func() { _ = unix.Close(candidateRootFD) }()
	var candidateRootStat unix.Stat_t
	if err = unix.Fstat(candidateRootFD, &candidateRootStat); err != nil || fmt.Sprint(candidateRootStat.Dev) != cfg.candidateDevice || fmt.Sprint(candidateRootStat.Ino) != cfg.candidateInode {
		return run, apptypes.ErrSegmentMigrationOrientation
	}
	runDirName := filepath.Base(migrationRunDir(cfg.candidateRoot, run.ID))
	runDirFD, err := unix.Openat(candidateRootFD, runDirName, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0)
	if err != nil {
		return run, apptypes.ErrSegmentMigrationOrientation
	}
	defer func() { _ = unix.Close(runDirFD) }()
	var runDirStat unix.Stat_t
	if err = unix.Fstat(runDirFD, &runDirStat); err != nil {
		return run, err
	}
	workFD, err := unix.Openat(runDirFD, filepath.Base(migrationCandidateWorkPath("")), unix.O_RDONLY|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0)
	if err != nil {
		return run, apptypes.ErrSegmentMigrationOrientation
	}
	defer func() { _ = unix.Close(workFD) }()
	workDB, err := sql.Open("sqlite", segmentSQLiteDSN(filepath.Join("/dev/fd", fmt.Sprint(workFD)), "ro", true))
	if err != nil {
		return run, err
	}
	defer func() { _ = workDB.Close() }()
	var candidateCount int
	if err = workDB.QueryRowContext(ctx, `SELECT count(*) FROM candidate_units WHERE sequence BETWEEN ? AND ?`, run.Range.Start, run.Range.End).Scan(&candidateCount); err != nil {
		return run, fmt.Errorf("inspect paged candidate: %w", err)
	}
	if int64(candidateCount) != run.CopiedRows {
		return run, fmt.Errorf("%w: candidate rows=%d journal rows=%d", apptypes.ErrSegmentMigrationOrientation, candidateCount, run.CopiedRows)
	}
	source := migrationCandidateSource{db: workDB, start: run.Range.Start, count: candidateCount, hook: d.runSegmentMigrationHook}
	h := sha256.New()
	_, _ = h.Write([]byte("traceary-segment-target-source-v1\x00"))
	var filterKey []byte
	if err = db.QueryRowContext(ctx, `SELECT filter_key FROM archive_store_lineage WHERE singleton=1`).Scan(&filterKey); err != nil {
		return run, err
	}
	defer func() { clear(filterKey) }()
	keyIdentity := sha256.Sum256(filterKey)
	filterKeyID := "store-filter-v1-" + hex.EncodeToString(keyIdentity[:8])
	summaryCfg := domain.SegmentSummaryGeneratorConfig{FilterKeyID: filterKeyID, HMACVersion: domain.SegmentSummaryHMACV1, MaxUnits: int(run.CopiedRows), MaxDistinctPerKind: int(run.CopiedRows), MaxSessions: int(run.CopiedRows), BloomBitCount: 65536, BloomHashCount: 7, BloomMaxSetPermille: 800}
	parts := make([]domain.SegmentCatalogSummaryV1, 0, (source.Len()+b.PageRows-1)/b.PageRows)
	for pageStart := 0; pageStart < source.Len(); pageStart += b.PageRows {
		pageEnd := min(pageStart+b.PageRows, source.Len())
		page := make([]domain.HistoryUnit, 0, pageEnd-pageStart)
		for index := pageStart; index < pageEnd; index++ {
			item, e := source.At(ctx, index)
			if e != nil {
				return run, e
			}
			encoded, e := item.Unit.CanonicalBytes()
			if e != nil {
				return run, e
			}
			var frame [8]byte
			putUint64(frame[:], uint64(len(encoded)))
			_, _ = h.Write(frame[:])
			_, _ = h.Write(encoded)
			page = append(page, item.Unit)
		}
		part, e := domain.GenerateSegmentCatalogSummaryV1(page, filterKey, summaryCfg)
		if e != nil {
			return run, e
		}
		parts = append(parts, part)
	}
	var expectedSource string
	if err = db.QueryRowContext(ctx, `SELECT source_digest FROM archive_segment_target_plans WHERE reservation_id=?`, run.ReservationID).Scan(&expectedSource); err != nil {
		return run, err
	}
	sourceDigest := hex.EncodeToString(h.Sum(nil))
	if sourceDigest != expectedSource {
		return run, apptypes.ErrSegmentMigrationStaleSource
	}
	summary, err := domain.MergeSegmentCatalogSummariesV1(parts, summaryCfg)
	if err != nil {
		return run, err
	}
	dir := migrationRunDir(cfg.candidateRoot, run.ID)
	manifest, recovered, err := recoverOwnedSegmentCandidateFD(ctx, runDirFD, migrationLimits(b))
	if err != nil {
		return run, err
	}
	if !recovered {
		builder := archiveSegmentBuilder{
			syncFile:     func(f *os.File) error { return f.Sync() },
			sealFile:     func(f *os.File, mode os.FileMode) error { return f.Chmod(mode) },
			beforeSQLite: func() error { return d.runSegmentMigrationHook("before_segment_candidate_sqlite_io") },
		}
		manifest, err = builder.buildSource(ctx, dir, source, ArchiveSegmentConfig{StoreID: run.StoreID, CompressionFloor: cfg.compressionFloor, Limits: migrationLimits(b), Summary: summary})
		if err != nil {
			return run, err
		}
	}
	if err = d.runSegmentMigrationHook("after_candidate_build"); err != nil {
		return run, err
	}
	sealedFD, openErr := unix.Openat(runDirFD, manifest.Basename, unix.O_RDONLY|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0)
	if openErr != nil {
		return run, apptypes.ErrSegmentMigrationOrientation
	}
	defer func() { _ = unix.Close(sealedFD) }()
	digestFD, dupErr := unix.Dup(sealedFD)
	if dupErr != nil {
		return run, dupErr
	}
	digestFile := os.NewFile(uintptr(digestFD), manifest.Basename)
	exactDigest, digestErr := digestOpenFile(ctx, digestFile, b.MaxFileBytes)
	_ = digestFile.Close()
	if digestErr != nil || exactDigest != manifest.FileDigest {
		return run, apptypes.ErrSegmentMigrationOrientation
	}
	verifiedManifest, verifyErr := verifyArchiveSegmentFDWithHooks(ctx, sealedFD, manifest, migrationLimits(b), true, nil, nil)
	if verifyErr != nil || !sameLogicalManifest(verifiedManifest, manifest) {
		return run, apptypes.ErrSegmentMigrationOrientation
	}
	summaryBytes, summaryErr := summary.CanonicalBytes(b.MaxSummaryBytes)
	if summaryErr != nil || manifest.StoreID != run.StoreID || manifest.StartSequence != uint64(run.Range.Start) || manifest.EndSequence != uint64(run.Range.End) || manifest.UnitCount != uint64(run.CopiedRows) || manifest.SummaryDigest != hex.EncodeToString(digestBytes(summaryBytes)) {
		return run, apptypes.ErrSegmentMigrationStaleSource
	}
	manifestDigest, err := ArchiveSegmentManifestDigest(manifest)
	if err != nil {
		return run, err
	}
	var currentRoot, currentRunDir unix.Stat_t
	if err = unix.Fstatat(candidateRootFD, runDirName, &currentRunDir, unix.AT_SYMLINK_NOFOLLOW); err != nil || currentRunDir.Mode&unix.S_IFMT != unix.S_IFDIR || currentRunDir.Dev != runDirStat.Dev || currentRunDir.Ino != runDirStat.Ino {
		return run, apptypes.ErrSegmentMigrationOrientation
	}
	currentRootFD, openErr := unix.Open(cfg.candidateRoot, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0)
	if openErr != nil {
		return run, apptypes.ErrSegmentMigrationOrientation
	}
	defer func() { _ = unix.Close(currentRootFD) }()
	if err = unix.Fstat(currentRootFD, &currentRoot); err != nil || currentRoot.Dev != candidateRootStat.Dev || currentRoot.Ino != candidateRootStat.Ino {
		return run, apptypes.ErrSegmentMigrationOrientation
	}
	next, err := run.RecordCandidateBuilt(domain.SegmentMigrationCandidateProof{SourceDigest: sourceDigest, Basename: manifest.Basename, ManifestDigest: manifestDigest, FileDigest: manifest.FileDigest})
	if err != nil {
		return run, err
	}
	return d.appendMigrationRevision(ctx, run, next)
}

func (d *Database) appendMigrationRevision(ctx context.Context, run, next domain.SegmentMigrationRun) (domain.SegmentMigrationRun, error) {
	db, err := d.open(ctx)
	if err != nil {
		return run, err
	}
	defer d.release(db)
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return run, err
	}
	defer func() { _ = tx.Rollback() }()
	if err = appendSegmentMigration(ctx, tx, run, next); err != nil {
		return run, err
	}
	if err = tx.Commit(); err != nil {
		return run, err
	}
	return next, nil
}

func putUint64(dst []byte, v uint64) {
	for i := 7; i >= 0; i-- {
		dst[i] = byte(v)
		v >>= 8
	}
}

func (d *Database) recordInstallIntent(ctx context.Context, run domain.SegmentMigrationRun) (domain.SegmentMigrationRun, error) {
	db, err := d.open(ctx)
	if err != nil {
		return run, err
	}
	defer d.release(db)
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return run, err
	}
	defer func() { _ = tx.Rollback() }()
	next, err := run.Advance(domain.SegmentMigrationInstallIntent)
	if err != nil {
		return run, err
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO archive_segment_migration_install_intents(run_id,candidate_basename,final_basename,file_digest,created_at) VALUES(?,?,?,?,?) ON CONFLICT(run_id) DO NOTHING`, run.ID, run.CandidateBasename, run.CandidateBasename, run.FileDigest, time.Now().UTC().Format(time.RFC3339Nano)); err != nil {
		return run, err
	}
	if err = appendSegmentMigration(ctx, tx, run, next); err != nil {
		return run, err
	}
	if err = tx.Commit(); err != nil {
		return run, err
	}
	return next, nil
}

func copyPinnedNoReplace(srcPath, rootPath, name string, maxBytes int64, device, inode string, hook func(string) error, critical func(func() error) error) error {
	return copyPinnedNoReplaceWithHook(srcPath, rootPath, name, maxBytes, device, inode, hook, critical)
}

func copyPinnedNoReplaceWithHook(srcPath, rootPath, name string, maxBytes int64, device, inode string, hook func(string) error, critical func(func() error) error) error {
	dirFD, err := unix.Open(rootPath, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0)
	if err != nil {
		return err
	}
	directory := os.NewFile(uintptr(dirFD), rootPath)
	defer func() { _ = directory.Close() }()
	info, statErr := directory.Stat()
	if statErr != nil {
		return statErr
	}
	actualDevice, actualInode, identityOK := physicalFileIdentity(info)
	if !identityOK || actualDevice != device || actualInode != inode {
		return apptypes.ErrSegmentMigrationOrientation
	}
	srcFD, err := unix.Open(srcPath, unix.O_RDONLY|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0)
	if err != nil {
		return err
	}
	src := os.NewFile(uintptr(srcFD), srcPath)
	defer func() { _ = src.Close() }()
	tmp := "." + name + ".installing"
	var tmpStat unix.Stat_t
	if statErr := unix.Fstatat(dirFD, tmp, &tmpStat, unix.AT_SYMLINK_NOFOLLOW); statErr == nil {
		if tmpStat.Mode&unix.S_IFMT != unix.S_IFREG {
			return apptypes.ErrSegmentMigrationOrientation
		}
		if err = unix.Unlinkat(dirFD, tmp, 0); err != nil {
			return err
		}
		if err = unix.Fsync(dirFD); err != nil {
			return err
		}
	} else if !errors.Is(statErr, unix.ENOENT) {
		return statErr
	}
	tmpFD, err := unix.Openat(dirFD, tmp, unix.O_WRONLY|unix.O_CREAT|unix.O_EXCL|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0o600)
	if err != nil {
		return err
	}
	dst := os.NewFile(uintptr(tmpFD), tmp)
	installed := false
	defer func() {
		_ = dst.Close()
		if !installed {
			_ = unix.Unlinkat(dirFD, tmp, 0)
		}
	}()
	written, err := io.Copy(dst, io.LimitReader(src, maxBytes+1))
	if err != nil || written > maxBytes {
		return segmentMigrationLimitError()
	}
	if err = dst.Sync(); err != nil {
		return err
	}
	if err = dst.Chmod(0o400); err != nil {
		return err
	}
	if err = dst.Sync(); err != nil {
		return err
	}
	if hook != nil {
		if err = hook("before_install_rename"); err != nil {
			return err
		}
	}
	publish := func() error {
		if renameErr := renameSegmentAtNoReplace(dirFD, tmp, name); renameErr != nil {
			return renameErr
		}
		if hook != nil {
			if hookErr := hook("after_install_rename"); hookErr != nil {
				return hookErr
			}
		}
		return unix.Fsync(dirFD) //nolint:wrapcheck // Preserve the publication fsync failure.
	}
	if critical != nil {
		if err = critical(publish); err != nil {
			return err
		}
	} else if err = publish(); err != nil {
		return err
	}
	installed = true
	return nil
}

func (d *Database) installMigrationCandidate(ctx context.Context, run domain.SegmentMigrationRun, cfg segmentMigrationConfig, b apptypes.SegmentMigrationBudget) (domain.SegmentMigrationRun, error) {
	if err := verifySegmentRootIdentity(cfg); err != nil {
		return run, err
	}
	if err := checkSegmentMigrationResources(d.Path(), cfg.archiveRoot, b); err != nil {
		return run, err
	}
	src := filepath.Join(migrationRunDir(cfg.candidateRoot, run.ID), run.CandidateBasename)
	candidateDigest, err := digestFile(src)
	if err != nil || candidateDigest != run.FileDigest {
		return run, apptypes.ErrSegmentMigrationOrientation
	}
	critical := func(publish func() error) error {
		return publish()
	}
	err = copyPinnedNoReplace(src, cfg.archiveRoot, run.CandidateBasename, b.MaxFileBytes, cfg.archiveDevice, cfg.archiveInode, d.runSegmentMigrationHook, critical)
	if errors.Is(err, os.ErrExist) {
		manifest, inspectErr := InspectArchiveSegmentManifest(ctx, cfg.archiveRoot, run.CandidateBasename, migrationLimits(b))
		if inspectErr == nil {
			manifest.FileDigest, inspectErr = digestFile(filepath.Join(cfg.archiveRoot, run.CandidateBasename))
		}
		if inspectErr != nil || manifest.FileDigest != run.FileDigest {
			return run, apptypes.ErrSegmentMigrationOrientation
		}
		if syncErr := fsyncPinnedMigrationRoot(cfg); syncErr != nil {
			return run, syncErr
		}
	} else if err != nil {
		return run, err
	}
	if _, err = InspectArchiveSegmentManifest(ctx, cfg.archiveRoot, run.CandidateBasename, migrationLimits(b)); err != nil {
		return run, err
	}
	db, e := d.open(ctx)
	if e != nil {
		return run, e
	}
	defer d.release(db)
	tx, e := db.BeginTx(ctx, nil)
	if e != nil {
		return run, e
	}
	defer func() { _ = tx.Rollback() }()
	next, e := run.Advance(domain.SegmentMigrationInstalled)
	if e != nil {
		return run, e
	}
	if _, e = tx.ExecContext(ctx, `UPDATE archive_segment_migration_install_intents SET completed=1 WHERE run_id=? AND completed=0`, run.ID); e != nil {
		return run, e
	}
	if e = appendSegmentMigration(ctx, tx, run, next); e != nil {
		return run, e
	}
	if e = tx.Commit(); e != nil {
		return run, e
	}
	return next, nil
}

func fsyncPinnedMigrationRoot(cfg segmentMigrationConfig) error {
	dirFD, err := unix.Open(cfg.archiveRoot, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0)
	if err != nil {
		return err
	}
	directory := os.NewFile(uintptr(dirFD), cfg.archiveRoot)
	defer func() { _ = directory.Close() }()
	info, err := directory.Stat()
	if err != nil {
		return err
	}
	device, inode, ok := physicalFileIdentity(info)
	if !ok || device != cfg.archiveDevice || inode != cfg.archiveInode {
		return apptypes.ErrSegmentMigrationOrientation
	}
	return unix.Fsync(dirFD) //nolint:wrapcheck // Preserve syscall failure for typed fault handling.
}

type verifiedInstalledSegment struct {
	rootFD, fileFD        int
	name                  string
	device, inode         uint64
	rootDevice, rootInode string
	rootPath              string
}

func pinInstalledSegment(cfg segmentMigrationConfig, name string) (*verifiedInstalledSegment, error) {
	rootFD, err := unix.Open(cfg.archiveRoot, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0)
	if err != nil {
		return nil, err
	}
	var rootStat unix.Stat_t
	if statErr := unix.Fstat(rootFD, &rootStat); statErr != nil {
		_ = unix.Close(rootFD)
		return nil, statErr
	}
	rootDevice, rootInode := fmt.Sprint(rootStat.Dev), fmt.Sprint(rootStat.Ino)
	if rootDevice != cfg.archiveDevice || rootInode != cfg.archiveInode {
		_ = unix.Close(rootFD)
		return nil, apptypes.ErrSegmentMigrationOrientation
	}
	fileFD, err := unix.Openat(rootFD, name, unix.O_RDONLY|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0)
	if err != nil {
		_ = unix.Close(rootFD)
		return nil, err
	}
	var stat unix.Stat_t
	if err = unix.Fstat(fileFD, &stat); err != nil || stat.Mode&unix.S_IFMT != unix.S_IFREG || stat.Mode&0o777 != 0o400 {
		_ = unix.Close(fileFD)
		_ = unix.Close(rootFD)
		return nil, apptypes.ErrSegmentMigrationOrientation
	}
	return &verifiedInstalledSegment{rootFD: rootFD, fileFD: fileFD, name: name, device: uint64(stat.Dev), inode: stat.Ino, rootDevice: rootDevice, rootInode: rootInode, rootPath: cfg.archiveRoot}, nil
}

func (p *verifiedInstalledSegment) Close() {
	_ = unix.Close(p.fileFD)
	_ = unix.Close(p.rootFD)
}

func (p *verifiedInstalledSegment) AssertCurrent() error {
	var rootStat, pinned, current unix.Stat_t
	if err := unix.Fstat(p.rootFD, &rootStat); err != nil {
		return err
	}
	if fmt.Sprint(rootStat.Dev) != p.rootDevice || fmt.Sprint(rootStat.Ino) != p.rootInode {
		return apptypes.ErrSegmentMigrationOrientation
	}
	currentRootFD, err := unix.Open(p.rootPath, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0)
	if err != nil {
		return apptypes.ErrSegmentMigrationOrientation
	}
	defer func() { _ = unix.Close(currentRootFD) }()
	var currentRoot unix.Stat_t
	if err = unix.Fstat(currentRootFD, &currentRoot); err != nil || fmt.Sprint(currentRoot.Dev) != p.rootDevice || fmt.Sprint(currentRoot.Ino) != p.rootInode {
		return apptypes.ErrSegmentMigrationOrientation
	}
	if err := unix.Fstat(p.fileFD, &pinned); err != nil {
		return err
	}
	if err := unix.Fstatat(p.rootFD, p.name, &current, unix.AT_SYMLINK_NOFOLLOW); err != nil {
		return err
	}
	if pinned.Mode&unix.S_IFMT != unix.S_IFREG || current.Mode&unix.S_IFMT != unix.S_IFREG || pinned.Mode&0o777 != 0o400 || current.Mode&0o777 != 0o400 || uint64(pinned.Dev) != p.device || pinned.Ino != p.inode || uint64(current.Dev) != p.device || current.Ino != p.inode {
		return apptypes.ErrSegmentMigrationOrientation
	}
	return nil
}

func (p *verifiedInstalledSegment) Digest(ctx context.Context, maxBytes int64) (string, error) {
	dupFD, err := unix.Dup(p.fileFD)
	if err != nil {
		return "", err
	}
	file := os.NewFile(uintptr(dupFD), p.name)
	defer func() { _ = file.Close() }()
	return digestOpenFile(ctx, file, maxBytes)
}

func migrationEvidenceDigest(run domain.SegmentMigrationRun, label string) string {
	h := sha256.New()
	_, _ = h.Write([]byte("traceary-segment-migration-proof-v1\x00"))
	_, _ = h.Write([]byte(run.ID))
	_, _ = h.Write([]byte(run.PlanDigest))
	_, _ = h.Write([]byte(run.ManifestDigest))
	_, _ = h.Write([]byte(run.FileDigest))
	_, _ = h.Write([]byte(label))
	return hex.EncodeToString(h.Sum(nil))
}

func bindingFromManifest(m ArchiveSegmentManifest) (domain.CatalogSegmentBinding, error) {
	logical, err := hex.DecodeString(m.LogicalDigest)
	if err != nil || len(logical) != 32 {
		return domain.CatalogSegmentBinding{}, ErrSegmentCorrupt
	}
	var ld, fd, md, sd [32]byte
	copy(ld[:], logical)
	file, _ := hex.DecodeString(m.FileDigest)
	copy(fd[:], file)
	manifestDigest, err := ArchiveSegmentManifestDigest(m)
	if err != nil {
		return domain.CatalogSegmentBinding{}, err
	}
	raw, _ := hex.DecodeString(manifestDigest)
	copy(md[:], raw)
	summary, _ := hex.DecodeString(m.SummaryDigest)
	copy(sd[:], summary)
	identity, err := domain.NewSegmentIdentity(m.StoreID, m.StartSequence, m.EndSequence, ld)
	if err != nil {
		return domain.CatalogSegmentBinding{}, err
	}
	return domain.NewCatalogSegmentBinding(identity, fd, md, sd)
}

func (d *Database) verifyMigrationSegmentProof(ctx context.Context, run domain.SegmentMigrationRun, pinned *verifiedInstalledSegment, manifest ArchiveSegmentManifest, limits ArchiveSegmentLimits) error {
	if manifest.StoreID != run.StoreID || manifest.StartSequence != uint64(run.Range.Start) || manifest.EndSequence != uint64(run.Range.End) || manifest.UnitCount != uint64(run.CopiedRows) || manifest.Basename != run.SegmentID {
		return apptypes.ErrSegmentMigrationStaleSource
	}
	proofDB, err := d.openReadOnly(ctx)
	if err != nil {
		return err
	}
	defer d.release(proofDB)
	_, err = verifyArchiveSegmentFDWithHooks(ctx, pinned.fileFD, manifest, limits, true, nil, func(unit domain.HistoryUnit, plain []byte) error {
		sum := sha256.Sum256(plain)
		var want string
		var bytes int64
		if queryErr := proofDB.QueryRowContext(ctx, `SELECT canonical_digest,canonical_bytes FROM archive_segment_target_plan_units WHERE reservation_id=? AND sequence=?`, run.ReservationID, unit.Sequence).Scan(&want, &bytes); queryErr != nil || want != hex.EncodeToString(sum[:]) || bytes != int64(len(plain)) {
			return apptypes.ErrSegmentMigrationStaleSource
		}
		return nil
	})
	return err
}

func (d *Database) sealMigrationCatalog(ctx context.Context, run domain.SegmentMigrationRun, b apptypes.SegmentMigrationBudget) (domain.SegmentMigrationRun, error) {
	cfgRun, cfg, err := d.loadMigrationConfig(ctx, run.ID)
	if err != nil || cfgRun.Revision != run.Revision {
		return run, apptypes.ErrSegmentMigrationConflict
	}
	if err = verifySegmentRootIdentity(cfg); err != nil {
		return run, err
	}
	pinned, err := pinInstalledSegment(cfg, run.CandidateBasename)
	if err != nil {
		return run, err
	}
	defer pinned.Close()
	if err = d.runSegmentMigrationHook("after_seal_installed_pin"); err != nil {
		return run, err
	}
	manifest, err := inspectArchiveSegmentFD(ctx, pinned.fileFD, run.CandidateBasename, b.MaxFileBytes)
	if err != nil {
		return run, err
	}
	manifest.FileDigest, err = pinned.Digest(ctx, b.MaxFileBytes)
	if err != nil || manifest.FileDigest != run.FileDigest {
		return run, apptypes.ErrSegmentMigrationOrientation
	}
	manifestDigest, digestErr := ArchiveSegmentManifestDigest(manifest)
	if digestErr != nil || manifestDigest != run.ManifestDigest {
		return run, apptypes.ErrSegmentMigrationOrientation
	}
	if err = d.verifyMigrationSegmentProof(ctx, run, pinned, manifest, migrationLimits(b)); err != nil {
		return run, err
	}
	binding, err := bindingFromManifest(manifest)
	if err != nil {
		return run, err
	}
	db, err := d.open(ctx)
	if err != nil {
		return run, err
	}
	defer d.release(db)
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return run, err
	}
	defer func() { _ = tx.Rollback() }()
	head, err := readCatalogHead(ctx, tx)
	if err != nil {
		return run, err
	}
	high, err := checkCatalogInventoryGate(ctx, tx)
	if err != nil {
		return run, err
	}
	transition, err := domain.SealSegmentTransition(run.Range, run.ReservationID, run.SegmentID)
	if err != nil {
		return run, err
	}
	newHead, err := commitCatalogEpoch(ctx, tx, catalogEpochCommit{expected: head, highWater: high, transitions: []domain.CatalogTransition{transition}, evidenceDigest: migrationEvidenceDigest(run, "sealed"), binding: &binding, proofClass: "segment_migration_v1", transitionDigestVersion: 2}, apptypes.CatalogMaxRanges)
	if err != nil {
		return run, err
	}
	next, err := run.RecordCatalogPhase(domain.SegmentMigrationSealed, newHead.Epoch)
	if err != nil {
		return run, err
	}
	if err = appendSegmentMigration(ctx, tx, run, next); err != nil {
		return run, err
	}
	if err = d.runSegmentMigrationHook("before_seal_catalog_commit"); err != nil {
		return run, err
	}
	if err = pinned.AssertCurrent(); err != nil {
		return run, err
	}
	if err = tx.Commit(); err != nil {
		return run, err
	}
	return next, nil
}

func (d *Database) verifyMigrationShadow(ctx context.Context, run domain.SegmentMigrationRun, cfg segmentMigrationConfig, b apptypes.SegmentMigrationBudget) (domain.SegmentMigrationRun, error) {
	if err := verifySegmentRootIdentity(cfg); err != nil {
		return run, err
	}
	pinned, err := pinInstalledSegment(cfg, run.CandidateBasename)
	if err != nil {
		return run, err
	}
	defer pinned.Close()
	if err = d.runSegmentMigrationHook("after_verify_installed_pin"); err != nil {
		return run, err
	}
	manifest, err := inspectArchiveSegmentFD(ctx, pinned.fileFD, run.CandidateBasename, b.MaxFileBytes)
	if err != nil {
		return run, err
	}
	manifest.FileDigest, err = pinned.Digest(ctx, b.MaxFileBytes)
	if err != nil || manifest.FileDigest != run.FileDigest {
		return run, apptypes.ErrSegmentMigrationOrientation
	}
	manifestDigest, digestErr := ArchiveSegmentManifestDigest(manifest)
	if digestErr != nil || manifestDigest != run.ManifestDigest {
		return run, apptypes.ErrSegmentMigrationOrientation
	}
	if err = d.verifyMigrationSegmentProof(ctx, run, pinned, manifest, migrationLimits(b)); err != nil {
		return run, err
	}
	db, err := d.open(ctx)
	if err != nil {
		return run, err
	}
	defer d.release(db)
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return run, err
	}
	defer func() { _ = tx.Rollback() }() // Exact row digests are rechecked against the frozen source while Hot is still authority.
	for seq := run.Range.Start; seq <= run.Range.End; seq++ {
		unit, e := hydrateSegmentTargetUnit(ctx, tx, seq)
		if e != nil {
			return run, e
		}
		encoded, e := unit.CanonicalBytes()
		if e != nil {
			return run, e
		}
		sum := sha256.Sum256(encoded)
		var want string
		if e = tx.QueryRowContext(ctx, `SELECT canonical_digest FROM archive_segment_target_plan_units WHERE reservation_id=? AND sequence=?`, run.ReservationID, seq).Scan(&want); e != nil || want != hex.EncodeToString(sum[:]) {
			return run, apptypes.ErrSegmentMigrationStaleSource
		}
	}
	head, err := readCatalogHead(ctx, tx)
	if err != nil {
		return run, err
	}
	high, err := checkCatalogInventoryGate(ctx, tx)
	if err != nil {
		return run, err
	}
	transition, err := domain.VerifyShadowTransition(run.Range, run.ReservationID, run.SegmentID)
	if err != nil {
		return run, err
	}
	newHead, err := commitCatalogEpoch(ctx, tx, catalogEpochCommit{expected: head, highWater: high, transitions: []domain.CatalogTransition{transition}, evidenceDigest: migrationEvidenceDigest(run, "verified_shadow"), proofClass: "segment_migration_v1", transitionDigestVersion: 2}, apptypes.CatalogMaxRanges)
	if err != nil {
		return run, err
	}
	next, err := run.RecordCatalogPhase(domain.SegmentMigrationVerifiedShadow, newHead.Epoch)
	if err != nil {
		return run, err
	}
	if err = appendSegmentMigration(ctx, tx, run, next); err != nil {
		return run, err
	}
	lineage := sha256.Sum256([]byte("traceary/store-lineage/v1\x00" + run.StoreID))
	evidence := apptypes.SegmentMigrationEvidenceV1{
		SchemaVersion: apptypes.SegmentMigrationEvidenceSchemaV1, SoftwareCommit: cfg.softwareCommit, ConfigDigest: cfg.configDigest,
		SourceDigest: run.SourceDigest, FormatVersion: manifest.FormatVersion, SummaryVersion: manifest.SummaryVersion, SummaryDigest: manifest.SummaryDigest,
		CompressionFloor: cfg.compressionFloor, PlanDigest: run.PlanDigest, Range: run.Range, LineageDigest: hex.EncodeToString(lineage[:]), ManifestDigest: run.ManifestDigest, FileDigest: run.FileDigest,
		CatalogHeadBefore: head, CatalogHeadAfter: newHead, CopiedRows: run.CopiedRows, CopiedPlainBytes: run.CopiedPlainBytes, SummaryRows: manifest.SummaryRowCount, SummaryBytes: manifest.SummaryByteCount, JournalRevision: next.Revision, CatalogEpoch: newHead.Epoch,
	}
	evidenceDigest, aggregateJSON, evidenceErr := evidence.CanonicalDigest()
	if evidenceErr != nil {
		return run, evidenceErr
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO archive_segment_migration_evidence(run_id,schema_version,evidence_digest,aggregate_json,recorded_at) VALUES(?,?,?,?,?) ON CONFLICT(run_id) DO NOTHING`, run.ID, evidence.SchemaVersion, evidenceDigest, aggregateJSON, time.Now().UTC().Format(time.RFC3339Nano)); err != nil {
		return run, err
	}
	if err = d.runSegmentMigrationHook("before_verify_catalog_commit"); err != nil {
		return run, err
	}
	if err = pinned.AssertCurrent(); err != nil {
		return run, err
	}
	if err = tx.Commit(); err != nil {
		return run, err
	}
	return next, nil
}

// AdvanceSegmentMigrationRollback advances one forward-only rollback boundary.
func (d *Database) AdvanceSegmentMigrationRollback(ctx context.Context, id string, b apptypes.SegmentMigrationBudget) (domain.SegmentMigrationRun, error) {
	if !b.Valid() {
		return domain.SegmentMigrationRun{}, apptypes.ErrCatalogLimit
	}
	run, _, err := d.loadMigrationConfigBounded(ctx, id, b.WallTime)
	if err != nil {
		return run, err
	}
	action, ok := run.NextRollbackAction()
	if !ok {
		if run.Phase == domain.SegmentMigrationRolledBack {
			return run, nil
		}
		return run, domain.ErrSegmentMigrationTransition
	}
	return d.ExecuteSegmentMigrationRollbackAction(ctx, id, action, b)
}

// ExecuteSegmentMigrationRollbackAction executes the application-selected rollback step.
func (d *Database) ExecuteSegmentMigrationRollbackAction(ctx context.Context, id string, action domain.SegmentMigrationRollbackAction, b apptypes.SegmentMigrationBudget) (domain.SegmentMigrationRun, error) {
	if !b.Valid() {
		return domain.SegmentMigrationRun{}, apptypes.ErrCatalogLimit
	}
	op, cancel := migrationOperationContext(ctx, b)
	defer cancel()
	run, cfg, err := d.loadMigrationConfigBounded(op, id, b.WallTime)
	if err != nil {
		return run, err
	}
	if !b.Tightens(cfg.envelope) {
		return run, apptypes.ErrSegmentMigrationEnvelopeExpansion
	}
	if err = checkSegmentMigrationWAL(d.Path(), b); err != nil {
		return run, err
	}
	release, err := d.acquireSegmentMigrationLease(op, b.LockTime)
	if err != nil {
		return domain.SegmentMigrationRun{}, err
	}
	defer release()
	run, cfg, err = d.loadMigrationConfig(op, id)
	if err != nil {
		return run, err
	}
	if !b.Tightens(cfg.envelope) {
		return run, apptypes.ErrSegmentMigrationEnvelopeExpansion
	}
	if run.Phase == domain.SegmentMigrationRolledBack {
		return run, nil
	}
	want, ok := run.NextRollbackAction()
	if !ok || want != action {
		return run, domain.ErrSegmentMigrationTransition
	}
	if action == domain.SegmentMigrationRollbackActionRecordIntent {
		return d.advanceMigrationPhase(op, run, domain.SegmentMigrationRollbackIntent, nil)
	}
	db, err := d.open(op)
	if err != nil {
		return run, err
	}
	defer d.release(db)
	tx, err := db.BeginTx(op, nil)
	if err != nil {
		return run, err
	}
	defer func() { _ = tx.Rollback() }()
	var e error
	var placement domain.CatalogPlacement
	if err = tx.QueryRowContext(op, `SELECT placement_state FROM archive_catalog_current_ranges WHERE start_sequence<=? AND end_sequence>=?`, run.Range.Start, run.Range.End).Scan(&placement); err != nil {
		return run, err
	}
	if placement == domain.CatalogPlacementSealed || placement == domain.CatalogPlacementVerifiedShadow {
		head, e := readCatalogHead(op, tx)
		if e != nil {
			return run, e
		}
		high, e := checkCatalogInventoryGate(op, tx)
		if e != nil {
			return run, e
		}
		transition, e := domain.RollbackSegmentTransition(run.Range, placement, run.ReservationID, run.SegmentID)
		if e != nil {
			return run, e
		}
		newHead, e := commitCatalogEpoch(op, tx, catalogEpochCommit{expected: head, highWater: high, transitions: []domain.CatalogTransition{transition}, evidenceDigest: migrationEvidenceDigest(run, "rollback"), proofClass: "segment_migration_v1", transitionDigestVersion: 2}, apptypes.CatalogMaxRanges)
		if e != nil {
			return run, e
		}
		run.CatalogEpoch = newHead.Epoch
	} else if placement != domain.CatalogPlacementReserved {
		return run, apptypes.ErrSegmentMigrationOrientation
	}
	if placement == domain.CatalogPlacementReserved && run.SegmentID != "" {
		var bindings int
		if e = tx.QueryRowContext(op, `SELECT count(*) FROM archive_catalog_segment_bindings WHERE segment_id=?`, run.SegmentID).Scan(&bindings); e != nil {
			return run, e
		}
		if bindings == 0 {
			if e = removeUnboundInstalledSegment(op, cfg, run, b.MaxFileBytes); e != nil {
				return run, e
			}
		}
	}
	next, e := run.Advance(domain.SegmentMigrationRolledBack)
	if e != nil {
		return run, e
	}
	next.CatalogEpoch = run.CatalogEpoch
	if e = cleanupOwnedMigrationCandidate(cfg, run); e != nil {
		return run, e
	}
	if e = appendSegmentMigration(op, tx, run, next); e != nil {
		return run, e
	}
	if e = d.runSegmentMigrationHook("before_rollback_deletes"); e != nil {
		return run, e
	}
	var expectedIntent int64
	if e = tx.QueryRowContext(op, `SELECT count(*) FROM archive_segment_migration_install_intents WHERE run_id=?`, run.ID).Scan(&expectedIntent); e != nil {
		return run, e
	}
	activeResult, deleteErr := tx.ExecContext(op, `DELETE FROM archive_segment_migration_active WHERE store_id=? AND run_id=?`, run.StoreID, run.ID)
	if deleteErr != nil {
		return run, deleteErr
	}
	activeRows, rowsErr := activeResult.RowsAffected()
	if rowsErr != nil || activeRows != 1 {
		return run, apptypes.ErrSegmentMigrationConflict
	}
	intentResult, deleteErr := tx.ExecContext(op, `DELETE FROM archive_segment_migration_install_intents WHERE run_id=?`, run.ID)
	if deleteErr != nil {
		return run, deleteErr
	}
	intentRows, rowsErr := intentResult.RowsAffected()
	if rowsErr != nil || intentRows != expectedIntent {
		return run, apptypes.ErrSegmentMigrationConflict
	}
	if e = tx.Commit(); e != nil {
		return run, e
	}
	return next, nil
}

func removeUnboundInstalledSegment(ctx context.Context, cfg segmentMigrationConfig, run domain.SegmentMigrationRun, maxBytes int64) error {
	rootFD, err := unix.Open(cfg.archiveRoot, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0)
	if err != nil {
		return err
	}
	root := os.NewFile(uintptr(rootFD), cfg.archiveRoot)
	defer func() { _ = root.Close() }()
	rootInfo, err := root.Stat()
	if err != nil {
		return err
	}
	device, inode, ok := physicalFileIdentity(rootInfo)
	if !ok || device != cfg.archiveDevice || inode != cfg.archiveInode {
		return apptypes.ErrSegmentMigrationOrientation
	}
	fileFD, err := unix.Openat(rootFD, run.SegmentID, unix.O_RDONLY|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0)
	if errors.Is(err, unix.ENOENT) {
		return nil
	}
	if err != nil {
		return apptypes.ErrSegmentMigrationOrientation
	}
	file := os.NewFile(uintptr(fileFD), run.SegmentID)
	digest, digestErr := digestOpenFile(ctx, file, maxBytes)
	_ = file.Close()
	if digestErr != nil || digest != run.FileDigest {
		return apptypes.ErrSegmentMigrationOrientation
	}
	if err = unix.Unlinkat(rootFD, run.SegmentID, 0); err != nil {
		return err
	}
	return unix.Fsync(rootFD) //nolint:wrapcheck // Preserve archive cleanup durability failure.
}

func cleanupOwnedMigrationCandidate(cfg segmentMigrationConfig, run domain.SegmentMigrationRun) error {
	parentFD, err := unix.Open(cfg.candidateRoot, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0)
	if err != nil {
		return err
	}
	parentFile := os.NewFile(uintptr(parentFD), cfg.candidateRoot)
	defer func() { _ = parentFile.Close() }()
	info, err := parentFile.Stat()
	if err != nil {
		return err
	}
	device, inode, ok := physicalFileIdentity(info)
	if !ok || device != cfg.candidateDevice || inode != cfg.candidateInode {
		return apptypes.ErrSegmentMigrationOrientation
	}
	childName := filepath.Base(migrationRunDir(cfg.candidateRoot, run.ID))
	childFD, err := unix.Openat(parentFD, childName, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0)
	if errors.Is(err, unix.ENOENT) {
		return nil
	}
	if err != nil {
		return apptypes.ErrSegmentMigrationOrientation
	}
	child := os.NewFile(uintptr(childFD), childName)
	entries, err := child.ReadDir(-1)
	if err != nil {
		_ = child.Close()
		return err
	}
	for _, entry := range entries {
		var stat unix.Stat_t
		if statErr := unix.Fstatat(childFD, entry.Name(), &stat, unix.AT_SYMLINK_NOFOLLOW); statErr != nil || stat.Mode&unix.S_IFMT != unix.S_IFREG {
			_ = child.Close()
			return apptypes.ErrSegmentMigrationOrientation
		}
		if removeErr := unix.Unlinkat(childFD, entry.Name(), 0); removeErr != nil {
			_ = child.Close()
			return removeErr
		}
	}
	if err = unix.Fsync(childFD); err != nil {
		_ = child.Close()
		return err
	}
	if err = child.Close(); err != nil {
		return err
	}
	if err = unix.Unlinkat(parentFD, childName, unix.AT_REMOVEDIR); err != nil {
		return err
	}
	return unix.Fsync(parentFD) //nolint:wrapcheck // Preserve cleanup durability failure.
}

// RecoverSegmentMigration reconciles a durable install intent, then leaves
// normal phase execution to Resume. It never infers authority from a file.
func (d *Database) RecoverSegmentMigration(ctx context.Context, id string, b apptypes.SegmentMigrationBudget) (domain.SegmentMigrationRun, error) {
	if !b.Valid() {
		return domain.SegmentMigrationRun{}, apptypes.ErrCatalogLimit
	}
	opCtx, cancel := migrationOperationContext(ctx, b)
	defer cancel()
	run, cfg, err := d.loadMigrationConfigBounded(opCtx, id, b.WallTime)
	if err != nil {
		return run, err
	}
	if !b.Tightens(cfg.envelope) {
		return run, apptypes.ErrSegmentMigrationEnvelopeExpansion
	}
	if err = checkSegmentMigrationWAL(d.Path(), b); err != nil {
		return run, err
	}
	release, err := d.acquireSegmentMigrationLease(opCtx, b.LockTime)
	if err != nil {
		return run, err
	}
	defer release()
	run, cfg, err = d.loadMigrationConfig(opCtx, id)
	if err != nil {
		return run, err
	}
	if !b.Tightens(cfg.envelope) {
		return run, apptypes.ErrSegmentMigrationEnvelopeExpansion
	}
	if err = verifySegmentRootIdentity(cfg); err != nil {
		return run, err
	}
	if run.Phase != domain.SegmentMigrationInstallIntent {
		return run, nil
	}
	final := filepath.Join(cfg.archiveRoot, run.CandidateBasename)
	candidate := filepath.Join(migrationRunDir(cfg.candidateRoot, run.ID), run.CandidateBasename)
	_, finalErr := os.Lstat(final)
	_, candidateErr := os.Lstat(candidate)
	if finalErr == nil {
		return d.installMigrationCandidate(opCtx, run, cfg, b)
	}
	if candidateErr == nil && errors.Is(finalErr, os.ErrNotExist) {
		return run, nil
	}
	return run, apptypes.ErrSegmentMigrationOrientation
}
