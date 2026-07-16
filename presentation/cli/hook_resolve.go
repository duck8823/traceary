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
	sessionID := types.SessionID(hookPayloadString(payload, "session_id", ""))
	if sessionID != "" {
		return sessionID, nil
	}
	return readHookSessionState(client)
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

func resolveHookWorkspace(ctx context.Context, payload []byte, client string, preferState bool) (types.Workspace, error) {
	if preferState {
		// Once a session has started, subsequent hook events should stay bound to
		// the persisted workspace so audit/prompt/end events do not drift when the
		// current cwd or explicit override changes mid-session.
		workspace, err := readHookWorkspaceState(client)
		if err != nil {
			return "", err
		}
		if workspace != "" {
			return workspace, nil
		}
	}
	if explicit := strings.TrimSpace(os.Getenv("TRACEARY_WORKSPACE")); explicit != "" {
		return types.Workspace(explicit), nil
	}

	return resolveHookWorkspaceFromPayload(ctx, payload)
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
