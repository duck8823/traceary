package types_test

import (
	"context"
	"testing"

	apptypes "github.com/duck8823/traceary/application/types"
)

func TestHookDeliveryContext_NormalizesIdentityAndPreservesRawWorkspace(t *testing.T) {
	input := apptypes.HookDeliveryInputOf(" native-1 ", " /repo/a ")
	ctx := apptypes.WithHookDelivery(context.Background(), input)

	got, ok := apptypes.HookDeliveryFromContext(ctx)
	if !ok {
		t.Fatal("HookDeliveryFromContext() found = false, want true")
	}
	if got.NativeID() != "native-1" {
		t.Fatalf("NativeID() = %q, want native-1", got.NativeID())
	}
	if got.RawWorkspace() != " /repo/a " {
		t.Fatalf("RawWorkspace() = %q, want exact host evidence", got.RawWorkspace())
	}
}

func TestHookDeliveryFromContext_Absent(t *testing.T) {
	if _, ok := apptypes.HookDeliveryFromContext(context.Background()); ok {
		t.Fatal("HookDeliveryFromContext() found = true, want false")
	}
}
