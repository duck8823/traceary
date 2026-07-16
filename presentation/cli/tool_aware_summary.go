package cli

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"regexp"
	"strings"
	"unicode/utf8"
)

// toolAwareSummaryMinRunes is the body size at which tool-aware compact
// projection replaces the truncated raw payload on list/snapshot surfaces.
// Smaller bodies keep the existing truncation-only path.
const toolAwareSummaryMinRunes = 200

var (
	toolAwareEditWritePattern = regexp.MustCompile(`(?is)^\s*(Edit|Write|MultiEdit|NotebookEdit)\b`)
	toolAwareReadPattern      = regexp.MustCompile(`(?is)^\s*(Read|NotebookRead)\b`)
	toolAwarePathPattern      = regexp.MustCompile(`(?i)(?:file_path|path|target_file|AbsolutePath|TargetFile)\s*[:=]\s*["']?([^\s"',}]+)`)
	toolAwareShellPattern     = regexp.MustCompile(`(?is)^\s*(Bash|run_command|Shell)\b`)
)

// toolAwareSummary is the compact projection for a large host-tool audit body.
type toolAwareSummary struct {
	Tool           string
	Path           string
	InputRunes     int
	OutputRunes    int
	ContentSHA256  string
	Head           string
	Tail           string
	Truncated      bool
	RetrievalNote  string
}

// summarizeToolAwareCommandBody projects large Edit/Write/Read/shell audit
// bodies into structured metadata for AI-facing list/snapshot surfaces.
// Returns ok=false when the body is small or not a recognized tool shape so
// callers keep the existing truncation path. Full fidelity remains available
// via `traceary show <event_id>`.
func summarizeToolAwareCommandBody(body string, eventID string) (string, bool) {
	trimmed := strings.TrimSpace(body)
	if trimmed == "" {
		return "", false
	}
	if utf8.RuneCountInString(trimmed) < toolAwareSummaryMinRunes {
		return "", false
	}

	tool := detectToolAwareTool(trimmed)
	if tool == "" {
		return "", false
	}

	path := firstSubmatch(toolAwarePathPattern, trimmed)
	inputPart, outputPart := splitToolAwareIO(trimmed)
	sum := toolAwareSummary{
		Tool:          tool,
		Path:          path,
		InputRunes:    utf8.RuneCountInString(inputPart),
		OutputRunes:   utf8.RuneCountInString(outputPart),
		ContentSHA256: shortSHA256(trimmed),
		Head:          headRunes(trimmed, 80),
		Tail:          tailRunes(trimmed, 40),
		Truncated:     true,
		RetrievalNote: largePayloadRetrievalHint(eventID),
	}
	return formatToolAwareSummary(sum), true
}

func detectToolAwareTool(body string) string {
	if m := toolAwareEditWritePattern.FindStringSubmatch(body); len(m) > 1 {
		return m[1]
	}
	if m := toolAwareReadPattern.FindStringSubmatch(body); len(m) > 1 {
		return m[1]
	}
	if m := toolAwareShellPattern.FindStringSubmatch(body); len(m) > 1 {
		return m[1]
	}
	// Bare shell command line (no host tool name): first token.
	fields := strings.Fields(body)
	if len(fields) == 0 {
		return ""
	}
	first := fields[0]
	switch first {
	case "cat", "head", "tail", "sed", "awk", "rg", "grep", "git", "go", "make":
		return "shell"
	default:
		return ""
	}
}

func splitToolAwareIO(body string) (input, output string) {
	// Common Traceary audit formatting embeds INPUT:/OUTPUT: sections.
	upper := body
	inIdx := strings.Index(upper, "INPUT:")
	outIdx := strings.Index(upper, "OUTPUT:")
	switch {
	case inIdx >= 0 && outIdx > inIdx:
		return body[inIdx:], body[outIdx:]
	case outIdx >= 0:
		return body[:outIdx], body[outIdx:]
	case inIdx >= 0:
		return body[inIdx:], ""
	default:
		return body, ""
	}
}

func formatToolAwareSummary(sum toolAwareSummary) string {
	parts := []string{
		fmt.Sprintf("tool=%s", sum.Tool),
	}
	if sum.Path != "" {
		parts = append(parts, "path="+sum.Path)
	}
	parts = append(parts,
		fmt.Sprintf("input_runes=%d", sum.InputRunes),
		fmt.Sprintf("output_runes=%d", sum.OutputRunes),
		"sha256="+sum.ContentSHA256,
		"truncated=true",
	)
	if sum.Head != "" {
		parts = append(parts, "head="+quoteSummaryFragment(sum.Head))
	}
	if sum.Tail != "" {
		parts = append(parts, "tail="+quoteSummaryFragment(sum.Tail))
	}
	if sum.RetrievalNote != "" {
		parts = append(parts, "detail="+sum.RetrievalNote)
	}
	return strings.Join(parts, " ")
}

func quoteSummaryFragment(value string) string {
	value = strings.ReplaceAll(value, "\n", "\\n")
	value = strings.ReplaceAll(value, "\"", "'")
	return `"` + value + `"`
}

func firstSubmatch(re *regexp.Regexp, value string) string {
	m := re.FindStringSubmatch(value)
	if len(m) < 2 {
		return ""
	}
	return strings.TrimSpace(m[1])
}

func shortSHA256(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:8])
}

func headRunes(value string, n int) string {
	runes := []rune(value)
	if n <= 0 || len(runes) <= n {
		return string(runes)
	}
	return string(runes[:n]) + "…"
}

func tailRunes(value string, n int) string {
	runes := []rune(value)
	if n <= 0 || len(runes) <= n {
		return ""
	}
	return "…" + string(runes[len(runes)-n:])
}
