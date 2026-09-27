package cli

import (
	"context"
	"encoding/json"
	"io"
	"strings"
	"time"

	apptypes "github.com/duck8823/traceary/application/types"
	"github.com/duck8823/traceary/domain/types"
	"github.com/spf13/cobra"
	"golang.org/x/xerrors"
)

func (c *RootCLI) newHookPassiveCommand() *cobra.Command {
	var dbPath string
	cmd := &cobra.Command{Use: "passive <client> <interrupt|stop_failure|session_end>", Hidden: true, Args: exactArgsLocalized(2), RunE: func(cmd *cobra.Command, args []string) error {
		return c.runPassiveHookDurably(cmd.Context(), cmd.InOrStdin(), args[0], args[1], dbPath)
	}}
	cmd.Flags().StringVar(&dbPath, "db-path", "", dbPathFlagUsage())
	return cmd
}

// Normalize before spooling: failure details and partial assistant text can contain secrets.
func (c *RootCLI) runPassiveHookDurably(ctx context.Context, input io.Reader, client, action, dbPath string) error {
	return runHookBestEffort("passive", func() error {
		var payload []byte
		var err error
		if client == kimiHookClient {
			payload, err = normalizeKimiHookPayload(input)
		} else {
			payload, err = readHookPayload(input)
		}
		if err != nil {
			return err
		}
		clean := map[string]any{}
		keys := append([]string{"session_id", "cwd", "hook_event_name"}, provenHookDeliveryIDFields(client)...)
		for _, key := range keys {
			if value := hookPayloadString(payload, key, ""); value != "" {
				clean[key] = value
			}
		}
		// Freeze attribution before persistence; replay must not consult a different wrapper environment.
		nativeSessionID := strings.TrimSpace(hookPayloadString(payload, "session_id", ""))
		if nativeSessionID == "" {
			delete(clean, "session_id")
		} else {
			clean["session_id"] = nativeSessionID
			if wrapperSessionID := explicitOneShotRuntimeSessionID(); wrapperSessionID != "" {
				clean["session_id"] = wrapperSessionID.String()
			}
		}
		payload, err = json.Marshal(clean)
		if err != nil {
			return xerrors.Errorf("failed to normalize passive hook: %w", err)
		}
		ctx, cancel := context.WithTimeout(ctx, 2500*time.Millisecond)
		defer cancel()
		return c.runHookDurably(ctx, "passive", hookInvocationSpec{Command: "passive", Client: client, Action: action, DBPath: dbPath}, newExplicitHookPayloadReader(payload), func(input io.Reader) error {
			return c.runHookPassive(ctx, input, client, action, dbPath)
		})
	})
}

func (c *RootCLI) runHookPassive(ctx context.Context, input io.Reader, client, action, dbPath string) error {
	if action != "interrupt" && action != "stop_failure" && action != "session_end" {
		return xerrors.Errorf("unsupported passive hook action")
	}
	if c.event == nil || c.storeManagement == nil {
		return xerrors.Errorf("passive hook usecase is not configured")
	}
	payload, err := readHookPayload(input)
	if err != nil {
		return err
	}
	sessionID := types.SessionID(strings.TrimSpace(hookPayloadString(payload, "session_id", "")))
	// Never attach an identity-free failure to a different active session.
	if sessionID == "" {
		return nil
	}
	ctx = apptypes.WithSourceHook(ctx, action)
	// Runtime close can recur on the same resumable thread: session_id is not a delivery ID.
	ctx = apptypes.WithHookDelivery(ctx, apptypes.HookDeliveryInputOf(resolveHookDeliveryNativeID(payload, client, "interrupt"), hookPayloadString(payload, "cwd", "")))
	agent, err := resolveHookAgent(client, payload)
	if err != nil {
		return err
	}
	workspace, err := resolveHookWorkspace(ctx, payload, client, true)
	if err != nil {
		return err
	}
	resolved, err := resolveDBPath(dbPath)
	if err != nil {
		return err
	}
	c.applyDatabasePath(resolved)
	if err := c.storeManagement.Initialize(ctx); err != nil {
		return xerrors.Errorf("failed to initialize passive hook store: %w", err)
	}
	message := "Host turn interrupted."
	if action == "session_end" {
		message = "Host session closed."
	}
	if action == "stop_failure" {
		message = "Host turn failed."
	}
	_, err = c.event.Log(ctx, message, types.EventKindNote, types.Client("hook"), agent, sessionID, workspace, apptypes.LogRedaction{})
	if err != nil {
		return xerrors.Errorf("failed to record passive hook: %w", err)
	}
	return nil
}

func boundedPassiveHook(spec hookInvocationSpec) bool {
	return spec.Command == "passive"
}
