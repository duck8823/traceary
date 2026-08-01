package sqlite

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"time"

	apptypes "github.com/duck8823/traceary/application/types"
	"golang.org/x/xerrors"
)

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
func loadOrCreateRun(ctx context.Context, db *sql.DB, identity rehearsalIdentity, configHash, backupDigest string, resume bool) (string, string, string, string, error) {
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
		result, e := db.ExecContext(ctx, `UPDATE payload_rehearsal_runs SET state='running',lease_token=?,lease_expires_at=?,updated_at=? WHERE run_id=? AND (lease_token IS NULL OR lease_expires_at<?)`, lease, now.Add(30*time.Second).Format(time.RFC3339Nano), now.Format(time.RFC3339Nano), run, now.Format(time.RFC3339Nano))
		if e != nil {
			return "", "", "", "", e
		}
		affected, _ := result.RowsAffected()
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
	if e := db.QueryRowContext(ctx, `SELECT coalesce(max(id),'') FROM events`).Scan(&eventHigh); e != nil {
		return "", "", "", "", e
	}
	if e := db.QueryRowContext(ctx, `SELECT coalesce(max(event_id),'') FROM command_audits`).Scan(&auditHigh); e != nil {
		return "", "", "", "", e
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	tx, e := db.BeginTx(ctx, nil)
	if e != nil {
		return "", "", "", "", e
	}
	defer func() { _ = tx.Rollback() }()
	if _, e = tx.ExecContext(ctx, `INSERT INTO payload_rehearsal_runs(run_id,target_fingerprint,config_hash,state,event_high_water,audit_high_water,started_at,updated_at,rollback_digest,target_device,target_inode,lease_token,lease_expires_at) VALUES(?,?,?,'running',?,?,?,?,?,?,?,?,?)`, run, identity.opaque, configHash, eventHigh, auditHigh, now, now, backupDigest, identity.device, identity.inode, lease, time.Now().UTC().Add(30*time.Second).Format(time.RFC3339Nano)); e != nil {
		return "", "", "", "", e
	}
	for _, f := range rehearsalFields {
		if _, e = tx.ExecContext(ctx, `INSERT INTO payload_rehearsal_checkpoints(run_id,table_kind,field_kind,state) VALUES(?,?,?,'pending')`, run, f.table, f.field); e != nil {
			return "", "", "", "", e
		}
	}
	if e = tx.Commit(); e != nil {
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
		_, err = db.ExecContext(ctx, `UPDATE payload_rehearsal_checkpoints SET state='complete' WHERE run_id=? AND table_kind=? AND field_kind=?`, run, f.table, f.field)
		return nil, true, err
	}
	return batch, false, nil
}

//nolint:wrapcheck // fixed SQL lane failures are classified by the public operation.
func commitRehearsalBatch(ctx context.Context, db *sql.DB, run, lease string, f rehearsalField, batch []selectedPayload) error {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	var heldLease string
	if err = tx.QueryRowContext(ctx, `SELECT coalesce(lease_token,'') FROM payload_rehearsal_runs WHERE run_id=?`, run).Scan(&heldLease); err != nil {
		return err
	}
	if heldLease != lease {
		return errors.New("rehearsal run lease was lost")
	}
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
	if _, err = tx.ExecContext(ctx, `UPDATE payload_rehearsal_runs SET lease_expires_at=?,updated_at=? WHERE run_id=? AND lease_token=?`, time.Now().UTC().Add(30*time.Second).Format(time.RFC3339Nano), time.Now().UTC().Format(time.RFC3339Nano), run, lease); err != nil {
		return err
	}
	return tx.Commit()
}

//nolint:wrapcheck // fixed SQL state mutation is internal to the adapter.
func setRunState(ctx context.Context, db *sql.DB, run, state string) error {
	_, err := db.ExecContext(ctx, `UPDATE payload_rehearsal_runs SET state=?,lease_token=NULL,lease_expires_at=NULL,updated_at=?,completed_at=CASE WHEN ? IN ('completed','scrubbed','rolled_back') THEN ? ELSE completed_at END WHERE run_id=?`, state, time.Now().UTC().Format(time.RFC3339Nano), state, time.Now().UTC().Format(time.RFC3339Nano), run)
	return err
}
func setRunStateWithLease(ctx context.Context, db *sql.DB, run, lease, state string) error {
	result, err := db.ExecContext(ctx, `UPDATE payload_rehearsal_runs SET state=?,lease_token=NULL,lease_expires_at=NULL,updated_at=?,completed_at=CASE WHEN ?='completed' THEN ? ELSE completed_at END WHERE run_id=? AND lease_token=?`, state, time.Now().UTC().Format(time.RFC3339Nano), state, time.Now().UTC().Format(time.RFC3339Nano), run, lease)
	if err != nil {
		return err
	}
	affected, _ := result.RowsAffected()
	if affected != 1 {
		return errors.New("rehearsal run lease was lost")
	}
	return nil
}
func pauseRun(ctx context.Context, db *sql.DB, run, lease string, cause error) error {
	pauseCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), time.Second)
	defer cancel()
	_ = setRunStateWithLease(pauseCtx, db, run, lease, "paused")
	return cause
}
