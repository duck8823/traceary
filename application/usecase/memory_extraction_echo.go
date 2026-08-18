package usecase

import (
	"encoding/json"
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"
)

const (
	extractionNoiseInstructionEcho     = "instruction_echo"
	extractionNoiseMidSentenceFragment = "mid_sentence_fragment"
	extractionNoisePayloadEcho         = "payload_echo"
)

var (
	listMarkerPattern = regexp.MustCompile(`^(?:\d{1,3}[.)]\s+|[-*•]\s+)`)

	// Imperative agent-rule openers after a list marker. Deliberately a
	// closed verb/cue set so declarative numbered facts stay visible.
	instructionEchoEnglishPattern = regexp.MustCompile(`(?i)^(?:` +
		`always|never|do\s+not|don't|must|should|shall|` +
		`read|write|use|avoid|ensure|keep|make\s+sure|prefer|` +
		`run|call|invoke|pass|set|add|remove|delete|update|create|` +
		`check|verify|confirm|ask|tell|remember|forget|ignore|` +
		`follow|apply|treat|consider|assume|include|exclude` +
		`)\b`)

	instructionEchoJapanesePattern = regexp.MustCompile(
		`(?:してください|してはいけない|するな|すること|すること。|必ず|禁止)`)

	continuationWordPattern = regexp.MustCompile(`(?i)^(?:` +
		`and|but|or|so|which|that|because|then|also|however|` +
		`while|although|though|since|unless|until|after|before|` +
		`when|where|who|whom|whose|than|as|if` +
		`)\b`)
)

func isInstructionEcho(fact string) bool {
	trimmed := strings.TrimSpace(fact)
	if trimmed == "" {
		return false
	}
	loc := listMarkerPattern.FindStringIndex(trimmed)
	if loc == nil {
		return false
	}
	rest := strings.TrimSpace(trimmed[loc[1]:])
	if rest == "" {
		return false
	}
	if instructionEchoEnglishPattern.MatchString(rest) {
		return true
	}
	return instructionEchoJapanesePattern.MatchString(rest)
}

func isMidSentenceFragment(fact string) bool {
	trimmed := strings.TrimSpace(fact)
	if trimmed == "" {
		return false
	}
	if hasDurableSignalMarker(trimmed) {
		return false
	}
	first, size := utf8.DecodeRuneInString(trimmed)
	if first == utf8.RuneError && size == 0 {
		return false
	}
	switch first {
	case 'を', 'に', 'は', 'が', 'で', 'と', 'も', 'へ', 'や':
		return true
	}
	if strings.HasPrefix(trimmed, "より") {
		return true
	}
	if !unicode.IsLower(first) {
		return false
	}
	return continuationWordPattern.MatchString(trimmed)
}

func isPayloadEcho(fact string) bool {
	trimmed := strings.TrimSpace(fact)
	if trimmed == "" {
		return false
	}
	if looksLikeJSONValue(trimmed) {
		return true
	}
	return jsonLiteralDominates(trimmed)
}

func looksLikeJSONValue(value string) bool {
	if !strings.HasPrefix(value, "{") && !strings.HasPrefix(value, "[") {
		return false
	}
	var decoded any
	if json.Unmarshal([]byte(value), &decoded) != nil {
		return false
	}
	switch decoded.(type) {
	case map[string]any, []any:
		return true
	default:
		return false
	}
}

func jsonLiteralDominates(value string) bool {
	start := strings.IndexAny(value, "{[")
	if start < 0 {
		return false
	}
	for end := len(value); end > start+1; end-- {
		candidate := strings.TrimSpace(value[start:end])
		if !looksLikeJSONValue(candidate) {
			continue
		}
		return len(candidate)*2 >= len(strings.TrimSpace(value))
	}
	return false
}

// hidesDespiteRememberIntent reports noise classes that stay extracted-hidden
// even when the user said "remember that". The remember trigger fired, but
// the stored body is not a durable fact (#2112).
func hidesDespiteRememberIntent(reasons []string) bool {
	for _, reason := range reasons {
		if reason == extractionNoisePayloadEcho {
			return true
		}
	}
	return false
}

func shouldHideLowQuality(reasons []string, explicitRemember bool) bool {
	if len(reasons) == 0 {
		return false
	}
	if !explicitRemember {
		return true
	}
	return hidesDespiteRememberIntent(reasons)
}
