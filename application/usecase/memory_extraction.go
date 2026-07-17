package usecase

import (
	"context"
	"regexp"
	"slices"
	"strings"
	"time"

	"golang.org/x/xerrors"

	"github.com/duck8823/traceary/application/queryservice"
	"github.com/duck8823/traceary/application/redaction"
	apptypes "github.com/duck8823/traceary/application/types"
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
		// Obvious code/diff fragments are dropped entirely rather than persisted
		// (#1169): they are never durable memories and previously flooded the
		// candidate inbox as extracted-hidden rows. Explicit remember-intent is
		// exempt so a user-driven `remember this: +foo` still lands.
		if !spec.intent.explicitRemember && isDroppableExtractionFragment(spec.lowQualityReasons) {
			continue
		}
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
		bestIndex, exists := bestCandidateByKey[key]
		if !exists {
			continue
		}
		// Mirror the Extract drop (#1169): obvious code/diff fragments are
		// removed before they can consume a candidate-limit slot, so this
		// accounting matches Extract, where the drop precedes the limit break.
		best := candidates[bestIndex]
		if !best.explicitRemember && isDroppableExtractionFragment(best.lowQualityReasons) {
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
		if !candidate.explicitRemember && isDroppableExtractionFragment(candidate.lowQualityReasons) {
			// Mirror the Extract drop (#1169): obvious code/diff fragments are
			// not persisted at all, so report them as dropped rather than
			// hidden. Checked before the candidate-limit and score / low-quality
			// branches to match the Extract ordering, where the drop precedes the
			// limit break and wins regardless of score. The limit-accounting loop
			// above skips these keys too, so a dropped fragment never consumes a
			// candidate-limit slot from a real candidate.
			decision.Decision = "dropped"
			decision.Reason = "fragment:" + strings.Join(candidate.lowQualityReasons, ",")
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
