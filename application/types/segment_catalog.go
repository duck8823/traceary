package types

import (
	"errors"
	"time"

	"github.com/duck8823/traceary/domain"
)

var (
	// ErrCatalogStaleHead identifies a failed expected-head compare-and-swap.
	ErrCatalogStaleHead = errors.New("segment catalog stale head")
	// ErrCatalogInventoryGate identifies an inactive or contradictory sequence inventory.
	ErrCatalogInventoryGate = errors.New("segment catalog inventory gate failed")
	// ErrCatalogOverlap identifies a transition over a non-Hot reserved range.
	ErrCatalogOverlap = errors.New("segment catalog range overlap")
	// ErrCatalogGap identifies a transition outside the proven source range.
	ErrCatalogGap = errors.New("segment catalog illegal gap")
	// ErrCatalogIllegalTransition identifies an unproven placement state edge.
	ErrCatalogIllegalTransition = errors.New("segment catalog illegal transition")
	// ErrCatalogLineageMismatch identifies metadata for another logical store.
	ErrCatalogLineageMismatch = errors.New("segment catalog lineage mismatch")
	// ErrCatalogBindingMismatch identifies contradictory immutable segment metadata.
	ErrCatalogBindingMismatch = errors.New("segment catalog binding mismatch")
	// ErrCatalogDrift identifies ledger, chain, or derived-cache corruption.
	ErrCatalogDrift = errors.New("segment catalog ledger or cache drift")
	// ErrCatalogNotFound identifies a missing active reservation.
	ErrCatalogNotFound = errors.New("segment catalog reservation not found")
	// ErrCatalogReservationConflict identifies reuse of an immutable reservation ID.
	ErrCatalogReservationConflict = errors.New("segment catalog reservation conflict")
	// ErrCatalogLimit identifies an invalid or exceeded operation bound.
	ErrCatalogLimit = errors.New("segment catalog operation limit exceeded")
	// ErrSegmentTargetSelectionIncomplete means the bounded snapshot was not fully read.
	ErrSegmentTargetSelectionIncomplete = errors.New("segment target selection incomplete")
	// ErrSegmentTargetRecentFirst means the smallest unplaced unit must remain Hot.
	ErrSegmentTargetRecentFirst = domain.ErrSegmentTargetRecentFirst
	// ErrSegmentTargetOversizeFirst means no non-empty prefix fits the configured caps.
	ErrSegmentTargetOversizeFirst = domain.ErrSegmentTargetOversizeFirst
	// ErrSegmentTargetMalformedTimestamp means deterministic horizon evaluation is impossible.
	ErrSegmentTargetMalformedTimestamp = domain.ErrSegmentTargetMalformedTimestamp
	// ErrSegmentTargetNotFound means no Hot sequence exists at the captured high-water.
	ErrSegmentTargetNotFound = domain.ErrSegmentTargetNotFound
	// ErrSegmentTargetRetryProof rejects an unmeasured or non-shortening retry.
	ErrSegmentTargetRetryProof = errors.New("segment target retry proof invalid")
	// ErrCatalogAuditIncomplete means a durable bounded audit must be resumed.
	ErrCatalogAuditIncomplete = errors.New("segment catalog audit incomplete")
)

const (
	// CatalogMaxRanges is the hard per-call replay range/epoch cap.
	CatalogMaxRanges = 10_000
	// CatalogMaxTransitionsPerEpoch bounds one atomic epoch independently of age.
	CatalogMaxTransitionsPerEpoch = 1_000
	// CatalogMaxBoundaryPoints bounds internal change-point validation independently of returned ranges.
	CatalogMaxBoundaryPoints = 100_000
	// CatalogMaxWallTime is the hard per-call elapsed cap.
	CatalogMaxWallTime = 2 * time.Minute
	// CatalogMaxLockTime is the hard per-call lock-wait cap.
	CatalogMaxLockTime = 30 * time.Second
)

// CatalogBudget bounds one ledger operation.
type CatalogBudget struct {
	Ranges   int
	WallTime time.Duration
	LockTime time.Duration
}

// Valid reports whether each independent cap is positive and bounded.
func (b CatalogBudget) Valid() bool {
	return b.Ranges > 0 && b.Ranges <= CatalogMaxRanges && b.WallTime > 0 && b.WallTime <= CatalogMaxWallTime && b.LockTime > 0 && b.LockTime <= CatalogMaxLockTime
}

// CatalogHead is the expected-head CAS token and derived-cache digest.
type CatalogHead struct {
	Epoch               int64
	LedgerDigest        string
	CurrentRangesDigest string
}

// CatalogInventoryGate is aggregate-only readiness for target planning.
type CatalogInventoryGate struct {
	StoreID   string
	HighWater int64
	Head      CatalogHead
}

// CatalogReservation requests one proof-free Hot-to-reserved transition.
type CatalogReservation struct {
	ExpectedHead   CatalogHead
	ReservationID  string
	Range          domain.CatalogRange
	EvidenceDigest string
	Budget         CatalogBudget
}

// CatalogRelease requests release of one exact active reservation.
type CatalogRelease struct {
	ExpectedHead   CatalogHead
	ReservationID  string
	EvidenceDigest string
	Budget         CatalogBudget
}

const (
	// SegmentStoredBoundV1 is the immutable bound used by segment format v1.
	SegmentStoredBoundV1 uint32 = domain.SegmentStoredBoundV1
	// SegmentTargetFailureStoredCap permits a measured stored-byte retry.
	SegmentTargetFailureStoredCap = "stored_cap"
	// SegmentTargetFailureFileCap permits a measured segment-file retry.
	SegmentTargetFailureFileCap = "file_cap"
)

// SegmentTargetPolicy fixes every input that can affect deterministic prefix selection.
type SegmentTargetPolicy = domain.SegmentTargetPolicy

// CatalogTargetRetry proves why #1651 is asking for a strictly shorter target.
// Empty PreviousReservationID means this is an initial selection.
type CatalogTargetRetry struct {
	PreviousReservationID string
	PreviousRange         domain.CatalogRange
	FailureClass          string
	MeasuredBytes         int64
	FailedCapBytes        int64
	EvidenceDigest        string
}

// CatalogTargetPlanRequest selects and reserves against one expected snapshot.
type CatalogTargetPlanRequest struct {
	ExpectedHead      CatalogHead
	ReservationID     string
	CapturedHighWater int64
	Policy            SegmentTargetPolicy
	Retry             CatalogTargetRetry
	Budget            CatalogBudget
}

// SegmentTargetCandidate is one fully hydrated immutable planning input.
type SegmentTargetCandidate = domain.SegmentTargetCandidate

// CatalogTargetPlan is the reserved deterministic target and aggregate-only evidence.
type CatalogTargetPlan struct {
	Head                      CatalogHead
	ReservationID             string
	Range                     domain.CatalogRange
	Rows                      int
	CanonicalPlainBytes       int64
	DecodedBytes              int64
	StoredUpperBytes          int64
	CapturedHighWater         int64
	PlanDigest                string
	ReservationEvidenceDigest string
}

// CatalogCurrentRange is one non-overlapping derived placement interval.
type CatalogCurrentRange struct {
	Range         domain.CatalogRange
	Placement     domain.CatalogPlacement
	ReservationID string
	SegmentID     string
	SourceEpoch   int64
}

// CatalogSnapshot is an epoch-pinned bounded source set.
type CatalogSnapshot struct {
	Head   CatalogHead
	Ranges []CatalogCurrentRange
}

// CatalogRebuildResult reports aggregate cache rebuild evidence.
type CatalogRebuildResult struct {
	Head    CatalogHead
	Ranges  int
	Changed bool
}

// CatalogAuditCursor is a resumable hash-chain checkpoint.
type CatalogAuditCursor struct {
	Epoch        int64
	LedgerDigest string
}

// InitialCatalogAuditCursor returns the canonical genesis checkpoint.
func InitialCatalogAuditCursor() CatalogAuditCursor {
	return CatalogAuditCursor{LedgerDigest: "0000000000000000000000000000000000000000000000000000000000000000"}
}

// CatalogAuditPage is aggregate-only bounded full-ledger audit evidence.
type CatalogAuditPage struct {
	Next     CatalogAuditCursor
	Verified int
	Done     bool
}
