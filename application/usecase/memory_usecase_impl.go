package usecase

import (
	"context"
	"slices"
	"strings"
	"time"

	"golang.org/x/xerrors"

	"github.com/duck8823/traceary/application/queryservice"
	"github.com/duck8823/traceary/application/redaction"
	apptypes "github.com/duck8823/traceary/application/types"
	"github.com/duck8823/traceary/domain/model"
	domtypes "github.com/duck8823/traceary/domain/types"
)

type memoryUsecase struct {
	memoryRepo          model.MemoryRepository
	memoryQuery         queryservice.MemoryQueryService
	extraRedactPatterns []string
}

// NewMemoryUsecase creates a MemoryUsecase.
func NewMemoryUsecase(
	memoryRepo model.MemoryRepository,
	memoryQuery queryservice.MemoryQueryService,
	extraRedactPatterns []string,
) MemoryUsecase {
	return &memoryUsecase{
		memoryRepo:          memoryRepo,
		memoryQuery:         memoryQuery,
		extraRedactPatterns: slices.Clone(extraRedactPatterns),
	}
}

func (u *memoryUsecase) Remember(
	ctx context.Context,
	memoryType domtypes.MemoryType,
	scope domtypes.MemoryScope,
	fact string,
	confidence domtypes.Optional[domtypes.Confidence],
	source domtypes.MemorySource,
	evidenceRefs []domtypes.EvidenceRef,
	artifactRefs []domtypes.ArtifactRef,
) (apptypes.MemoryDetails, error) {
	if u.memoryRepo == nil {
		return apptypes.MemoryDetails{}, xerrors.Errorf("memory repository is not configured")
	}

	resolvedType, err := resolveRequiredMemoryType(memoryType)
	if err != nil {
		return apptypes.MemoryDetails{}, err
	}
	resolvedScope, err := resolveRequiredMemoryScope(scope)
	if err != nil {
		return apptypes.MemoryDetails{}, err
	}
	resolvedSource, err := resolveMemorySource(source)
	if err != nil {
		return apptypes.MemoryDetails{}, err
	}
	resolvedConfidence, err := resolveAcceptedConfidence(confidence)
	if err != nil {
		return apptypes.MemoryDetails{}, err
	}
	fact, evidenceRefs, artifactRefs, err = u.sanitizeMemoryPayload(fact, evidenceRefs, artifactRefs)
	if err != nil {
		return apptypes.MemoryDetails{}, err
	}
	if err := requireAcceptedEvidenceRefs(evidenceRefs); err != nil {
		return apptypes.MemoryDetails{}, err
	}

	memoryID, err := newMemoryID()
	if err != nil {
		return apptypes.MemoryDetails{}, xerrors.Errorf("failed to generate memory ID: %w", err)
	}
	memory, err := model.NewAcceptedMemory(
		memoryID,
		resolvedType,
		resolvedScope,
		fact,
		resolvedConfidence,
		resolvedSource,
		evidenceRefs,
		artifactRefs,
		domtypes.None[domtypes.MemoryID](),
	)
	if err != nil {
		return apptypes.MemoryDetails{}, xerrors.Errorf("failed to build accepted memory: %w", err)
	}
	if err := u.memoryRepo.Save(ctx, memory); err != nil {
		return apptypes.MemoryDetails{}, xerrors.Errorf("failed to save accepted memory: %w", err)
	}

	details, err := apptypes.MemoryDetailsFrom(memory)
	if err != nil {
		return apptypes.MemoryDetails{}, xerrors.Errorf("failed to build memory details: %w", err)
	}
	return details, nil
}

func (u *memoryUsecase) Propose(
	ctx context.Context,
	memoryType domtypes.MemoryType,
	scope domtypes.MemoryScope,
	fact string,
	source domtypes.MemorySource,
	evidenceRefs []domtypes.EvidenceRef,
	artifactRefs []domtypes.ArtifactRef,
) (apptypes.MemoryDetails, error) {
	if u.memoryRepo == nil {
		return apptypes.MemoryDetails{}, xerrors.Errorf("memory repository is not configured")
	}

	resolvedType, err := resolveRequiredMemoryType(memoryType)
	if err != nil {
		return apptypes.MemoryDetails{}, err
	}
	resolvedScope, err := resolveRequiredMemoryScope(scope)
	if err != nil {
		return apptypes.MemoryDetails{}, err
	}
	resolvedSource, err := resolveMemorySource(source)
	if err != nil {
		return apptypes.MemoryDetails{}, err
	}
	fact, evidenceRefs, artifactRefs, err = u.sanitizeMemoryPayload(fact, evidenceRefs, artifactRefs)
	if err != nil {
		return apptypes.MemoryDetails{}, err
	}

	memoryID, err := newMemoryID()
	if err != nil {
		return apptypes.MemoryDetails{}, xerrors.Errorf("failed to generate memory ID: %w", err)
	}
	memory, err := model.NewMemoryCandidate(
		memoryID,
		resolvedType,
		resolvedScope,
		fact,
		resolvedSource,
		evidenceRefs,
		artifactRefs,
		domtypes.None[domtypes.MemoryID](),
	)
	if err != nil {
		return apptypes.MemoryDetails{}, xerrors.Errorf("failed to build candidate memory: %w", err)
	}
	if err := u.memoryRepo.Save(ctx, memory); err != nil {
		return apptypes.MemoryDetails{}, xerrors.Errorf("failed to save candidate memory: %w", err)
	}

	details, err := apptypes.MemoryDetailsFrom(memory)
	if err != nil {
		return apptypes.MemoryDetails{}, xerrors.Errorf("failed to build memory details: %w", err)
	}
	return details, nil
}

func (u *memoryUsecase) Accept(ctx context.Context, memoryID domtypes.MemoryID, confidence domtypes.Optional[domtypes.Confidence]) (apptypes.MemoryDetails, error) {
	if u.memoryRepo == nil {
		return apptypes.MemoryDetails{}, xerrors.Errorf("memory repository is not configured")
	}

	memory, err := u.findMemoryByID(ctx, memoryID)
	if err != nil {
		return apptypes.MemoryDetails{}, err
	}
	if err := requireAcceptedEvidenceRefs(memory.EvidenceRefs()); err != nil {
		return apptypes.MemoryDetails{}, err
	}
	resolvedConfidence, err := resolveAcceptedConfidence(confidence)
	if err != nil {
		return apptypes.MemoryDetails{}, err
	}
	if err := memory.Accept(resolvedConfidence); err != nil {
		return apptypes.MemoryDetails{}, xerrors.Errorf("failed to accept memory: %w", err)
	}
	if err := u.memoryRepo.Save(ctx, memory); err != nil {
		return apptypes.MemoryDetails{}, xerrors.Errorf("failed to save accepted memory: %w", err)
	}

	details, err := apptypes.MemoryDetailsFrom(memory)
	if err != nil {
		return apptypes.MemoryDetails{}, xerrors.Errorf("failed to build memory details: %w", err)
	}
	return details, nil
}

func (u *memoryUsecase) Reject(ctx context.Context, memoryID domtypes.MemoryID) (apptypes.MemoryDetails, error) {
	if u.memoryRepo == nil {
		return apptypes.MemoryDetails{}, xerrors.Errorf("memory repository is not configured")
	}

	memory, err := u.findMemoryByID(ctx, memoryID)
	if err != nil {
		return apptypes.MemoryDetails{}, err
	}
	if err := memory.Reject(); err != nil {
		return apptypes.MemoryDetails{}, xerrors.Errorf("failed to reject memory: %w", err)
	}
	if err := u.memoryRepo.Save(ctx, memory); err != nil {
		return apptypes.MemoryDetails{}, xerrors.Errorf("failed to save rejected memory: %w", err)
	}

	details, err := apptypes.MemoryDetailsFrom(memory)
	if err != nil {
		return apptypes.MemoryDetails{}, xerrors.Errorf("failed to build memory details: %w", err)
	}
	return details, nil
}

func (u *memoryUsecase) Supersede(
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
) (apptypes.MemoryDetails, error) {
	if u.memoryRepo == nil {
		return apptypes.MemoryDetails{}, xerrors.Errorf("memory repository is not configured")
	}

	existing, err := u.findMemoryByID(ctx, memoryID)
	if err != nil {
		return apptypes.MemoryDetails{}, err
	}

	resolvedType, err := inheritOrResolveMemoryType(memoryType, existing.MemoryType())
	if err != nil {
		return apptypes.MemoryDetails{}, err
	}
	resolvedScope, err := inheritOrResolveMemoryScope(scope, existing.Scope())
	if err != nil {
		return apptypes.MemoryDetails{}, err
	}
	resolvedSource, err := resolveMemorySource(source)
	if err != nil {
		return apptypes.MemoryDetails{}, err
	}
	resolvedConfidence, err := resolveAcceptedConfidence(confidence)
	if err != nil {
		return apptypes.MemoryDetails{}, err
	}
	fact, evidenceRefs, artifactRefs, err = u.sanitizeMemoryPayload(fact, evidenceRefs, artifactRefs)
	if err != nil {
		return apptypes.MemoryDetails{}, err
	}
	if err := requireAcceptedEvidenceRefs(evidenceRefs); err != nil {
		return apptypes.MemoryDetails{}, err
	}

	// Reject reversed validity windows up front, matching SetValidity
	// (valid_to must not be earlier than valid_from). Without this a
	// supersede caller coming in via CLI --from/--to or MCP
	// supersede_memory valid_from/valid_to could persist a replacement
	// whose window is inverted, which would then break hygiene and
	// runtime retrieval that assume monotonic window bounds. When
	// validFrom is None the replacement will be created with
	// validFrom=now (per NewAcceptedMemoryWithValidity), so fall back
	// to the wall clock for the comparison.
	if to, ok := validTo.Value(); ok {
		effectiveFrom := time.Now()
		if from, hasFrom := validFrom.Value(); hasFrom {
			effectiveFrom = from
		}
		if to.Before(effectiveFrom) {
			return apptypes.MemoryDetails{}, xerrors.Errorf("valid_to must not be earlier than valid_from")
		}
	}

	newMemoryID, err := newMemoryID()
	if err != nil {
		return apptypes.MemoryDetails{}, xerrors.Errorf("failed to generate replacement memory ID: %w", err)
	}
	memory, err := model.NewAcceptedMemoryWithValidity(
		newMemoryID,
		resolvedType,
		resolvedScope,
		fact,
		resolvedConfidence,
		resolvedSource,
		evidenceRefs,
		artifactRefs,
		domtypes.Some(existing.MemoryID()),
		validFrom,
		validTo,
	)
	if err != nil {
		return apptypes.MemoryDetails{}, xerrors.Errorf("failed to build replacement memory: %w", err)
	}
	if err := existing.MarkSuperseded(); err != nil {
		return apptypes.MemoryDetails{}, xerrors.Errorf("failed to supersede existing memory: %w", err)
	}
	if err := u.memoryRepo.SaveSupersession(ctx, existing, memory); err != nil {
		return apptypes.MemoryDetails{}, xerrors.Errorf("failed to save supersession: %w", err)
	}

	details, err := apptypes.MemoryDetailsFrom(memory)
	if err != nil {
		return apptypes.MemoryDetails{}, xerrors.Errorf("failed to build memory details: %w", err)
	}
	return details, nil
}

func (u *memoryUsecase) SetValidity(
	ctx context.Context,
	memoryID domtypes.MemoryID,
	validFrom domtypes.Optional[time.Time],
	validTo domtypes.Optional[time.Time],
	clearValidTo bool,
) (apptypes.MemoryDetails, error) {
	if u.memoryRepo == nil {
		return apptypes.MemoryDetails{}, xerrors.Errorf("memory repository is not configured")
	}
	if clearValidTo {
		if _, supplied := validTo.Value(); supplied {
			return apptypes.MemoryDetails{}, xerrors.Errorf("clearValidTo cannot be combined with a validTo value")
		}
	}

	memory, err := u.findMemoryByID(ctx, memoryID)
	if err != nil {
		return apptypes.MemoryDetails{}, err
	}

	// Resolve the effective validity window (after applying the
	// requested change) before mutating so we can reject reversed
	// windows without partially writing to the repository. The check
	// compares the final (post-change) bounds — not just the flags the
	// caller supplied — so shifting only validFrom after the current
	// validTo still fails, as does shifting only validTo before the
	// current validFrom.
	effectiveFrom := memory.ValidFrom()
	if from, ok := validFrom.Value(); ok {
		effectiveFrom = from
	}
	effectiveTo := memory.ValidTo()
	if to, ok := validTo.Value(); ok {
		effectiveTo = domtypes.Some(to)
	} else if clearValidTo {
		effectiveTo = domtypes.None[time.Time]()
	}
	if to, ok := effectiveTo.Value(); ok {
		if to.Before(effectiveFrom) {
			return apptypes.MemoryDetails{}, xerrors.Errorf("valid_to must not be earlier than valid_from")
		}
	}

	memory.SetValidity(validFrom, effectiveTo)
	if err := u.memoryRepo.Save(ctx, memory); err != nil {
		return apptypes.MemoryDetails{}, xerrors.Errorf("failed to save memory validity window: %w", err)
	}

	details, err := apptypes.MemoryDetailsFrom(memory)
	if err != nil {
		return apptypes.MemoryDetails{}, xerrors.Errorf("failed to build memory details: %w", err)
	}
	return details, nil
}

func (u *memoryUsecase) Expire(ctx context.Context, memoryID domtypes.MemoryID, expiresAt domtypes.Optional[time.Time]) (apptypes.MemoryDetails, error) {
	if u.memoryRepo == nil {
		return apptypes.MemoryDetails{}, xerrors.Errorf("memory repository is not configured")
	}

	memory, err := u.findMemoryByID(ctx, memoryID)
	if err != nil {
		return apptypes.MemoryDetails{}, err
	}
	resolvedExpiresAt, err := resolveExpiresAt(expiresAt)
	if err != nil {
		return apptypes.MemoryDetails{}, err
	}
	if err := memory.Expire(resolvedExpiresAt); err != nil {
		return apptypes.MemoryDetails{}, xerrors.Errorf("failed to expire memory: %w", err)
	}
	if err := u.memoryRepo.Save(ctx, memory); err != nil {
		return apptypes.MemoryDetails{}, xerrors.Errorf("failed to save expired memory: %w", err)
	}

	details, err := apptypes.MemoryDetailsFrom(memory)
	if err != nil {
		return apptypes.MemoryDetails{}, xerrors.Errorf("failed to build memory details: %w", err)
	}
	return details, nil
}

func (u *memoryUsecase) List(ctx context.Context, criteria apptypes.MemoryListCriteria) ([]apptypes.MemorySummary, error) {
	if u.memoryQuery == nil {
		return nil, xerrors.Errorf("memory query service is not configured")
	}
	if criteria.Limit() <= 0 {
		return nil, xerrors.Errorf("limit must be greater than or equal to 1")
	}
	if criteria.Offset() < 0 {
		return nil, xerrors.Errorf("offset must be greater than or equal to 0")
	}

	summaries, err := u.memoryQuery.List(ctx, criteria)
	if err != nil {
		return nil, xerrors.Errorf("failed to list durable memories: %w", err)
	}
	return summaries, nil
}

func (u *memoryUsecase) Search(ctx context.Context, criteria apptypes.MemorySearchCriteria) ([]apptypes.MemorySummary, error) {
	if u.memoryQuery == nil {
		return nil, xerrors.Errorf("memory query service is not configured")
	}
	if criteria.Limit() <= 0 {
		return nil, xerrors.Errorf("limit must be greater than or equal to 1")
	}
	if criteria.Offset() < 0 {
		return nil, xerrors.Errorf("offset must be greater than or equal to 0")
	}
	if !hasMemorySearchConstraint(criteria) {
		return nil, xerrors.Errorf("at least one search filter is required")
	}

	summaries, err := u.memoryQuery.Search(ctx, criteria)
	if err != nil {
		return nil, xerrors.Errorf("failed to search durable memories: %w", err)
	}
	return summaries, nil
}

func (u *memoryUsecase) Show(ctx context.Context, memoryID domtypes.MemoryID) (apptypes.MemoryDetails, error) {
	if u.memoryQuery == nil {
		return apptypes.MemoryDetails{}, xerrors.Errorf("memory query service is not configured")
	}
	resolvedMemoryID, err := domtypes.MemoryIDOf(memoryID.String())
	if err != nil {
		return apptypes.MemoryDetails{}, xerrors.Errorf("failed to resolve memory ID: %w", err)
	}

	details, err := u.memoryQuery.GetDetails(ctx, resolvedMemoryID)
	if err != nil {
		return apptypes.MemoryDetails{}, xerrors.Errorf("failed to get memory details: %w", err)
	}
	return details, nil
}

func (u *memoryUsecase) findMemoryByID(ctx context.Context, memoryID domtypes.MemoryID) (*model.Memory, error) {
	resolvedMemoryID, err := domtypes.MemoryIDOf(memoryID.String())
	if err != nil {
		return nil, xerrors.Errorf("failed to resolve memory ID: %w", err)
	}

	result, err := u.memoryRepo.FindByID(ctx, resolvedMemoryID)
	if err != nil {
		return nil, xerrors.Errorf("failed to find memory: %w", err)
	}
	memory, ok := result.Value()
	if !ok {
		return nil, xerrors.Errorf("memory not found: %s", resolvedMemoryID)
	}
	return memory, nil
}

func (u *memoryUsecase) sanitizeMemoryPayload(
	fact string,
	evidenceRefs []domtypes.EvidenceRef,
	artifactRefs []domtypes.ArtifactRef,
) (string, []domtypes.EvidenceRef, []domtypes.ArtifactRef, error) {
	return sanitizeMemoryPayload(fact, evidenceRefs, artifactRefs, u.extraRedactPatterns)
}

func sanitizeEvidenceRefs(refs []domtypes.EvidenceRef, extraRedactors []redaction.Redactor) ([]domtypes.EvidenceRef, error) {
	sanitized := make([]domtypes.EvidenceRef, 0, len(refs))
	for _, ref := range refs {
		value, _ := redaction.Apply(ref.Value(), extraRedactors)
		resolvedRef, err := domtypes.EvidenceRefOf(ref.Kind(), value)
		if err != nil {
			return nil, xerrors.Errorf("failed to sanitize evidence ref: %w", err)
		}
		sanitized = append(sanitized, resolvedRef)
	}
	return sanitized, nil
}

func sanitizeArtifactRefs(refs []domtypes.ArtifactRef, extraRedactors []redaction.Redactor) ([]domtypes.ArtifactRef, error) {
	sanitized := make([]domtypes.ArtifactRef, 0, len(refs))
	for _, ref := range refs {
		value, _ := redaction.Apply(ref.Value(), extraRedactors)
		resolvedRef, err := domtypes.ArtifactRefOf(ref.Kind(), value)
		if err != nil {
			return nil, xerrors.Errorf("failed to sanitize artifact ref: %w", err)
		}
		sanitized = append(sanitized, resolvedRef)
	}
	return sanitized, nil
}

func resolveRequiredMemoryType(memoryType domtypes.MemoryType) (domtypes.MemoryType, error) {
	resolved, err := domtypes.MemoryTypeOf(memoryType.String())
	if err != nil {
		return domtypes.MemoryType(""), xerrors.Errorf("failed to resolve memory type: %w", err)
	}
	return resolved, nil
}

func inheritOrResolveMemoryType(memoryType domtypes.MemoryType, fallback domtypes.MemoryType) (domtypes.MemoryType, error) {
	if strings.TrimSpace(memoryType.String()) == "" {
		return resolveRequiredMemoryType(fallback)
	}
	return resolveRequiredMemoryType(memoryType)
}

func resolveRequiredMemoryScope(scope domtypes.MemoryScope) (domtypes.MemoryScope, error) {
	if scope == nil {
		return nil, xerrors.Errorf("memory scope must not be nil")
	}
	if _, err := domtypes.MemoryScopeFrom(scope.Kind().String(), scope.Key()); err != nil {
		return nil, xerrors.Errorf("failed to resolve memory scope: %w", err)
	}
	return scope, nil
}

func inheritOrResolveMemoryScope(scope domtypes.MemoryScope, fallback domtypes.MemoryScope) (domtypes.MemoryScope, error) {
	if scope == nil {
		return resolveRequiredMemoryScope(fallback)
	}
	return resolveRequiredMemoryScope(scope)
}

func resolveMemorySource(source domtypes.MemorySource) (domtypes.MemorySource, error) {
	if strings.TrimSpace(source.String()) == "" {
		return domtypes.MemorySourceManual, nil
	}
	resolved, err := domtypes.MemorySourceOf(source.String())
	if err != nil {
		return domtypes.MemorySource(""), xerrors.Errorf("failed to resolve memory source: %w", err)
	}
	return resolved, nil
}

func resolveAcceptedConfidence(confidence domtypes.Optional[domtypes.Confidence]) (domtypes.Confidence, error) {
	if value, ok := confidence.Value(); ok {
		resolved, err := domtypes.ConfidenceOf(value.String())
		if err != nil {
			return domtypes.Confidence(""), xerrors.Errorf("failed to resolve confidence: %w", err)
		}
		return resolved, nil
	}
	return domtypes.ConfidenceVerified, nil
}

func requireAcceptedEvidenceRefs(evidenceRefs []domtypes.EvidenceRef) error {
	if len(evidenceRefs) == 0 {
		return xerrors.Errorf("accepted memory requires at least one evidence ref")
	}
	return nil
}

func resolveExpiresAt(expiresAt domtypes.Optional[time.Time]) (time.Time, error) {
	if value, ok := expiresAt.Value(); ok {
		if value.IsZero() {
			return time.Time{}, xerrors.Errorf("expiry timestamp must not be zero")
		}
		return value, nil
	}
	return time.Now(), nil
}

func hasMemorySearchConstraint(criteria apptypes.MemorySearchCriteria) bool {
	return strings.TrimSpace(criteria.Query()) != "" ||
		len(criteria.Scopes()) > 0 ||
		len(criteria.Statuses()) > 0 ||
		len(criteria.MemoryTypes()) > 0
}
