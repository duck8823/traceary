package types

import (
	"time"

	domtypes "github.com/duck8823/traceary/domain/types"
)

// MemoryDecayCriteria configures a decay run.
type MemoryDecayCriteria struct {
	OlderThan     time.Duration
	Limit         int
	Apply         bool
	Dedupe        bool
	IncludeHidden bool
	Workspace     domtypes.Optional[domtypes.Workspace]
	Now           time.Time
}

// MemoryDecayResult is the outcome of a decay dry-run or apply.
type MemoryDecayResult struct {
	ExpiredIDs     []string
	SupersededIDs  []string
	Skipped        map[string]int
	RemainingAfter int
	Applied        bool
	OlderThan      time.Duration
	Scanned        int
}
