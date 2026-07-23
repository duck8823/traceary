package cli

import (
	"context"
	"encoding/json"
	"os"
	"strconv"
	"strings"

	"golang.org/x/xerrors"

	apptypes "github.com/duck8823/traceary/application/types"
	"github.com/duck8823/traceary/domain/types"
)

func resolveHookAgent(client string, payload []byte) (types.Agent, error) {
	agentType := hookPayloadString(payload, "agent_type", "")
	agentValue := strings.TrimSpace(client)
	if agentType != "" {
		agentValue += "/" + agentType
	}

	agent, err := types.AgentFrom(agentValue)
	if err != nil {
		return "", xerrors.Errorf("failed to resolve hook agent: %w", err)
	}

	return agent, nil
}

func resolveHookSubagentAgentOrDefault(client string, payload []byte, defaultAgent types.Agent) types.Agent {
	subagentType := hookPayloadString(payload, "subagent_type", "")
	if subagentType == "" {
		subagentType = hookPayloadString(payload, "agent_type", "")
	}
	if subagentType == "" {
		subagentType = hookPayloadString(payload, "tool_input.subagent_type", "")
	}
	if strings.TrimSpace(subagentType) == "" {
		return defaultAgent
	}
	agent, err := types.AgentFrom(strings.TrimSpace(client) + "/" + strings.TrimSpace(subagentType))
	if err != nil {
		return defaultAgent
	}
	return agent
}

func (c *RootCLI) inferHookParentSessionID(ctx context.Context, payload []byte, client string, agent types.Agent, workspace types.Workspace) (types.SessionID, error) {
	if !hookParentSessionInferenceEnabled() || !isPlausibleSubagentStart(payload, agent) {
		return "", nil
	}
	active, err := c.session.Active(ctx, apptypes.NewSessionLookupCriteriaBuilder().
		Client(types.Client("hook")).
		Workspace(workspace).
		Build())
	if err != nil {
		return "", xerrors.Errorf("failed to infer parent session: %w", err)
	}
	activeEvent, ok := active.Value()
	if !ok || activeEvent.SessionID() == "" {
		return "", nil
	}
	searchStartSessionID := hookParentInferenceSearchStartSessionID(activeEvent.SessionID())
	activeSubagent, err := findHookDeepestLatestActiveSubagentState(client, searchStartSessionID)
	if err != nil {
		return "", err
	}
	if activeSubagent.ParentSessionID == "" {
		return "", nil
	}
	return activeSubagent.ParentSessionID, nil
}

func hookParentInferenceSearchStartSessionID(sessionID types.SessionID) types.SessionID {
	value := strings.TrimSpace(sessionID.String())
	if value == "" {
		return ""
	}
	if idx := strings.LastIndex(value, ":sub:"); idx > 0 {
		return types.SessionID(value[:idx])
	}
	return sessionID
}

func hookParentSessionInferenceEnabled() bool {
	value := strings.ToLower(strings.TrimSpace(os.Getenv("TRACEARY_INFER_PARENT_SESSION")))
	switch value {
	case "0", "false", "no", "off", "disabled":
		return false
	default:
		return true
	}
}

func isPlausibleSubagentStart(payload []byte, agent types.Agent) bool {
	if strings.TrimSpace(hookPayloadString(payload, "agent_type", "")) != "" ||
		strings.TrimSpace(hookPayloadString(payload, "subagent_type", "")) != "" ||
		strings.TrimSpace(hookPayloadString(payload, "tool_input.subagent_type", "")) != "" {
		return true
	}
	parts := strings.Split(strings.TrimSpace(agent.String()), "/")
	return len(parts) > 1 && strings.TrimSpace(parts[len(parts)-1]) != ""
}

func resolveHookSessionID(payload []byte, client string) (types.SessionID, error) {
	parentSessionID, err := resolveHookParentSessionID(payload, client)
	if err != nil {
		return "", err
	}
	if parentSessionID == "" {
		return "", nil
	}
	active, err := findHookDeepestLatestActiveSubagentState(client, parentSessionID)
	if err != nil {
		return "", err
	}
	if active.ChildSessionID != "" {
		return active.ChildSessionID, nil
	}

	return parentSessionID, nil
}

func resolveHookSubagentStartParentSessionID(payload []byte, client string) (types.SessionID, error) {
	return resolveHookParentSessionID(payload, client)
}

func resolveHookParentSessionID(payload []byte, client string) (types.SessionID, error) {
	if sessionID := explicitOneShotRuntimeSessionID(); sessionID != "" {
		return sessionID, nil
	}
	sessionID := types.SessionID(hookPayloadString(payload, "session_id", ""))
	if sessionID != "" {
		return sessionID, nil
	}
	return readHookSessionState(client)
}

func explicitOneShotRuntimeSessionID() types.SessionID {
	mode, err := types.RuntimeModeFrom(strings.TrimSpace(os.Getenv(runtimeModeEnvKey)))
	if err != nil || mode != types.RuntimeModeOneShot {
		return ""
	}
	sessionID, err := types.SessionIDFrom(strings.TrimSpace(os.Getenv(runtimeSessionIDEnvKey)))
	if err != nil {
		return ""
	}
	return sessionID
}

func synthesizeHookChildSessionID(parentSessionID types.SessionID, toolUseID string) types.SessionID {
	return types.SessionID(parentSessionID.String() + ":sub:" + strings.TrimSpace(toolUseID))
}

func resolveHookToolUseID(payload []byte) string {
	if toolUseID := strings.TrimSpace(hookPayloadString(payload, "tool_use_id", "")); toolUseID != "" {
		return toolUseID
	}
	if eventID := strings.TrimSpace(hookPayloadString(payload, "event_id", "")); eventID != "" {
		return "event-" + eventID
	}
	if agentID := strings.TrimSpace(hookPayloadString(payload, "agent_id", "")); agentID != "" {
		return "agent-" + agentID
	}
	return ""
}

// withResolvedHookDelivery carries only host-native identity evidence into the
// usecase. It never falls back to prompt/command/body equality: equal content
// without one of these proven identifiers remains a legitimate distinct event.
func withResolvedHookDelivery(ctx context.Context, payload []byte, client string) context.Context {
	sourceHook := apptypes.SourceHookFromContext(ctx)
	nativeID := resolveHookDeliveryNativeID(payload, client, sourceHook)
	rawWorkspace := hookPayloadString(payload, "cwd", "")
	return apptypes.WithHookDelivery(ctx, apptypes.HookDeliveryInputOf(nativeID, rawWorkspace))
}

func resolveHookDeliveryNativeID(payload []byte, client, sourceHook string) string {
	for _, path := range provenHookDeliveryIDFields(strings.ToLower(strings.TrimSpace(client))) {
		if value := strings.TrimSpace(hookPayloadString(payload, path, "")); value != "" {
			return path + ":" + value
		}
	}
	switch strings.TrimSpace(sourceHook) {
	case "session_start", "session_end":
		if sessionID := strings.TrimSpace(hookPayloadString(payload, "session_id", "")); sessionID != "" {
			return "session_id:" + sessionID
		}
	}
	return ""
}

func provenHookDeliveryIDFields(client string) []string {
	// This allowlist is intentionally narrower than the shared normalized JSON
	// shape. A lookalike field from another host is not delivery evidence until
	// that host's contract and fixtures prove its stability.
	switch client {
	case "codex":
		return []string{"event_id"}
	case "claude":
		return []string{"tool_use_id"}
	case "antigravity":
		return []string{"tool_use_id", "prompt_id", "event_id"}
	case "grok":
		return []string{"tool_use_id", "prompt_id"}
	case "kimi":
		return []string{"tool_use_id"}
	case "gemini":
		// The documented base hook schema carries the event execution timestamp.
		// It is retained only as body-free delivery identity.
		return []string{"timestamp"}
	default:
		return nil
	}
}

func resolveHookWorkspace(ctx context.Context, payload []byte, client string, preferState bool) (types.Workspace, error) {
	if explicit := strings.TrimSpace(os.Getenv("TRACEARY_WORKSPACE")); explicit != "" {
		return types.Workspace(explicit), nil
	}

	workspace, err := resolveHookWorkspaceFromPayload(ctx, payload)
	if err != nil || workspace != "" || !preferState {
		return workspace, err
	}

	// A persisted workspace is a fallback for payloads without event-local
	// evidence. It is not allowed to overwrite an explicit cwd observation.
	return readHookWorkspaceState(client)
}

func resolveHookWorkspaceForAudit(ctx context.Context, payload []byte, client string) (types.Workspace, error) {
	if explicit := strings.TrimSpace(os.Getenv("TRACEARY_WORKSPACE")); explicit != "" {
		return types.Workspace(explicit), nil
	}

	if workspace, err := resolveHookWorkspaceFromPayload(ctx, payload); err != nil {
		return "", err
	} else if workspace != "" {
		return workspace, nil
	}

	workspace, err := readHookWorkspaceState(client)
	if err != nil {
		return "", err
	}
	return workspace, nil
}

func resolveHookWorkspaceFromPayload(ctx context.Context, payload []byte) (types.Workspace, error) {
	cwd := hookPayloadString(payload, "cwd", "")
	if cwd == "" {
		return "", nil
	}

	workspace, err := detectRepoContextFromDir(ctx, cwd)
	if err == nil {
		return types.Workspace(workspace), nil
	}

	return types.Workspace(normalizeLocalWorkContextPath(cwd)), nil
}

func (c *RootCLI) canonicalHookSessionWorkspace(ctx context.Context, sessionID types.SessionID, fallback types.Workspace) (types.Workspace, error) {
	if c.session == nil || sessionID == "" {
		return fallback, nil
	}
	summaries, err := c.session.List(ctx, apptypes.NewSessionListCriteriaBuilder(1).
		SessionID(sessionID).
		Build())
	if err != nil {
		return "", xerrors.Errorf("failed to reload canonical session workspace: %w", err)
	}
	if len(summaries) == 1 && summaries[0].Workspace() != "" {
		return summaries[0].Workspace(), nil
	}
	return fallback, nil
}

func hookPayloadString(payload []byte, path string, defaultValue string) string {
	if len(payload) == 0 {
		return defaultValue
	}
	value, ok := lookupHookPayloadValue(payload, path)
	if !ok || value == nil {
		return defaultValue
	}
	renderedValue, err := renderHookHelperValue(value)
	if err != nil {
		return defaultValue
	}

	return renderedValue
}

func hookPayloadExitCode(payload []byte) types.Optional[int] {
	if len(payload) == 0 {
		return types.None[int]()
	}
	value, ok := lookupHookPayloadValue(payload, "tool_response.exitCode")
	if !ok || value == nil {
		return types.None[int]()
	}
	switch typedValue := value.(type) {
	case float64:
		return types.Some(int(typedValue))
	case int:
		return types.Some(typedValue)
	case int64:
		return types.Some(int(typedValue))
	case json.Number:
		parsed, err := typedValue.Int64()
		if err != nil {
			return types.None[int]()
		}
		return types.Some(int(parsed))
	case string:
		parsed, err := strconv.Atoi(strings.TrimSpace(typedValue))
		if err != nil {
			return types.None[int]()
		}
		return types.Some(parsed)
	default:
		return types.None[int]()
	}
}

// hookPayloadFailed derives a structural failure signal from a post-tool hook
// payload. No host exposes a numeric exit code in the post-tool payload, so
// failure is detected from the failure-shaped fields each host does provide:
//
//   - Claude Code: a top-level "error" string on the PostToolUseFailure
//     payload (the success PostToolUse payload has no "error" field).
//   - Gemini CLI: a nested "tool_response.error" object, set only for
//     spawn/OS-level errors (a plain non-zero shell exit is NOT reported here,
//     so Gemini failure capture is partial — see docs/hooks/contract.md).
//
// Codex exposes no structured failure field, so its failures are not flagged.
func hookPayloadFailed(payload []byte) bool {
	if len(payload) == 0 {
		return false
	}
	if hookPayloadString(payload, "error", "") != "" {
		return true
	}
	if value, ok := lookupHookPayloadValue(payload, "tool_response.error"); ok && value != nil {
		if errString, isString := value.(string); isString {
			return strings.TrimSpace(errString) != ""
		}
		return true
	}
	return false
}

// hookPayloadFailureReason resolves only structured hook evidence. It never
// searches error/output text, so quoted failure examples cannot become actual
// failures. Host adapters may emit the shared failure_reason field after
// interpreting their versioned payload contracts.
func hookPayloadFailureReason(payload []byte) types.CommandFailureReason {
	exitCode, hasExitCode := hookPayloadExitCode(payload).Value()
	if hasExitCode && exitCode == 0 {
		return types.CommandFailureReasonNone
	}
	explicitReason := types.CommandFailureReasonUnknown
	if value := hookPayloadString(payload, "failure_reason", ""); value != "" {
		if reason, err := types.CommandFailureReasonFrom(value); err == nil {
			explicitReason = reason
			switch reason {
			case types.CommandFailureReasonSignal,
				types.CommandFailureReasonTimeout,
				types.CommandFailureReasonHookDenied:
				return reason
			}
		}
	}
	if hookPayloadBool(payload, "is_interrupt") || hookPayloadBool(payload, "tool_response.interrupted") {
		return types.CommandFailureReasonSignal
	}
	for _, path := range []string{"timed_out", "tool_response.timed_out", "tool_response.timedOut"} {
		if hookPayloadBool(payload, path) {
			return types.CommandFailureReasonTimeout
		}
	}
	if hasExitCode {
		return types.CommandFailureReasonExitCode
	}
	if explicitReason != types.CommandFailureReasonUnknown {
		return explicitReason
	}
	if hookPayloadFailed(payload) {
		return types.CommandFailureReasonHostError
	}
	return types.CommandFailureReasonUnknown
}

func hookPayloadBool(payload []byte, path string) bool {
	value, ok := lookupHookPayloadValue(payload, path)
	if !ok {
		return false
	}
	boolean, ok := value.(bool)
	return ok && boolean
}

func buildHookFailureOutput(payload []byte) (string, error) {
	if len(payload) == 0 {
		return "", nil
	}
	result := map[string]any{}
	if errorValue := hookPayloadString(payload, "error", ""); errorValue != "" {
		result["error"] = errorValue
	}
	if interruptValue, ok := lookupHookPayloadValue(payload, "is_interrupt"); ok {
		result["is_interrupt"] = interruptValue
	}
	if len(result) == 0 {
		return "", nil
	}
	encodedValue, err := marshalStableJSON(result)
	if err != nil {
		return "", xerrors.Errorf("failed to marshal failure payload: %w", err)
	}

	return string(encodedValue), nil
}
