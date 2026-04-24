package types

import (
	"encoding/json"
	"strings"

	"golang.org/x/xerrors"
)

// EventBodyBlockType is the stable vocabulary for block types in a
// structured event body (transcript / prompt). Unknown types are
// preserved round-trip but downstream filters may skip them.
type EventBodyBlockType string

const (
	// EventBodyBlockTypeThinking marks a block that carries the
	// assistant's internal reasoning (Claude Code "thinking" blocks,
	// extended-thinking output). Downstream consumers like memory
	// extraction usually skip these to avoid ingesting reasoning as
	// durable facts.
	EventBodyBlockTypeThinking EventBodyBlockType = "thinking"
	// EventBodyBlockTypeText marks a user-visible block — the
	// assistant's rendered reply, or the user's prompt. Readers that
	// want "what the human saw" look at text blocks only.
	EventBodyBlockTypeText EventBodyBlockType = "text"
)

// EventBodyBlock is one element of a structured event body.
type EventBodyBlock struct {
	Type EventBodyBlockType `json:"type"`
	Text string             `json:"text"`
}

// EventBodyBlocks wraps a slice so JSON encoding can stay forward
// compatible (new sibling fields can be added without breaking
// existing consumers).
type EventBodyBlocks struct {
	Blocks []EventBodyBlock `json:"blocks"`
}

// envelopeProbe distinguishes "this is our blocks envelope" from
// "this is some other JSON object" by looking for the blocks key
// presence (pointer stays nil when the key is absent). Without this
// probe a legitimate non-envelope body like `{"foo":"bar"}` would be
// silently rewritten.
type envelopeProbe struct {
	Blocks *[]EventBodyBlock `json:"blocks"`
}

// MarshalEventBodyBlocks serializes a block slice to the canonical
// JSON shape used for structured event bodies:
//
//	{"blocks":[{"type":"thinking","text":"..."},{"type":"text","text":"..."}]}
//
// Callers that produce the blocks (hook runtime, CLI log writing
// transcript) use this to pin the persisted body format.
func MarshalEventBodyBlocks(blocks []EventBodyBlock) (string, error) {
	envelope := EventBodyBlocks{Blocks: blocks}
	encoded, err := json.Marshal(envelope)
	if err != nil {
		return "", xerrors.Errorf("failed to marshal event body blocks: %w", err)
	}
	return string(encoded), nil
}

// ParseEventBodyBlocks returns the structured blocks encoded in body
// if it is JSON-shaped, and falls back to a single synthetic text
// block carrying the raw body for legacy rows (v0.8.0 and earlier)
// that predate #662. New code paths should always call this helper
// instead of inspecting body directly so legacy + new rows behave
// the same.
func ParseEventBodyBlocks(body string) []EventBodyBlock {
	trimmed := strings.TrimSpace(body)
	if trimmed == "" {
		return nil
	}
	if trimmed[0] != '{' {
		return []EventBodyBlock{{Type: EventBodyBlockTypeText, Text: body}}
	}
	var probe envelopeProbe
	if err := json.Unmarshal([]byte(trimmed), &probe); err != nil || probe.Blocks == nil {
		return []EventBodyBlock{{Type: EventBodyBlockTypeText, Text: body}}
	}
	if len(*probe.Blocks) == 0 {
		return []EventBodyBlock{{Type: EventBodyBlockTypeText, Text: body}}
	}
	return *probe.Blocks
}

// ExtractPlainBody returns the flat-text projection of a body for
// readers that have not yet been updated to consume blocks directly.
// Behaviour:
//
//   - Canonical transcript envelopes (JSON object with a "blocks"
//     key): concatenate `text`-type block contents with "\n\n";
//     `thinking` blocks are excluded so memory extraction / search
//     don't ingest internal reasoning as user-visible fact. If the
//     envelope carries no text blocks (e.g. thinking-only) the result
//     is an empty string — never the raw JSON envelope, which would
//     leak reasoning to plain-text consumers.
//   - Non-envelope JSON (e.g. a note body that legitimately stores
//     `{"foo":"bar"}`): returned unchanged so domain data isn't
//     silently discarded.
//   - Legacy plain-text bodies: returned unchanged.
//   - Malformed JSON: returned unchanged so the caller sees what's
//     actually stored rather than silently losing data.
//
// Use this at the boundary between storage and legacy consumers;
// prefer ParseEventBodyBlocks for new code that can render blocks.
func ExtractPlainBody(body string) string {
	trimmed := strings.TrimSpace(body)
	if trimmed == "" || trimmed[0] != '{' {
		return body
	}
	var probe envelopeProbe
	if err := json.Unmarshal([]byte(trimmed), &probe); err != nil || probe.Blocks == nil {
		return body
	}
	if len(*probe.Blocks) == 0 {
		return ""
	}
	parts := make([]string, 0, len(*probe.Blocks))
	for _, b := range *probe.Blocks {
		if b.Type != EventBodyBlockTypeText {
			continue
		}
		if text := strings.TrimSpace(b.Text); text == "" {
			continue
		}
		parts = append(parts, b.Text)
	}
	if len(parts) == 0 {
		// Structured body that only carries thinking / unknown
		// block types — nothing user-visible to surface.
		return ""
	}
	return strings.Join(parts, "\n\n")
}