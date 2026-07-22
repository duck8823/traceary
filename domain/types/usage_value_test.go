package types_test

import (
	"testing"

	"github.com/duck8823/traceary/domain/types"
)

func TestUsageValue_distinguishesUnknownUnavailableAndKnownZero(t *testing.T) {
	t.Parallel()

	unknown := types.UnknownUsageValue()
	if unknown.State() != types.UsageValueUnknown {
		t.Fatalf("unknown state = %q", unknown.State())
	}
	if _, ok := unknown.Value(); ok {
		t.Fatal("unknown Value() unexpectedly present")
	}

	unavailable := types.UnavailableUsageValue()
	if unavailable.State() != types.UsageValueUnavailable {
		t.Fatalf("unavailable state = %q", unavailable.State())
	}
	if _, ok := unavailable.Value(); ok {
		t.Fatal("unavailable Value() unexpectedly present")
	}

	zero, err := types.KnownUsageValue(0)
	if err != nil {
		t.Fatalf("KnownUsageValue(0) error = %v", err)
	}
	value, ok := zero.Value()
	if !ok || value != 0 || zero.State() != types.UsageValueKnown {
		t.Fatalf("known zero = state %q value %d present %v", zero.State(), value, ok)
	}
}

func TestKnownUsageValue_rejectsNegativeValue(t *testing.T) {
	t.Parallel()

	if _, err := types.KnownUsageValue(-1); err == nil {
		t.Fatal("KnownUsageValue(-1) error = nil")
	}
}

func TestUsageCounters_derivesAvailability(t *testing.T) {
	t.Parallel()

	knownZero, err := types.KnownUsageValue(0)
	if err != nil {
		t.Fatal(err)
	}
	knownPositive, err := types.KnownUsageValue(12)
	if err != nil {
		t.Fatal(err)
	}

	allUnknown := types.UnknownUsageCounters()
	if got := allUnknown.Availability(); got != types.UsageAvailabilityUnknown {
		t.Fatalf("unknown availability = %q", got)
	}

	allUnavailable, err := types.UsageCountersOf(
		types.UnavailableUsageValue(), types.UnavailableUsageValue(), types.UnavailableUsageValue(),
		types.UnavailableUsageValue(), types.UnavailableUsageValue(), types.UnavailableUsageValue(),
	)
	if err != nil {
		t.Fatalf("UsageCountersOf(unavailable) error = %v", err)
	}
	if got := allUnavailable.Availability(); got != types.UsageAvailabilityUnavailable {
		t.Fatalf("unavailable availability = %q", got)
	}

	partial, err := types.UsageCountersOf(
		knownZero, types.UnavailableUsageValue(), types.UnavailableUsageValue(),
		knownPositive, types.UnavailableUsageValue(), types.UnavailableUsageValue(),
	)
	if err != nil {
		t.Fatalf("UsageCountersOf(partial) error = %v", err)
	}
	if got := partial.Availability(); got != types.UsageAvailabilityPartial {
		t.Fatalf("partial availability = %q", got)
	}

	available, err := types.UsageCountersOf(
		knownZero, knownZero, knownZero, knownZero, knownZero, knownPositive,
	)
	if err != nil {
		t.Fatalf("UsageCountersOf(available) error = %v", err)
	}
	if got := available.Availability(); got != types.UsageAvailabilityAvailable {
		t.Fatalf("available availability = %q", got)
	}
}
