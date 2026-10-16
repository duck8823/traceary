package cli

import (
	"bytes"
	"context"
	"strings"

	"github.com/duck8823/traceary/domain/types"
	"golang.org/x/xerrors"
)

// hookWorkspaceCapture is trusted local acquisition metadata, never a field
// extracted from the host payload. An absent value means legacy resolution;
// a present empty value pins an unknown workspace without a later fallback.
type hookWorkspaceCapture struct {
	value  *string
	failed bool
}

type hookWorkspaceCaptureContextKey struct{}

func capturedHookWorkspace(ctx context.Context) (types.Workspace, bool, error) {
	capture, _ := ctx.Value(hookWorkspaceCaptureContextKey{}).(hookWorkspaceCapture)
	if capture.failed {
		return "", true, xerrors.New("hook workspace capture failed; refusing to reassociate delivery")
	}
	if capture.value == nil {
		return "", false, nil
	}
	return types.Workspace(*capture.value), true, nil
}

func (r hookSpoolRecord) payloadReader() *explicitHookPayloadReader {
	return &explicitHookPayloadReader{
		Reader:    bytes.NewReader([]byte(r.Payload)),
		receiptID: r.ReceiptID,
		workspace: hookWorkspaceCapture{value: r.ResolvedWorkspace, failed: r.WorkspaceCaptureFailed},
	}
}

// captureHookWorkspace follows the existing adapter's routing rules before
// spooling. It does not mutate host payloads, hook state or session metadata.
func captureHookWorkspace(ctx context.Context, spec hookInvocationSpec, payload []byte) hookWorkspaceCapture {
	normalized, preferState, applicable, err := hookWorkspaceCapturePayload(spec, payload)
	if !applicable {
		return hookWorkspaceCapture{}
	}
	if err != nil {
		return hookWorkspaceCapture{failed: true}
	}
	workspace, err := resolveHookWorkspace(ctx, normalized, spec.Client, preferState)
	if err != nil {
		return hookWorkspaceCapture{failed: true}
	}
	value := workspace.String()
	return hookWorkspaceCapture{value: &value}
}

func hookWorkspaceCapturePayload(spec hookInvocationSpec, payload []byte) ([]byte, bool, bool, error) {
	preferState := true
	switch spec.Command {
	case "usage":
		// Usage is session-attributed, not event-workspace-attributed. Its queries
		// join canonical sessions.workspace; keep sanitized payloads path-free.
		return nil, false, false, nil
	case "session":
		preferState = spec.Action != "start"
	case "passive", "audit", "compact", "subagent-start", "subagent-stop", "prompt", "transcript":
	case "grok", "muse", "kimi":
		if spec.Action == "pre-tool-use" && spec.Command != "kimi" {
			return nil, false, false, nil // Validation-only adapters.
		}
		var err error
		switch spec.Command {
		case "grok":
			payload, err = normalizeGrokHookPayload(newExplicitHookPayloadReader(payload))
		case "muse":
			payload, err = normalizeMuseHookPayload(newExplicitHookPayloadReader(payload))
		case "kimi":
			payload, err = normalizeKimiHookPayload(newExplicitHookPayloadReader(payload))
		}
		if err != nil {
			return nil, false, true, err
		}
		preferState = spec.Action != "session-start"
	case "antigravity":
		if spec.Action == "pre-tool-use" {
			return nil, false, false, nil
		}
		cwd := antigravityWorkspaceCwd(payload)
		conversationID := strings.TrimSpace(hookPayloadString(payload, "conversationId", ""))
		switch spec.Action {
		case "pre-invocation":
			if conversationID == "" {
				return nil, false, false, nil
			}
			existing, err := readHookSessionState(antigravityHookClient)
			if err != nil {
				return nil, false, true, err
			}
			preferState = existing.String() == conversationID
		case "post-tool-use":
			step := strings.TrimSpace(hookPayloadString(payload, "stepIdx", ""))
			if conversationID == "" || step == "" {
				return nil, false, false, nil
			}
			pending, ok, err := readAntigravityPendingCommand(conversationID, step)
			if err != nil {
				return nil, false, true, err
			}
			if !ok || strings.TrimSpace(pending.Command) == "" {
				return nil, false, false, nil
			}
			cwd = firstNonEmpty(pending.Cwd, cwd)
		}
		payload = normalizeAntigravityPayload(antigravityNormalizeOptions{cwd: cwd})
	default:
		return nil, false, false, nil
	}
	return payload, preferState, true, nil
}
