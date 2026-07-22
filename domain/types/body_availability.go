package types

import "golang.org/x/xerrors"

// BodyAvailability describes whether an event raw body can be returned.
type BodyAvailability string

const (
	// BodyAvailabilityAvailable means the persisted raw body is readable.
	BodyAvailabilityAvailable BodyAvailability = "available"
	// BodyAvailabilityUnavailableRetention means an explicit retention apply removed the raw body.
	BodyAvailabilityUnavailableRetention BodyAvailability = "unavailable_retention"

	// EventBodyUnavailableRetentionMarker is the compatibility-safe persisted
	// placeholder for readers that predate body_availability. Current readers
	// convert it to an absent body plus an explicit availability reason.
	EventBodyUnavailableRetentionMarker = "[traceary:body-unavailable:retention]"
)

// BodyAvailabilityFrom validates a persisted body-availability value.
func BodyAvailabilityFrom(value string) (BodyAvailability, error) {
	availability := BodyAvailability(value)
	switch availability {
	case BodyAvailabilityAvailable, BodyAvailabilityUnavailableRetention:
		return availability, nil
	default:
		return "", xerrors.Errorf("unsupported body availability: %q", value)
	}
}

// String returns the persisted representation.
func (a BodyAvailability) String() string { return string(a) }

// IsAvailable reports whether the raw body can be returned.
func (a BodyAvailability) IsAvailable() bool { return a == BodyAvailabilityAvailable }
