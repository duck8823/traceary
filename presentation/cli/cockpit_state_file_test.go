package cli

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"
)

func TestFileCockpitStateStorePersistsAndResetsLastSeen(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "state", "cockpit.json")
	store := newFileCockpitStateStoreAt(path)
	memorySeen := time.Date(2026, 5, 23, 1, 2, 3, 4, time.UTC)
	eventSeen := memorySeen.Add(time.Minute)

	if _, ok, err := store.MemoryLastSeenAt(context.Background()); err != nil || ok {
		t.Fatalf("initial MemoryLastSeenAt ok/err = %v/%v, want false/nil", ok, err)
	}
	if err := store.MarkMemoryLastSeenAt(context.Background(), memorySeen); err != nil {
		t.Fatalf("MarkMemoryLastSeenAt() error = %v", err)
	}
	if err := store.MarkEventLastSeenAt(context.Background(), eventSeen, []string{"evt-2", "evt-1"}); err != nil {
		t.Fatalf("MarkEventLastSeenAt() error = %v", err)
	}

	gotMemory, ok, err := store.MemoryLastSeenAt(context.Background())
	if err != nil || !ok || !gotMemory.Equal(memorySeen) {
		t.Fatalf("MemoryLastSeenAt = %v/%v/%v, want %v/true/nil", gotMemory, ok, err, memorySeen)
	}
	gotEvent, ok, err := store.EventLastSeenAt(context.Background())
	if err != nil || !ok || !gotEvent.Equal(eventSeen) {
		t.Fatalf("EventLastSeenAt = %v/%v/%v, want %v/true/nil", gotEvent, ok, err, eventSeen)
	}
	gotEventIDs, ok, err := store.EventLastSeenIDs(context.Background())
	if err != nil || !ok || !slices.Equal(gotEventIDs, []string{"evt-1", "evt-2"}) {
		t.Fatalf("EventLastSeenIDs = %v/%v/%v, want [evt-1 evt-2]/true/nil", gotEventIDs, ok, err)
	}

	if err := store.ResetCockpitState(context.Background()); err != nil {
		t.Fatalf("ResetCockpitState() error = %v", err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("state file still exists or stat failed with unexpected error: %v", err)
	}
}

func TestFileCockpitStateStoreKeepsLastSeenMonotonic(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "state", "cockpit.json")
	store := newFileCockpitStateStoreAt(path)
	newer := time.Date(2026, 5, 23, 3, 0, 0, 0, time.UTC)
	older := newer.Add(-time.Hour)

	if err := store.MarkMemoryLastSeenAt(context.Background(), newer); err != nil {
		t.Fatalf("MarkMemoryLastSeenAt(newer) error = %v", err)
	}
	if err := store.MarkMemoryLastSeenAt(context.Background(), older); err != nil {
		t.Fatalf("MarkMemoryLastSeenAt(older) error = %v", err)
	}
	if err := store.MarkEventLastSeenAt(context.Background(), newer, []string{"evt-newer"}); err != nil {
		t.Fatalf("MarkEventLastSeenAt(newer) error = %v", err)
	}
	if err := store.MarkEventLastSeenAt(context.Background(), older, []string{"evt-older"}); err != nil {
		t.Fatalf("MarkEventLastSeenAt(older) error = %v", err)
	}
	if err := store.MarkEventLastSeenAt(context.Background(), newer, []string{"evt-equal"}); err != nil {
		t.Fatalf("MarkEventLastSeenAt(equal) error = %v", err)
	}

	gotMemory, ok, err := store.MemoryLastSeenAt(context.Background())
	if err != nil || !ok || !gotMemory.Equal(newer) {
		t.Fatalf("MemoryLastSeenAt = %v/%v/%v, want %v/true/nil", gotMemory, ok, err, newer)
	}
	gotEvent, ok, err := store.EventLastSeenAt(context.Background())
	if err != nil || !ok || !gotEvent.Equal(newer) {
		t.Fatalf("EventLastSeenAt = %v/%v/%v, want %v/true/nil", gotEvent, ok, err, newer)
	}
	gotEventIDs, ok, err := store.EventLastSeenIDs(context.Background())
	if err != nil || !ok || !slices.Equal(gotEventIDs, []string{"evt-equal", "evt-newer"}) {
		t.Fatalf("EventLastSeenIDs = %v/%v/%v, want [evt-equal evt-newer]/true/nil", gotEventIDs, ok, err)
	}
}

func TestFileCockpitStateStoreRecoversStaleLock(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "state", "cockpit.json")
	store := newFileCockpitStateStoreAt(path)
	lockPath := path + ".lock"
	if err := os.MkdirAll(lockPath, 0o700); err != nil {
		t.Fatalf("MkdirAll(lockPath) error = %v", err)
	}
	staleAt := time.Now().Add(-cockpitStateStaleLockAfter - time.Minute)
	if err := os.Chtimes(lockPath, staleAt, staleAt); err != nil {
		t.Fatalf("Chtimes(lockPath) error = %v", err)
	}
	seenAt := time.Date(2026, 5, 23, 4, 0, 0, 0, time.UTC)

	if err := store.MarkEventLastSeenAt(context.Background(), seenAt, []string{"evt-seen"}); err != nil {
		t.Fatalf("MarkEventLastSeenAt() error = %v", err)
	}
	if _, err := os.Stat(lockPath); !os.IsNotExist(err) {
		t.Fatalf("stale lock still exists or stat failed with unexpected error: %v", err)
	}
	got, ok, err := store.EventLastSeenAt(context.Background())
	if err != nil || !ok || !got.Equal(seenAt) {
		t.Fatalf("EventLastSeenAt = %v/%v/%v, want %v/true/nil", got, ok, err, seenAt)
	}
}
