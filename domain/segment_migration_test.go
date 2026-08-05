package domain_test

import (
	"errors"
	"testing"

	"github.com/duck8823/traceary/domain"
)

func TestSegmentMigrationRunOwnsOnlyShadowProtocol(t *testing.T) {
	r := domain.SegmentMigrationRun{ID: "run", StoreID: "0123456789abcdef0123456789abcdef", ReservationID: "reservation", PlanDigest: "0000000000000000000000000000000000000000000000000000000000000000", Range: domain.CatalogRange{Start: 1, End: 2}, Phase: domain.SegmentMigrationPlanned, Revision: 1, NextSequence: 1}
	var err error
	r, err = r.Advance(domain.SegmentMigrationCopying)
	if err != nil {
		t.Fatal(err)
	}
	r, err = r.CheckpointPage(domain.SegmentMigrationPageProof{NextSequence: 3, Rows: 2, PlainBytes: 10})
	if err != nil {
		t.Fatal(err)
	}
	proof := domain.SegmentMigrationCandidateProof{SourceDigest: r.PlanDigest, Basename: "segment", ManifestDigest: r.PlanDigest, FileDigest: r.PlanDigest}
	r, err = r.RecordCandidateBuilt(proof)
	if err != nil {
		t.Fatal(err)
	}
	for _, phase := range []domain.SegmentMigrationPhase{domain.SegmentMigrationInstallIntent, domain.SegmentMigrationInstalled, domain.SegmentMigrationSealIntent} {
		r, err = r.Advance(phase)
		if err != nil {
			t.Fatal(err)
		}
	}
	r, err = r.RecordCatalogPhase(domain.SegmentMigrationSealed, 1)
	if err != nil {
		t.Fatal(err)
	}
	r, err = r.Advance(domain.SegmentMigrationVerifyIntent)
	if err != nil {
		t.Fatal(err)
	}
	r, err = r.RecordCatalogPhase(domain.SegmentMigrationVerifiedShadow, 2)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = r.Advance(domain.SegmentMigrationInstalled); !errors.Is(err, domain.ErrSegmentMigrationTransition) {
		t.Fatalf("backward transition: %v", err)
	}
	if r.Phase != domain.SegmentMigrationVerifiedShadow {
		t.Fatal("failed transition mutated run")
	}
}

func TestSegmentMigrationRollbackIsForwardOnly(t *testing.T) {
	r := domain.SegmentMigrationRun{ID: "run", StoreID: "0123456789abcdef0123456789abcdef", ReservationID: "reservation", PlanDigest: "0000000000000000000000000000000000000000000000000000000000000000", Range: domain.CatalogRange{Start: 1, End: 1}, Phase: domain.SegmentMigrationSealed, Revision: 7, NextSequence: 2, CopiedRows: 1, SourceDigest: "0000000000000000000000000000000000000000000000000000000000000000", CandidateBasename: "segment", SegmentID: "segment", ManifestDigest: "0000000000000000000000000000000000000000000000000000000000000000", FileDigest: "0000000000000000000000000000000000000000000000000000000000000000", CatalogEpoch: 1}
	r, err := r.Advance(domain.SegmentMigrationRollbackIntent)
	if err != nil {
		t.Fatal(err)
	}
	r, err = r.Advance(domain.SegmentMigrationRolledBack)
	if err != nil {
		t.Fatal(err)
	}
	if r.Phase != domain.SegmentMigrationRolledBack {
		t.Fatal(r.Phase)
	}
}

func TestSegmentMigrationRevisionRejectsRegressionAndEvidenceMutation(t *testing.T) {
	base := domain.SegmentMigrationRun{ID: "run", StoreID: "0123456789abcdef0123456789abcdef", ReservationID: "reservation", PlanDigest: "0000000000000000000000000000000000000000000000000000000000000000", Range: domain.CatalogRange{Start: 1, End: 2}, Phase: domain.SegmentMigrationCopying, Revision: 2, NextSequence: 1}
	next := base
	next.Revision++
	next.NextSequence = 2
	next.CopiedRows = 1
	next.CopiedPlainBytes = 10
	if err := base.ValidateRevision(next); err != nil {
		t.Fatal(err)
	}
	mutated := next
	mutated.Revision++
	mutated.NextSequence = 3
	mutated.CopiedRows = 2
	mutated.PlanDigest = "1111111111111111111111111111111111111111111111111111111111111111"
	if !errors.Is(next.ValidateRevision(mutated), domain.ErrSegmentMigrationTransition) {
		t.Fatal("immutable proof mutation accepted")
	}
	regressed := next
	regressed.Revision++
	regressed.NextSequence = 1
	regressed.CopiedRows = 0
	if !errors.Is(next.ValidateRevision(regressed), domain.ErrSegmentMigrationTransition) {
		t.Fatal("checkpoint regression accepted")
	}
}
