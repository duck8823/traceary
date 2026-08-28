package sqlite

import (
	"context"
	"database/sql"
	"errors"

	"golang.org/x/xerrors"

	"github.com/duck8823/traceary/application"
)

// auditEncodePageRows bounds one work-copy transaction. Eligible rows carrying
// multi-GiB of audit text must not enter one slice the way encodeAvailableEventBodies
// does for events.
const auditEncodePageRows = 500

type auditEncodeKey struct {
	RowID   int64
	EventID string
}

type auditEncodeRowStats struct {
	LanesRewritten int64
	LanesZstd      int64
	LanesIdentity  int64
	BytesBefore    int64
	BytesAfter     int64
}

func encodeCommandAuditPayloadsStep(ctx context.Context, db *sql.DB, filter application.CompactFilter) error {
	hasTable, err := tableExists(ctx, db, "command_audits")
	if err != nil {
		return err
	}
	if !hasTable {
		filter.Report(application.CompactStep{
			Name:    application.CompactStepAuditEncode,
			Skipped: "no command_audits table",
			Detail:  map[string]int64{},
		})
		return nil
	}
	hasCodec, err := databaseColumnExists(ctx, db, "command_audits", "command_codec")
	if err != nil {
		return err
	}
	if !hasCodec {
		filter.Report(application.CompactStep{
			Name:    application.CompactStepAuditEncode,
			Skipped: "no codec columns",
			Detail:  map[string]int64{},
		})
		return nil
	}
	if skipped, reason, modeErr := auditEncodeCompatibilitySkip(ctx, db); modeErr != nil {
		return modeErr
	} else if skipped {
		filter.Report(application.CompactStep{
			Name:    application.CompactStepAuditEncode,
			Skipped: reason,
			Detail:  map[string]int64{},
		})
		return nil
	}
	step, err := encodeCommandAuditPayloadsOn(ctx, db, auditEncodePageRows)
	if err != nil {
		return err
	}
	filter.Report(step)
	return nil
}

func auditEncodeCompatibilitySkip(ctx context.Context, db *sql.DB) (bool, string, error) {
	hasState, err := tableExists(ctx, db, "payload_codec_compatibility_state")
	if err != nil {
		return false, "", err
	}
	if !hasState {
		return true, "payload codec compatibility is missing/missing", nil
	}
	var mode, state string
	err = db.QueryRowContext(ctx, `SELECT mode, state FROM payload_codec_compatibility_state WHERE singleton = 1`).Scan(&mode, &state)
	if errors.Is(err, sql.ErrNoRows) {
		return true, "payload codec compatibility is missing/missing", nil
	}
	if err != nil {
		return false, "", xerrors.Errorf("inspect payload codec compatibility state: %w", err)
	}
	if mode != payloadCodecCompatibilityCounterMode || state != "valid" {
		return true, "payload codec compatibility is " + mode + "/" + state, nil
	}
	return false, "", nil
}

func encodeCommandAuditPayloadsOn(ctx context.Context, db *sql.DB, pageRows int) (application.CompactStep, error) {
	step := application.CompactStep{
		Name: application.CompactStepAuditEncode,
		Detail: map[string]int64{
			"lanes_encoded_zstd":  0,
			"lanes_identity_kept": 0,
			"rows_scanned":        0,
		},
	}
	if pageRows <= 0 {
		pageRows = auditEncodePageRows
	}
	var afterRowID int64
	for {
		keys, err := auditEncodePageKeys(ctx, db, afterRowID, pageRows)
		if err != nil {
			return step, err
		}
		if len(keys) == 0 {
			break
		}
		tx, err := db.BeginTx(ctx, nil)
		if err != nil {
			return step, xerrors.Errorf("begin command audit encode page: %w", err)
		}
		for _, key := range keys {
			stats, rowErr := encodeAuditRow(ctx, tx, key)
			if rowErr != nil {
				_ = tx.Rollback()
				return step, rowErr
			}
			step.Detail["rows_scanned"]++
			step.Detail["lanes_encoded_zstd"] += stats.LanesZstd
			step.Detail["lanes_identity_kept"] += stats.LanesIdentity
			if stats.LanesRewritten > 0 {
				step.Rows++
				step.BytesBefore += stats.BytesBefore
				step.BytesAfter += stats.BytesAfter
			}
		}
		if err := tx.Commit(); err != nil {
			return step, xerrors.Errorf("commit command audit encode page: %w", err)
		}
		afterRowID = keys[len(keys)-1].RowID
		if len(keys) < pageRows {
			break
		}
	}
	if step.BytesBefore > step.BytesAfter {
		step.BytesReclaimed = step.BytesBefore - step.BytesAfter
	}
	return step, nil
}

func auditEncodePageKeys(ctx context.Context, db *sql.DB, afterRowID int64, pageRows int) ([]auditEncodeKey, error) {
	rows, err := db.QueryContext(ctx, `
		SELECT rowid, event_id
		  FROM command_audits
		 WHERE rowid > ?
		   AND (command_codec IS NULL OR input_codec IS NULL OR output_codec IS NULL)
		 ORDER BY rowid
		 LIMIT ?`, afterRowID, pageRows)
	if err != nil {
		return nil, xerrors.Errorf("select command audit encode page keys: %w", err)
	}
	defer func() { _ = rows.Close() }()
	keys := make([]auditEncodeKey, 0, pageRows)
	for rows.Next() {
		var key auditEncodeKey
		if err := rows.Scan(&key.RowID, &key.EventID); err != nil {
			return nil, xerrors.Errorf("scan command audit encode page key: %w", err)
		}
		keys = append(keys, key)
	}
	if err := rows.Err(); err != nil {
		return nil, xerrors.Errorf("iterate command audit encode page keys: %w", err)
	}
	return keys, nil
}

func encodeAuditRow(ctx context.Context, tx *sql.Tx, key auditEncodeKey) (auditEncodeRowStats, error) {
	var stats auditEncodeRowStats
	lanes := commandAuditLanes()
	payloads := make([]payloadRow, len(lanes))
	dest := make([]any, 0, len(lanes)*6)
	for i := range payloads {
		dest = append(dest, payloads[i].scanDestinations()...)
	}
	if err := tx.QueryRowContext(ctx, `
		SELECT command_text, command_codec, command_format_version, command_plaintext_bytes, command_encoded_bytes, command_sha256,
		       input_text,   input_codec,   input_format_version,   input_plaintext_bytes,   input_encoded_bytes,   input_sha256,
		       output_text,  output_codec,  output_format_version,  output_plaintext_bytes,  output_encoded_bytes,  output_sha256
		  FROM command_audits
		 WHERE rowid = ?`, key.RowID).Scan(dest...); err != nil {
		return stats, xerrors.Errorf("load command audit %s for encode: %w", key.EventID, err)
	}
	for i, lane := range lanes {
		row := payloads[i]
		meta := payloadMetadataCount(row)
		if meta == 5 {
			continue
		}
		if meta != 0 {
			return stats, xerrors.Errorf("command audit %s lane %s has partial codec metadata (%d of 5 columns set): refusing to encode", key.EventID, lane.Field, meta)
		}
		plain, err := row.decode(maxDecodedPayloadBytes)
		if err != nil {
			return stats, xerrors.Errorf("decode command audit %s lane %s: %w", key.EventID, lane.Field, err)
		}
		encoded, err := encodeCanonicalPayload(plain, true)
		if err != nil {
			return stats, xerrors.Errorf("encode command audit %s lane %s: %w", key.EventID, lane.Field, err)
		}
		if backfillRowAlreadyMatches(row, encoded) {
			continue
		}
		if _, err := tx.ExecContext(ctx, `
			UPDATE command_audits
			   SET `+lane.Column+` = ?,
			       `+lane.CodecPrefix+`_codec = ?,
			       `+lane.CodecPrefix+`_format_version = ?,
			       `+lane.CodecPrefix+`_plaintext_bytes = ?,
			       `+lane.CodecPrefix+`_encoded_bytes = ?,
			       `+lane.CodecPrefix+`_sha256 = ?
			 WHERE rowid = ?`,
			storedBodyArg(encoded), encoded.Codec, encoded.FormatVersion,
			encoded.PlaintextBytes, encoded.StoredBytes, encoded.SHA256,
			key.RowID,
		); err != nil {
			return stats, xerrors.Errorf("rewrite command audit %s lane %s: %w", key.EventID, lane.Field, err)
		}
		stats.LanesRewritten++
		stats.BytesBefore += int64(len(row.Stored))
		stats.BytesAfter += encoded.StoredBytes
		if encoded.Codec == payloadCodecZstd {
			stats.LanesZstd++
		} else {
			stats.LanesIdentity++
		}
	}
	return stats, nil
}
