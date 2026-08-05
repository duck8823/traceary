package domain

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"math"
	"math/bits"
	"time"
)

// SegmentFormatV1 is the first immutable archive segment format.
const SegmentFormatV1 uint32 = 1

// SQLiteStorageClass preserves the class returned by SQLite.
type SQLiteStorageClass byte

const (
	// SQLiteNull identifies the NULL storage class.
	SQLiteNull SQLiteStorageClass = iota
	// SQLiteInteger identifies the INTEGER storage class.
	SQLiteInteger
	// SQLiteReal identifies the REAL storage class.
	SQLiteReal
	// SQLiteText identifies the TEXT storage class.
	SQLiteText
	// SQLiteBlob identifies the BLOB storage class.
	SQLiteBlob
)

// SQLiteValue is a canonical SQLite value. Bytes are copied on construction.
type SQLiteValue struct {
	Class SQLiteStorageClass
	Int   int64
	Real  float64
	Bytes []byte
}

// NullValue constructs a NULL value.
func NullValue() SQLiteValue { return SQLiteValue{Class: SQLiteNull} }

// IntegerValue constructs an INTEGER value.
func IntegerValue(v int64) SQLiteValue { return SQLiteValue{Class: SQLiteInteger, Int: v} }

// RealValue constructs a REAL value.
func RealValue(v float64) SQLiteValue { return SQLiteValue{Class: SQLiteReal, Real: v} }

// TextValue constructs a TEXT value without interpreting its bytes.
func TextValue(v []byte) SQLiteValue { return byteValue(SQLiteText, v) }

// BlobValue constructs a BLOB value.
func BlobValue(v []byte) SQLiteValue { return byteValue(SQLiteBlob, v) }
func byteValue(c SQLiteStorageClass, v []byte) SQLiteValue {
	return SQLiteValue{Class: c, Bytes: append([]byte(nil), v...)}
}

var archiveEventV1Columns = [...]string{"id", "kind", "agent", "session_id", "body", "created_at", "client", "workspace", "source_hook", "body_original_bytes", "body_stored_bytes", "body_ingest_truncated", "body_storage_truncated", "body_metadata_version", "body_availability", "body_pruned_at", "body_pruned_plan_id", "created_at_norm", "body_codec", "body_format_version", "body_plaintext_bytes", "body_encoded_bytes", "body_sha256"}
var archiveAuditV1Columns = [...]string{"command_text", "input_text", "output_text", "input_truncated", "output_truncated", "exit_code", "failed", "input_original_bytes", "output_original_bytes", "command_wrapper", "command_name", "failure_reason", "command_codec", "command_format_version", "command_plaintext_bytes", "command_encoded_bytes", "command_sha256", "input_codec", "input_format_version", "input_plaintext_bytes", "input_encoded_bytes", "input_sha256", "output_codec", "output_format_version", "output_plaintext_bytes", "output_encoded_bytes", "output_sha256"}

// ArchiveEventV1Columns returns the fixed events descriptor.
func ArchiveEventV1Columns() []string { return append([]string(nil), archiveEventV1Columns[:]...) }

// ArchiveAuditV1Columns returns the fixed command_audits descriptor excluding event_id.
func ArchiveAuditV1Columns() []string { return append([]string(nil), archiveAuditV1Columns[:]...) }

// ArchiveEventV1 fixes every format-v1 events column and its order.
type ArchiveEventV1 struct {
	values    [len(archiveEventV1Columns)]SQLiteValue
	eventID   []byte
	createdAt time.Time
}

// NewArchiveEventV1 constructs the fixed event row and derives identity/time from canonical columns.
func NewArchiveEventV1(values []SQLiteValue) (ArchiveEventV1, error) {
	if len(values) != len(archiveEventV1Columns) {
		return ArchiveEventV1{}, fmt.Errorf("archive event v1 requires %d columns", len(archiveEventV1Columns))
	}
	if values[0].Class != SQLiteText || len(values[0].Bytes) == 0 || values[5].Class != SQLiteText {
		return ArchiveEventV1{}, fmt.Errorf("archive event v1 id and created_at must be text")
	}
	created, err := time.Parse(time.RFC3339Nano, string(values[5].Bytes))
	if err != nil {
		return ArchiveEventV1{}, fmt.Errorf("parse archive event created_at: %w", err)
	}
	var event ArchiveEventV1
	copy(event.values[:], values)
	event.eventID = append([]byte(nil), values[0].Bytes...)
	event.createdAt = created.UTC()
	return event, nil
}

// Values returns a defensive copy in the fixed v1 order.
func (e ArchiveEventV1) Values() []SQLiteValue { return append([]SQLiteValue(nil), e.values[:]...) }

// ArchiveAuditV1 fixes every format-v1 command_audits column except event_id.
// The parent event_id is derived solely from HistoryUnit.Event.
type ArchiveAuditV1 struct {
	values [len(archiveAuditV1Columns)]SQLiteValue
}

// NewArchiveAuditV1 constructs the fixed audit row without a parent-id slot.
func NewArchiveAuditV1(values []SQLiteValue) (ArchiveAuditV1, error) {
	if len(values) != len(archiveAuditV1Columns) {
		return ArchiveAuditV1{}, fmt.Errorf("archive audit v1 requires %d columns", len(archiveAuditV1Columns))
	}
	var audit ArchiveAuditV1
	copy(audit.values[:], values)
	return audit, nil
}

// Values returns a defensive copy in the fixed v1 order.
func (a ArchiveAuditV1) Values() []SQLiteValue { return append([]SQLiteValue(nil), a.values[:]...) }

// HistoryUnit is the indivisible archive unit.
type HistoryUnit struct {
	Sequence uint64
	Event    ArchiveEventV1
	Audit    *ArchiveAuditV1
}

// CreatedAt returns the timestamp derived from the fixed event created_at column.
func (u HistoryUnit) CreatedAt() time.Time { return u.Event.createdAt }

// CanonicalBytes returns the versioned, length-delimited logical encoding.
func (u HistoryUnit) CanonicalBytes() ([]byte, error) {
	if u.Sequence == 0 || len(u.Event.eventID) == 0 || u.Event.createdAt.IsZero() {
		return nil, fmt.Errorf("history unit requires a positive sequence, event identity, and event")
	}
	b := make([]byte, 0, 128)
	b = append(b, 'T', 'R', 'H', 'U')
	b = binary.BigEndian.AppendUint32(b, SegmentFormatV1)
	b = binary.AppendUvarint(b, u.Sequence)
	b = binary.AppendUvarint(b, uint64(len(u.Event.eventID)))
	b = append(b, u.Event.eventID...)
	b = binary.AppendUvarint(b, uint64(len(archiveEventV1Columns)))
	var err error
	b, err = appendValues(b, u.Event.values[:])
	if err != nil {
		return nil, err
	}
	if u.Audit == nil {
		b = append(b, 0)
	} else {
		b = append(b, 1)
		b = binary.AppendUvarint(b, uint64(len(archiveAuditV1Columns)))
		b, err = appendValues(b, u.Audit.values[:])
		if err != nil {
			return nil, err
		}
	}
	return b, nil
}

// ValidateCanonicalSize rejects an encoding before allocating its duplicate
// canonical byte stream.
func (u HistoryUnit) ValidateCanonicalSize(maxBytes int64) error {
	if maxBytes <= 0 {
		return fmt.Errorf("canonical size cap must be positive")
	}
	if u.Sequence == 0 || len(u.Event.eventID) == 0 || u.Event.createdAt.IsZero() {
		return fmt.Errorf("invalid history unit")
	}
	size := int64(8 + uvarintSize(u.Sequence) + uvarintSize(uint64(len(u.Event.eventID))) + len(u.Event.eventID) + uvarintSize(uint64(len(archiveEventV1Columns))) + 1)
	var err error
	size, err = addValuesSize(size, u.Event.values[:], maxBytes)
	if err != nil {
		return err
	}
	if u.Audit != nil {
		size += int64(uvarintSize(uint64(len(archiveAuditV1Columns))))
		size, err = addValuesSize(size, u.Audit.values[:], maxBytes)
		if err != nil {
			return err
		}
	}
	if size > maxBytes {
		return fmt.Errorf("canonical history unit exceeds byte cap")
	}
	return nil
}

func addValuesSize(size int64, values []SQLiteValue, limit int64) (int64, error) {
	for _, v := range values {
		add := int64(1)
		switch v.Class {
		case SQLiteNull:
		case SQLiteInteger, SQLiteReal:
			add += 8
		case SQLiteText, SQLiteBlob:
			add += int64(uvarintSize(uint64(len(v.Bytes))) + len(v.Bytes))
		default:
			return 0, fmt.Errorf("unknown SQLite storage class %d", v.Class)
		}
		if add > limit-size {
			return 0, fmt.Errorf("canonical history unit exceeds byte cap")
		}
		size += add
	}
	return size, nil
}
func uvarintSize(value uint64) int { return (bits.Len64(value|1) + 6) / 7 }

func appendValues(dst []byte, values []SQLiteValue) ([]byte, error) {
	for _, v := range values {
		dst = append(dst, byte(v.Class))
		switch v.Class {
		case SQLiteNull:
		case SQLiteInteger:
			dst = binary.BigEndian.AppendUint64(dst, uint64(v.Int))
		case SQLiteReal:
			dst = binary.BigEndian.AppendUint64(dst, math.Float64bits(v.Real))
		case SQLiteText, SQLiteBlob:
			dst = binary.AppendUvarint(dst, uint64(len(v.Bytes)))
			dst = append(dst, v.Bytes...)
		default:
			return nil, fmt.Errorf("unknown SQLite storage class %d", v.Class)
		}
	}
	return dst, nil
}

// DecodeHistoryUnitCanonical decodes exactly one v1 History Unit.
func DecodeHistoryUnitCanonical(encoded []byte) (HistoryUnit, error) {
	if len(encoded) < 8 || !bytes.Equal(encoded[:4], []byte("TRHU")) || binary.BigEndian.Uint32(encoded[4:8]) != SegmentFormatV1 {
		return HistoryUnit{}, fmt.Errorf("unsupported history unit encoding")
	}
	r := bytes.NewReader(encoded[8:])
	sequence, err := binary.ReadUvarint(r)
	if err != nil || sequence == 0 {
		return HistoryUnit{}, fmt.Errorf("decode history unit sequence")
	}
	eventIDLength, err := binary.ReadUvarint(r)
	if err != nil || eventIDLength == 0 || eventIDLength > uint64(r.Len()) {
		return HistoryUnit{}, fmt.Errorf("decode history unit event identity")
	}
	eventID := make([]byte, eventIDLength)
	if _, err = r.Read(eventID); err != nil {
		return HistoryUnit{}, fmt.Errorf("decode history unit event identity bytes")
	}
	eventCount, err := binary.ReadUvarint(r)
	if err != nil || eventCount != uint64(len(archiveEventV1Columns)) {
		return HistoryUnit{}, fmt.Errorf("decode history unit event count")
	}
	event, err := readValues(r, eventCount)
	if err != nil {
		return HistoryUnit{}, err
	}
	eventRow, err := NewArchiveEventV1(event)
	if err != nil {
		return HistoryUnit{}, err
	}
	if !bytes.Equal(eventID, eventRow.eventID) {
		return HistoryUnit{}, fmt.Errorf("history unit parent identity mismatch")
	}
	present, err := r.ReadByte()
	if err != nil || present > 1 {
		return HistoryUnit{}, fmt.Errorf("decode history unit audit marker")
	}
	var audit *ArchiveAuditV1
	if present == 1 {
		count, readErr := binary.ReadUvarint(r)
		if readErr != nil {
			return HistoryUnit{}, fmt.Errorf("decode history unit audit count")
		}
		if count != uint64(len(archiveAuditV1Columns)) {
			return HistoryUnit{}, fmt.Errorf("decode history unit audit count")
		}
		auditValues, readValuesErr := readValues(r, count)
		err = readValuesErr
		if err != nil {
			return HistoryUnit{}, err
		}
		auditRow, auditErr := NewArchiveAuditV1(auditValues)
		if auditErr != nil {
			return HistoryUnit{}, auditErr
		}
		audit = &auditRow
	}
	if r.Len() != 0 {
		return HistoryUnit{}, fmt.Errorf("trailing history unit bytes")
	}
	return HistoryUnit{Sequence: sequence, Event: eventRow, Audit: audit}, nil
}

func readValues(r *bytes.Reader, count uint64) ([]SQLiteValue, error) {
	if count > uint64(r.Len()) {
		return nil, fmt.Errorf("impossible SQLite value count")
	}
	values := make([]SQLiteValue, 0, count)
	for range count {
		class, err := r.ReadByte()
		if err != nil {
			return nil, fmt.Errorf("decode SQLite storage class")
		}
		value := SQLiteValue{Class: SQLiteStorageClass(class)}
		switch value.Class {
		case SQLiteNull:
		case SQLiteInteger, SQLiteReal:
			var raw [8]byte
			if _, err = r.Read(raw[:]); err != nil {
				return nil, fmt.Errorf("decode numeric SQLite value")
			}
			bits := binary.BigEndian.Uint64(raw[:])
			if value.Class == SQLiteInteger {
				value.Int = int64(bits)
			} else {
				value.Real = math.Float64frombits(bits)
			}
		case SQLiteText, SQLiteBlob:
			length, readErr := binary.ReadUvarint(r)
			if readErr != nil || length > uint64(r.Len()) {
				return nil, fmt.Errorf("decode byte SQLite value length")
			}
			value.Bytes = make([]byte, length)
			if length > 0 {
				_, readErr = r.Read(value.Bytes)
			}
			if readErr != nil {
				return nil, fmt.Errorf("decode byte SQLite value")
			}
		default:
			return nil, fmt.Errorf("unknown SQLite storage class %d", class)
		}
		values = append(values, value)
	}
	return values, nil
}

// SegmentIdentity binds a logical segment to its lineage and closed range.
type SegmentIdentity struct {
	StoreID       string
	FormatVersion uint32
	StartSequence uint64
	EndSequence   uint64
	LogicalDigest [sha256.Size]byte
}

// NewSegmentIdentity validates and creates a format-v1 identity.
func NewSegmentIdentity(storeID string, start, end uint64, digest [sha256.Size]byte) (SegmentIdentity, error) {
	if storeID == "" || start == 0 || end < start {
		return SegmentIdentity{}, fmt.Errorf("invalid segment identity")
	}
	return SegmentIdentity{StoreID: storeID, FormatVersion: SegmentFormatV1, StartSequence: start, EndSequence: end, LogicalDigest: digest}, nil
}

// Basename is content-addressed and safe to join beneath an archive root.
func (i SegmentIdentity) Basename() string {
	h := sha256.New()
	h.Write([]byte("traceary-segment-identity-v1\x00"))
	h.Write([]byte(i.StoreID))
	var b [20]byte
	binary.BigEndian.PutUint32(b[:4], i.FormatVersion)
	binary.BigEndian.PutUint64(b[4:12], i.StartSequence)
	binary.BigEndian.PutUint64(b[12:20], i.EndSequence)
	h.Write(b[:])
	h.Write(i.LogicalDigest[:])
	return "segment-v1-" + hex.EncodeToString(h.Sum(nil)) + ".sqlite"
}
