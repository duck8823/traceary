package sqlite

import (
	"context"
	"strings"
	"testing"
	"time"

	apptypes "github.com/duck8823/traceary/application/types"
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
	seedProjectionFamilyDBStatCache(t, store.Path(), 4096)
	status, err := store.SearchProjectionStatus(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if status.PhysicalEvidence.Status != "cached" {
		t.Fatalf("physical_evidence=%+v, want cached family total", status.PhysicalEvidence)
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

func TestSearchProjectionStatusSkipsUncachedDBStat(t *testing.T) {
	store, _ := newCapacityTestStore(t, []struct{ id, body, created string }{
		{"e1", "status must not walk dbstat", "2026-06-01T12:00:00Z"},
	})
	now := time.Date(2026, 6, 2, 0, 0, 0, 0, time.UTC)
	if _, err := store.Start(context.Background(), capacityBudget(64<<20), now); err != nil {
		t.Fatal(err)
	}
	status, err := store.SearchProjectionStatus(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if status.PhysicalEvidence.Status != "unavailable" {
		t.Fatalf("physical_evidence=%+v, want unavailable without a cache", status.PhysicalEvidence)
	}
	if !strings.Contains(status.PhysicalEvidence.Reason, "cache miss") {
		t.Fatalf("reason=%q, want cache miss", status.PhysicalEvidence.Reason)
	}
	if status.State == "" {
		t.Fatal("lifecycle state must still be populated without dbstat")
	}
}

func seedProjectionFamilyDBStatCache(t *testing.T, path string, familyBytes int64) {
	t.Helper()
	storeDBStatCache(path, []apptypes.CapacityObject{{
		Name:  "search_projection_recent_documents",
		Kind:  "table",
		Pages: 1,
		Bytes: familyBytes,
	}})
}
