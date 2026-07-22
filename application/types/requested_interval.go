package types

import (
	"strings"
	"time"

	"golang.org/x/xerrors"
)

const defaultIntervalTimezone = "UTC"

// RequestedInterval preserves the caller-facing interval while exposing one
// effective half-open UTC range for every query surface. Date-only bounds are
// interpreted in Timezone; RFC3339 bounds are exact instants.
type RequestedInterval struct {
	requestedFrom          string
	requestedTo            string
	timezone               string
	snapshotAt             time.Time
	effectiveFromInclusive time.Time
	effectiveToExclusive   time.Time
	fromDateOnly           bool
	toDateOnly             bool
}

// RequestedIntervalFrom resolves caller-facing bounds into a half-open UTC
// interval. An omitted upper bound uses snapshotAt when it is non-zero.
func RequestedIntervalFrom(requestedFrom, requestedTo, timezone string, snapshotAt time.Time) (RequestedInterval, error) {
	requestedFrom = strings.TrimSpace(requestedFrom)
	requestedTo = strings.TrimSpace(requestedTo)
	timezone = strings.TrimSpace(timezone)
	if timezone == "" {
		timezone = defaultIntervalTimezone
	}
	if timezone == "Local" {
		return RequestedInterval{}, xerrors.New(`invalid timezone "Local": system-local timezone is not allowed`)
	}

	location, err := time.LoadLocation(timezone)
	if err != nil {
		return RequestedInterval{}, xerrors.Errorf("invalid timezone %q: %w", timezone, err)
	}

	from, fromDateOnly, err := parseRequestedIntervalBound(requestedFrom, location, false)
	if err != nil {
		return RequestedInterval{}, xerrors.Errorf("invalid from bound: %w", err)
	}
	to, toDateOnly, err := parseRequestedIntervalBound(requestedTo, location, true)
	if err != nil {
		return RequestedInterval{}, xerrors.Errorf("invalid to bound: %w", err)
	}
	if requestedTo == "" && !snapshotAt.IsZero() {
		to = snapshotAt.UTC()
	}
	if !from.IsZero() && !to.IsZero() && !from.Before(to) {
		return RequestedInterval{}, xerrors.New("from bound must be earlier than to bound")
	}

	return RequestedInterval{
		requestedFrom:          requestedFrom,
		requestedTo:            requestedTo,
		timezone:               timezone,
		snapshotAt:             snapshotAt.UTC(),
		effectiveFromInclusive: from,
		effectiveToExclusive:   to,
		fromDateOnly:           fromDateOnly,
		toDateOnly:             toDateOnly,
	}, nil
}

// WithDefaultFrom applies fallback as the effective lower bound only when the
// caller omitted the lower bound. The requested value remains empty so output
// can distinguish an explicit bound from a command-provided default window.
func (i RequestedInterval) WithDefaultFrom(fallback time.Time) (RequestedInterval, error) {
	if i.HasRequestedFrom() || !i.effectiveFromInclusive.IsZero() || fallback.IsZero() {
		return i, nil
	}
	fallback = fallback.UTC()
	if !i.effectiveToExclusive.IsZero() && !fallback.Before(i.effectiveToExclusive) {
		return RequestedInterval{}, xerrors.New("default from bound must be earlier than to bound")
	}
	i.effectiveFromInclusive = fallback
	return i, nil
}

func parseRequestedIntervalBound(value string, location *time.Location, endExclusive bool) (time.Time, bool, error) {
	if value == "" {
		return time.Time{}, false, nil
	}
	if parsed, err := time.Parse(time.RFC3339Nano, value); err == nil {
		return parsed.UTC(), false, nil
	}
	parsed, err := time.ParseInLocation(time.DateOnly, value, location)
	if err != nil {
		return time.Time{}, false, xerrors.Errorf("time must be RFC3339 or YYYY-MM-DD: %w", err)
	}
	if endExclusive {
		parsed = parsed.AddDate(0, 0, 1)
	}
	return parsed.UTC(), true, nil
}

// RequestedFrom returns the trimmed caller-supplied lower bound.
func (i RequestedInterval) RequestedFrom() string { return i.requestedFrom }

// RequestedTo returns the trimmed caller-supplied upper bound.
func (i RequestedInterval) RequestedTo() string { return i.requestedTo }

// Timezone returns the IANA timezone used for date-only bounds.
func (i RequestedInterval) Timezone() string { return i.timezone }

// SnapshotAt returns the UTC snapshot supplied during resolution.
func (i RequestedInterval) SnapshotAt() time.Time { return i.snapshotAt }

// EffectiveFromInclusive returns the resolved inclusive UTC lower bound.
func (i RequestedInterval) EffectiveFromInclusive() time.Time { return i.effectiveFromInclusive }

// EffectiveToExclusive returns the resolved exclusive UTC upper bound.
func (i RequestedInterval) EffectiveToExclusive() time.Time { return i.effectiveToExclusive }

// FromIsDateOnly reports whether the lower bound was a calendar date.
func (i RequestedInterval) FromIsDateOnly() bool { return i.fromDateOnly }

// ToIsDateOnly reports whether the upper bound was a calendar date.
func (i RequestedInterval) ToIsDateOnly() bool { return i.toDateOnly }

// HasRequestedFrom reports whether the caller supplied a lower bound.
func (i RequestedInterval) HasRequestedFrom() bool { return i.requestedFrom != "" }

// HasRequestedTo reports whether the caller supplied an upper bound.
func (i RequestedInterval) HasRequestedTo() bool { return i.requestedTo != "" }
