package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"strings"

	"github.com/spf13/cobra"
	"golang.org/x/xerrors"
)

const kimiHookClient = "kimi"

// newHookKimiCommand wires the native Kimi Code hook protocol boundary. The
// adapter normalizes the live-observed snake_case payload (host contract:
// docs/hooks/host-contract.json) and delegates Traceary event semantics to
// the existing shared hook runtime.
func (c *RootCLI) newHookKimiCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:    "kimi <session-start|session-end|user-prompt-submit|pre-tool-use|post-tool-use|post-tool-use-failure|stop>",
		Short:  "Runtime entrypoints for Kimi Code hooks",
		Hidden: true,
	}
	cmd.AddCommand(c.newHookKimiEventCommand("session-start", c.runHookKimiSessionStart))
	cmd.AddCommand(c.newHookKimiEventCommand("session-end", c.runHookKimiSessionEnd))
	cmd.AddCommand(c.newHookKimiEventCommand("user-prompt-submit", c.runHookKimiUserPromptSubmit))
	cmd.AddCommand(c.newHookKimiEventCommand("pre-tool-use", c.runHookKimiPreToolUse))
	cmd.AddCommand(c.newHookKimiEventCommand("post-tool-use", c.runHookKimiPostToolUse))
	cmd.AddCommand(c.newHookKimiEventCommand("post-tool-use-failure", c.runHookKimiPostToolUseFailure))
	cmd.AddCommand(c.newHookKimiEventCommand("stop", c.runHookKimiStop))
	cmd.AddCommand(c.newHookKimiEventCommand("pre-compact", c.runHookKimiPreCompact))
	cmd.AddCommand(c.newHookKimiEventCommand("post-compact", c.runHookKimiPostCompact))
	cmd.AddCommand(c.newHookKimiEventCommand("subagent-stop", c.runHookKimiSubagentStop))
	return cmd
}

func (c *RootCLI) newHookKimiEventCommand(
	action string,
	run func(context.Context, io.Reader, string) error,
) *cobra.Command {
	var dbPath string
	cmd := &cobra.Command{
		Use:    action,
		Short:  "Record Kimi Code " + action + " hook events",
		Hidden: true,
		Args:   noArgsLocalized(),
		RunE: func(cmd *cobra.Command, _ []string) error {
			return c.runHookDurably(cmd.Context(), "kimi "+action, hookInvocationSpec{
				Command: "kimi",
				Client:  kimiHookClient,
				Action:  action,
				DBPath:  dbPath,
			}, cmd.InOrStdin(), func(input io.Reader) error {
				return run(cmd.Context(), input, dbPath)
			})
		},
	}
	cmd.Flags().StringVar(&dbPath, "db-path", "", dbPathFlagUsage())
	return cmd
}

func (c *RootCLI) runHookKimiSessionStart(ctx context.Context, input io.Reader, dbPath string) error {
	normalized, err := normalizeKimiHookPayload(input)
	if err != nil {
		return err
	}
	if strings.TrimSpace(hookPayloadString(normalized, "session_id", "")) == "" {
		return nil
	}
	return c.runHookSession(ctx, nil, bytes.NewReader(normalized), kimiHookClient, "start", dbPath)
}

func (c *RootCLI) runHookKimiSessionEnd(ctx context.Context, input io.Reader, dbPath string) error {
	normalized, err := normalizeKimiHookPayload(input)
	if err != nil {
		return err
	}
	if strings.TrimSpace(hookPayloadString(normalized, "session_id", "")) == "" {
		return nil
	}
	return c.runHookSession(ctx, nil, bytes.NewReader(normalized), kimiHookClient, "end", dbPath)
}

func (c *RootCLI) runHookKimiUserPromptSubmit(ctx context.Context, input io.Reader, dbPath string) error {
	normalized, err := normalizeKimiHookPayload(input)
	if err != nil {
		return err
	}
	return c.runHookPrompt(ctx, bytes.NewReader(normalized), kimiHookClient, dbPath)
}

// PreToolUse is a subagent-start boundary for Agent tool calls and a
// validation-only boundary for everything else. Kimi's PostToolUse already
// carries both input and output, so recording a general pre-tool audit here
// would duplicate the completed audit; only Agent calls carry the
// correlating tool_call_id + tool_input.subagent_type the subagent
// parent/child attribution needs.
func (c *RootCLI) runHookKimiPreToolUse(ctx context.Context, input io.Reader, dbPath string) error {
	normalized, err := normalizeKimiHookPayload(input)
	if err != nil {
		return err
	}
	if strings.TrimSpace(hookPayloadString(normalized, "tool_name", "")) != "Agent" {
		return nil
	}
	return c.runHookSubagentStart(ctx, bytes.NewReader(normalized), kimiHookClient, dbPath)
}

// Kimi's SubagentStop carries agent_name and the subagent response but no
// tool_use_id, so the shared subagent-stop path falls back to the latest
// active child of the parent session (same semantics as Claude).
func (c *RootCLI) runHookKimiSubagentStop(ctx context.Context, input io.Reader, dbPath string) error {
	normalized, err := normalizeKimiHookPayload(input)
	if err != nil {
		return err
	}
	return c.runHookSubagentStop(ctx, bytes.NewReader(normalized), kimiHookClient, dbPath)
}

// Kimi's compact hooks expose trigger and token counts but no summary body,
// so the shared compact path records markers only (mirroring Grok).
func (c *RootCLI) runHookKimiPreCompact(ctx context.Context, input io.Reader, dbPath string) error {
	normalized, err := normalizeKimiHookPayload(input)
	if err != nil {
		return err
	}
	return c.runHookCompact(ctx, nil, bytes.NewReader(normalized), kimiHookClient, "pre-compact", dbPath)
}

func (c *RootCLI) runHookKimiPostCompact(ctx context.Context, input io.Reader, dbPath string) error {
	normalized, err := normalizeKimiHookPayload(input)
	if err != nil {
		return err
	}
	return c.runHookCompact(ctx, nil, bytes.NewReader(normalized), kimiHookClient, "post-compact", dbPath)
}

func (c *RootCLI) runHookKimiPostToolUse(ctx context.Context, input io.Reader, dbPath string) error {
	normalized, err := normalizeKimiHookPayload(input)
	if err != nil {
		return err
	}
	return c.runHookAudit(ctx, bytes.NewReader(normalized), kimiHookClient, dbPath)
}

func (c *RootCLI) runHookKimiPostToolUseFailure(ctx context.Context, input io.Reader, dbPath string) error {
	normalized, err := normalizeKimiHookPayload(input)
	if err != nil {
		return err
	}
	return c.runHookAudit(ctx, bytes.NewReader(normalized), kimiHookClient, dbPath)
}

// Kimi's Stop is a turn boundary, not a session end: the host emits a true
// SessionEnd (reason=exit), so Stop only captures the assistant transcript
// via the session wire log side channel.
func (c *RootCLI) runHookKimiStop(ctx context.Context, input io.Reader, dbPath string) error {
	normalized, err := normalizeKimiHookPayload(input)
	if err != nil {
		return err
	}
	return c.runHookTranscript(ctx, bytes.NewReader(normalized), kimiHookClient, dbPath)
}

// normalizeKimiHookPayload maps fields proven by the versioned live contract
// into the shared snake_case hook envelope. Kimi's payload is already
// snake_case, so this is mostly a pass-through with these renames:
//
//   - tool_call_id → tool_use_id (shared tool correlation field)
//   - tool_output  → tool_response (shared audit output field)
//   - agent_name   → subagent_type (shared subagent label field)
//   - error{code,message,retryable} → error (string; shared failure signal)
//   - prompt content-block array → prompt (plain text, joined)
//
// Unknown fields remain at the host boundary and unavailable events are not
// synthesized.
func normalizeKimiHookPayload(input io.Reader) ([]byte, error) {
	payload, err := readHookPayload(input)
	if err != nil {
		return nil, err
	}
	if len(bytes.TrimSpace(payload)) == 0 {
		return []byte("{}"), nil
	}

	var source map[string]any
	if err := json.Unmarshal(payload, &source); err != nil {
		return nil, xerrors.Errorf("failed to decode Kimi hook payload: %w", err)
	}
	normalized := map[string]any{}
	copyKimiHookField(normalized, "session_id", source, "session_id")
	copyKimiHookField(normalized, "cwd", source, "cwd")
	copyKimiHookField(normalized, "tool_name", source, "tool_name")
	copyKimiHookField(normalized, "tool_input", source, "tool_input")
	copyKimiHookField(normalized, "tool_use_id", source, "tool_call_id")
	copyKimiHookField(normalized, "tool_response", source, "tool_output")
	copyKimiHookField(normalized, "trigger", source, "trigger")
	copyKimiHookField(normalized, "subagent_type", source, "agent_name")
	if errorValue, ok := source["error"]; ok {
		if message := kimiErrorMessage(errorValue); message != "" {
			normalized["error"] = message
		}
	}
	if promptValue, ok := source["prompt"]; ok {
		if text := flattenKimiPrompt(promptValue); text != "" {
			normalized["prompt"] = text
		}
	}

	encoded, err := marshalStableJSON(normalized)
	if err != nil {
		return nil, xerrors.Errorf("failed to normalize Kimi hook payload: %w", err)
	}
	return encoded, nil
}

func copyKimiHookField(target map[string]any, targetName string, source map[string]any, sourceName string) {
	value, ok := source[sourceName]
	if !ok || value == nil {
		return
	}
	if stringValue, ok := value.(string); ok && strings.TrimSpace(stringValue) == "" {
		return
	}
	target[targetName] = value
}

// kimiErrorMessage flattens Kimi's PostToolUseFailure error object
// {code, message, retryable} into the shared string failure signal. The
// message is preferred; a missing/blank message falls back to the code, and
// a shapeless error still yields a fixed marker so a failure event can never
// degrade into a silent success. code/retryable are otherwise intentionally
// dropped — the shared signal has no structured fields for them.
func kimiErrorMessage(value any) string {
	if message, ok := value.(string); ok {
		if trimmed := strings.TrimSpace(message); trimmed != "" {
			return trimmed
		}
		return "unknown error"
	}
	object, ok := value.(map[string]any)
	if !ok {
		return "unknown error"
	}
	if message, _ := object["message"].(string); strings.TrimSpace(message) != "" {
		return strings.TrimSpace(message)
	}
	if code, _ := object["code"].(string); strings.TrimSpace(code) != "" {
		return "kimi tool error: " + strings.TrimSpace(code)
	}
	return "unknown error"
}

// flattenKimiPrompt renders Kimi's UserPromptSubmit prompt — a content-block
// array of {type, text} objects — as plain text. A plain-string prompt (as
// observed on SubagentStart) passes through unchanged.
func flattenKimiPrompt(value any) string {
	if text, ok := value.(string); ok {
		return strings.TrimSpace(text)
	}
	blocks, ok := value.([]any)
	if !ok {
		return ""
	}
	parts := make([]string, 0, len(blocks))
	for _, block := range blocks {
		object, ok := block.(map[string]any)
		if !ok {
			continue
		}
		if object["type"] != "text" {
			continue
		}
		if text, ok := object["text"].(string); ok && strings.TrimSpace(text) != "" {
			parts = append(parts, text)
		}
	}
	return strings.Join(parts, "\n")
}
