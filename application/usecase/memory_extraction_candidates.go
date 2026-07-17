package usecase

import (
	"slices"
	"strings"
	"unicode/utf8"

	domtypes "github.com/duck8823/traceary/domain/types"
)

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
