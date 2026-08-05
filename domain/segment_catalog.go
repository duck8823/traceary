package domain

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"strings"
)

var (
	// ErrCatalogRangeInvalid identifies an invalid or overlapping closed range.
	ErrCatalogRangeInvalid = errors.New("catalog range is invalid")
	// ErrCatalogDigestInvalid identifies a malformed or contradictory digest chain.
	ErrCatalogDigestInvalid = errors.New("catalog digest is invalid")
	// ErrCatalogTransitionIllegal identifies an unsupported placement edge.
	ErrCatalogTransitionIllegal = errors.New("catalog transition is illegal")
	// ErrCatalogBindingInvalid identifies malformed immutable segment metadata.
	ErrCatalogBindingInvalid = errors.New("catalog segment binding is invalid")
)

// CatalogPlacement is the authority placement of a closed sequence range.
type CatalogPlacement string

// CatalogAuthority identifies the source that remains authoritative.
type CatalogAuthority string

const (
	// CatalogAuthorityHot keeps reads on the Hot store.
	CatalogAuthorityHot CatalogAuthority = "hot"
	// CatalogAuthoritySegment reads the immutable segment.
	CatalogAuthoritySegment CatalogAuthority = "segment"
)

// AuthorityOwner prevents migration phases from being mistaken for cutover.
func (p CatalogPlacement) AuthorityOwner() CatalogAuthority {
	switch p {
	case CatalogPlacementSegmentAuthoritative, CatalogPlacementEvicting, CatalogPlacementCold:
		return CatalogAuthoritySegment
	default:
		return CatalogAuthorityHot
	}
}

const (
	// CatalogPlacementHot means the canonical unit is authoritative in Hot.
	CatalogPlacementHot CatalogPlacement = "hot"
	// CatalogPlacementReserved means a target owns the range without changing authority.
	CatalogPlacementReserved CatalogPlacement = "reserved"
	// CatalogPlacementSealed means a proof-bearing immutable candidate exists.
	CatalogPlacementSealed CatalogPlacement = "sealed"
	// CatalogPlacementVerifiedShadow means the segment passed shadow verification.
	CatalogPlacementVerifiedShadow CatalogPlacement = "verified_shadow"
	// CatalogPlacementSegmentAuthoritative means the segment is the read authority.
	CatalogPlacementSegmentAuthoritative CatalogPlacement = "segment_authoritative"
	// CatalogPlacementEvicting means proven Hot duplicates are being reclaimed.
	CatalogPlacementEvicting CatalogPlacement = "evicting"
	// CatalogPlacementCold means physical reclaim completed.
	CatalogPlacementCold CatalogPlacement = "cold"
)

// Valid reports whether placement is part of the versioned state vocabulary.
func (p CatalogPlacement) Valid() bool {
	switch p {
	case CatalogPlacementHot, CatalogPlacementReserved, CatalogPlacementSealed,
		CatalogPlacementVerifiedShadow, CatalogPlacementSegmentAuthoritative,
		CatalogPlacementEvicting, CatalogPlacementCold:
		return true
	default:
		return false
	}
}

// CatalogRange is a closed positive archive-sequence range.
type CatalogRange struct {
	Start int64
	End   int64
}

// NewCatalogRange validates and constructs a closed positive interval.
func NewCatalogRange(start, end int64) (CatalogRange, error) {
	if start <= 0 || end < start {
		return CatalogRange{}, ErrCatalogRangeInvalid
	}
	return CatalogRange{Start: start, End: end}, nil
}

// Overlaps reports whether two closed intervals share a sequence.
func (r CatalogRange) Overlaps(other CatalogRange) bool {
	return r.Start <= other.End && other.Start <= r.End
}

// CatalogTransition is one canonical append-only range delta.
type CatalogTransition struct {
	Range         CatalogRange
	From          CatalogPlacement
	To            CatalogPlacement
	ReservationID string
	SegmentID     string
}

// NewCatalogTransition validates one canonical range delta.
func NewCatalogTransition(r CatalogRange, from, to CatalogPlacement, reservationID, segmentID string) (CatalogTransition, error) {
	if _, err := NewCatalogRange(r.Start, r.End); err != nil {
		return CatalogTransition{}, err
	}
	if !from.Valid() || !to.Valid() || from == to {
		return CatalogTransition{}, ErrCatalogTransitionIllegal
	}
	reservationID = strings.TrimSpace(reservationID)
	segmentID = strings.TrimSpace(segmentID)
	if (from == CatalogPlacementReserved || to == CatalogPlacementReserved) != (reservationID != "") {
		return CatalogTransition{}, ErrCatalogTransitionIllegal
	}
	allowedReservationEdge := (from == CatalogPlacementHot && to == CatalogPlacementReserved) || (from == CatalogPlacementReserved && to == CatalogPlacementHot)
	if !allowedReservationEdge || segmentID != "" {
		return CatalogTransition{}, ErrCatalogTransitionIllegal
	}
	return CatalogTransition{Range: r, From: from, To: to, ReservationID: reservationID, SegmentID: segmentID}, nil
}

func newProofCatalogTransition(r CatalogRange, from, to CatalogPlacement, reservationID, segmentID string) (CatalogTransition, error) {
	if _, err := NewCatalogRange(r.Start, r.End); err != nil || strings.TrimSpace(segmentID) == "" {
		return CatalogTransition{}, ErrCatalogTransitionIllegal
	}
	allowed := (from == CatalogPlacementReserved && to == CatalogPlacementSealed) ||
		(from == CatalogPlacementSealed && to == CatalogPlacementVerifiedShadow)
	if !allowed {
		return CatalogTransition{}, ErrCatalogTransitionIllegal
	}
	if from == CatalogPlacementReserved && strings.TrimSpace(reservationID) == "" {
		return CatalogTransition{}, ErrCatalogTransitionIllegal
	}
	if from == CatalogPlacementSealed {
		reservationID = ""
	}
	return CatalogTransition{Range: r, From: from, To: to, ReservationID: strings.TrimSpace(reservationID), SegmentID: strings.TrimSpace(segmentID)}, nil
}

// SealSegmentTransition is the proof-specific Reserved-to-Sealed edge.
func SealSegmentTransition(r CatalogRange, reservationID, segmentID string) (CatalogTransition, error) {
	return newProofCatalogTransition(r, CatalogPlacementReserved, CatalogPlacementSealed, reservationID, segmentID)
}

// VerifyShadowTransition is the proof-specific Sealed-to-VerifiedShadow edge.
func VerifyShadowTransition(r CatalogRange, reservationID, segmentID string) (CatalogTransition, error) {
	return newProofCatalogTransition(r, CatalogPlacementSealed, CatalogPlacementVerifiedShadow, reservationID, segmentID)
}

// RollbackSegmentTransition retains immutable binding while restoring Reserved.
func RollbackSegmentTransition(r CatalogRange, from CatalogPlacement, reservationID string) (CatalogTransition, error) {
	if _, err := NewCatalogRange(r.Start, r.End); err != nil || strings.TrimSpace(reservationID) == "" || (from != CatalogPlacementSealed && from != CatalogPlacementVerifiedShadow) {
		return CatalogTransition{}, ErrCatalogTransitionIllegal
	}
	return CatalogTransition{Range: r, From: from, To: CatalogPlacementReserved, ReservationID: strings.TrimSpace(reservationID)}, nil
}

func validateCatalogTransition(transition CatalogTransition) (CatalogTransition, error) {
	if value, err := NewCatalogTransition(transition.Range, transition.From, transition.To, transition.ReservationID, transition.SegmentID); err == nil {
		return value, nil
	}
	if transition.To == CatalogPlacementReserved {
		return RollbackSegmentTransition(transition.Range, transition.From, transition.ReservationID)
	}
	return newProofCatalogTransition(transition.Range, transition.From, transition.To, transition.ReservationID, transition.SegmentID)
}

// ReservationTransition permits only the proof-free #1661 state edge.
func ReservationTransition(r CatalogRange, reservationID string) (CatalogTransition, error) {
	return NewCatalogTransition(r, CatalogPlacementHot, CatalogPlacementReserved, reservationID, "")
}

// ReleaseReservationTransition returns a reservation to Hot. It deliberately
// does not introduce an abandoned placement state.
func ReleaseReservationTransition(r CatalogRange, reservationID string) (CatalogTransition, error) {
	return NewCatalogTransition(r, CatalogPlacementReserved, CatalogPlacementHot, reservationID, "")
}

// CanonicalCatalogTransitionDigest deterministically binds an ordered epoch.
func CanonicalCatalogTransitionDigest(transitions []CatalogTransition) (string, error) {
	h := sha256.New()
	writeCatalogFrame(h, []byte("traceary/catalog-transitions/v1"))
	previousEnd := int64(0)
	for i, transition := range transitions {
		validated, err := validateCatalogTransition(transition)
		if err != nil {
			return "", err
		}
		if i > 0 && validated.Range.Start <= previousEnd {
			return "", ErrCatalogRangeInvalid
		}
		previousEnd = validated.Range.End
		var number [8]byte
		binary.BigEndian.PutUint64(number[:], uint64(validated.Range.Start))
		writeCatalogFrame(h, number[:])
		binary.BigEndian.PutUint64(number[:], uint64(validated.Range.End))
		writeCatalogFrame(h, number[:])
		writeCatalogFrame(h, []byte(validated.From))
		writeCatalogFrame(h, []byte(validated.To))
		writeCatalogFrame(h, []byte(validated.ReservationID))
		writeCatalogFrame(h, []byte(validated.SegmentID))
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

func writeCatalogFrame(h interface{ Write([]byte) (int, error) }, value []byte) {
	var size [8]byte
	binary.BigEndian.PutUint64(size[:], uint64(len(value)))
	_, _ = h.Write(size[:])
	_, _ = h.Write(value)
}

// ValidCatalogDigest accepts only lower-case SHA-256 hex.
func ValidCatalogDigest(value string) bool {
	if len(value) != sha256.Size*2 || strings.ToLower(value) != value {
		return false
	}
	_, err := hex.DecodeString(value)
	return err == nil
}

// CatalogLedgerDigest chains an epoch to its exact parent and evidence.
func CatalogLedgerDigest(parentDigest string, epoch, parentEpoch, sourceHighWater, boundaryCount int64, transitionDigest, boundaryDigest, evidenceDigest string) (string, error) {
	if epoch <= 0 || parentEpoch != epoch-1 || sourceHighWater <= 0 || boundaryCount < 1 || !ValidCatalogDigest(parentDigest) || !ValidCatalogDigest(transitionDigest) || !ValidCatalogDigest(boundaryDigest) || !ValidCatalogDigest(evidenceDigest) {
		return "", ErrCatalogDigestInvalid
	}
	h := sha256.New()
	writeCatalogFrame(h, []byte("traceary/catalog-ledger/v1"))
	writeCatalogFrame(h, []byte(parentDigest))
	var number [8]byte
	binary.BigEndian.PutUint64(number[:], uint64(epoch))
	writeCatalogFrame(h, number[:])
	binary.BigEndian.PutUint64(number[:], uint64(parentEpoch))
	writeCatalogFrame(h, number[:])
	binary.BigEndian.PutUint64(number[:], uint64(sourceHighWater))
	writeCatalogFrame(h, number[:])
	writeCatalogFrame(h, []byte(transitionDigest))
	binary.BigEndian.PutUint64(number[:], uint64(boundaryCount))
	writeCatalogFrame(h, number[:])
	writeCatalogFrame(h, []byte(boundaryDigest))
	writeCatalogFrame(h, []byte(evidenceDigest))
	return hex.EncodeToString(h.Sum(nil)), nil
}

// CanonicalCatalogBoundaryDigest binds a sorted unique positive boundary set.
func CanonicalCatalogBoundaryDigest(boundaries []int64) (string, error) {
	h := sha256.New()
	writeCatalogFrame(h, []byte("traceary/catalog-boundaries/v1"))
	previous := int64(0)
	for _, boundary := range boundaries {
		if boundary <= previous {
			return "", ErrCatalogRangeInvalid
		}
		var number [8]byte
		binary.BigEndian.PutUint64(number[:], uint64(boundary))
		writeCatalogFrame(h, number[:])
		previous = boundary
	}
	if len(boundaries) < 1 {
		return "", ErrCatalogRangeInvalid
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// CatalogSegmentBinding contains fixed immutable, non-authority metadata.
type CatalogSegmentBinding struct {
	identity                                  SegmentIdentity
	fileDigest, manifestDigest, summaryDigest [sha256.Size]byte
}

// NewCatalogSegmentBinding constructs fixed format-v1 metadata from the
// canonical #1649 identity and validated SHA-256 values.
func NewCatalogSegmentBinding(identity SegmentIdentity, fileDigest, manifestDigest, summaryDigest [sha256.Size]byte) (CatalogSegmentBinding, error) {
	if identity.FormatVersion != SegmentFormatV1 || identity.StoreID == "" || identity.StartSequence == 0 || identity.EndSequence < identity.StartSequence {
		return CatalogSegmentBinding{}, ErrCatalogBindingInvalid
	}
	return CatalogSegmentBinding{identity: identity, fileDigest: fileDigest, manifestDigest: manifestDigest, summaryDigest: summaryDigest}, nil
}

// Validate checks the fixed identity lineage.
func (b CatalogSegmentBinding) Validate(expectedStoreID string) error {
	if b.identity.StoreID != expectedStoreID || len(expectedStoreID) != 32 || b.identity.FormatVersion != SegmentFormatV1 || b.identity.Basename() == "" {
		return ErrCatalogBindingInvalid
	}
	return nil
}

// SegmentID returns the content-addressed identity.
func (b CatalogSegmentBinding) SegmentID() string { return b.identity.Basename() }

// StoreID returns the bound lineage.
func (b CatalogSegmentBinding) StoreID() string { return b.identity.StoreID }

// Range returns the bound closed archive-sequence range.
func (b CatalogSegmentBinding) Range() CatalogRange {
	return CatalogRange{Start: int64(b.identity.StartSequence), End: int64(b.identity.EndSequence)}
}

// FormatVersion returns the fixed segment format.
func (b CatalogSegmentBinding) FormatVersion() int { return int(SegmentFormatV1) }

// ManifestVersion returns the fixed manifest schema version.
func (b CatalogSegmentBinding) ManifestVersion() int { return 1 }

// SummaryVersion returns the fixed Catalog summary version.
func (b CatalogSegmentBinding) SummaryVersion() int { return int(SegmentSummaryV1) }

// LogicalDigest returns the canonical logical SHA-256.
func (b CatalogSegmentBinding) LogicalDigest() string {
	return hex.EncodeToString(b.identity.LogicalDigest[:])
}

// FileDigest returns the sealed file SHA-256.
func (b CatalogSegmentBinding) FileDigest() string { return hex.EncodeToString(b.fileDigest[:]) }

// ManifestDigest returns the manifest SHA-256.
func (b CatalogSegmentBinding) ManifestDigest() string {
	return hex.EncodeToString(b.manifestDigest[:])
}

// SummaryDigest returns the immutable summary SHA-256.
func (b CatalogSegmentBinding) SummaryDigest() string { return hex.EncodeToString(b.summaryDigest[:]) }

// RelativeBasename returns the recomputed content-addressed basename.
func (b CatalogSegmentBinding) RelativeBasename() string { return b.identity.Basename() }

// StorageClass returns the fixed format-v1 storage class.
func (b CatalogSegmentBinding) StorageClass() string { return "sealed_sqlite_zstd_v1" }
