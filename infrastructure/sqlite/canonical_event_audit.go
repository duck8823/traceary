package sqlite

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"unicode/utf8"

	"github.com/duck8823/traceary/domain"
)

const (
	canonicalRowDomain   = "traceary/canonical-event-audit/v1/row"
	canonicalTableDomain = "traceary/canonical-event-audit/v1/table"
	canonicalStoreDomain = "traceary/canonical-event-audit/v1/store"
)

var canonicalEventColumns = []string{
	"id", "kind", "agent", "session_id", "body", "created_at", "client", "workspace", "source_hook",
	"body_original_bytes", "body_stored_bytes", "body_ingest_truncated", "body_storage_truncated", "body_metadata_version",
	"body_pruned_at", "body_pruned_plan_id", "created_at_norm",
}

var canonicalAuditColumns = []string{
	"event_id", "command_text", "input_text", "output_text", "input_truncated", "output_truncated", "exit_code", "failed",
	"input_original_bytes", "output_original_bytes", "command_wrapper", "command_name", "failure_reason",
}

type canonicalAccumulator struct {
	count uint64
	xor   [32]byte
	sums  [4]uint64
}

func (a *canonicalAccumulator) add(digest [32]byte) {
	a.count++
	for i := range a.xor {
		a.xor[i] ^= digest[i]
	}
	for i := range a.sums {
		a.sums[i] += binary.BigEndian.Uint64(digest[i*8 : (i+1)*8])
	}
}

func canonicalFrame(value []byte) []byte {
	framed := make([]byte, 8+len(value))
	binary.BigEndian.PutUint64(framed[:8], uint64(len(value)))
	copy(framed[8:], value)
	return framed
}

func canonicalEncodedValue(value any) ([]byte, error) {
	switch typed := value.(type) {
	case nil:
		return []byte{0x00}, nil
	case int64:
		encoded := make([]byte, 9)
		encoded[0] = 0x01
		binary.BigEndian.PutUint64(encoded[1:], uint64(typed))
		return encoded, nil
	case string:
		if !utf8.ValidString(typed) {
			return nil, errors.New("canonical text is not valid UTF-8")
		}
		return append([]byte{0x02}, canonicalFrame([]byte(typed))...), nil
	case []byte:
		return append([]byte{0x03}, canonicalFrame(typed)...), nil
	default:
		return nil, fmt.Errorf("unsupported canonical SQLite value type %T", value)
	}
}

func canonicalRowDigest(table string, columns []string, values []any) ([32]byte, error) {
	if len(columns) != len(values) {
		return [32]byte{}, errors.New("canonical column/value length mismatch")
	}
	row := append([]byte{}, canonicalFrame([]byte(canonicalRowDomain))...)
	row = append(row, canonicalFrame([]byte(table))...)
	for i, column := range columns {
		encoded, err := canonicalEncodedValue(values[i])
		if err != nil {
			return [32]byte{}, err
		}
		row = append(row, canonicalFrame([]byte(column))...)
		row = append(row, encoded...)
	}
	return sha256.Sum256(row), nil
}

func (a canonicalAccumulator) tableDigest(table string, columns []string) [32]byte {
	input := append([]byte{}, canonicalFrame([]byte(canonicalTableDomain))...)
	input = append(input, canonicalFrame([]byte(table))...)
	joined := make([]byte, 0)
	for i, column := range columns {
		if i > 0 {
			joined = append(joined, 0)
		}
		joined = append(joined, column...)
	}
	input = append(input, canonicalFrame(joined)...)
	var number [8]byte
	binary.BigEndian.PutUint64(number[:], a.count)
	input = append(input, number[:]...)
	input = append(input, a.xor[:]...)
	for _, sum := range a.sums {
		binary.BigEndian.PutUint64(number[:], sum)
		input = append(input, number[:]...)
	}
	return sha256.Sum256(input)
}

// CanonicalEventAuditDigest streams the exact versioned logical event/audit
// contract without retaining row identifiers or payloads in evidence.
func CanonicalEventAuditDigest(ctx context.Context, db *sql.DB) (domain.CanonicalEventAuditEvidence, error) {
	hasCodec, err := tableHasColumn(ctx, db, "events", "body_codec")
	if err != nil {
		return domain.CanonicalEventAuditEvidence{}, err
	}
	events, err := canonicalEvents(ctx, db, hasCodec)
	if err != nil {
		return domain.CanonicalEventAuditEvidence{}, err
	}
	audits, err := canonicalAudits(ctx, db, hasCodec)
	if err != nil {
		return domain.CanonicalEventAuditEvidence{}, err
	}
	eventDigest := events.tableDigest("events", canonicalEventColumns)
	auditDigest := audits.tableDigest("command_audits", canonicalAuditColumns)
	store := append([]byte{}, canonicalFrame([]byte(canonicalStoreDomain))...)
	store = append(store, canonicalFrame(eventDigest[:])...)
	store = append(store, canonicalFrame(auditDigest[:])...)
	final := sha256.Sum256(store)
	return domain.CanonicalEventAuditEvidence{EventCount: events.count, AuditCount: audits.count, Digest: hex.EncodeToString(final[:])}, nil
}

func canonicalEvents(ctx context.Context, db *sql.DB, hasCodec bool) (canonicalAccumulator, error) {
	columns := append([]string{}, canonicalEventColumns...)
	if hasCodec {
		columns = append(columns, "body_codec", "body_format_version", "body_plaintext_bytes", "body_encoded_bytes", "body_sha256")
	}
	rows, err := db.QueryContext(ctx, `SELECT `+joinQuotedIdentifiers(columns)+` FROM events ORDER BY id COLLATE BINARY`)
	if err != nil {
		return canonicalAccumulator{}, fmt.Errorf("query canonical events: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var accumulator canonicalAccumulator
	for rows.Next() {
		values, scanErr := scanCanonicalValues(rows, len(columns))
		if scanErr != nil {
			return accumulator, scanErr
		}
		plain, decodeErr := canonicalPayload(values[4], values[len(canonicalEventColumns):])
		if decodeErr != nil {
			return accumulator, decodeErr
		}
		values[4] = canonicalLogicalValue(plain)
		digest, digestErr := canonicalRowDigest("events", canonicalEventColumns, values[:len(canonicalEventColumns)])
		if digestErr != nil {
			return accumulator, digestErr
		}
		accumulator.add(digest)
	}
	if err = rows.Err(); err != nil {
		return accumulator, fmt.Errorf("iterate canonical events: %w", err)
	}
	return accumulator, nil
}

func canonicalAudits(ctx context.Context, db *sql.DB, hasCodec bool) (canonicalAccumulator, error) {
	columns := append([]string{}, canonicalAuditColumns...)
	if hasCodec {
		for _, prefix := range []string{"command", "input", "output"} {
			columns = append(columns, prefix+"_codec", prefix+"_format_version", prefix+"_plaintext_bytes", prefix+"_encoded_bytes", prefix+"_sha256")
		}
	}
	rows, err := db.QueryContext(ctx, `SELECT `+joinQuotedIdentifiers(columns)+` FROM command_audits ORDER BY event_id COLLATE BINARY`)
	if err != nil {
		return canonicalAccumulator{}, fmt.Errorf("query canonical audits: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var accumulator canonicalAccumulator
	for rows.Next() {
		values, scanErr := scanCanonicalValues(rows, len(columns))
		if scanErr != nil {
			return accumulator, scanErr
		}
		for index := 0; index < 3; index++ {
			var metadata []any
			if hasCodec {
				metadataStart := len(canonicalAuditColumns) + index*5
				metadata = values[metadataStart : metadataStart+5]
			}
			plain, decodeErr := canonicalPayload(values[index+1], metadata)
			if decodeErr != nil {
				return accumulator, decodeErr
			}
			values[index+1] = canonicalLogicalValue(plain)
		}
		digest, digestErr := canonicalRowDigest("command_audits", canonicalAuditColumns, values[:len(canonicalAuditColumns)])
		if digestErr != nil {
			return accumulator, digestErr
		}
		accumulator.add(digest)
	}
	if err = rows.Err(); err != nil {
		return accumulator, fmt.Errorf("iterate canonical audits: %w", err)
	}
	return accumulator, nil
}

func scanCanonicalValues(rows *sql.Rows, count int) ([]any, error) {
	values := make([]any, count)
	destinations := make([]any, count)
	for i := range values {
		destinations[i] = &values[i]
	}
	if err := rows.Scan(destinations...); err != nil {
		return nil, fmt.Errorf("scan canonical row: %w", err)
	}
	return values, nil
}

func canonicalPayload(stored any, metadata []any) ([]byte, error) {
	var bytes []byte
	switch typed := stored.(type) {
	case string:
		bytes = []byte(typed)
	case []byte:
		bytes = typed
	default:
		return nil, fmt.Errorf("canonical payload has unexpected SQLite type %T", stored)
	}
	if int64(len(bytes)) > maxStoredPayloadBytes {
		return nil, errors.New("canonical stored payload exceeds limit")
	}
	row := payloadRow{Stored: bytes}
	if len(metadata) != 0 {
		if len(metadata) != 5 {
			return nil, errors.New("canonical codec metadata shape mismatch")
		}
		var ok bool
		if metadata[0] != nil {
			row.Codec.String, ok = metadata[0].(string)
			row.Codec.Valid = ok
		}
		if metadata[1] != nil {
			row.FormatVersion.Int64, ok = metadata[1].(int64)
			row.FormatVersion.Valid = ok
		}
		if metadata[2] != nil {
			row.PlaintextBytes.Int64, ok = metadata[2].(int64)
			row.PlaintextBytes.Valid = ok
		}
		if metadata[3] != nil {
			row.StoredBytes.Int64, ok = metadata[3].(int64)
			row.StoredBytes.Valid = ok
		}
		if metadata[4] != nil {
			row.SHA256.String, ok = metadata[4].(string)
			row.SHA256.Valid = ok
		}
	}
	plain, err := row.decode(maxDecodedPayloadBytes)
	if err != nil {
		return nil, err
	}
	return plain, nil
}

// canonicalLogicalValue keeps valid UTF-8 as text (tag 0x02) and hashes
// non-UTF-8 decoded payloads as blobs (tag 0x03). Operator stores can
// hold binary audit output; refusing those rows would block verified
// publication of an otherwise conserving candidate.
func canonicalLogicalValue(plain []byte) any {
	if utf8.Valid(plain) {
		return string(plain)
	}
	copied := make([]byte, len(plain))
	copy(copied, plain)
	return copied
}

func tableHasColumn(ctx context.Context, db *sql.DB, table, column string) (bool, error) {
	columns, err := tableColumns(ctx, db, table)
	if err != nil {
		return false, err
	}
	for _, candidate := range columns {
		if candidate == column {
			return true, nil
		}
	}
	return false, nil
}
