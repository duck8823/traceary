package cli

import (
	"strings"
	"time"

	"golang.org/x/xerrors"
)

// parseFlexibleTime parses a date/time string in either RFC3339 or YYYY-MM-DD format.
// When endExclusive is true and the input is a date-only string, the returned
// time is advanced by one day so that the caller can use a strict less-than
// comparison for the upper bound.
func parseFlexibleTime(value string, endExclusive bool) (time.Time, error) {
	trimmedValue := strings.TrimSpace(value)
	if trimmedValue == "" {
		return time.Time{}, nil
	}

	if parsedTime, err := time.Parse(time.RFC3339, trimmedValue); err == nil {
		return parsedTime.UTC(), nil
	}

	parsedDate, err := time.Parse("2006-01-02", trimmedValue)
	if err != nil {
		return time.Time{}, xerrors.Errorf(
			"%s: %w",
			Localize("time must be RFC3339 or YYYY-MM-DD", "日時は RFC3339 または YYYY-MM-DD 形式で指定してください"),
			err,
		)
	}
	if endExclusive {
		return parsedDate.AddDate(0, 0, 1), nil
	}

	return parsedDate, nil
}

// parseFlexibleTimePtr parses a date/time string into a *time.Time pointer.
// Returns nil when the input is empty. Used by commands that pass optional
// date pointers to query services (e.g. session list).
func parseFlexibleTimePtr(value string, endExclusive bool) (*time.Time, error) {
	t, err := parseFlexibleTime(value, endExclusive)
	if err != nil {
		return nil, err
	}
	if t.IsZero() {
		return nil, nil
	}
	return &t, nil
}
