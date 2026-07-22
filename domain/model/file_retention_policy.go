package model

import (
	"sort"
	"time"

	"golang.org/x/xerrors"

	"github.com/duck8823/traceary/domain/types"
)

// FileRetentionEntry is verified inventory evidence consumed by the policy.
// Filesystem metadata and verification mechanics remain outside the domain.
type FileRetentionEntry struct {
	identity       string
	relativePath   string
	createdAt      time.Time
	generation     string
	contentDigest  string
	allocatedBytes int64
	allocatedKnown bool
	verified       bool
	protected      bool
	pinned         bool
	blockingReason string
}

// FileRetentionEntryParams restores one complete inventory entry.
type FileRetentionEntryParams struct {
	Identity       string
	RelativePath   string
	CreatedAt      time.Time
	Generation     string
	ContentDigest  string
	AllocatedBytes int64
	AllocatedKnown bool
	Verified       bool
	Protected      bool
	Pinned         bool
	BlockingReason string
}

// FileCapacityBudget contains independently evaluated optional ceilings.
type FileCapacityBudget struct {
	maxAge            types.Optional[time.Duration]
	maxCount          types.Optional[int]
	maxAllocatedBytes types.Optional[int64]
}

// FileCapacityBudgetParams configures optional capacity ceilings.
type FileCapacityBudgetParams struct {
	MaxAge            types.Optional[time.Duration]
	MaxCount          types.Optional[int]
	MaxAllocatedBytes types.Optional[int64]
}

// FileRetentionCandidate is one policy-approved file identity and all reasons.
type FileRetentionCandidate struct {
	entry   FileRetentionEntry
	reasons []string
}

// FileRetentionCeilingResult is one observable ceiling projection.
type FileRetentionCeilingResult struct {
	ceiling   string
	current   int64
	projected int64
}

// FileRetentionDecision is the complete deterministic policy result.
type FileRetentionDecision struct {
	status     string
	candidates []FileRetentionCandidate
	ceilings   []FileRetentionCeilingResult
}

// NewFileRetentionEntry validates and restores one inventory entry.
func NewFileRetentionEntry(params FileRetentionEntryParams) (FileRetentionEntry, error) {
	if params.Identity == "" || params.RelativePath == "" || params.CreatedAt.IsZero() || params.ContentDigest == "" {
		return FileRetentionEntry{}, xerrors.New("file retention identity, path, timestamp, and digest are required")
	}
	if params.AllocatedBytes < 0 {
		return FileRetentionEntry{}, xerrors.New("file retention allocated bytes must not be negative")
	}
	return FileRetentionEntry{
		identity:       params.Identity,
		relativePath:   params.RelativePath,
		createdAt:      params.CreatedAt.UTC(),
		generation:     params.Generation,
		contentDigest:  params.ContentDigest,
		allocatedBytes: params.AllocatedBytes,
		allocatedKnown: params.AllocatedKnown,
		verified:       params.Verified,
		protected:      params.Protected,
		pinned:         params.Pinned,
		blockingReason: params.BlockingReason,
	}, nil
}

// NewFileCapacityBudget validates configured ceilings.
func NewFileCapacityBudget(params FileCapacityBudgetParams) (FileCapacityBudget, error) {
	if value, ok := params.MaxAge.Value(); ok && value < 0 {
		return FileCapacityBudget{}, xerrors.New("file retention max age must not be negative")
	}
	if value, ok := params.MaxCount.Value(); ok && value < 0 {
		return FileCapacityBudget{}, xerrors.New("file retention max count must not be negative")
	}
	if value, ok := params.MaxAllocatedBytes.Value(); ok && value < 0 {
		return FileCapacityBudget{}, xerrors.New("file retention allocated-byte ceiling must not be negative")
	}
	return FileCapacityBudget{
		maxAge:            params.MaxAge,
		maxCount:          params.MaxCount,
		maxAllocatedBytes: params.MaxAllocatedBytes,
	}, nil
}

// DecideFileRetention evaluates all ceilings against the original inventory.
func DecideFileRetention(entries []FileRetentionEntry, budget FileCapacityBudget, planTime time.Time) FileRetentionDecision {
	ordered := append([]FileRetentionEntry(nil), entries...)
	sort.Slice(ordered, func(i, j int) bool { return fileRetentionLess(ordered[i], ordered[j]) })

	if hasIndeterminateInventory(ordered, budget) {
		return FileRetentionDecision{status: "indeterminate", ceilings: evaluateFileCeilings(ordered, nil, budget, planTime)}
	}

	reasonsByIdentity := make(map[string]map[string]struct{}, len(ordered))
	if maxAge, ok := budget.maxAge.Value(); ok {
		cutoff := planTime.UTC().Add(-maxAge)
		for _, entry := range ordered {
			if entry.createdAt.Before(cutoff) && entry.eligible() {
				addFileRetentionReason(reasonsByIdentity, entry.identity, "age")
			}
		}
	}
	if maxCount, ok := budget.maxCount.Value(); ok {
		remaining := len(ordered)
		for _, entry := range ordered {
			if remaining <= maxCount {
				break
			}
			if !entry.eligible() {
				continue
			}
			addFileRetentionReason(reasonsByIdentity, entry.identity, "count")
			remaining--
		}
	}
	if maxBytes, ok := budget.maxAllocatedBytes.Value(); ok {
		remaining := sumAllocatedBytes(ordered, nil)
		for _, entry := range ordered {
			if remaining <= maxBytes {
				break
			}
			if !entry.eligible() {
				continue
			}
			addFileRetentionReason(reasonsByIdentity, entry.identity, "allocated_bytes")
			remaining -= entry.allocatedBytes
		}
	}

	selected := make(map[string]struct{}, len(reasonsByIdentity))
	candidates := make([]FileRetentionCandidate, 0, len(reasonsByIdentity))
	for _, entry := range ordered {
		reasonSet, ok := reasonsByIdentity[entry.identity]
		if !ok {
			continue
		}
		selected[entry.identity] = struct{}{}
		candidates = append(candidates, FileRetentionCandidate{entry: entry, reasons: orderedFileRetentionReasons(reasonSet)})
	}
	ceilings := evaluateFileCeilings(ordered, selected, budget, planTime)
	for _, ceiling := range ceilings {
		if ceiling.projected > fileRetentionCeilingLimit(ceiling.ceiling, budget) {
			return FileRetentionDecision{status: "unsatisfied", ceilings: ceilings}
		}
	}
	return FileRetentionDecision{status: "satisfied", candidates: candidates, ceilings: ceilings}
}

func (entry FileRetentionEntry) eligible() bool {
	return entry.verified && !entry.protected && !entry.pinned && entry.blockingReason == ""
}

func fileRetentionLess(left, right FileRetentionEntry) bool {
	if !left.createdAt.Equal(right.createdAt) {
		return left.createdAt.Before(right.createdAt)
	}
	if left.generation != right.generation {
		return left.generation < right.generation
	}
	if left.contentDigest != right.contentDigest {
		return left.contentDigest < right.contentDigest
	}
	return left.relativePath < right.relativePath
}

func hasIndeterminateInventory(entries []FileRetentionEntry, budget FileCapacityBudget) bool {
	_, byteCeiling := budget.maxAllocatedBytes.Value()
	for _, entry := range entries {
		if entry.blockingReason != "" || (byteCeiling && !entry.allocatedKnown) {
			return true
		}
	}
	return false
}

func addFileRetentionReason(values map[string]map[string]struct{}, identity, reason string) {
	if values[identity] == nil {
		values[identity] = make(map[string]struct{}, 3)
	}
	values[identity][reason] = struct{}{}
}

func orderedFileRetentionReasons(values map[string]struct{}) []string {
	result := make([]string, 0, len(values))
	for _, reason := range []string{"age", "count", "allocated_bytes"} {
		if _, ok := values[reason]; ok {
			result = append(result, reason)
		}
	}
	return result
}

func evaluateFileCeilings(entries []FileRetentionEntry, selected map[string]struct{}, budget FileCapacityBudget, planTime time.Time) []FileRetentionCeilingResult {
	result := make([]FileRetentionCeilingResult, 0, 3)
	if maxAge, ok := budget.maxAge.Value(); ok {
		cutoff := planTime.UTC().Add(-maxAge)
		current, projected := int64(0), int64(0)
		for _, entry := range entries {
			if entry.createdAt.Before(cutoff) {
				current++
				if _, removed := selected[entry.identity]; !removed {
					projected++
				}
			}
		}
		result = append(result, FileRetentionCeilingResult{ceiling: "age", current: current, projected: projected})
	}
	if _, ok := budget.maxCount.Value(); ok {
		result = append(result, FileRetentionCeilingResult{ceiling: "count", current: int64(len(entries)), projected: int64(retainedEntryCount(entries, selected))})
	}
	if _, ok := budget.maxAllocatedBytes.Value(); ok {
		result = append(result, FileRetentionCeilingResult{ceiling: "allocated_bytes", current: sumAllocatedBytes(entries, nil), projected: sumAllocatedBytes(entries, selected)})
	}
	return result
}

func retainedEntryCount(entries []FileRetentionEntry, selected map[string]struct{}) int {
	count := 0
	for _, entry := range entries {
		if _, removed := selected[entry.identity]; !removed {
			count++
		}
	}
	return count
}

func sumAllocatedBytes(entries []FileRetentionEntry, selected map[string]struct{}) int64 {
	var total int64
	for _, entry := range entries {
		if _, removed := selected[entry.identity]; removed {
			continue
		}
		total += entry.allocatedBytes
	}
	return total
}

func fileRetentionCeilingLimit(ceiling string, budget FileCapacityBudget) int64 {
	switch ceiling {
	case "age":
		return 0
	case "count":
		value, _ := budget.maxCount.Value()
		return int64(value)
	case "allocated_bytes":
		value, _ := budget.maxAllocatedBytes.Value()
		return value
	default:
		return -1
	}
}

// Identity returns the exact file identity.
func (entry FileRetentionEntry) Identity() string { return entry.identity }

// RelativePath returns the root-relative file path.
func (entry FileRetentionEntry) RelativePath() string { return entry.relativePath }

// CreatedAt returns the generation timestamp.
func (entry FileRetentionEntry) CreatedAt() time.Time { return entry.createdAt }

// Generation returns the verified source generation.
func (entry FileRetentionEntry) Generation() string { return entry.generation }

// ContentDigest returns the file SHA-256 digest.
func (entry FileRetentionEntry) ContentDigest() string { return entry.contentDigest }

// AllocatedBytes returns filesystem accounting bytes.
func (entry FileRetentionEntry) AllocatedBytes() int64 { return entry.allocatedBytes }

// AllocatedKnown reports whether allocation evidence is complete.
func (entry FileRetentionEntry) AllocatedKnown() bool { return entry.allocatedKnown }

// Verified reports whether the recovery artifact passed verification.
func (entry FileRetentionEntry) Verified() bool { return entry.verified }

// Protected reports whether the entry is the current recovery floor.
func (entry FileRetentionEntry) Protected() bool { return entry.protected }

// Pinned reports whether an operator pin blocks deletion.
func (entry FileRetentionEntry) Pinned() bool { return entry.pinned }

// BlockingReason returns fail-closed inventory evidence.
func (entry FileRetentionEntry) BlockingReason() string { return entry.blockingReason }

// Entry returns the exact selected inventory entry.
func (candidate FileRetentionCandidate) Entry() FileRetentionEntry { return candidate.entry }

// Reasons returns a defensive copy in canonical order.
func (candidate FileRetentionCandidate) Reasons() []string {
	return append([]string(nil), candidate.reasons...)
}

// Status returns satisfied, unsatisfied, or indeterminate.
func (decision FileRetentionDecision) Status() string { return decision.status }

// Candidates returns a defensive copy in canonical order.
func (decision FileRetentionDecision) Candidates() []FileRetentionCandidate {
	return append([]FileRetentionCandidate(nil), decision.candidates...)
}

// Ceilings returns the observable ceiling projections.
func (decision FileRetentionDecision) Ceilings() []FileRetentionCeilingResult {
	return append([]FileRetentionCeilingResult(nil), decision.ceilings...)
}

// Ceiling returns the canonical ceiling name.
func (result FileRetentionCeilingResult) Ceiling() string { return result.ceiling }

// Current returns the original inventory measure.
func (result FileRetentionCeilingResult) Current() int64 { return result.current }

// Projected returns the retained inventory measure.
func (result FileRetentionCeilingResult) Projected() int64 { return result.projected }
