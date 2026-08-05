package domain_test

import (
	"errors"
	"testing"

	"github.com/duck8823/traceary/domain"
)

func TestCatalogTransitionDigestRejectsOverlap(t *testing.T) {
	one, _ := domain.ReservationTransition(domain.CatalogRange{Start: 1, End: 3}, "a")
	two, _ := domain.ReservationTransition(domain.CatalogRange{Start: 3, End: 4}, "b")
	_, err := domain.CanonicalCatalogTransitionDigest([]domain.CatalogTransition{one, two})
	if !errors.Is(err, domain.ErrCatalogRangeInvalid) {
		t.Fatalf("error = %v", err)
	}
}

func TestReleaseReservationReturnsHotWithoutAbandonedState(t *testing.T) {
	transition, err := domain.ReleaseReservationTransition(domain.CatalogRange{Start: 2, End: 5}, "target")
	if err != nil {
		t.Fatal(err)
	}
	if transition.From != domain.CatalogPlacementReserved || transition.To != domain.CatalogPlacementHot {
		t.Fatalf("transition = %+v", transition)
	}
}

func TestGenericCatalogTransitionRejectsProofBearingEdges(t *testing.T) {
	_, err := domain.NewCatalogTransition(domain.CatalogRange{Start: 1, End: 1}, domain.CatalogPlacementReserved, domain.CatalogPlacementSealed, "target", "segment")
	if !errors.Is(err, domain.ErrCatalogTransitionIllegal) {
		t.Fatalf("error = %v", err)
	}
}

func TestProofSpecificShadowTransitionsAuthenticateSegment(t *testing.T) {
	r := domain.CatalogRange{Start: 1, End: 2}
	sealed, err := domain.SealSegmentTransition(r, "reservation", "segment")
	if err != nil {
		t.Fatal(err)
	}
	shadow, err := domain.VerifyShadowTransition(r, "reservation", "segment")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = domain.CanonicalCatalogTransitionDigest([]domain.CatalogTransition{sealed}); err != nil {
		t.Fatal(err)
	}
	if _, err = domain.CanonicalCatalogTransitionDigest([]domain.CatalogTransition{shadow}); err != nil {
		t.Fatal(err)
	}
	if _, err = domain.RollbackSegmentTransition(r, domain.CatalogPlacementVerifiedShadow, "reservation", "segment"); err != nil {
		t.Fatal(err)
	}
}

func TestCatalogAuthorityRemainsHotThroughVerifiedShadow(t *testing.T) {
	for _, placement := range []domain.CatalogPlacement{domain.CatalogPlacementHot, domain.CatalogPlacementReserved, domain.CatalogPlacementSealed, domain.CatalogPlacementVerifiedShadow} {
		if placement.AuthorityOwner() != domain.CatalogAuthorityHot {
			t.Fatalf("%s became authority", placement)
		}
	}
	for _, placement := range []domain.CatalogPlacement{domain.CatalogPlacementSegmentAuthoritative, domain.CatalogPlacementEvicting, domain.CatalogPlacementCold} {
		if placement.AuthorityOwner() != domain.CatalogAuthoritySegment {
			t.Fatalf("%s did not own authority", placement)
		}
	}
}

func TestCatalogLedgerDigestBindsParent(t *testing.T) {
	zero := "0000000000000000000000000000000000000000000000000000000000000000"
	boundaryDigest, err := domain.CanonicalCatalogBoundaryDigest([]int64{1, 2})
	if err != nil {
		t.Fatal(err)
	}
	one, err := domain.CatalogLedgerDigest(zero, 1, 0, 1, 2, zero, boundaryDigest, zero)
	if err != nil {
		t.Fatal(err)
	}
	two, err := domain.CatalogLedgerDigest(one, 2, 1, 2, 2, zero, boundaryDigest, zero)
	if err != nil {
		t.Fatal(err)
	}
	if one == two {
		t.Fatal("ledger digest did not change")
	}
	otherHighWater, err := domain.CatalogLedgerDigest(zero, 1, 0, 2, 2, zero, boundaryDigest, zero)
	if err != nil {
		t.Fatal(err)
	}
	if otherHighWater == one {
		t.Fatal("source high-water was not digest-bound")
	}
}

func TestCatalogTransitionDigestV1CompatibilityAndV2OwnerBinding(t *testing.T) {
	r := domain.CatalogRange{Start: 1, End: 1}
	one, err := domain.VerifyShadowTransition(r, "reservation-a", "segment")
	if err != nil {
		t.Fatal(err)
	}
	two, err := domain.VerifyShadowTransition(r, "reservation-b", "segment")
	if err != nil {
		t.Fatal(err)
	}
	v1a, err := domain.CanonicalCatalogTransitionDigest([]domain.CatalogTransition{one})
	if err != nil {
		t.Fatal(err)
	}
	v1b, err := domain.CanonicalCatalogTransitionDigest([]domain.CatalogTransition{two})
	if err != nil || v1a != v1b {
		t.Fatalf("v1 changed: %q %q %v", v1a, v1b, err)
	}
	v2a, err := domain.CanonicalCatalogTransitionDigestV2([]domain.CatalogTransition{one})
	if err != nil {
		t.Fatal(err)
	}
	v2b, err := domain.CanonicalCatalogTransitionDigestV2([]domain.CatalogTransition{two})
	if err != nil || v2a == v2b {
		t.Fatalf("v2 owner not bound: %q %q %v", v2a, v2b, err)
	}
}
