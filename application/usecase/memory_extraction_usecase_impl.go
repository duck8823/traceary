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
	"github.com/duck8823/traceary/application/redaction"
	apptypes "github.com/duck8823/traceary/application/types"
	"github.com/duck8823/traceary/domain/model"
	domtypes "github.com/duck8823/traceary/domain/types"
)

var (
	explicitMemoryLabelPattern = regexp.MustCompile(`(?i)^(?:[-*]\s+|\d+\.\s+)?(?:(?:user\s+)?(preference|decision|constraint|lesson|artifact|feedback|correction))s?\s*[:\-]\s*(.+)$`)
	urlRefPattern              = regexp.MustCompile(`https?://[^\s)]+`)
	issueRefPattern            = regexp.MustCompile(`(?i)\bissues?\s*#(\d+)\b`)
	prRefPattern               = regexp.MustCompile(`(?i)\b(?:pr|pull request)\s*#(\d+)\b`)
	bareFileRefPattern         = regexp.MustCompile(`(?i)\b[A-Za-z0-9_.-]+\.(?:[A-Za-z0-9_-]+\.)*(?:go|md|json|sh|sql|yaml|yml|toml|ts|tsx|js|jsx|py|rb|ini|cfg|conf|proto|tpl)\b`)
	pathLikeRefPattern         = regexp.MustCompile(`(?:\./|\.\./|/)?(?:[A-Za-z0-9_.-]+/)+[A-Za-z0-9_.-]+(?:\.[A-Za-z0-9_-]+)*`)
)

var extensionlessArtifactRootSegments = map[string]struct{}{
	".github":        {},
	"application":    {},
	"bin":            {},
	"cmd":            {},
	"config":         {},
	"configs":        {},
	"coverage":       {},
	"dist":           {},
	"docs":           {},
	"domain":         {},
	"fixtures":       {},
	"formula":        {},
	"infrastructure": {},
	"integrations":   {},
	"internal":       {},
	"pkg":            {},
	"plugins":        {},
	"presentation":   {},
	"schema":         {},
	"scripts":        {},
	"src":            {},
	"test":           {},
	"tests":          {},
}

var artifactFileExtensions = map[string]struct{}{
	"go":    {},
	"md":    {},
	"json":  {},
	"sh":    {},
	"sql":   {},
	"yaml":  {},
	"yml":   {},
	"toml":  {},
	"ts":    {},
	"tsx":   {},
	"js":    {},
	"jsx":   {},
	"py":    {},
	"rb":    {},
	"ini":   {},
	"cfg":   {},
	"conf":  {},
	"proto": {},
	"tpl":   {},
}

const memoryExtractionDedupePageSize = 200

type memoryExtractionUsecase struct {
	sessionQuery        queryservice.SessionQueryService
	eventQuery          queryservice.EventQueryService
	memory              memoryExtractionWriter
	extraRedactPatterns []string
}

// NewMemoryExtractionUsecase creates a MemoryExtractionUsecase.
//
// Deprecated: use NewMemoryUsecase with MemoryUsecaseDependencies and call Extract.
func NewMemoryExtractionUsecase(
	sessionQuery queryservice.SessionQueryService,
	eventQuery queryservice.EventQueryService,
	memory memoryExtractionWriter,
	extraRedactPatterns []string,
) MemoryExtractionUsecase {
	if facade, ok := memory.(*memoryUsecase); ok {
		facade.sessionQuery = sessionQuery
		facade.eventQuery = eventQuery
		facade.extraRedactPatterns = slices.Clone(extraRedactPatterns)
		return facade
	}
	return &memoryExtractionUsecase{
		sessionQuery:        sessionQuery,
		eventQuery:          eventQuery,
		memory:              memory,
		extraRedactPatterns: slices.Clone(extraRedactPatterns),
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
	extraRedactors, err := redaction.CompileExtraPatterns(u.extraRedactPatterns)
	if err != nil {
		return nil, xerrors.Errorf("failed to compile extraction redaction patterns: %w", err)
	}
	existingKeys, err := u.loadExistingCandidateKeys(ctx, scope, extraRedactors)
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
		key := memoryCandidateKey(scope, spec.memoryType, sanitizeCandidateFact(spec.fact, extraRedactors))
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
		domtypes.None[time.Time](),
		domtypes.None[time.Time](),
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

func (u *memoryExtractionUsecase) loadExistingCandidateKeys(
	ctx context.Context,
	scope domtypes.MemoryScope,
	extraRedactors []redaction.Redactor,
) (map[string]struct{}, error) {
	keys := make(map[string]struct{})
	offset := 0
	for {
		summaries, err := u.memory.List(
			ctx,
			apptypes.NewMemoryListCriteriaBuilder(memoryExtractionDedupePageSize).
				Offset(offset).
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
		for _, summary := range summaries {
			keys[memoryCandidateKey(summary.Scope(), summary.MemoryType(), sanitizeCandidateFact(summary.Fact(), extraRedactors))] = struct{}{}
		}
		if len(summaries) < memoryExtractionDedupePageSize {
			break
		}
		offset += len(summaries)
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
			"",
		)
		if err != nil {
			return xerrors.Errorf("failed to list %s events for extraction: %w", kind, err)
		}
		for _, event := range events {
			signals = append(signals, extractionSignal{
				text:            apptypes.ExtractPlainBody(event.Body()),
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
	if err := appendKindSignals(domtypes.EventKindTranscript, true, true); err != nil {
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

func sanitizeCandidateFact(value string, extraRedactors []redaction.Redactor) string {
	sanitized, _ := redaction.Apply(strings.TrimSpace(value), extraRedactors)
	return strings.Join(strings.Fields(sanitized), " ")
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
		ref, err := domtypes.EvidenceRefFrom(domtypes.EvidenceRefKindSession, sessionID.String())
		if err != nil {
			return nil, xerrors.Errorf("failed to build session evidence ref: %w", err)
		}
		evidenceRefs = append(evidenceRefs, ref)
	}
	if event != nil {
		ref, err := domtypes.EvidenceRefFrom(domtypes.EvidenceRefKindEvent, event.EventID().String())
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
		ref, err := domtypes.ArtifactRefFrom(kind, value)
		if err != nil {
			return xerrors.Errorf("invalid artifact ref %s:%s: %w", kind, value, err)
		}
		refs = append(refs, ref)
		seen[key] = struct{}{}
		return nil
	}

	textWithoutURLs := text
	for _, match := range urlRefPattern.FindAllString(text, -1) {
		if err := appendRef(domtypes.ArtifactRefKindURL, match); err != nil {
			return nil, xerrors.Errorf("failed to build URL artifact ref: %w", err)
		}
		textWithoutURLs = strings.ReplaceAll(textWithoutURLs, match, " ")
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
	textWithoutPaths := textWithoutURLs
	for _, match := range pathLikeRefPattern.FindAllString(textWithoutURLs, -1) {
		normalized := normalizeArtifactPathMatch(match)
		if !looksPathLikeArtifact(normalized) {
			continue
		}
		if err := appendRef(domtypes.ArtifactRefKindFile, normalized); err != nil {
			return nil, xerrors.Errorf("failed to build file artifact ref: %w", err)
		}
		textWithoutPaths = strings.ReplaceAll(textWithoutPaths, match, " ")
	}
	for _, match := range bareFileRefPattern.FindAllString(textWithoutPaths, -1) {
		if err := appendRef(domtypes.ArtifactRefKindFile, match); err != nil {
			return nil, xerrors.Errorf("failed to build file artifact ref: %w", err)
		}
	}

	return refs, nil
}

func normalizeArtifactPathMatch(value string) string {
	trimmed := strings.TrimSpace(value)
	trimmed = strings.TrimLeft(trimmed, "`'\"()[]{}<>,:;")
	trimmed = strings.TrimRight(trimmed, "`'\"()[]{}<>,:;.!?")
	return trimmed
}

func looksPathLikeArtifact(value string) bool {
	if value == "" {
		return false
	}
	if !strings.Contains(value, "/") {
		return false
	}
	if strings.Contains(value, "://") {
		return false
	}
	hasExplicitPrefix := strings.HasPrefix(value, "./") || strings.HasPrefix(value, "../") || strings.HasPrefix(value, "/")

	hasLetter := false
	for _, r := range value {
		if ('a' <= r && r <= 'z') || ('A' <= r && r <= 'Z') {
			hasLetter = true
			break
		}
	}
	if !hasLetter {
		return false
	}
	segments := strings.Split(value, "/")
	if len(segments) < 2 {
		return false
	}
	firstMeaningful := ""
	lastMeaningful := ""
	for _, segment := range segments {
		if segment == "" || segment == "." || segment == ".." {
			continue
		}
		if firstMeaningful == "" {
			firstMeaningful = segment
		}
		lastMeaningful = segment
	}
	if firstMeaningful == "" || lastMeaningful == "" {
		return false
	}
	if strings.Contains(lastMeaningful, ".") {
		return hasAllowedArtifactExtension(lastMeaningful)
	}
	if hasExplicitPrefix {
		return true
	}
	_, ok := extensionlessArtifactRootSegments[strings.ToLower(firstMeaningful)]
	return ok
}

func hasAllowedArtifactExtension(value string) bool {
	lastDot := strings.LastIndex(value, ".")
	if lastDot <= 0 || lastDot == len(value)-1 {
		return false
	}
	_, ok := artifactFileExtensions[strings.ToLower(value[lastDot+1:])]
	return ok
}

func memoryCandidateKey(scope domtypes.MemoryScope, memoryType domtypes.MemoryType, fact string) string {
	scopeKey := ""
	if scope != nil {
		scopeKey = fmt.Sprintf("%s:%s", scope.Kind(), scope.Key())
	}
	return fmt.Sprintf("%s|%s|%s", scopeKey, memoryType, strings.ToLower(strings.TrimSpace(fact)))
}
