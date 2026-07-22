package types

import "golang.org/x/xerrors"

// UsageValueState describes whether one provider-reported usage dimension has
// a numeric value. Known zero is distinct from both missing states.
type UsageValueState string

const (
	// UsageValueUnknown means the authoritative terminal value is not known yet.
	UsageValueUnknown UsageValueState = "unknown"
	// UsageValueUnavailable means the terminal source did not report a value.
	UsageValueUnavailable UsageValueState = "unavailable"
	// UsageValueKnown means a non-negative number, including zero, is present.
	UsageValueKnown UsageValueState = "known"
)

// UsageAvailability is the derived completeness of a set of usage counters.
type UsageAvailability string

const (
	// UsageAvailabilityUnknown means at least one dimension remains open.
	UsageAvailabilityUnknown UsageAvailability = "unknown"
	// UsageAvailabilityUnavailable means no dimension has a numeric value.
	UsageAvailabilityUnavailable UsageAvailability = "unavailable"
	// UsageAvailabilityPartial means some, but not all, dimensions are known.
	UsageAvailabilityPartial UsageAvailability = "partial"
	// UsageAvailabilityAvailable means every dimension is known.
	UsageAvailabilityAvailable UsageAvailability = "available"
)

// UsageValue preserves the availability state independently from its value.
type UsageValue struct {
	state UsageValueState
	value int64
}

// UnknownUsageValue creates a value whose authoritative terminal state is not
// known yet. It is valid only on pending observations.
func UnknownUsageValue() UsageValue { return UsageValue{state: UsageValueUnknown} }

// UnavailableUsageValue creates a terminal value for a dimension the source
// did not report.
func UnavailableUsageValue() UsageValue { return UsageValue{state: UsageValueUnavailable} }

// KnownUsageValue creates a provider-reported or derived non-negative count.
// Zero remains an explicit known value.
func KnownUsageValue(value int64) (UsageValue, error) {
	if value < 0 {
		return UsageValue{}, xerrors.Errorf("usage value must not be negative")
	}
	return UsageValue{state: UsageValueKnown, value: value}, nil
}

// UsageValueFrom restores and validates a persisted state/value pair.
func UsageValueFrom(state string, value Optional[int64]) (UsageValue, error) {
	switch UsageValueState(state) {
	case UsageValueUnknown:
		if _, present := value.Value(); present {
			return UsageValue{}, xerrors.Errorf("unknown usage value must not carry a number")
		}
		return UnknownUsageValue(), nil
	case UsageValueUnavailable:
		if _, present := value.Value(); present {
			return UsageValue{}, xerrors.Errorf("unavailable usage value must not carry a number")
		}
		return UnavailableUsageValue(), nil
	case UsageValueKnown:
		numeric, present := value.Value()
		if !present {
			return UsageValue{}, xerrors.Errorf("known usage value requires a number")
		}
		return KnownUsageValue(numeric)
	default:
		return UsageValue{}, xerrors.Errorf("unsupported usage value state: %q", state)
	}
}

// State returns the explicit availability state.
func (v UsageValue) State() UsageValueState { return v.state }

// Value returns a numeric value only for the known state.
func (v UsageValue) Value() (int64, bool) {
	if v.state != UsageValueKnown {
		return 0, false
	}
	return v.value, true
}

func (v UsageValue) validate() error {
	_, err := UsageValueFrom(v.state.String(), optionalUsageValue(v))
	return err
}

// String returns the persisted state representation.
func (s UsageValueState) String() string { return string(s) }

func optionalUsageValue(value UsageValue) Optional[int64] {
	if numeric, present := value.Value(); present {
		return Some(numeric)
	}
	return None[int64]()
}

// UsageCounters contains every provider-neutral token dimension. Each
// dimension keeps its own availability because host contracts are partial.
type UsageCounters struct {
	input           UsageValue
	cachedInput     UsageValue
	cacheWriteInput UsageValue
	output          UsageValue
	reasoningOutput UsageValue
	total           UsageValue
}

// UnknownUsageCounters creates the only counter state valid for a pending
// observation.
func UnknownUsageCounters() UsageCounters {
	unknown := UnknownUsageValue()
	return usageCountersOfUnchecked(unknown, unknown, unknown, unknown, unknown, unknown)
}

// UsageCountersOf creates a validated counter set.
func UsageCountersOf(
	input UsageValue,
	cachedInput UsageValue,
	cacheWriteInput UsageValue,
	output UsageValue,
	reasoningOutput UsageValue,
	total UsageValue,
) (UsageCounters, error) {
	values := []UsageValue{input, cachedInput, cacheWriteInput, output, reasoningOutput, total}
	for _, value := range values {
		if err := value.validate(); err != nil {
			return UsageCounters{}, xerrors.Errorf("invalid usage counters: %w", err)
		}
	}
	return usageCountersOfUnchecked(input, cachedInput, cacheWriteInput, output, reasoningOutput, total), nil
}

func usageCountersOfUnchecked(
	input UsageValue,
	cachedInput UsageValue,
	cacheWriteInput UsageValue,
	output UsageValue,
	reasoningOutput UsageValue,
	total UsageValue,
) UsageCounters {
	return UsageCounters{
		input: input, cachedInput: cachedInput, cacheWriteInput: cacheWriteInput,
		output: output, reasoningOutput: reasoningOutput, total: total,
	}
}

// Input returns input-token availability and value.
func (c UsageCounters) Input() UsageValue { return c.input }

// CachedInput returns cached-input-token availability and value.
func (c UsageCounters) CachedInput() UsageValue { return c.cachedInput }

// CacheWriteInput returns cache-write-input-token availability and value.
func (c UsageCounters) CacheWriteInput() UsageValue { return c.cacheWriteInput }

// Output returns output-token availability and value.
func (c UsageCounters) Output() UsageValue { return c.output }

// ReasoningOutput returns reasoning-output-token availability and value.
func (c UsageCounters) ReasoningOutput() UsageValue { return c.reasoningOutput }

// Total returns total-token availability and value.
func (c UsageCounters) Total() UsageValue { return c.total }

// Availability derives source completeness without collapsing unavailable
// dimensions into numeric zero.
func (c UsageCounters) Availability() UsageAvailability {
	values := c.values()
	known := 0
	for _, value := range values {
		if value.State() == UsageValueUnknown {
			return UsageAvailabilityUnknown
		}
		if value.State() == UsageValueKnown {
			known++
		}
	}
	switch known {
	case 0:
		return UsageAvailabilityUnavailable
	case len(values):
		return UsageAvailabilityAvailable
	default:
		return UsageAvailabilityPartial
	}
}

func (c UsageCounters) values() []UsageValue {
	return []UsageValue{c.input, c.cachedInput, c.cacheWriteInput, c.output, c.reasoningOutput, c.total}
}
