//go:build !darwin && !linux

package sqlite

import (
	"context"
	"testing"
)

func TestStoreLeaseUnsupportedPlatformFailsClosed(t *testing.T) {
	if _, err := (StoreLeaseCoordinator{}).AcquireExclusive(context.Background(), "store.db"); err == nil {
		t.Fatal("unsupported platform acquired a store lease")
	}
	if err := validateStoreLinkIdentity("store.db"); err == nil {
		t.Fatal("unsupported platform accepted a store identity")
	}
}
