package types

import "time"

// Clock provides the current wall-clock time.
type Clock interface {
	Now() time.Time
}

// SystemClock reads the current wall-clock time from the system.
type SystemClock struct{}

// Now returns the current system time.
func (SystemClock) Now() time.Time { return time.Now() }
