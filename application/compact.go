package application

import (
	"time"

	"github.com/duck8823/traceary/domain"
)

// CompactInput is the single-command compaction request.
type CompactInput struct {
	Source string
	// WorkDir, when set, holds the copy-filter work file on another volume
	// so the source volume only needs dest-sized free space for VACUUM INTO.
	WorkDir string
}

// Compact strategies recorded in CompactResult.CompactStrategy.
const (
	CompactStrategyReplica   = "replica"
	CompactStrategyExternal  = "external"
	CompactStrategyDestSized = "dest_sized"
	CompactStrategyInPlace   = "in_place"
)

// CompactResult is the operator-visible outcome of one rewrite.
type CompactResult struct {
	Run                       domain.CompactionRun
	BytesBefore               int64
	BytesAfter                int64
	ReleasedCommandBodyRows   int
	ReleasedCommandBodyBytes  int64
	EstimatedReclaimableBytes int64
	CompactStrategy           string
	Steps                     CompactSteps
}

// CompactStep is one attributed copy-filter step. Bytes are stored (blob)
// bytes on the work copy, not plaintext, so they add up to file-size deltas.
type CompactStep struct {
	Name           string
	Rows           int64
	BytesBefore    int64
	BytesAfter     int64
	BytesReclaimed int64
	Skipped        string
	Detail         map[string]int64
}

// Compact step names are the JSON keys under store compact "steps".
const (
	CompactStepProjectionReclaim = "projection_reclaim" // #2261
)

// CompactSteps is the ordered list of steps a rewrite performed.
type CompactSteps []CompactStep

// Find returns the named step when the rewrite recorded it.
func (s CompactSteps) Find(name string) (CompactStep, bool) {
	for _, step := range s {
		if step.Name == name {
			return step, true
		}
	}
	return CompactStep{}, false
}

// CommandBodyReclaim is the measured set of duplicated command_executed
// bodies compact will clear. Bytes are stored blob lengths, not plaintext.
type CommandBodyReclaim struct {
	Rows  int
	Bytes int64
}

// CompactFilter configures the copy-filter inside Build.
// A zero value is vacuum-only besides command-body reclaim and retired-index drop.
type CompactFilter struct {
	// WorkDir stages the source-sized work copy on another volume (#2008).
	WorkDir string
	// OnStep receives one attributed copy-filter step after that step runs.
	OnStep func(CompactStep)
}

// Report delivers one copy-filter step. It is a no-op when OnStep is nil.
func (f CompactFilter) Report(step CompactStep) {
	if f.OnStep == nil {
		return
	}
	if step.BytesReclaimed < 0 {
		step.BytesReclaimed = 0
	}
	f.OnStep(step)
}

// DefaultCompactKeepDays matches the retired store gc window.
const DefaultCompactKeepDays = 90

// CompactCutoff returns now minus keepDays, using DefaultCompactKeepDays when
// keepDays is not positive.
func CompactCutoff(now time.Time, keepDays int) time.Time {
	if now.IsZero() {
		now = time.Now()
	}
	if keepDays <= 0 {
		keepDays = DefaultCompactKeepDays
	}
	return now.UTC().AddDate(0, 0, -keepDays)
}
