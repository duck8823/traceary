package types

import (
	"strings"

	"golang.org/x/xerrors"
)

// UsageCostState separates absent cost evidence from a known amount.
type UsageCostState string

const (
	// UsageCostUnknown means terminal cost evidence is not known yet.
	UsageCostUnknown UsageCostState = "unknown"
	// UsageCostUnavailable means the terminal source has no usable cost.
	UsageCostUnavailable UsageCostState = "unavailable"
	// UsageCostKnown means a non-negative amount and provenance are present.
	UsageCostKnown UsageCostState = "known"
)

// UsageCostOrigin distinguishes a versioned Traceary estimate from an amount
// explicitly reported by a provider surface.
type UsageCostOrigin string

const (
	// UsageCostEstimated identifies a Traceary estimate from a versioned table.
	UsageCostEstimated UsageCostOrigin = "estimated"
	// UsageCostProviderReported identifies an amount reported by the source.
	UsageCostProviderReported UsageCostOrigin = "provider_reported"
)

// UsageCost stores integer currency micro-units to avoid floating-point
// accounting. Currency is an uppercase ISO-style three-letter code.
type UsageCost struct {
	state             UsageCostState
	amountMicros      int64
	currency          string
	origin            UsageCostOrigin
	priceTableVersion string
}

// UnknownUsageCost creates the cost state for a pending observation.
func UnknownUsageCost() UsageCost { return UsageCost{state: UsageCostUnknown} }

// UnavailableUsageCost creates a terminal cost without a numeric amount.
func UnavailableUsageCost() UsageCost { return UsageCost{state: UsageCostUnavailable} }

// EstimatedUsageCost creates a versioned Traceary estimate in micro-units.
func EstimatedUsageCost(amountMicros int64, currency string, priceTableVersion string) (UsageCost, error) {
	version := strings.TrimSpace(priceTableVersion)
	if version == "" {
		return UsageCost{}, xerrors.Errorf("estimated usage cost requires a price-table version")
	}
	return knownUsageCost(amountMicros, currency, UsageCostEstimated, version)
}

// ProviderReportedUsageCost creates source-reported cost in micro-units.
func ProviderReportedUsageCost(amountMicros int64, currency string) (UsageCost, error) {
	return knownUsageCost(amountMicros, currency, UsageCostProviderReported, "")
}

// UsageCostFrom restores a validated persisted cost tuple.
func UsageCostFrom(
	state string,
	amountMicros Optional[int64],
	currency string,
	origin string,
	priceTableVersion string,
) (UsageCost, error) {
	switch UsageCostState(state) {
	case UsageCostUnknown:
		if err := requireEmptyUsageCost(amountMicros, currency, origin, priceTableVersion); err != nil {
			return UsageCost{}, xerrors.Errorf("invalid unknown usage cost: %w", err)
		}
		return UnknownUsageCost(), nil
	case UsageCostUnavailable:
		if err := requireEmptyUsageCost(amountMicros, currency, origin, priceTableVersion); err != nil {
			return UsageCost{}, xerrors.Errorf("invalid unavailable usage cost: %w", err)
		}
		return UnavailableUsageCost(), nil
	case UsageCostKnown:
		amount, present := amountMicros.Value()
		if !present {
			return UsageCost{}, xerrors.Errorf("known usage cost requires an amount")
		}
		switch UsageCostOrigin(origin) {
		case UsageCostEstimated:
			return EstimatedUsageCost(amount, currency, priceTableVersion)
		case UsageCostProviderReported:
			if strings.TrimSpace(priceTableVersion) != "" {
				return UsageCost{}, xerrors.Errorf("provider-reported usage cost must not carry a price-table version")
			}
			return ProviderReportedUsageCost(amount, currency)
		default:
			return UsageCost{}, xerrors.Errorf("unsupported usage cost origin: %q", origin)
		}
	default:
		return UsageCost{}, xerrors.Errorf("unsupported usage cost state: %q", state)
	}
}

func knownUsageCost(amountMicros int64, currency string, origin UsageCostOrigin, priceTableVersion string) (UsageCost, error) {
	if amountMicros < 0 {
		return UsageCost{}, xerrors.Errorf("usage cost amount must not be negative")
	}
	normalizedCurrency := strings.TrimSpace(currency)
	if !isUpperCurrency(normalizedCurrency) {
		return UsageCost{}, xerrors.Errorf("usage cost currency must be three uppercase ASCII letters")
	}
	return UsageCost{
		state: UsageCostKnown, amountMicros: amountMicros, currency: normalizedCurrency,
		origin: origin, priceTableVersion: priceTableVersion,
	}, nil
}

func requireEmptyUsageCost(amount Optional[int64], currency, origin, version string) error {
	if _, present := amount.Value(); present || currency != "" || origin != "" || version != "" {
		return xerrors.Errorf("cost without a known value must not carry amount or provenance")
	}
	return nil
}

func isUpperCurrency(value string) bool {
	if len(value) != 3 {
		return false
	}
	for _, char := range value {
		if char < 'A' || char > 'Z' {
			return false
		}
	}
	return true
}

// State returns the explicit cost availability state.
func (c UsageCost) State() UsageCostState { return c.state }

// AmountMicros returns the integer micro-unit amount only when known.
func (c UsageCost) AmountMicros() (int64, bool) {
	if c.state != UsageCostKnown {
		return 0, false
	}
	return c.amountMicros, true
}

// Currency returns the uppercase three-letter currency when known.
func (c UsageCost) Currency() string { return c.currency }

// Origin returns estimate or provider-reported provenance when known.
func (c UsageCost) Origin() UsageCostOrigin { return c.origin }

// PriceTableVersion returns the required version for an estimated cost.
func (c UsageCost) PriceTableVersion() string { return c.priceTableVersion }

func (s UsageCostState) String() string { return string(s) }

func (o UsageCostOrigin) String() string { return string(o) }
