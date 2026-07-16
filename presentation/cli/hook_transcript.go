package cli

import (
	"bufio"
	"bytes"
	"encoding/json"
	"log/slog"
	"os"
	"strings"

	apptypes "github.com/duck8823/traceary/application/types"
)

// transcriptExtractor derives the assistant-reply content for a
// single transcript hook invocation from the host-supplied stdin
// payload, as a slice of structured blocks (thinking / text).
// Implementations must return ok=false (and a nil slice) when the
// payload carries no usable reply text, so the caller can silently
// skip without logging an empty `transcript` event.
type transcriptExtractor func(payload []byte) ([]apptypes.EventBodyBlock, bool)

// transcriptExtractorFor returns the extractor registered for the
// named client. Clients without a registered extractor silently
// skip — this keeps us forward-compatible with packaged hooks that
// pass unknown client arguments during staged rollouts.
func transcriptExtractorFor(client string) (transcriptExtractor, bool) {
	switch client {
	case "claude":
		return extractClaudeTranscript, true
	case "codex":
		return extractCodexTranscript, true
	case "gemini":
		return extractGeminiTranscript, true
	case "antigravity":
		return extractAntigravityTranscript, true
	case "grok":
		return extractGrokTranscript, true
	default:
		return nil, false
	}
}

// extractClaudeTranscript resolves the Claude Code assistant turn for
// a Stop (or SessionEnd) hook payload.
//
// Preferred path: read the host `transcript_path` JSONL and keep the
// last assistant turn's thinking/text blocks.
//
// Fallback: Claude Code 2.1.x print-mode Stop payloads often race the
// JSONL flush — `transcript_path` is present but the assistant row is
// not on disk yet, while `last_assistant_message` already carries the
// final rendered reply. When the file yields no blocks, use that field
// (same shape as Codex) so non-interactive sessions still record one
// transcript event. Empty last_assistant_message (quota/error exits)
// remains a soft skip so we never invent a successful reply.
func extractClaudeTranscript(payload []byte) ([]apptypes.EventBodyBlock, bool) {
	transcriptPath := hookPayloadString(payload, "transcript_path", "")
	if transcriptPath != "" {
		if blocks, ok := readLastAssistantTranscriptBlocks(transcriptPath); ok && len(blocks) > 0 {
			return blocks, true
		}
	}
	text := strings.TrimSpace(hookPayloadString(payload, "last_assistant_message", ""))
	if text == "" {
		return nil, false
	}
	return []apptypes.EventBodyBlock{{Type: apptypes.EventBodyBlockTypeText, Text: text}}, true
}

// extractCodexTranscript reads Codex CLI's `last_assistant_message`
// field from the Stop-hook payload. Codex delivers the final turn
// as a single pre-rendered string (no thinking/text distinction on
// the host side), so we emit one `text` block for parity with the
// Claude / Gemini shapes.
func extractCodexTranscript(payload []byte) ([]apptypes.EventBodyBlock, bool) {
	text := hookPayloadString(payload, "last_assistant_message", "")
	text = strings.TrimSpace(text)
	if text == "" {
		return nil, false
	}
	return []apptypes.EventBodyBlock{{Type: apptypes.EventBodyBlockTypeText, Text: text}}, true
}

// extractGeminiTranscript reads Gemini CLI's `prompt_response` field
// from the AfterAgent-hook payload. Gemini has no Stop event; the
// closest analogue is AfterAgent, which fires once the agent has
// produced a full response and includes the response text inline.
// Gemini renders the response as a single pre-formatted string, so
// the transcript carries a single `text` block — matching the shape
// Claude / Codex expose.
func extractGeminiTranscript(payload []byte) ([]apptypes.EventBodyBlock, bool) {
	text := hookPayloadString(payload, "prompt_response", "")
	text = strings.TrimSpace(text)
	if text == "" {
		return nil, false
	}
	return []apptypes.EventBodyBlock{{Type: apptypes.EventBodyBlockTypeText, Text: text}}, true
}

// readLastAssistantTranscriptBlocks reads the JSONL transcript file
// at path and returns the ordered content blocks of the LAST
// assistant turn.
//
// Real Claude Code transcripts use an envelope shape:
//
//	{"type":"assistant", "message":{"role":"assistant","content":[...]}}
//	{"type":"user",      "message":{"role":"user",     "content":"..."}}
//	{"type":"file-history-snapshot", ...}
//
// Each assistant turn's `message.content` is an array of blocks — we
// keep `type=text` and `type=thinking` blocks (reasoning and extended
// thinking) and drop `type=tool_use` / `type=tool_result` because
// those are already captured by `command_executed` audits. The block
// order and type distinction are preserved so downstream consumers
// can render thinking collapsed / filter reasoning out of memory
// extraction.
//
// Returns ok=false for IO / parse failure so callers can silently
// skip; slog.Debug lines preserve the underlying cause for
// TRACEARY_HOOK_DEBUG-style troubleshooting without aborting the
// host's Stop hook.
func readLastAssistantTranscriptBlocks(path string) ([]apptypes.EventBodyBlock, bool) {
	file, err := os.Open(path) // #nosec G304 -- path supplied by the host Stop hook
	if err != nil {
		slog.Debug("failed to open transcript file", "path", path, "error", err)
		return nil, false
	}
	defer func() { _ = file.Close() }()

	scanner := bufio.NewScanner(file)
	// Transcript entries can carry multi-KB reasoning payloads; lift
	// the default 64KB line limit so long turns don't truncate.
	scanner.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)

	var lastAssistantBlocks []apptypes.EventBodyBlock
	var parseErrors int
	for scanner.Scan() {
		line := bytes.TrimSpace(scanner.Bytes())
		if len(line) == 0 {
			continue
		}
		var entry transcriptLine
		if err := json.Unmarshal(line, &entry); err != nil {
			parseErrors++
			continue
		}
		// Only assistant turns contribute. The envelope carries its
		// own type field; we also verify the inner message.role to
		// avoid mismatched snapshots.
		if entry.Type != "assistant" && entry.Message.Role != "assistant" {
			continue
		}
		blocks := extractAssistantBlocks(entry.Message.Content)
		if len(blocks) == 0 {
			continue
		}
		lastAssistantBlocks = blocks
	}
	if err := scanner.Err(); err != nil {
		slog.Debug("failed while scanning transcript file", "path", path, "error", err)
		return nil, false
	}
	if parseErrors > 0 && len(lastAssistantBlocks) == 0 {
		slog.Debug("transcript file had no parseable assistant entries", "path", path, "parse_errors", parseErrors)
		return nil, false
	}
	return lastAssistantBlocks, len(lastAssistantBlocks) > 0
}

// transcriptLine is one row in Claude Code's JSONL transcript. Only
// the envelope `type` and the nested `message` matter for this
// feature; everything else (timestamps, message-id, snapshots) is
// deliberately ignored.
type transcriptLine struct {
	Type    string            `json:"type"`
	Message transcriptMessage `json:"message"`
}

type transcriptMessage struct {
	Role    string              `json:"role"`
	Content []transcriptContent `json:"content"`
}

// transcriptContent covers both `text` blocks (normal assistant
// reasoning) and `thinking` blocks (extended thinking). Tool-use /
// tool-result blocks are ignored by the extractor.
type transcriptContent struct {
	Type     string `json:"type"`
	Text     string `json:"text"`
	Thinking string `json:"thinking"`
}

// extractAssistantBlocks maps a Claude Code transcript envelope's
// content array to the structured block shape Traceary persists.
// `text` blocks become `text`; `thinking` blocks become `thinking`
// so consumers can distinguish rendered reply from internal
// reasoning. tool_use / tool_result blocks are skipped because they
// are already recorded via PostToolUse / PostToolUseFailure hooks.
func extractAssistantBlocks(blocks []transcriptContent) []apptypes.EventBodyBlock {
	if len(blocks) == 0 {
		return nil
	}
	result := make([]apptypes.EventBodyBlock, 0, len(blocks))
	for _, block := range blocks {
		var blockType apptypes.EventBodyBlockType
		var text string
		switch block.Type {
		case "text":
			blockType = apptypes.EventBodyBlockTypeText
			text = strings.TrimSpace(block.Text)
		case "thinking":
			blockType = apptypes.EventBodyBlockTypeThinking
			text = strings.TrimSpace(block.Thinking)
		default:
			continue
		}
		if text == "" {
			continue
		}
		result = append(result, apptypes.EventBodyBlock{Type: blockType, Text: text})
	}
	return result
}
