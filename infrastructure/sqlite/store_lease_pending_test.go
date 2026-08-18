package sqlite

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func TestAcquireExclusive_WritesAndClearsCompactPendingMarker(t *testing.T) {
	path := filepath.Join(t.TempDir(), "store.db")
	release, err := (StoreLeaseCoordinator{}).AcquireExclusive(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	if !CompactPendingActive(path) {
		t.Fatal("expected compact-pending marker while exclusive is held")
	}
	marker, err := CompactPendingMarkerPath(path)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(marker)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(strings.TrimSpace(string(raw)), strconv.Itoa(os.Getpid())) {
		t.Fatalf("marker pid = %q, want this process", raw)
	}
	release()
	if CompactPendingActive(path) {
		t.Fatal("marker should clear after exclusive release")
	}
}

func TestCompactPendingActive_RemovesStalePID(t *testing.T) {
	path := filepath.Join(t.TempDir(), "store.db")
	marker, err := CompactPendingMarkerPath(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(marker, []byte("9999999\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if CompactPendingActive(path) {
		t.Fatal("dead pid should not keep a compact-pending marker alive")
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatalf("stale marker remained: %v", err)
	}
}
