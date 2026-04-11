package types

import "time"

// TimelineBlock represents a contiguous work block separated by idle gaps.
type TimelineBlock struct {
	blockStart time.Time
	blockEnd   time.Time
	eventCount int
	workspaces []string
	agents     []string
	kinds      []string
}

// NewTimelineBlock creates a TimelineBlock.
func NewTimelineBlock(
	blockStart time.Time,
	blockEnd time.Time,
	eventCount int,
	workspaces []string,
	agents []string,
	kinds []string,
) TimelineBlock {
	return TimelineBlock{
		blockStart: blockStart,
		blockEnd:   blockEnd,
		eventCount: eventCount,
		workspaces: workspaces,
		agents:     agents,
		kinds:      kinds,
	}
}

// BlockStart returns when the block started.
func (b TimelineBlock) BlockStart() time.Time { return b.blockStart }

// BlockEnd returns when the block ended.
func (b TimelineBlock) BlockEnd() time.Time { return b.blockEnd }

// EventCount returns the number of events in the block.
func (b TimelineBlock) EventCount() int { return b.eventCount }

// Workspaces returns the workspaces involved.
func (b TimelineBlock) Workspaces() []string { return b.workspaces }

// Agents returns the agents involved.
func (b TimelineBlock) Agents() []string { return b.agents }

// Kinds returns the event kinds in the block.
func (b TimelineBlock) Kinds() []string { return b.kinds }
