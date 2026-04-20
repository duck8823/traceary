package usecase

import (
	"context"
	"time"

	apptypes "github.com/duck8823/traceary/application/types"
	domtypes "github.com/duck8823/traceary/domain/types"
)

// MemoryUsecase consolidates durable-memory lifecycle and query operations.
type MemoryUsecase interface {
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

	// Reject rejects an existing candidate memory.
	Reject(ctx context.Context, memoryID domtypes.MemoryID) (apptypes.MemoryDetails, error)

	// Supersede replaces an accepted memory with a new accepted memory.
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

	// List returns memory summaries matching the criteria.
	List(ctx context.Context, criteria apptypes.MemoryListCriteria) ([]apptypes.MemorySummary, error)

	// Search searches durable memories.
	Search(ctx context.Context, criteria apptypes.MemorySearchCriteria) ([]apptypes.MemorySummary, error)

	// Show returns the details for a single durable memory.
	Show(ctx context.Context, memoryID domtypes.MemoryID) (apptypes.MemoryDetails, error)
}
