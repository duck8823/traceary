package types

import (
	"slices"

	"github.com/duck8823/traceary/application/redaction"
)

// LogRedaction holds redaction settings for event logging via
// EventUsecase.Log. It mirrors AuditRedaction for the Audit path so
// every log-ingest surface (CLI `traceary log`, transcript hook, MCP
// add_log) carries the same policy shape and the usecase
// implementation can apply redaction once on behalf of all callers.
//
// The zero value is intentionally a pass-through: no extra patterns,
// matching today's behaviour for non-transcript kinds.
type LogRedaction struct {
	extraRedactPatterns []string
	structuredRules     []redaction.RuleConfig
}

// ExtraRedactPatterns returns additional redaction regex patterns.
func (r LogRedaction) ExtraRedactPatterns() []string {
	return slices.Clone(r.extraRedactPatterns)
}

// StructuredRules returns configured structured redaction rules.
func (r LogRedaction) StructuredRules() []redaction.RuleConfig {
	return slices.Clone(r.structuredRules)
}

// LogRedactionBuilder builds a LogRedaction value.
type LogRedactionBuilder struct {
	redaction LogRedaction
}

// NewLogRedactionBuilder starts building an empty LogRedaction.
func NewLogRedactionBuilder() *LogRedactionBuilder {
	return &LogRedactionBuilder{}
}

// ExtraRedactPatterns sets additional redaction regex patterns.
func (b *LogRedactionBuilder) ExtraRedactPatterns(patterns []string) *LogRedactionBuilder {
	b.redaction.extraRedactPatterns = slices.Clone(patterns)
	return b
}

// StructuredRules sets structured redaction rules.
func (b *LogRedactionBuilder) StructuredRules(rules []redaction.RuleConfig) *LogRedactionBuilder {
	b.redaction.structuredRules = slices.Clone(rules)
	return b
}

// Build finalizes and returns the LogRedaction.
func (b *LogRedactionBuilder) Build() LogRedaction {
	return b.redaction
}
