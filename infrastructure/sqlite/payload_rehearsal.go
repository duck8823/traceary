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
	ErrLivePayloadNotIdentityOnly = errors.New("live store contains non-identity payloads")
)

func requireLiveIdentityOnly(ctx context.Context, path string) error {
	ok, err := inspectLiveCompatibility(ctx, path)
	if err != nil {
		return err
	}
	if !ok {
		return ErrLivePayloadNotIdentityOnly
	}
	return nil
}

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
var _ application.PayloadRehearsalRunWorkflow = (*PayloadRehearsalAdapter)(nil)
var _ application.PayloadRehearsalScrubWorkflow = (*PayloadRehearsalAdapter)(nil)

type payloadRehearsalRunHandle struct {
	adapter              *PayloadRehearsalAdapter
	config               apptypes.PayloadRehearsalConfig
	id                   rehearsalIdentity
	db                   *sql.DB
	minimumWAL           int64
	liveIdentity         bool
	runID, leaseToken    string
	eventHigh, auditHigh string
	guard                rehearsalMutationGuard
	session              walBudgetedMutationSession
	metrics              apptypes.PayloadRehearsalMetrics
}

func (*payloadRehearsalRunHandle) PayloadRehearsalRunHandle() {}

type payloadRehearsalScrubHandle struct {
	adapter           *PayloadRehearsalAdapter
	config            apptypes.PayloadRehearsalConfig
	id                rehearsalIdentity
	db                *sql.DB
	minimumWAL        int64
	liveIdentity      bool
	runID, leaseToken string
	guard             rehearsalMutationGuard
	session           walBudgetedMutationSession
	metrics           apptypes.PayloadRehearsalMetrics
	leaseActive       bool
	deadline          time.Time
}

func (*payloadRehearsalScrubHandle) PayloadRehearsalScrubHandle() {}

func ownedScrubHandle(owner *PayloadRehearsalAdapter, opaque application.PayloadRehearsalScrubHandle) (*payloadRehearsalScrubHandle, error) {
	h, ok := opaque.(*payloadRehearsalScrubHandle)
	if !ok || h == nil || h.adapter != owner || h.db == nil {
		return nil, errors.New("invalid payload rehearsal scrub handle")
	}
	return h, nil
}

func rehearsalFieldFor(field apptypes.PayloadRehearsalField) (rehearsalField, bool) {
	for i, candidate := range apptypes.OrderedPayloadRehearsalFields() {
		if candidate == field {
			return rehearsalFields[i], true
		}
	}
	return rehearsalField{}, false
}

func ownedRehearsalHandle(owner *PayloadRehearsalAdapter, opaque application.PayloadRehearsalRunHandle) (*payloadRehearsalRunHandle, error) {
	h, ok := opaque.(*payloadRehearsalRunHandle)
	if !ok || h == nil || h.adapter != owner || h.db == nil {
		return nil, errors.New("invalid payload rehearsal run handle")
	}
	return h, nil
}

func rehearsalDiskPlan(targetBytes int64, maxWALBytes int64, backupExists bool) apptypes.RehearsalDiskPlan {
	target := uint64(max(0, targetBytes))
	wal := uint64(max(0, maxWALBytes))
	plan := apptypes.RehearsalDiskPlan{
		MigrationCloneBytes: target,
		MigrationWALBytes:   wal,
		ShadowGrowthBytes:   target,
	}
	if !backupExists {
		plan.BackupBytes = target
	}
	plan.TotalBytes = plan.BackupBytes + plan.MigrationCloneBytes + plan.MigrationWALBytes + plan.ShadowGrowthBytes
	return plan
}

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
	_, backupErr := os.Stat(c.BackupPath)
	plan := rehearsalDiskPlan(id.info.Size(), c.MaxWALBytes, c.BackupPath != "" && backupErr == nil)
	headroom := plan.TotalBytes
	return apptypes.PayloadRehearsalMetrics{State: "planned", ScannedRows: eventRows + auditRows*3, PlaintextBytes: eventBytes + auditBytes, FreeBytes: free, EstimatedHeadroom: headroom, DiskPlan: plan, DryRunZeroWrite: true, MigrationRequired: migrationRequired, LiveIdentityOnly: liveIdentity, Before: before, After: after}, nil
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

// Prepare fixes the target identity and owns all SQLite resources for a run.
func (a *PayloadRehearsalAdapter) Prepare(ctx context.Context, c apptypes.PayloadRehearsalConfig, command apptypes.PayloadRehearsalRunCommand) (application.PayloadRehearsalRunHandle, apptypes.PayloadRehearsalMetrics, error) {
	if !command.Valid() {
		return nil, apptypes.PayloadRehearsalMetrics{}, errors.New("invalid payload rehearsal run command")
	}
	resume := command.IsResume()
	id, err := a.inspectTarget(c.TargetPath, c.LivePath)
	if err != nil {
		return nil, apptypes.PayloadRehearsalMetrics{}, err
	}
	liveIdentity, err := inspectLiveCompatibility(ctx, c.LivePath)
	if err != nil {
		return nil, apptypes.PayloadRehearsalMetrics{}, err
	}
	if !liveIdentity {
		return nil, apptypes.PayloadRehearsalMetrics{}, ErrLivePayloadNotIdentityOnly
	}
	parts, err := componentSnapshots(id.canonical)
	if err != nil {
		return nil, apptypes.PayloadRehearsalMetrics{}, err
	}
	if !resume && (parts[1].Exists || parts[2].Exists) {
		return nil, apptypes.PayloadRehearsalMetrics{}, ErrRehearsalNeedsCleanDB
	}
	if c.BackupPath == "" {
		return nil, apptypes.PayloadRehearsalMetrics{}, errors.New("payload rehearsal run requires a physical rollback artifact")
	}
	if err = validateBackupIndependence(c.BackupPath, id.canonical, c.LivePath, a.configuredLivePath); err != nil {
		return nil, apptypes.PayloadRehearsalMetrics{}, err
	}
	freeBytes, freeErr := filesystemFreeBytes(filepath.Dir(id.canonical))
	if freeErr != nil {
		return nil, apptypes.PayloadRehearsalMetrics{}, errors.New("cannot verify rehearsal disk headroom")
	}
	_, backupErr := os.Stat(c.BackupPath)
	diskPlan := rehearsalDiskPlan(id.info.Size(), c.MaxWALBytes, backupErr == nil)
	requiredHeadroom := diskPlan.TotalBytes
	if freeBytes < requiredHeadroom {
		return nil, apptypes.PayloadRehearsalMetrics{}, errors.New("insufficient disk headroom for rehearsal and rollback")
	}
	if err = requireLiveIdentityOnly(ctx, c.LivePath); err != nil {
		return nil, apptypes.PayloadRehearsalMetrics{}, err
	}
	var backupDigest string
	if resume {
		backupDigest, err = fileDigest(c.BackupPath)
	} else {
		backupDigest, err = ensurePhysicalBackup(id.canonical, c.BackupPath)
	}
	if err != nil {
		return nil, apptypes.PayloadRehearsalMetrics{}, err
	}
	if err = validateBackupIndependence(c.BackupPath, id.canonical, c.LivePath, a.configuredLivePath); err != nil {
		return nil, apptypes.PayloadRehearsalMetrics{}, err
	}
	backupIdentity, err := secureFileIdentity(c.BackupPath)
	if err != nil {
		return nil, apptypes.PayloadRehearsalMetrics{}, ErrUnsafeRehearsalTarget
	}
	minimumWAL, walErr := minimumWALFrameBytes(ctx, id.canonical)
	if walErr != nil {
		return nil, apptypes.PayloadRehearsalMetrics{}, walErr
	}
	if c.MaxWALBytes < minimumWAL {
		return nil, apptypes.PayloadRehearsalMetrics{PeakWALBytes: minimumWAL, RollbackDigest: backupDigest, RollbackVerified: true}, errors.New("rehearsal WAL hard cap is smaller than one SQLite WAL frame")
	}
	peakWAL := minimumWAL
	if a.beforeInitialize != nil {
		a.beforeInitialize()
	}
	if err = a.recheckExpectedTarget(id, c, true); err != nil {
		return nil, apptypes.PayloadRehearsalMetrics{}, err
	}
	if err = requireLiveIdentityOnly(ctx, c.LivePath); err != nil {
		return nil, apptypes.PayloadRehearsalMetrics{}, err
	}
	db, err := sql.Open("sqlite", writableRehearsalDSN(id.canonical, c.LockTimeLimit))
	if err != nil {
		return nil, apptypes.PayloadRehearsalMetrics{}, xerrors.Errorf("open rehearsal target: %w", err)
	}
	prepared := false
	defer func() {
		if !prepared {
			_ = db.Close()
		}
	}()
	db.SetMaxOpenConns(1)
	if _, err = db.ExecContext(ctx, `PRAGMA wal_autocheckpoint=0`); err != nil {
		return nil, apptypes.PayloadRehearsalMetrics{}, xerrors.Errorf("disable rehearsal WAL autocheckpoint: %w", err)
	}
	if err = VerifyStoreCompatibility(ctx, db); err != nil {
		return nil, apptypes.PayloadRehearsalMetrics{}, err
	}
	// Prove the migration's WAL extent on an exact byte clone before allowing
	// the copied target to change. The target identity is then fixed again and
	// the measured reservation is consumed under the same BEGIN IMMEDIATE as
	// the migration itself.
	if err = a.recheckExpectedTarget(id, c, true); err != nil {
		return nil, apptypes.PayloadRehearsalMetrics{}, err
	}
	database := NewDatabase(id.canonical, a.migrations)
	preMigrationIdentity, err := inspectRehearsalMigrationIdentity(ctx, db, id.canonical)
	if err != nil {
		return nil, apptypes.PayloadRehearsalMetrics{}, xerrors.Errorf("fix migration target identity: %w", err)
	}
	measuredMigrationWAL, err := database.measureRehearsalMigrationWAL(ctx, id.canonical, c.LockTimeLimit)
	if err != nil {
		return nil, apptypes.PayloadRehearsalMetrics{}, xerrors.Errorf("preflight rehearsal bookkeeping migration: %w", err)
	}
	if err = a.recheckExpectedTarget(id, c, true); err != nil {
		return nil, apptypes.PayloadRehearsalMetrics{}, err
	}
	recheckedMigrationIdentity, err := inspectRehearsalMigrationIdentity(ctx, db, id.canonical)
	if err != nil || recheckedMigrationIdentity != preMigrationIdentity {
		return nil, apptypes.PayloadRehearsalMetrics{}, ErrUnsafeRehearsalTarget
	}
	if err = requireLiveIdentityOnly(ctx, c.LivePath); err != nil {
		return nil, apptypes.PayloadRehearsalMetrics{}, err
	}
	if measuredMigrationWAL > 0 {
		pageBytes := minimumWAL - 32
		reservedFrames := max(int64(1), (measuredMigrationWAL-32+pageBytes-1)/pageBytes)
		migrationSession := walBudgetedMutationSession{db: db, path: id.canonical, expectedSchemaSHA: preMigrationIdentity.schema, frameBytes: minimumWAL, maximum: c.MaxWALBytes, peak: &peakWAL, lockLimit: c.LockTimeLimit}
		if err = migrationSession.run(ctx, reservedFrames, func(conn *sql.Conn) error {
			return database.migrateOnBudgetedConnection(ctx, conn)
		}); err != nil {
			return nil, apptypes.PayloadRehearsalMetrics{}, xerrors.Errorf("initialize rehearsal bookkeeping: %w", err)
		}
	}
	if err = observeWALPeak(id.canonical, minimumWAL, c.MaxWALBytes, &peakWAL); err != nil {
		_ = db.Close()
		if a.beforeMigrationRecovery != nil {
			a.beforeMigrationRecovery()
		}
		if recoveryErr := restoreVerifiedRehearsalBackup(id.canonical, c.BackupPath, id, backupIdentity, backupDigest); recoveryErr != nil {
			return nil, apptypes.PayloadRehearsalMetrics{PeakWALBytes: peakWAL, RollbackDigest: backupDigest, RollbackVerified: false}, xerrors.Errorf("migration WAL cap exceeded and rollback failed: %w", recoveryErr)
		}
		return nil, apptypes.PayloadRehearsalMetrics{PeakWALBytes: peakWAL, RollbackDigest: backupDigest, RollbackVerified: true}, err
	}
	if err = VerifyStoreCompatibility(ctx, db); err != nil {
		return nil, apptypes.PayloadRehearsalMetrics{}, err
	}
	rehearsalSchemaSHA, err := rehearsalSchemaFingerprint(ctx, db)
	if err != nil {
		return nil, apptypes.PayloadRehearsalMetrics{}, xerrors.Errorf("fingerprint rehearsal schema: %w", err)
	}
	if a.beforeStartRun != nil {
		a.beforeStartRun()
	}
	if err = requireLiveIdentityOnly(ctx, c.LivePath); err != nil {
		return nil, apptypes.PayloadRehearsalMetrics{}, err
	}
	if err = a.recheckExpectedTarget(id, c, true); err != nil {
		return nil, apptypes.PayloadRehearsalMetrics{}, err
	}
	configHash := hashConfig(c, id.opaque)
	guard := rehearsalMutationGuard(func() error { return a.recheckExpectedTarget(id, c, true) })
	resumePeak := peakWAL
	startSession := walBudgetedMutationSession{db: db, path: id.canonical, expectedSchemaSHA: rehearsalSchemaSHA, frameBytes: minimumWAL, maximum: c.MaxWALBytes, peak: &resumePeak, lockLimit: c.LockTimeLimit}
	runID, eventHigh, auditHigh, leaseToken, err := loadOrCreateRun(ctx, startSession, id, configHash, backupDigest, resume, guard)
	if err != nil {
		return nil, apptypes.PayloadRehearsalMetrics{}, err
	}
	metrics := apptypes.PayloadRehearsalMetrics{RunID: runID, State: "running", RollbackDigest: backupDigest, RollbackVerified: true, LiveIdentityOnly: liveIdentity, Before: parts, FreeBytes: freeBytes, EstimatedHeadroom: requiredHeadroom, DiskPlan: diskPlan, PeakWALBytes: resumePeak, BatchDurationHistogram: map[string]int64{"lt_1ms": 0, "lt_10ms": 0, "lt_100ms": 0, "gte_100ms": 0}}
	batchSession := walBudgetedMutationSession{db: db, path: id.canonical, expectedSchemaSHA: rehearsalSchemaSHA, frameBytes: minimumWAL, maximum: c.MaxWALBytes, peak: &metrics.PeakWALBytes, lockLimit: c.LockTimeLimit}
	if err = observeWALPeak(id.canonical, minimumWAL, c.MaxWALBytes, &metrics.PeakWALBytes); err != nil {
		return nil, metrics, pauseRun(ctx, batchSession, runID, leaseToken, err, guard)
	}
	h := &payloadRehearsalRunHandle{adapter: a, config: c, id: id, db: db, minimumWAL: minimumWAL, liveIdentity: liveIdentity, runID: runID, leaseToken: leaseToken, eventHigh: eventHigh, auditHigh: auditHigh, guard: guard, metrics: metrics}
	h.session = walBudgetedMutationSession{db: db, path: id.canonical, expectedSchemaSHA: rehearsalSchemaSHA, frameBytes: minimumWAL, maximum: c.MaxWALBytes, peak: &h.metrics.PeakWALBytes, lockLimit: c.LockTimeLimit}
	prepared = true
	return h, h.metrics, nil
}

// AdvanceField persists at most one bounded batch for a field.
func (a *PayloadRehearsalAdapter) AdvanceField(ctx context.Context, opaque application.PayloadRehearsalRunHandle, field apptypes.PayloadRehearsalField) (apptypes.PayloadRehearsalMetrics, bool, error) {
	h, err := ownedRehearsalHandle(a, opaque)
	if err != nil {
		return apptypes.PayloadRehearsalMetrics{}, false, err
	}
	if err = requireLiveIdentityOnly(ctx, h.config.LivePath); err != nil {
		h.metrics.LiveIdentityOnly = false
		return h.metrics, false, err
	}
	f, ok := rehearsalFieldFor(field)
	if !ok {
		return h.metrics, false, errors.New("invalid payload rehearsal field")
	}
	high := h.eventHigh
	if f.table == "command_audits" {
		high = h.auditHigh
	}
	if err = a.recheckExpectedTarget(h.id, h.config, true); err != nil {
		return h.metrics, false, err
	}
	batch, done, err := selectRehearsalBatch(ctx, h.db, h.runID, f, high, h.config)
	if err != nil {
		return h.metrics, false, err
	}
	if done {
		if a.beforePersistence != nil {
			a.beforePersistence("lane-complete")
		}
		if err = a.recheckExpectedTarget(h.id, h.config, true); err != nil {
			return h.metrics, false, err
		}
		if err = markRehearsalLaneComplete(ctx, h.session, h.runID, h.leaseToken, f, h.guard); err != nil {
			return h.metrics, false, err
		}
		if err = observeWALPeak(h.id.canonical, h.minimumWAL, h.config.MaxWALBytes, &h.metrics.PeakWALBytes); err != nil {
			return h.metrics, false, err
		}
		return h.metrics, true, nil
	}
	for i := range batch {
		batch[i].encoded, err = encodePayload(batch[i].plaintext, payloadCodecZstd)
		if err != nil {
			return h.metrics, false, err
		}
	}
	if err = a.recheckExpectedTarget(h.id, h.config, true); err != nil {
		return h.metrics, false, ErrUnsafeRehearsalTarget
	}
	start := time.Now()
	if a.beforePersistence != nil {
		a.beforePersistence("batch")
	}
	if err = a.recheckExpectedTarget(h.id, h.config, true); err != nil {
		return h.metrics, false, err
	}
	if err = commitRehearsalBatch(ctx, h.session, h.runID, h.leaseToken, f, batch, h.guard); err != nil {
		return h.metrics, false, err
	}
	duration := time.Since(start)
	recordBatchDuration(h.metrics.BatchDurationHistogram, duration)
	if duration > h.config.LockTimeLimit {
		return h.metrics, false, errors.New("rehearsal lock duration cap exceeded")
	}
	h.metrics.BatchCount++
	for _, row := range batch {
		h.metrics.ScannedRows++
		h.metrics.EncodedRows++
		h.metrics.PlaintextBytes += int64(len(row.plaintext))
		h.metrics.StoredBytes += row.encoded.StoredBytes
	}
	if err = observeWALPeak(h.id.canonical, h.minimumWAL, h.config.MaxWALBytes, &h.metrics.PeakWALBytes); err != nil {
		return h.metrics, false, err
	}
	return h.metrics, false, nil
}

// Pause releases the active lease while preserving resumable checkpoints.
func (a *PayloadRehearsalAdapter) Pause(ctx context.Context, opaque application.PayloadRehearsalRunHandle) (apptypes.PayloadRehearsalMetrics, error) {
	h, err := ownedRehearsalHandle(a, opaque)
	if err != nil {
		return apptypes.PayloadRehearsalMetrics{}, err
	}
	if liveErr := requireLiveIdentityOnly(ctx, h.config.LivePath); liveErr != nil {
		h.metrics.LiveIdentityOnly = false
	}
	if a.beforePersistence != nil {
		a.beforePersistence("pause")
	}
	if err = pauseRunState(ctx, h.session, h.runID, h.leaseToken, h.guard); err != nil {
		return h.metrics, err
	}
	if err = observeWALPeak(h.id.canonical, h.minimumWAL, h.config.MaxWALBytes, &h.metrics.PeakWALBytes); err != nil {
		return h.metrics, err
	}
	h.metrics.State = "paused"
	h.metrics.After, _ = componentSnapshots(h.id.canonical)
	return h.metrics, nil
}

// Complete makes a fully advanced run terminal.
func (a *PayloadRehearsalAdapter) Complete(ctx context.Context, opaque application.PayloadRehearsalRunHandle) (apptypes.PayloadRehearsalMetrics, error) {
	h, err := ownedRehearsalHandle(a, opaque)
	if err != nil {
		return apptypes.PayloadRehearsalMetrics{}, err
	}
	if err = requireLiveIdentityOnly(ctx, h.config.LivePath); err != nil {
		h.metrics.LiveIdentityOnly = false
		return h.metrics, err
	}
	if a.beforePersistence != nil {
		a.beforePersistence("run-complete")
	}
	if err = a.recheckExpectedTarget(h.id, h.config, true); err != nil {
		return h.metrics, err
	}
	if err = transitionTerminalWithinWALBudget(ctx, h.session, h.runID, h.leaseToken, apptypes.PayloadRehearsalCompleted, h.guard); err != nil {
		return h.metrics, err
	}
	h.metrics.State = "completed"
	h.metrics.After, _ = componentSnapshots(h.id.canonical)
	return h.metrics, nil
}

// Close releases the database connection held by an opaque run handle.
func (a *PayloadRehearsalAdapter) Close(opaque application.PayloadRehearsalRunHandle) error {
	h, err := ownedRehearsalHandle(a, opaque)
	if err != nil {
		return err
	}
	err = h.db.Close()
	h.db = nil
	if err != nil {
		return xerrors.Errorf("close payload rehearsal database: %w", err)
	}
	return nil
}

// PrepareScrub acquires a scrub lease and fixes the copied target identity.
//
//nolint:wrapcheck // public errors are payload-free and already operation scoped.
func (a *PayloadRehearsalAdapter) PrepareScrub(ctx context.Context, c apptypes.PayloadRehearsalConfig) (application.PayloadRehearsalScrubHandle, apptypes.PayloadRehearsalMetrics, error) {
	id, err := a.inspectTarget(c.TargetPath, c.LivePath)
	if err != nil {
		return nil, apptypes.PayloadRehearsalMetrics{}, err
	}
	liveIdentity, err := inspectLiveCompatibility(ctx, c.LivePath)
	if err != nil {
		return nil, apptypes.PayloadRehearsalMetrics{}, err
	}
	if !liveIdentity {
		return nil, apptypes.PayloadRehearsalMetrics{}, ErrLivePayloadNotIdentityOnly
	}
	db, err := sql.Open("sqlite", writableRehearsalDSN(id.canonical, c.LockTimeLimit))
	if err != nil {
		return nil, apptypes.PayloadRehearsalMetrics{}, err
	}
	db.SetMaxOpenConns(1)
	prepared := false
	defer func() {
		if !prepared {
			_ = db.Close()
		}
	}()
	if err = VerifyStoreCompatibility(ctx, db); err != nil {
		return nil, apptypes.PayloadRehearsalMetrics{}, err
	}
	minimumWAL, err := minimumWALFrameBytes(ctx, id.canonical)
	if err != nil || c.MaxWALBytes < minimumWAL {
		return nil, apptypes.PayloadRehearsalMetrics{PeakWALBytes: minimumWAL}, errors.New("scrub WAL hard cap is smaller than one SQLite WAL frame")
	}
	if _, err = db.ExecContext(ctx, `PRAGMA wal_autocheckpoint=0`); err != nil {
		return nil, apptypes.PayloadRehearsalMetrics{}, xerrors.Errorf("disable scrub WAL autocheckpoint: %w", err)
	}
	var run, recordedFingerprint, recordedDevice, recordedInode string
	if err = db.QueryRowContext(ctx, `SELECT run_id,target_fingerprint,target_device,target_inode FROM payload_rehearsal_runs WHERE state='completed' ORDER BY started_at DESC LIMIT 1`).Scan(&run, &recordedFingerprint, &recordedDevice, &recordedInode); err != nil {
		return nil, apptypes.PayloadRehearsalMetrics{}, errors.New("no completed rehearsal run")
	}
	if recordedFingerprint != id.opaque || recordedDevice != id.device || recordedInode != id.inode {
		return nil, apptypes.PayloadRehearsalMetrics{}, ErrUnsafeRehearsalTarget
	}
	guard := rehearsalMutationGuard(func() error { return a.recheckExpectedTarget(id, c, true) })
	if err = guard(); err != nil {
		return nil, apptypes.PayloadRehearsalMetrics{}, err
	}
	acquirePeak := minimumWAL
	rehearsalSchemaSHA, err := rehearsalSchemaFingerprint(ctx, db)
	if err != nil {
		return nil, apptypes.PayloadRehearsalMetrics{}, err
	}
	scrubSession := walBudgetedMutationSession{db: db, path: id.canonical, expectedSchemaSHA: rehearsalSchemaSHA, frameBytes: minimumWAL, maximum: c.MaxWALBytes, peak: &acquirePeak, lockLimit: c.LockTimeLimit}
	lease, err := acquireScrubLease(ctx, scrubSession, run, guard)
	if err != nil {
		return nil, apptypes.PayloadRehearsalMetrics{}, err
	}
	m := apptypes.PayloadRehearsalMetrics{RunID: run, State: "scrubbing", LiveIdentityOnly: liveIdentity, PeakWALBytes: acquirePeak}
	h := &payloadRehearsalScrubHandle{adapter: a, config: c, id: id, db: db, minimumWAL: minimumWAL, liveIdentity: liveIdentity, runID: run, leaseToken: lease, guard: guard, metrics: m, leaseActive: true, deadline: time.Now().Add(c.ScrubTimeLimit)}
	h.session = walBudgetedMutationSession{db: db, path: id.canonical, expectedSchemaSHA: rehearsalSchemaSHA, frameBytes: minimumWAL, maximum: c.MaxWALBytes, peak: &h.metrics.PeakWALBytes, lockLimit: c.LockTimeLimit}
	prepared = true
	return h, h.metrics, nil
}

// AdvanceScrubField verifies and checkpoints at most one bounded page.
func (a *PayloadRehearsalAdapter) AdvanceScrubField(ctx context.Context, opaque application.PayloadRehearsalScrubHandle, field apptypes.PayloadRehearsalField) (apptypes.PayloadRehearsalMetrics, bool, error) {
	h, err := ownedScrubHandle(a, opaque)
	if err != nil {
		return apptypes.PayloadRehearsalMetrics{}, false, err
	}
	if err = requireLiveIdentityOnly(ctx, h.config.LivePath); err != nil {
		h.metrics.LiveIdentityOnly = false
		return h.metrics, false, err
	}
	f, ok := rehearsalFieldFor(field)
	if !ok {
		return h.metrics, false, errors.New("invalid payload rehearsal field")
	}
	var last string
	if err = h.db.QueryRowContext(ctx, `SELECT scrub_last_primary_key FROM payload_rehearsal_checkpoints WHERE run_id=? AND table_kind=? AND field_kind=?`, h.runID, f.table, f.field).Scan(&last); err != nil {
		return h.metrics, false, xerrors.Errorf("load scrub checkpoint: %w", err)
	}
	rows, err := h.db.QueryContext(ctx, `SELECT source_primary_key,source_sha256,length(payload),CASE WHEN length(payload)<=? THEN payload ELSE NULL END,codec,format_version,plaintext_bytes,stored_bytes,payload_sha256 FROM payload_rehearsal_rows WHERE run_id=? AND table_kind=? AND field_kind=? AND source_primary_key>? ORDER BY source_primary_key LIMIT ?`, h.config.ScrubByteLimit, h.runID, f.table, f.field, last, h.config.BatchRows)
	if err != nil {
		return h.metrics, false, xerrors.Errorf("select scrub page: %w", err)
	}
	defer func() { _ = rows.Close() }()
	count := 0
	var pageDecoded int64
	resourceCapped := false
	for rows.Next() {
		if !time.Now().Before(h.deadline) {
			resourceCapped = true
			break
		}
		var key, sourceSHA string
		var payloadLength int64
		var p payloadRow
		if err = rows.Scan(&key, &sourceSHA, &payloadLength, &p.Stored, &p.Codec, &p.FormatVersion, &p.PlaintextBytes, &p.StoredBytes, &p.SHA256); err != nil {
			return h.metrics, false, xerrors.Errorf("scan scrub row: %w", err)
		}
		if payloadLength > h.config.ScrubByteLimit {
			return h.metrics, false, errors.New("single scrub page exceeds scrub hard cap")
		}
		if h.metrics.StoredBytes+int64(len(p.Stored)) > h.config.ScrubByteLimit {
			resourceCapped = true
			break
		}
		remainingDecoded := h.config.DecodedByteLimit - pageDecoded
		if remainingDecoded <= 0 || (p.PlaintextBytes.Valid && p.PlaintextBytes.Int64 > remainingDecoded) {
			resourceCapped = true
			break
		}
		plain, decodeErr := p.decode(h.config.DecodedByteLimit)
		if decodeErr != nil {
			return h.metrics, false, decodeErr
		}
		if int64(len(plain)) > remainingDecoded {
			resourceCapped = true
			break
		}
		if !time.Now().Before(h.deadline) {
			resourceCapped = true
			break
		}
		sum := sha256.Sum256(plain)
		if hex.EncodeToString(sum[:]) != sourceSHA {
			return h.metrics, false, errors.New("scrub source checksum mismatch")
		}
		last = key
		count++
		pageDecoded += int64(len(plain))
		h.metrics.ScannedRows++
		h.metrics.StoredBytes += int64(len(p.Stored))
		h.metrics.PlaintextBytes += int64(len(plain))
	}
	if err = rows.Err(); err != nil {
		return h.metrics, false, xerrors.Errorf("iterate scrub page: %w", err)
	}
	if err = rows.Close(); err != nil {
		return h.metrics, false, xerrors.Errorf("close scrub page: %w", err)
	}
	if count == 0 {
		if resourceCapped {
			return h.metrics, false, errors.New("scrub resource cap reached; resume scrub with the persisted checkpoint")
		}
		return h.metrics, true, nil
	}
	if a.beforePersistence != nil {
		a.beforePersistence("scrub-progress")
	}
	if err = a.recheckExpectedTarget(h.id, h.config, true); err != nil {
		return h.metrics, false, err
	}
	if err = persistScrubProgress(ctx, h.session, h.runID, h.leaseToken, f, last, count, h.guard); err != nil {
		return h.metrics, false, err
	}
	if err = observeWALPeak(h.id.canonical, h.minimumWAL, h.config.MaxWALBytes, &h.metrics.PeakWALBytes); err != nil {
		return h.metrics, false, err
	}
	if resourceCapped {
		return h.metrics, false, errors.New("scrub resource cap reached; resume scrub with the persisted checkpoint")
	}
	return h.metrics, false, nil
}

// CompleteScrub verifies cardinality and transitions a fully scrubbed run.
func (a *PayloadRehearsalAdapter) CompleteScrub(ctx context.Context, opaque application.PayloadRehearsalScrubHandle) (apptypes.PayloadRehearsalMetrics, error) {
	h, err := ownedScrubHandle(a, opaque)
	if err != nil {
		return apptypes.PayloadRehearsalMetrics{}, err
	}
	if err = requireLiveIdentityOnly(ctx, h.config.LivePath); err != nil {
		h.metrics.LiveIdentityOnly = false
		return h.metrics, err
	}
	var mismatched int
	if err = h.db.QueryRowContext(ctx, `SELECT count(*) FROM payload_rehearsal_checkpoints c WHERE c.run_id=? AND (c.changed_rows<>(SELECT count(*) FROM payload_rehearsal_rows r WHERE r.run_id=c.run_id AND r.table_kind=c.table_kind AND r.field_kind=c.field_kind) OR c.scrubbed_rows<>c.changed_rows OR c.scrub_last_primary_key<>coalesce((SELECT max(r.source_primary_key) FROM payload_rehearsal_rows r WHERE r.run_id=c.run_id AND r.table_kind=c.table_kind AND r.field_kind=c.field_kind),''))`, h.runID).Scan(&mismatched); err != nil || mismatched != 0 {
		return h.metrics, errors.New("scrub shadow/checkpoint cardinality mismatch")
	}
	var changedTotal, shadowTotal int64
	if err = h.db.QueryRowContext(ctx, `SELECT coalesce(sum(changed_rows),0) FROM payload_rehearsal_checkpoints WHERE run_id=?`, h.runID).Scan(&changedTotal); err != nil {
		return h.metrics, xerrors.Errorf("count scrubbed rows: %w", err)
	}
	if err = h.db.QueryRowContext(ctx, `SELECT count(*) FROM payload_rehearsal_rows WHERE run_id=?`, h.runID).Scan(&shadowTotal); err != nil || changedTotal != shadowTotal {
		return h.metrics, errors.New("scrub shadow total mismatch")
	}
	if a.beforePersistence != nil {
		a.beforePersistence("scrub-complete")
	}
	if err = a.recheckExpectedTarget(h.id, h.config, true); err != nil {
		return h.metrics, err
	}
	if err = transitionTerminalWithinWALBudget(ctx, h.session, h.runID, h.leaseToken, apptypes.PayloadRehearsalScrubbed, h.guard); err != nil {
		return h.metrics, err
	}
	h.leaseActive = false
	h.metrics.State = "scrubbed"
	return h.metrics, nil
}

// ReleaseScrub releases an active lease without completing the run.
func (a *PayloadRehearsalAdapter) ReleaseScrub(ctx context.Context, opaque application.PayloadRehearsalScrubHandle) error {
	h, err := ownedScrubHandle(a, opaque)
	if err != nil {
		return err
	}
	if !h.leaseActive {
		return nil
	}
	if err = releaseScrubLease(ctx, h.session, h.runID, h.leaseToken, h.guard); err != nil {
		return err
	}
	h.leaseActive = false
	return nil
}

// CloseScrub releases the database connection held by a scrub handle.
func (a *PayloadRehearsalAdapter) CloseScrub(opaque application.PayloadRehearsalScrubHandle) error {
	h, err := ownedScrubHandle(a, opaque)
	if err != nil {
		return err
	}
	err = h.db.Close()
	h.db = nil
	if err != nil {
		return xerrors.Errorf("close payload rehearsal scrub database: %w", err)
	}
	return nil
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
	return apptypes.PayloadRehearsalMetrics{State: "rolled_back", RollbackDigest: digest, RollbackVerified: true, LiveIdentityOnly: liveIdentity}, nil
}

func inspectLiveCompatibility(ctx context.Context, path string) (bool, error) {
	db := OpenCoordinatedSQLite(path, liveReadOnlyDSN(path))
	var err error
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
