package cli

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"os"
	"strings"

	"github.com/spf13/cobra"
	"golang.org/x/xerrors"

	apptypes "github.com/duck8823/traceary/application/types"
)

const grokHookClient = "grok"

// newHookGrokCommand wires the native Grok Build hook protocol boundary. The
// adapter consumes the live-observed camelCase payload and delegates Traceary
// event semantics to the existing shared hook runtime.
func (c *RootCLI) newHookGrokCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:    "grok <session-start|user-prompt-submit|pre-tool-use|post-tool-use|stop|pre-compact|post-compact>",
		Short:  "Runtime entrypoints for Grok Build hooks",
		Hidden: true,
	}
	cmd.AddCommand(c.newHookGrokEventCommand("session-start", c.runHookGrokSessionStart))
	cmd.AddCommand(c.newHookGrokEventCommand("user-prompt-submit", c.runHookGrokUserPromptSubmit))
	cmd.AddCommand(c.newHookGrokEventCommand("pre-tool-use", c.runHookGrokPreToolUse))
	cmd.AddCommand(c.newHookGrokEventCommand("post-tool-use", c.runHookGrokPostToolUse))
	cmd.AddCommand(c.newHookGrokEventCommand("stop", c.runHookGrokStop))
	cmd.AddCommand(c.newHookGrokEventCommand("pre-compact", c.runHookGrokPreCompact))
	cmd.AddCommand(c.newHookGrokEventCommand("post-compact", c.runHookGrokPostCompact))
	cmd.AddCommand(c.newHookGrokTranscriptWorkerCommand())
	return cmd
}

func (c *RootCLI) runHookGrokPreCompact(ctx context.Context, input io.Reader, dbPath string) error {
	return c.runHookGrokCompact(ctx, input, "pre-compact", dbPath)
}

func (c *RootCLI) runHookGrokPostCompact(ctx context.Context, input io.Reader, dbPath string) error {
	return c.runHookGrokCompact(ctx, input, "post-compact", dbPath)
}

func (c *RootCLI) runHookGrokCompact(ctx context.Context, input io.Reader, action string, dbPath string) error {
	normalized, err := normalizeGrokHookPayload(input)
	if err != nil {
		return err
	}
	return c.runHookCompact(ctx, nil, bytes.NewReader(normalized), grokHookClient, action, dbPath)
}

func (c *RootCLI) newHookGrokEventCommand(
	action string,
	run func(context.Context, io.Reader, string) error,
) *cobra.Command {
	var dbPath string
	cmd := &cobra.Command{
		Use:    action,
		Short:  "Record Grok Build " + action + " hook events",
		Hidden: true,
		Args:   noArgsLocalized(),
		RunE: func(cmd *cobra.Command, _ []string) error {
			return c.runHookDurably(cmd.Context(), "grok "+action, hookInvocationSpec{
				Command: "grok",
				Client:  grokHookClient,
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

func (c *RootCLI) runHookGrokSessionStart(ctx context.Context, input io.Reader, dbPath string) error {
	normalized, err := normalizeGrokHookPayload(input)
	if err != nil {
		return err
	}
	if strings.TrimSpace(hookPayloadString(normalized, "session_id", "")) == "" {
		return nil
	}
	return c.runHookSession(ctx, nil, bytes.NewReader(normalized), grokHookClient, "start", dbPath)
}

func (c *RootCLI) runHookGrokUserPromptSubmit(ctx context.Context, input io.Reader, dbPath string) error {
	normalized, err := normalizeGrokHookPayload(input)
	if err != nil {
		return err
	}
	return c.runHookPrompt(ctx, bytes.NewReader(normalized), grokHookClient, dbPath)
}

// PreToolUse is intentionally a validation-only boundary. Grok's verified
// PostToolUse payload already carries both input and result, so recording here
// would duplicate the completed audit. Exit 0 with no stdout is fail-open.
func (c *RootCLI) runHookGrokPreToolUse(_ context.Context, input io.Reader, _ string) error {
	_, err := normalizeGrokHookPayload(input)
	return err
}

func (c *RootCLI) runHookGrokPostToolUse(ctx context.Context, input io.Reader, dbPath string) error {
	normalized, err := normalizeGrokHookPayload(input)
	if err != nil {
		return err
	}
	return c.runHookAudit(ctx, bytes.NewReader(normalized), grokHookClient, dbPath)
}

func (c *RootCLI) runHookGrokStop(ctx context.Context, input io.Reader, dbPath string) error {
	normalized, err := normalizeGrokHookPayload(input)
	if err != nil {
		return err
	}
	sessionID := hookPayloadString(normalized, "session_id", "")
	var transcriptErr error
	if _, ready := extractGrokTranscript(normalized); ready {
		transcriptErr = c.runHookTranscript(ctx, bytes.NewReader(normalized), grokHookClient, dbPath)
		if transcriptErr != nil {
			slog.Debug("grok stop transcript failed", "session_id", sessionID, "error", transcriptErr)
		}
	} else if hookPayloadString(normalized, "transcript_path", "") != "" {
		transcriptErr = c.scheduleHookGrokTranscript(normalized, dbPath)
		if transcriptErr != nil {
			slog.Debug("grok stop transcript scheduling failed", "session_id", sessionID, "error", transcriptErr)
		}
	}
	boundaryErr := c.runHookSession(ctx, nil, bytes.NewReader(normalized), grokHookClient, "stop", dbPath)
	return errors.Join(transcriptErr, boundaryErr)
}

// normalizeGrokHookPayload maps only fields proven by the versioned live
// contract into the shared snake_case hook envelope. Unknown fields remain at
// the host boundary and unavailable events are not synthesized.
func normalizeGrokHookPayload(input io.Reader) ([]byte, error) {
	payload, err := readHookPayload(input)
	if err != nil {
		return nil, err
	}
	if len(bytes.TrimSpace(payload)) == 0 {
		return []byte("{}"), nil
	}

	var source map[string]any
	if err := json.Unmarshal(payload, &source); err != nil {
		return nil, xerrors.Errorf("failed to decode Grok hook payload: %w", err)
	}
	normalized := map[string]any{}
	copyGrokHookField(normalized, "session_id", source, "sessionId")
	copyGrokHookField(normalized, "cwd", source, "cwd")
	if _, ok := normalized["cwd"]; !ok {
		copyGrokHookField(normalized, "cwd", source, "workspaceRoot")
	}
	copyGrokHookField(normalized, "transcript_path", source, "transcriptPath")
	copyGrokHookField(normalized, "prompt", source, "prompt")
	copyGrokHookField(normalized, "prompt_id", source, "promptId")
	copyGrokHookField(normalized, "trigger", source, "source")
	if hookEventName, _ := source["hookEventName"].(string); strings.Contains(hookEventName, "compact") {
		if _, ok := normalized["trigger"]; !ok {
			normalized["trigger"] = "unavailable"
		}
	}
	copyGrokHookField(normalized, "tool_name", source, "toolName")
	copyGrokHookField(normalized, "tool_use_id", source, "toolUseId")
	copyGrokHookField(normalized, "tool_input", source, "toolInput")
	copyGrokHookField(normalized, "tool_response", source, "toolResult")
	if failureKind := grokToolFailureKind(source["toolResult"]); failureKind != "" {
		// The shared audit path needs only a structural failure signal. Keep the
		// fixed class here and preserve/redact the full result through
		// tool_response rather than duplicating a host path in the error field.
		normalized["error"] = "grok tool result: " + failureKind
	}

	encoded, err := marshalStableJSON(normalized)
	if err != nil {
		return nil, xerrors.Errorf("failed to normalize Grok hook payload: %w", err)
	}
	return encoded, nil
}

func copyGrokHookField(target map[string]any, targetName string, source map[string]any, sourceName string) {
	value, ok := source[sourceName]
	if !ok || value == nil {
		return
	}
	if stringValue, ok := value.(string); ok && strings.TrimSpace(stringValue) == "" {
		return
	}
	target[targetName] = value
}

func grokToolFailureKind(result any) string {
	object, ok := result.(map[string]any)
	if !ok {
		return ""
	}
	for _, kind := range []string{"FileNotFound", "PermissionDenied"} {
		if value, exists := object[kind]; exists && value != nil {
			if text, isString := value.(string); !isString || strings.TrimSpace(text) != "" {
				return kind
			}
		}
	}
	return ""
}

type grokTranscriptUpdate struct {
	Method string `json:"method"`
	Params struct {
		Update struct {
			SessionUpdate string `json:"sessionUpdate"`
			Content       struct {
				Type string `json:"type"`
				Text string `json:"text"`
			} `json:"content"`
		} `json:"update"`
		Meta struct {
			PromptID string `json:"promptId"`
		} `json:"_meta"`
	} `json:"params"`
}

// extractGrokTranscript reads Grok's updates.jsonl and joins the streamed
// agent_message_chunk rows for the Stop payload's promptId. Thought and tool
// rows are intentionally excluded from the persisted assistant transcript.
func extractGrokTranscript(payload []byte) ([]apptypes.EventBodyBlock, bool) {
	path := strings.TrimSpace(hookPayloadString(payload, "transcript_path", ""))
	if path == "" {
		return nil, false
	}
	targetPromptID := strings.TrimSpace(hookPayloadString(payload, "prompt_id", ""))

	file, err := os.Open(path) // #nosec G304 -- path supplied by the Grok Stop hook
	if err != nil {
		slog.Debug("failed to open Grok transcript file", "path", path, "error", err)
		return nil, false
	}
	defer func() { _ = file.Close() }()

	chunks := map[string]*strings.Builder{}
	lastPromptID := ""
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	for scanner.Scan() {
		var row grokTranscriptUpdate
		if err := json.Unmarshal(scanner.Bytes(), &row); err != nil {
			continue
		}
		if row.Method != "session/update" || row.Params.Update.SessionUpdate != "agent_message_chunk" || row.Params.Update.Content.Type != "text" {
			continue
		}
		promptID := strings.TrimSpace(row.Params.Meta.PromptID)
		if promptID == "" {
			continue
		}
		builder := chunks[promptID]
		if builder == nil {
			builder = &strings.Builder{}
			chunks[promptID] = builder
		}
		builder.WriteString(row.Params.Update.Content.Text)
		lastPromptID = promptID
	}
	if err := scanner.Err(); err != nil {
		slog.Debug("failed while scanning Grok transcript file", "path", path, "error", err)
		return nil, false
	}
	if targetPromptID == "" {
		targetPromptID = lastPromptID
	}
	builder := chunks[targetPromptID]
	if builder == nil {
		return nil, false
	}
	text := strings.TrimSpace(builder.String())
	if text == "" {
		return nil, false
	}
	return []apptypes.EventBodyBlock{{Type: apptypes.EventBodyBlockTypeText, Text: text}}, true
}
