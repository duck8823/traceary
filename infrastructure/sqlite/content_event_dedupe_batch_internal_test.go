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
			// The hazard itself: within one second, descending text order puts
			// the whole second first because '.' (0x2E) precedes 'Z' (0x5A),
			// even though the fractional one is later. formatTimestamp emits
			// RFC3339Nano, which drops the fraction entirely when it is zero, so
			// both forms really do occur in the same column.
			name: "a fractional second is newer than the whole second it follows",
			runs: []apptypes.ContentEventDedupeRun{
				{RunID: "whole", ArchivedAt: "2026-06-20T00:00:01Z"},
				{RunID: "fractional", ArchivedAt: "2026-06-20T00:00:01.5Z"},
			},
			want: []string{"fractional", "whole"},
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
			// An unparsable timestamp has no position on the timeline, so it sinks
			// below every run that does rather than displacing one.
			name: "an unparsable timestamp sinks below every parsable run",
			runs: []apptypes.ContentEventDedupeRun{
				{RunID: "broken", ArchivedAt: "not-a-timestamp"},
				{RunID: "valid", ArchivedAt: "2026-06-20T00:00:00Z"},
			},
			want: []string{"valid", "broken"},
		},
		{
			// The transitivity counterexample. Comparing parsable pairs by instant
			// and any pair involving an unparsable one by text is not a strict weak
			// ordering: B < A by instant, A < C because 'Z' > '.', C < B because
			// '9' > '5'. sort does not detect the cycle, it just returns an
			// arbitrary order.
			name: "a mix of parsable and unparsable timestamps has no ordering cycle",
			runs: []apptypes.ContentEventDedupeRun{
				{RunID: "A", ArchivedAt: "2026-06-20T00:00:01Z"},
				{RunID: "B", ArchivedAt: "2026-06-20T00:00:01.5Z"},
				{RunID: "C", ArchivedAt: "2026-06-20T00:00:01.9"},
			},
			want: []string{"B", "A", "C"},
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

// The ordering must not depend on the order the rows arrive in. A comparator
// that violates transitivity passes a two-element test and still produces
// different answers for different input permutations of the same three runs.
func TestSortDedupeRunsNewestFirst_IndependentOfInputOrder(t *testing.T) {
	t.Parallel()

	runs := []apptypes.ContentEventDedupeRun{
		{RunID: "A", ArchivedAt: "2026-06-20T00:00:01Z"},
		{RunID: "B", ArchivedAt: "2026-06-20T00:00:01.5Z"},
		{RunID: "C", ArchivedAt: "2026-06-20T00:00:01.9"},
	}
	permutations := [][]int{{0, 1, 2}, {0, 2, 1}, {1, 0, 2}, {1, 2, 0}, {2, 0, 1}, {2, 1, 0}}
	want := []string{"B", "A", "C"}
	for _, permutation := range permutations {
		permuted := make([]apptypes.ContentEventDedupeRun, 0, len(runs))
		for _, index := range permutation {
			permuted = append(permuted, runs[index])
		}
		sortDedupeRunsNewestFirst(permuted)
		got := make([]string, 0, len(permuted))
		for _, run := range permuted {
			got = append(got, run.RunID)
		}
		if diff := cmp.Diff(want, got); diff != "" {
			t.Errorf("input order %v produced a different result (-want +got):\n%s", permutation, diff)
		}
	}
}

// A retention-ledger row cannot be archived, but removing it from its identity
// group would widen the proximity gap across it. Here three rows sit 9s apart
// inside the 10s window; the middle one is ledger-held. Hiding it makes the
// outer two 18s apart -- two singletons, nothing collapsed -- so an ordinary
// duplicate pair with nothing to do with retention would be stranded forever.
func TestPlanContentEventDedupe_RetentionHeldRowKeepsItsClusterIntact(t *testing.T) {
	t.Parallel()

	base := time.Date(2026, 6, 20, 0, 0, 0, 0, time.UTC)
	key := newDedupeGroupKey("prompt", "hook", "kimi", "s1", "w1", "stop", "same body")
	member := func(id string, offset time.Duration, held bool) dedupeMemberRef {
		at := base.Add(offset)
		return dedupeMemberRef{
			id: id, createdAt: at.Format(time.RFC3339Nano), parsedAt: at,
			parseOK: true, retentionHeld: held,
		}
	}
	members := []dedupeMemberRef{
		member("evt-a", 0, false),
		member("evt-b", 9*time.Second, true),
		member("evt-c", 18*time.Second, false),
	}
	plan := planContentEventDedupe(dedupeSurvey{
		groups: map[dedupeGroupKey][]dedupeMemberRef{key: members},
		order:  []dedupeGroupKey{key},
	}, false)

	if len(plan.groups) != 1 {
		t.Fatalf("groups = %d, want 1 (the ledger row must still hold the cluster together)", len(plan.groups))
	}
	if got := plan.groups[0].keptID; got != "evt-a" {
		t.Errorf("keptID = %q, want evt-a", got)
	}
	got := make([]string, 0, len(plan.groups[0].duplicates))
	for _, dup := range plan.groups[0].duplicates {
		got = append(got, dup.id)
	}
	if diff := cmp.Diff([]string{"evt-c"}, got); diff != "" {
		t.Errorf("duplicates (-want +got):\n%s", diff)
	}
}

// When every non-canonical member is ledger-held there is nothing to archive,
// and the group must not reach the plan as an empty entry.
func TestPlanContentEventDedupe_AllDuplicatesRetentionHeldYieldsNoGroup(t *testing.T) {
	t.Parallel()

	base := time.Date(2026, 6, 20, 0, 0, 0, 0, time.UTC)
	key := newDedupeGroupKey("prompt", "hook", "kimi", "s1", "w1", "stop", "same body")
	members := []dedupeMemberRef{
		{id: "evt-a", createdAt: base.Format(time.RFC3339Nano), parsedAt: base, parseOK: true},
		{
			id: "evt-b", createdAt: base.Add(time.Second).Format(time.RFC3339Nano),
			parsedAt: base.Add(time.Second), parseOK: true, retentionHeld: true,
		},
	}
	plan := planContentEventDedupe(dedupeSurvey{
		groups: map[dedupeGroupKey][]dedupeMemberRef{key: members},
		order:  []dedupeGroupKey{key},
	}, false)

	if len(plan.groups) != 0 {
		t.Errorf("groups = %d, want 0 (nothing is archivable)", len(plan.groups))
	}
}

func TestPlanContentEventDedupe_AttestedDuplicateIsNotArchived(t *testing.T) {
	t.Parallel()

	base := time.Date(2026, 6, 20, 0, 0, 0, 0, time.UTC)
	key := newDedupeGroupKey("prompt", "hook", "codex", "s1", "w1", "user_prompt_submit", "same body")
	members := []dedupeMemberRef{
		{id: "evt-a", createdAt: base.Format(time.RFC3339Nano), parsedAt: base, parseOK: true, attested: true},
		{
			id: "evt-b", createdAt: base.Add(time.Second).Format(time.RFC3339Nano),
			parsedAt: base.Add(time.Second), parseOK: true, attested: true,
		},
	}
	plan := planContentEventDedupe(dedupeSurvey{
		groups: map[dedupeGroupKey][]dedupeMemberRef{key: members},
		order:  []dedupeGroupKey{key},
	}, false)

	if len(plan.groups) != 0 {
		t.Errorf("groups = %d, want 0 (attested hook duplicates stay)", len(plan.groups))
	}
}

func TestPlanContentEventDedupe_AuditHeldDuplicateIsNotArchived(t *testing.T) {
	t.Parallel()

	base := time.Date(2026, 6, 20, 0, 0, 0, 0, time.UTC)
	key := newDedupeGroupKey("prompt", "hook", "codex", "s1", "w1", "user_prompt_submit", "same body")
	members := []dedupeMemberRef{
		{id: "evt-a", createdAt: base.Format(time.RFC3339Nano), parsedAt: base, parseOK: true},
		{
			id: "evt-b", createdAt: base.Add(time.Second).Format(time.RFC3339Nano),
			parsedAt: base.Add(time.Second), parseOK: true, auditHeld: true,
		},
	}
	plan := planContentEventDedupe(dedupeSurvey{
		groups: map[dedupeGroupKey][]dedupeMemberRef{key: members},
		order:  []dedupeGroupKey{key},
	}, false)

	if len(plan.groups) != 0 {
		t.Errorf("groups = %d, want 0 (audit-held hook duplicates stay)", len(plan.groups))
	}
}

func TestPlanContentEventDedupe_AuditHeldRowKeepsItsClusterIntact(t *testing.T) {
	t.Parallel()

	base := time.Date(2026, 6, 20, 0, 0, 0, 0, time.UTC)
	key := newDedupeGroupKey("prompt", "hook", "codex", "s1", "w1", "stop", "same body")
	member := func(id string, offset time.Duration, auditHeld bool) dedupeMemberRef {
		at := base.Add(offset)
		return dedupeMemberRef{
			id: id, createdAt: at.Format(time.RFC3339Nano), parsedAt: at,
			parseOK: true, auditHeld: auditHeld,
		}
	}
	plan := planContentEventDedupe(dedupeSurvey{
		groups: map[dedupeGroupKey][]dedupeMemberRef{
			key: {member("evt-a", 0, false), member("evt-b", 9*time.Second, true), member("evt-c", 18*time.Second, false)},
		},
		order: []dedupeGroupKey{key},
	}, false)

	if len(plan.groups) != 1 {
		t.Fatalf("groups = %d, want 1 (the audit-held row must still hold the cluster together)", len(plan.groups))
	}
	if got := plan.groups[0].keptID; got != "evt-a" {
		t.Errorf("keptID = %q, want evt-a", got)
	}
	got := make([]string, 0, len(plan.groups[0].duplicates))
	for _, dup := range plan.groups[0].duplicates {
		got = append(got, dup.id)
	}
	if diff := cmp.Diff([]string{"evt-c"}, got); diff != "" {
		t.Errorf("duplicates (-want +got):\n%s", diff)
	}
}
