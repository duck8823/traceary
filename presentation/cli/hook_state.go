package cli

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"golang.org/x/xerrors"

	"github.com/duck8823/traceary/domain/types"
)

func resolveHookStateDir() (string, error) {
	if envValue := strings.TrimSpace(os.Getenv(hookStateDirEnvKey)); envValue != "" {
		resolvedPath, err := filepath.Abs(envValue)
		if err != nil {
			return "", xerrors.Errorf("failed to resolve hook state directory: %w", err)
		}

		return resolvedPath, nil
	}

	homeDir, err := userHomeDirFunc()
	if err != nil {
		return "", xerrors.Errorf("failed to get user home directory: %w", err)
	}

	return filepath.Join(homeDir, ".config", "traceary", "hooks"), nil
}

func resolveHookStateKey() string {
	if explicit := strings.TrimSpace(os.Getenv("TRACEARY_HOOK_STATE_KEY")); explicit != "" {
		return sanitizeHookStateKey(explicit)
	}
	if parentPID := os.Getppid(); parentPID > 0 {
		if processInfo, err := hookParentProcessLookup(parentPID); err == nil {
			if processInfo.parentPID > 0 && looksLikeHookWrapperProcess(processInfo.command) {
				return sanitizeHookStateKey(strconv.Itoa(processInfo.parentPID))
			}
		}
		return sanitizeHookStateKey(strconv.Itoa(parentPID))
	}

	return sanitizeHookStateKey(strconv.Itoa(os.Getpid()))
}

func defaultHookParentProcessLookup(pid int) (hookParentProcessInfo, error) {
	output, err := exec.Command("ps", "-o", "ppid=,comm=", "-p", strconv.Itoa(pid)).Output()
	if err != nil {
		return hookParentProcessInfo{}, xerrors.Errorf("failed to resolve hook parent process: %w", err)
	}
	fields := strings.Fields(strings.TrimSpace(string(output)))
	if len(fields) == 0 {
		return hookParentProcessInfo{}, nil
	}
	parentPID, err := strconv.Atoi(fields[0])
	if err != nil {
		return hookParentProcessInfo{}, xerrors.Errorf("failed to parse hook parent process ID: %w", err)
	}
	command := ""
	if len(fields) > 1 {
		command = fields[1]
	}
	return hookParentProcessInfo{parentPID: parentPID, command: command}, nil
}

func looksLikeHookWrapperProcess(command string) bool {
	baseName := filepath.Base(strings.TrimSpace(command))
	switch baseName {
	case "sh", "bash", "zsh", "dash":
		return true
	default:
		return false
	}
}

func runHookBestEffort(commandName string, run func() error) error {
	if err := run(); err != nil {
		if debugEnabled := strings.TrimSpace(os.Getenv("TRACEARY_HOOK_DEBUG")); debugEnabled != "" {
			_, _ = fmt.Fprintf(os.Stderr, "traceary hook %s suppressed error: %v\n", commandName, err)
		}
		return nil
	}
	return nil
}

func sanitizeHookStateKey(value string) string {
	var builder strings.Builder
	for _, r := range value {
		switch {
		case r >= 'A' && r <= 'Z':
			builder.WriteRune(r)
		case r >= 'a' && r <= 'z':
			builder.WriteRune(r)
		case r >= '0' && r <= '9':
			builder.WriteRune(r)
		case r == '.' || r == '_' || r == '-':
			builder.WriteRune(r)
		default:
			builder.WriteRune('_')
		}
	}
	if builder.Len() == 0 {
		return "default"
	}

	return builder.String()
}

func hookSessionStatePath(client string) (string, error) {
	stateDir, err := resolveHookStateDir()
	if err != nil {
		return "", err
	}

	return filepath.Join(stateDir, client+"-"+resolveHookStateKey()), nil
}

func hookWorkspaceStatePath(client string) (string, error) {
	statePath, err := hookSessionStatePath(client)
	if err != nil {
		return "", err
	}

	return statePath + "-repo", nil
}

func hookSessionEndMarkerPath(client string, sessionID types.SessionID) (string, error) {
	stateDir, err := resolveHookStateDir()
	if err != nil {
		return "", err
	}
	sanitizedSessionID := sanitizeHookStateKey(sessionID.String())

	return filepath.Join(stateDir, "ended", client+"-"+sanitizedSessionID), nil
}

func hookActiveSubagentStatePath(client string, parentSessionID types.SessionID) (string, error) {
	stateDir, err := resolveHookStateDir()
	if err != nil {
		return "", err
	}
	sanitizedParentSessionID := sanitizeHookStateKey(parentSessionID.String())
	return filepath.Join(stateDir, "active-subagents", client+"-"+sanitizedParentSessionID), nil
}

func writeHookSessionState(client string, sessionID types.SessionID) error {
	statePath, err := hookSessionStatePath(client)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(statePath), 0o755); err != nil {
		return xerrors.Errorf("failed to create hook state directory: %w", err)
	}

	if err := os.WriteFile(statePath, []byte(sessionID), 0o600); err != nil {
		return xerrors.Errorf("failed to write hook session state: %w", err)
	}

	return nil
}

func readHookSessionState(client string) (types.SessionID, error) {
	statePath, err := hookSessionStatePath(client)
	if err != nil {
		return "", err
	}
	data, err := os.ReadFile(statePath)
	if err != nil {
		if os.IsNotExist(err) {
			return "", nil
		}
		return "", xerrors.Errorf("failed to read hook session state: %w", err)
	}

	return types.SessionID(strings.TrimSpace(string(data))), nil
}

func clearHookSessionState(client string) error {
	statePath, err := hookSessionStatePath(client)
	if err != nil {
		return err
	}
	if err := os.Remove(statePath); err != nil && !os.IsNotExist(err) {
		return xerrors.Errorf("failed to clear hook session state: %w", err)
	}

	return nil
}

func withHookActiveSubagentStateLock(client string, parentSessionID types.SessionID, fn func() error) error {
	statePath, err := hookActiveSubagentStatePath(client, parentSessionID)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(statePath), 0o755); err != nil {
		return xerrors.Errorf("failed to create hook active-subagent state directory: %w", err)
	}
	lockPath := statePath + ".lock"
	for i := 0; ; i++ {
		err := os.Mkdir(lockPath, 0o700)
		if err == nil {
			defer func() { _ = os.Remove(lockPath) }()
			return fn()
		}
		if !os.IsExist(err) {
			return xerrors.Errorf("failed to lock hook active-subagent state: %w", err)
		}
		if i >= 100 {
			return xerrors.Errorf("failed to lock hook active-subagent state: timed out")
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func writeHookActiveSubagentState(client string, parentSessionID types.SessionID, toolUseID string, childSessionID types.SessionID) error {
	toolUseID = strings.TrimSpace(toolUseID)
	if toolUseID == "" {
		return nil
	}
	return withHookActiveSubagentStateLock(client, parentSessionID, func() error {
		statePath, err := hookActiveSubagentStatePath(client, parentSessionID)
		if err != nil {
			return err
		}
		state, err := readHookActiveSubagentStateFile(statePath)
		if err != nil {
			return err
		}
		if state.Children == nil {
			state.Children = map[string]hookActiveSubagentChild{}
		}
		state.Children[toolUseID] = hookActiveSubagentChild{
			ChildSessionID: childSessionID,
			StartedAt:      time.Now().UTC(),
		}
		data, err := json.Marshal(state)
		if err != nil {
			return xerrors.Errorf("failed to encode hook active-subagent state: %w", err)
		}
		if err := os.WriteFile(statePath, data, 0o600); err != nil {
			return xerrors.Errorf("failed to write hook active-subagent state: %w", err)
		}
		return nil
	})
}

func readHookActiveSubagentStateFile(statePath string) (hookActiveSubagentState, error) {
	data, err := os.ReadFile(statePath)
	if err != nil {
		if os.IsNotExist(err) {
			return hookActiveSubagentState{Children: map[string]hookActiveSubagentChild{}}, nil
		}
		return hookActiveSubagentState{}, xerrors.Errorf("failed to read hook active-subagent state: %w", err)
	}
	trimmed := strings.TrimSpace(string(data))
	if trimmed == "" {
		return hookActiveSubagentState{Children: map[string]hookActiveSubagentChild{}}, nil
	}
	if !strings.HasPrefix(trimmed, "{") {
		return hookActiveSubagentState{
			Children: map[string]hookActiveSubagentChild{
				"legacy": {
					ChildSessionID: types.SessionID(trimmed),
				},
			},
		}, nil
	}
	var state hookActiveSubagentState
	if err := json.Unmarshal(data, &state); err != nil {
		return hookActiveSubagentState{}, xerrors.Errorf("failed to decode hook active-subagent state: %w", err)
	}
	if state.Children == nil {
		state.Children = map[string]hookActiveSubagentChild{}
	}
	if pruneHookActiveSubagentState(&state, time.Now().UTC()) {
		if len(state.Children) == 0 {
			if err := os.Remove(statePath); err != nil && !os.IsNotExist(err) {
				return hookActiveSubagentState{}, xerrors.Errorf("failed to clear stale hook active-subagent state: %w", err)
			}
		} else {
			data, err := json.Marshal(state)
			if err != nil {
				return hookActiveSubagentState{}, xerrors.Errorf("failed to encode hook active-subagent state: %w", err)
			}
			if err := os.WriteFile(statePath, data, 0o600); err != nil {
				return hookActiveSubagentState{}, xerrors.Errorf("failed to write hook active-subagent state: %w", err)
			}
		}
	}
	return state, nil
}

func pruneHookActiveSubagentState(state *hookActiveSubagentState, now time.Time) bool {
	if state == nil || hookActiveSubagentStateTTL <= 0 {
		return false
	}
	changed := false
	cutoff := now.Add(-hookActiveSubagentStateTTL)
	for toolUseID, child := range state.Children {
		if child.StartedAt.IsZero() {
			continue
		}
		if child.StartedAt.Before(cutoff) {
			delete(state.Children, toolUseID)
			changed = true
		}
	}
	return changed
}

func readHookActiveSubagentStateForTool(client string, parentSessionID types.SessionID, toolUseID string) (types.SessionID, bool, error) {
	statePath, err := hookActiveSubagentStatePath(client, parentSessionID)
	if err != nil {
		return "", false, err
	}
	state, err := readHookActiveSubagentStateFile(statePath)
	if err != nil {
		return "", false, err
	}
	child, ok := state.Children[toolUseID]
	if !ok || child.ChildSessionID == "" {
		return "", false, nil
	}
	return child.ChildSessionID, true, nil
}

func findHookActiveSubagentStateForTool(client string, rootParentSessionID types.SessionID, toolUseID string) (hookResolvedActiveSubagent, error) {
	if strings.TrimSpace(toolUseID) == "" {
		return hookResolvedActiveSubagent{}, nil
	}
	return findHookActiveSubagentStateForToolDepth(client, rootParentSessionID, toolUseID, 0)
}

func findHookActiveSubagentStateForToolDepth(client string, parentSessionID types.SessionID, toolUseID string, depth int) (hookResolvedActiveSubagent, error) {
	if depth >= 32 || parentSessionID == "" {
		return hookResolvedActiveSubagent{}, nil
	}
	childSessionID, ok, err := readHookActiveSubagentStateForTool(client, parentSessionID, toolUseID)
	if err != nil {
		return hookResolvedActiveSubagent{}, err
	}
	if ok {
		return hookResolvedActiveSubagent{
			ParentSessionID: parentSessionID,
			ToolUseID:       toolUseID,
			ChildSessionID:  childSessionID,
		}, nil
	}
	children, err := readHookActiveSubagentChildren(client, parentSessionID)
	if err != nil {
		return hookResolvedActiveSubagent{}, err
	}
	for _, childSessionID := range children {
		found, err := findHookActiveSubagentStateForToolDepth(client, childSessionID, toolUseID, depth+1)
		if err != nil {
			return hookResolvedActiveSubagent{}, err
		}
		if found.ChildSessionID != "" {
			return found, nil
		}
	}
	return hookResolvedActiveSubagent{}, nil
}

func readHookLatestActiveSubagentState(client string, parentSessionID types.SessionID) (string, types.SessionID, error) {
	statePath, err := hookActiveSubagentStatePath(client, parentSessionID)
	if err != nil {
		return "", "", err
	}
	state, err := readHookActiveSubagentStateFile(statePath)
	if err != nil {
		return "", "", err
	}
	var latestToolUseID string
	var latestChildID types.SessionID
	var latestStartedAt time.Time
	for toolUseID, child := range state.Children {
		if child.ChildSessionID == "" {
			continue
		}
		if latestChildID == "" || child.StartedAt.After(latestStartedAt) {
			latestToolUseID = toolUseID
			latestChildID = child.ChildSessionID
			latestStartedAt = child.StartedAt
		}
	}
	return latestToolUseID, latestChildID, nil
}

func readHookActiveSubagentChildren(client string, parentSessionID types.SessionID) ([]types.SessionID, error) {
	statePath, err := hookActiveSubagentStatePath(client, parentSessionID)
	if err != nil {
		return nil, err
	}
	state, err := readHookActiveSubagentStateFile(statePath)
	if err != nil {
		return nil, err
	}
	children := make([]types.SessionID, 0, len(state.Children))
	for _, child := range state.Children {
		if child.ChildSessionID != "" {
			children = append(children, child.ChildSessionID)
		}
	}
	return children, nil
}

func findHookDeepestLatestActiveSubagentState(client string, rootParentSessionID types.SessionID) (hookResolvedActiveSubagent, error) {
	return findHookDeepestLatestActiveSubagentStateDepth(client, rootParentSessionID, 0)
}

func findHookDeepestLatestActiveSubagentStateDepth(client string, parentSessionID types.SessionID, depth int) (hookResolvedActiveSubagent, error) {
	if depth >= 32 || parentSessionID == "" {
		return hookResolvedActiveSubagent{}, nil
	}
	toolUseID, childSessionID, err := readHookLatestActiveSubagentState(client, parentSessionID)
	if err != nil {
		return hookResolvedActiveSubagent{}, err
	}
	if childSessionID == "" {
		return hookResolvedActiveSubagent{}, nil
	}
	deeper, err := findHookDeepestLatestActiveSubagentStateDepth(client, childSessionID, depth+1)
	if err != nil {
		return hookResolvedActiveSubagent{}, err
	}
	if deeper.ChildSessionID != "" {
		return deeper, nil
	}
	return hookResolvedActiveSubagent{
		ParentSessionID: parentSessionID,
		ToolUseID:       toolUseID,
		ChildSessionID:  childSessionID,
	}, nil
}

func clearHookActiveSubagentState(client string, parentSessionID types.SessionID, toolUseID string) error {
	return withHookActiveSubagentStateLock(client, parentSessionID, func() error {
		statePath, err := hookActiveSubagentStatePath(client, parentSessionID)
		if err != nil {
			return err
		}
		if strings.TrimSpace(toolUseID) == "" || toolUseID == "legacy" {
			if err := os.Remove(statePath); err != nil && !os.IsNotExist(err) {
				return xerrors.Errorf("failed to clear hook active-subagent state: %w", err)
			}
			return nil
		}
		state, err := readHookActiveSubagentStateFile(statePath)
		if err != nil {
			return err
		}
		delete(state.Children, toolUseID)
		if len(state.Children) == 0 {
			if err := os.Remove(statePath); err != nil && !os.IsNotExist(err) {
				return xerrors.Errorf("failed to clear hook active-subagent state: %w", err)
			}
			return nil
		}
		data, err := json.Marshal(state)
		if err != nil {
			return xerrors.Errorf("failed to encode hook active-subagent state: %w", err)
		}
		if err := os.WriteFile(statePath, data, 0o600); err != nil {
			return xerrors.Errorf("failed to write hook active-subagent state: %w", err)
		}
		return nil
	})
}

func cleanupHookActiveSubagentStates(client string) error {
	stateDir, err := resolveHookStateDir()
	if err != nil {
		return err
	}
	activeDir := filepath.Join(stateDir, "active-subagents")
	entries, err := os.ReadDir(activeDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return xerrors.Errorf("failed to read hook active-subagent state directory: %w", err)
	}
	prefix := client + "-"
	for _, entry := range entries {
		if entry.IsDir() || strings.HasSuffix(entry.Name(), ".lock") || !strings.HasPrefix(entry.Name(), prefix) {
			continue
		}
		statePath := filepath.Join(activeDir, entry.Name())
		if _, err := readHookActiveSubagentStateFile(statePath); err != nil {
			return err
		}
	}
	return nil
}

func writeHookWorkspaceState(client string, workspace types.Workspace) error {
	statePath, err := hookWorkspaceStatePath(client)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(statePath), 0o755); err != nil {
		return xerrors.Errorf("failed to create hook state directory: %w", err)
	}

	if err := os.WriteFile(statePath, []byte(workspace), 0o600); err != nil {
		return xerrors.Errorf("failed to write hook workspace state: %w", err)
	}

	return nil
}

func readHookWorkspaceState(client string) (types.Workspace, error) {
	statePath, err := hookWorkspaceStatePath(client)
	if err != nil {
		return "", err
	}
	data, err := os.ReadFile(statePath)
	if err != nil {
		if os.IsNotExist(err) {
			return "", nil
		}
		return "", xerrors.Errorf("failed to read hook workspace state: %w", err)
	}

	return types.Workspace(strings.TrimSpace(string(data))), nil
}

func clearHookWorkspaceState(client string) error {
	statePath, err := hookWorkspaceStatePath(client)
	if err != nil {
		return err
	}
	if err := os.Remove(statePath); err != nil && !os.IsNotExist(err) {
		return xerrors.Errorf("failed to clear hook workspace state: %w", err)
	}

	return nil
}

func hookSessionEndAlreadyRecorded(client string, sessionID types.SessionID) (bool, error) {
	markerPath, err := hookSessionEndMarkerPath(client, sessionID)
	if err != nil {
		return false, err
	}
	_, err = os.Stat(markerPath)
	if err == nil {
		return true, nil
	}
	if os.IsNotExist(err) {
		return false, nil
	}

	return false, xerrors.Errorf("failed to inspect hook end marker: %w", err)
}

func markHookSessionEnded(client string, sessionID types.SessionID) error {
	markerPath, err := hookSessionEndMarkerPath(client, sessionID)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(markerPath), 0o755); err != nil {
		return xerrors.Errorf("failed to create hook end-marker directory: %w", err)
	}

	if err := os.WriteFile(markerPath, []byte{}, 0o600); err != nil {
		return xerrors.Errorf("failed to write hook end marker: %w", err)
	}

	return nil
}

func clearHookSessionEndMarker(client string, sessionID types.SessionID) error {
	markerPath, err := hookSessionEndMarkerPath(client, sessionID)
	if err != nil {
		return err
	}
	if err := os.Remove(markerPath); err != nil && !os.IsNotExist(err) {
		return xerrors.Errorf("failed to clear hook end marker: %w", err)
	}

	return nil
}
