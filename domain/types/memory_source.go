package types

import (
	"slices"
	"strings"

	"golang.org/x/xerrors"
)

// MemorySource describes how a durable memory was produced.
type MemorySource string

const (
	// MemorySourceManual indicates a manually entered memory.
	MemorySourceManual MemorySource = "manual"
	// MemorySourceExtracted indicates a memory extracted from existing signals.
	MemorySourceExtracted MemorySource = "extracted"
	// MemorySourceExtractedHidden indicates a memory extracted from existing
	// signals that did not pass the quality filter. The row is kept in the
	// store for audit and `--include-hidden` review, but is omitted from the
	// default inbox view so reviewers do not get drowned by low-quality
	// candidates.
	MemorySourceExtractedHidden MemorySource = "extracted-hidden"
	// MemorySourceRememberIntent indicates a candidate produced from an
	// explicit user remember-intent prompt (e.g. "remember this:", "覚えて
	// おいて") or from the immediately adjacent context of a short
	// remember-intent confirmation. The candidate stays reviewable
	// (status=candidate) and is prioritized above generic extracted
	// candidates in the default inbox view.
	MemorySourceRememberIntent MemorySource = "remember-intent"
	// MemorySourceCompactSummary indicates a candidate produced from a
	// meaningful compact_summary event, especially a post_compact digest.
	// It remains a candidate so operators can review condensed session
	// memories before accepting them.
	MemorySourceCompactSummary MemorySource = "compact-summary"
	// MemorySourceImported indicates a memory imported from another source.
	MemorySourceImported MemorySource = "imported"
)

var knownMemorySources = []MemorySource{
	MemorySourceManual,
	MemorySourceExtracted,
	MemorySourceExtractedHidden,
	MemorySourceRememberIntent,
	MemorySourceCompactSummary,
	MemorySourceImported,
}

// MemorySourceFrom creates a MemorySource from a string.
func MemorySourceFrom(value string) (MemorySource, error) {
	trimmedValue := strings.TrimSpace(value)
	if trimmedValue == "" {
		return MemorySource(""), xerrors.Errorf("memory source must not be empty")
	}
	if slices.Contains(knownMemorySources, MemorySource(trimmedValue)) {
		return MemorySource(trimmedValue), nil
	}
	return MemorySource(""), xerrors.Errorf("unknown memory source: %s", trimmedValue)
}

// String returns the string representation.
func (m MemorySource) String() string { return string(m) }
