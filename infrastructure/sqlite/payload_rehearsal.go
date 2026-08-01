package sqlite

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
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
type PayloadRehearsalAdapter struct{ migrations fs.FS }

// NewPayloadRehearsalAdapter binds the migration set used by copied targets.
func NewPayloadRehearsalAdapter(migrations fs.FS) *PayloadRehearsalAdapter {
	return &PayloadRehearsalAdapter{migrations: migrations}
}

var _ application.PayloadRehearsalPreview = (*PayloadRehearsalAdapter)(nil)
var _ application.PayloadRehearsalRunner = (*PayloadRehearsalAdapter)(nil)

type rehearsalIdentity struct {
	canonical string
	info      os.FileInfo
	opaque    string
}

func inspectRehearsalTarget(target, live string) (rehearsalIdentity, error) {
	t, err := secureFileIdentity(target)
	if err != nil {
		return rehearsalIdentity{}, ErrUnsafeRehearsalTarget
	}
	l, err := secureFileIdentity(live)
	if err != nil {
		return rehearsalIdentity{}, ErrUnsafeRehearsalTarget
	}
	if t.canonical == l.canonical || os.SameFile(t.info, l.info) {
		return rehearsalIdentity{}, ErrUnsafeRehearsalTarget
	}
	for _, suffix := range []string{"-wal", "-shm"} {
		if aliasesExistingFile(target+suffix, live+suffix) {
			return rehearsalIdentity{}, ErrUnsafeRehearsalTarget
		}
	}
	return t, nil
}

//nolint:wrapcheck // callers deliberately collapse filesystem details into a safe typed error.
func secureFileIdentity(path string) (rehearsalIdentity, error) {
	abs, err := filepath.Abs(filepath.Clean(path))
	if err != nil {
		return rehearsalIdentity{}, err
	}
	// Reject symlinks in every existing path component, not merely the leaf.
	cur := string(filepath.Separator)
	volume := filepath.VolumeName(abs)
	if volume != "" {
		cur = volume + string(filepath.Separator)
	}
	parts := strings.Split(strings.TrimPrefix(abs, cur), string(filepath.Separator))
	for _, part := range parts {
		if part == "" {
			continue
		}
		cur = filepath.Join(cur, part)
		fi, e := os.Lstat(cur)
		if e != nil {
			return rehearsalIdentity{}, e
		}
		if fi.Mode()&os.ModeSymlink != 0 {
			return rehearsalIdentity{}, ErrUnsafeRehearsalTarget
		}
	}
	resolved, err := filepath.EvalSymlinks(abs)
	if err != nil {
		return rehearsalIdentity{}, err
	}
	info, err := os.Stat(resolved)
	if err != nil {
		return rehearsalIdentity{}, err
	}
	if !info.Mode().IsRegular() {
		return rehearsalIdentity{}, ErrUnsafeRehearsalTarget
	}
	if !fileLinkCountOne(info) {
		return rehearsalIdentity{}, ErrUnsafeRehearsalTarget
	}
	sum := sha256.Sum256([]byte(resolved))
	return rehearsalIdentity{canonical: resolved, info: info, opaque: hex.EncodeToString(sum[:])}, nil
}

func aliasesExistingFile(a, b string) bool {
	ai, ae := os.Lstat(a)
	bi, be := os.Lstat(b)
	if ae != nil || be != nil {
		return false
	}
	if ai.Mode()&os.ModeSymlink != 0 || bi.Mode()&os.ModeSymlink != 0 {
		return true
	}
	return os.SameFile(ai, bi)
}

func componentSnapshots(path string) ([]apptypes.PayloadRehearsalFileState, error) {
	result := make([]apptypes.PayloadRehearsalFileState, 0, 3)
	for _, component := range []struct{ name, suffix string }{{"db", ""}, {"wal", "-wal"}, {"shm", "-shm"}} {
		fi, err := os.Lstat(path + component.suffix)
		if os.IsNotExist(err) {
			result = append(result, apptypes.PayloadRehearsalFileState{Component: component.name})
			continue
		}
		if err != nil || fi.Mode()&os.ModeSymlink != 0 {
			return nil, ErrUnsafeRehearsalTarget
		}
		s := sha256.Sum256([]byte(fmt.Sprintf("%s:%d:%d", component.name, fi.Size(), fi.ModTime().UnixNano())))
		result = append(result, apptypes.PayloadRehearsalFileState{Component: component.name, Exists: true, SizeBytes: fi.Size(), ModUnixNS: fi.ModTime().UnixNano(), Identity: hex.EncodeToString(s[:])})
	}
	return result, nil
}

func immutableRehearsalDSN(path string) string {
	q := url.Values{}
	q.Set("mode", "ro")
	q.Set("immutable", "1")
	q.Add("_pragma", "query_only(1)")
	return (&url.URL{Scheme: "file", Path: path, RawQuery: q.Encode()}).String()
}

// Preview proves a strictly immutable, zero-write inspection.
func (a *PayloadRehearsalAdapter) Preview(ctx context.Context, c apptypes.PayloadRehearsalConfig) (apptypes.PayloadRehearsalMetrics, error) {
	id, err := inspectRehearsalTarget(c.TargetPath, c.LivePath)
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
	if bookkeeping == 0 {
		return apptypes.PayloadRehearsalMetrics{}, ErrRehearsalMigrationRequired
	}
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
	return apptypes.PayloadRehearsalMetrics{State: "planned", ScannedRows: eventRows + auditRows*3, PlaintextBytes: eventBytes + auditBytes, FreeBytes: free, EstimatedHeadroom: uint64(eventBytes + auditBytes), DryRunZeroWrite: true, LiveIdentityOnly: liveIdentityOnly(ctx, c.LivePath), Before: before, After: after}, nil
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
	return (&url.URL{Scheme: "file", Path: path, RawQuery: q.Encode()}).String()
}

// Run creates or resumes bounded zstd shadow rows without changing canonical payloads.
func (a *PayloadRehearsalAdapter) Run(ctx context.Context, c apptypes.PayloadRehearsalConfig, resume bool) (apptypes.PayloadRehearsalMetrics, error) {
	id, err := inspectRehearsalTarget(c.TargetPath, c.LivePath)
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
	configHash := hashConfig(c, id.opaque)
	runID, eventHigh, auditHigh, err := loadOrCreateRun(ctx, db, id.opaque, configHash, backupDigest, resume)
	if err != nil {
		return apptypes.PayloadRehearsalMetrics{}, err
	}
	metrics := apptypes.PayloadRehearsalMetrics{RunID: runID, State: "running", RollbackDigest: backupDigest, RollbackVerified: true, LiveIdentityOnly: liveIdentityOnly(ctx, c.LivePath), Before: parts, FreeBytes: freeBytes, EstimatedHeadroom: requiredHeadroom, BatchDurationHistogram: map[string]int64{"lt_1ms": 0, "lt_10ms": 0, "lt_100ms": 0, "gte_100ms": 0}}
	deadline := time.Now().Add(c.WallTimeLimit)
	for _, f := range rehearsalFields {
		high := eventHigh
		if f.table == "command_audits" {
			high = auditHigh
		}
		for time.Now().Before(deadline) {
			batch, done, e := selectRehearsalBatch(ctx, db, runID, f, high, c)
			if e != nil {
				return metrics, pauseRun(ctx, db, runID, e)
			}
			if done {
				break
			}
			for i := range batch {
				batch[i].encoded, e = encodePayload(batch[i].plaintext, payloadCodecZstd)
				if e != nil {
					return metrics, pauseRun(ctx, db, runID, e)
				}
			}
			currentIdentity, identityErr := secureFileIdentity(id.canonical)
			if identityErr != nil || !os.SameFile(id.info, currentIdentity.info) {
				return metrics, pauseRun(ctx, db, runID, ErrUnsafeRehearsalTarget)
			}
			start := time.Now()
			e = commitRehearsalBatch(ctx, db, runID, f, batch)
			if e != nil {
				return metrics, pauseRun(ctx, db, runID, e)
			}
			batchDuration := time.Since(start)
			recordBatchDuration(metrics.BatchDurationHistogram, batchDuration)
			if batchDuration > c.LockTimeLimit {
				return metrics, pauseRun(ctx, db, runID, errors.New("rehearsal lock duration cap exceeded"))
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
		}
		if !time.Now().Before(deadline) {
			metrics.State = "paused"
			_ = setRunState(ctx, db, runID, "paused")
			metrics.After, _ = componentSnapshots(id.canonical)
			return metrics, nil
		}
	}
	if err = setRunState(ctx, db, runID, "completed"); err != nil {
		return metrics, err
	}
	metrics.State = "completed"
	metrics.After, _ = componentSnapshots(id.canonical)
	return metrics, nil
}

func recordBatchDuration(histogram map[string]int64, duration time.Duration) {
	switch {
	case duration < time.Millisecond:
		histogram["lt_1ms"]++
	case duration < 10*time.Millisecond:
		histogram["lt_10ms"]++
	case duration < 100*time.Millisecond:
		histogram["lt_100ms"]++
	default:
		histogram["gte_100ms"]++
	}
}

func hashConfig(c apptypes.PayloadRehearsalConfig, fingerprint string) string {
	raw := fmt.Sprintf("%s:%d:%d:%d:%s:%s:%d:%s", fingerprint, c.BatchRows, c.StoredByteLimit, c.DecodedByteLimit, c.WallTimeLimit, c.LockTimeLimit, c.ScrubByteLimit, c.ScrubTimeLimit)
	s := sha256.Sum256([]byte(raw))
	return hex.EncodeToString(s[:])
}

//nolint:wrapcheck // the caller adds a payload-free operation classification.
func randomRunID() (string, error) {
	b := make([]byte, 16)
	if _, e := rand.Read(b); e != nil {
		return "", e
	}
	return hex.EncodeToString(b), nil
}

//nolint:wrapcheck // SQL details stay inside this adapter and public boundaries add context.
func loadOrCreateRun(ctx context.Context, db *sql.DB, fingerprint, configHash, backupDigest string, resume bool) (string, string, string, error) {
	var run, state, storedConfig, eventHigh, auditHigh, storedBackup string
	err := db.QueryRowContext(ctx, `SELECT run_id,state,config_hash,event_high_water,audit_high_water,rollback_digest FROM payload_rehearsal_runs WHERE target_fingerprint=? AND state IN ('running','paused','completed','scrubbed')`, fingerprint).Scan(&run, &state, &storedConfig, &eventHigh, &auditHigh, &storedBackup)
	if err == nil {
		if !resume || storedConfig != configHash || storedBackup != backupDigest || state == "completed" || state == "scrubbed" {
			return "", "", "", errors.New("rehearsal run state does not permit this operation")
		}
		if e := setRunState(ctx, db, run, "running"); e != nil {
			return "", "", "", e
		}
		return run, eventHigh, auditHigh, nil
	}
	if err != sql.ErrNoRows {
		return "", "", "", xerrors.Errorf("inspect rehearsal run: %w", err)
	}
	if resume {
		return "", "", "", errors.New("no resumable rehearsal run")
	}
	run, err = randomRunID()
	if err != nil {
		return "", "", "", xerrors.Errorf("generate opaque rehearsal run: %w", err)
	}
	if e := db.QueryRowContext(ctx, `SELECT coalesce(max(id),'') FROM events`).Scan(&eventHigh); e != nil {
		return "", "", "", e
	}
	if e := db.QueryRowContext(ctx, `SELECT coalesce(max(event_id),'') FROM command_audits`).Scan(&auditHigh); e != nil {
		return "", "", "", e
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	tx, e := db.BeginTx(ctx, nil)
	if e != nil {
		return "", "", "", e
	}
	defer func() { _ = tx.Rollback() }()
	if _, e = tx.ExecContext(ctx, `INSERT INTO payload_rehearsal_runs(run_id,target_fingerprint,config_hash,state,event_high_water,audit_high_water,started_at,updated_at,rollback_digest) VALUES(?,?,?,'running',?,?,?,?,?)`, run, fingerprint, configHash, eventHigh, auditHigh, now, now, backupDigest); e != nil {
		return "", "", "", e
	}
	for _, f := range rehearsalFields {
		if _, e = tx.ExecContext(ctx, `INSERT INTO payload_rehearsal_checkpoints(run_id,table_kind,field_kind,state) VALUES(?,?,?,'pending')`, run, f.table, f.field); e != nil {
			return "", "", "", e
		}
	}
	if e = tx.Commit(); e != nil {
		return "", "", "", e
	}
	return run, eventHigh, auditHigh, nil
}

//nolint:wrapcheck // fixed SQL lane failures are classified by the public operation.
func selectRehearsalBatch(ctx context.Context, db *sql.DB, run string, f rehearsalField, high string, c apptypes.PayloadRehearsalConfig) ([]selectedPayload, bool, error) {
	var last, state string
	if err := db.QueryRowContext(ctx, `SELECT last_primary_key,state FROM payload_rehearsal_checkpoints WHERE run_id=? AND table_kind=? AND field_kind=?`, run, f.table, f.field).Scan(&last, &state); err != nil {
		return nil, false, err
	}
	if state == "complete" {
		return nil, true, nil
	}
	q := fmt.Sprintf(`SELECT %s,%s,%s_codec,%s_format_version,%s_plaintext_bytes,%s_encoded_bytes,%s_sha256 FROM %s WHERE %s>? AND %s<=? ORDER BY %s LIMIT ?`, f.pk, f.column, f.codecPrefix, f.codecPrefix, f.codecPrefix, f.codecPrefix, f.codecPrefix, f.table, f.pk, f.pk, f.pk)
	rows, err := db.QueryContext(ctx, q, last, high, c.BatchRows)
	if err != nil {
		return nil, false, err
	}
	defer func() { _ = rows.Close() }()
	batch := make([]selectedPayload, 0, c.BatchRows)
	var stored, decoded int64
	for rows.Next() {
		var key string
		var p payloadRow
		if err = rows.Scan(&key, &p.Stored, &p.Codec, &p.FormatVersion, &p.PlaintextBytes, &p.StoredBytes, &p.SHA256); err != nil {
			return nil, false, err
		}
		if int64(len(p.Stored)) > c.StoredByteLimit-stored {
			return nil, false, errors.New("stored byte cap is smaller than one source payload")
		}
		plain, e := p.decode(c.DecodedByteLimit - decoded)
		if e != nil {
			return nil, false, e
		}
		if len(batch) > 0 && (stored+int64(len(p.Stored)) > c.StoredByteLimit || decoded+int64(len(plain)) > c.DecodedByteLimit) {
			break
		}
		sum := sha256.Sum256(plain)
		batch = append(batch, selectedPayload{key: key, plaintext: plain, sourceSHA: hex.EncodeToString(sum[:])})
		stored += int64(len(p.Stored))
		decoded += int64(len(plain))
	}
	if err = rows.Err(); err != nil {
		return nil, false, err
	}
	if len(batch) == 0 {
		_, err = db.ExecContext(ctx, `UPDATE payload_rehearsal_checkpoints SET state='complete' WHERE run_id=? AND table_kind=? AND field_kind=?`, run, f.table, f.field)
		return nil, true, err
	}
	return batch, false, nil
}

//nolint:wrapcheck // fixed SQL lane failures are classified by the public operation.
func commitRehearsalBatch(ctx context.Context, db *sql.DB, run string, f rehearsalField, batch []selectedPayload) error {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	for _, r := range batch {
		_, err = tx.ExecContext(ctx, `INSERT INTO payload_rehearsal_rows(run_id,table_kind,field_kind,source_primary_key,source_sha256,payload,codec,format_version,plaintext_bytes,stored_bytes,payload_sha256) VALUES(?,?,?,?,?,?,'zstd',1,?,?,?) ON CONFLICT(run_id,table_kind,field_kind,source_primary_key) DO NOTHING`, run, f.table, f.field, r.key, r.sourceSHA, r.encoded.Bytes, r.encoded.PlaintextBytes, r.encoded.StoredBytes, r.encoded.SHA256)
		if err != nil {
			return err
		}
	}
	last := batch[len(batch)-1].key
	var plain, stored int64
	for _, r := range batch {
		plain += int64(len(r.plaintext))
		stored += r.encoded.StoredBytes
	}
	_, err = tx.ExecContext(ctx, `UPDATE payload_rehearsal_checkpoints SET last_primary_key=?,state='advancing',scanned_rows=scanned_rows+?,changed_rows=changed_rows+?,plaintext_bytes=plaintext_bytes+?,stored_bytes=stored_bytes+? WHERE run_id=? AND table_kind=? AND field_kind=?`, last, len(batch), len(batch), plain, stored, run, f.table, f.field)
	if err != nil {
		return err
	}
	return tx.Commit()
}

//nolint:wrapcheck // fixed SQL state mutation is internal to the adapter.
func setRunState(ctx context.Context, db *sql.DB, run, state string) error {
	_, err := db.ExecContext(ctx, `UPDATE payload_rehearsal_runs SET state=?,updated_at=?,completed_at=CASE WHEN ? IN ('completed','scrubbed','rolled_back') THEN ? ELSE completed_at END WHERE run_id=?`, state, time.Now().UTC().Format(time.RFC3339Nano), state, time.Now().UTC().Format(time.RFC3339Nano), run)
	return err
}
func pauseRun(ctx context.Context, db *sql.DB, run string, cause error) error {
	pauseCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), time.Second)
	defer cancel()
	_ = setRunState(pauseCtx, db, run, "paused")
	return cause
}

// Scrub decodes and verifies shadow payloads under persisted bounded checkpoints.
//
//nolint:wrapcheck // public errors are payload-free and already operation scoped.
func (a *PayloadRehearsalAdapter) Scrub(ctx context.Context, c apptypes.PayloadRehearsalConfig) (apptypes.PayloadRehearsalMetrics, error) {
	id, err := inspectRehearsalTarget(c.TargetPath, c.LivePath)
	if err != nil {
		return apptypes.PayloadRehearsalMetrics{}, err
	}
	db, err := sql.Open("sqlite", writableRehearsalDSN(id.canonical, c.LockTimeLimit))
	if err != nil {
		return apptypes.PayloadRehearsalMetrics{}, err
	}
	defer func() { _ = db.Close() }()
	var run string
	if err = db.QueryRowContext(ctx, `SELECT run_id FROM payload_rehearsal_runs WHERE target_fingerprint=? AND state IN ('completed','scrubbed')`, id.opaque).Scan(&run); err != nil {
		return apptypes.PayloadRehearsalMetrics{}, errors.New("no completed rehearsal run")
	}
	deadline := time.Now().Add(c.ScrubTimeLimit)
	m := apptypes.PayloadRehearsalMetrics{RunID: run, State: "scrubbing", LiveIdentityOnly: liveIdentityOnly(ctx, c.LivePath)}
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
			if _, err = db.ExecContext(ctx, `UPDATE payload_rehearsal_checkpoints SET scrub_last_primary_key=?,scrubbed_rows=scrubbed_rows+? WHERE run_id=? AND table_kind=? AND field_kind=?`, last, count, run, f.table, f.field); err != nil {
				return m, err
			}
		}
	}
	if err = setRunState(ctx, db, run, "scrubbed"); err != nil {
		return m, err
	}
	m.State = "scrubbed"
	return m, nil
}

// Rollback restores and verifies the physical artifact for the copied target.
//
//nolint:wrapcheck // public errors are sanitized rollback classifications.
func (a *PayloadRehearsalAdapter) Rollback(ctx context.Context, c apptypes.PayloadRehearsalConfig) (apptypes.PayloadRehearsalMetrics, error) {
	id, err := inspectRehearsalTarget(c.TargetPath, c.LivePath)
	if err != nil {
		return apptypes.PayloadRehearsalMetrics{}, err
	}
	if c.BackupPath == "" {
		return apptypes.PayloadRehearsalMetrics{}, errors.New("rollback artifact is required")
	}
	digest, err := fileDigest(c.BackupPath)
	if err != nil {
		return apptypes.PayloadRehearsalMetrics{}, errors.New("rollback artifact unavailable")
	}
	if _, err = os.Stat(id.canonical + "-wal"); !os.IsNotExist(err) {
		return apptypes.PayloadRehearsalMetrics{}, ErrRehearsalNeedsCleanDB
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
	var integrity string
	if err = db.QueryRowContext(ctx, `PRAGMA integrity_check`).Scan(&integrity); err != nil || integrity != "ok" {
		return apptypes.PayloadRehearsalMetrics{}, errors.New("rollback integrity verification failed")
	}
	return apptypes.PayloadRehearsalMetrics{State: "rolled_back", RollbackDigest: digest, RollbackVerified: true, LiveIdentityOnly: liveIdentityOnly(ctx, c.LivePath)}, nil
}

func ensurePhysicalBackup(source, dest string) (string, error) {
	sourceDigest, err := fileDigest(source)
	if err != nil {
		return "", err
	}
	if existing, existingErr := fileDigest(dest); existingErr == nil {
		if existing != sourceDigest {
			return "", errors.New("existing rollback artifact does not match the copied target")
		}
		return existing, nil
	}
	if err := copyFileAtomic(source, dest); err != nil {
		return "", xerrors.Errorf("create rollback artifact: %w", err)
	}
	return fileDigest(dest)
}

//nolint:wrapcheck // caller provides the safe rollback operation context.
func copyFileAtomic(source, dest string) error {
	in, err := os.Open(source)
	if err != nil {
		return err
	}
	defer func() { _ = in.Close() }()
	tmp := dest + ".tmp"
	out, err := os.OpenFile(tmp, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	ok := false
	defer func() {
		_ = out.Close()
		if !ok {
			_ = os.Remove(tmp)
		}
	}()
	if _, err = io.Copy(out, in); err != nil {
		return err
	}
	if err = out.Sync(); err != nil {
		return err
	}
	if err = out.Close(); err != nil {
		return err
	}
	if err = os.Rename(tmp, dest); err != nil {
		return err
	}
	ok = true
	return nil
}

//nolint:wrapcheck // caller converts path-sensitive failures to fixed messages.
func fileDigest(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer func() { _ = f.Close() }()
	h := sha256.New()
	if _, err = io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}
func liveIdentityOnly(ctx context.Context, path string) bool {
	db, err := sql.Open("sqlite", immutableRehearsalDSN(path))
	if err != nil {
		return false
	}
	defer func() { _ = db.Close() }()
	var n int64
	err = db.QueryRowContext(ctx, `SELECT (SELECT count(*) FROM events WHERE body_codec IS NOT NULL AND body_codec<>'identity')+(SELECT count(*) FROM command_audits WHERE (command_codec IS NOT NULL AND command_codec<>'identity') OR (input_codec IS NOT NULL AND input_codec<>'identity') OR (output_codec IS NOT NULL AND output_codec<>'identity'))`).Scan(&n)
	return err == nil && n == 0
}
