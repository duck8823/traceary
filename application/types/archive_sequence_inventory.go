package types

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"time"

	"github.com/duck8823/traceary/domain"
)

var (
	// ErrArchiveSequenceStaleGeneration identifies a failed generation/cursor CAS.
	ErrArchiveSequenceStaleGeneration = errors.New("archive sequence stale generation")
	// ErrArchiveSequenceDrift identifies a selected page or mapping that changed before apply.
	ErrArchiveSequenceDrift = errors.New("archive sequence inventory drift")
	// ErrArchiveSequenceGap identifies a non-contiguous or orphaned verified range.
	ErrArchiveSequenceGap = errors.New("archive sequence gap")
	// ErrArchiveSequenceOverflow identifies exhaustion of the signed SQLite sequence space.
	ErrArchiveSequenceOverflow = errors.New("archive sequence overflow")
	// ErrArchiveSequenceLimit identifies an invalid or exceeded bounded-operation cap.
	ErrArchiveSequenceLimit = errors.New("archive sequence limit exceeded")
	// ErrArchiveSequenceIncomplete identifies a phase or coverage that is not activation-ready.
	ErrArchiveSequenceIncomplete = errors.New("archive sequence inventory incomplete")
	// ErrArchiveSequenceActivation identifies a failed terminal activation proof.
	ErrArchiveSequenceActivation = errors.New("archive sequence activation failed")
)

const (
	// ArchiveSequenceMaxRows is the hard per-call row cap.
	ArchiveSequenceMaxRows = 10_000
	// ArchiveSequenceMaxStoredBytes is the hard per-call identity-read cap.
	ArchiveSequenceMaxStoredBytes = int64(64 << 20)
	// ArchiveSequenceMaxWriteBytes is the hard per-call logical-write cap.
	ArchiveSequenceMaxWriteBytes = int64(64 << 20)
	// ArchiveSequenceMaxWallTime is the hard per-call elapsed cap.
	ArchiveSequenceMaxWallTime = 10 * time.Minute
	// ArchiveSequenceMaxLockTime is the hard per-call write-lock cap.
	ArchiveSequenceMaxLockTime = 30 * time.Second
)

// ArchiveSequenceBudget bounds one select/apply/verify call. StoredBytes caps
// identities read, WriteBytes caps mapping bytes, WallTime caps total work,
// and LockTime caps the write transaction lock wait.
type ArchiveSequenceBudget struct {
	Rows        int
	StoredBytes int64
	WriteBytes  int64
	WallTime    time.Duration
	LockTime    time.Duration
}

// Valid reports whether every independent cap is positive.
func (b ArchiveSequenceBudget) Valid() bool {
	return b.Rows > 0 && b.Rows <= ArchiveSequenceMaxRows && b.StoredBytes > 0 && b.StoredBytes <= ArchiveSequenceMaxStoredBytes && b.WriteBytes > 0 && b.WriteBytes <= ArchiveSequenceMaxWriteBytes && b.WallTime > 0 && b.WallTime <= ArchiveSequenceMaxWallTime && b.LockTime > 0 && b.LockTime <= ArchiveSequenceMaxLockTime
}

// ConfigHash binds resume calls to the exact reviewed caps.
func (b ArchiveSequenceBudget) ConfigHash() string {
	h := sha256.New()
	_, _ = h.Write([]byte("traceary/archive-sequence-budget/v1"))
	var frame [8]byte
	for _, value := range []int64{int64(b.Rows), b.StoredBytes, b.WriteBytes, int64(b.WallTime), int64(b.LockTime)} {
		binary.BigEndian.PutUint64(frame[:], uint64(value))
		_, _ = h.Write(frame[:])
	}
	return hex.EncodeToString(h.Sum(nil))
}

// ArchiveSequenceInventoryItem is one selected historical event identity.
type ArchiveSequenceInventoryItem struct {
	EventID      string
	Missing      bool
	LogicalBytes int64
}

// ArchiveSequenceInventorySnapshot binds a selected page to its CAS cursor.
type ArchiveSequenceInventorySnapshot struct {
	Generation    domain.ArchiveInventoryGeneration
	ConfigHash    string
	Cursor        string
	CursorStarted bool
	Items         []ArchiveSequenceInventoryItem
	Done          bool
	PageDigest    string
}

// ArchiveSequenceProgress reports aggregate progress without event identities.
type ArchiveSequenceProgress struct {
	Generation domain.ArchiveInventoryGeneration
	Processed  int
	Assigned   int
	Done       bool
}

// ArchiveSequenceStatus is aggregate-only and never exposes the filter key.
type ArchiveSequenceStatus struct {
	StoreID            string
	Generation         domain.ArchiveInventoryGeneration
	ActiveGenerationID string
	VerifiedHighWater  int64
	MappedEvents       int64
	AllocatorNext      int64
}
