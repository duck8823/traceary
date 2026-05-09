package usecase

import (
	"context"
	"time"

	apptypes "github.com/duck8823/traceary/application/types"
	domtypes "github.com/duck8823/traceary/domain/types"
)

// MemoryUsecase consolidates durable-memory lifecycle, query, capture, hygiene,
// and export operations.
type MemoryUsecase interface {
	// Lifecycle
	// Remember records an accepted memory directly.
	Remember(
		ctx context.Context,
		memoryType domtypes.MemoryType,
		scope domtypes.MemoryScope,
		fact string,
		confidence domtypes.Optional[domtypes.Confidence],
		source domtypes.MemorySource,
		evidenceRefs []domtypes.EvidenceRef,
		artifactRefs []domtypes.ArtifactRef,
	) (apptypes.MemoryDetails, error)

	// Propose records a candidate memory that still requires review.
	Propose(
		ctx context.Context,
		memoryType domtypes.MemoryType,
		scope domtypes.MemoryScope,
		fact string,
		source domtypes.MemorySource,
		evidenceRefs []domtypes.EvidenceRef,
		artifactRefs []domtypes.ArtifactRef,
	) (apptypes.MemoryDetails, error)

	// Accept accepts an existing candidate memory.
	Accept(ctx context.Context, memoryID domtypes.MemoryID, confidence domtypes.Optional[domtypes.Confidence]) (apptypes.MemoryDetails, error)

	// Distill turns one or more candidate memories into a new accepted
	// operator-provided memory while preserving source refs.
	Distill(ctx context.Context, criteria apptypes.MemoryDistillCriteria) (apptypes.MemoryDistillResult, error)

	// Reject rejects an existing candidate memory.
	Reject(ctx context.Context, memoryID domtypes.MemoryID) (apptypes.MemoryDetails, error)

	// Supersede replaces an accepted memory with a new accepted memory.
	// validFrom / validTo control the replacement's temporal validity
	// window. Both default to the legacy behaviour (validFrom=now,
	// validTo=open-ended) when None, which keeps manual supersede
	// compatible with pre-v0.8.1 callers. Callers that need to carry
	// a bounded window over to the replacement (e.g. the hygiene
	// `validity_overlap_supersede` apply) must pass the explicit
	// window through — without this, applying the suggestion would
	// discard the very window that caused the pair to be flagged.
	Supersede(
		ctx context.Context,
		memoryID domtypes.MemoryID,
		memoryType domtypes.MemoryType,
		scope domtypes.MemoryScope,
		fact string,
		confidence domtypes.Optional[domtypes.Confidence],
		source domtypes.MemorySource,
		evidenceRefs []domtypes.EvidenceRef,
		artifactRefs []domtypes.ArtifactRef,
		validFrom domtypes.Optional[time.Time],
		validTo domtypes.Optional[time.Time],
	) (apptypes.MemoryDetails, error)

	// Expire expires an active memory at the given time. Empty expiry means now.
	Expire(ctx context.Context, memoryID domtypes.MemoryID, expiresAt domtypes.Optional[time.Time]) (apptypes.MemoryDetails, error)

	// SetValidity sets the content-validity window (valid_from / valid_to)
	// on an existing memory. Either bound may be omitted to leave the
	// current value unchanged. Set clearValidTo=true to explicitly
	// remove an existing validTo (return the memory to open-ended
	// validity). clearValidTo=true with a non-empty validTo argument
	// is invalid.
	SetValidity(
		ctx context.Context,
		memoryID domtypes.MemoryID,
		validFrom domtypes.Optional[time.Time],
		validTo domtypes.Optional[time.Time],
		clearValidTo bool,
	) (apptypes.MemoryDetails, error)

	// Query

	// List returns memory summaries matching the criteria.
	List(ctx context.Context, criteria apptypes.MemoryListCriteria) ([]apptypes.MemorySummary, error)

	// ListStale returns stale durable-memory rows and the total count before
	// paging. It is a read-side helper for the top dashboard's stale-memory
	// pane.
	ListStale(ctx context.Context, criteria apptypes.StaleMemoryListCriteria) (apptypes.StaleMemoryListResult, error)

	// Search searches durable memories.
	Search(ctx context.Context, criteria apptypes.MemorySearchCriteria) ([]apptypes.MemorySummary, error)

	// Show returns the details for a single durable memory.
	Show(ctx context.Context, memoryID domtypes.MemoryID) (apptypes.MemoryDetails, error)

	// Capture

	// Extract proposes candidate memories from existing session/history signals.
	Extract(ctx context.Context, criteria apptypes.MemoryExtractionCriteria) ([]apptypes.MemoryDetails, error)

	// ExplainExtraction returns segment-level extraction diagnostics without proposing candidates.
	ExplainExtraction(ctx context.Context, criteria apptypes.MemoryExtractionCriteria) (apptypes.MemoryExtractionDebugReport, error)

	// ImportCodex proposes candidate memories from host-native Codex memory sources.
	ImportCodex(ctx context.Context, criteria apptypes.CodexImportCriteria) (apptypes.MemoryImportResult, error)

	// ImportInstructions proposes candidate memories from host instruction files.
	ImportInstructions(ctx context.Context, criteria apptypes.MemoryBridgeImportCriteria) (apptypes.MemoryBridgeImportResult, error)

	// Hygiene

	// Scan surfaces suggestions for accepted durable memories that need attention.
	Scan(ctx context.Context, criteria apptypes.MemoryHygieneScanCriteria) (apptypes.MemoryHygieneScanResult, error)

	// Apply commits lifecycle transitions for matching hygiene suggestions.
	Apply(ctx context.Context, criteria apptypes.MemoryHygieneApplyCriteria) (apptypes.MemoryHygieneApplyResult, error)

	// Export

	// Export serializes accepted durable memories into host instruction markdown.
	Export(ctx context.Context, criteria apptypes.MemoryExportCriteria) (apptypes.MemoryExportResult, error)

	// ActivatePlan resolves a host-native activation target and renders the dry-run content.
	ActivatePlan(ctx context.Context, criteria apptypes.MemoryActivationCriteria) (apptypes.MemoryActivationPlan, error)

	// Activate writes accepted durable memories into a host-native activation target.
	Activate(ctx context.Context, criteria apptypes.MemoryActivationCriteria) (apptypes.MemoryActivationApplyResult, error)

	// ActivationStatus compares the host-native activation target with the current accepted memories.
	ActivationStatus(ctx context.Context, criteria apptypes.MemoryActivationCriteria) (apptypes.MemoryActivationStatusResult, error)
}

type memoryProposer interface {
	Propose(
		ctx context.Context,
		memoryType domtypes.MemoryType,
		scope domtypes.MemoryScope,
		fact string,
		source domtypes.MemorySource,
		evidenceRefs []domtypes.EvidenceRef,
		artifactRefs []domtypes.ArtifactRef,
	) (apptypes.MemoryDetails, error)
}

type memoryExtractionWriter interface {
	memoryProposer
	List(ctx context.Context, criteria apptypes.MemoryListCriteria) ([]apptypes.MemorySummary, error)
}

type memoryHygieneWriter interface {
	Show(ctx context.Context, memoryID domtypes.MemoryID) (apptypes.MemoryDetails, error)
	Supersede(
		ctx context.Context,
		memoryID domtypes.MemoryID,
		memoryType domtypes.MemoryType,
		scope domtypes.MemoryScope,
		fact string,
		confidence domtypes.Optional[domtypes.Confidence],
		source domtypes.MemorySource,
		evidenceRefs []domtypes.EvidenceRef,
		artifactRefs []domtypes.ArtifactRef,
		validFrom domtypes.Optional[time.Time],
		validTo domtypes.Optional[time.Time],
	) (apptypes.MemoryDetails, error)
	Expire(ctx context.Context, memoryID domtypes.MemoryID, expiresAt domtypes.Optional[time.Time]) (apptypes.MemoryDetails, error)
	Reject(ctx context.Context, memoryID domtypes.MemoryID) (apptypes.MemoryDetails, error)
}
