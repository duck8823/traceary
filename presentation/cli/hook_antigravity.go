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
	"path/filepath"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"golang.org/x/xerrors"

	apptypes "github.com/duck8823/traceary/application/types"
	"github.com/duck8823/traceary/domain/model"
	"github.com/duck8823/traceary/domain/types"
)

// newHookAntigravityCommand wires the hidden Antigravity hook runtime
// entrypoints. Antigravity's hook payloads use camelCase fields
// (conversationId, workspacePaths, transcriptPath, toolCall.*, stepIdx) that
// differ from the snake_case fields the shared runtime resolves, so each
// subcommand normalizes the payload into the internal shape before reusing the
// shared session / audit / transcript logic. Each subcommand also writes the
// JSON output contract Antigravity expects on stdout.
func (c *RootCLI) newHookAntigravityCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:    "antigravity <pre-invocation|pre-tool-use|post-tool-use|stop>",
		Short:  "Runtime entrypoints for Antigravity hooks",
		Hidden: true,
	}
	cmd.AddCommand(c.newHookAntigravityEventCommand("pre-invocation", c.runHookAntigravityPreInvocation))
	cmd.AddCommand(c.newHookAntigravityEventCommand("pre-tool-use", c.runHookAntigravityPreToolUse))
	cmd.AddCommand(c.newHookAntigravityEventCommand("post-tool-use", c.runHookAntigravityPostToolUse))
	cmd.AddCommand(c.newHookAntigravityEventCommand("stop", c.runHookAntigravityStop))

	return cmd
}

func (c *RootCLI) newHookAntigravityEventCommand(
	event string,
	run func(ctx context.Context, output io.Writer, input io.Reader, dbPath string) error,
) *cobra.Command {
	var dbPath string

	cmd := &cobra.Command{
		Use:    event,
		Short:  "Record Antigravity " + event + " hook events",
		Hidden: true,
		Args:   noArgsLocalized(),
		RunE: func(cmd *cobra.Command, _ []string) error {
			return c.runHookDurably(cmd.Context(), "antigravity "+event, hookInvocationSpec{Command: "antigravity", Client: antigravityHookClient, Action: event, DBPath: dbPath}, cmd.InOrStdin(), func(input io.Reader) error {
				return run(cmd.Context(), cmd.OutOrStdout(), input, dbPath)
			})
		},
	}
	cmd.Flags().StringVar(&dbPath, "db-path", "", dbPathFlagUsage())

	return cmd
}

// antigravityHookClient is the canonical client name used for Antigravity hook
// state files and recorded events.
const antigravityHookClient = "antigravity"

// runHookAntigravityPreInvocation starts or refreshes a Traceary session keyed
// by conversationId. PreInvocation fires before every model call, so this is
// idempotent: an existing session is treated as already started. Output is an
// empty object so Antigravity injects no steps.
func (c *RootCLI) runHookAntigravityPreInvocation(ctx context.Context, output io.Writer, input io.Reader, dbPath string) error {
	defer func() { _ = writeAntigravityJSON(output, map[string]any{}) }()

	if c.storeManagement == nil || c.session == nil {
		return nil
	}
	ctx = apptypes.WithSourceHook(ctx, "pre_invocation")

	payload, err := readHookPayload(input)
	if err != nil {
		return err
	}
	sessionID := types.SessionID(strings.TrimSpace(hookPayloadString(payload, "conversationId", "")))
	if sessionID == "" {
		return nil
	}

	// Idempotent: if the persisted hook state already points at this
	// conversation, the session is started — just refresh workspace state.
	existingState, err := readHookSessionState(antigravityHookClient)
	if err != nil {
		return err
	}

	normalized := normalizeAntigravityPayload(antigravityNormalizeOptions{
		sessionID: sessionID.String(),
		cwd:       antigravityWorkspaceCwd(payload),
	})

	resolvedDBPath, err := resolveDBPath(dbPath)
	if err != nil {
		return err
	}
	c.applyDatabasePath(resolvedDBPath)
	if err := c.storeManagement.Initialize(ctx); err != nil {
		return xerrors.Errorf("failed to initialize store: %w", err)
	}

	workspace, err := resolveHookWorkspace(ctx, normalized, antigravityHookClient, existingState == sessionID)
	if err != nil {
		return err
	}
	agent, err := types.AgentFrom(antigravityHookClient)
	if err != nil {
		return xerrors.Errorf("failed to resolve antigravity agent: %w", err)
	}

	if existingState != sessionID {
		if _, err := c.session.Start(ctx, types.Client("hook"), agent, sessionID, workspace, ""); err != nil {
			// PreInvocation re-fires for the same conversation after the
			// session row already exists; treat that as a no-op refresh
			// instead of an error so the session/workspace state still
			// gets (re)written below.
			if !errors.Is(err, model.ErrInvalidSessionState) {
				return xerrors.Errorf("failed to record antigravity session start: %w", err)
			}
		}
		if err := writeHookSessionState(antigravityHookClient, sessionID); err != nil {
			return err
		}
		if err := clearHookSessionEndMarker(antigravityHookClient, sessionID); err != nil {
			return err
		}
	}

	if workspace != "" {
		if err := writeHookWorkspaceState(antigravityHookClient, workspace); err != nil {
			return err
		}
	}
	c.runOpportunisticSessionGC(ctx, resolvedDBPath, sessionID)
	return nil
}

// runHookAntigravityPreToolUse persists the proposed run_command details keyed
// by conversationId + stepIdx so PostToolUse — which carries no command args —
// can pair them when recording the audit. It never blocks: output is always
// {"decision":"allow"}.
func (c *RootCLI) runHookAntigravityPreToolUse(_ context.Context, output io.Writer, input io.Reader, _ string) error {
	defer func() { _ = writeAntigravityJSON(output, map[string]any{"decision": "allow"}) }()

	payload, err := readHookPayload(input)
	if err != nil {
		return err
	}
	toolName := strings.TrimSpace(hookPayloadString(payload, "toolCall.name", ""))
	if toolName != "run_command" {
		return nil
	}
	conversationID := strings.TrimSpace(hookPayloadString(payload, "conversationId", ""))
	stepIdx := strings.TrimSpace(hookPayloadString(payload, "stepIdx", ""))
	command := hookPayloadString(payload, "toolCall.args.CommandLine", "")
	if conversationID == "" || stepIdx == "" || strings.TrimSpace(command) == "" {
		return nil
	}
	cwd := hookPayloadString(payload, "toolCall.args.Cwd", "")
	return writeAntigravityPendingCommand(conversationID, stepIdx, antigravityPendingCommand{Command: command, Cwd: cwd})
}

// runHookAntigravityPostToolUse pairs the PostToolUse step with the command
// persisted by PreToolUse and records a command audit. It fails soft and emits
// {} when no pending command is available (e.g. a non-run_command tool, or a
// PostToolUse without a matching PreToolUse).
func (c *RootCLI) runHookAntigravityPostToolUse(ctx context.Context, output io.Writer, input io.Reader, dbPath string) error {
	defer func() { _ = writeAntigravityJSON(output, map[string]any{}) }()

	payload, err := readHookPayload(input)
	if err != nil {
		return err
	}
	conversationID := strings.TrimSpace(hookPayloadString(payload, "conversationId", ""))
	stepIdx := strings.TrimSpace(hookPayloadString(payload, "stepIdx", ""))
	if conversationID == "" || stepIdx == "" {
		return nil
	}
	pending, ok, err := readAntigravityPendingCommand(conversationID, stepIdx)
	if err != nil {
		return err
	}
	if !ok || strings.TrimSpace(pending.Command) == "" {
		return nil
	}
	// Pair the persisted command with the PostToolUse error/cwd and reuse the
	// shared audit path through a normalized snake_case payload.
	normalized := normalizeAntigravityPayload(antigravityNormalizeOptions{
		sessionID:   conversationID,
		cwd:         firstNonEmpty(pending.Cwd, antigravityWorkspaceCwd(payload)),
		toolName:    "run_command",
		toolCommand: pending.Command,
		errMessage:  hookPayloadString(payload, "error", ""),
	})
	if err := c.runHookAudit(ctx, bytes.NewReader(normalized), antigravityHookClient, dbPath); err != nil {
		return err
	}
	return clearAntigravityPendingCommand(conversationID, stepIdx)
}

// runHookAntigravityStop records the final assistant turn (best effort, from
// transcriptPath) and a turn boundary. Like Codex, Antigravity's Stop is a
// per-execution boundary rather than a hard session end, so the session row is
// kept open (memory auto-extract still fires). Output keeps the agent stopped
// with {"decision":""}.
func (c *RootCLI) runHookAntigravityStop(ctx context.Context, output io.Writer, input io.Reader, dbPath string) error {
	defer func() { _ = writeAntigravityJSON(output, map[string]any{"decision": ""}) }()

	payload, err := readHookPayload(input)
	if err != nil {
		return err
	}
	sessionID := strings.TrimSpace(hookPayloadString(payload, "conversationId", ""))
	normalized := normalizeAntigravityPayload(antigravityNormalizeOptions{
		sessionID:      sessionID,
		cwd:            antigravityWorkspaceCwd(payload),
		transcriptPath: hookPayloadString(payload, "transcriptPath", ""),
	})

	// Transcript first so the turn's transcript event is recorded before any
	// turn-boundary side effects, keeping the event order chronological.
	transcriptErr := c.runHookTranscript(ctx, bytes.NewReader(normalized), antigravityHookClient, dbPath)
	if transcriptErr != nil {
		slog.Debug("antigravity stop transcript failed", "session_id", sessionID, "error", transcriptErr)
	}
	if err := c.runHookSession(ctx, nil, bytes.NewReader(normalized), antigravityHookClient, "stop", dbPath); err != nil {
		return err
	}
	return transcriptErr
}

// antigravityNormalizeOptions carries the resolved values used to build a
// normalized snake_case payload from an Antigravity hook payload.
type antigravityNormalizeOptions struct {
	sessionID      string
	cwd            string
	transcriptPath string
	toolName       string
	toolCommand    string
	errMessage     string
}

// normalizeAntigravityPayload builds an internal snake_case payload from the
// Antigravity camelCase payload so the shared runtime helpers
// (resolveHookSessionID, resolveHookWorkspace, runHookAudit, runHookTranscript)
// can consume it unchanged.
func normalizeAntigravityPayload(opts antigravityNormalizeOptions) []byte {
	normalized := map[string]any{}
	if opts.sessionID != "" {
		normalized["session_id"] = opts.sessionID
	}
	if opts.cwd != "" {
		normalized["cwd"] = opts.cwd
	}
	if opts.transcriptPath != "" {
		normalized["transcript_path"] = opts.transcriptPath
	}
	if opts.toolName != "" {
		normalized["tool_name"] = opts.toolName
	}
	if opts.toolCommand != "" {
		normalized["tool_input"] = map[string]any{"command": opts.toolCommand}
	}
	if strings.TrimSpace(opts.errMessage) != "" {
		normalized["error"] = opts.errMessage
	}
	encoded, err := json.Marshal(normalized)
	if err != nil {
		return []byte("{}")
	}
	return encoded
}

// antigravityWorkspaceCwd returns the first workspace path from the payload, if
// any, used as the cwd for workspace detection. Antigravity exposes
// workspacePaths (an array of absolute mounted workspace roots) rather than a
// single cwd on common-field payloads.
func antigravityWorkspaceCwd(payload []byte) string {
	value, ok := lookupHookPayloadValue(payload, "workspacePaths")
	if !ok || value == nil {
		return ""
	}
	paths, ok := value.([]any)
	if !ok {
		return ""
	}
	for _, entry := range paths {
		if path, ok := entry.(string); ok && strings.TrimSpace(path) != "" {
			return path
		}
	}
	return ""
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

func writeAntigravityJSON(output io.Writer, value map[string]any) error {
	if output == nil {
		return nil
	}
	encoded, err := json.Marshal(value)
	if err != nil {
		return xerrors.Errorf("failed to marshal antigravity hook output: %w", err)
	}
	if _, err := output.Write(encoded); err != nil {
		return xerrors.Errorf("failed to write antigravity hook output: %w", err)
	}
	return nil
}

// antigravityPendingCommand is the run_command detail persisted by PreToolUse
// and paired by PostToolUse.
type antigravityPendingCommand struct {
	Command string `json:"command"`
	Cwd     string `json:"cwd,omitempty"`
}

// antigravityPendingStaleAfter bounds how long an unpaired PreToolUse pending
// command file is kept. PostToolUse normally consumes and clears it within
// seconds, but a cancelled, interrupted, or never-delivered PostToolUse would
// otherwise leak the file forever. Writes opportunistically prune siblings
// older than this so the antigravity-pending directory does not grow unbounded.
const antigravityPendingStaleAfter = 24 * time.Hour

// antigravityPendingNowFunc returns the current time used for pending-state TTL
// pruning. It is a package var so tests can pin a deterministic clock.
var antigravityPendingNowFunc = time.Now

func antigravityPendingCommandPath(conversationID, stepIdx string) (string, error) {
	stateDir, err := resolveHookStateDir()
	if err != nil {
		return "", err
	}
	key := sanitizeHookStateKey(conversationID) + "-" + sanitizeHookStateKey(stepIdx)
	return filepath.Join(stateDir, "antigravity-pending", key), nil
}

func writeAntigravityPendingCommand(conversationID, stepIdx string, pending antigravityPendingCommand) error {
	statePath, err := antigravityPendingCommandPath(conversationID, stepIdx)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(statePath), 0o755); err != nil {
		return xerrors.Errorf("failed to create antigravity pending state directory: %w", err)
	}
	// Opportunistically drop stale siblings left by PreToolUse steps whose
	// PostToolUse never paired them (cancelled / interrupted / missing).
	pruneStaleAntigravityPendingCommands(filepath.Dir(statePath))
	data, err := json.Marshal(pending)
	if err != nil {
		return xerrors.Errorf("failed to encode antigravity pending command: %w", err)
	}
	if err := os.WriteFile(statePath, data, 0o600); err != nil {
		return xerrors.Errorf("failed to write antigravity pending command: %w", err)
	}
	return nil
}

func readAntigravityPendingCommand(conversationID, stepIdx string) (antigravityPendingCommand, bool, error) {
	statePath, err := antigravityPendingCommandPath(conversationID, stepIdx)
	if err != nil {
		return antigravityPendingCommand{}, false, err
	}
	data, err := os.ReadFile(statePath)
	if err != nil {
		if os.IsNotExist(err) {
			return antigravityPendingCommand{}, false, nil
		}
		return antigravityPendingCommand{}, false, xerrors.Errorf("failed to read antigravity pending command: %w", err)
	}
	var pending antigravityPendingCommand
	if err := json.Unmarshal(data, &pending); err != nil {
		return antigravityPendingCommand{}, false, nil
	}
	return pending, true, nil
}

func clearAntigravityPendingCommand(conversationID, stepIdx string) error {
	statePath, err := antigravityPendingCommandPath(conversationID, stepIdx)
	if err != nil {
		return err
	}
	if err := os.Remove(statePath); err != nil && !os.IsNotExist(err) {
		return xerrors.Errorf("failed to clear antigravity pending command: %w", err)
	}
	return nil
}

// pruneStaleAntigravityPendingCommands removes pending command files in
// pendingDir whose modification time is older than antigravityPendingStaleAfter.
// It is best effort: directory and per-file errors are ignored so a cleanup
// failure never blocks recording the current step.
func pruneStaleAntigravityPendingCommands(pendingDir string) {
	entries, err := os.ReadDir(pendingDir)
	if err != nil {
		return
	}
	now := antigravityPendingNowFunc()
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		info, err := entry.Info()
		if err != nil {
			continue
		}
		if now.Sub(info.ModTime()) <= antigravityPendingStaleAfter {
			continue
		}
		_ = os.Remove(filepath.Join(pendingDir, entry.Name()))
	}
}

// extractAntigravityTranscript resolves the Antigravity transcriptPath and
// leniently reads the final assistant turn. Antigravity's transcript.jsonl
// per-line schema is not part of the public hook contract, so the extractor
// tries several plausible assistant-turn shapes and fails soft otherwise. Only
// the documented transcriptPath hook field is read — no private app data.
func extractAntigravityTranscript(payload []byte) ([]apptypes.EventBodyBlock, bool) {
	transcriptPath := strings.TrimSpace(hookPayloadString(payload, "transcript_path", ""))
	if transcriptPath == "" {
		return nil, false
	}
	return readLastAssistantTranscriptBlocksLenient(transcriptPath)
}

// readLastAssistantTranscriptBlocksLenient scans a JSONL transcript for the
// last assistant turn across several plausible shapes:
//
//   - Claude-style envelope: {"type":"assistant","message":{"role":"assistant","content":[{type,text|thinking}]}}
//   - flat role + content array: {"role":"assistant","content":[{type,text}]}
//   - flat role + content string: {"role":"assistant","content":"…"}
//   - flat role + text string: {"role":"assistant","text":"…"}
//
// Returns ok=false on IO/parse failure so callers can silently skip.
func readLastAssistantTranscriptBlocksLenient(path string) ([]apptypes.EventBodyBlock, bool) {
	file, err := os.Open(path) // #nosec G304 -- path supplied by the host Stop hook
	if err != nil {
		slog.Debug("failed to open antigravity transcript file", "path", path, "error", err)
		return nil, false
	}
	defer func() { _ = file.Close() }()

	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)

	var lastBlocks []apptypes.EventBodyBlock
	for scanner.Scan() {
		line := bytes.TrimSpace(scanner.Bytes())
		if len(line) == 0 {
			continue
		}
		var entry antigravityTranscriptLine
		if err := json.Unmarshal(line, &entry); err != nil {
			continue
		}
		if !entry.isAssistant() {
			continue
		}
		if blocks := entry.blocks(); len(blocks) > 0 {
			lastBlocks = blocks
		}
	}
	if err := scanner.Err(); err != nil {
		slog.Debug("failed while scanning antigravity transcript file", "path", path, "error", err)
		return nil, false
	}
	return lastBlocks, len(lastBlocks) > 0
}

// antigravityTranscriptLine is a lenient view over a JSONL transcript row. It
// accepts both the nested Claude-style envelope and a flat role/content shape.
type antigravityTranscriptLine struct {
	Type    string                        `json:"type"`
	Role    string                        `json:"role"`
	Text    string                        `json:"text"`
	Content json.RawMessage               `json:"content"`
	Message *antigravityTranscriptMessage `json:"message"`
}

type antigravityTranscriptMessage struct {
	Role    string          `json:"role"`
	Content json.RawMessage `json:"content"`
}

func (l antigravityTranscriptLine) isAssistant() bool {
	if strings.EqualFold(l.Type, "assistant") || strings.EqualFold(l.Role, "assistant") {
		return true
	}
	return l.Message != nil && strings.EqualFold(l.Message.Role, "assistant")
}

func (l antigravityTranscriptLine) blocks() []apptypes.EventBodyBlock {
	if l.Message != nil && len(l.Message.Content) > 0 {
		if blocks := antigravityContentBlocks(l.Message.Content); len(blocks) > 0 {
			return blocks
		}
	}
	if len(l.Content) > 0 {
		if blocks := antigravityContentBlocks(l.Content); len(blocks) > 0 {
			return blocks
		}
	}
	if text := strings.TrimSpace(l.Text); text != "" {
		return []apptypes.EventBodyBlock{{Type: apptypes.EventBodyBlockTypeText, Text: text}}
	}
	return nil
}

// antigravityContentBlocks parses a content value that may be a plain string or
// an array of typed blocks. Unknown block types are skipped.
func antigravityContentBlocks(raw json.RawMessage) []apptypes.EventBodyBlock {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 {
		return nil
	}
	if trimmed[0] == '"' {
		var text string
		if err := json.Unmarshal(trimmed, &text); err != nil {
			return nil
		}
		if strings.TrimSpace(text) == "" {
			return nil
		}
		return []apptypes.EventBodyBlock{{Type: apptypes.EventBodyBlockTypeText, Text: strings.TrimSpace(text)}}
	}
	var items []transcriptContent
	if err := json.Unmarshal(trimmed, &items); err != nil {
		return nil
	}
	return extractAssistantBlocks(items)
}
