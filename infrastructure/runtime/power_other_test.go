//go:build !darwin

package runtime

import (
	"context"
	"errors"
	"strings"
	"testing"

	app "github.com/duck8823/traceary/application/runtime"
)

func TestNativePowerUnsupportedPlatformRemainsClosed(t *testing.T) {
	sampler := nativeContinuitySampler{}
	if sampler.Sample(context.Background()).Available {
		t.Fatal("unsupported platform fabricated native continuity")
	}
	gate, err := app.NewObservationGate(context.Background(), sampler, strings.Repeat("a", 32))
	if err != nil {
		t.Fatal(err)
	}
	defer gate.Close()
	if _, err = gate.Stamp(context.Background()); !errors.Is(err, app.ErrObservationUnknown) || gate.Snapshot().Available {
		t.Fatalf("unsupported power evidence permitted causal intake: %v", err)
	}
}
