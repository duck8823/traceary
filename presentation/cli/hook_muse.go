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

const museHookClient = "muse"

// newHookMuseCommand wires the native Muse Code hook protocol boundary. The
// adapter consumes the live-observed payload and delegates Traceary event
// semantics to the existing shared hook runtime. Actions match the wired set
// in integrations/muse-plugin/hooks/hooks.json (#2350 findings §4).
func (c *RootCLI) newHookMuseCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:    "muse <session-start|user-prompt-submit|pre-tool-use|post-tool-use|post-tool-use-failure|stop|pre-compact|post-compact>",
		Short:  "Runtime entrypoints for Muse Code hooks",
		Hidden: true,
	}
	cmd.AddCommand(c.newHookMuseEventCommand("session-start", c.runHookMuseSessionStart))
	cmd.AddCommand(c.newHookMuseEventCommand("user-prompt-submit", c.runHookMuseUserPromptSubmit))
	cmd.AddCommand(c.newHookMuseEventCommand("pre-tool-use", c.runHookMusePreToolUse))
	cmd.AddCommand(c.newHookMuseEventCommand("post-tool-use", c.runHookMusePostToolUse))
	cmd.AddCommand(c.newHookMuseEventCommand("post-tool-use-failure", c.runHookMusePostToolUseFailure))
	cmd.AddCommand(c.newHookMuseEventCommand("stop", c.runHookMuseStop))
	cmd.AddCommand(c.newHookMuseEventCommand("pre-compact", c.runHookMusePreCompact))
	cmd.AddCommand(c.newHookMuseEventCommand("post-compact", c.runHookMusePostCompact))
	return cmd
}

func (c *RootCLI) newHookMuseEventCommand(
	action string,
	run func(context.Context, io.Writer, io.Reader, string) error,
) *cobra.Command {
	var dbPath string
	cmd := &cobra.Command{
		Use:    action,
		Short:  "Record Muse Code " + action + " hook events",
		Hidden: true,
		Args:   noArgsLocalized(),
		RunE: func(cmd *cobra.Command, _ []string) error {
			return c.runHookDurably(cmd.Context(), "muse "+action, hookInvocationSpec{
				Command: "muse",
				Client:  museHookClient,
				Action:  action,
				DBPath:  dbPath,
			}, cmd.InOrStdin(), func(input io.Reader) error {
				return run(cmd.Context(), cmd.OutOrStdout(), input, dbPath)
			})
		},
	}
	cmd.Flags().StringVar(&dbPath, "db-path", "", dbPathFlagUsage())
	return cmd
}

func (c *RootCLI) runHookMuseSessionStart(ctx context.Context, output io.Writer, input io.Reader, dbPath string) error {
	normalized, err := normalizeMuseHookPayload(input)
	if err != nil {
		return err
	}
	if strings.TrimSpace(hookPayloadString(normalized, "session_id", "")) == "" {
		return nil
	}
	return c.runHookSession(ctx, output, bytes.NewReader(normalized), museHookClient, "start", dbPath)
}

func (c *RootCLI) runHookMuseUserPromptSubmit(ctx context.Context, _ io.Writer, input io.Reader, dbPath string) error {
	normalized, err := normalizeMuseHookPayload(input)
	if err != nil {
		return err
	}
	return c.runHookPrompt(ctx, bytes.NewReader(normalized), museHookClient, dbPath)
}

func (c *RootCLI) runHookMusePreToolUse(_ context.Context, _ io.Writer, input io.Reader, _ string) error {
	_, err := normalizeMuseHookPayload(input)
	return err
}

func (c *RootCLI) runHookMusePostToolUse(ctx context.Context, _ io.Writer, input io.Reader, dbPath string) error {
	normalized, err := normalizeMuseHookPayload(input)
	if err != nil {
		return err
	}
	return c.runHookAudit(ctx, bytes.NewReader(normalized), museHookClient, dbPath)
}

func (c *RootCLI) runHookMusePostToolUseFailure(ctx context.Context, _ io.Writer, input io.Reader, dbPath string) error {
	normalized, err := normalizeMuseHookPayload(input)
	if err != nil {
		return err
	}
	return c.runHookAudit(ctx, bytes.NewReader(normalized), museHookClient, dbPath)
}

func (c *RootCLI) runHookMuseStop(ctx context.Context, _ io.Writer, input io.Reader, dbPath string) error {
	normalized, err := normalizeMuseHookPayload(input)
	if err != nil {
		return err
	}
	return c.runHookSession(ctx, nil, bytes.NewReader(normalized), museHookClient, "stop", dbPath)
}

func (c *RootCLI) runHookMusePreCompact(ctx context.Context, _ io.Writer, input io.Reader, dbPath string) error {
	return c.runHookMuseCompact(ctx, input, "pre-compact", dbPath)
}

func (c *RootCLI) runHookMusePostCompact(ctx context.Context, _ io.Writer, input io.Reader, dbPath string) error {
	return c.runHookMuseCompact(ctx, input, "post-compact", dbPath)
}

func (c *RootCLI) runHookMuseCompact(ctx context.Context, input io.Reader, action string, dbPath string) error {
	normalized, err := normalizeMuseHookPayload(input)
	if err != nil {
		return err
	}
	normalized, err = ensureMuseCompactTrigger(normalized)
	if err != nil {
		return err
	}
	return c.runHookCompact(ctx, nil, bytes.NewReader(normalized), museHookClient, action, dbPath)
}

func ensureMuseCompactTrigger(payload []byte) ([]byte, error) {
	var normalized map[string]any
	if err := json.Unmarshal(payload, &normalized); err != nil {
		return nil, xerrors.Errorf("failed to decode normalized Muse compact payload: %w", err)
	}
	trigger, ok := normalized["trigger"].(string)
	if !ok || strings.TrimSpace(trigger) == "" {
		normalized["trigger"] = "unavailable"
	}
	encoded, err := marshalStableJSON(normalized)
	if err != nil {
		return nil, xerrors.Errorf("failed to normalize Muse compact trigger: %w", err)
	}
	return encoded, nil
}

// normalizeMuseHookPayload maps host fields into the shared snake_case hook
// envelope. Muse live fixtures were not published in #2350; both camelCase
// (Grok-shaped) and snake_case keys are accepted. Unknown fields stay at the
// host boundary.
func normalizeMuseHookPayload(input io.Reader) ([]byte, error) {
	payload, err := readHookPayload(input)
	if err != nil {
		return nil, err
	}
	if len(bytes.TrimSpace(payload)) == 0 {
		return []byte("{}"), nil
	}

	var source map[string]any
	if err := json.Unmarshal(payload, &source); err != nil {
		return nil, xerrors.Errorf("failed to decode Muse hook payload: %w", err)
	}
	normalized := map[string]any{}
	copyMuseHookField(normalized, "session_id", source, "session_id", "sessionId")
	copyMuseHookField(normalized, "cwd", source, "cwd", "workspaceRoot")
	copyMuseHookField(normalized, "transcript_path", source, "transcript_path", "transcriptPath")
	copyMuseHookField(normalized, "prompt", source, "prompt")
	copyMuseHookField(normalized, "prompt_id", source, "prompt_id", "promptId")
	copyMuseHookField(normalized, "trigger", source, "trigger", "source")
	copyMuseHookField(normalized, "tool_name", source, "tool_name", "toolName")
	copyMuseHookField(normalized, "tool_use_id", source, "tool_use_id", "toolUseId", "tool_call_id")
	copyMuseHookField(normalized, "tool_input", source, "tool_input", "toolInput")
	copyMuseHookField(normalized, "tool_response", source, "tool_response", "toolResult", "tool_output")
	copyMuseHookField(normalized, "stop_hook_active", source, "stop_hook_active", "stopHookActive")
	if errorValue, ok := source["error"]; ok {
		if message := museErrorMessage(errorValue); message != "" {
			normalized["error"] = message
		}
	}

	encoded, err := marshalStableJSON(normalized)
	if err != nil {
		return nil, xerrors.Errorf("failed to normalize Muse hook payload: %w", err)
	}
	return encoded, nil
}

func copyMuseHookField(target map[string]any, targetName string, source map[string]any, sourceNames ...string) {
	if _, exists := target[targetName]; exists {
		return
	}
	for _, sourceName := range sourceNames {
		value, ok := source[sourceName]
		if !ok || value == nil {
			continue
		}
		if stringValue, ok := value.(string); ok && strings.TrimSpace(stringValue) == "" {
			continue
		}
		target[targetName] = value
		return
	}
}

func museErrorMessage(value any) string {
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
		return "muse tool error: " + strings.TrimSpace(code)
	}
	return "unknown error"
}
