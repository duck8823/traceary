package types_test

import (
	"errors"
	"testing"

	apptypes "github.com/duck8823/traceary/application/types"
	"golang.org/x/xerrors"
)

func TestStoreMaintenancePendingErrorSurvivesWrapping(t *testing.T) {
	t.Parallel()
	inner := &apptypes.StoreMaintenancePendingError{StorePath: "/tmp/store.db"}
	wrapped := xerrors.Errorf("failed to ping SQLite DB: %w", inner)
	var pending *apptypes.StoreMaintenancePendingError
	if !errors.As(wrapped, &pending) {
		t.Fatal("errors.As must detect StoreMaintenancePendingError through xerrors wrap")
	}
	if pending.StorePath != "/tmp/store.db" {
		t.Fatalf("StorePath = %q", pending.StorePath)
	}
	if pending.Error() != "store upgrade in progress; hook write deferred to spool" {
		t.Fatalf("Error() = %q", pending.Error())
	}
}
