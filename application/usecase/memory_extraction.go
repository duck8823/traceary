package usecase

import (
	"context"
	"fmt"
	"regexp"
	"slices"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

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
	explicitRememberLabelPattern       = regexp.MustCompile(`(?i)^(?:[-*]\s+|\d+\.\s+)?(?:durable\s+memory|memory\s+note|remember(?:\s+(?:this|that))?|keep\s+this\s+in\s+(?:memory|mind))\s*[:\-]\s*(.+)$`)
	explicitJapaneseRememberPattern    = regexp.MustCompile(`^(?:[-*]\s+|\d+\.\s+)?(?:覚えておいて(?:ください)?|おぼえておいて(?:ください)?|覚えておく|覚えてください|記憶しておいて(?:ください)?|記憶して(?:ください)?|記憶)\s*[:：\-ー]\s*(.+)$`)
	urlRefPattern                      = regexp.MustCompile(`https?://[^\s)]+`)
	issueRefPattern                    = regexp.MustCompile(`(?i)\bissues?\s*#(\d+)\b`)
	prRefPattern                       = regexp.MustCompile(`(?i)\b(?:pr|pull request)\s*#(\d+)\b`)
	bareFileRefPattern                 = regexp.MustCompile(`(?i)\b[A-Za-z0-9_.-]+\.(?:[A-Za-z0-9_-]+\.)*(?:go|md|json|sh|sql|yaml|yml|toml|ts|tsx|js|jsx|py|rb|ini|cfg|conf|proto|tpl)\b`)
	pathLikeRefPattern                 = regexp.MustCompile(`(?:\./|\.\./|/)?(?:[A-Za-z0-9_.-]+/)+[A-Za-z0-9_.-]+(?:\.[A-Za-z0-9_-]+)*`)
)

var (
	rememberIntentEnglishTriggers = []string{
		"keep this in memory",
		"keep this in mind",
		"remember this",
		"remember that",
	}
	rememberIntentJapaneseTriggers = []string{
		"記憶しておいてください",
		"おぼえておいてください",
		"覚えておいてください",
		"記憶しておいて",
		"おぼえておいて",
		"覚えてください",
		"覚えておいて",
	}
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
		//
		// Noise classifier (#857): high-score candidates that match the
		// deterministic noise rules (diff fragments, standalone commands,
		// generated-code markers, review-only conclusions, work
		// declarations, PR/Round chatter) are also routed to
		// extracted-hidden. The explicit-remember intent is exempt so
		// user-driven `remember this:` prompts always remain visible.
		source := spec.source
		hiddenByQuality := spec.signalScore < extractionVisibleScoreThreshold ||
			(len(spec.lowQualityReasons) > 0 && !spec.intent.explicitRemember)
		if hiddenByQuality && !spec.intent.explicitRemember {
			// Preserve audit-first gating for every auto-extracted source,
			// including compact-summary. Source-specific candidates should not
			// bypass the low-quality/noise routing just because their source was
			// assigned before this final persistence step.
			source = domtypes.MemorySourceExtractedHidden
		}
		if source == "" {
			source = domtypes.MemorySourceExtracted
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
		segmentIndex      int
		key               string
		score             int
		explicitRemember  bool
		lowQualityReasons []string
	}

	report := apptypes.MemoryExtractionDebugReport{SessionID: session.SessionID(), Workspace: session.Workspace()}
	candidates := make([]candidateDecision, 0)
	bestCandidateByKey := make(map[string]int)
	seenKeys := make(map[string]struct{})
	orderedKeys := make([]string, 0)
	rememberContextSignals, err := u.collectRememberIntentContextSignals(ctx, session, criteria.EventLimit())
	if err != nil {
		return apptypes.MemoryExtractionDebugReport{}, err
	}
	rememberContextSpecs, err := collectRememberIntentContextSpecs(session.SessionID(), rememberContextSignals)
	if err != nil {
		return apptypes.MemoryExtractionDebugReport{}, err
	}
	for _, spec := range rememberContextSpecs {
		decision := apptypes.MemoryExtractionSegmentDecision{
			Text:              spec.fact,
			EventKind:         domtypes.EventKindPrompt,
			MemoryType:        spec.memoryType,
			Features:          spec.intent.featureNames(),
			Score:             spec.signalScore,
			EvidenceRefs:      slices.Clone(spec.evidenceRefs),
			ArtifactRefs:      slices.Clone(spec.artifactRefs),
			LowQualityReasons: slices.Clone(spec.lowQualityReasons),
		}
		segmentIndex := len(report.Segments)
		report.Segments = append(report.Segments, decision)
		key := memoryCandidateKey(scope, spec.memoryType, sanitizeCandidateFact(spec.fact, extraRedactors))
		if _, exists := seenKeys[key]; !exists {
			seenKeys[key] = struct{}{}
			orderedKeys = append(orderedKeys, key)
		}
		candidateIndex := len(candidates)
		candidates = append(candidates, candidateDecision{
			segmentIndex:      segmentIndex,
			key:               key,
			score:             spec.signalScore,
			explicitRemember:  spec.intent.explicitRemember,
			lowQualityReasons: slices.Clone(spec.lowQualityReasons),
		})
		bestIndex, exists := bestCandidateByKey[key]
		if !exists || candidates[bestIndex].score < spec.signalScore {
			bestCandidateByKey[key] = candidateIndex
		}
	}
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
			if signal.skipReason != "" {
				decision.Decision = "ignored"
				decision.Reason = signal.skipReason
				report.Segments = append(report.Segments, decision)
				continue
			}
			if isShortRememberIntentSegment(segment) {
				decision.Decision = "ignored"
				decision.Reason = "remember_intent_context_only"
				report.Segments = append(report.Segments, decision)
				continue
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
			decision.LowQualityReasons = slices.Clone(spec.lowQualityReasons)
			segmentIndex := len(report.Segments)
			report.Segments = append(report.Segments, decision)
			key := memoryCandidateKey(scope, spec.memoryType, sanitizeCandidateFact(spec.fact, extraRedactors))
			if _, exists := seenKeys[key]; !exists {
				seenKeys[key] = struct{}{}
				orderedKeys = append(orderedKeys, key)
			}
			candidateIndex := len(candidates)
			candidates = append(candidates, candidateDecision{
				segmentIndex:      segmentIndex,
				key:               key,
				score:             spec.signalScore,
				explicitRemember:  spec.intent.explicitRemember,
				lowQualityReasons: slices.Clone(spec.lowQualityReasons),
			})
			bestIndex, exists := bestCandidateByKey[key]
			if !exists || candidates[bestIndex].score < spec.signalScore {
				bestCandidateByKey[key] = candidateIndex
			}
		}
	}

	selectedKeys := make(map[string]struct{})
	limitSkippedKeys := make(map[string]struct{})
	emittedCandidates := 0
	for _, key := range orderedKeys {
		if _, exists := existingKeys[key]; exists {
			continue
		}
		if _, exists := bestCandidateByKey[key]; !exists {
			continue
		}
		if emittedCandidates >= criteria.CandidateLimit() {
			limitSkippedKeys[key] = struct{}{}
			continue
		}
		selectedKeys[key] = struct{}{}
		emittedCandidates++
	}

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
		if _, exists := limitSkippedKeys[candidate.key]; exists {
			decision.Decision = "skipped"
			decision.Reason = "candidate_limit"
			continue
		}
		if _, exists := selectedKeys[candidate.key]; !exists {
			decision.Decision = "skipped"
			decision.Reason = "candidate_limit"
			continue
		}
		if candidate.score < extractionVisibleScoreThreshold {
			decision.Decision = "hidden"
			decision.Reason = "below_visible_threshold"
			continue
		}
		if len(candidate.lowQualityReasons) > 0 && !candidate.explicitRemember {
			decision.Decision = "hidden"
			decision.Reason = "low_quality:" + strings.Join(candidate.lowQualityReasons, ",")
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
	if hasExplicitRememberIntentMarker(value) || containsAny(lower,
		"remember to",
		"durable memory",
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
	if hasExplicitRememberIntentMarker(value) {
		return true
	}
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
		"durable memory",
		"決定",
		"判断",
		"制約",
		"教訓",
		"学び",
		"必須",
		"必要",
		"必ず",
		"常に",
		"次回",
		"再起動",
		"確認済み",
		"今後",
		"以後",
		// Japanese preference / request markers — durable signals equivalent
		// to English "please / prefer / want X to". They override the
		// work-declaration prefix so durable preferences like
		// "これから日本語で回答してほしい" stay visible (#857).
		"してほしい",
		"してください",
		"好み",
		"要望",
		"希望",
	)
}

func hasExplicitRememberIntentMarker(value string) bool {
	normalized := normalizeCandidateFact(value)
	if normalized == "" {
		return false
	}
	if explicitRememberLabelPattern.MatchString(normalized) || explicitJapaneseRememberPattern.MatchString(normalized) {
		return true
	}
	if fact, ok := extractInlineRememberIntentFact(normalized); ok && fact != "" {
		return true
	}
	return isShortEnglishRememberIntentSegment(normalized) || isShortJapaneseRememberIntentSegment(normalized)
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
	skipReason      string
}

func (u *memoryExtractionUsecase) collectCandidateSpecs(ctx context.Context, session apptypes.SessionSummary, eventLimit int) ([]memoryCandidateSpec, error) {
	signals, err := u.collectExtractionSignals(ctx, session, eventLimit)
	if err != nil {
		return nil, err
	}

	rememberContextSignals, err := u.collectRememberIntentContextSignals(ctx, session, eventLimit)
	if err != nil {
		return nil, err
	}
	rememberSpecs, err := collectRememberIntentContextSpecs(session.SessionID(), rememberContextSignals)
	if err != nil {
		return nil, err
	}
	genericSpecs := make([]memoryCandidateSpec, 0, len(signals))
	for _, signal := range signals {
		evidenceRefs, err := buildSignalEvidenceRefs(session.SessionID(), signal.event)
		if err != nil {
			return nil, err
		}
		signalSpecs, err := extractMemoryCandidatesFromSignal(signal, evidenceRefs)
		if err != nil {
			return nil, err
		}
		for _, spec := range signalSpecs {
			if spec.source == domtypes.MemorySourceRememberIntent {
				rememberSpecs = append(rememberSpecs, spec)
				continue
			}
			genericSpecs = append(genericSpecs, spec)
		}
	}

	specs := make([]memoryCandidateSpec, 0, len(rememberSpecs)+len(genericSpecs))
	specs = append(specs, rememberSpecs...)
	specs = append(specs, genericSpecs...)
	return specs, nil
}

func collectRememberIntentContextSpecs(sessionID domtypes.SessionID, signals []extractionSignal) ([]memoryCandidateSpec, error) {
	timeline := make([]extractionSignal, 0, len(signals))
	for _, signal := range signals {
		if signal.event == nil {
			continue
		}
		if signal.kind != domtypes.EventKindPrompt && signal.kind != domtypes.EventKindTranscript {
			continue
		}
		timeline = append(timeline, signal)
	}
	sort.SliceStable(timeline, func(i, j int) bool {
		left := timeline[i].event.CreatedAt()
		right := timeline[j].event.CreatedAt()
		if left.Equal(right) {
			return timeline[i].event.EventID().String() < timeline[j].event.EventID().String()
		}
		return left.Before(right)
	})

	specs := make([]memoryCandidateSpec, 0)
	for index, signal := range timeline {
		if signal.kind != domtypes.EventKindPrompt || !isShortRememberIntentSegment(signal.text) {
			continue
		}
		contextSignal, ok := previousRememberIntentContext(timeline[:index])
		if !ok {
			continue
		}
		fact := rememberIntentContextFact(contextSignal.text)
		if fact == "" {
			continue
		}
		rememberEvidenceRefs, err := buildSignalEvidenceRefs(sessionID, signal.event)
		if err != nil {
			return nil, err
		}
		contextEvidenceRefs, err := buildSignalEvidenceRefs(sessionID, contextSignal.event)
		if err != nil {
			return nil, err
		}
		evidenceRefs := mergeEvidenceRefs(rememberEvidenceRefs, contextEvidenceRefs)
		spec, ok, err := buildMemoryCandidateSpec(inferMemoryTypeForExplicitIntent(fact), fact, evidenceRefs, false)
		if err != nil {
			return nil, err
		}
		if !ok {
			continue
		}
		spec.intent.explicitRemember = true
		spec.source = domtypes.MemorySourceRememberIntent
		spec.signalScore = memoryExtractionSignalScore(spec)
		specs = append(specs, spec)
	}
	// Reverse so the newest remember-intent prompt comes first. Extract
	// applies CandidateLimit by first-seen key, so without this older context
	// would consume the budget before more recent facts.
	slices.Reverse(specs)
	return specs, nil
}

// rememberIntentContextLookback bounds how far back a short remember-only
// prompt scans for adjacent factual context. Anything further away than the
// last few prompt/transcript turns is treated as unrelated stale chatter and
// will not be used as evidence for the candidate.
const rememberIntentContextLookback = 3

func previousRememberIntentContext(signals []extractionSignal) (extractionSignal, bool) {
	examined := 0
	for index := len(signals) - 1; index >= 0; index-- {
		signal := signals[index]
		if signal.kind != domtypes.EventKindPrompt && signal.kind != domtypes.EventKindTranscript {
			continue
		}
		examined++
		if rememberIntentContextFact(signal.text) != "" {
			return signal, true
		}
		if examined >= rememberIntentContextLookback {
			return extractionSignal{}, false
		}
	}
	return extractionSignal{}, false
}

func rememberIntentContextFact(text string) string {
	segments := candidateSegments(text)
	for index := len(segments) - 1; index >= 0; index-- {
		segment := segments[index]
		if isShortRememberIntentSegment(segment) {
			continue
		}
		if fact, ok := rememberIntentFactFromSegment(segment); ok {
			return boundRememberIntentContext(fact)
		}
		fact := normalizeCandidateFact(segment)
		if fact == "" {
			continue
		}
		if _, ok := inferMemoryTypeFromText(fact); !ok {
			continue
		}
		if len(classifyExtractionNoise(fact)) > 0 {
			continue
		}
		return boundRememberIntentContext(fact)
	}
	return ""
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
			body := apptypes.ExtractPlainBody(event.Body())
			skipReason := ""
			if kind == domtypes.EventKindCompactSummary {
				skipReason = compactSummaryExtractionSkipReason(event, body)
			}
			signals = append(signals, extractionSignal{
				text:            body,
				event:           event,
				heuristics:      heuristics,
				allowStructured: allowStructured,
				client:          event.Client(),
				kind:            event.Kind(),
				sourceHook:      event.SourceHook(),
				skipReason:      skipReason,
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

func (u *memoryExtractionUsecase) collectRememberIntentContextSignals(ctx context.Context, session apptypes.SessionSummary, eventLimit int) ([]extractionSignal, error) {
	if eventLimit == 0 {
		return nil, nil
	}
	kinds := []domtypes.EventKind{domtypes.EventKindPrompt, domtypes.EventKindTranscript}
	signals := make([]extractionSignal, 0, len(kinds)*eventLimit)
	for _, kind := range kinds {
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
			return nil, xerrors.Errorf("failed to list %s events for remember-intent context: %w", kind, err)
		}
		for _, event := range events {
			signals = append(signals, extractionSignal{
				text:       apptypes.ExtractPlainBody(event.Body()),
				event:      event,
				client:     event.Client(),
				kind:       event.Kind(),
				sourceHook: event.SourceHook(),
			})
		}
	}
	return signals, nil
}

func compactSummaryExtractionSkipReason(event *model.Event, body string) string {
	if event == nil {
		return "missing_event"
	}
	trimmed := strings.TrimSpace(body)
	if trimmed == "" {
		return "empty_summary"
	}
	if event.SourceHook() == "pre_compact" || strings.HasPrefix(trimmed, domtypes.EventBodyMarkerCompactPreSnapshot) {
		return "pre_compact_snapshot"
	}
	lower := strings.ToLower(trimmed)
	switch lower {
	case "manual", "auto", "automatic", "compact triggered", "triggered", "clear", "cleared", "context cleared", "reset", "reset context", "context reset":
		return "marker_only_context_boundary"
	default:
		return ""
	}
}

type memoryCandidateSpec struct {
	memoryType        domtypes.MemoryType
	fact              string
	source            domtypes.MemorySource
	evidenceRefs      []domtypes.EvidenceRef
	artifactRefs      []domtypes.ArtifactRef
	structured        bool
	intent            memoryIntentFeatures
	signalScore       int
	lowQualityReasons []string
}

func extractMemoryCandidatesFromSignal(signal extractionSignal, evidenceRefs []domtypes.EvidenceRef) ([]memoryCandidateSpec, error) {
	if signal.skipReason != "" {
		return nil, nil
	}
	segments := candidateSegments(signal.text)
	specs := make([]memoryCandidateSpec, 0, len(segments))
	for _, segment := range segments {
		if isShortRememberIntentSegment(segment) {
			continue
		}
		spec, ok, err := extractBestMemoryCandidateFromSegment(signal, segment, evidenceRefs)
		if err != nil {
			return nil, err
		}
		if ok {
			if shouldUseRememberIntentSource(signal, spec) {
				spec.source = domtypes.MemorySourceRememberIntent
			}
			if shouldUseCompactSummarySource(signal, spec) {
				spec.source = domtypes.MemorySourceCompactSummary
			}
			specs = append(specs, spec)
		}
	}
	return specs, nil
}

func shouldUseRememberIntentSource(signal extractionSignal, spec memoryCandidateSpec) bool {
	if !spec.intent.explicitRemember {
		return false
	}
	return signal.kind == domtypes.EventKindPrompt || signal.kind == domtypes.EventKindTranscript
}

func shouldUseCompactSummarySource(signal extractionSignal, spec memoryCandidateSpec) bool {
	if spec.source != "" {
		return false
	}
	return signal.kind == domtypes.EventKindCompactSummary
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

	fact, ok := rememberIntentFactFromSegment(segment)
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
		memoryType:        memoryType,
		fact:              fact,
		evidenceRefs:      slices.Clone(evidenceRefs),
		artifactRefs:      artifactRefs,
		structured:        structured,
		intent:            intent,
		lowQualityReasons: classifyExtractionNoise(fact),
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

func rememberIntentFactFromSegment(segment string) (string, bool) {
	if fact, ok := stripExplicitRememberLabel(segment); ok {
		return fact, true
	}
	if fact, ok := extractInlineRememberIntentFact(segment); ok {
		return fact, true
	}
	return "", false
}

func extractInlineRememberIntentFact(segment string) (string, bool) {
	normalized := normalizeCandidateFact(segment)
	if normalized == "" {
		return "", false
	}
	for _, trigger := range rememberIntentEnglishTriggers {
		searchStart := 0
		for searchStart < len(normalized) {
			index := indexFoldASCIIFrom(normalized, trigger, searchStart)
			if index < 0 {
				break
			}
			// Only treat the inline trigger as explicit remember-intent when
			// it is used imperatively (start of a clause, after a sentence
			// boundary, or after a polite softener). This rejects declarative
			// phrasing like "I remember that we already fixed this" and
			// trigger-only negations like "Don't remember this." without
			// discarding later imperative instructions in the same segment.
			if !isImperativeEnglishRememberContext(normalized, index) {
				searchStart = index + len(trigger)
				continue
			}
			fact := rememberIntentFactAroundTrigger(normalized, index, len(trigger))
			if fact != "" {
				return fact, true
			}
			searchStart = index + len(trigger)
		}
	}
	for _, trigger := range rememberIntentJapaneseTriggers {
		searchStart := 0
		for searchStart < len(normalized) {
			relativeIndex := strings.Index(normalized[searchStart:], trigger)
			if relativeIndex < 0 {
				break
			}
			index := searchStart + relativeIndex
			// Japanese remember phrases can also appear in non-imperative
			// thanks/declarative clauses ("覚えておいてくれてありがとう").
			// Require either a clause boundary, whitespace before the fact,
			// a polite short prompt, or a trigger at the end of a clause before
			// promoting the surrounding text as explicit remember intent.
			if !isImperativeJapaneseRememberContext(normalized, index, len(trigger)) {
				searchStart = index + len(trigger)
				continue
			}
			fact := rememberIntentFactAroundTrigger(normalized, index, len(trigger))
			if fact != "" {
				return fact, true
			}
			searchStart = index + len(trigger)
		}
	}
	return "", false
}

// imperativeRememberContextEndings list characters that mark a clause boundary
// before an English remember-intent trigger. When the immediately-preceding
// non-whitespace character belongs to this set, the trigger introduces a new
// imperative clause ("..., remember this." / "Stop. Remember this.").
const imperativeRememberContextEndings = ".!?;:,、。"

// imperativeRememberSoftenerTokens list polite request markers that may
// directly precede an imperative trigger ("please remember this:", "kindly
// remember that", "do remember"). They are the only non-boundary tokens that
// count as imperative context.
var imperativeRememberSoftenerTokens = map[string]struct{}{
	"please": {},
	"pls":    {},
	"kindly": {},
	"do":     {},
}

func isImperativeEnglishRememberContext(value string, index int) bool {
	before := strings.TrimRight(value[:index], " \t\r\n")
	if before == "" {
		return true
	}
	last, _ := utf8.DecodeLastRuneInString(before)
	if strings.ContainsRune(imperativeRememberContextEndings, last) {
		return true
	}
	lastSpace := strings.LastIndexAny(before, " \t")
	var lastToken string
	if lastSpace < 0 {
		lastToken = strings.ToLower(before)
	} else {
		lastToken = strings.ToLower(before[lastSpace+1:])
	}
	lastToken = strings.Trim(lastToken, "`'\"()[]{}.,:;!?")
	if _, ok := imperativeRememberSoftenerTokens[lastToken]; ok {
		return true
	}
	return false
}

const japaneseRememberFactDelimiters = ":：-ー,，.。!！?？;；「」『』()[]{}"

var japaneseRememberNonImperativeContinuations = []string{
	"くれて",
	"くれた",
	"くれる",
	"くださり",
	"くださって",
	"もらって",
	"もらえる",
	"いただいて",
	"いただき",
	"ありがとう",
	"ありがとうございます",
}

func isImperativeJapaneseRememberContext(value string, index int, triggerLength int) bool {
	suffix := value[index+triggerLength:]
	if suffix == "" || strings.TrimSpace(suffix) == "" {
		return true
	}
	if startsWithASCIIWhitespace(suffix) {
		return true
	}
	first, _ := utf8.DecodeRuneInString(suffix)
	if strings.ContainsRune(japaneseRememberFactDelimiters, first) {
		return true
	}
	for _, continuation := range japaneseRememberNonImperativeContinuations {
		if strings.HasPrefix(suffix, continuation) {
			return false
		}
	}
	for _, particle := range []string{"ね", "よ"} {
		if strings.HasPrefix(suffix, particle) {
			remainder := strings.TrimPrefix(suffix, particle)
			if remainder == "" || strings.TrimSpace(remainder) == "" {
				return true
			}
			if startsWithASCIIWhitespace(remainder) {
				return true
			}
			next, _ := utf8.DecodeRuneInString(remainder)
			return strings.ContainsRune(japaneseRememberFactDelimiters, next)
		}
	}
	return false
}

func startsWithASCIIWhitespace(value string) bool {
	if value == "" {
		return false
	}
	switch value[0] {
	case ' ', '\t', '\r', '\n':
		return true
	default:
		return false
	}
}

func indexFoldASCIIFrom(value string, needle string, start int) int {
	if needle == "" {
		return start
	}
	if start < 0 {
		start = 0
	}
	if start > len(value) {
		return -1
	}
	for offset := range value[start:] {
		index := start + offset
		if index+len(needle) > len(value) {
			continue
		}
		if strings.EqualFold(value[index:index+len(needle)], needle) {
			return index
		}
	}
	return -1
}

func rememberIntentFactAroundTrigger(value string, index int, triggerLength int) string {
	before := cleanRememberIntentFactRemainder(value[:index])
	after := cleanRememberIntentFactRemainder(value[index+triggerLength:])
	if after != "" {
		return after
	}
	return before
}

func cleanRememberIntentFactRemainder(value string) string {
	trimmed := strings.TrimSpace(value)
	trimmed = strings.Trim(trimmed, " \t\r\n:：-ー,，.。!！?？;；「」『』()[]{}")
	lower := strings.ToLower(trimmed)
	for _, prefix := range []string{"please ", "pls ", "that "} {
		if strings.HasPrefix(lower, prefix) {
			trimmed = strings.TrimSpace(trimmed[len(prefix):])
			lower = strings.ToLower(trimmed)
		}
	}
	trimmed = strings.TrimSpace(strings.TrimSuffix(trimmed, "を"))
	trimmed = strings.TrimSpace(strings.TrimSuffix(trimmed, "は"))
	trimmed = strings.TrimSpace(strings.TrimSuffix(trimmed, "ね"))
	// Negation-only remainders ("Don't remember this." → "Don't") would
	// otherwise be promoted to a remember-intent candidate when no factual
	// text follows the trigger. Drop them here as a second line of defense
	// behind the imperative gate.
	switch strings.ToLower(trimmed) {
	case "", "please", "pls", "do", "kindly", "this", "that", "it", "ね", "ください", "お願いします",
		"don't", "dont", "don´t", "do not", "donot",
		"never", "no", "not",
		"won't", "wont", "can't", "cant", "couldn't", "couldnt",
		"shouldn't", "shouldnt", "wouldn't", "wouldnt":
		return ""
	}
	return normalizeCandidateFact(trimmed)
}

func isShortRememberIntentSegment(segment string) bool {
	normalized := normalizeCandidateFact(segment)
	if normalized == "" {
		return false
	}
	if fact, ok := rememberIntentFactFromSegment(normalized); ok && fact != "" {
		return false
	}
	return isShortEnglishRememberIntentSegment(normalized) || isShortJapaneseRememberIntentSegment(normalized)
}

func isShortEnglishRememberIntentSegment(normalized string) bool {
	for _, trigger := range rememberIntentEnglishTriggers {
		searchStart := 0
		for searchStart < len(normalized) {
			index := indexFoldASCIIFrom(normalized, trigger, searchStart)
			if index < 0 {
				break
			}
			if isImperativeEnglishRememberContext(normalized, index) && rememberIntentTriggerHasNoFact(normalized, index, len(trigger)) {
				return true
			}
			searchStart = index + len(trigger)
		}
	}
	return false
}

func isShortJapaneseRememberIntentSegment(normalized string) bool {
	for _, trigger := range rememberIntentJapaneseTriggers {
		searchStart := 0
		for searchStart < len(normalized) {
			relativeIndex := strings.Index(normalized[searchStart:], trigger)
			if relativeIndex < 0 {
				break
			}
			index := searchStart + relativeIndex
			if isImperativeJapaneseRememberContext(normalized, index, len(trigger)) && rememberIntentTriggerHasNoFact(normalized, index, len(trigger)) {
				return true
			}
			searchStart = index + len(trigger)
		}
	}
	return false
}

func rememberIntentTriggerHasNoFact(value string, index int, triggerLength int) bool {
	before := cleanRememberIntentFactRemainder(value[:index])
	after := cleanRememberIntentFactRemainder(value[index+triggerLength:])
	return before == "" && after == ""
}

const rememberIntentContextMaxRunes = 500

func boundRememberIntentContext(value string) string {
	normalized := normalizeCandidateFact(value)
	if normalized == "" {
		return ""
	}
	runes := []rune(normalized)
	if len(runes) <= rememberIntentContextMaxRunes {
		return normalized
	}
	return string(runes[:rememberIntentContextMaxRunes])
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
		"remember that",
		"durable memory",
		"keep this in memory",
		"keep this in mind",
		"mistake",
		"教訓",
		"学び",
		"次回",
		"再起動",
		"restart",
		"解消",
		"確認済み",
		"覚えておいて",
		"おぼえておいて",
		"覚えてください",
		"覚えておく",
		"記憶しておいて",
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

func mergeEvidenceRefs(groups ...[]domtypes.EvidenceRef) []domtypes.EvidenceRef {
	refs := make([]domtypes.EvidenceRef, 0)
	seen := make(map[string]struct{})
	for _, group := range groups {
		for _, ref := range group {
			key := ref.Kind().String() + ":" + ref.Value()
			if _, ok := seen[key]; ok {
				continue
			}
			seen[key] = struct{}{}
			refs = append(refs, ref)
		}
	}
	return refs
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
