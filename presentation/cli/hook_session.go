package cli

import (
	"context"
	"errors"
	"github.com/duck8823/traceary/application/usecase"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/xerrors"

	apptypes "github.com/duck8823/traceary/application/types"
	"github.com/duck8823/traceary/domain/model"
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
	ctx = withHookSpoolReceipt(ctx, input)
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
	ctx = withResolvedHookDelivery(ctx, payload, client)

	switch action {
	case "start":
		resolvedDBPath, err := resolveDBPath(dbPath)
		if err != nil {
			return err
		}
		wrapperSessionID := explicitOneShotRuntimeSessionID()
		if strings.TrimSpace(os.Getenv(runtimeModeEnvKey)) == types.RuntimeModeOneShot.String() {
			inheritedStore := strings.TrimSpace(os.Getenv("TRACEARY_DB_PATH"))
			if wrapperSessionID == "" || !filepath.IsAbs(inheritedStore) || filepath.Clean(inheritedStore) != resolvedDBPath {
				return xerrors.Errorf("nested session start contradicts fixed wrapper SID/store binding: %w", model.ErrInvalidSessionState)
			}
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
		runtimeMode := types.RuntimeModeInteractive
		if wrapperSessionID != "" {
			sessionID = wrapperSessionID
			runtimeMode = types.RuntimeModeOneShot
		}
		parentSessionID := types.SessionID(strings.TrimSpace(os.Getenv("TRACEARY_PARENT_SESSION_ID")))
		if parentSessionID == "" && runtimeMode != types.RuntimeModeOneShot {
			inferredParentSessionID, inferErr := c.inferHookParentSessionID(ctx, payload, client, agent, workspace)
			if inferErr != nil {
				return inferErr
			}
			parentSessionID = inferredParentSessionID
		}
		var event *model.Event
		if runtimeMode == types.RuntimeModeOneShot {
			capture, ok := c.session.(usecase.OneShotCaptureUsecase)
			if !ok {
				return xerrors.Errorf("one-shot capture binding lookup is not configured")
			}
			event, err = capture.CaptureOneShotStart(ctx, sessionID, parentSessionID)
		} else {
			event, err = c.session.Start(ctx, types.Client("hook"), agent, sessionID, workspace, parentSessionID)
		}
		if err != nil {
			return xerrors.Errorf("failed to record hook session start: %w", err)
		}
		// Host-reported model is optional. Claude may omit it; Gemini/Antigravity
		// never send it. Empty degrades to no model (never fabricated).
		if modelName := strings.TrimSpace(hookPayloadString(payload, "model", "")); modelName != "" {
			if _, err := c.session.SetModelIfEmpty(ctx, event.SessionID(), modelName); err != nil {
				slog.Debug("failed to attach host-reported session model", "error", err, "session_id", event.SessionID().String())
			}
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
		canonicalWorkspace, err := c.canonicalHookSessionWorkspace(ctx, event.SessionID(), workspace)
		if err != nil {
			return err
		}
		if canonicalWorkspace != "" {
			if err := writeHookWorkspaceState(client, canonicalWorkspace); err != nil {
				return err
			}
		} else if err := clearHookWorkspaceState(client); err != nil {
			return err
		}
		c.maintainHookActivityLeases(event.SessionID())
		// SessionStart stdout is the wake-injection channel only — never print
		// the bare session id (#1684). Prefer the canonical workspace when known.
		injectWorkspace := workspace
		if canonicalWorkspace != "" {
			injectWorkspace = canonicalWorkspace
		}
		c.maybeInjectWakeSummaries(ctx, output, client, event.SessionID(), injectWorkspace, resolvedDBPath)
		return nil
	case "end":
		if explicitOneShotRuntimeSessionID() != "" {
			// A nested host SessionEnd fires while the supervised child is
			// still exiting. Only the one-shot wrapper writes the typed
			// terminal reason after the child exits; recording a host end
			// here would either fail on the unknown host session or pre-empt
			// the wrapper's reason with an untyped success.
			return nil
		}
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

		resolvedDBPath, err := resolveDBPath(dbPath)
		if err != nil {
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
				resolvedDBPath,
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
			if err := updateHookCancellationDiagnosticPhase(hookCancellationDiagnosticPath, hookCancellationDiagnosticPhaseWorkspaceResolved); err != nil {
				slog.Debug("hook session-end cancellation diagnostic phase update failed", "client", client, "session_id", sessionID, "path", hookCancellationDiagnosticPath, "error", err)
			}
		}
		c.applyDatabasePath(resolvedDBPath)
		if err := c.storeManagement.Initialize(ctx); err != nil {
			return xerrors.Errorf("failed to initialize store: %w", err)
		}
		if shouldTrackClaudeCancellation {
			if err := updateHookCancellationDiagnosticPhase(hookCancellationDiagnosticPath, hookCancellationDiagnosticPhaseStoreInitialized); err != nil {
				slog.Debug("hook session-end cancellation diagnostic phase update failed", "client", client, "session_id", sessionID, "path", hookCancellationDiagnosticPath, "error", err)
			}
		}
		if _, err := c.session.End(ctx, types.Client("hook"), agent, sessionID, workspace, ""); err != nil {
			if !errors.Is(err, model.ErrSupervisorOwnedSession) {
				return xerrors.Errorf("failed to record hook session end: %w", err)
			}
			slog.Debug("supervisor owns session outcome; ordinary end drained", "client", client)
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
		// Schedule only after every primary event and hook-state transition is
		// complete, so worker startup cannot consume the cleanup budget.
		c.scheduleHookMemoryExtract(hookMemoryExtractRequest{
			SessionID: sessionID, Workspace: workspace, DBPath: resolvedDBPath, SourceBoundary: "session_end",
		})
		c.runHookMemoryDecayBestEffort(ctx, resolvedDBPath)
		// Retain lease cleanup independently of any grouping lifecycle.
		c.maintainHookActivityLeases(sessionID)
		return nil
	case "stop":
		// Stop is a turn boundary. Recorded grouping and routing remain
		// available for later prompts/audits, independent of old end markers.
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
		// Turn-boundary extraction only; decay is reserved for true session-end
		// and subagent-stop so multi-turn hosts are not taxed every Stop.
		return nil
	default:
		return xerrors.Errorf("unsupported hook session action: %s", action)
	}
}
