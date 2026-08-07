package sqlite

import (
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"

	apptypes "github.com/duck8823/traceary/application/types"
)

func dedupeGroupPlanFixture(kept string, duplicateIDs ...string) dedupeGroupPlan {
	group := strictDedupeGroupPlanFixture(kept, duplicateIDs...)
	group.atomic = true
	return group
}

func strictDedupeGroupPlanFixture(kept string, duplicateIDs ...string) dedupeGroupPlan {
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
		{
			name:      "a strict group is split at the batch size",
			groups:    []dedupeGroupPlan{strictDedupeGroupPlanFixture("k1", "d1", "d2", "d3", "d4", "d5")},
			batchSize: 2,
			want:      [][]string{{"d1", "d2"}, {"d3", "d4"}, {"d5"}},
		},
		{
			name: "a strict group packs alongside others without becoming atomic",
			groups: []dedupeGroupPlan{
				dedupeGroupPlanFixture("k1", "d1"),
				strictDedupeGroupPlanFixture("k2", "d2", "d3", "d4"),
			},
			batchSize: 2,
			want:      [][]string{{"d1", "d2"}, {"d3", "d4"}},
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

// The wiring that decides whether a batch boundary may fall inside a group:
// planContentEventDedupe marks proximity groups atomic and strict groups not.
// Without this, --strict --apply would put an entire identity group into one
// transaction — on the live store the largest is over 36,000 rows, which is the
// unresumable transaction batching exists to prevent.
func TestPlanContentEventDedupe_StrictGroupsAreSplittable(t *testing.T) {
	t.Parallel()

	base := time.Date(2026, 6, 20, 0, 0, 0, 0, time.UTC)
	key := newDedupeGroupKey("prompt", "hook", "kimi", "s1", "w1", "stop", "same body")
	members := make([]dedupeMemberRef, 0, 4)
	for i := range 4 {
		// 60s apart: far outside the proximity window, so the default mode splits
		// them into singletons and only strict mode groups them at all.
		at := base.Add(time.Duration(i) * time.Minute)
		members = append(members, dedupeMemberRef{
			id: "evt-" + string(rune('a'+i)), createdAt: at.Format(time.RFC3339Nano), parsedAt: at, parseOK: true,
		})
	}
	survey := dedupeSurvey{
		groups: map[dedupeGroupKey][]dedupeMemberRef{key: members},
		order:  []dedupeGroupKey{key},
	}

	strictPlan := planContentEventDedupe(survey, true)
	if len(strictPlan.groups) != 1 {
		t.Fatalf("strict groups = %d, want 1", len(strictPlan.groups))
	}
	if strictPlan.groups[0].atomic {
		t.Error("strict group is marked atomic; --batch-size would be ignored for it")
	}
	if got := batchedIDs(partitionDedupeTargets(strictPlan, 2)); len(got) != 2 {
		t.Errorf("strict batches = %v, want 2 transactions of 2 rows each", got)
	}

	// The same rows under the default window are far apart, so nothing is
	// eligible — which is exactly why strict exists and why it needs its own
	// batching rule rather than inheriting the proximity one.
	if groups := planContentEventDedupe(survey, false).groups; len(groups) != 0 {
		t.Errorf("proximity groups = %d, want 0 (members are 60s apart)", len(groups))
	}
}

// A proximity group must stay atomic, because its membership is decided from
// the gaps between surviving rows.
func TestPlanContentEventDedupe_ProximityGroupsAreAtomic(t *testing.T) {
	t.Parallel()

	base := time.Date(2026, 6, 20, 0, 0, 0, 0, time.UTC)
	key := newDedupeGroupKey("prompt", "hook", "kimi", "s1", "w1", "stop", "same body")
	members := []dedupeMemberRef{
		{id: "evt-a", createdAt: base.Format(time.RFC3339Nano), parsedAt: base, parseOK: true},
		{id: "evt-b", createdAt: base.Add(time.Second).Format(time.RFC3339Nano), parsedAt: base.Add(time.Second), parseOK: true},
	}
	plan := planContentEventDedupe(dedupeSurvey{
		groups: map[dedupeGroupKey][]dedupeMemberRef{key: members},
		order:  []dedupeGroupKey{key},
	}, false)

	if len(plan.groups) != 1 {
		t.Fatalf("groups = %d, want 1", len(plan.groups))
	}
	if !plan.groups[0].atomic {
		t.Error("proximity group is not marked atomic; a batch boundary could split it")
	}
}

// archived_at is RFC3339Nano, and its text form is not order-preserving: '.'
// sorts before 'Z', so a whole second sorts *after* a fractional one inside the
// same second. Letting SQLite order the strings would show an operator the wrong
// run as newest — the same lexical hazard tracked in #1185.
func TestSortDedupeRunsNewestFirst(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		runs []apptypes.ContentEventDedupeRun
		want []string
	}{
		{
			name: "a whole second is newer than a fractional one it follows",
			runs: []apptypes.ContentEventDedupeRun{
				{RunID: "fractional", ArchivedAt: "2026-06-20T00:00:00.5Z"},
				{RunID: "whole", ArchivedAt: "2026-06-20T00:00:01Z"},
			},
			want: []string{"whole", "fractional"},
		},
		{
			name: "offsets are compared as instants, not as text",
			runs: []apptypes.ContentEventDedupeRun{
				{RunID: "utc", ArchivedAt: "2026-06-20T00:00:00Z"},
				{RunID: "offset", ArchivedAt: "2026-06-20T09:00:01+09:00"},
			},
			want: []string{"offset", "utc"},
		},
		{
			name: "runs sharing an instant fall back to the run id",
			runs: []apptypes.ContentEventDedupeRun{
				{RunID: "run-a", ArchivedAt: "2026-06-20T00:00:00Z"},
				{RunID: "run-b", ArchivedAt: "2026-06-20T00:00:00Z"},
			},
			want: []string{"run-b", "run-a"},
		},
		{
			name: "an unparsable timestamp still lands somewhere deterministic",
			runs: []apptypes.ContentEventDedupeRun{
				{RunID: "broken", ArchivedAt: "not-a-timestamp"},
				{RunID: "valid", ArchivedAt: "2026-06-20T00:00:00Z"},
			},
			want: []string{"broken", "valid"},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			runs := append([]apptypes.ContentEventDedupeRun(nil), test.runs...)
			sortDedupeRunsNewestFirst(runs)
			got := make([]string, 0, len(runs))
			for _, run := range runs {
				got = append(got, run.RunID)
			}
			if diff := cmp.Diff(test.want, got); diff != "" {
				t.Errorf("sortDedupeRunsNewestFirst() (-want +got):\n%s", diff)
			}
		})
	}
}
