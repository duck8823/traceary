package sqlite

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"golang.org/x/xerrors"

	"github.com/duck8823/traceary/application"
	apptypes "github.com/duck8823/traceary/application/types"
)

// Exported sentinel errors are stable safety classifications for CLI/tests.
var (
	ErrUnsafeRehearsalTarget      = errors.New("payload rehearsal target is not an independent regular copy")
	ErrDryRunMutation             = errors.New("payload rehearsal preview changed a database component")
	ErrRehearsalNeedsCleanDB      = errors.New("payload rehearsal requires a checkpointed copy without WAL or SHM sidecars")
	ErrRehearsalMigrationRequired = errors.New("payload rehearsal bookkeeping migration is required on the copied target")
)

// PayloadRehearsalAdapter implements copied-store SQLite rehearsal operations.
type PayloadRehearsalAdapter struct {
	migrations                fs.FS
	configuredLivePath        string
	beforeInitialize          func()
	beforeStartRun            func()
	beforePersistence         func(string)
	beforeMigrationRecovery   func()
	beforeRollbackRename      func()
	duringRollbackHeavyVerify func()
}

func (a *PayloadRehearsalAdapter) recheckExpectedTarget(expected rehearsalIdentity, c apptypes.PayloadRehearsalConfig, allowSidecars bool) error {
	current, err := a.inspectTarget(c.TargetPath, c.LivePath)
	if err != nil || !os.SameFile(expected.info, current.info) || expected.device != current.device || expected.inode != current.inode {
		return ErrUnsafeRehearsalTarget
	}
	parts, err := componentSnapshots(current.canonical)
	if err != nil {
		return err
	}
	if !allowSidecars && (parts[1].Exists || parts[2].Exists) {
		return ErrRehearsalNeedsCleanDB
	}
	return nil
}

// NewPayloadRehearsalAdapter binds the migration set used by copied targets.
func NewPayloadRehearsalAdapter(migrations fs.FS, configuredLivePath string) (*PayloadRehearsalAdapter, error) {
	if strings.TrimSpace(configuredLivePath) == "" || strings.ContainsRune(configuredLivePath, '\x00') {
		return nil, errors.New("configured canonical live path is required")
	}
	absolute, err := filepath.Abs(filepath.Clean(configuredLivePath))
	if err != nil {
		return nil, errors.New("configured canonical live path is invalid")
	}
	return &PayloadRehearsalAdapter{migrations: migrations, configuredLivePath: absolute}, nil
}

var _ application.PayloadRehearsalPreview = (*PayloadRehearsalAdapter)(nil)
var _ application.PayloadRehearsalRunner = (*PayloadRehearsalAdapter)(nil)

// Preview performs an immutable copied-store preflight and zero-write proof.
func (a *PayloadRehearsalAdapter) Preview(ctx context.Context, c apptypes.PayloadRehearsalConfig) (apptypes.PayloadRehearsalMetrics, error) {
	id, err := a.inspectTarget(c.TargetPath, c.LivePath)
	if err != nil {
		return apptypes.PayloadRehearsalMetrics{}, err
	}
	liveIdentity, err := inspectLiveCompatibility(ctx, c.LivePath)
	if err != nil {
		return apptypes.PayloadRehearsalMetrics{}, err
	}
	before, err := componentSnapshots(id.canonical)
	if err != nil {
		return apptypes.PayloadRehearsalMetrics{}, err
	}
	if before[1].Exists || before[2].Exists {
		return apptypes.PayloadRehearsalMetrics{}, ErrRehearsalNeedsCleanDB
	}
	db, err := sql.Open("sqlite", immutableRehearsalDSN(id.canonical))
	if err != nil {
		return apptypes.PayloadRehearsalMetrics{}, xerrors.Errorf("open immutable rehearsal target: %w", err)
	}
	defer func() { _ = db.Close() }()
	if err = db.PingContext(ctx); err != nil {
		return apptypes.PayloadRehearsalMetrics{}, xerrors.Errorf("inspect rehearsal target: %w", err)
	}
	if err = VerifyStoreCompatibility(ctx, db); err != nil {
		return apptypes.PayloadRehearsalMetrics{}, err
	}
	var bookkeeping int
	if err = db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM sqlite_master WHERE type='table' AND name='payload_rehearsal_runs')`).Scan(&bookkeeping); err != nil {
		return apptypes.PayloadRehearsalMetrics{}, xerrors.Errorf("inspect rehearsal schema: %w", err)
	}
	migrationRequired := bookkeeping == 0
	var eventRows, auditRows, eventBytes, auditBytes int64
	if err = db.QueryRowContext(ctx, `SELECT count(*),coalesce(sum(length(body)),0) FROM events`).Scan(&eventRows, &eventBytes); err != nil {
		return apptypes.PayloadRehearsalMetrics{}, xerrors.Errorf("inspect event aggregates: %w", err)
	}
	if err = db.QueryRowContext(ctx, `SELECT count(*),coalesce(sum(length(command_text)+length(input_text)+length(output_text)),0) FROM command_audits`).Scan(&auditRows, &auditBytes); err != nil {
		return apptypes.PayloadRehearsalMetrics{}, xerrors.Errorf("inspect audit aggregates: %w", err)
	}
	if err = db.Close(); err != nil {
		return apptypes.PayloadRehearsalMetrics{}, xerrors.Errorf("close immutable rehearsal target: %w", err)
	}
	after, err := componentSnapshots(id.canonical)
	if err != nil {
		return apptypes.PayloadRehearsalMetrics{}, err
	}
	unchanged := snapshotsEqual(before, after)
	if !unchanged {
		return apptypes.PayloadRehearsalMetrics{}, ErrDryRunMutation
	}
	free, _ := filesystemFreeBytes(filepath.Dir(id.canonical))
	headroom := uint64(eventBytes + auditBytes)
	if migrationRequired {
		headroom += uint64(id.info.Size()) + uint64(max(0, c.MaxWALBytes))
	}
	readiness := activationReadiness(liveIdentity, false, free >= headroom, false, false, false)
	return apptypes.PayloadRehearsalMetrics{State: "planned", ScannedRows: eventRows + auditRows*3, PlaintextBytes: eventBytes + auditBytes, FreeBytes: free, EstimatedHeadroom: headroom, DryRunZeroWrite: true, MigrationRequired: migrationRequired, LiveIdentityOnly: liveIdentity, Before: before, After: after, ActivationReadiness: readiness}, nil
}

func snapshotsEqual(a, b []apptypes.PayloadRehearsalFileState) bool {
	aa, _ := json.Marshal(a)
	bb, _ := json.Marshal(b)
	return string(aa) == string(bb)
}

type rehearsalField struct{ table, field, pk, column, codecPrefix string }

var rehearsalFields = []rehearsalField{{"events", "body", "id", "body", "body"}, {"command_audits", "command", "event_id", "command_text", "command"}, {"command_audits", "input", "event_id", "input_text", "input"}, {"command_audits", "output", "event_id", "output_text", "output"}}

type selectedPayload struct {
	key       string
	plaintext []byte
	sourceSHA string
	encoded   encodedPayload
}

func writableRehearsalDSN(path string, lock time.Duration) string {
	q := url.Values{}
	q.Add("_pragma", "foreign_keys(1)")
	q.Add("_pragma", fmt.Sprintf("busy_timeout(%d)", max(1, lock.Milliseconds())))
	q.Set("_txlock", "immediate")
	return (&url.URL{Scheme: "file", Path: path, RawQuery: q.Encode()}).String()
}

func liveReadOnlyDSN(path string) string {
	q := url.Values{}
	q.Set("mode", "ro")
	q.Add("_pragma", "query_only(1)")
	q.Add("_pragma", "busy_timeout(1000)")
	return (&url.URL{Scheme: "file", Path: path, RawQuery: q.Encode()}).String()
}

func observeWALPeak(path string, minimum, maximum int64, peak *int64) error {
	if minimum > *peak {
		*peak = minimum
	}
	if info, err := os.Lstat(path + "-wal"); err == nil {
		if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
			return errors.New("rehearsal WAL is not a regular file")
		}
		if info.Size() > *peak {
			*peak = info.Size()
		}
	} else if !os.IsNotExist(err) {
		return errors.New("cannot observe rehearsal WAL size")
	}
	if *peak > maximum {
		return errors.New("rehearsal WAL hard cap exceeded")
	}
	return nil
}

func transitionTerminalWithinWALBudget(ctx context.Context, session walBudgetedMutationSession, run, lease string, state apptypes.PayloadRehearsalState, guard rehearsalMutationGuard) error {
	return transitionRunWithLease(ctx, session, run, lease, state, guard)
}

// Run creates or resumes bounded zstd shadow rows without changing canonical payloads.
func (a *PayloadRehearsalAdapter) Run(ctx context.Context, c apptypes.PayloadRehearsalConfig, command apptypes.PayloadRehearsalRunCommand) (apptypes.PayloadRehearsalMetrics, error) {
	if !command.Valid() {
		return apptypes.PayloadRehearsalMetrics{}, errors.New("invalid payload rehearsal run command")
	}
	resume := command.IsResume()
	id, err := a.inspectTarget(c.TargetPath, c.LivePath)
	if err != nil {
		return apptypes.PayloadRehearsalMetrics{}, err
	}
	liveIdentity, err := inspectLiveCompatibility(ctx, c.LivePath)
	if err != nil {
		return apptypes.PayloadRehearsalMetrics{}, err
	}
	parts, err := componentSnapshots(id.canonical)
	if err != nil {
		return apptypes.PayloadRehearsalMetrics{}, err
	}
	if !resume && (parts[1].Exists || parts[2].Exists) {
		return apptypes.PayloadRehearsalMetrics{}, ErrRehearsalNeedsCleanDB
	}
	if c.BackupPath == "" {
		return apptypes.PayloadRehearsalMetrics{}, errors.New("payload rehearsal run requires a physical rollback artifact")
	}
	if err = validateBackupIndependence(c.BackupPath, id.canonical, c.LivePath, a.configuredLivePath); err != nil {
		return apptypes.PayloadRehearsalMetrics{}, err
	}
	freeBytes, freeErr := filesystemFreeBytes(filepath.Dir(id.canonical))
	if freeErr != nil {
		return apptypes.PayloadRehearsalMetrics{}, errors.New("cannot verify rehearsal disk headroom")
	}
	requiredHeadroom := uint64(id.info.Size())
	if _, backupErr := os.Stat(c.BackupPath); os.IsNotExist(backupErr) {
		requiredHeadroom += uint64(id.info.Size())
	}
	// Exact migration preflight needs a byte clone plus its bounded WAL.
	requiredHeadroom += uint64(id.info.Size()) + uint64(max(0, c.MaxWALBytes))
	if freeBytes < requiredHeadroom {
		return apptypes.PayloadRehearsalMetrics{}, errors.New("insufficient disk headroom for rehearsal and rollback")
	}
	var backupDigest string
	if resume {
		backupDigest, err = fileDigest(c.BackupPath)
	} else {
		backupDigest, err = ensurePhysicalBackup(id.canonical, c.BackupPath)
	}
	if err != nil {
		return apptypes.PayloadRehearsalMetrics{}, err
	}
	if err = validateBackupIndependence(c.BackupPath, id.canonical, c.LivePath, a.configuredLivePath); err != nil {
		return apptypes.PayloadRehearsalMetrics{}, err
	}
	backupIdentity, err := secureFileIdentity(c.BackupPath)
	if err != nil {
		return apptypes.PayloadRehearsalMetrics{}, ErrUnsafeRehearsalTarget
	}
	minimumWAL, walErr := minimumWALFrameBytes(ctx, id.canonical)
	if walErr != nil {
		return apptypes.PayloadRehearsalMetrics{}, walErr
	}
	if c.MaxWALBytes < minimumWAL {
		return apptypes.PayloadRehearsalMetrics{PeakWALBytes: minimumWAL, RollbackDigest: backupDigest, RollbackVerified: true}, errors.New("rehearsal WAL hard cap is smaller than one SQLite WAL frame")
	}
	peakWAL := minimumWAL
	if a.beforeInitialize != nil {
		a.beforeInitialize()
	}
	if err = a.recheckExpectedTarget(id, c, true); err != nil {
		return apptypes.PayloadRehearsalMetrics{}, err
	}
	db, err := sql.Open("sqlite", writableRehearsalDSN(id.canonical, c.LockTimeLimit))
	if err != nil {
		return apptypes.PayloadRehearsalMetrics{}, xerrors.Errorf("open rehearsal target: %w", err)
	}
	defer func() { _ = db.Close() }()
	db.SetMaxOpenConns(1)
	if _, err = db.ExecContext(ctx, `PRAGMA wal_autocheckpoint=0`); err != nil {
		return apptypes.PayloadRehearsalMetrics{}, xerrors.Errorf("disable rehearsal WAL autocheckpoint: %w", err)
	}
	if err = VerifyStoreCompatibility(ctx, db); err != nil {
		return apptypes.PayloadRehearsalMetrics{}, err
	}
	// Prove the migration's WAL extent on an exact byte clone before allowing
	// the copied target to change. The target identity is then fixed again and
	// the measured reservation is consumed under the same BEGIN IMMEDIATE as
	// the migration itself.
	if err = a.recheckExpectedTarget(id, c, true); err != nil {
		return apptypes.PayloadRehearsalMetrics{}, err
	}
	database := NewDatabase(id.canonical, a.migrations)
	preMigrationIdentity, err := inspectRehearsalMigrationIdentity(ctx, db, id.canonical)
	if err != nil {
		return apptypes.PayloadRehearsalMetrics{}, xerrors.Errorf("fix migration target identity: %w", err)
	}
	measuredMigrationWAL, err := database.measureRehearsalMigrationWAL(ctx, id.canonical, c.LockTimeLimit)
	if err != nil {
		return apptypes.PayloadRehearsalMetrics{}, xerrors.Errorf("preflight rehearsal bookkeeping migration: %w", err)
	}
	if err = a.recheckExpectedTarget(id, c, true); err != nil {
		return apptypes.PayloadRehearsalMetrics{}, err
	}
	recheckedMigrationIdentity, err := inspectRehearsalMigrationIdentity(ctx, db, id.canonical)
	if err != nil || recheckedMigrationIdentity != preMigrationIdentity {
		return apptypes.PayloadRehearsalMetrics{}, ErrUnsafeRehearsalTarget
	}
	if measuredMigrationWAL > 0 {
		pageBytes := minimumWAL - 32
		reservedFrames := max(int64(1), (measuredMigrationWAL-32+pageBytes-1)/pageBytes)
		migrationSession := walBudgetedMutationSession{db: db, path: id.canonical, expectedSchemaSHA: preMigrationIdentity.schema, frameBytes: minimumWAL, maximum: c.MaxWALBytes, peak: &peakWAL, lockLimit: c.LockTimeLimit}
		if err = migrationSession.run(ctx, reservedFrames, func(conn *sql.Conn) error {
			return database.migrateOnBudgetedConnection(ctx, conn)
		}); err != nil {
			return apptypes.PayloadRehearsalMetrics{}, xerrors.Errorf("initialize rehearsal bookkeeping: %w", err)
		}
	}
	if err = observeWALPeak(id.canonical, minimumWAL, c.MaxWALBytes, &peakWAL); err != nil {
		_ = db.Close()
		if a.beforeMigrationRecovery != nil {
			a.beforeMigrationRecovery()
		}
		if recoveryErr := restoreVerifiedRehearsalBackup(id.canonical, c.BackupPath, id, backupIdentity, backupDigest); recoveryErr != nil {
			return apptypes.PayloadRehearsalMetrics{PeakWALBytes: peakWAL, RollbackDigest: backupDigest, RollbackVerified: false}, xerrors.Errorf("migration WAL cap exceeded and rollback failed: %w", recoveryErr)
		}
		return apptypes.PayloadRehearsalMetrics{PeakWALBytes: peakWAL, RollbackDigest: backupDigest, RollbackVerified: true}, err
	}
	if err = VerifyStoreCompatibility(ctx, db); err != nil {
		return apptypes.PayloadRehearsalMetrics{}, err
	}
	rehearsalSchemaSHA, err := rehearsalSchemaFingerprint(ctx, db)
	if err != nil {
		return apptypes.PayloadRehearsalMetrics{}, xerrors.Errorf("fingerprint rehearsal schema: %w", err)
	}
	if a.beforeStartRun != nil {
		a.beforeStartRun()
	}
	if err = a.recheckExpectedTarget(id, c, true); err != nil {
		return apptypes.PayloadRehearsalMetrics{}, err
	}
	configHash := hashConfig(c, id.opaque)
	guard := rehearsalMutationGuard(func() error { return a.recheckExpectedTarget(id, c, true) })
	resumePeak := peakWAL
	startSession := walBudgetedMutationSession{db: db, path: id.canonical, expectedSchemaSHA: rehearsalSchemaSHA, frameBytes: minimumWAL, maximum: c.MaxWALBytes, peak: &resumePeak, lockLimit: c.LockTimeLimit}
	runID, eventHigh, auditHigh, leaseToken, err := loadOrCreateRun(ctx, startSession, id, configHash, backupDigest, resume, guard)
	if err != nil {
		return apptypes.PayloadRehearsalMetrics{}, err
	}
	metrics := apptypes.PayloadRehearsalMetrics{RunID: runID, State: "running", RollbackDigest: backupDigest, RollbackVerified: true, LiveIdentityOnly: liveIdentity, Before: parts, FreeBytes: freeBytes, EstimatedHeadroom: requiredHeadroom, PeakWALBytes: resumePeak, BatchDurationHistogram: map[string]int64{"lt_1ms": 0, "lt_10ms": 0, "lt_100ms": 0, "gte_100ms": 0}}
	batchSession := walBudgetedMutationSession{db: db, path: id.canonical, expectedSchemaSHA: rehearsalSchemaSHA, frameBytes: minimumWAL, maximum: c.MaxWALBytes, peak: &metrics.PeakWALBytes, lockLimit: c.LockTimeLimit}
	observeRunWAL := func() error {
		return observeWALPeak(id.canonical, minimumWAL, c.MaxWALBytes, &metrics.PeakWALBytes)
	}
	if err = observeWALPeak(id.canonical, minimumWAL, c.MaxWALBytes, &metrics.PeakWALBytes); err != nil {
		return metrics, pauseRun(ctx, batchSession, runID, leaseToken, err, guard)
	}
	deadline := time.Now().Add(c.WallTimeLimit)
	for _, f := range rehearsalFields {
		high := eventHigh
		if f.table == "command_audits" {
			high = auditHigh
		}
		for time.Now().Before(deadline) {
			if e := a.recheckExpectedTarget(id, c, true); e != nil {
				return metrics, e
			}
			batch, done, e := selectRehearsalBatch(ctx, db, runID, f, high, c)
			if e != nil {
				return metrics, pauseRun(ctx, batchSession, runID, leaseToken, e, guard)
			}
			if done {
				if a.beforePersistence != nil {
					a.beforePersistence("lane-complete")
				}
				if e = a.recheckExpectedTarget(id, c, true); e != nil {
					return metrics, e
				}
				if e = markRehearsalLaneComplete(ctx, batchSession, runID, leaseToken, f, guard); e != nil {
					return metrics, pauseRun(ctx, batchSession, runID, leaseToken, e, guard)
				}
				if e = observeWALPeak(id.canonical, minimumWAL, c.MaxWALBytes, &metrics.PeakWALBytes); e != nil {
					return metrics, pauseRun(ctx, batchSession, runID, leaseToken, e, guard)
				}
				break
			}
			for i := range batch {
				batch[i].encoded, e = encodePayload(batch[i].plaintext, payloadCodecZstd)
				if e != nil {
					return metrics, pauseRun(ctx, batchSession, runID, leaseToken, e, guard)
				}
			}
			if identityErr := a.recheckExpectedTarget(id, c, true); identityErr != nil {
				return metrics, ErrUnsafeRehearsalTarget
			}
			start := time.Now()
			if a.beforePersistence != nil {
				a.beforePersistence("batch")
			}
			if e = a.recheckExpectedTarget(id, c, true); e != nil {
				return metrics, e
			}
			e = commitRehearsalBatch(ctx, batchSession, runID, leaseToken, f, batch, guard)
			if e != nil {
				return metrics, pauseRun(ctx, batchSession, runID, leaseToken, e, guard)
			}
			batchDuration := time.Since(start)
			recordBatchDuration(metrics.BatchDurationHistogram, batchDuration)
			if batchDuration > c.LockTimeLimit {
				return metrics, pauseRun(ctx, batchSession, runID, leaseToken, errors.New("rehearsal lock duration cap exceeded"), guard)
			}
			metrics.BatchCount++
			for _, r := range batch {
				metrics.ScannedRows++
				metrics.EncodedRows++
				metrics.PlaintextBytes += int64(len(r.plaintext))
				metrics.StoredBytes += r.encoded.StoredBytes
			}
			if err = observeRunWAL(); err != nil {
				return metrics, err
			}
		}
		if !time.Now().Before(deadline) {
			if a.beforePersistence != nil {
				a.beforePersistence("pause")
			}
			if err = pauseRunState(ctx, batchSession, runID, leaseToken, guard); err != nil {
				return metrics, err
			}
			if err = observeRunWAL(); err != nil {
				return metrics, err
			}
			metrics.State = "paused"
			metrics.After, _ = componentSnapshots(id.canonical)
			return metrics, nil
		}
	}
	if a.beforePersistence != nil {
		a.beforePersistence("run-complete")
	}
	if err = a.recheckExpectedTarget(id, c, true); err != nil {
		return metrics, err
	}
	if err = transitionTerminalWithinWALBudget(ctx, batchSession, runID, leaseToken, apptypes.PayloadRehearsalCompleted, guard); err != nil {
		return metrics, err
	}
	metrics.State = "completed"
	metrics.ActivationReadiness = activationReadiness(liveIdentity, true, true, true, false, false)
	metrics.After, _ = componentSnapshots(id.canonical)
	return metrics, nil
}

// Scrub decodes and verifies shadow payloads under persisted bounded checkpoints.
//
//nolint:wrapcheck // public errors are payload-free and already operation scoped.
func (a *PayloadRehearsalAdapter) Scrub(ctx context.Context, c apptypes.PayloadRehearsalConfig) (m apptypes.PayloadRehearsalMetrics, resultErr error) {
	id, err := a.inspectTarget(c.TargetPath, c.LivePath)
	if err != nil {
		return apptypes.PayloadRehearsalMetrics{}, err
	}
	liveIdentity, err := inspectLiveCompatibility(ctx, c.LivePath)
	if err != nil {
		return apptypes.PayloadRehearsalMetrics{}, err
	}
	db, err := sql.Open("sqlite", writableRehearsalDSN(id.canonical, c.LockTimeLimit))
	if err != nil {
		return apptypes.PayloadRehearsalMetrics{}, err
	}
	defer func() { _ = db.Close() }()
	if err = VerifyStoreCompatibility(ctx, db); err != nil {
		return apptypes.PayloadRehearsalMetrics{}, err
	}
	minimumWAL, err := minimumWALFrameBytes(ctx, id.canonical)
	if err != nil || c.MaxWALBytes < minimumWAL {
		return apptypes.PayloadRehearsalMetrics{PeakWALBytes: minimumWAL}, errors.New("scrub WAL hard cap is smaller than one SQLite WAL frame")
	}
	if _, err = db.ExecContext(ctx, `PRAGMA wal_autocheckpoint=0`); err != nil {
		return apptypes.PayloadRehearsalMetrics{}, xerrors.Errorf("disable scrub WAL autocheckpoint: %w", err)
	}
	var run string
	if err = db.QueryRowContext(ctx, `SELECT run_id FROM payload_rehearsal_runs WHERE target_fingerprint=? AND state='completed'`, id.opaque).Scan(&run); err != nil {
		return apptypes.PayloadRehearsalMetrics{}, errors.New("no completed rehearsal run")
	}
	guard := rehearsalMutationGuard(func() error { return a.recheckExpectedTarget(id, c, true) })
	if err = guard(); err != nil {
		return apptypes.PayloadRehearsalMetrics{}, err
	}
	acquirePeak := minimumWAL
	rehearsalSchemaSHA, err := rehearsalSchemaFingerprint(ctx, db)
	if err != nil {
		return apptypes.PayloadRehearsalMetrics{}, err
	}
	scrubSession := walBudgetedMutationSession{db: db, path: id.canonical, expectedSchemaSHA: rehearsalSchemaSHA, frameBytes: minimumWAL, maximum: c.MaxWALBytes, peak: &acquirePeak, lockLimit: c.LockTimeLimit}
	lease, err := acquireScrubLease(ctx, scrubSession, run, guard)
	if err != nil {
		return apptypes.PayloadRehearsalMetrics{}, err
	}
	deadline := time.Now().Add(c.ScrubTimeLimit)
	m = apptypes.PayloadRehearsalMetrics{RunID: run, State: "scrubbing", LiveIdentityOnly: liveIdentity, PeakWALBytes: acquirePeak}
	scrubSession.peak = &m.PeakWALBytes
	leaseActive := true
	observeScrubWAL := func() error {
		return observeWALPeak(id.canonical, minimumWAL, c.MaxWALBytes, &m.PeakWALBytes)
	}
	defer func() {
		if leaseActive {
			releaseCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), time.Second)
			defer cancel()
			if releaseErr := releaseScrubLease(releaseCtx, scrubSession, run, lease, guard); releaseErr != nil && resultErr == nil {
				resultErr = releaseErr
			}
		}
		if observationErr := observeScrubWAL(); observationErr != nil && resultErr == nil {
			resultErr = observationErr
		}
	}()
	if err = observeWALPeak(id.canonical, minimumWAL, c.MaxWALBytes, &m.PeakWALBytes); err != nil {
		return m, err
	}
	var bytes int64
	for _, f := range rehearsalFields {
		var last string
		if err = db.QueryRowContext(ctx, `SELECT scrub_last_primary_key FROM payload_rehearsal_checkpoints WHERE run_id=? AND table_kind=? AND field_kind=?`, run, f.table, f.field).Scan(&last); err != nil {
			return m, err
		}
		for {
			if time.Now().After(deadline) || bytes >= c.ScrubByteLimit {
				return m, errors.New("scrub resource cap reached; resume scrub with the persisted checkpoint")
			}
			rows, queryErr := db.QueryContext(ctx, `SELECT source_primary_key,source_sha256,length(payload),CASE WHEN length(payload)<=? THEN payload ELSE NULL END,codec,format_version,plaintext_bytes,stored_bytes,payload_sha256 FROM payload_rehearsal_rows WHERE run_id=? AND table_kind=? AND field_kind=? AND source_primary_key>? ORDER BY source_primary_key LIMIT ?`, c.ScrubByteLimit, run, f.table, f.field, last, c.BatchRows)
			if queryErr != nil {
				return m, queryErr
			}
			count := 0
			for rows.Next() {
				var key, sourceSHA string
				var payloadLength int64
				var p payloadRow
				if err = rows.Scan(&key, &sourceSHA, &payloadLength, &p.Stored, &p.Codec, &p.FormatVersion, &p.PlaintextBytes, &p.StoredBytes, &p.SHA256); err != nil {
					_ = rows.Close()
					return m, err
				}
				if payloadLength > c.ScrubByteLimit {
					_ = rows.Close()
					return m, errors.New("single shadow payload exceeds scrub hard cap")
				}
				if bytes+int64(len(p.Stored)) > c.ScrubByteLimit {
					_ = rows.Close()
					if a.beforePersistence != nil {
						a.beforePersistence("scrub-progress")
					}
					if guardErr := a.recheckExpectedTarget(id, c, true); guardErr != nil {
						return m, guardErr
					}
					if persistErr := persistScrubProgress(ctx, scrubSession, run, lease, f, last, count, guard); persistErr != nil {
						return m, persistErr
					}
					if err = observeWALPeak(id.canonical, minimumWAL, c.MaxWALBytes, &m.PeakWALBytes); err != nil {
						return m, err
					}
					if bytes == 0 {
						return m, errors.New("single shadow payload exceeds scrub hard cap")
					}
					return m, errors.New("scrub resource cap reached; resume scrub with the persisted checkpoint")
				}
				plain, decodeErr := p.decode(c.DecodedByteLimit)
				if decodeErr != nil {
					_ = rows.Close()
					return m, decodeErr
				}
				sum := sha256.Sum256(plain)
				if hex.EncodeToString(sum[:]) != sourceSHA {
					_ = rows.Close()
					return m, errors.New("scrub source checksum mismatch")
				}
				last = key
				count++
				bytes += int64(len(p.Stored))
				m.ScannedRows++
				m.StoredBytes += int64(len(p.Stored))
				m.PlaintextBytes += int64(len(plain))
			}
			if err = rows.Err(); err != nil {
				_ = rows.Close()
				return m, err
			}
			_ = rows.Close()
			if count == 0 {
				break
			}
			if a.beforePersistence != nil {
				a.beforePersistence("scrub-progress")
			}
			if err = a.recheckExpectedTarget(id, c, true); err != nil {
				return m, err
			}
			if err = persistScrubProgress(ctx, scrubSession, run, lease, f, last, count, guard); err != nil {
				return m, err
			}
			if err = observeWALPeak(id.canonical, minimumWAL, c.MaxWALBytes, &m.PeakWALBytes); err != nil {
				return m, err
			}
		}
	}
	var mismatched int
	if err = db.QueryRowContext(ctx, `SELECT count(*) FROM payload_rehearsal_checkpoints c WHERE c.run_id=? AND (c.changed_rows<>(SELECT count(*) FROM payload_rehearsal_rows r WHERE r.run_id=c.run_id AND r.table_kind=c.table_kind AND r.field_kind=c.field_kind) OR c.scrubbed_rows<>c.changed_rows OR c.scrub_last_primary_key<>coalesce((SELECT max(r.source_primary_key) FROM payload_rehearsal_rows r WHERE r.run_id=c.run_id AND r.table_kind=c.table_kind AND r.field_kind=c.field_kind),''))`, run).Scan(&mismatched); err != nil || mismatched != 0 {
		return m, errors.New("scrub shadow/checkpoint cardinality mismatch")
	}
	var changedTotal, shadowTotal int64
	if err = db.QueryRowContext(ctx, `SELECT coalesce(sum(changed_rows),0) FROM payload_rehearsal_checkpoints WHERE run_id=?`, run).Scan(&changedTotal); err != nil {
		return m, err
	}
	if err = db.QueryRowContext(ctx, `SELECT count(*) FROM payload_rehearsal_rows WHERE run_id=?`, run).Scan(&shadowTotal); err != nil || changedTotal != shadowTotal {
		return m, errors.New("scrub shadow total mismatch")
	}
	if a.beforePersistence != nil {
		a.beforePersistence("scrub-complete")
	}
	if err = a.recheckExpectedTarget(id, c, true); err != nil {
		return m, err
	}
	if err = transitionTerminalWithinWALBudget(ctx, scrubSession, run, lease, apptypes.PayloadRehearsalScrubbed, guard); err != nil {
		return m, err
	}
	leaseActive = false
	m.State = "scrubbed"
	m.ActivationReadiness = activationReadiness(liveIdentity, true, true, true, true, false)
	return m, nil
}

// Rollback restores and verifies the physical artifact for the copied target.
//
//nolint:wrapcheck // public errors are sanitized rollback classifications.
func (a *PayloadRehearsalAdapter) Rollback(ctx context.Context, c apptypes.PayloadRehearsalConfig) (apptypes.PayloadRehearsalMetrics, error) {
	id, err := a.inspectTarget(c.TargetPath, c.LivePath)
	if err != nil {
		return apptypes.PayloadRehearsalMetrics{}, err
	}
	liveIdentity, err := inspectLiveCompatibility(ctx, c.LivePath)
	if err != nil {
		return apptypes.PayloadRehearsalMetrics{}, err
	}
	if c.BackupPath == "" {
		return apptypes.PayloadRehearsalMetrics{}, errors.New("rollback artifact is required")
	}
	backupIdentity, identityErr := secureFileIdentity(c.BackupPath)
	if identityErr != nil || os.SameFile(id.info, backupIdentity.info) {
		return apptypes.PayloadRehearsalMetrics{}, ErrUnsafeRehearsalTarget
	}
	digest, err := fileDigest(c.BackupPath)
	if err != nil {
		return apptypes.PayloadRehearsalMetrics{}, errors.New("rollback artifact unavailable")
	}
	for _, suffix := range []string{"-wal", "-shm"} {
		if _, statErr := os.Lstat(id.canonical + suffix); !os.IsNotExist(statErr) {
			return apptypes.PayloadRehearsalMetrics{}, ErrRehearsalNeedsCleanDB
		}
	}
	currentDB, openErr := sql.Open("sqlite", immutableRehearsalDSN(id.canonical))
	if openErr != nil {
		return apptypes.PayloadRehearsalMetrics{}, errors.New("cannot inspect rollback state")
	}
	if compatibilityErr := VerifyStoreCompatibility(ctx, currentDB); compatibilityErr != nil {
		_ = currentDB.Close()
		return apptypes.PayloadRehearsalMetrics{}, compatibilityErr
	}
	var recordedDigest, runState string
	var activeLease int
	stateErr := currentDB.QueryRowContext(ctx, `SELECT rollback_digest,state,lease_token IS NOT NULL FROM payload_rehearsal_runs ORDER BY started_at DESC LIMIT 1`).Scan(&recordedDigest, &runState, &activeLease)
	_ = currentDB.Close()
	safeState := runState == "paused" || runState == "completed" || runState == "scrubbed" || runState == "failed"
	if stateErr != nil || !safeState || activeLease != 0 || recordedDigest != digest {
		return apptypes.PayloadRehearsalMetrics{}, errors.New("rollback artifact does not match a safe rehearsal state")
	}
	rechecked, recheckErr := a.inspectTarget(c.TargetPath, c.LivePath)
	freshBackup, backupErr := secureFileIdentity(c.BackupPath)
	if recheckErr != nil || backupErr != nil || !os.SameFile(id.info, rechecked.info) || !os.SameFile(backupIdentity.info, freshBackup.info) {
		return apptypes.PayloadRehearsalMetrics{}, ErrUnsafeRehearsalTarget
	}
	for _, suffix := range []string{"-wal", "-shm"} {
		if _, statErr := os.Lstat(rechecked.canonical + suffix); !os.IsNotExist(statErr) {
			return apptypes.PayloadRehearsalMetrics{}, ErrRehearsalNeedsCleanDB
		}
	}
	verifiedDigest, verifiedDigestErr := fileDigest(c.BackupPath)
	if verifiedDigestErr != nil || verifiedDigest != digest {
		return apptypes.PayloadRehearsalMetrics{}, errors.New("rollback artifact changed before restore")
	}
	if err = verifySQLiteArtifact(c.BackupPath); err != nil {
		return apptypes.PayloadRehearsalMetrics{}, err
	}
	if a.duringRollbackHeavyVerify != nil {
		a.duringRollbackHeavyVerify()
	}
	verifyBeforeRename := func() error {
		if a.beforeRollbackRename != nil {
			a.beforeRollbackRename()
		}
		currentTarget, targetErr := a.inspectTarget(c.TargetPath, c.LivePath)
		currentBackup, backupErr := secureFileIdentity(c.BackupPath)
		if targetErr != nil || backupErr != nil || !os.SameFile(rechecked.info, currentTarget.info) || !os.SameFile(backupIdentity.info, currentBackup.info) || currentTarget.device != rechecked.device || currentTarget.inode != rechecked.inode || currentBackup.device != backupIdentity.device || currentBackup.inode != backupIdentity.inode {
			return ErrUnsafeRehearsalTarget
		}
		for _, suffix := range []string{"-wal", "-shm"} {
			if _, sidecarErr := os.Lstat(currentTarget.canonical + suffix); !os.IsNotExist(sidecarErr) {
				return ErrRehearsalNeedsCleanDB
			}
		}
		return nil
	}
	if err = copyFileAtomicVerifiedWithHook(c.BackupPath, id.canonical, digest, verifyBeforeRename); err != nil {
		return apptypes.PayloadRehearsalMetrics{}, xerrors.Errorf("restore rollback artifact: %w", err)
	}
	restoredIdentity, restoredIdentityErr := secureFileIdentity(id.canonical)
	postBackupIdentity, postBackupErr := secureFileIdentity(c.BackupPath)
	if restoredIdentityErr != nil || postBackupErr != nil || os.SameFile(restoredIdentity.info, postBackupIdentity.info) {
		return apptypes.PayloadRehearsalMetrics{}, ErrUnsafeRehearsalTarget
	}
	restored, err := fileDigest(id.canonical)
	if err != nil || restored != digest {
		return apptypes.PayloadRehearsalMetrics{}, errors.New("rollback digest verification failed")
	}
	db, err := sql.Open("sqlite", immutableRehearsalDSN(id.canonical))
	if err != nil {
		return apptypes.PayloadRehearsalMetrics{}, err
	}
	defer func() { _ = db.Close() }()
	if err = VerifyStoreCompatibility(ctx, db); err != nil {
		return apptypes.PayloadRehearsalMetrics{}, err
	}
	var integrity string
	if err = db.QueryRowContext(ctx, `PRAGMA integrity_check`).Scan(&integrity); err != nil || integrity != "ok" {
		return apptypes.PayloadRehearsalMetrics{}, errors.New("rollback integrity verification failed")
	}
	var runTable, activeRuns int
	if err = db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM sqlite_master WHERE type='table' AND name='payload_rehearsal_runs')`).Scan(&runTable); err != nil {
		return apptypes.PayloadRehearsalMetrics{}, errors.New("rollback state verification failed")
	}
	if runTable != 0 {
		if err = db.QueryRowContext(ctx, `SELECT count(*) FROM payload_rehearsal_runs WHERE state IN ('running','paused','completed','scrubbed')`).Scan(&activeRuns); err != nil || activeRuns != 0 {
			return apptypes.PayloadRehearsalMetrics{}, errors.New("rollback retained rehearsal state")
		}
	}
	scrubPassed := runState == "scrubbed"
	rehearsalComplete := runState == "completed" || scrubPassed
	return apptypes.PayloadRehearsalMetrics{State: "rolled_back", RollbackDigest: digest, RollbackVerified: true, LiveIdentityOnly: liveIdentity, ActivationReadiness: activationReadiness(liveIdentity, true, true, rehearsalComplete, scrubPassed, true)}, nil
}

func activationReadiness(liveIdentity, backup, headroom, complete, scrub, rollback bool) apptypes.PayloadActivationReadiness {
	status := func(value bool) apptypes.ReadinessGateStatus {
		if value {
			return apptypes.ReadinessPassed
		}
		return apptypes.ReadinessUnknown
	}
	headroomStatus := apptypes.ReadinessFailed
	if headroom {
		headroomStatus = apptypes.ReadinessPassed
	}
	return apptypes.PayloadActivationReadiness{CompatibleReader: true, LiveIdentityOnly: liveIdentity, BackupVerified: backup, HeadroomSufficient: headroom, RehearsalComplete: complete, ScrubPassed: scrub, RollbackVerified: rollback, ActivationAllowed: false, MinimumReaderStatus: apptypes.ReadinessPassed, OldProcessesStoppedStatus: apptypes.ReadinessUnknown, BackupStatus: status(backup), HeadroomStatus: headroomStatus, ScrubStatus: status(scrub), RollbackStatus: status(rollback), EvidenceAt: time.Now().UTC()}
}

func inspectLiveCompatibility(ctx context.Context, path string) (bool, error) {
	db, err := sql.Open("sqlite", liveReadOnlyDSN(path))
	if err != nil {
		return false, xerrors.Errorf("open immutable live compatibility check: %w", err)
	}
	defer func() { _ = db.Close() }()
	if err = db.PingContext(ctx); err != nil {
		return false, xerrors.Errorf("inspect live store compatibility: %w", err)
	}
	if err = VerifyStoreCompatibility(ctx, db); err != nil {
		return false, err
	}
	var codecColumns int
	if err = db.QueryRowContext(ctx, `SELECT count(*) FROM pragma_table_info('events') WHERE name='body_codec'`).Scan(&codecColumns); err != nil {
		return false, xerrors.Errorf("inspect live payload schema: %w", err)
	}
	// A legacy pre-codec store contains plaintext canonical values only.
	if codecColumns == 0 {
		return true, nil
	}
	var n int64
	err = db.QueryRowContext(ctx, `SELECT (SELECT count(*) FROM events WHERE body_codec IS NOT NULL AND body_codec<>'identity')+(SELECT count(*) FROM command_audits WHERE (command_codec IS NOT NULL AND command_codec<>'identity') OR (input_codec IS NOT NULL AND input_codec<>'identity') OR (output_codec IS NOT NULL AND output_codec<>'identity'))`).Scan(&n)
	if err != nil {
		return false, xerrors.Errorf("inspect live payload codecs: %w", err)
	}
	return n == 0, nil
}

func minimumWALFrameBytes(ctx context.Context, path string) (int64, error) {
	db, err := sql.Open("sqlite", immutableRehearsalDSN(path))
	if err != nil {
		return 0, xerrors.Errorf("open SQLite page-size inspection: %w", err)
	}
	defer func() { _ = db.Close() }()
	var pageSize int64
	if err = db.QueryRowContext(ctx, `PRAGMA page_size`).Scan(&pageSize); err != nil || pageSize <= 0 {
		return 0, errors.New("cannot determine SQLite WAL frame size")
	}
	return 32 + 24 + pageSize, nil
}
