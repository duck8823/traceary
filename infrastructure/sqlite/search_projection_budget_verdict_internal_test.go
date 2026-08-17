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

func TestSearchProjectionFamilyBytesFromObjectsIncludesIndexes(t *testing.T) {
	objects := []apptypes.CapacityObject{
		{Name: "search_projection_recent_documents", Kind: "table", Bytes: 100},
		{Name: "idx_search_projection_recent_eviction", Kind: "index", Bytes: 50},
		{Name: "idx_search_projection_exclusions_event", Kind: "index", Bytes: 25},
		{Name: "sqlite_autoindex_search_projection_source_sequence_1", Kind: "index", Bytes: 10},
		{Name: "idx_literal_search_fingerprint_candidate", Kind: "index", Bytes: 5},
		{Name: "sqlite_autoindex_literal_search_fingerprints_1", Kind: "index", Bytes: 7},
		{Name: "events", Kind: "table", Bytes: 999},
		{Name: "idx_events_created", Kind: "index", Bytes: 888},
		{Name: "sqlite_autoindex_event_search_documents_1", Kind: "index", Bytes: 777},
	}
	got := searchProjectionFamilyBytesFromObjects(objects)
	const want = 100 + 50 + 25 + 10 + 5 + 7
	if got != want {
		t.Fatalf("family bytes=%d, want %d (indexes must count; unrelated objects must not)", got, want)
	}
}

func TestSearchProjectionStatusCachedFamilyTotalIncludesIndexes(t *testing.T) {
	store, db := newCapacityTestStore(t, []struct{ id, body, created string }{
		{"e1", "index allocations belong to the family total", "2026-06-01T12:00:00Z"},
	})
	now := time.Date(2026, 6, 2, 0, 0, 0, 0, time.UTC)
	if _, err := store.Start(context.Background(), capacityBudget(64<<20), now); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`UPDATE search_projection_state SET index_family_byte_limit=100 WHERE singleton=1`); err != nil {
		t.Fatal(err)
	}
	storeDBStatCache(store.Path(), []apptypes.CapacityObject{
		{Name: "search_projection_recent_documents", Kind: "table", Pages: 1, Bytes: 1},
		{Name: "idx_search_projection_recent_eviction", Kind: "index", Pages: 1, Bytes: 4096},
	})
	status, err := store.SearchProjectionStatus(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if status.PhysicalEvidence.Status != "cached" {
		t.Fatalf("physical_evidence=%+v, want cached", status.PhysicalEvidence)
	}
	if status.PhysicalBytes != 4097 {
		t.Fatalf("physical_bytes=%d, want table+index=4097", status.PhysicalBytes)
	}
	if status.IndexFamilyWithinBudget != 0 {
		t.Fatalf("index_family_within_budget=%d, want 0 once index bytes exceed the 100-byte limit", status.IndexFamilyWithinBudget)
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
