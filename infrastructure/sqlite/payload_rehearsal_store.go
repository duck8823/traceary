package sqlite

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	apptypes "github.com/duck8823/traceary/application/types"
	"golang.org/x/xerrors"
)

type rehearsalMutationGuard func() error

// walBudgetedMutationSession serializes the schema proof, WAL reservation and
// mutation under one BEGIN IMMEDIATE lock.  Keeping these operations on a
// dedicated connection closes the check/use window that exists when a WAL
// stat and a later database/sql transaction can use different connections.
type walBudgetedMutationSession struct {
	db                *sql.DB
	path              string
	expectedSchemaSHA string
	frameBytes        int64
	maximum           int64
	peak              *int64
}

//nolint:wrapcheck // callers add the rehearsal operation classification.
func rehearsalSchemaFingerprint(ctx context.Context, q interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
}) (string, error) {
	rows, err := q.QueryContext(ctx, `SELECT type,name,tbl_name,coalesce(sql,'') FROM sqlite_schema ORDER BY type,name,tbl_name,sql`)
	if err != nil {
		return "", err
	}
	defer func() { _ = rows.Close() }()
	h := sha256.New()
	for rows.Next() {
		var typ, name, table, statement string
		if err = rows.Scan(&typ, &name, &table, &statement); err != nil {
			return "", err
		}
		_, _ = fmt.Fprintf(h, "%d:%s%d:%s%d:%s%d:%s\n", len(typ), typ, len(name), name, len(table), table, len(statement), statement)
	}
	if err = rows.Err(); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

//nolint:wrapcheck // the bounded session is an internal adapter primitive.
func (s walBudgetedMutationSession) run(ctx context.Context, reservedFrames int64, mutate func(*sql.Conn) error) (err error) {
	if reservedFrames <= 0 || s.peak == nil || strings.TrimSpace(s.expectedSchemaSHA) == "" {
		return ErrUnsafeRehearsalTarget
	}
	conn, err := s.db.Conn(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = conn.Close() }()
	if _, err = conn.ExecContext(ctx, `BEGIN IMMEDIATE`); err != nil {
		return err
	}
	defer func() { _, _ = conn.ExecContext(context.WithoutCancel(ctx), `ROLLBACK`) }()
	fingerprint, err := rehearsalSchemaFingerprint(ctx, conn)
	if err != nil {
		return err
	}
	if fingerprint != s.expectedSchemaSHA {
		return errors.New("rehearsal schema changed before budgeted mutation")
	}
	if err = observeWALPeak(s.path, s.frameBytes, s.maximum, s.peak); err != nil {
		return err
	}
	current := int64(0)
	if info, statErr := os.Stat(s.path + "-wal"); statErr == nil {
		current = info.Size()
	} else if !os.IsNotExist(statErr) {
		return errors.New("cannot inspect rehearsal WAL before mutation")
	}
	frame := s.frameBytes - 32
	reservation := reservedFrames * frame
	if current == 0 {
		reservation += 32
	}
	if max(current, *s.peak) > s.maximum-reservation {
		return errors.New("rehearsal WAL hard cap cannot accommodate mutation")
	}
	if err = mutate(conn); err != nil {
		return err
	}
	if _, err = conn.ExecContext(ctx, `COMMIT`); err != nil {
		return err
	}
	return observeWALPeak(s.path, s.frameBytes, s.maximum, s.peak)
}

func batchWALFrames(batch []selectedPayload, frameBytes int64) int64 {
	pageBytes := frameBytes - 32
	var stored, decoded int64
	for _, row := range batch {
		stored += row.encoded.StoredBytes
		decoded += int64(len(row.plaintext))
	}
	// Payload overflow pages are bounded by the actual encoded bytes.  Include
	// decoded bytes as a conservative bound for transient/index record growth,
	// two b-tree pages per row, and the checkpoint/run control pages.
	return (stored+decoded+pageBytes-1)/pageBytes + int64(len(batch))*2 + 4
}

func requireSafeRehearsalMutation(guard rehearsalMutationGuard) error {
	if guard == nil {
		return ErrUnsafeRehearsalTarget
	}
	return guard()
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
	raw := fmt.Sprintf("%s:%d:%d:%d:%s:%s:%d:%s:%d", fingerprint, c.BatchRows, c.StoredByteLimit, c.DecodedByteLimit, c.WallTimeLimit, c.LockTimeLimit, c.ScrubByteLimit, c.ScrubTimeLimit, c.MaxWALBytes)
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
func loadOrCreateRun(ctx context.Context, session walBudgetedMutationSession, identity rehearsalIdentity, configHash, backupDigest string, resume bool, guard rehearsalMutationGuard) (string, string, string, string, error) {
	db := session.db
	lease, leaseErr := randomRunID()
	if leaseErr != nil {
		return "", "", "", "", leaseErr
	}
	var run, state, storedConfig, eventHigh, auditHigh, storedBackup, device, inode string
	err := db.QueryRowContext(ctx, `SELECT run_id,state,config_hash,event_high_water,audit_high_water,rollback_digest,target_device,target_inode FROM payload_rehearsal_runs WHERE target_fingerprint=? AND state IN ('running','paused','completed','scrubbed')`, identity.opaque).Scan(&run, &state, &storedConfig, &eventHigh, &auditHigh, &storedBackup, &device, &inode)
	if err == nil {
		if !resume || storedConfig != configHash || storedBackup != backupDigest || device != identity.device || inode != identity.inode || !apptypes.PayloadRehearsalState(state).CanResume() {
			return "", "", "", "", errors.New("rehearsal run state does not permit this operation")
		}
		now := time.Now().UTC()
		if e := requireSafeRehearsalMutation(guard); e != nil {
			return "", "", "", "", e
		}
		var affected int64
		e := session.run(ctx, 4, func(tx *sql.Conn) error {
			result, updateErr := tx.ExecContext(ctx, `UPDATE payload_rehearsal_runs SET state='running',lease_token=?,lease_expires_at=?,updated_at=? WHERE run_id=? AND (lease_token IS NULL OR lease_expires_at<?)`, lease, now.Add(30*time.Second).Format(time.RFC3339Nano), now.Format(time.RFC3339Nano), run, now.Format(time.RFC3339Nano))
			if updateErr != nil {
				return updateErr
			}
			affected, _ = result.RowsAffected()
			return nil
		})
		if e != nil {
			return "", "", "", "", e
		}
		if affected != 1 {
			return "", "", "", "", errors.New("rehearsal run is leased by another worker")
		}
		return run, eventHigh, auditHigh, lease, nil
	}
	if err != sql.ErrNoRows {
		return "", "", "", "", xerrors.Errorf("inspect rehearsal run: %w", err)
	}
	if resume {
		return "", "", "", "", errors.New("no resumable rehearsal run")
	}
	run, err = randomRunID()
	if err != nil {
		return "", "", "", "", xerrors.Errorf("generate opaque rehearsal run: %w", err)
	}
	if e := requireSafeRehearsalMutation(guard); e != nil {
		return "", "", "", "", e
	}
	e := session.run(ctx, 12, func(tx *sql.Conn) error {
		if queryErr := tx.QueryRowContext(ctx, `SELECT coalesce(max(id),'') FROM events`).Scan(&eventHigh); queryErr != nil {
			return queryErr
		}
		if queryErr := tx.QueryRowContext(ctx, `SELECT coalesce(max(event_id),'') FROM command_audits`).Scan(&auditHigh); queryErr != nil {
			return queryErr
		}
		now := time.Now().UTC().Format(time.RFC3339Nano)
		if _, insertErr := tx.ExecContext(ctx, `INSERT INTO payload_rehearsal_runs(run_id,target_fingerprint,config_hash,state,event_high_water,audit_high_water,started_at,updated_at,rollback_digest,target_device,target_inode,lease_token,lease_expires_at) VALUES(?,?,?,'running',?,?,?,?,?,?,?,?,?)`, run, identity.opaque, configHash, eventHigh, auditHigh, now, now, backupDigest, identity.device, identity.inode, lease, time.Now().UTC().Add(30*time.Second).Format(time.RFC3339Nano)); insertErr != nil {
			return insertErr
		}
		for _, f := range rehearsalFields {
			if _, insertErr := tx.ExecContext(ctx, `INSERT INTO payload_rehearsal_checkpoints(run_id,table_kind,field_kind,state) VALUES(?,?,?,'pending')`, run, f.table, f.field); insertErr != nil {
				return insertErr
			}
		}
		return nil
	})
	if e != nil {
		return "", "", "", "", e
	}
	return run, eventHigh, auditHigh, lease, nil
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
	q := fmt.Sprintf(`SELECT %s,length(%s),CASE WHEN length(%s)<=? THEN %s ELSE NULL END,%s_codec,%s_format_version,%s_plaintext_bytes,%s_encoded_bytes,%s_sha256 FROM %s WHERE %s>? AND %s<=? ORDER BY %s LIMIT ?`, f.pk, f.column, f.column, f.column, f.codecPrefix, f.codecPrefix, f.codecPrefix, f.codecPrefix, f.codecPrefix, f.table, f.pk, f.pk, f.pk)
	rows, err := db.QueryContext(ctx, q, c.StoredByteLimit, last, high, c.BatchRows)
	if err != nil {
		return nil, false, err
	}
	defer func() { _ = rows.Close() }()
	batch := make([]selectedPayload, 0, c.BatchRows)
	var stored, decoded int64
	for rows.Next() {
		var key string
		var payloadLength int64
		var p payloadRow
		if err = rows.Scan(&key, &payloadLength, &p.Stored, &p.Codec, &p.FormatVersion, &p.PlaintextBytes, &p.StoredBytes, &p.SHA256); err != nil {
			return nil, false, err
		}
		if payloadLength > c.StoredByteLimit {
			if len(batch) > 0 {
				break
			}
			return nil, false, errors.New("stored byte cap is smaller than one source payload")
		}
		if int64(len(p.Stored)) > c.StoredByteLimit-stored {
			if len(batch) > 0 {
				break
			}
			return nil, false, errors.New("stored byte cap is smaller than one source payload")
		}
		expectedDecoded := int64(len(p.Stored))
		if p.PlaintextBytes.Valid {
			expectedDecoded = p.PlaintextBytes.Int64
		}
		if expectedDecoded > c.DecodedByteLimit-decoded && len(batch) > 0 {
			break
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
		return nil, true, nil
	}
	return batch, false, nil
}

//nolint:wrapcheck // caller performs target guard immediately before this primitive.
func markRehearsalLaneComplete(ctx context.Context, session walBudgetedMutationSession, run, lease string, f rehearsalField, guard rehearsalMutationGuard) error {
	if err := requireSafeRehearsalMutation(guard); err != nil {
		return err
	}
	var affected int64
	err := session.run(ctx, 4, func(tx *sql.Conn) error {
		result, updateErr := tx.ExecContext(ctx, `UPDATE payload_rehearsal_checkpoints SET state='complete' WHERE run_id=? AND table_kind=? AND field_kind=? AND EXISTS(SELECT 1 FROM payload_rehearsal_runs WHERE run_id=? AND lease_token=?)`, run, f.table, f.field, run, lease)
		if updateErr != nil {
			return updateErr
		}
		affected, _ = result.RowsAffected()
		return nil
	})
	if err != nil {
		return err
	}
	if affected != 1 {
		return errors.New("rehearsal run lease was lost")
	}
	return nil
}

//nolint:wrapcheck // fixed SQL lane failures are classified by the public operation.
func commitRehearsalBatch(ctx context.Context, session walBudgetedMutationSession, run, lease string, f rehearsalField, batch []selectedPayload, guard rehearsalMutationGuard) error {
	if err := requireSafeRehearsalMutation(guard); err != nil {
		return err
	}
	return session.run(ctx, batchWALFrames(batch, session.frameBytes), func(tx *sql.Conn) error {
		var heldLease string
		if err := tx.QueryRowContext(ctx, `SELECT coalesce(lease_token,'') FROM payload_rehearsal_runs WHERE run_id=?`, run).Scan(&heldLease); err != nil {
			return err
		}
		if heldLease != lease {
			return errors.New("rehearsal run lease was lost")
		}
		for _, r := range batch {
			if _, err := tx.ExecContext(ctx, `INSERT INTO payload_rehearsal_rows(run_id,table_kind,field_kind,source_primary_key,source_sha256,payload,codec,format_version,plaintext_bytes,stored_bytes,payload_sha256) VALUES(?,?,?,?,?,?,'zstd',1,?,?,?) ON CONFLICT(run_id,table_kind,field_kind,source_primary_key) DO NOTHING`, run, f.table, f.field, r.key, r.sourceSHA, r.encoded.Bytes, r.encoded.PlaintextBytes, r.encoded.StoredBytes, r.encoded.SHA256); err != nil {
				return err
			}
		}
		last := batch[len(batch)-1].key
		var plain, stored int64
		for _, r := range batch {
			plain += int64(len(r.plaintext))
			stored += r.encoded.StoredBytes
		}
		if _, err := tx.ExecContext(ctx, `UPDATE payload_rehearsal_checkpoints SET last_primary_key=?,state='advancing',scanned_rows=scanned_rows+?,changed_rows=changed_rows+?,plaintext_bytes=plaintext_bytes+?,stored_bytes=stored_bytes+? WHERE run_id=? AND table_kind=? AND field_kind=?`, last, len(batch), len(batch), plain, stored, run, f.table, f.field); err != nil {
			return err
		}
		_, err := tx.ExecContext(ctx, `UPDATE payload_rehearsal_runs SET lease_expires_at=?,updated_at=? WHERE run_id=? AND lease_token=?`, time.Now().UTC().Add(30*time.Second).Format(time.RFC3339Nano), time.Now().UTC().Format(time.RFC3339Nano), run, lease)
		return err
	})
}

//nolint:wrapcheck // fixed SQL transition failures are classified by the caller.
func transitionRunWithLease(ctx context.Context, session walBudgetedMutationSession, run, lease string, state apptypes.PayloadRehearsalState, guard rehearsalMutationGuard) error {
	if err := requireSafeRehearsalMutation(guard); err != nil {
		return err
	}
	var affected int64
	err := session.run(ctx, 4, func(tx *sql.Conn) error {
		now := time.Now().UTC().Format(time.RFC3339Nano)
		result, updateErr := tx.ExecContext(ctx, `UPDATE payload_rehearsal_runs SET state=?,lease_token=NULL,lease_expires_at=NULL,updated_at=?,completed_at=CASE WHEN ? IN ('completed','scrubbed') THEN ? ELSE completed_at END WHERE run_id=? AND lease_token=?`, string(state), now, string(state), now, run, lease)
		if updateErr != nil {
			return updateErr
		}
		affected, _ = result.RowsAffected()
		return nil
	})
	if err != nil {
		return xerrors.Errorf("transition rehearsal run with lease: %w", err)
	}
	if affected != 1 {
		return errors.New("rehearsal run lease was lost")
	}
	return nil
}

func pauseRunState(ctx context.Context, session walBudgetedMutationSession, run, lease string, guard rehearsalMutationGuard) error {
	return transitionRunWithLease(ctx, session, run, lease, apptypes.PayloadRehearsalPaused, guard)
}
func pauseRun(ctx context.Context, session walBudgetedMutationSession, run, lease string, cause error, guard rehearsalMutationGuard) error {
	pauseCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), time.Second)
	defer cancel()
	if err := pauseRunState(pauseCtx, session, run, lease, guard); err != nil {
		return err
	}
	return cause
}

//nolint:wrapcheck // internal SQL lease errors are classified by Scrub.
func acquireScrubLease(ctx context.Context, session walBudgetedMutationSession, run string, guard rehearsalMutationGuard) (string, error) {
	lease, err := randomRunID()
	if err != nil {
		return "", err
	}
	now := time.Now().UTC()
	if err = requireSafeRehearsalMutation(guard); err != nil {
		return "", err
	}
	var affected int64
	err = session.run(ctx, 4, func(tx *sql.Conn) error {
		result, updateErr := tx.ExecContext(ctx, `UPDATE payload_rehearsal_runs SET lease_token=?,lease_expires_at=?,updated_at=? WHERE run_id=? AND state='completed' AND (lease_token IS NULL OR lease_expires_at<?)`, lease, now.Add(30*time.Second).Format(time.RFC3339Nano), now.Format(time.RFC3339Nano), run, now.Format(time.RFC3339Nano))
		if updateErr != nil {
			return updateErr
		}
		affected, _ = result.RowsAffected()
		return nil
	})
	if err != nil {
		return "", err
	}
	if affected != 1 {
		return "", errors.New("rehearsal scrub is leased or not completed")
	}
	return lease, nil
}

//nolint:wrapcheck // internal SQL progress is guarded by the scrub lease.
func persistScrubProgress(ctx context.Context, session walBudgetedMutationSession, run, lease string, f rehearsalField, last string, count int, guard rehearsalMutationGuard) error {
	if count == 0 {
		return nil
	}
	if err := requireSafeRehearsalMutation(guard); err != nil {
		return err
	}
	return session.run(ctx, 6, func(tx *sql.Conn) error {
		var held string
		if err := tx.QueryRowContext(ctx, `SELECT coalesce(lease_token,'') FROM payload_rehearsal_runs WHERE run_id=?`, run).Scan(&held); err != nil {
			return err
		}
		if held != lease {
			return errors.New("rehearsal scrub lease was lost")
		}
		if _, err := tx.ExecContext(ctx, `UPDATE payload_rehearsal_checkpoints SET scrub_last_primary_key=?,scrubbed_rows=scrubbed_rows+? WHERE run_id=? AND table_kind=? AND field_kind=?`, last, count, run, f.table, f.field); err != nil {
			return err
		}
		_, err := tx.ExecContext(ctx, `UPDATE payload_rehearsal_runs SET lease_expires_at=? WHERE run_id=? AND lease_token=?`, time.Now().UTC().Add(30*time.Second).Format(time.RFC3339Nano), run, lease)
		return err
	})
}

//nolint:wrapcheck // best-effort lease cleanup is classified at the Scrub boundary.
func releaseScrubLease(ctx context.Context, session walBudgetedMutationSession, run, lease string, guard rehearsalMutationGuard) error {
	if err := requireSafeRehearsalMutation(guard); err != nil {
		return err
	}
	return session.run(ctx, 4, func(tx *sql.Conn) error {
		_, err := tx.ExecContext(ctx, `UPDATE payload_rehearsal_runs SET lease_token=NULL,lease_expires_at=NULL WHERE run_id=? AND lease_token=?`, run, lease)
		return err
	})
}
