package types

import (
	"strings"

	"golang.org/x/xerrors"

	domtypes "github.com/duck8823/traceary/domain/types"
)

// MemoryRetrievalPreset names a canonical retrieval shape over durable
// memory. Presets pre-populate a MemoryListCriteriaBuilder with the
// filters that match a specific operator scenario (resume / review /
// incident), and the caller can still layer explicit filters on top.
type MemoryRetrievalPreset string

const (
	// MemoryRetrievalPresetResume surfaces memories operators want when
	// picking up where a session left off: accepted memories ordered
	// newest-first, no type restriction so recent lessons /
	// constraints / preferences all surface together.
	MemoryRetrievalPresetResume MemoryRetrievalPreset = "resume"

	// MemoryRetrievalPresetReview focuses on long-lived "what did we
	// decide" knowledge: only accepted decisions and constraints, no
	// preferences or one-off lessons.
	MemoryRetrievalPresetReview MemoryRetrievalPreset = "review"

	// MemoryRetrievalPresetIncident surfaces what an operator needs to
	// know when a failure just happened: accepted lessons and
	// constraints (the "what to avoid" axis) plus decisions that might
	// explain why a contested path exists.
	MemoryRetrievalPresetIncident MemoryRetrievalPreset = "incident"
)

// MemoryRetrievalPresetOf parses a free-form string into a known
// preset. Empty input returns "" (no preset applied) so callers can
// pass a user-supplied flag value through without special-casing.
func MemoryRetrievalPresetOf(value string) (MemoryRetrievalPreset, error) {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return "", nil
	}
	switch MemoryRetrievalPreset(trimmed) {
	case MemoryRetrievalPresetResume, MemoryRetrievalPresetReview, MemoryRetrievalPresetIncident:
		return MemoryRetrievalPreset(trimmed), nil
	}
	return "", xerrors.Errorf(
		"unknown memory retrieval preset: %q (allowed values: %s)",
		trimmed,
		strings.Join(knownMemoryRetrievalPresetStrings(), ", "),
	)
}

// KnownMemoryRetrievalPresets returns the exhaustive list of built-in
// presets, in the order CLI help and docs should render them.
func KnownMemoryRetrievalPresets() []MemoryRetrievalPreset {
	return []MemoryRetrievalPreset{
		MemoryRetrievalPresetResume,
		MemoryRetrievalPresetReview,
		MemoryRetrievalPresetIncident,
	}
}

func knownMemoryRetrievalPresetStrings() []string {
	presets := KnownMemoryRetrievalPresets()
	out := make([]string, 0, len(presets))
	for _, p := range presets {
		out = append(out, string(p))
	}
	return out
}

// String returns the canonical name of the preset.
func (p MemoryRetrievalPreset) String() string { return string(p) }

// presetFilters is the internal representation of what a preset
// contributes to a criteria builder. Keeping this as an intermediate
// value lets us share the resume / review / incident definitions
// between MemoryListCriteriaBuilder and MemorySearchCriteriaBuilder
// without duplicating the switch on preset.
type presetFilters struct {
	statuses    []domtypes.MemoryStatus
	memoryTypes []domtypes.MemoryType
}

func (p MemoryRetrievalPreset) filters() presetFilters {
	switch p {
	case MemoryRetrievalPresetResume:
		return presetFilters{
			statuses: []domtypes.MemoryStatus{domtypes.MemoryStatusAccepted},
		}
	case MemoryRetrievalPresetReview:
		return presetFilters{
			statuses: []domtypes.MemoryStatus{domtypes.MemoryStatusAccepted},
			memoryTypes: []domtypes.MemoryType{
				domtypes.MemoryTypeDecision,
				domtypes.MemoryTypeConstraint,
				domtypes.MemoryTypeArtifact,
			},
		}
	case MemoryRetrievalPresetIncident:
		return presetFilters{
			statuses: []domtypes.MemoryStatus{domtypes.MemoryStatusAccepted},
			memoryTypes: []domtypes.MemoryType{
				domtypes.MemoryTypeDecision,
				domtypes.MemoryTypeConstraint,
				domtypes.MemoryTypeLesson,
				domtypes.MemoryTypeArtifact,
			},
		}
	}
	return presetFilters{}
}

// ApplyToMemoryListCriteriaBuilder pre-populates the builder with the
// preset's default filters. Explicit filters the caller sets after
// calling this method override the preset's defaults — the preset is
// purely a convenience starting point, not a ceiling.
//
// Design choices:
//
//   - `resume` does not restrict memoryTypes because the scenario is
//     "what was I working on" — preferences and lessons are as useful
//     as decisions. It only pins statuses=[accepted].
//   - `review` narrows to decision + constraint because those are the
//     types operators expect to reread.
//   - `incident` includes lesson + constraint + decision so the
//     "what did we learn" / "what must not happen" / "why is this
//     state here" axes all surface together.
//
// Callers pass a MemoryListCriteriaBuilder to keep the dependency
// direction domain-free; no MemoryListCriteria allocation is returned
// directly because the caller typically still needs to chain Limit(),
// Scopes(), AsOf(), etc.
func (p MemoryRetrievalPreset) ApplyToMemoryListCriteriaBuilder(builder *MemoryListCriteriaBuilder) *MemoryListCriteriaBuilder {
	if builder == nil {
		return builder
	}
	filters := p.filters()
	if len(filters.statuses) > 0 {
		builder = builder.Statuses(filters.statuses)
	}
	if len(filters.memoryTypes) > 0 {
		builder = builder.MemoryTypes(filters.memoryTypes)
	}
	return builder
}

// ApplyMemoryTypeFiltersToMemoryListCriteriaBuilder applies only the
// preset's type restrictions, leaving lifecycle status untouched. This
// lets review-oriented surfaces ask for candidate memories in a separate
// section while preserving the same resume / review / incident memory
// type shape as the trusted accepted section.
func (p MemoryRetrievalPreset) ApplyMemoryTypeFiltersToMemoryListCriteriaBuilder(builder *MemoryListCriteriaBuilder) *MemoryListCriteriaBuilder {
	if builder == nil {
		return builder
	}
	filters := p.filters()
	if len(filters.memoryTypes) > 0 {
		builder = builder.MemoryTypes(filters.memoryTypes)
	}
	return builder
}

// ApplyToMemorySearchCriteriaBuilder is the search-side counterpart
// to ApplyToMemoryListCriteriaBuilder. Same defaults, same override
// semantics.
func (p MemoryRetrievalPreset) ApplyToMemorySearchCriteriaBuilder(builder *MemorySearchCriteriaBuilder) *MemorySearchCriteriaBuilder {
	if builder == nil {
		return builder
	}
	filters := p.filters()
	if len(filters.statuses) > 0 {
		builder = builder.Statuses(filters.statuses)
	}
	if len(filters.memoryTypes) > 0 {
		builder = builder.MemoryTypes(filters.memoryTypes)
	}
	return builder
}
