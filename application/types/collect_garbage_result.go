package types

import "time"

// CollectGarbageResult is the result of a garbage-collection run.
type CollectGarbageResult struct {
	deletedCount int
	before       time.Time
	dryRun       bool
}

// CollectGarbageResultOf creates a CollectGarbageResult.
func CollectGarbageResultOf(deletedCount int, before time.Time, dryRun bool) CollectGarbageResult {
	return CollectGarbageResult{
		deletedCount: deletedCount,
		before:       before,
		dryRun:       dryRun,
	}
}

// DeletedCount returns the number of deleted events.
func (r CollectGarbageResult) DeletedCount() int { return r.deletedCount }

// Before returns the threshold used for the deletion.
func (r CollectGarbageResult) Before() time.Time { return r.before }

// DryRun reports whether the run was a dry run.
func (r CollectGarbageResult) DryRun() bool { return r.dryRun }
