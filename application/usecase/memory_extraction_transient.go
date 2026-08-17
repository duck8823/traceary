package usecase

import (
	"regexp"
	"strings"
)

var (
	scratchPathPattern       = regexp.MustCompile(`(?i)(?:^scratch\s*:|/var/folders/)`)
	ephemeralEnvelopePattern = regexp.MustCompile(`(?i)"ephemeralMessage"\s*:`)
	issueOrPRRefPattern      = regexp.MustCompile(`#\d+|pull/\d+`)
	// Progress narration about the current sprint/session, not a durable rule.
	transientProgressPattern = regexp.MustCompile(`(?i)` +
		`作業を始めます|作業中|実装中|merge\s*済み|マージ済み|` +
		`draft\s*pr|出しました|open\s*のまま|すべて\s*open|子はすべて|` +
		`計画と受け入れ条件を読み|` +
		`starting\s+(?:the\s+)?work|already\s+merged|still\s+open|in\s+progress`)
	orchestrationContextPattern = regexp.MustCompile(`(?i)wave\s*\d|worktree|\bopen\b|draft`)
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
	if transientProgressPattern.MatchString(trimmed) {
		return true
	}
	refs := issueOrPRRefPattern.FindAllString(trimmed, -1)
	return len(refs) >= 2 && orchestrationContextPattern.MatchString(trimmed)
}

func isMarkdownStatusTableRow(fact string) bool {
	if !strings.HasPrefix(fact, "|") {
		return false
	}
	return strings.Contains(fact[1:], "|")
}
