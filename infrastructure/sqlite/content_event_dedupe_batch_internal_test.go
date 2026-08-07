package sqlite

import (
	"testing"

	"github.com/google/go-cmp/cmp"

	apptypes "github.com/duck8823/traceary/application/types"
)

func dedupeGroupPlanFixture(kept string, duplicateIDs ...string) dedupeGroupPlan {
	duplicates := make([]dedupeMemberRef, 0, len(duplicateIDs))
	for _, id := range duplicateIDs {
		duplicates = append(duplicates, dedupeMemberRef{id: id})
	}
	return dedupeGroupPlan{keptID: kept, forensicKey: "group-" + kept, reason: "duplicate", duplicates: duplicates}
}

func batchedIDs(batches [][]dedupeArchiveTarget) [][]string {
	out := make([][]string, 0, len(batches))
	for _, batch := range batches {
		ids := make([]string, 0, len(batch))
		for _, target := range batch {
			ids = append(ids, target.id)
		}
		out = append(out, ids)
	}
	return out
}

// Apply commits one partition per transaction, so a partition boundary that fell
// inside a cluster would let an interruption leave that cluster half-repaired.
// Proximity clustering measures gaps between surviving rows, so a half-repaired
// cluster can split into pieces a re-run will never collapse — the repair would
// silently stop short of what a clean run produces. Every case here pins that a
// cluster stays whole.
func TestPartitionDedupeTargets(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		groups    []dedupeGroupPlan
		batchSize int
		want      [][]string
	}{
		{
			name:      "whole clusters are packed until the batch size is reached",
			groups:    []dedupeGroupPlan{dedupeGroupPlanFixture("k1", "d1", "d2"), dedupeGroupPlanFixture("k2", "d3", "d4")},
			batchSize: 4,
			want:      [][]string{{"d1", "d2", "d3", "d4"}},
		},
		{
			name:      "a cluster that would overflow the batch starts a new one",
			groups:    []dedupeGroupPlan{dedupeGroupPlanFixture("k1", "d1", "d2"), dedupeGroupPlanFixture("k2", "d3", "d4")},
			batchSize: 3,
			want:      [][]string{{"d1", "d2"}, {"d3", "d4"}},
		},
		{
			name:      "a cluster larger than the batch size is never split",
			groups:    []dedupeGroupPlan{dedupeGroupPlanFixture("k1", "d1", "d2", "d3")},
			batchSize: 1,
			want:      [][]string{{"d1", "d2", "d3"}},
		},
		{
			name:      "an oversized cluster does not drag the next cluster along",
			groups:    []dedupeGroupPlan{dedupeGroupPlanFixture("k1", "d1", "d2", "d3"), dedupeGroupPlanFixture("k2", "d4")},
			batchSize: 2,
			want:      [][]string{{"d1", "d2", "d3"}, {"d4"}},
		},
		{
			name:      "groups without duplicates contribute nothing",
			groups:    []dedupeGroupPlan{dedupeGroupPlanFixture("k1"), dedupeGroupPlanFixture("k2", "d1")},
			batchSize: 10,
			want:      [][]string{{"d1"}},
		},
		{
			name:      "an empty plan yields no transactions",
			groups:    nil,
			batchSize: 10,
			want:      [][]string{},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			got := batchedIDs(partitionDedupeTargets(dedupePlan{groups: test.groups}, test.batchSize))
			if diff := cmp.Diff(test.want, got); diff != "" {
				t.Errorf("partitionDedupeTargets() (-want +got):\n%s", diff)
			}
		})
	}
}

// Zero means "use the default", not "one transaction per row" and not "no
// batching at all".
func TestPartitionDedupeTargets_ZeroBatchSizeUsesDefault(t *testing.T) {
	t.Parallel()

	groups := make([]dedupeGroupPlan, 0, apptypes.DefaultContentEventDedupeBatchSize+1)
	for i := range apptypes.DefaultContentEventDedupeBatchSize + 1 {
		groups = append(groups, dedupeGroupPlanFixture(
			"k"+string(rune('a'+i%26)),
			"d"+string(rune('a'+i%26))+string(rune('a'+i/26)),
		))
	}

	batches := partitionDedupeTargets(dedupePlan{groups: groups}, 0)
	if len(batches) != 2 {
		t.Fatalf("batches = %d, want 2 (the default batch size splits one row past it)", len(batches))
	}
	if len(batches[0]) != apptypes.DefaultContentEventDedupeBatchSize {
		t.Errorf("first batch = %d rows, want %d", len(batches[0]), apptypes.DefaultContentEventDedupeBatchSize)
	}
	if len(batches[1]) != 1 {
		t.Errorf("second batch = %d rows, want 1", len(batches[1]))
	}
}
