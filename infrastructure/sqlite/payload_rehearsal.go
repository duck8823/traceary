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
	migrations         fs.FS
	configuredLivePath string
	beforeInitialize   func()
	beforeStartRun     func()
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
	readiness := activationReadiness(liveIdentity, false, free >= uint64(eventBytes+auditBytes), false, false, false)
	return apptypes.PayloadRehearsalMetrics{State: "planned", ScannedRows: eventRows + auditRows*3, PlaintextBytes: eventBytes + auditBytes, FreeBytes: free, EstimatedHeadroom: uint64(eventBytes + auditBytes), DryRunZeroWrite: true, MigrationRequired: migrationRequired, LiveIdentityOnly: liveIdentity, Before: before, After: after, ActivationReadiness: readiness}, nil
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

// Run creates or resumes bounded zstd shadow rows without changing canonical payloads.
func (a *PayloadRehearsalAdapter) Run(ctx context.Context, c apptypes.PayloadRehearsalConfig, resume bool) (apptypes.PayloadRehearsalMetrics, error) {
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
	freeBytes, freeErr := filesystemFreeBytes(filepath.Dir(id.canonical))
	if freeErr != nil {
		return apptypes.PayloadRehearsalMetrics{}, errors.New("cannot verify rehearsal disk headroom")
	}
	requiredHeadroom := uint64(id.info.Size())
	if _, backupErr := os.Stat(c.BackupPath); os.IsNotExist(backupErr) {
		requiredHeadroom += uint64(id.info.Size())
	}
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
	if a.beforeInitialize != nil {
		a.beforeInitialize()
	}
	if err = a.recheckExpectedTarget(id, c, resume); err != nil {
		return apptypes.PayloadRehearsalMetrics{}, err
	}
	// Installing migration 37 is allowed only on the verified copied target.
	database := NewDatabase(id.canonical, a.migrations)
	if err = NewStoreManagementDatasource(database).Initialize(ctx); err != nil {
		return apptypes.PayloadRehearsalMetrics{}, xerrors.Errorf("initialize rehearsal bookkeeping: %w", err)
	}
	db, err := sql.Open("sqlite", writableRehearsalDSN(id.canonical, c.LockTimeLimit))
	if err != nil {
		return apptypes.PayloadRehearsalMetrics{}, xerrors.Errorf("open rehearsal target: %w", err)
	}
	defer func() { _ = db.Close() }()
	if err = VerifyStoreCompatibility(ctx, db); err != nil {
		return apptypes.PayloadRehearsalMetrics{}, err
	}
	if a.beforeStartRun != nil {
		a.beforeStartRun()
	}
	if err = a.recheckExpectedTarget(id, c, true); err != nil {
		return apptypes.PayloadRehearsalMetrics{}, err
	}
	configHash := hashConfig(c, id.opaque)
	runID, eventHigh, auditHigh, leaseToken, err := loadOrCreateRun(ctx, db, id, configHash, backupDigest, resume)
	if err != nil {
		return apptypes.PayloadRehearsalMetrics{}, err
	}
	metrics := apptypes.PayloadRehearsalMetrics{RunID: runID, State: "running", RollbackDigest: backupDigest, RollbackVerified: true, LiveIdentityOnly: liveIdentity, Before: parts, FreeBytes: freeBytes, EstimatedHeadroom: requiredHeadroom, BatchDurationHistogram: map[string]int64{"lt_1ms": 0, "lt_10ms": 0, "lt_100ms": 0, "gte_100ms": 0}}
	deadline := time.Now().Add(c.WallTimeLimit)
	for _, f := range rehearsalFields {
		high := eventHigh
		if f.table == "command_audits" {
			high = auditHigh
		}
		for time.Now().Before(deadline) {
			batch, done, e := selectRehearsalBatch(ctx, db, runID, f, high, c)
			if e != nil {
				return metrics, pauseRun(ctx, db, runID, leaseToken, e)
			}
			if done {
				break
			}
			for i := range batch {
				batch[i].encoded, e = encodePayload(batch[i].plaintext, payloadCodecZstd)
				if e != nil {
					return metrics, pauseRun(ctx, db, runID, leaseToken, e)
				}
			}
			currentIdentity, identityErr := secureFileIdentity(id.canonical)
			if identityErr != nil || !os.SameFile(id.info, currentIdentity.info) {
				return metrics, pauseRun(ctx, db, runID, leaseToken, ErrUnsafeRehearsalTarget)
			}
			start := time.Now()
			e = commitRehearsalBatch(ctx, db, runID, leaseToken, f, batch)
			if e != nil {
				return metrics, pauseRun(ctx, db, runID, leaseToken, e)
			}
			batchDuration := time.Since(start)
			recordBatchDuration(metrics.BatchDurationHistogram, batchDuration)
			if batchDuration > c.LockTimeLimit {
				return metrics, pauseRun(ctx, db, runID, leaseToken, errors.New("rehearsal lock duration cap exceeded"))
			}
			metrics.BatchCount++
			for _, r := range batch {
				metrics.ScannedRows++
				metrics.EncodedRows++
				metrics.PlaintextBytes += int64(len(r.plaintext))
				metrics.StoredBytes += r.encoded.StoredBytes
			}
			wal, _ := os.Stat(id.canonical + "-wal")
			if wal != nil && wal.Size() > metrics.PeakWALBytes {
				metrics.PeakWALBytes = wal.Size()
			}
			if metrics.PeakWALBytes > c.MaxWALBytes {
				return metrics, pauseRun(ctx, db, runID, leaseToken, errors.New("rehearsal WAL hard cap exceeded"))
			}
		}
		if !time.Now().Before(deadline) {
			metrics.State = "paused"
			_ = pauseRunState(ctx, db, runID, leaseToken)
			metrics.After, _ = componentSnapshots(id.canonical)
			return metrics, nil
		}
	}
	if err = completeRunState(ctx, db, runID, leaseToken); err != nil {
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
func (a *PayloadRehearsalAdapter) Scrub(ctx context.Context, c apptypes.PayloadRehearsalConfig) (apptypes.PayloadRehearsalMetrics, error) {
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
	var run string
	if err = db.QueryRowContext(ctx, `SELECT run_id FROM payload_rehearsal_runs WHERE target_fingerprint=? AND state='completed'`, id.opaque).Scan(&run); err != nil {
		return apptypes.PayloadRehearsalMetrics{}, errors.New("no completed rehearsal run")
	}
	lease, err := acquireScrubLease(ctx, db, run)
	if err != nil {
		return apptypes.PayloadRehearsalMetrics{}, err
	}
	defer releaseScrubLease(db, run, lease)
	deadline := time.Now().Add(c.ScrubTimeLimit)
	m := apptypes.PayloadRehearsalMetrics{RunID: run, State: "scrubbing", LiveIdentityOnly: liveIdentity}
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
			rows, queryErr := db.QueryContext(ctx, `SELECT source_primary_key,source_sha256,payload,codec,format_version,plaintext_bytes,stored_bytes,payload_sha256 FROM payload_rehearsal_rows WHERE run_id=? AND table_kind=? AND field_kind=? AND source_primary_key>? ORDER BY source_primary_key LIMIT ?`, run, f.table, f.field, last, c.BatchRows)
			if queryErr != nil {
				return m, queryErr
			}
			count := 0
			for rows.Next() {
				var key, sourceSHA string
				var p payloadRow
				if err = rows.Scan(&key, &sourceSHA, &p.Stored, &p.Codec, &p.FormatVersion, &p.PlaintextBytes, &p.StoredBytes, &p.SHA256); err != nil {
					_ = rows.Close()
					return m, err
				}
				if bytes+int64(len(p.Stored)) > c.ScrubByteLimit {
					_ = rows.Close()
					if persistErr := persistScrubProgress(ctx, db, run, lease, f, last, count); persistErr != nil {
						return m, persistErr
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
			if err = persistScrubProgress(ctx, db, run, lease, f, last, count); err != nil {
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
	if err = completeScrubState(ctx, db, run, lease); err != nil {
		return m, err
	}
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
	if err = copyFileAtomic(c.BackupPath, id.canonical); err != nil {
		return apptypes.PayloadRehearsalMetrics{}, xerrors.Errorf("restore rollback artifact: %w", err)
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
	db, err := sql.Open("sqlite", immutableRehearsalDSN(path))
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
