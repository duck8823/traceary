package cli

import (
	"context"
	"io"
	"strings"
	"time"

	"golang.org/x/xerrors"

	apptypes "github.com/duck8823/traceary/application/types"
	"github.com/duck8823/traceary/domain/types"
)

func (c *RootCLI) runHookSubagentStart(
	ctx context.Context,
	input io.Reader,
	client string,
	dbPath string,
) error {
	if c.storeManagement == nil {
		return xerrors.Errorf("initialize store usecase is not configured")
	}
	if c.session == nil {
		return xerrors.Errorf("session usecase is not configured")
	}
	if client == "codex" {
		ctx = apptypes.WithSourceHook(ctx, "subagent_start")
	} else {
		ctx = apptypes.WithSourceHook(ctx, "pre_tool_use")
	}

	payload, err := readHookPayload(input)
	if err != nil {
		return err
	}
	parentSessionID, err := resolveHookSubagentStartParentSessionID(payload, client)
	if err != nil {
		return err
	}
	if parentSessionID == "" {
		return nil
	}
	toolUseID := resolveHookToolUseID(payload)
	if toolUseID == "" {
		return nil
	}
	subagentType := hookPayloadString(payload, "subagent_type", "")
	if subagentType == "" {
		subagentType = hookPayloadString(payload, "agent_type", "")
	}
	if subagentType == "" {
		subagentType = hookPayloadString(payload, "tool_input.subagent_type", "")
	}
	if subagentType == "" {
		subagentType = "task"
	}
	agent, err := types.AgentFrom(strings.TrimSpace(client) + "/" + subagentType)
	if err != nil {
		return xerrors.Errorf("failed to resolve subagent agent: %w", err)
	}
	workspace, err := resolveHookWorkspace(ctx, payload, client, true)
	if err != nil {
		return err
	}

	resolvedDBPath, err := resolveDBPath(dbPath)
	if err != nil {
		return xerrors.Errorf("failed to resolve DB path: %w", err)
	}
	c.applyDatabasePath(resolvedDBPath)
	if err := c.storeManagement.Initialize(ctx); err != nil {
		return xerrors.Errorf("failed to initialize store: %w", err)
	}

	childSessionID := synthesizeHookChildSessionID(parentSessionID, toolUseID)
	if _, err := c.session.StartChild(ctx, parentSessionID, childSessionID, agent, workspace, types.EventID(toolUseID), "task", time.Now()); err != nil {
		return xerrors.Errorf("failed to record subagent start: %w", err)
	}
	if err := writeHookActiveSubagentState(client, parentSessionID, toolUseID, childSessionID); err != nil {
		return err
	}
	return nil
}

func (c *RootCLI) runHookSubagentStop(
	ctx context.Context,
	input io.Reader,
	client string,
	dbPath string,
) error {
	if c.storeManagement == nil {
		return xerrors.Errorf("initialize store usecase is not configured")
	}
	if c.event == nil {
		return xerrors.Errorf("event usecase is not configured")
	}
	if c.session == nil {
		return xerrors.Errorf("session usecase is not configured")
	}
	// Tag for #672: SubagentStop produces a session_ended event
	// (distinguished today only via the `[phase:subagent]` body
	// prefix). source_hook lets downstream queries filter without
	// parsing the body string.
	ctx = apptypes.WithSourceHook(ctx, "subagent_stop")
	payload, err := readHookPayload(input)
	if err != nil {
		return err
	}
	resolvedDBPath, err := resolveDBPath(dbPath)
	if err != nil {
		return xerrors.Errorf("failed to resolve DB path: %w", err)
	}
	c.applyDatabasePath(resolvedDBPath)
	if err := c.storeManagement.Initialize(ctx); err != nil {
		return xerrors.Errorf("failed to initialize store: %w", err)
	}
	parentSessionID, err := resolveHookParentSessionID(payload, client)
	if err != nil {
		return err
	}
	if parentSessionID == "" {
		return nil
	}
	workspace, err := resolveHookWorkspace(ctx, payload, client, true)
	if err != nil {
		return err
	}
	agent, err := resolveHookAgent(client, payload)
	if err != nil {
		return err
	}
	toolUseID := resolveHookToolUseID(payload)
	childSessionID := types.SessionID("")
	activeToolUseID := ""
	activeParentSessionID := types.SessionID("")
	childWasActive := false
	lazySynthesizedChild := false
	var memoryExtractRequest *hookMemoryExtractRequest
	if toolUseID != "" {
		active, findErr := findHookActiveSubagentStateForTool(client, parentSessionID, toolUseID)
		if findErr != nil {
			return findErr
		}
		childSessionID = active.ChildSessionID
		activeParentSessionID = active.ParentSessionID
		childWasActive = childSessionID != ""
		if activeParentSessionID == "" {
			activeParentSessionID = parentSessionID
		}
		if childSessionID == "" {
			childSessionID = synthesizeHookChildSessionID(parentSessionID, toolUseID)
		}
		activeToolUseID = toolUseID
	} else {
		var latestChildSessionID types.SessionID
		var findErr error
		activeToolUseID, latestChildSessionID, findErr = readHookLatestActiveSubagentState(client, parentSessionID)
		if findErr != nil {
			return findErr
		}
		childSessionID = latestChildSessionID
		activeParentSessionID = parentSessionID
		childWasActive = childSessionID != ""
	}
	if childSessionID != "" {
		if !childWasActive {
			childAgent := resolveHookSubagentAgentOrDefault(client, payload, agent)
			if _, startErr := c.session.StartChild(ctx, parentSessionID, childSessionID, childAgent, workspace, types.EventID(toolUseID), "task", time.Now()); startErr != nil {
				return xerrors.Errorf("failed to synthesize missing subagent start: %w", startErr)
			}
			lazySynthesizedChild = true
		}
		if _, err := c.session.End(ctx, types.Client("hook"), types.Agent(""), childSessionID, workspace, ""); err != nil {
			return xerrors.Errorf("failed to end subagent session: %w", err)
		}
		request := hookMemoryExtractRequest{
			SessionID: childSessionID, Workspace: workspace, DBPath: resolvedDBPath, SourceBoundary: "subagent_stop",
		}
		memoryExtractRequest = &request
		if activeParentSessionID != "" {
			parentSessionID = activeParentSessionID
		}
		if err := clearHookActiveSubagentState(client, parentSessionID, activeToolUseID); err != nil {
			return err
		}
	}
	// Claude Code's SubagentStop hook fires whenever a Task-tool subagent
	// completes. source_hook = "subagent_stop" (stamped via ctx) now
	// discriminates the subagent boundary from main-session session_ended
	// events; the legacy `[phase:subagent]` body prefix is retired on
	// write but readers still match it for pre-#672 rows.
	if lazySynthesizedChild {
		if memoryExtractRequest != nil {
			c.scheduleHookMemoryExtract(*memoryExtractRequest)
		}
		return nil
	}
	subagentType := hookPayloadString(payload, "subagent_type", "")
	_, err = c.event.Log(ctx, subagentType, types.EventKindSessionEnded, types.Client("hook"), agent, parentSessionID, workspace, apptypes.LogRedaction{})
	if err != nil {
		return xerrors.Errorf("failed to record subagent-stop event: %w", err)
	}
	if memoryExtractRequest != nil {
		c.scheduleHookMemoryExtract(*memoryExtractRequest)
	}
	return nil
}
