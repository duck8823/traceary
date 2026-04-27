package types

import domtypes "github.com/duck8823/traceary/domain/types"

// MemoryExtractionDebugReport explains how durable-memory extraction evaluated
// each inspected signal segment. It is intentionally read-only: callers use it
// for dogfooding and false-negative analysis without creating candidates.
type MemoryExtractionDebugReport struct {
	SessionID domtypes.SessionID
	Workspace domtypes.Workspace
	Segments  []MemoryExtractionSegmentDecision
}

// MemoryExtractionSegmentDecision captures the classifier features and final
// extraction decision for one normalized signal segment.
type MemoryExtractionSegmentDecision struct {
	Text         string
	Client       domtypes.Client
	EventKind    domtypes.EventKind
	SourceHook   string
	MemoryType   domtypes.MemoryType
	Features     []string
	Score        int
	Decision     string
	Reason       string
	EvidenceRefs []domtypes.EvidenceRef
	ArtifactRefs []domtypes.ArtifactRef
}
