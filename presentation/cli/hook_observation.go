package cli

import (
	"context"
	"github.com/duck8823/traceary/application"
	app "github.com/duck8823/traceary/application/runtime"
	domain "github.com/duck8823/traceary/domain/runtime"
	"github.com/spf13/cobra"
	"strings"
	"time"
)

type hookObservationConfig struct {
	root  string
	flags observationSelectionFlags
}

func (c *RootCLI) registerHookObservationFlags(command *cobra.Command) {
	command.PersistentFlags().StringVar(&c.hookObservation.root, "observe-runtime-dir", "", "Explicit optional telemetry root; never inferred from environment")
	for _, flag := range []struct {
		name  string
		value *string
	}{{"epoch", &c.hookObservation.flags.epoch}, {"run", &c.hookObservation.flags.run}, {"generation", &c.hookObservation.flags.generation}, {"worktree", &c.hookObservation.flags.worktree}, {"profile", &c.hookObservation.flags.profile}, {"host-version", &c.hookObservation.flags.version}} {
		command.PersistentFlags().StringVar(flag.value, "observe-"+flag.name, "", "Optional explicit untrusted correlation; invalid or partial context skips delivery")
	}
}
func hookObservationBoundary(spec hookInvocationSpec) (string, domain.ObservationKind, bool) {
	host := strings.ToLower(strings.TrimSpace(spec.Client))
	switch host {
	case "codex", "claude", "antigravity", "grok":
	default:
		return "", "", false
	}
	switch spec.Command {
	case "antigravity":
		if host != "antigravity" {
			return "", "", false
		}
		switch spec.Action {
		case "pre-invocation":
			return host, domain.ObservationPromptBoundary, true
		case "pre-tool-use", "post-tool-use":
			return host, domain.ObservationToolBoundary, true
		case "stop":
			return host, domain.ObservationTurnBoundary, true
		}
	case "grok":
		if host != "grok" {
			return "", "", false
		}
		switch spec.Action {
		case "session-start":
			return host, domain.ObservationSessionBoundary, true
		case "user-prompt-submit":
			return host, domain.ObservationPromptBoundary, true
		case "pre-tool-use", "post-tool-use":
			return host, domain.ObservationToolBoundary, true
		case "stop":
			return host, domain.ObservationTurnBoundary, true
		}
	case "prompt":
		return host, domain.ObservationPromptBoundary, true
	case "audit":
		return host, domain.ObservationToolBoundary, true
	case "transcript":
		return host, domain.ObservationTurnBoundary, true
	case "session":
		switch spec.Action {
		case "start", "end":
			return host, domain.ObservationSessionBoundary, true
		case "stop":
			return host, domain.ObservationTurnBoundary, true
		}
	}
	return "", "", false
}

// Hook-native IDs remain optional untrusted correlation. No claimed ID grants
// association, readiness or recovery authority; versioned host proof is separate.
func (c *RootCLI) hookObservationNativeIDs(ctx context.Context, payload, host, version string, kind domain.ObservationKind) (string, string) {
	if host == "codex" {
		if c.codexObservationClaims == nil {
			return "", ""
		}
		claims, err := c.codexObservationClaims.Decode(ctx, application.CodexObservationClaimInput{SourceVersion: version, ExpectedBoundary: kind, Payload: payload})
		if err != nil || !claims.Available {
			return "", ""
		}
		return claims.SessionID, claims.TurnID
	}
	if host == "claude" {
		return c.claudeHookObservationSession(ctx, payload, version, kind), ""
	}
	if host == "antigravity" {
		return c.antigravityHookObservationSession(ctx, payload, version, kind), ""
	}
	body := []byte(payload)
	value, _ := lookupHookPayloadValue(body, "session_id")
	session, _ := value.(string)
	value, _ = lookupHookPayloadValue(body, "turn_id")
	turn, _ := value.(string)
	return session, turn
}

func (c *RootCLI) claudeHookObservationSession(ctx context.Context, payload, version string, kind domain.ObservationKind) string {
	if c.claudeObservationClaims == nil {
		return ""
	}
	claim, err := c.claudeObservationClaims.Decode(ctx, application.ClaudeObservationClaimInput{Payload: payload, SourceVersion: version, ExpectedBoundary: kind})
	if err != nil || !claim.Available {
		return ""
	}
	return claim.SessionID
}

// Antigravity provides no event-name proof through this correlation port.
// Only the measured caller boundaries can yield an untrusted session claim.
func (c *RootCLI) antigravityHookObservationSession(ctx context.Context, payload, version string, kind domain.ObservationKind) string {
	if c.antigravityObservationClaims == nil {
		return ""
	}
	claim, err := c.antigravityObservationClaims.Decode(ctx, application.AntigravityObservationClaimInput{Payload: payload, SourceVersion: version, ExpectedBoundary: kind})
	if err != nil || !claim.Available {
		return ""
	}
	return claim.SessionID
}

// deliverHookObservation is deliberately void: optional failures never replace
// primary capture/stdout/exit, receipt/spool ownership or backlog replay policy.
func (c *RootCLI) deliverHookObservation(parent context.Context, started time.Time, spec hookInvocationSpec, record hookSpoolRecord) {
	config := c.hookObservation
	if config.root == "" || hookSpoolDrainRemaining(parent, started, time.Now()) <= app.ObservationDeliveryBudget+hookSpoolDrainReserve {
		return
	}
	ctx, cancel := context.WithTimeout(parent, app.ObservationDeliveryBudget)
	defer cancel()
	client, _ := c.runtimeDaemon.(app.SelectedObservationClient)
	_ = app.NewObservationDelivery(client).Deliver(ctx, config.root, func(deriveCtx context.Context) (app.SelectedObservation, error) {
		if !explicitObservationRoot(config.root) {
			return app.SelectedObservation{}, domain.ErrInvalid
		}
		host, kind, ok := hookObservationBoundary(spec)
		if !ok {
			return app.SelectedObservation{}, domain.ErrUnsupported
		}
		flags := config.flags
		flags.host = host
		flags.kind = string(kind)
		flags.event = record.ReceiptID
		value, err := flags.observation()
		if err != nil {
			return app.SelectedObservation{}, err
		}
		state := value.Metadata.Snapshot()
		state.NativeSessionID, state.NativeTurnID = c.hookObservationNativeIDs(deriveCtx, record.Payload, host, flags.version, kind)
		if metadata, e := domain.DerivedObservationOf(state); e == nil {
			value.Metadata = metadata
		}
		return value, nil
	})
}
