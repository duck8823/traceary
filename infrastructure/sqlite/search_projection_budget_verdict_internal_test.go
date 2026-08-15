package sqlite

import (
	"context"
	"strings"
	"testing"
	"time"
)

func TestSearchProjectionStatusBudgetVerdictUsesFamilyTotalWhenPersistedUnknown(t *testing.T) {
	store, db := newCapacityTestStore(t, []struct{ id, body, created string }{
		{"e1", strings.Repeat("budget verdict body ", 80), "2026-06-01T12:00:00Z"},
	})
	now := time.Date(2026, 6, 2, 0, 0, 0, 0, time.UTC)
	if _, err := store.Start(context.Background(), capacityBudget(64<<20), now); err != nil {
		t.Fatal(err)
	}
	var persisted int
	if err := db.QueryRow(`SELECT index_family_within_budget FROM search_projection_state`).Scan(&persisted); err != nil {
		t.Fatal(err)
	}
	if persisted != -1 {
		t.Fatalf("persisted index_family_within_budget=%d, want -1 mid-rebuild", persisted)
	}
	if _, err := db.Exec(`UPDATE search_projection_state SET index_family_byte_limit=1 WHERE singleton=1`); err != nil {
		t.Fatal(err)
	}
	status, err := store.SearchProjectionStatus(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if status.PhysicalEvidence.Status != "complete" {
		t.Fatalf("physical_evidence=%+v, want complete so a coarse verdict exists", status.PhysicalEvidence)
	}
	if status.PhysicalBytes <= 1 {
		t.Fatalf("physical_bytes=%d, want above the forced 1-byte limit", status.PhysicalBytes)
	}
	if status.IndexFamilyWithinBudget != 0 {
		t.Fatalf("status index_family_within_budget=%d, want 0 from family total vs limit", status.IndexFamilyWithinBudget)
	}
	if err = db.QueryRow(`SELECT index_family_within_budget FROM search_projection_state`).Scan(&persisted); err != nil {
		t.Fatal(err)
	}
	if persisted != -1 {
		t.Fatalf("status must not persist the coarse verdict; column=%d", persisted)
	}
}

func TestSearchProjectionStatusUnknownBudgetVerdictNamesReason(t *testing.T) {
	store, _ := newCapacityTestStore(t, []struct{ id, body, created string }{
		{"e1", "verdict reason body", "2026-06-01T12:00:00Z"},
	})
	now := time.Date(2026, 6, 2, 0, 0, 0, 0, time.UTC)
	if _, err := store.Start(context.Background(), capacityBudget(64<<20), now); err != nil {
		t.Fatal(err)
	}
	searchProjectionFamilyTotalUnavailableForTest = true
	t.Cleanup(func() { searchProjectionFamilyTotalUnavailableForTest = false })
	status, err := store.SearchProjectionStatus(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if status.IndexFamilyWithinBudget != -1 {
		t.Fatalf("index_family_within_budget=%d, want -1 when family total is unavailable", status.IndexFamilyWithinBudget)
	}
	if strings.TrimSpace(status.PhysicalEvidence.Reason) == "" {
		t.Fatal("timeout/unavailable path returned -1 with no reason")
	}
}
