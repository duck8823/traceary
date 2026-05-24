package usecase

import (
	"context"
	"slices"
	"strings"
	"time"

	"golang.org/x/xerrors"

	"github.com/duck8823/traceary/application"
	"github.com/duck8823/traceary/application/queryservice"
	"github.com/duck8823/traceary/application/redaction"
	apptypes "github.com/duck8823/traceary/application/types"
	"github.com/duck8823/traceary/domain/model"
	domtypes "github.com/duck8823/traceary/domain/types"
)

type memoryUsecase struct {
	memoryRepo          model.MemoryRepository
	memoryQuery         queryservice.MemoryQueryService
	sessionQuery        queryservice.SessionQueryService
	eventQuery          queryservice.EventQueryService
	codexSource         application.CodexMemorySource
	extraRedactPatterns []string
}

// MemoryUsecaseDependencies carries the optional dependency set needed by
// capture, hygiene, and export methods on the consolidated MemoryUsecase.
//
// The parameter remains optional during the adapter-shim transition so legacy
// lifecycle/query-only call sites keep compiling until DI is collapsed.
type MemoryUsecaseDependencies struct {
	SessionQuery queryservice.SessionQueryService
	EventQuery   queryservice.EventQueryService
	CodexSource  application.CodexMemorySource
}

// NewMemoryUsecase creates a consolidated MemoryUsecase facade.
func NewMemoryUsecase(
	memoryRepo model.MemoryRepository,
	memoryQuery queryservice.MemoryQueryService,
	extraRedactPatterns []string,
	optionalDeps ...MemoryUsecaseDependencies,
) MemoryUsecase {
	usecase := &memoryUsecase{
		memoryRepo:          memoryRepo,
		memoryQuery:         memoryQuery,
		extraRedactPatterns: slices.Clone(extraRedactPatterns),
	}
	if len(optionalDeps) > 0 {
		deps := optionalDeps[0]
		usecase.sessionQuery = deps.SessionQuery
		usecase.eventQuery = deps.EventQuery
		usecase.codexSource = deps.CodexSource
	}
	return usecase
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

func (u *memoryUsecase) Distill(ctx context.Context, criteria apptypes.MemoryDistillCriteria) (apptypes.MemoryDistillResult, error) {
	if u.memoryRepo == nil {
		return apptypes.MemoryDistillResult{}, xerrors.Errorf("memory repository is not configured")
	}

	sourceIDs := dedupeMemoryIDs(criteria.FromIDs())
	if len(sourceIDs) == 0 {
		return apptypes.MemoryDistillResult{}, xerrors.Errorf("distill requires at least one source candidate")
	}
	resolvedType, err := resolveRequiredMemoryType(criteria.MemoryType())
	if err != nil {
		return apptypes.MemoryDistillResult{}, err
	}
	resolvedScope, err := resolveRequiredMemoryScope(criteria.Scope())
	if err != nil {
		return apptypes.MemoryDistillResult{}, err
	}
	resolvedSource, err := resolveMemorySource(criteria.Source())
	if err != nil {
		return apptypes.MemoryDistillResult{}, err
	}
	resolvedConfidence, err := resolveAcceptedConfidence(criteria.Confidence())
	if err != nil {
		return apptypes.MemoryDistillResult{}, err
	}
	replace, err := resolveMemoryDistillReplace(criteria.Replace())
	if err != nil {
		return apptypes.MemoryDistillResult{}, err
	}

	sources := make([]*model.Memory, 0, len(sourceIDs))
	evidenceRefs := make([]domtypes.EvidenceRef, 0)
	artifactRefs := make([]domtypes.ArtifactRef, 0)
	for _, sourceID := range sourceIDs {
		memory, err := u.findMemoryByID(ctx, sourceID)
		if err != nil {
			return apptypes.MemoryDistillResult{}, err
		}
		if memory.Status() != domtypes.MemoryStatusCandidate {
			return apptypes.MemoryDistillResult{}, xerrors.Errorf("distill source must be a candidate memory: %s", sourceID)
		}
		sources = append(sources, memory)
		evidenceRefs = appendMemoryEvidenceRefs(evidenceRefs, memory.EvidenceRefs())
		artifactRefs = appendMemoryArtifactRefs(artifactRefs, memory.ArtifactRefs())
	}

	fact, evidenceRefs, artifactRefs, err := u.sanitizeMemoryPayload(criteria.Fact(), evidenceRefs, artifactRefs)
	if err != nil {
		return apptypes.MemoryDistillResult{}, err
	}
	if err := requireAcceptedEvidenceRefs(evidenceRefs); err != nil {
		return apptypes.MemoryDistillResult{}, err
	}

	newMemoryID, err := newMemoryID()
	if err != nil {
		return apptypes.MemoryDistillResult{}, xerrors.Errorf("failed to generate distilled memory ID: %w", err)
	}
	distilled, err := model.NewAcceptedMemory(
		newMemoryID,
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
		return apptypes.MemoryDistillResult{}, xerrors.Errorf("failed to build distilled memory: %w", err)
	}
	resultSources := make([]apptypes.MemorySummary, 0, len(sources))
	sourcesToPersist := make([]*model.Memory, 0, len(sources))
	for _, source := range sources {
		switch replace {
		case apptypes.MemoryDistillReplaceKeep:
			// Leave the candidate as-is.
		case apptypes.MemoryDistillReplaceReject:
			if err := source.Reject(); err != nil {
				return apptypes.MemoryDistillResult{}, xerrors.Errorf("failed to reject source candidate %s: %w", source.MemoryID(), err)
			}
			sourcesToPersist = append(sourcesToPersist, source)
		case apptypes.MemoryDistillReplaceSupersede:
			if err := source.MarkCandidateSupersededByDistillation(); err != nil {
				return apptypes.MemoryDistillResult{}, xerrors.Errorf("failed to supersede source candidate %s: %w", source.MemoryID(), err)
			}
			sourcesToPersist = append(sourcesToPersist, source)
		default:
			return apptypes.MemoryDistillResult{}, xerrors.Errorf("unsupported distill replace policy: %s", replace)
		}

		summary, err := apptypes.MemorySummaryFrom(source)
		if err != nil {
			return apptypes.MemoryDistillResult{}, xerrors.Errorf("failed to build source summary: %w", err)
		}
		resultSources = append(resultSources, summary)
	}

	if err := u.memoryRepo.SaveDistillation(ctx, distilled, sourcesToPersist); err != nil {
		return apptypes.MemoryDistillResult{}, xerrors.Errorf("failed to save distilled memory and source updates: %w", err)
	}

	details, err := apptypes.MemoryDetailsFrom(distilled)
	if err != nil {
		return apptypes.MemoryDistillResult{}, xerrors.Errorf("failed to build distilled memory details: %w", err)
	}
	return apptypes.MemoryDistillResultOf(details, resultSources, replace), nil
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

func (u *memoryUsecase) AttachCandidateRefs(
	ctx context.Context,
	memoryID domtypes.MemoryID,
	evidenceRefs []domtypes.EvidenceRef,
	artifactRefs []domtypes.ArtifactRef,
) (apptypes.MemoryDetails, error) {
	if u.memoryRepo == nil {
		return apptypes.MemoryDetails{}, xerrors.Errorf("memory repository is not configured")
	}
	if len(evidenceRefs) == 0 && len(artifactRefs) == 0 {
		return apptypes.MemoryDetails{}, xerrors.Errorf("attaching candidate refs requires at least one evidence or artifact ref")
	}

	memory, err := u.findMemoryByID(ctx, memoryID)
	if err != nil {
		return apptypes.MemoryDetails{}, err
	}
	sanitizedEvidenceRefs, sanitizedArtifactRefs, err := sanitizeMemoryRefs(evidenceRefs, artifactRefs, u.extraRedactPatterns)
	if err != nil {
		return apptypes.MemoryDetails{}, err
	}
	if len(sanitizedEvidenceRefs) == 0 && len(sanitizedArtifactRefs) == 0 {
		return apptypes.MemoryDetails{}, xerrors.Errorf("attaching candidate refs requires at least one evidence or artifact ref")
	}
	if len(sanitizedEvidenceRefs) == 0 && len(memory.EvidenceRefs()) == 0 {
		return apptypes.MemoryDetails{}, xerrors.Errorf("attaching candidate refs requires at least one evidence ref")
	}
	if err := memory.AttachRefs(sanitizedEvidenceRefs, sanitizedArtifactRefs); err != nil {
		return apptypes.MemoryDetails{}, xerrors.Errorf("failed to attach candidate refs: %w", err)
	}
	if err := u.memoryRepo.Save(ctx, memory); err != nil {
		return apptypes.MemoryDetails{}, xerrors.Errorf("failed to save memory candidate refs: %w", err)
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

func (u *memoryUsecase) ListStale(ctx context.Context, criteria apptypes.StaleMemoryListCriteria) (apptypes.StaleMemoryListResult, error) {
	if u.memoryQuery == nil {
		return apptypes.StaleMemoryListResult{}, xerrors.Errorf("memory query service is not configured")
	}
	staleQuery, ok := u.memoryQuery.(queryservice.StaleMemoryQueryService)
	if !ok {
		return apptypes.StaleMemoryListResult{}, xerrors.Errorf("stale memory query service is not configured")
	}
	if criteria.Limit() <= 0 {
		return apptypes.StaleMemoryListResult{}, xerrors.Errorf("limit must be greater than or equal to 1")
	}
	if criteria.Offset() < 0 {
		return apptypes.StaleMemoryListResult{}, xerrors.Errorf("offset must be greater than or equal to 0")
	}

	result, err := staleQuery.ListStale(ctx, criteria)
	if err != nil {
		return apptypes.StaleMemoryListResult{}, xerrors.Errorf("failed to list stale durable memories: %w", err)
	}
	return result, nil
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
	resolvedMemoryID, err := domtypes.MemoryIDFrom(memoryID.String())
	if err != nil {
		return apptypes.MemoryDetails{}, xerrors.Errorf("failed to resolve memory ID: %w", err)
	}

	details, err := u.memoryQuery.GetDetails(ctx, resolvedMemoryID)
	if err != nil {
		return apptypes.MemoryDetails{}, xerrors.Errorf("failed to get memory details: %w", err)
	}
	return details, nil
}

func (u *memoryUsecase) Extract(ctx context.Context, criteria apptypes.MemoryExtractionCriteria) ([]apptypes.MemoryDetails, error) {
	return u.newMemoryExtractionUsecase().Extract(ctx, criteria)
}

func (u *memoryUsecase) ExplainExtraction(ctx context.Context, criteria apptypes.MemoryExtractionCriteria) (apptypes.MemoryExtractionDebugReport, error) {
	return u.newMemoryExtractionUsecase().Explain(ctx, criteria)
}

func (u *memoryUsecase) newMemoryExtractionUsecase() *memoryExtractionUsecase {
	return &memoryExtractionUsecase{
		sessionQuery:        u.sessionQuery,
		eventQuery:          u.eventQuery,
		memory:              u,
		extraRedactPatterns: u.extraRedactPatterns,
	}
}

func (u *memoryUsecase) ImportCodex(ctx context.Context, criteria apptypes.CodexImportCriteria) (apptypes.MemoryImportResult, error) {
	return (&memoryImportUsecase{
		memoryUsecase:       u,
		memoryQuery:         u.memoryQuery,
		codexSource:         u.codexSource,
		extraRedactPatterns: u.extraRedactPatterns,
	}).ImportCodex(ctx, criteria)
}

func (u *memoryUsecase) ImportInstructions(ctx context.Context, criteria apptypes.MemoryBridgeImportCriteria) (apptypes.MemoryBridgeImportResult, error) {
	return (&memoryBridgeImportUsecase{
		memoryUsecase:       u,
		memoryQuery:         u.memoryQuery,
		extraRedactPatterns: u.extraRedactPatterns,
	}).ImportInstructions(ctx, criteria)
}

func (u *memoryUsecase) Scan(ctx context.Context, criteria apptypes.MemoryHygieneScanCriteria) (apptypes.MemoryHygieneScanResult, error) {
	return (&memoryHygieneUsecase{
		memory:              u,
		memoryQuery:         u.memoryQuery,
		extraRedactPatterns: u.extraRedactPatterns,
	}).Scan(ctx, criteria)
}

func (u *memoryUsecase) Apply(ctx context.Context, criteria apptypes.MemoryHygieneApplyCriteria) (apptypes.MemoryHygieneApplyResult, error) {
	return (&memoryHygieneUsecase{
		memory:              u,
		memoryQuery:         u.memoryQuery,
		extraRedactPatterns: u.extraRedactPatterns,
	}).Apply(ctx, criteria)
}

func (u *memoryUsecase) Export(ctx context.Context, criteria apptypes.MemoryExportCriteria) (apptypes.MemoryExportResult, error) {
	return (&memoryExportUsecase{memoryQuery: u.memoryQuery}).Export(ctx, criteria)
}

func (u *memoryUsecase) ActivatePlan(ctx context.Context, criteria apptypes.MemoryActivationCriteria) (apptypes.MemoryActivationPlan, error) {
	return (&memoryActivationUsecase{memoryQuery: u.memoryQuery}).Plan(ctx, criteria)
}

func (u *memoryUsecase) Activate(ctx context.Context, criteria apptypes.MemoryActivationCriteria) (apptypes.MemoryActivationApplyResult, error) {
	return (&memoryActivationUsecase{memoryQuery: u.memoryQuery}).Apply(ctx, criteria)
}

func (u *memoryUsecase) ActivationStatus(ctx context.Context, criteria apptypes.MemoryActivationCriteria) (apptypes.MemoryActivationStatusResult, error) {
	return (&memoryActivationUsecase{memoryQuery: u.memoryQuery}).Status(ctx, criteria)
}

func (u *memoryUsecase) findMemoryByID(ctx context.Context, memoryID domtypes.MemoryID) (*model.Memory, error) {
	resolvedMemoryID, err := domtypes.MemoryIDFrom(memoryID.String())
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
		resolvedRef, err := domtypes.EvidenceRefFrom(ref.Kind(), value)
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
		resolvedRef, err := domtypes.ArtifactRefFrom(ref.Kind(), value)
		if err != nil {
			return nil, xerrors.Errorf("failed to sanitize artifact ref: %w", err)
		}
		sanitized = append(sanitized, resolvedRef)
	}
	return sanitized, nil
}

func resolveRequiredMemoryType(memoryType domtypes.MemoryType) (domtypes.MemoryType, error) {
	resolved, err := domtypes.MemoryTypeFrom(memoryType.String())
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
	resolved, err := domtypes.MemorySourceFrom(source.String())
	if err != nil {
		return domtypes.MemorySource(""), xerrors.Errorf("failed to resolve memory source: %w", err)
	}
	return resolved, nil
}

func resolveAcceptedConfidence(confidence domtypes.Optional[domtypes.Confidence]) (domtypes.Confidence, error) {
	if value, ok := confidence.Value(); ok {
		resolved, err := domtypes.ConfidenceFrom(value.String())
		if err != nil {
			return domtypes.Confidence(""), xerrors.Errorf("failed to resolve confidence: %w", err)
		}
		return resolved, nil
	}
	return domtypes.ConfidenceVerified, nil
}

func resolveMemoryDistillReplace(replace apptypes.MemoryDistillReplace) (apptypes.MemoryDistillReplace, error) {
	switch replace {
	case "", apptypes.MemoryDistillReplaceKeep:
		return apptypes.MemoryDistillReplaceKeep, nil
	case apptypes.MemoryDistillReplaceReject:
		return apptypes.MemoryDistillReplaceReject, nil
	case apptypes.MemoryDistillReplaceSupersede:
		return apptypes.MemoryDistillReplaceSupersede, nil
	default:
		return "", xerrors.Errorf("unsupported distill replace policy: %s", replace)
	}
}

func requireAcceptedEvidenceRefs(evidenceRefs []domtypes.EvidenceRef) error {
	if len(evidenceRefs) == 0 {
		return xerrors.Errorf("accepted memory requires at least one evidence ref")
	}
	return nil
}

func dedupeMemoryIDs(ids []domtypes.MemoryID) []domtypes.MemoryID {
	result := make([]domtypes.MemoryID, 0, len(ids))
	seen := make(map[string]struct{}, len(ids))
	for _, id := range ids {
		key := id.String()
		if strings.TrimSpace(key) == "" {
			continue
		}
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		result = append(result, id)
	}
	return result
}

func appendMemoryEvidenceRefs(dst []domtypes.EvidenceRef, refs []domtypes.EvidenceRef) []domtypes.EvidenceRef {
	seen := make(map[string]struct{}, len(dst)+len(refs))
	for _, ref := range dst {
		seen[ref.Kind().String()+":"+ref.Value()] = struct{}{}
	}
	for _, ref := range refs {
		key := ref.Kind().String() + ":" + ref.Value()
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		dst = append(dst, ref)
	}
	return dst
}

func appendMemoryArtifactRefs(dst []domtypes.ArtifactRef, refs []domtypes.ArtifactRef) []domtypes.ArtifactRef {
	seen := make(map[string]struct{}, len(dst)+len(refs))
	for _, ref := range dst {
		seen[ref.Kind().String()+":"+ref.Value()] = struct{}{}
	}
	for _, ref := range refs {
		key := ref.Kind().String() + ":" + ref.Value()
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		dst = append(dst, ref)
	}
	return dst
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
