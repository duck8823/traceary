package cli

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"strings"

	"golang.org/x/xerrors"

	apptypes "github.com/duck8823/traceary/application/types"
	"github.com/duck8823/traceary/domain/types"
)

func (c *RootCLI) runHookSession(
	ctx context.Context,
	output io.Writer,
	input io.Reader,
	client string,
	action string,
	dbPath string,
) error {
	if c.storeManagement == nil {
		return xerrors.Errorf("initialize store usecase is not configured")
	}
	if c.session == nil {
		return xerrors.Errorf("record session boundary usecase is not configured")
	}

	if action != "start" && action != "end" && action != "stop" {
		return xerrors.Errorf("unsupported hook session action: %s", action)
	}

	// Tag downstream events with which host hook fired so
	// retrospective queries can tell a Claude SessionEnd apart from
	// a Codex Stop (#672). start / end map to session_start /
	// session_end; stop is Codex's per-response turn boundary and no
	// longer produces a session_ended row (#1170).
	switch action {
	case "start":
		ctx = apptypes.WithSourceHook(ctx, "session_start")
	case "end":
		ctx = apptypes.WithSourceHook(ctx, "session_end")
	case "stop":
		ctx = apptypes.WithSourceHook(ctx, "stop")
	}

	payload, err := readHookPayload(input)
	if err != nil {
		return err
	}

	switch action {
	case "start":
		resolvedDBPath, err := resolveDBPath(dbPath)
		if err != nil {
			return err
		}
		c.applyDatabasePath(resolvedDBPath)
		if err := c.storeManagement.Initialize(ctx); err != nil {
			return xerrors.Errorf("failed to initialize store: %w", err)
		}
		agent, err := resolveHookAgent(client, payload)
		if err != nil {
			return err
		}
		workspace, err := resolveHookWorkspace(ctx, payload, client, false)
		if err != nil {
			return err
		}
		sessionID := types.SessionID(hookPayloadString(payload, "session_id", ""))
		parentSessionID := types.SessionID(strings.TrimSpace(os.Getenv("TRACEARY_PARENT_SESSION_ID")))
		if parentSessionID == "" {
			inferredParentSessionID, inferErr := c.inferHookParentSessionID(ctx, payload, client, agent, workspace)
			if inferErr != nil {
				return inferErr
			}
			parentSessionID = inferredParentSessionID
		}
		event, err := c.session.Start(ctx, types.Client("hook"), agent, sessionID, workspace, parentSessionID)
		if err != nil {
			return xerrors.Errorf("failed to record hook session start: %w", err)
		}
		if err := writeHookSessionState(client, event.SessionID()); err != nil {
			return err
		}
		if err := clearHookActiveSubagentState(client, event.SessionID(), ""); err != nil {
			return err
		}
		if err := cleanupHookActiveSubagentStates(client); err != nil {
			return err
		}
		if err := clearHookSessionEndMarker(client, event.SessionID()); err != nil {
			return err
		}
		if workspace != "" {
			if err := writeHookWorkspaceState(client, workspace); err != nil {
				return err
			}
		} else if err := clearHookWorkspaceState(client); err != nil {
			return err
		}
		c.runOpportunisticSessionGC(ctx, resolvedDBPath, event.SessionID())
		if output != nil {
			if _, err := fmt.Fprintln(output, event.SessionID()); err != nil {
				return xerrors.Errorf("failed to print session ID: %w", err)
			}
		}
		return nil
	case "end":
		agent, err := resolveHookAgent(client, payload)
		if err != nil {
			return err
		}
		sessionID := types.SessionID(hookPayloadString(payload, "session_id", ""))
		if sessionID == "" {
			sessionID, err = readHookSessionState(client)
			if err != nil {
				return err
			}
		}
		if sessionID == "" {
			return nil
		}
		if alreadyEnded, err := hookSessionEndAlreadyRecorded(client, sessionID); err == nil && alreadyEnded {
			if clearErr := clearHookSessionState(client); clearErr != nil {
				return clearErr
			}
			if clearErr := clearHookWorkspaceState(client); clearErr != nil {
				return clearErr
			}
			if strings.TrimSpace(client) == "claude" {
				if clearErr := clearHookCancellationDiagnosticsForSession(client, "SessionEnd", sessionID); clearErr != nil {
					slog.Debug("hook already-ended cancellation diagnostic cleanup failed", "client", client, "session_id", sessionID, "error", clearErr)
				}
			}
			return nil
		} else if err != nil {
			return err
		}

		hookCancellationDiagnosticPath := ""
		shouldTrackClaudeCancellation := strings.TrimSpace(client) == "claude"
		if shouldTrackClaudeCancellation {
			if path, err := beginHookCancellationDiagnostic(
				client,
				"SessionEnd",
				"'traceary' 'hook' 'session' 'claude' 'end'",
				sessionID,
				"",
			); err != nil {
				slog.Debug("hook session-end cancellation diagnostic failed", "client", client, "session_id", sessionID, "error", err)
			} else {
				hookCancellationDiagnosticPath = path
			}
		}
		workspace, err := resolveHookWorkspace(ctx, payload, client, true)
		if err != nil {
			return err
		}
		if shouldTrackClaudeCancellation {
			if err := updateHookCancellationDiagnosticWorkspace(hookCancellationDiagnosticPath, workspace); err != nil {
				slog.Debug("hook session-end cancellation diagnostic workspace update failed", "client", client, "session_id", sessionID, "path", hookCancellationDiagnosticPath, "error", err)
			}
		}
		resolvedDBPath, err := resolveDBPath(dbPath)
		if err != nil {
			return err
		}
		c.applyDatabasePath(resolvedDBPath)
		if err := c.storeManagement.Initialize(ctx); err != nil {
			return xerrors.Errorf("failed to initialize store: %w", err)
		}
		if _, err := c.session.End(ctx, types.Client("hook"), agent, sessionID, workspace, ""); err != nil {
			return xerrors.Errorf("failed to record hook session end: %w", err)
		}
		if shouldTrackClaudeCancellation {
			if err := clearHookCancellationDiagnosticsForSession(client, "SessionEnd", sessionID); err != nil {
				slog.Debug("hook session-end cancellation diagnostic cleanup failed", "client", client, "session_id", sessionID, "path", hookCancellationDiagnosticPath, "error", err)
				_ = clearHookCancellationDiagnostic(hookCancellationDiagnosticPath)
			}
		}
		if err := clearHookSessionState(client); err != nil {
			return err
		}
		if err := clearHookWorkspaceState(client); err != nil {
			return err
		}
		if err := clearHookActiveSubagentState(client, sessionID, ""); err != nil {
			return err
		}
		if err := cleanupHookActiveSubagentStates(client); err != nil {
			return err
		}
		if err := markHookSessionEnded(client, sessionID); err != nil {
			return err
		}
		// Schedule only after every primary event and hook-state transition is
		// complete, so worker startup cannot consume the cleanup budget.
		c.scheduleHookMemoryExtract(hookMemoryExtractRequest{
			SessionID: sessionID, Workspace: workspace, DBPath: resolvedDBPath, SourceBoundary: "session_end",
		})
		return nil
	case "stop":
		// Codex fires Stop after every assistant response, not when the
		// conversation is over: the same Codex session keeps receiving
		// turns afterwards (one rollout JSONL spans days), so treating
		// Stop as a session end closed multi-turn sessions early and
		// emptied active-session reads (#1170). Stop is a turn boundary:
		// keep the session row open and the hook state intact so later
		// prompts and tool audits resolve to the same session. A session
		// now ends via an explicit end signal (hook end action, MCP
		// manage_session) or stale GC (`traceary session gc`).
		sessionID := types.SessionID(hookPayloadString(payload, "session_id", ""))
		if sessionID == "" {
			var err error
			sessionID, err = readHookSessionState(client)
			if err != nil {
				return err
			}
		}
		if sessionID == "" {
			return nil
		}
		if alreadyEnded, err := hookSessionEndAlreadyRecorded(client, sessionID); err == nil && alreadyEnded {
			// The session was already ended explicitly; a late stop only
			// cleans up leftover state so later events cannot re-attach
			// to the ended session through stale state.
			if clearErr := clearHookSessionState(client); clearErr != nil {
				return clearErr
			}
			return clearHookWorkspaceState(client)
		} else if err != nil {
			return err
		}
		// Codex exposes no true session-end hook, so keep requesting
		// extraction at each turn boundary. The durable queue coalesces
		// repeated requests and moves extraction outside the host budget.
		if c.memory == nil {
			return nil
		}
		resolvedDBPath, err := resolveDBPath(dbPath)
		if err != nil {
			return err
		}
		workspace, err := resolveHookWorkspace(ctx, payload, client, true)
		if err != nil {
			return err
		}
		c.scheduleHookMemoryExtract(hookMemoryExtractRequest{
			SessionID: sessionID, Workspace: workspace, DBPath: resolvedDBPath, SourceBoundary: "turn_boundary",
		})
		return nil
	default:
		return xerrors.Errorf("unsupported hook session action: %s", action)
	}
}
