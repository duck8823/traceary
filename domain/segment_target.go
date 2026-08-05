package domain

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"time"
)

var (
	// ErrSegmentTargetRecentFirst means the first candidate must remain Hot.
	ErrSegmentTargetRecentFirst = errors.New("segment target starts inside hot horizon")
	// ErrSegmentTargetOversizeFirst means no non-empty target fits the policy.
	ErrSegmentTargetOversizeFirst = errors.New("segment target first unit exceeds cap")
	// ErrSegmentTargetMalformedTimestamp means the Hot horizon cannot be evaluated.
	ErrSegmentTargetMalformedTimestamp = errors.New("segment target contains malformed timestamp")
	// ErrSegmentTargetNotFound means no eligible contiguous Hot prefix exists.
	ErrSegmentTargetNotFound = errors.New("segment target not found")
)

const (
	// SegmentStoredBoundV1 identifies the immutable format-v1 conservative bound.
	SegmentStoredBoundV1 uint32 = 1
	// SegmentTargetMaxRows is the hard per-invocation hydration bound.
	SegmentTargetMaxRows = 100_000
	// SegmentTargetMaxBytes is the hard aggregate planning byte bound.
	SegmentTargetMaxBytes int64 = 4 << 30
)

// SegmentTargetPolicy fixes every value that can affect deterministic selection.
type SegmentTargetPolicy struct {
	CapturedAt             time.Time
	HotHorizon             time.Duration
	MaxRows                int
	MaxCanonicalPlainBytes int64
	MaxDecodedBytes        int64
	MaxStoredUpperBytes    int64
	MaxFileBytes           int64
	StoredBoundVersion     uint32
}

// SegmentTargetCandidate contains no body, only deterministic aggregate facts.
type SegmentTargetCandidate struct {
	Sequence            int64
	CreatedAt           time.Time
	TimestampValid      bool
	CanonicalPlainBytes int64
	DecodedBytes        int64
}

// SegmentTargetSelection is one non-empty contiguous closed prefix.
type SegmentTargetSelection struct {
	Range               CatalogRange
	Rows                int
	CanonicalPlainBytes int64
	DecodedBytes        int64
	StoredUpperBytes    int64
}

// Valid reports whether the policy completely fixes bounded v1 selection.
func (p SegmentTargetPolicy) Valid() bool {
	return !p.CapturedAt.IsZero() && p.HotHorizon > 0 &&
		p.MaxRows > 0 && p.MaxRows <= SegmentTargetMaxRows &&
		p.MaxCanonicalPlainBytes > 0 && p.MaxCanonicalPlainBytes <= SegmentTargetMaxBytes &&
		p.MaxDecodedBytes > 0 && p.MaxDecodedBytes <= SegmentTargetMaxBytes &&
		p.MaxStoredUpperBytes > 0 && p.MaxStoredUpperBytes <= SegmentTargetMaxBytes &&
		p.MaxFileBytes > 0 && p.MaxFileBytes <= SegmentTargetMaxBytes &&
		p.StoredBoundVersion == SegmentStoredBoundV1
}

// Complete reports whether the selection is closed by its deterministic row cap.
func (s SegmentTargetSelection) Complete(policy SegmentTargetPolicy) bool {
	return s.Rows >= policy.MaxRows
}

// Consider admits one candidate or deterministically closes/rejects the prefix.
// The boolean is false only for a non-first recent or oversize boundary.
func (s *SegmentTargetSelection) Consider(candidate SegmentTargetCandidate, policy SegmentTargetPolicy) (bool, error) {
	if s == nil || !policy.Valid() || candidate.Sequence <= 0 || candidate.CanonicalPlainBytes <= 0 || candidate.DecodedBytes < 0 ||
		(s.Rows > 0 && candidate.Sequence != s.Range.End+1) {
		return false, ErrSegmentTargetNotFound
	}
	if s.Complete(policy) {
		return false, nil
	}
	if !candidate.TimestampValid || candidate.CreatedAt.IsZero() {
		return false, ErrSegmentTargetMalformedTimestamp
	}
	if candidate.CreatedAt.After(policy.CapturedAt.Add(-policy.HotHorizon)) {
		if s.Rows == 0 {
			return false, ErrSegmentTargetRecentFirst
		}
		return false, nil
	}
	plain := s.CanonicalPlainBytes + candidate.CanonicalPlainBytes
	decoded := s.DecodedBytes + candidate.DecodedBytes
	stored := s.StoredUpperBytes + candidate.CanonicalPlainBytes
	if plain < s.CanonicalPlainBytes || decoded < s.DecodedBytes || stored < s.StoredUpperBytes || plain > policy.MaxCanonicalPlainBytes || decoded > policy.MaxDecodedBytes || stored > policy.MaxStoredUpperBytes {
		if s.Rows == 0 {
			return false, ErrSegmentTargetOversizeFirst
		}
		return false, nil
	}
	if s.Rows == 0 {
		s.Range.Start = candidate.Sequence
	}
	s.Range.End = candidate.Sequence
	s.Rows++
	s.CanonicalPlainBytes = plain
	s.DecodedBytes = decoded
	s.StoredUpperBytes = stored
	return true, nil
}

// SelectSegmentTarget chooses a prefix from a completely-read snapshot page.
// Format v1 compresses only when the encoded value is smaller, therefore its
// worst-case stored payload is the canonical plaintext length itself.
func SelectSegmentTarget(candidates []SegmentTargetCandidate, capturedHighWater int64, policy SegmentTargetPolicy) (SegmentTargetSelection, error) {
	if !policy.Valid() || capturedHighWater <= 0 {
		return SegmentTargetSelection{}, ErrSegmentTargetNotFound
	}
	if len(candidates) == 0 {
		return SegmentTargetSelection{}, ErrSegmentTargetNotFound
	}
	var result SegmentTargetSelection
	for index, candidate := range candidates {
		if candidate.Sequence > capturedHighWater || index >= policy.MaxRows {
			break
		}
		admitted, err := result.Consider(candidate, policy)
		if err != nil {
			return SegmentTargetSelection{}, err
		}
		if !admitted {
			break
		}
	}
	if result.Rows == 0 {
		return SegmentTargetSelection{}, ErrSegmentTargetNotFound
	}
	return result, nil
}

// SegmentTargetPlanDigest authenticates the complete selection policy and result.
func SegmentTargetPlanDigest(storeID, reservationID string, catalogEpoch int64, catalogLedgerDigest, catalogRangesDigest, sourceDigest string, capturedHighWater int64, policy SegmentTargetPolicy, selection SegmentTargetSelection) (string, error) {
	if storeID == "" || reservationID == "" || catalogEpoch < 0 || !ValidCatalogDigest(catalogLedgerDigest) || !ValidCatalogDigest(catalogRangesDigest) || !ValidCatalogDigest(sourceDigest) || !policy.Valid() || capturedHighWater <= 0 || selection.Rows <= 0 {
		return "", ErrSegmentTargetNotFound
	}
	h := sha256.New()
	_, _ = h.Write([]byte("traceary-segment-target-plan-v1\x00"))
	writeTargetFrame(h, []byte(storeID))
	writeTargetFrame(h, []byte(reservationID))
	writeTargetFrame(h, []byte(catalogLedgerDigest))
	writeTargetFrame(h, []byte(catalogRangesDigest))
	writeTargetFrame(h, []byte(sourceDigest))
	for _, value := range []int64{catalogEpoch, capturedHighWater, policy.CapturedAt.UnixNano(), int64(policy.HotHorizon), int64(policy.MaxRows), policy.MaxCanonicalPlainBytes, policy.MaxDecodedBytes, policy.MaxStoredUpperBytes, policy.MaxFileBytes, int64(policy.StoredBoundVersion), selection.Range.Start, selection.Range.End, int64(selection.Rows), selection.CanonicalPlainBytes, selection.DecodedBytes, selection.StoredUpperBytes} {
		var encoded [8]byte
		binary.BigEndian.PutUint64(encoded[:], uint64(value))
		_, _ = h.Write(encoded[:])
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// SegmentTargetRetryEvidenceDigest authenticates measured failure evidence.
func SegmentTargetRetryEvidenceDigest(previousPlanDigest, failureClass string, measuredBytes, failedCapBytes int64) (string, error) {
	if !ValidCatalogDigest(previousPlanDigest) || (failureClass != "stored_cap" && failureClass != "file_cap") || measuredBytes <= failedCapBytes || failedCapBytes <= 0 {
		return "", ErrSegmentTargetNotFound
	}
	h := sha256.New()
	_, _ = h.Write([]byte("traceary-segment-target-measured-failure-v1\x00"))
	writeTargetFrame(h, []byte(previousPlanDigest))
	writeTargetFrame(h, []byte(failureClass))
	for _, value := range []int64{measuredBytes, failedCapBytes} {
		var encoded [8]byte
		binary.BigEndian.PutUint64(encoded[:], uint64(value))
		_, _ = h.Write(encoded[:])
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

func writeTargetFrame(h interface{ Write([]byte) (int, error) }, value []byte) {
	var size [8]byte
	binary.BigEndian.PutUint64(size[:], uint64(len(value)))
	_, _ = h.Write(size[:])
	_, _ = h.Write(value)
}
