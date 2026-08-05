package domain_test

import (
	"errors"
	"testing"
	"time"

	"github.com/duck8823/traceary/domain"
)

func targetPolicy() domain.SegmentTargetPolicy {
	return domain.SegmentTargetPolicy{CapturedAt: time.Date(2026, 8, 5, 12, 0, 0, 0, time.UTC), HotHorizon: 24 * time.Hour, MaxRows: 3, MaxCanonicalPlainBytes: 100, MaxDecodedBytes: 100, MaxStoredUpperBytes: 100, MaxFileBytes: 200, StoredBoundVersion: domain.SegmentStoredBoundV1}
}

func targetCandidate(sequence, bytes int64, createdAt time.Time) domain.SegmentTargetCandidate {
	return domain.SegmentTargetCandidate{Sequence: sequence, CreatedAt: createdAt, TimestampValid: true, CanonicalPlainBytes: bytes, DecodedBytes: bytes}
}

func TestSelectSegmentTargetUsesOnlyDeterministicPrefixInputs(t *testing.T) {
	policy := targetPolicy()
	old := policy.CapturedAt.Add(-48 * time.Hour)
	candidates := []domain.SegmentTargetCandidate{targetCandidate(7, 20, old), targetCandidate(8, 30, old), targetCandidate(9, 40, old), targetCandidate(10, 1, old)}
	first, err := domain.SelectSegmentTarget(candidates, 10, policy)
	if err != nil {
		t.Fatal(err)
	}
	second, err := domain.SelectSegmentTarget(candidates, 10, policy)
	if err != nil || second != first {
		t.Fatalf("repeat = %+v, %v; first=%+v", second, err, first)
	}
	if first.Range != (domain.CatalogRange{Start: 7, End: 9}) || first.Rows != 3 || first.CanonicalPlainBytes != 90 || first.StoredUpperBytes != 90 {
		t.Fatalf("selection = %+v", first)
	}
	const emptyDigest = "0000000000000000000000000000000000000000000000000000000000000000"
	digestA, err := domain.SegmentTargetPlanDigest("store", "reservation", 0, emptyDigest, emptyDigest, emptyDigest, 10, policy, first)
	if err != nil {
		t.Fatal(err)
	}
	digestB, _ := domain.SegmentTargetPlanDigest("store", "reservation", 0, emptyDigest, emptyDigest, emptyDigest, 10, policy, second)
	if digestA != digestB {
		t.Fatalf("digest changed: %s != %s", digestA, digestB)
	}
}

func TestSelectSegmentTargetHasTypedBoundaryOutcomes(t *testing.T) {
	policy := targetPolicy()
	old := policy.CapturedAt.Add(-48 * time.Hour)
	recent := policy.CapturedAt.Add(-time.Hour)
	tests := []struct {
		name       string
		candidates []domain.SegmentTargetCandidate
		wantErr    error
		wantEnd    int64
	}{
		{name: "recent first", candidates: []domain.SegmentTargetCandidate{targetCandidate(1, 1, recent)}, wantErr: domain.ErrSegmentTargetRecentFirst},
		{name: "oversize first", candidates: []domain.SegmentTargetCandidate{targetCandidate(1, 101, old)}, wantErr: domain.ErrSegmentTargetOversizeFirst},
		{name: "malformed first", candidates: []domain.SegmentTargetCandidate{{Sequence: 1, CanonicalPlainBytes: 1}}, wantErr: domain.ErrSegmentTargetMalformedTimestamp},
		{name: "later recent closes prefix", candidates: []domain.SegmentTargetCandidate{targetCandidate(1, 10, old), targetCandidate(2, 10, recent)}, wantEnd: 1},
		{name: "later oversize closes prefix", candidates: []domain.SegmentTargetCandidate{targetCandidate(1, 60, old), targetCandidate(2, 60, old)}, wantEnd: 1},
		{name: "later malformed fails closed", candidates: []domain.SegmentTargetCandidate{targetCandidate(1, 10, old), {Sequence: 2, CanonicalPlainBytes: 10}}, wantErr: domain.ErrSegmentTargetMalformedTimestamp},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			selection, err := domain.SelectSegmentTarget(test.candidates, 2, policy)
			if test.wantErr != nil {
				if !errors.Is(err, test.wantErr) {
					t.Fatalf("error = %v", err)
				}
				return
			}
			if err != nil || selection.Range.End != test.wantEnd {
				t.Fatalf("selection=%+v error=%v", selection, err)
			}
		})
	}
}

func TestSelectSegmentTargetRejectsSequenceGap(t *testing.T) {
	policy := targetPolicy()
	old := policy.CapturedAt.Add(-48 * time.Hour)
	_, err := domain.SelectSegmentTarget([]domain.SegmentTargetCandidate{targetCandidate(1, 1, old), targetCandidate(3, 1, old)}, 3, policy)
	if !errors.Is(err, domain.ErrSegmentTargetNotFound) {
		t.Fatalf("error = %v", err)
	}
}
