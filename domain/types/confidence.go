package types

import (
	"slices"
	"strings"

	"golang.org/x/xerrors"
)

// Confidence represents confidence in a durable memory.
type Confidence string

const (
	// ConfidenceLow indicates weak confidence.
	ConfidenceLow Confidence = "low"
	// ConfidenceMedium indicates moderate confidence.
	ConfidenceMedium Confidence = "medium"
	// ConfidenceHigh indicates strong confidence.
	ConfidenceHigh Confidence = "high"
	// ConfidenceVerified indicates externally or manually verified confidence.
	ConfidenceVerified Confidence = "verified"
)

var knownConfidenceLevels = []Confidence{
	ConfidenceLow,
	ConfidenceMedium,
	ConfidenceHigh,
	ConfidenceVerified,
}

// ConfidenceFrom creates a Confidence from a string.
func ConfidenceFrom(value string) (Confidence, error) {
	trimmedValue := strings.TrimSpace(value)
	if trimmedValue == "" {
		return Confidence(""), xerrors.Errorf("confidence must not be empty")
	}
	if slices.Contains(knownConfidenceLevels, Confidence(trimmedValue)) {
		return Confidence(trimmedValue), nil
	}
	return Confidence(""), xerrors.Errorf("unknown confidence: %s", trimmedValue)
}

// String returns the string representation.
func (c Confidence) String() string { return string(c) }
