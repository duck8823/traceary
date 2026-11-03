package runtime

import (
	"errors"
	"math"
	"strconv"
)

// Output bounds apply to bytes, never interpreted terminal text.
const (
	MaxOutputBytes       = 2 * 1024 * 1024
	MaxOutputChunk       = 32 * 1024
	MaxOutputSubscribers = 4
	MaxOutputPending     = 256 * 1024
)

// ErrOutputCursor rejects offsets beyond captured output without guessing.
var ErrOutputCursor = errors.New("invalid runtime output cursor")

// ByteOffset is an exact monotonic byte counter, encoded as decimal on the wire.
type ByteOffset uint64

// ParseByteOffset rejects lossy or noncanonical wire counters.
func ParseByteOffset(s string) (ByteOffset, error) {
	if len(s) == 0 || len(s) > 20 || len(s) > 1 && s[0] == '0' {
		return 0, ErrOutputCursor
	}
	for _, c := range s {
		if c < '0' || c > '9' {
			return 0, ErrOutputCursor
		}
	}
	n, err := strconv.ParseUint(s, 10, 64)
	if err != nil {
		return 0, ErrOutputCursor
	}
	return ByteOffset(n), nil
}
func (b ByteOffset) String() string { return strconv.FormatUint(uint64(b), 10) }

// Advance checks overflow before changing an offset.
func (b ByteOffset) Advance(n uint64) (ByteOffset, error) {
	if n > math.MaxUint64-uint64(b) {
		return 0, ErrLimit
	}
	return b + ByteOffset(n), nil
}

// ByteSpan is either exact retained bytes or an exact evicted range.
type ByteSpan struct {
	Start, End ByteOffset
	Data       []byte
	Gap        bool
}

// ByteLogState contains no terminal content.
type ByteLogState struct {
	Base, Head ByteOffset
	Retained   int
}

// ByteLog is an unsynchronized, lazily allocated byte ring. Its owner serializes access.
type ByteLog struct {
	limit      int
	data       []byte
	base, head ByteOffset
}

// NewByteLog constructs a ring with a fixed maximum retention size.
func NewByteLog(limit int) (*ByteLog, error) {
	if limit < 1 || limit > MaxOutputBytes {
		return nil, ErrLimit
	}
	return &ByteLog{limit: limit}, nil
}

// Snapshot returns bounded metadata without exposing retained bytes.
func (b *ByteLog) Snapshot() ByteLogState { return ByteLogState{b.base, b.head, int(b.head - b.base)} }

// Append copies bytes, retaining only the newest bounded suffix.
func (b *ByteLog) Append(p []byte) (ByteSpan, error) {
	if len(p) > MaxOutputChunk {
		return ByteSpan{}, ErrLimit
	}
	next, err := b.head.Advance(uint64(len(p)))
	if err != nil {
		return ByteSpan{}, err
	}
	start := b.head
	if len(p) > 0 {
		// Growing storage retains absolute-offset modulo indexing when capacity changes.
		required := min(b.limit, int(b.head-b.base)+len(p))
		size := len(b.data)
		if required > size {
			size = min(b.limit, max(required, max(1024, size*2)))
		}
		if size > len(b.data) {
			grown := make([]byte, size)
			for pos := b.base; pos < b.head; pos++ {
				grown[uint64(pos)%uint64(size)] = b.data[uint64(pos)%uint64(len(b.data))]
			}
			b.data = grown
		}
		keep := p[max(0, len(p)-b.limit):]
		begin := next - ByteOffset(len(keep))
		for i, v := range keep {
			b.data[(uint64(begin)+uint64(i))%uint64(len(b.data))] = v
		}
	}
	b.head = next
	if uint64(next) > uint64(b.limit) {
		b.base = next - ByteOffset(b.limit)
	}
	return ByteSpan{Start: start, End: next}, nil
}

// Read returns a defensive chunk; eviction is a separate frame before retained data.
func (b *ByteLog) Read(cursor ByteOffset, limit int) (ByteSpan, error) {
	if cursor > b.head {
		return ByteSpan{}, ErrOutputCursor
	}
	if limit < 1 || limit > MaxOutputChunk {
		return ByteSpan{}, ErrLimit
	}
	if cursor < b.base {
		return ByteSpan{Start: cursor, End: b.base, Gap: true}, nil
	}
	size := min(uint64(limit), uint64(b.head-cursor))
	p := make([]byte, int(size))
	for i := range p {
		p[i] = b.data[(uint64(cursor)+uint64(i))%uint64(len(b.data))]
	}
	return ByteSpan{Start: cursor, End: cursor + ByteOffset(size), Data: p}, nil
}
