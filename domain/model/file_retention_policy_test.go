package model_test

import (
	"testing"
	"time"

	"github.com/duck8823/traceary/domain/model"
	"github.com/duck8823/traceary/domain/types"
)

func TestDecideFileRetentionSelectionVectors(t *testing.T) {
	t.Parallel()

	planTime := time.Date(2026, 1, 10, 0, 0, 0, 0, time.UTC)
	base := []model.FileRetentionEntry{
		fileRetentionEntry(t, "A", "a.arc", planTime.AddDate(0, 0, -9), 10, true, true, false, false, ""),
		fileRetentionEntry(t, "B", "b.arc", planTime.AddDate(0, 0, -8), 10, true, true, false, false, ""),
		fileRetentionEntry(t, "C", "c.arc", planTime.AddDate(0, 0, -7), 10, true, true, true, false, ""),
	}

	tests := []struct {
		name       string
		entries    []model.FileRetentionEntry
		budget     model.FileCapacityBudgetParams
		wantStatus string
		wantIDs    []string
		wantReason []string
	}{
		{name: "age", entries: base, budget: model.FileCapacityBudgetParams{MaxAge: types.Some(8 * 24 * time.Hour)}, wantStatus: "satisfied", wantIDs: []string{"A"}, wantReason: []string{"age"}},
		{name: "count", entries: base, budget: model.FileCapacityBudgetParams{MaxCount: types.Some(2)}, wantStatus: "satisfied", wantIDs: []string{"A"}, wantReason: []string{"count"}},
		{name: "allocated bytes", entries: base, budget: model.FileCapacityBudgetParams{MaxAllocatedBytes: types.Some[int64](20)}, wantStatus: "satisfied", wantIDs: []string{"A"}, wantReason: []string{"allocated_bytes"}},
		{name: "composite reasons", entries: base, budget: model.FileCapacityBudgetParams{MaxAge: types.Some(8 * 24 * time.Hour), MaxCount: types.Some(2), MaxAllocatedBytes: types.Some[int64](20)}, wantStatus: "satisfied", wantIDs: []string{"A"}, wantReason: []string{"age", "count", "allocated_bytes"}},
		{name: "unknown extent", entries: replaceFileRetentionEntry(t, base, 1, "B", "b.arc", planTime.AddDate(0, 0, -8), 0, false, true, false, false, ""), budget: model.FileCapacityBudgetParams{MaxAllocatedBytes: types.Some[int64](20)}, wantStatus: "indeterminate"},
		{name: "incomplete inventory", entries: replaceFileRetentionEntry(t, base, 1, "B", "b.arc", planTime.AddDate(0, 0, -8), 10, true, true, false, false, "unreadable"), budget: model.FileCapacityBudgetParams{MaxCount: types.Some(2)}, wantStatus: "indeterminate"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			budget, err := model.NewFileCapacityBudget(tt.budget)
			if err != nil {
				t.Fatalf("NewFileCapacityBudget() error = %v", err)
			}
			decision := model.DecideFileRetention(tt.entries, budget, planTime)
			if decision.Status() != tt.wantStatus {
				t.Fatalf("Status() = %q, want %q", decision.Status(), tt.wantStatus)
			}
			candidates := decision.Candidates()
			if len(candidates) != len(tt.wantIDs) {
				t.Fatalf("Candidates() length = %d, want %d", len(candidates), len(tt.wantIDs))
			}
			for index, candidate := range candidates {
				if candidate.Entry().Identity() != tt.wantIDs[index] {
					t.Fatalf("candidate[%d] = %q, want %q", index, candidate.Entry().Identity(), tt.wantIDs[index])
				}
				if got := candidate.Reasons(); !equalStrings(got, tt.wantReason) {
					t.Fatalf("candidate reasons = %v, want %v", got, tt.wantReason)
				}
			}
		})
	}
}

func TestDecideFileRetentionKnownIneligibleAndUnsatisfied(t *testing.T) {
	t.Parallel()

	planTime := time.Date(2026, 1, 10, 0, 0, 0, 0, time.UTC)
	entries := []model.FileRetentionEntry{
		fileRetentionEntry(t, "A", "a.arc", planTime.Add(-3*time.Hour), 10, true, true, false, true, ""),
		fileRetentionEntry(t, "B", "b.arc", planTime.Add(-2*time.Hour), 10, true, true, false, false, ""),
		fileRetentionEntry(t, "C", "c.arc", planTime.Add(-time.Hour), 10, true, true, true, false, ""),
	}
	budget, err := model.NewFileCapacityBudget(model.FileCapacityBudgetParams{MaxCount: types.Some(2)})
	if err != nil {
		t.Fatalf("NewFileCapacityBudget() error = %v", err)
	}
	decision := model.DecideFileRetention(entries, budget, planTime)
	if decision.Status() != "satisfied" || len(decision.Candidates()) != 1 || decision.Candidates()[0].Entry().Identity() != "B" {
		t.Fatalf("DecideFileRetention() = status %q candidates %v, want B", decision.Status(), decision.Candidates())
	}

	budget, err = model.NewFileCapacityBudget(model.FileCapacityBudgetParams{MaxCount: types.Some(1)})
	if err != nil {
		t.Fatalf("NewFileCapacityBudget() error = %v", err)
	}
	decision = model.DecideFileRetention(entries, budget, planTime)
	if decision.Status() != "unsatisfied" || len(decision.Candidates()) != 0 {
		t.Fatalf("DecideFileRetention() = status %q candidates %d, want unsatisfied with none", decision.Status(), len(decision.Candidates()))
	}
}

func fileRetentionEntry(t *testing.T, identity, path string, createdAt time.Time, allocated int64, known, verified, protected, pinned bool, blocker string) model.FileRetentionEntry {
	t.Helper()
	entry, err := model.NewFileRetentionEntry(model.FileRetentionEntryParams{
		Identity: identity, RelativePath: path, CreatedAt: createdAt, Generation: "generation", ContentDigest: "digest-" + identity,
		AllocatedBytes: allocated, AllocatedKnown: known, Verified: verified, Protected: protected, Pinned: pinned, BlockingReason: blocker,
	})
	if err != nil {
		t.Fatalf("NewFileRetentionEntry() error = %v", err)
	}
	return entry
}

func replaceFileRetentionEntry(t *testing.T, entries []model.FileRetentionEntry, index int, identity, path string, createdAt time.Time, allocated int64, known, verified, protected, pinned bool, blocker string) []model.FileRetentionEntry {
	t.Helper()
	result := append([]model.FileRetentionEntry(nil), entries...)
	result[index] = fileRetentionEntry(t, identity, path, createdAt, allocated, known, verified, protected, pinned, blocker)
	return result
}

func equalStrings(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}
