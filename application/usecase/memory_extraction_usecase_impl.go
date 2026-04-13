package usecase

import (
	"context"
	"fmt"
	"regexp"
	"slices"
	"strings"
	"time"

	"golang.org/x/xerrors"

	"github.com/duck8823/traceary/application/queryservice"
	apptypes "github.com/duck8823/traceary/application/types"
	"github.com/duck8823/traceary/domain/model"
	domtypes "github.com/duck8823/traceary/domain/types"
)

var (
	explicitMemoryLabelPattern = regexp.MustCompile(`(?i)^(?:[-*]\s+|\d+\.\s+)?(?:(?:user\s+)?(preference|decision|constraint|lesson|artifact|feedback|correction))s?\s*[:\-]\s*(.+)$`)
	urlRefPattern              = regexp.MustCompile(`https?://[^\s)]+`)
	issueRefPattern            = regexp.MustCompile(`(?i)\bissues?\s*#(\d+)\b`)
	prRefPattern               = regexp.MustCompile(`(?i)\b(?:pr|pull request)\s*#(\d+)\b`)
	fileRefPattern             = regexp.MustCompile(`(?i)\b(?:[A-Za-z0-9_.-]+/)*[A-Za-z0-9_.-]+\.(?:go|md|json|sh|sql|yaml|yml|toml)\b`)
)

type memoryExtractionUsecase struct {
	sessionQuery queryservice.SessionQueryService
	eventQuery   queryservice.EventQueryService
	memory       MemoryUsecase
}

// NewMemoryExtractionUsecase creates a MemoryExtractionUsecase.
func NewMemoryExtractionUsecase(
	sessionQuery queryservice.SessionQueryService,
	eventQuery queryservice.EventQueryService,
	memory MemoryUsecase,
) MemoryExtractionUsecase {
	return &memoryExtractionUsecase{
		sessionQuery: sessionQuery,
		eventQuery:   eventQuery,
		memory:       memory,
	}
}

func (u *memoryExtractionUsecase) Extract(ctx context.Context, criteria apptypes.MemoryExtractionCriteria) ([]apptypes.MemoryDetails, error) {
	if u.sessionQuery == nil {
		return nil, xerrors.Errorf("session query service is not configured")
	}
	if u.eventQuery == nil {
		return nil, xerrors.Errorf("event query service is not configured")
	}
	if u.memory == nil {
		return nil, xerrors.Errorf("memory usecase is not configured")
	}
	if criteria.SessionID().String() == "" {
		return nil, xerrors.Errorf("session ID must not be empty")
	}
	if criteria.EventLimit() < 0 {
		return nil, xerrors.Errorf("event limit must be greater than or equal to 0")
	}
	if criteria.CandidateLimit() <= 0 {
		return nil, xerrors.Errorf("candidate limit must be greater than or equal to 1")
	}

	session, ok, err := u.findTargetSession(ctx, criteria)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, nil
	}

	scope := extractedMemoryScope(session)
	existingKeys, err := u.loadExistingCandidateKeys(ctx, scope)
	if err != nil {
		return nil, err
	}

	specs, err := u.collectCandidateSpecs(ctx, session, criteria.EventLimit())
	if err != nil {
		return nil, err
	}

	results := make([]apptypes.MemoryDetails, 0, min(criteria.CandidateLimit(), len(specs)))
	seenKeys := make(map[string]struct{}, len(specs))
	for _, spec := range specs {
		key := memoryCandidateKey(scope, spec.memoryType, spec.fact)
		if _, exists := seenKeys[key]; exists {
			continue
		}
		if _, exists := existingKeys[key]; exists {
			continue
		}

		details, err := u.memory.Propose(
			ctx,
			spec.memoryType,
			scope,
			spec.fact,
			domtypes.MemorySourceExtracted,
			spec.evidenceRefs,
			spec.artifactRefs,
		)
		if err != nil {
			return nil, xerrors.Errorf("failed to propose extracted memory candidate: %w", err)
		}
		results = append(results, details)
		seenKeys[key] = struct{}{}
		if len(results) >= criteria.CandidateLimit() {
			break
		}
	}

	return results, nil
}

func (u *memoryExtractionUsecase) findTargetSession(
	ctx context.Context,
	criteria apptypes.MemoryExtractionCriteria,
) (apptypes.SessionSummary, bool, error) {
	summaries, err := u.sessionQuery.ListSummaries(
		ctx,
		1,
		0,
		criteria.SessionID(),
		criteria.Workspace(),
		domtypes.Client(""),
		domtypes.Agent(""),
		"",
		domtypes.Empty[time.Time](),
		domtypes.Empty[time.Time](),
	)
	if err != nil {
		return apptypes.SessionSummary{}, false, xerrors.Errorf("failed to list sessions for memory extraction: %w", err)
	}
	if len(summaries) == 0 {
		return apptypes.SessionSummary{}, false, nil
	}
	return summaries[0], true, nil
}

func extractedMemoryScope(session apptypes.SessionSummary) domtypes.MemoryScope {
	if workspace := session.Workspace(); workspace.String() != "" {
		return domtypes.WorkspaceScopeOf(workspace)
	}
	return domtypes.SessionFamilyScopeOf(session.SessionID())
}

func (u *memoryExtractionUsecase) loadExistingCandidateKeys(ctx context.Context, scope domtypes.MemoryScope) (map[string]struct{}, error) {
	summaries, err := u.memory.List(
		ctx,
		apptypes.NewMemoryListCriteriaBuilder(200).
			Scopes([]domtypes.MemoryScope{scope}).
			Statuses([]domtypes.MemoryStatus{
				domtypes.MemoryStatusCandidate,
				domtypes.MemoryStatusAccepted,
			}).
			Build(),
	)
	if err != nil {
		return nil, xerrors.Errorf("failed to list existing memories for extraction dedupe: %w", err)
	}

	keys := make(map[string]struct{}, len(summaries))
	for _, summary := range summaries {
		keys[memoryCandidateKey(summary.Scope(), summary.MemoryType(), summary.Fact())] = struct{}{}
	}
	return keys, nil
}

type extractionSignal struct {
	text            string
	event           *model.Event
	heuristics      bool
	allowStructured bool
}

func (u *memoryExtractionUsecase) collectCandidateSpecs(ctx context.Context, session apptypes.SessionSummary, eventLimit int) ([]memoryCandidateSpec, error) {
	signals := make([]extractionSignal, 0, 1+4*max(eventLimit, 1))
	if summary := strings.TrimSpace(session.Summary()); summary != "" {
		signals = append(signals, extractionSignal{
			text:            summary,
			heuristics:      true,
			allowStructured: true,
		})
	}

	appendKindSignals := func(kind domtypes.EventKind, heuristics bool, allowStructured bool) error {
		if eventLimit == 0 {
			return nil
		}
		events, err := u.eventQuery.ListRecent(
			ctx,
			eventLimit,
			0,
			kind,
			domtypes.Client(""),
			domtypes.Agent(""),
			session.SessionID(),
			domtypes.Workspace(""),
			false,
			time.Time{},
			time.Time{},
		)
		if err != nil {
			return xerrors.Errorf("failed to list %s events for extraction: %w", kind, err)
		}
		for _, event := range events {
			signals = append(signals, extractionSignal{
				text:            event.Body(),
				event:           event,
				heuristics:      heuristics,
				allowStructured: allowStructured,
			})
		}
		return nil
	}

	if err := appendKindSignals(domtypes.EventKindPrompt, true, true); err != nil {
		return nil, err
	}
	if err := appendKindSignals(domtypes.EventKindReviewed, true, true); err != nil {
		return nil, err
	}
	if err := appendKindSignals(domtypes.EventKindNote, true, true); err != nil {
		return nil, err
	}
	if err := appendKindSignals(domtypes.EventKindCompactSummary, false, true); err != nil {
		return nil, err
	}

	specs := make([]memoryCandidateSpec, 0, len(signals))
	for _, signal := range signals {
		evidenceRefs, err := buildSignalEvidenceRefs(session.SessionID(), signal.event)
		if err != nil {
			return nil, err
		}
		signalSpecs, err := extractMemoryCandidatesFromSignal(signal, evidenceRefs)
		if err != nil {
			return nil, err
		}
		specs = append(specs, signalSpecs...)
	}

	return specs, nil
}

type memoryCandidateSpec struct {
	memoryType   domtypes.MemoryType
	fact         string
	evidenceRefs []domtypes.EvidenceRef
	artifactRefs []domtypes.ArtifactRef
}

func extractMemoryCandidatesFromSignal(signal extractionSignal, evidenceRefs []domtypes.EvidenceRef) ([]memoryCandidateSpec, error) {
	segments := candidateSegments(signal.text)
	specs := make([]memoryCandidateSpec, 0, len(segments))
	for _, segment := range segments {
		if signal.allowStructured {
			spec, ok, err := structuredCandidateSpec(segment, evidenceRefs)
			if err != nil {
				return nil, err
			}
			if ok {
				specs = append(specs, spec)
				continue
			}
		}

		if signal.heuristics {
			spec, ok, err := heuristicCandidateSpec(segment, evidenceRefs)
			if err != nil {
				return nil, err
			}
			if ok {
				specs = append(specs, spec)
			}
		}
	}
	return specs, nil
}

func structuredCandidateSpec(segment string, evidenceRefs []domtypes.EvidenceRef) (memoryCandidateSpec, bool, error) {
	matches := explicitMemoryLabelPattern.FindStringSubmatch(segment)
	if len(matches) != 3 {
		return memoryCandidateSpec{}, false, nil
	}

	memoryType, err := memoryTypeFromLabel(matches[1])
	if err != nil {
		return memoryCandidateSpec{}, false, err
	}
	fact := normalizeCandidateFact(matches[2])
	if fact == "" {
		return memoryCandidateSpec{}, false, nil
	}

	artifactRefs, err := inferArtifactRefs(fact)
	if err != nil {
		return memoryCandidateSpec{}, false, err
	}
	return memoryCandidateSpec{
		memoryType:   memoryType,
		fact:         fact,
		evidenceRefs: slices.Clone(evidenceRefs),
		artifactRefs: artifactRefs,
	}, true, nil
}

func heuristicCandidateSpec(segment string, evidenceRefs []domtypes.EvidenceRef) (memoryCandidateSpec, bool, error) {
	fact := normalizeCandidateFact(segment)
	if fact == "" {
		return memoryCandidateSpec{}, false, nil
	}

	memoryType, ok := inferMemoryTypeFromText(fact)
	if !ok {
		return memoryCandidateSpec{}, false, nil
	}

	artifactRefs, err := inferArtifactRefs(fact)
	if err != nil {
		return memoryCandidateSpec{}, false, err
	}
	return memoryCandidateSpec{
		memoryType:   memoryType,
		fact:         fact,
		evidenceRefs: slices.Clone(evidenceRefs),
		artifactRefs: artifactRefs,
	}, true, nil
}

func candidateSegments(text string) []string {
	lines := strings.Split(text, "\n")
	segments := make([]string, 0, len(lines))
	for _, line := range lines {
		trimmed := normalizeCandidateFact(line)
		if trimmed == "" {
			continue
		}
		segments = append(segments, trimmed)
	}
	return segments
}

func normalizeCandidateFact(value string) string {
	trimmed := strings.TrimSpace(value)
	trimmed = strings.TrimPrefix(trimmed, "- ")
	trimmed = strings.TrimPrefix(trimmed, "* ")
	trimmed = strings.TrimPrefix(trimmed, "> ")
	trimmed = strings.Trim(trimmed, "`")
	trimmed = strings.Trim(trimmed, "[]")
	return strings.Join(strings.Fields(trimmed), " ")
}

func memoryTypeFromLabel(label string) (domtypes.MemoryType, error) {
	switch strings.ToLower(strings.TrimSpace(label)) {
	case "preference", "feedback", "correction":
		return domtypes.MemoryTypePreference, nil
	case "decision":
		return domtypes.MemoryTypeDecision, nil
	case "constraint":
		return domtypes.MemoryTypeConstraint, nil
	case "lesson":
		return domtypes.MemoryTypeLesson, nil
	case "artifact":
		return domtypes.MemoryTypeArtifact, nil
	default:
		return domtypes.MemoryType(""), xerrors.Errorf("unsupported memory label: %s", label)
	}
}

func inferMemoryTypeFromText(text string) (domtypes.MemoryType, bool) {
	lower := strings.ToLower(text)

	switch {
	case containsAny(lower,
		"prefer ",
		"please ",
		"always ",
		"never ",
		"do not ",
		"don't ",
		"respond in",
		"call it ",
		"use english",
		"use japanese",
		"correction:",
		"feedback:",
	):
		return domtypes.MemoryTypePreference, true
	case containsAny(lower,
		"must ",
		"must not",
		"required",
		"cannot ",
		"can't ",
		"only ",
		"forbidden",
		"out of scope",
	):
		return domtypes.MemoryTypeConstraint, true
	case containsAny(lower,
		"decision:",
		"decided ",
		"we chose",
		"chosen ",
		"agreed ",
	):
		return domtypes.MemoryTypeDecision, true
	case containsAny(lower,
		"lesson:",
		"learned ",
		"next time",
		"remember to",
		"mistake",
	):
		return domtypes.MemoryTypeLesson, true
	default:
		if inferredArtifactRefs, err := inferArtifactRefs(text); err == nil && len(inferredArtifactRefs) > 0 && containsAny(lower, "see ", "reference ", "refer to", "artifact:") {
			return domtypes.MemoryTypeArtifact, true
		}
		return domtypes.MemoryType(""), false
	}
}

func containsAny(value string, needles ...string) bool {
	for _, needle := range needles {
		if strings.Contains(value, needle) {
			return true
		}
	}
	return false
}

func buildSignalEvidenceRefs(sessionID domtypes.SessionID, event *model.Event) ([]domtypes.EvidenceRef, error) {
	evidenceRefs := make([]domtypes.EvidenceRef, 0, 2)
	if sessionID.String() != "" {
		ref, err := domtypes.EvidenceRefOf(domtypes.EvidenceRefKindSession, sessionID.String())
		if err != nil {
			return nil, xerrors.Errorf("failed to build session evidence ref: %w", err)
		}
		evidenceRefs = append(evidenceRefs, ref)
	}
	if event != nil {
		ref, err := domtypes.EvidenceRefOf(domtypes.EvidenceRefKindEvent, event.EventID().String())
		if err != nil {
			return nil, xerrors.Errorf("failed to build event evidence ref: %w", err)
		}
		evidenceRefs = append(evidenceRefs, ref)
	}
	return evidenceRefs, nil
}

func inferArtifactRefs(text string) ([]domtypes.ArtifactRef, error) {
	refs := make([]domtypes.ArtifactRef, 0)
	seen := make(map[string]struct{})
	appendRef := func(kind domtypes.ArtifactRefKind, value string) error {
		key := fmt.Sprintf("%s:%s", kind, value)
		if _, exists := seen[key]; exists {
			return nil
		}
		ref, err := domtypes.ArtifactRefOf(kind, value)
		if err != nil {
			return xerrors.Errorf("invalid artifact ref %s:%s: %w", kind, value, err)
		}
		refs = append(refs, ref)
		seen[key] = struct{}{}
		return nil
	}

	for _, match := range urlRefPattern.FindAllString(text, -1) {
		if err := appendRef(domtypes.ArtifactRefKindURL, match); err != nil {
			return nil, xerrors.Errorf("failed to build URL artifact ref: %w", err)
		}
	}
	for _, matches := range issueRefPattern.FindAllStringSubmatch(text, -1) {
		if len(matches) != 2 {
			continue
		}
		if err := appendRef(domtypes.ArtifactRefKindIssue, "#"+matches[1]); err != nil {
			return nil, xerrors.Errorf("failed to build issue artifact ref: %w", err)
		}
	}
	for _, matches := range prRefPattern.FindAllStringSubmatch(text, -1) {
		if len(matches) != 2 {
			continue
		}
		if err := appendRef(domtypes.ArtifactRefKindPR, "#"+matches[1]); err != nil {
			return nil, xerrors.Errorf("failed to build PR artifact ref: %w", err)
		}
	}
	for _, match := range fileRefPattern.FindAllString(text, -1) {
		if err := appendRef(domtypes.ArtifactRefKindFile, match); err != nil {
			return nil, xerrors.Errorf("failed to build file artifact ref: %w", err)
		}
	}

	return refs, nil
}

func memoryCandidateKey(scope domtypes.MemoryScope, memoryType domtypes.MemoryType, fact string) string {
	scopeKey := ""
	if scope != nil {
		scopeKey = fmt.Sprintf("%s:%s", scope.Kind(), scope.Key())
	}
	return fmt.Sprintf("%s|%s|%s", scopeKey, memoryType, strings.ToLower(strings.TrimSpace(fact)))
}
