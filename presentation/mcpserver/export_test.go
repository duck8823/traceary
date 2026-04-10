package mcpserver

import "time"

// ParseFlexibleTime exposes parseFlexibleTime for testing.
var ParseFlexibleTime = func(value string, endExclusive bool) (time.Time, error) {
	return parseFlexibleTime(value, endExclusive)
}

// ResolveLimit exposes resolveLimit for testing.
var ResolveLimit = resolveLimit

// ResolveOffset exposes resolveOffset for testing.
var ResolveOffset = resolveOffset
