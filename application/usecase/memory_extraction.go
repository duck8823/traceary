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
	explicitMemoryLabelPattern         = regexp.MustCompile(`(?i)^(?:[-*]\s+|\d+\.\s+)?(?:(?:user\s+)?(preference|decision|constraint|lesson|artifact|feedback|correction))s?\s*[:\-]\s*(.+)$`)
	explicitJapaneseMemoryLabelPattern = regexp.MustCompile(`^(?:[-*]\s+|\d+\.\s+)?(好み|設定|要望|決定|判断|制約|教訓|学び|成果物|資料|修正|フィードバック)\s*[:：\-ー]\s*(.+)$`)
	explicitRememberLabelPattern       = regexp.MustCompile(`(?i)^(?:[-*]\s+|\d+\.\s+)?(?:durable\s+memory|memory\s+note|remember(?:\s+this)?|keep\s+this\s+in\s+memory)\s*[:\-]\s*(.+)$`)
	explicitJapaneseRememberPattern    = regexp.MustCompile(`^(?:[-*]\s+|\d+\.\s+)?(?:覚えておいて|覚えておく|記憶)\s*[:：\-ー]\s*(.+)$`)
	urlRefPattern                      = regexp.MustCompile(`https?://[^\s)]+`)
	issueRefPattern                    = regexp.MustCompile(`(?i)\bissues?\s*#(\d+)\b`)
	prRefPattern                       = regexp.MustCompile(`(?i)\b(?:pr|pull request)\s*#(\d+)\b`)
	bareFileRefPattern                 = regexp.MustCompile(`(?i)\b[A-Za-z0-9_.-]+\.(?:[A-Za-z0-9_-]+\.)*(?:go|md|json|sh|sql|yaml|yml|toml|ts|tsx|js|jsx|py|rb|ini|cfg|conf|proto|tpl)\b`)
	pathLikeRefPattern                 = regexp.MustCompile(`(?:\./|\.\./|/)?(?:[A-Za-z0-9_.-]+/)+[A-Za-z0-9_.-]+(?:\.[A-Za-z0-9_-]+)*`)
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

	bestSpecs := make(map[string]memoryCandidateSpec, len(specs))
	orderedKeys := make([]string, 0, len(specs))
	for _, spec := range specs {
		key := memoryCandidateKey(scope, spec.memoryType, sanitizeCandidateFact(spec.fact, extraRedactors))
		if _, exists := existingKeys[key]; exists {
			continue
		}
		current, exists := bestSpecs[key]
		if exists && current.signalScore >= spec.signalScore {
			continue
		}
		if !exists {
			orderedKeys = append(orderedKeys, key)
		}
		bestSpecs[key] = spec
	}

	results := make([]apptypes.MemoryDetails, 0, min(criteria.CandidateLimit(), len(bestSpecs)))
	for _, key := range orderedKeys {
		spec, ok := bestSpecs[key]
		if !ok {
			continue
		}
		// Signal-quality classifier (#835): low-score candidates are
		// still persisted for audit, but tagged with the extracted-hidden
		// source so the default inbox view skips them. `memory inbox
		// --include-hidden` surfaces them when reviewers want to triage
		// borderline cases.
		source := domtypes.MemorySourceExtracted
		if spec.signalScore < extractionVisibleScoreThreshold {
			source = domtypes.MemorySourceExtractedHidden
		}

		details, err := u.memory.Propose(
			ctx,
			spec.memoryType,
			scope,
			spec.fact,
			source,
			spec.evidenceRefs,
			spec.artifactRefs,
		)
		if err != nil {
			return nil, xerrors.Errorf("failed to propose extracted memory candidate: %w", err)
		}
		results = append(results, details)
		if len(results) >= criteria.CandidateLimit() {
			break
		}
	}

	return results, nil
}

func (u *memoryExtractionUsecase) Explain(ctx context.Context, criteria apptypes.MemoryExtractionCriteria) (apptypes.MemoryExtractionDebugReport, error) {
	if u.sessionQuery == nil {
		return apptypes.MemoryExtractionDebugReport{}, xerrors.Errorf("session query service is not configured")
	}
	if u.eventQuery == nil {
		return apptypes.MemoryExtractionDebugReport{}, xerrors.Errorf("event query service is not configured")
	}
	if u.memory == nil {
		return apptypes.MemoryExtractionDebugReport{}, xerrors.Errorf("memory usecase is not configured")
	}
	if criteria.SessionID().String() == "" {
		return apptypes.MemoryExtractionDebugReport{}, xerrors.Errorf("session ID must not be empty")
	}
	if criteria.EventLimit() < 0 {
		return apptypes.MemoryExtractionDebugReport{}, xerrors.Errorf("event limit must be greater than or equal to 0")
	}
	if criteria.CandidateLimit() <= 0 {
		return apptypes.MemoryExtractionDebugReport{}, xerrors.Errorf("candidate limit must be greater than or equal to 1")
	}

	session, ok, err := u.findTargetSession(ctx, criteria)
	if err != nil {
		return apptypes.MemoryExtractionDebugReport{}, err
	}
	if !ok {
		return apptypes.MemoryExtractionDebugReport{}, nil
	}
	scope := extractedMemoryScope(session)
	extraRedactors, err := redaction.CompileExtraPatterns(u.extraRedactPatterns)
	if err != nil {
		return apptypes.MemoryExtractionDebugReport{}, xerrors.Errorf("failed to compile extraction redaction patterns: %w", err)
	}
	existingKeys, err := u.loadExistingCandidateKeys(ctx, scope, extraRedactors)
	if err != nil {
		return apptypes.MemoryExtractionDebugReport{}, err
	}
	signals, err := u.collectExtractionSignals(ctx, session, criteria.EventLimit())
	if err != nil {
		return apptypes.MemoryExtractionDebugReport{}, err
	}

	type candidateDecision struct {
		segmentIndex int
		key          string
		score        int
	}

	report := apptypes.MemoryExtractionDebugReport{SessionID: session.SessionID(), Workspace: session.Workspace()}
	candidates := make([]candidateDecision, 0)
	bestCandidateByKey := make(map[string]int)
	for _, signal := range signals {
		evidenceRefs, err := buildSignalEvidenceRefs(session.SessionID(), signal.event)
		if err != nil {
			return apptypes.MemoryExtractionDebugReport{}, err
		}
		for _, segment := range candidateSegments(signal.text) {
			decision := apptypes.MemoryExtractionSegmentDecision{
				Text:         segment,
				Client:       signal.client,
				EventKind:    signal.kind,
				SourceHook:   signal.sourceHook,
				EvidenceRefs: slices.Clone(evidenceRefs),
			}
			spec, ok, err := extractBestMemoryCandidateFromSegment(signal, segment, evidenceRefs)
			if err != nil {
				return apptypes.MemoryExtractionDebugReport{}, err
			}
			if !ok {
				decision.Decision = "ignored"
				decision.Reason = "no_memory_intent"
				report.Segments = append(report.Segments, decision)
				continue
			}
			decision.MemoryType = spec.memoryType
			decision.Features = spec.intent.featureNames()
			decision.Score = spec.signalScore
			decision.ArtifactRefs = slices.Clone(spec.artifactRefs)
			segmentIndex := len(report.Segments)
			report.Segments = append(report.Segments, decision)
			key := memoryCandidateKey(scope, spec.memoryType, sanitizeCandidateFact(spec.fact, extraRedactors))
			candidateIndex := len(candidates)
			candidates = append(candidates, candidateDecision{segmentIndex: segmentIndex, key: key, score: spec.signalScore})
			bestIndex, exists := bestCandidateByKey[key]
			if !exists || candidates[bestIndex].score < spec.signalScore {
				bestCandidateByKey[key] = candidateIndex
			}
		}
	}

	emittedCandidates := 0
	for index, candidate := range candidates {
		decision := &report.Segments[candidate.segmentIndex]
		if _, exists := existingKeys[candidate.key]; exists {
			decision.Decision = "skipped"
			decision.Reason = "duplicate"
			continue
		}
		if bestCandidateByKey[candidate.key] != index {
			decision.Decision = "skipped"
			decision.Reason = "duplicate_in_run"
			continue
		}
		if emittedCandidates >= criteria.CandidateLimit() {
			decision.Decision = "skipped"
			decision.Reason = "candidate_limit"
			continue
		}
		emittedCandidates++
		if candidate.score < extractionVisibleScoreThreshold {
			decision.Decision = "hidden"
			decision.Reason = "below_visible_threshold"
			continue
		}
		decision.Decision = "proposed"
		decision.Reason = "visible_candidate"
	}
	return report, nil
}

// extractionQualityMinRunesLatin contributes to the signal-quality score for
// Latin-script candidates. Length alone no longer decides visibility (#835);
// it is just one weak signal combined with labels, evidence, and artifacts.
const extractionQualityMinRunesLatin = 20

// extractionQualityMinRunesCJK is the lower length score threshold applied
// when the fact contains CJK ideographs / kana / hangul. CJK text is much
// information-denser per rune than Latin text.
const extractionQualityMinRunesCJK = 10

const extractionVisibleScoreThreshold = 4

func memoryExtractionSignalScore(spec memoryCandidateSpec) int {
	if spec.memoryType == domtypes.MemoryTypeArtifact {
		return extractionVisibleScoreThreshold
	}

	score := 0
	trimmed := strings.TrimSpace(spec.fact)
	runes := []rune(trimmed)
	threshold := extractionQualityMinRunesLatin
	if containsCJK(runes) {
		threshold = extractionQualityMinRunesCJK
	}
	hasSufficientLength := len(runes) >= threshold

	if spec.intent.explicitRemember {
		score += 5
	}
	if spec.intent.futureApplicability {
		score += 2
	}
	if (spec.intent.userPreference || spec.intent.userCorrection || spec.intent.operationalConstraint || spec.intent.recurringWorkflow) && (spec.intent.explicitRemember || hasSufficientLength) {
		score += 2
	}
	if spec.structured {
		score += 3
	}
	if len(spec.evidenceRefs) > 0 {
		score++
	}
	if len(spec.artifactRefs) > 0 {
		score++
	}

	if hasSufficientLength {
		score++
	}
	if len(runes) >= threshold*2 {
		score++
	}
	if hasDurableSignalMarker(trimmed) {
		score += 2
	}

	return score
}

type memoryIntentFeatures struct {
	explicitRemember      bool
	futureApplicability   bool
	userPreference        bool
	userCorrection        bool
	operationalConstraint bool
	recurringWorkflow     bool
}

func classifyMemoryIntent(value string) memoryIntentFeatures {
	lower := strings.ToLower(strings.TrimSpace(value))
	features := memoryIntentFeatures{}
	if explicitRememberLabelPattern.MatchString(value) || explicitJapaneseRememberPattern.MatchString(value) || containsAny(lower,
		"remember this",
		"remember to",
		"keep this in memory",
		"durable memory",
		"覚えておいて",
		"覚えておく",
		"記憶して",
	) {
		features.explicitRemember = true
	}
	if containsAny(lower, "next time", "from now on", "going forward", "future", "今後", "以後", "次回", "次から", "これから") {
		features.futureApplicability = true
	}
	if containsAny(lower, "prefer ", "preference", "please ", "respond in", "use english", "use japanese", "好み", "設定", "要望", "してほしい") {
		features.userPreference = true
	}
	if containsAny(lower, "correction", "instead of", "rather than", "not ", "違う", "ではなく", "修正", "訂正") {
		features.userCorrection = true
	}
	if containsAny(lower, "must ", "must not", "never ", "always ", "required", "forbidden", "only ", "cannot ", "can't ", "必須", "必要", "禁止", "不可", "常に", "必ず") {
		features.operationalConstraint = true
	}
	if containsAny(lower, "review", "merge", "release", "issue", "pr ", "pull request", "poll", "polling", "dogfood", "workflow", "レビュー", "マージ", "リリース", "ポーリング", "起票") {
		features.recurringWorkflow = true
	}
	return features
}

func (f memoryIntentFeatures) featureNames() []string {
	names := make([]string, 0, 6)
	if f.explicitRemember {
		names = append(names, "explicit_remember")
	}
	if f.futureApplicability {
		names = append(names, "future_applicability")
	}
	if f.userPreference {
		names = append(names, "user_preference")
	}
	if f.userCorrection {
		names = append(names, "user_correction")
	}
	if f.operationalConstraint {
		names = append(names, "operational_constraint")
	}
	if f.recurringWorkflow {
		names = append(names, "recurring_workflow")
	}
	return names
}

func hasDurableSignalMarker(value string) bool {
	lower := strings.ToLower(value)
	return containsAny(lower,
		"decision:",
		"constraint:",
		"lesson:",
		"artifact:",
		"preference:",
		"decided ",
		"agreed ",
		"must ",
		"must not",
		"never ",
		"always ",
		"next time",
		"remember to",
		"remember this",
		"durable memory",
		"keep this in memory",
		"決定",
		"判断",
		"制約",
		"教訓",
		"学び",
		"必須",
		"必要",
		"次回",
		"再起動",
		"確認済み",
		"覚えておいて",
		"覚えておく",
		"記憶",
		"今後",
		"以後",
	)
}

// containsCJK reports whether the rune slice carries at least one
// Chinese / Japanese / Korean character. The set covers the common
// Unicode blocks for these scripts; punctuation and ASCII intermixed
// with CJK still counts as a CJK fact.
func containsCJK(runes []rune) bool {
	for _, r := range runes {
		switch {
		case r >= 0x3040 && r <= 0x309F: // Hiragana
			return true
		case r >= 0x30A0 && r <= 0x30FF: // Katakana
			return true
		case r >= 0x3400 && r <= 0x4DBF: // CJK Unified Ideographs Extension A
			return true
		case r >= 0x4E00 && r <= 0x9FFF: // CJK Unified Ideographs
			return true
		case r >= 0xAC00 && r <= 0xD7AF: // Hangul Syllables
			return true
		case r >= 0xF900 && r <= 0xFAFF: // CJK Compatibility Ideographs
			return true
		}
	}
	return false
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
		false,
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
	client          domtypes.Client
	kind            domtypes.EventKind
	sourceHook      string
}

func (u *memoryExtractionUsecase) collectCandidateSpecs(ctx context.Context, session apptypes.SessionSummary, eventLimit int) ([]memoryCandidateSpec, error) {
	signals, err := u.collectExtractionSignals(ctx, session, eventLimit)
	if err != nil {
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

func (u *memoryExtractionUsecase) collectExtractionSignals(ctx context.Context, session apptypes.SessionSummary, eventLimit int) ([]extractionSignal, error) {
	signals := make([]extractionSignal, 0, 1+5*max(eventLimit, 1))
	if summary := strings.TrimSpace(session.Summary()); summary != "" {
		signals = append(signals, extractionSignal{
			text:            summary,
			heuristics:      true,
			allowStructured: true,
			kind:            domtypes.EventKind("session_summary"),
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
				client:          event.Client(),
				kind:            event.Kind(),
				sourceHook:      event.SourceHook(),
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
	if err := appendKindSignals(domtypes.EventKindCompactSummary, true, true); err != nil {
		return nil, err
	}

	return signals, nil
}

type memoryCandidateSpec struct {
	memoryType   domtypes.MemoryType
	fact         string
	evidenceRefs []domtypes.EvidenceRef
	artifactRefs []domtypes.ArtifactRef
	structured   bool
	intent       memoryIntentFeatures
	signalScore  int
}

func extractMemoryCandidatesFromSignal(signal extractionSignal, evidenceRefs []domtypes.EvidenceRef) ([]memoryCandidateSpec, error) {
	segments := candidateSegments(signal.text)
	specs := make([]memoryCandidateSpec, 0, len(segments))
	for _, segment := range segments {
		spec, ok, err := extractBestMemoryCandidateFromSegment(signal, segment, evidenceRefs)
		if err != nil {
			return nil, err
		}
		if ok {
			specs = append(specs, spec)
		}
	}
	return specs, nil
}

func extractBestMemoryCandidateFromSegment(signal extractionSignal, segment string, evidenceRefs []domtypes.EvidenceRef) (memoryCandidateSpec, bool, error) {
	if signal.allowStructured {
		spec, ok, err := structuredCandidateSpec(segment, evidenceRefs)
		if err != nil {
			return memoryCandidateSpec{}, false, err
		}
		if ok {
			return spec, true, nil
		}
	}

	if signal.heuristics {
		spec, ok, err := heuristicCandidateSpec(segment, evidenceRefs)
		if err != nil {
			return memoryCandidateSpec{}, false, err
		}
		if ok {
			return spec, true, nil
		}
	}
	return memoryCandidateSpec{}, false, nil
}

func structuredCandidateSpec(segment string, evidenceRefs []domtypes.EvidenceRef) (memoryCandidateSpec, bool, error) {
	matches := explicitMemoryLabelPattern.FindStringSubmatch(segment)
	if len(matches) == 3 {
		memoryType, err := memoryTypeFromLabel(matches[1])
		if err != nil {
			return memoryCandidateSpec{}, false, err
		}
		return buildMemoryCandidateSpec(memoryType, normalizeCandidateFact(matches[2]), evidenceRefs, true)
	}
	matches = explicitJapaneseMemoryLabelPattern.FindStringSubmatch(segment)
	if len(matches) == 3 {
		memoryType, err := memoryTypeFromLabel(matches[1])
		if err != nil {
			return memoryCandidateSpec{}, false, err
		}
		return buildMemoryCandidateSpec(memoryType, normalizeCandidateFact(matches[2]), evidenceRefs, true)
	}

	fact, ok := stripExplicitRememberLabel(segment)
	if !ok {
		return memoryCandidateSpec{}, false, nil
	}
	spec, ok, err := buildMemoryCandidateSpec(inferMemoryTypeForExplicitIntent(fact), fact, evidenceRefs, true)
	if !ok || err != nil {
		return spec, ok, err
	}
	spec.intent.explicitRemember = true
	spec.signalScore = memoryExtractionSignalScore(spec)
	return spec, true, nil
}

func buildMemoryCandidateSpec(memoryType domtypes.MemoryType, fact string, evidenceRefs []domtypes.EvidenceRef, structured bool) (memoryCandidateSpec, bool, error) {
	fact = normalizeCandidateFact(fact)
	if fact == "" {
		return memoryCandidateSpec{}, false, nil
	}
	artifactRefs, err := inferArtifactRefs(fact)
	if err != nil {
		return memoryCandidateSpec{}, false, err
	}
	intent := classifyMemoryIntent(fact)
	spec := memoryCandidateSpec{
		memoryType:   memoryType,
		fact:         fact,
		evidenceRefs: slices.Clone(evidenceRefs),
		artifactRefs: artifactRefs,
		structured:   structured,
		intent:       intent,
	}
	spec.signalScore = memoryExtractionSignalScore(spec)
	return spec, true, nil
}

func stripExplicitRememberLabel(segment string) (string, bool) {
	if matches := explicitRememberLabelPattern.FindStringSubmatch(segment); len(matches) == 2 {
		return normalizeCandidateFact(matches[1]), true
	}
	if matches := explicitJapaneseRememberPattern.FindStringSubmatch(segment); len(matches) == 2 {
		return normalizeCandidateFact(matches[1]), true
	}
	return "", false
}

func inferMemoryTypeForExplicitIntent(fact string) domtypes.MemoryType {
	if memoryType, ok := inferMemoryTypeFromText(fact); ok {
		return memoryType
	}
	if refs, err := inferArtifactRefs(fact); err == nil && len(refs) > 0 {
		return domtypes.MemoryTypeArtifact
	}
	return domtypes.MemoryTypeLesson
}

func heuristicCandidateSpec(segment string, evidenceRefs []domtypes.EvidenceRef) (memoryCandidateSpec, bool, error) {
	fact := normalizeCandidateFact(segment)
	if fact == "" {
		return memoryCandidateSpec{}, false, nil
	}

	intent := classifyMemoryIntent(fact)
	memoryType, ok := inferMemoryTypeFromText(fact)
	if !ok && intent.explicitRemember {
		memoryType = inferMemoryTypeForExplicitIntent(fact)
		ok = true
	}
	if !ok {
		return memoryCandidateSpec{}, false, nil
	}

	spec, ok, err := buildMemoryCandidateSpec(memoryType, fact, evidenceRefs, false)
	if !ok || err != nil {
		return spec, ok, err
	}
	spec.intent = intent
	spec.signalScore = memoryExtractionSignalScore(spec)
	return spec, true, nil
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
	case "好み", "設定", "要望", "修正", "フィードバック":
		return domtypes.MemoryTypePreference, nil
	case "decision":
		return domtypes.MemoryTypeDecision, nil
	case "決定", "判断":
		return domtypes.MemoryTypeDecision, nil
	case "constraint":
		return domtypes.MemoryTypeConstraint, nil
	case "制約":
		return domtypes.MemoryTypeConstraint, nil
	case "lesson":
		return domtypes.MemoryTypeLesson, nil
	case "教訓", "学び":
		return domtypes.MemoryTypeLesson, nil
	case "artifact":
		return domtypes.MemoryTypeArtifact, nil
	case "成果物", "資料":
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
		"してください",
		"してほしい",
		"常に",
		"必ず",
	):
		return domtypes.MemoryTypePreference, true
	case containsAny(lower,
		"decision:",
		"decided ",
		"we chose",
		"chosen ",
		"agreed ",
		"not tech-debt",
		"not technical debt",
		"not be treated as tech-debt",
		"決定",
		"判断",
		"採用",
		"ではない",
		"扱わない",
		"扱う",
		"済み",
	):
		return domtypes.MemoryTypeDecision, true
	case containsAny(lower,
		"must ",
		"must not",
		"required",
		"cannot ",
		"can't ",
		"only ",
		"forbidden",
		"out of scope",
		"必須",
		"必要",
		"不可",
		"できない",
		"してはいけない",
		"禁止",
	):
		return domtypes.MemoryTypeConstraint, true
	case containsAny(lower,
		"lesson:",
		"learned ",
		"next time",
		"remember to",
		"remember this",
		"durable memory",
		"keep this in memory",
		"mistake",
		"教訓",
		"学び",
		"次回",
		"再起動",
		"restart",
		"解消",
		"確認済み",
		"覚えておいて",
		"覚えておく",
		"記憶",
		"今後",
		"以後",
		"誤り",
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
