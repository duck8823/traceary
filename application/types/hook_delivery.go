package types

import (
	"context"
	"strings"
)

type hookDeliveryContextKey struct{}

// HookDeliveryInput is host-normalized delivery evidence carried from a hook
// adapter to the event/session usecase. NativeID is empty when the host does
// not expose a stable delivery identifier; callers must not invent one from
// body equality.
type HookDeliveryInput struct {
	nativeID     string
	rawWorkspace string
}

// HookDeliveryInputOf creates normalized hook delivery input.
func HookDeliveryInputOf(nativeID, rawWorkspace string) HookDeliveryInput {
	return HookDeliveryInput{
		nativeID:     strings.TrimSpace(nativeID),
		rawWorkspace: strings.TrimSpace(rawWorkspace),
	}
}

// NativeID returns the host-native stable delivery identifier.
func (i HookDeliveryInput) NativeID() string { return i.nativeID }

// RawWorkspace returns the unnormalized host workspace evidence.
func (i HookDeliveryInput) RawWorkspace() string { return i.rawWorkspace }

// WithHookDelivery returns a derived context carrying normalized delivery
// input. Empty native IDs are intentionally retained so usecases can preserve
// raw workspace provenance without enabling delivery deduplication.
func WithHookDelivery(ctx context.Context, input HookDeliveryInput) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	return context.WithValue(ctx, hookDeliveryContextKey{}, input)
}

// HookDeliveryFromContext returns host-normalized delivery input when present.
func HookDeliveryFromContext(ctx context.Context) (HookDeliveryInput, bool) {
	if ctx == nil {
		return HookDeliveryInput{}, false
	}
	input, ok := ctx.Value(hookDeliveryContextKey{}).(HookDeliveryInput)
	return input, ok
}
