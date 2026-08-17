package types

import "github.com/duck8823/traceary/domain/types"

// MemorySourceCounts is the true per-source candidate count matching a
// list filter, independent of Limit/Offset. It backs the inbox list
// pool-size summary (#2064).
type MemorySourceCounts struct {
	total    int
	bySource map[types.MemorySource]int
}

// MemorySourceCountsFrom builds counts from a per-source map.
func MemorySourceCountsFrom(bySource map[types.MemorySource]int) MemorySourceCounts {
	copied := make(map[types.MemorySource]int, len(bySource))
	total := 0
	for source, count := range bySource {
		if count <= 0 {
			continue
		}
		copied[source] = count
		total += count
	}
	return MemorySourceCounts{total: total, bySource: copied}
}

// Total returns the sum of all source buckets.
func (c MemorySourceCounts) Total() int { return c.total }

// Count returns the bucket for source, or 0.
func (c MemorySourceCounts) Count(source types.MemorySource) int {
	if c.bySource == nil {
		return 0
	}
	return c.bySource[source]
}
