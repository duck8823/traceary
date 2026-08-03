//go:build !darwin && !linux

package sqlite

import (
	"context"
	"errors"
	"testing"
)

func TestPreparedStoreUpgradeFailsClosedOnUnsupportedPlatform(t *testing.T) {
	if atomicExchangeSupported() {
		t.Fatal("unsupported platform reported atomic exchange support")
	}
	if err := atomicExchange("left", "right"); !errors.Is(err, ErrPreparedUpgradeUnsupported) {
		t.Fatalf("atomicExchange error = %v", err)
	}
	if _, err := (StoreLeaseCoordinator{}).AcquireExclusive(context.Background(), "/tmp/store.db"); !errors.Is(err, ErrPreparedUpgradeUnsupported) {
		t.Fatalf("AcquireExclusive error = %v", err)
	}
}
