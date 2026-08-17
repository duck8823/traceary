package usecase

import (
	"regexp"
	"strings"
)

var (
	scratchPathPattern       = regexp.MustCompile(`(?i)(?:^scratch\s*:|/var/folders/)`)
	ephemeralEnvelopePattern = regexp.MustCompile(`(?i)"ephemeralMessage"\s*:`)
	// Progress narration about the current sprint/session, not a durable rule.
	// Anchored to status predicates ("already merged", "作業中") rather than
	// nouns like "Draft PR" or "worktree" that appear in standing policy.
	transientProgressPattern = regexp.MustCompile(`(?i)` +
		`作業を始めます|作業中|実装中|merge\s*済み|マージ済み|` +
		`まで出しました|open\s*のまま|すべて\s*open|子はすべて|` +
		`計画と受け入れ条件を読み|` +
		`starting\s+(?:the\s+)?work|already\s+merged`)
)

func isTransientStatus(fact string) bool {
	trimmed := strings.TrimSpace(fact)
	if trimmed == "" || hasDurableSignalMarker(trimmed) {
		return false
	}
	if scratchPathPattern.MatchString(trimmed) ||
		ephemeralEnvelopePattern.MatchString(trimmed) ||
		isMarkdownStatusTableRow(trimmed) {
		return true
	}
	return transientProgressPattern.MatchString(trimmed)
}

func isMarkdownStatusTableRow(fact string) bool {
	if !strings.HasPrefix(fact, "|") {
		return false
	}
	return strings.Contains(fact[1:], "|")
}
