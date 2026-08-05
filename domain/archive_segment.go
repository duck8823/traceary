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

// HistoryUnit is the indivisible archive unit. Field order is schema-defined.
type HistoryUnit struct {
	Sequence uint64
	// EventID is encoded once for both rows. Audit fields deliberately exclude
	// a second foreign-key value, so an audit cannot claim another parent.
	EventID   []byte
	CreatedAt time.Time
	Event     []SQLiteValue
	Audit     []SQLiteValue
}

// CanonicalBytes returns the versioned, length-delimited logical encoding.
func (u HistoryUnit) CanonicalBytes() ([]byte, error) {
	if u.Sequence == 0 || len(u.EventID) == 0 || u.CreatedAt.IsZero() || len(u.Event) == 0 {
		return nil, fmt.Errorf("history unit requires a positive sequence, event identity, and event")
	}
	b := make([]byte, 0, 128)
	b = append(b, 'T', 'R', 'H', 'U')
	b = binary.BigEndian.AppendUint32(b, SegmentFormatV1)
	b = binary.AppendUvarint(b, u.Sequence)
	b = binary.AppendUvarint(b, uint64(len(u.EventID)))
	b = append(b, u.EventID...)
	b = binary.BigEndian.AppendUint64(b, uint64(u.CreatedAt.UTC().Unix()))
	b = binary.BigEndian.AppendUint32(b, uint32(u.CreatedAt.UTC().Nanosecond()))
	b = binary.AppendUvarint(b, uint64(len(u.Event)))
	var err error
	b, err = appendValues(b, u.Event)
	if err != nil {
		return nil, err
	}
	if u.Audit == nil {
		b = append(b, 0)
	} else {
		b = append(b, 1)
		b = binary.AppendUvarint(b, uint64(len(u.Audit)))
		b, err = appendValues(b, u.Audit)
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
	if u.Sequence == 0 || len(u.EventID) == 0 || u.CreatedAt.IsZero() || len(u.Event) == 0 {
		return fmt.Errorf("invalid history unit")
	}
	size := int64(8 + uvarintSize(u.Sequence) + uvarintSize(uint64(len(u.EventID))) + len(u.EventID) + 12 + uvarintSize(uint64(len(u.Event))) + 1)
	var err error
	size, err = addValuesSize(size, u.Event, maxBytes)
	if err != nil {
		return err
	}
	if u.Audit != nil {
		size += int64(uvarintSize(uint64(len(u.Audit))))
		size, err = addValuesSize(size, u.Audit, maxBytes)
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
	var seconds [8]byte
	var nanos [4]byte
	if _, err = r.Read(seconds[:]); err != nil {
		return HistoryUnit{}, fmt.Errorf("decode history unit created_at seconds")
	}
	if _, err = r.Read(nanos[:]); err != nil {
		return HistoryUnit{}, fmt.Errorf("decode history unit created_at nanos")
	}
	createdAt := time.Unix(int64(binary.BigEndian.Uint64(seconds[:])), int64(binary.BigEndian.Uint32(nanos[:]))).UTC()
	eventCount, err := binary.ReadUvarint(r)
	if err != nil || eventCount == 0 {
		return HistoryUnit{}, fmt.Errorf("decode history unit event count")
	}
	event, err := readValues(r, eventCount)
	if err != nil {
		return HistoryUnit{}, err
	}
	present, err := r.ReadByte()
	if err != nil || present > 1 {
		return HistoryUnit{}, fmt.Errorf("decode history unit audit marker")
	}
	var audit []SQLiteValue
	if present == 1 {
		count, readErr := binary.ReadUvarint(r)
		if readErr != nil {
			return HistoryUnit{}, fmt.Errorf("decode history unit audit count")
		}
		audit, err = readValues(r, count)
		if err != nil {
			return HistoryUnit{}, err
		}
	}
	if r.Len() != 0 {
		return HistoryUnit{}, fmt.Errorf("trailing history unit bytes")
	}
	return HistoryUnit{Sequence: sequence, EventID: eventID, CreatedAt: createdAt, Event: event, Audit: audit}, nil
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
