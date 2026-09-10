package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"strings"

	"github.com/duck8823/traceary/application/usecase"
	"github.com/duck8823/traceary/domain/types"
	"github.com/duck8823/traceary/presentation"
	"golang.org/x/xerrors"
)

// consolidationPromptClients maps hosts whose prompt-time hook can inject a
// refinement request onto the turn being started. Claude and Kimi already
// continue at Stop; Grok Stop uses a JSON envelope instead.
var consolidationPromptClients = map[string]func(io.Writer, string) error{
	"codex":  writeConsolidationPlainText,
	"gemini": writeConsolidationGeminiJSON,
}

func (c *RootCLI) runHookPromptWithConsolidation(
	ctx context.Context,
	output io.Writer,
	client string,
	input io.Reader,
	dbPath string,
) error {
	var captured []byte
	if err := c.runHookDurably(ctx, "prompt", hookInvocationSpec{
		Command: "prompt",
		Client:  client,
		DBPath:  dbPath,
	}, input, func(input io.Reader) error {
		payload, err := readHookPayload(input)
		if err != nil {
			return err
		}
		if err := c.runHookPrompt(ctx, newExplicitHookPayloadReader(payload), client, dbPath); err != nil {
			return err
		}
		captured = payload
		return nil
	}); err != nil {
		return err
	}
	c.maybeInjectConsolidationAtPrompt(ctx, output, client, captured, dbPath)
	return nil
}

func (c *RootCLI) maybeInjectConsolidationAtPrompt(
	ctx context.Context,
	output io.Writer,
	client string,
	payload []byte,
	dbPath string,
) {
	if payload == nil {
		return
	}
	client = strings.TrimSpace(client)
	if client == "codex" && presentation.LoadConfig().Consolidation.CodexPromptOnly {
		c.maybeDeliverPendingCodexConsolidation(ctx, output, payload, client)
		return
	}
	write, ok := consolidationPromptClients[client]
	if !ok {
		return
	}
	req, due := c.consolidationRequestIfDue(ctx, client, payload, dbPath)
	if !due {
		return
	}
	_ = c.recordConsolidationRequest(ctx, req, types.ConsolidationDeliveryAdditionalContext)
	text := formatConsolidationContext(req)
	if err := write(output, text); err != nil {
		slog.Debug("consolidation prompt injection failed",
			"session_id", req.SessionID.String(), "client", client, "error", err)
	}
}

func writeConsolidationPlainText(output io.Writer, text string) error {
	if output == nil {
		return nil
	}
	if !strings.HasSuffix(text, "\n") {
		text += "\n"
	}
	if _, err := io.WriteString(output, text); err != nil {
		return xerrors.Errorf("failed to write consolidation context: %w", err)
	}
	return nil
}

func writeConsolidationGeminiJSON(output io.Writer, text string) error {
	if output == nil {
		return nil
	}
	encoded, err := json.Marshal(map[string]any{
		"hookSpecificOutput": map[string]any{
			"hookEventName":     "BeforeAgent",
			"additionalContext": text,
		},
	})
	if err != nil {
		return xerrors.Errorf("failed to marshal gemini consolidation context: %w", err)
	}
	if _, err := output.Write(encoded); err != nil {
		return xerrors.Errorf("failed to write gemini consolidation context: %w", err)
	}
	return nil
}

func formatConsolidationContext(req consolidationRequest) string {
	coversTo := "<event-id>"
	if id, ok := req.AtEventID.Value(); ok {
		coversTo = id.String()
	}
	return fmt.Sprintf(
		"[Traceary] Session %s has %d audited commands of unrefined material. Before you start this turn, load the traceary-session-refine skill and record what happened.\n"+
			"Run: traceary session refine %s --covers-to %s --summary \"<why + what changed>\" --produced-by agent\n"+
			"Then continue with the user's request.",
		req.SessionID.String(),
		req.Result.Commands,
		req.SessionID.String(),
		coversTo,
	)
}

// maybeDeliverPendingCodexConsolidation writes only a bounded action request.
// Prompt payload content and prior summaries intentionally never enter this route.
func (c *RootCLI) maybeDeliverPendingCodexConsolidation(ctx context.Context, output io.Writer, payload []byte, client string) {
	if c.consolidationRequest == nil {
		return
	}
	sessionID, err := resolveHookTranscriptSessionIDFunc(payload, client)
	if err != nil {
		return
	}
	claimed, err := c.consolidationRequest.ClaimCodexPrompt(ctx, sessionID)
	if err != nil {
		slog.Debug("Codex prompt consolidation claim failed", "session_id", sessionID.String(), "error", err)
		return
	}
	claim, ok := claimed.Value()
	if !ok {
		return
	}
	request := claim.Request()
	req := consolidationRequest{SessionID: request.SessionID(), Client: client, AtEventID: types.Some(request.AtEventID()), MinCommands: request.ThresholdValue(), Result: usecase.ConsolidationPressureResult{Commands: request.PressureValue(), Due: true}}
	if err := writeConsolidationPlainText(output, formatConsolidationContext(req)); err != nil {
		if releaseErr := c.consolidationRequest.ReleaseCodexPrompt(ctx, claim.RequestID(), claim.Token()); releaseErr != nil {
			slog.Debug("Codex prompt consolidation release failed", "session_id", sessionID.String(), "error", releaseErr)
		}
		slog.Debug("Codex prompt consolidation injection failed", "session_id", sessionID.String(), "error", err)
		return
	}
	if err := c.consolidationRequest.ConfirmCodexPrompt(ctx, claim.RequestID(), claim.Token()); err != nil {
		// stdout was already written. An expired lease deliberately permits a later
		// prompt to redeliver, preserving at-least-once rather than losing work.
		slog.Debug("Codex prompt consolidation confirmation failed", "session_id", sessionID.String(), "error", err)
	}
}
