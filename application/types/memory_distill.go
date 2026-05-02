package types

import (
	"slices"

	domtypes "github.com/duck8823/traceary/domain/types"
)

// MemoryDistillReplace controls what happens to source candidates after
// distillation creates the accepted memory.
type MemoryDistillReplace string

const (
	// MemoryDistillReplaceKeep leaves source candidates unchanged after distillation.
	MemoryDistillReplaceKeep MemoryDistillReplace = "keep"
	// MemoryDistillReplaceReject rejects source candidates after distillation.
	MemoryDistillReplaceReject MemoryDistillReplace = "reject"
	// MemoryDistillReplaceSupersede marks source candidates as superseded after distillation.
	MemoryDistillReplaceSupersede MemoryDistillReplace = "supersede"
)

// String returns the stable CLI / JSON value.
func (r MemoryDistillReplace) String() string { return string(r) }

// MemoryDistillCriteria describes an explicit operator distillation of one or
// more candidate memories into a new accepted memory.
type MemoryDistillCriteria struct {
	fromIDs    []domtypes.MemoryID
	memoryType domtypes.MemoryType
	scope      domtypes.MemoryScope
	fact       string
	confidence domtypes.Optional[domtypes.Confidence]
	source     domtypes.MemorySource
	replace    MemoryDistillReplace
}

// MemoryDistillCriteriaOf creates distillation criteria.
func MemoryDistillCriteriaOf(
	fromIDs []domtypes.MemoryID,
	memoryType domtypes.MemoryType,
	scope domtypes.MemoryScope,
	fact string,
	confidence domtypes.Optional[domtypes.Confidence],
	source domtypes.MemorySource,
	replace MemoryDistillReplace,
) MemoryDistillCriteria {
	return MemoryDistillCriteria{
		fromIDs:    slices.Clone(fromIDs),
		memoryType: memoryType,
		scope:      scope,
		fact:       fact,
		confidence: confidence,
		source:     source,
		replace:    replace,
	}
}

// FromIDs returns the source candidate IDs.
func (c MemoryDistillCriteria) FromIDs() []domtypes.MemoryID { return slices.Clone(c.fromIDs) }

// MemoryType returns the accepted memory type to create.
func (c MemoryDistillCriteria) MemoryType() domtypes.MemoryType { return c.memoryType }

// Scope returns the accepted memory scope to create.
func (c MemoryDistillCriteria) Scope() domtypes.MemoryScope { return c.scope }

// Fact returns the operator-provided distilled fact.
func (c MemoryDistillCriteria) Fact() string { return c.fact }

// Confidence returns the accepted confidence override.
func (c MemoryDistillCriteria) Confidence() domtypes.Optional[domtypes.Confidence] {
	return c.confidence
}

// Source returns the source attribution for the accepted memory.
func (c MemoryDistillCriteria) Source() domtypes.MemorySource { return c.source }

// Replace returns how source candidates should be handled.
func (c MemoryDistillCriteria) Replace() MemoryDistillReplace { return c.replace }

// MemoryDistillResult reports the accepted memory and resulting source states.
type MemoryDistillResult struct {
	distilled MemoryDetails
	sources   []MemorySummary
	replace   MemoryDistillReplace
}

// MemoryDistillResultOf creates a distillation result.
func MemoryDistillResultOf(distilled MemoryDetails, sources []MemorySummary, replace MemoryDistillReplace) MemoryDistillResult {
	return MemoryDistillResult{
		distilled: distilled,
		sources:   slices.Clone(sources),
		replace:   replace,
	}
}

// Distilled returns the accepted memory created by distillation.
func (r MemoryDistillResult) Distilled() MemoryDetails { return r.distilled }

// Sources returns source candidate summaries after replacement handling.
func (r MemoryDistillResult) Sources() []MemorySummary { return slices.Clone(r.sources) }

// Replace returns the source replacement policy that was applied.
func (r MemoryDistillResult) Replace() MemoryDistillReplace { return r.replace }
