package cli_test

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/duck8823/traceary/application/usecase"
	"github.com/duck8823/traceary/domain/model"
	"github.com/duck8823/traceary/domain/types"
	cli "github.com/duck8823/traceary/presentation/cli"
)

type kimiUsageCaptureStub struct {
	inputs []usecase.KimiUsageCaptureInput
	err    error
}

func (s *kimiUsageCaptureStub) Capture(
	_ context.Context,
	input usecase.KimiUsageCaptureInput,
) (usecase.KimiUsageCaptureResult, error) {
	s.inputs = append(s.inputs, input)
	return usecase.KimiUsageCaptureResult{}, s.err
}

func TestRootCLI_HookKimiCoreEvents(t *testing.T) {
	t.Setenv("TRACEARY_HOOK_STATE_DIR", t.TempDir())
	t.Setenv("TRACEARY_HOOK_STATE_KEY", "kimi-core-events")
	t.Setenv("TRACEARY_WORKSPACE", "github.com/duck8823/traceary")

	homeDir := t.TempDir()
	cli.SetUserHomeDirFunc(func() (string, error) { return homeDir, nil })
	t.Cleanup(cli.ResetUserHomeDirFunc)

	t.Run("records session start with Kimi identity", func(t *testing.T) {
		payload := readKimiFixture(t, "session_start.json")
		sessionStub := &sessionUsecaseStub{startEvent: model.EventOf(
			types.EventID("event-kimi-start"),
			types.EventKindSessionStarted,
			types.Client("hook"),
			types.Agent("kimi"),
			types.SessionID("session_00000000-0000-4000-8000-000000000001"),
			types.Workspace("github.com/duck8823/traceary"),
			"session started",
			time.Now(),
		)}

		stdout, _, gotSession := runKimiHook(t, "session-start", payload, nil, sessionStub)

		if stdout != "" {
			t.Fatalf("SessionStart output = %q, want empty passive-hook output", stdout)
		}
		if got, want := gotSession.startCall.sessionID, types.SessionID("session_00000000-0000-4000-8000-000000000001"); got != want {
			t.Fatalf("session ID = %q, want %q", got, want)
		}
		if got, want := gotSession.startCall.client, types.Client("hook"); got != want {
			t.Fatalf("session client = %q, want %q", got, want)
		}
		if got, want := gotSession.startCall.agent, types.Agent("kimi"); got != want {
			t.Fatalf("session agent = %q, want %q", got, want)
		}
	})

	t.Run("records session end with Kimi identity", func(t *testing.T) {
		payload := readKimiFixture(t, "session_end.json")
		sessionStub := &sessionUsecaseStub{}

		stdout, _, gotSession := runKimiHook(t, "session-end", payload, nil, sessionStub)

		if stdout != "" {
			t.Fatalf("SessionEnd output = %q, want empty passive-hook output", stdout)
		}
		if got, want := gotSession.endCall.sessionID, types.SessionID("session_00000000-0000-4000-8000-000000000001"); got != want {
			t.Fatalf("session ID = %q, want %q", got, want)
		}
		if got, want := gotSession.endCall.agent, types.Agent("kimi"); got != want {
			t.Fatalf("session agent = %q, want %q", got, want)
		}
	})

	t.Run("flattens prompt content blocks and records prompt", func(t *testing.T) {
		stdout, eventStub, _ := runKimiHook(t, "user-prompt-submit", readKimiFixture(t, "user_prompt_submit.json"), nil, nil)

		if stdout != "" {
			t.Fatalf("UserPromptSubmit output = %q, want empty passive-hook output", stdout)
		}
		if got, want := eventStub.logCall.kind, types.EventKindPrompt; got != want {
			t.Fatalf("prompt kind = %q, want %q", got, want)
		}
		if got, want := eventStub.logCall.message, "Reply with exactly one word: pong."; got != want {
			t.Fatalf("prompt body = %q, want %q", got, want)
		}
		if got, want := eventStub.logCall.agent, types.Agent("kimi"); got != want {
			t.Fatalf("prompt agent = %q, want %q", got, want)
		}
		if got, want := eventStub.logCall.sourceHook, "user_prompt_submit"; got != want {
			t.Fatalf("prompt source hook = %q, want %q", got, want)
		}
	})

	t.Run("keeps PreToolUse fail open without recording a duplicate audit", func(t *testing.T) {
		stdout, eventStub, _ := runKimiHook(t, "pre-tool-use", readKimiFixture(t, "pre_tool_use.json"), nil, nil)

		if stdout != "" {
			t.Fatalf("PreToolUse output = %q, want empty passive-hook output", stdout)
		}
		if eventStub.auditCall.command != "" {
			t.Fatalf("PreToolUse recorded audit %q, want validation-only boundary", eventStub.auditCall.command)
		}
	})

	t.Run("records completed tool audit from PostToolUse", func(t *testing.T) {
		stdout, eventStub, _ := runKimiHook(t, "post-tool-use", readKimiFixture(t, "post_tool_use.json"), nil, nil)

		if stdout != "" {
			t.Fatalf("PostToolUse output = %q, want empty passive-hook output", stdout)
		}
		if got, want := eventStub.auditCall.command, "echo hello-from-kimi-probe"; got != want {
			t.Fatalf("audit command = %q, want %q", got, want)
		}
		if got, want := eventStub.auditCall.output, "hello-from-kimi-probe\n"; got != want {
			t.Fatalf("audit output = %q, want %q", got, want)
		}
		if got, want := eventStub.auditCall.agent, types.Agent("kimi"); got != want {
			t.Fatalf("audit agent = %q, want %q", got, want)
		}
		if eventStub.auditCall.failed {
			t.Fatal("PostToolUse audit must not be flagged failed")
		}
	})

	t.Run("flags failed tool audit from PostToolUseFailure error object", func(t *testing.T) {
		stdout, eventStub, _ := runKimiHook(t, "post-tool-use-failure", readKimiFixture(t, "post_tool_use_failure.json"), nil, nil)

		if stdout != "" {
			t.Fatalf("PostToolUseFailure output = %q, want empty passive-hook output", stdout)
		}
		if got, want := eventStub.auditCall.command, "ls /nonexistent-dir"; got != want {
			t.Fatalf("audit command = %q, want %q", got, want)
		}
		if !eventStub.auditCall.failed {
			t.Fatal("PostToolUseFailure audit must be flagged failed")
		}
		if !strings.Contains(eventStub.auditCall.output, "No such file or directory") {
			t.Fatalf("audit output = %q, want the flattened error message", eventStub.auditCall.output)
		}
	})

	t.Run("records transcript from the session wire log on Stop", func(t *testing.T) {
		// wire_main.jsonl is a sanitized capture of a real 0.27.0 wire log:
		// two turns, the last carrying a think and a text content.part row.
		seedKimiSessionFromFixture(t, homeDir, "session_00000000-0000-4000-8000-000000000001", "wire_main.jsonl")

		stdout, eventStub, _ := runKimiHook(t, "stop", readKimiFixture(t, "stop.json"), nil, nil)

		if stdout != "" {
			t.Fatalf("Stop output = %q, want empty passive-hook output", stdout)
		}
		if got, want := eventStub.logCall.kind, types.EventKindTranscript; got != want {
			t.Fatalf("transcript kind = %q, want %q", got, want)
		}
		if !strings.Contains(eventStub.logCall.message, "done") {
			t.Fatalf("transcript body = %q, want the wire log text block", eventStub.logCall.message)
		}
		if !strings.Contains(eventStub.logCall.message, "thinking about the failing command") {
			t.Fatalf("transcript body = %q, want the wire log thinking block", eventStub.logCall.message)
		}
	})

	t.Run("tolerates numeric turnId in wire log rows", func(t *testing.T) {
		seedKimiSession(t, homeDir, "session_00000000-0000-4000-8000-000000000001", []string{
			`{"type":"metadata","protocol_version":"1.4","created_at":1784466738324}`,
			`{"type":"context.append_loop_event","event":{"type":"content.part","turnId":0,"part":{"type":"text","text":"pong"}},"time":1784466740000}`,
		})

		stdout, eventStub, _ := runKimiHook(t, "stop", readKimiFixture(t, "stop.json"), nil, nil)

		if stdout != "" {
			t.Fatalf("Stop output = %q, want empty passive-hook output", stdout)
		}
		if !strings.Contains(eventStub.logCall.message, "pong") {
			t.Fatalf("transcript body = %q, want the numeric-turn wire text block", eventStub.logCall.message)
		}
	})

	t.Run("keeps failure marker when the error object has no message", func(t *testing.T) {
		for _, tc := range []struct {
			name        string
			errorJSON   string
			wantContain string
		}{
			{name: "code only", errorJSON: `{"code":"internal","retryable":false}`, wantContain: "internal"},
			{name: "blank message", errorJSON: `{"code":"io","message":"  ","retryable":true}`, wantContain: "io"},
			{name: "empty object", errorJSON: `{}`, wantContain: "unknown error"},
			{name: "null error", errorJSON: `null`, wantContain: "unknown error"},
		} {
			t.Run(tc.name, func(t *testing.T) {
				payload := `{"hook_event_name":"PostToolUseFailure","session_id":"session_00000000-0000-4000-8000-000000000001","cwd":"/workspace/kimi-contract-probe","tool_name":"Bash","tool_input":{"command":"ls /nonexistent-dir"},"tool_call_id":"tool_1","error":` + tc.errorJSON + `}`

				stdout, eventStub, _ := runKimiHook(t, "post-tool-use-failure", payload, nil, nil)

				if stdout != "" {
					t.Fatalf("PostToolUseFailure output = %q, want empty passive-hook output", stdout)
				}
				if !eventStub.auditCall.failed {
					t.Fatal("failure without a message must still be flagged failed")
				}
				if !strings.Contains(eventStub.auditCall.output, tc.wantContain) {
					t.Fatalf("audit output = %q, want fallback marker containing %q", eventStub.auditCall.output, tc.wantContain)
				}
			})
		}
	})

	t.Run("skips transcript when the session dir escapes the sessions root", func(t *testing.T) {
		for _, tc := range []struct {
			name       string
			sessionDir string
		}{
			{name: "absolute outside path", sessionDir: t.TempDir()},
			{name: "dot-dot traversal", sessionDir: filepath.Join(homeDir, ".kimi-code", "sessions", "..", "..")},
		} {
			t.Run(tc.name, func(t *testing.T) {
				writeKimiSessionIndex(t, homeDir, "session_00000000-0000-4000-8000-000000000001", tc.sessionDir)

				stdout, eventStub, _ := runKimiHook(t, "stop", readKimiFixture(t, "stop.json"), nil, nil)

				if stdout != "" {
					t.Fatalf("Stop output = %q, want empty passive-hook output", stdout)
				}
				if eventStub.logCall.kind != "" {
					t.Fatalf("Stop with an escaping session dir recorded %q, want silent skip", eventStub.logCall.kind)
				}
			})
		}
	})

	t.Run("skips transcript when the session dir is a symlink escape", func(t *testing.T) {
		outside := t.TempDir()
		link := filepath.Join(homeDir, ".kimi-code", "sessions", "wd_evil_000000000000", "session_00000000-0000-4000-8000-000000000001")
		if err := os.MkdirAll(filepath.Dir(link), 0o755); err != nil {
			t.Fatalf("mkdir symlink parent: %v", err)
		}
		if err := os.Symlink(outside, link); err != nil {
			t.Fatalf("symlink: %v", err)
		}
		writeKimiSessionIndex(t, homeDir, "session_00000000-0000-4000-8000-000000000001", link)

		stdout, eventStub, _ := runKimiHook(t, "stop", readKimiFixture(t, "stop.json"), nil, nil)

		if stdout != "" {
			t.Fatalf("Stop output = %q, want empty passive-hook output", stdout)
		}
		if eventStub.logCall.kind != "" {
			t.Fatalf("Stop with a symlink-escaped session dir recorded %q, want silent skip", eventStub.logCall.kind)
		}
	})

	t.Run("skips transcript when the wire log itself is a symlink escape", func(t *testing.T) {
		// The session dir is legitimate, but agents/main/wire.jsonl points
		// outside the sessions root — containment must hold for the final
		// resolved file, not just the directory.
		outsideFile := filepath.Join(t.TempDir(), "wire.jsonl")
		if err := os.WriteFile(outsideFile, []byte(`{"type":"context.append_loop_event","event":{"type":"content.part","turnId":"0","part":{"type":"text","text":"escaped"}}}`+"\n"), 0o600); err != nil {
			t.Fatalf("write outside wire log: %v", err)
		}
		kimiHome := filepath.Join(homeDir, ".kimi-code")
		sessionDir := filepath.Join(kimiHome, "sessions", "wd_probe_000000000000", "session_00000000-0000-4000-8000-000000000001")
		if err := os.MkdirAll(filepath.Join(sessionDir, "agents", "main"), 0o755); err != nil {
			t.Fatalf("mkdir wire log dir: %v", err)
		}
		wirePath := filepath.Join(sessionDir, "agents", "main", "wire.jsonl")
		if err := os.Remove(wirePath); err != nil && !os.IsNotExist(err) {
			t.Fatalf("remove pre-seeded wire log: %v", err)
		}
		if err := os.Symlink(outsideFile, wirePath); err != nil {
			t.Fatalf("symlink wire log: %v", err)
		}
		writeKimiSessionIndex(t, homeDir, "session_00000000-0000-4000-8000-000000000001", sessionDir)

		stdout, eventStub, _ := runKimiHook(t, "stop", readKimiFixture(t, "stop.json"), nil, nil)

		if stdout != "" {
			t.Fatalf("Stop output = %q, want empty passive-hook output", stdout)
		}
		if eventStub.logCall.kind != "" {
			t.Fatalf("Stop with a symlink-escaped wire log recorded %q, want silent skip", eventStub.logCall.kind)
		}
	})

	t.Run("skips transcript silently when the session index has no entry", func(t *testing.T) {
		payload := strings.Replace(readKimiFixture(t, "stop.json"), "session_00000000-0000-4000-8000-000000000001", "session_99999999-9999-4999-8999-999999999999", 1)

		stdout, eventStub, _ := runKimiHook(t, "stop", payload, nil, nil)

		if stdout != "" {
			t.Fatalf("Stop output = %q, want empty passive-hook output", stdout)
		}
		if eventStub.logCall.kind != "" {
			t.Fatalf("Stop without a session index recorded %q, want silent skip", eventStub.logCall.kind)
		}
	})

	t.Run("records compact markers with the trigger body", func(t *testing.T) {
		stdout, eventStub, _ := runKimiHook(t, "pre-compact", readKimiFixture(t, "pre_compact.json"), nil, nil)

		if stdout != "" {
			t.Fatalf("PreCompact output = %q, want empty passive-hook output", stdout)
		}
		if got, want := eventStub.logCall.kind, types.EventKindCompactSummary; got != want {
			t.Fatalf("pre-compact kind = %q, want %q", got, want)
		}
		if got, want := eventStub.logCall.message, "auto"; got != want {
			t.Fatalf("pre-compact body = %q, want %q", got, want)
		}
		if got, want := eventStub.logCall.sourceHook, "pre_compact"; got != want {
			t.Fatalf("pre-compact source hook = %q, want %q", got, want)
		}

		stdout, eventStub, _ = runKimiHook(t, "post-compact", readKimiFixture(t, "post_compact.json"), nil, nil)

		if stdout != "" {
			t.Fatalf("PostCompact output = %q, want empty passive-hook output", stdout)
		}
		if got, want := eventStub.logCall.kind, types.EventKindCompactSummary; got != want {
			t.Fatalf("post-compact kind = %q, want %q", got, want)
		}
		if got, want := eventStub.logCall.message, "auto"; got != want {
			t.Fatalf("post-compact body = %q, want %q", got, want)
		}
		if got, want := eventStub.logCall.sourceHook, "post_compact"; got != want {
			t.Fatalf("post-compact source hook = %q, want %q", got, want)
		}
	})

	t.Run("attributes subagent start from the Agent tool and end from SubagentStop", func(t *testing.T) {
		sessionStub := &sessionUsecaseStub{}

		stdout, _, gotSession := runKimiHook(t, "pre-tool-use", readKimiFixture(t, "pre_tool_use_agent.json"), nil, sessionStub)

		if stdout != "" {
			t.Fatalf("PreToolUse(Agent) output = %q, want empty passive-hook output", stdout)
		}
		if got, want := gotSession.startChildCall.parent, types.SessionID("session_00000000-0000-4000-8000-000000000001"); got != want {
			t.Fatalf("subagent parent = %q, want %q", got, want)
		}
		wantChild := types.SessionID("session_00000000-0000-4000-8000-000000000001:sub:tool_0000000000000000000000AA")
		if got := gotSession.startChildCall.childID; got != wantChild {
			t.Fatalf("subagent child = %q, want %q", got, wantChild)
		}
		if got, want := gotSession.startChildCall.agent, types.Agent("kimi/explore"); got != want {
			t.Fatalf("subagent agent = %q, want %q", got, want)
		}

		stdout, _, gotSession = runKimiHook(t, "subagent-stop", readKimiFixture(t, "subagent_stop.json"), nil, sessionStub)

		if stdout != "" {
			t.Fatalf("SubagentStop output = %q, want empty passive-hook output", stdout)
		}
		if got := gotSession.endCall.sessionID; got != wantChild {
			t.Fatalf("subagent-stop ended session = %q, want the active child %q", got, wantChild)
		}
	})

	t.Run("ignores non-Agent PreToolUse calls for subagent attribution", func(t *testing.T) {
		sessionStub := &sessionUsecaseStub{}

		stdout, _, gotSession := runKimiHook(t, "pre-tool-use", readKimiFixture(t, "pre_tool_use.json"), nil, sessionStub)

		if stdout != "" {
			t.Fatalf("PreToolUse(Bash) output = %q, want empty passive-hook output", stdout)
		}
		if gotSession.startChildCall.childID != "" {
			t.Fatalf("PreToolUse(Bash) attributed a child %q, want no subagent start", gotSession.startChildCall.childID)
		}
	})

	t.Run("keeps SubagentStop without an active child fail open", func(t *testing.T) {
		sessionStub := &sessionUsecaseStub{}

		stdout, _, gotSession := runKimiHook(t, "subagent-stop", readKimiFixture(t, "subagent_stop.json"), nil, sessionStub)

		if stdout != "" {
			t.Fatalf("SubagentStop output = %q, want empty passive-hook output", stdout)
		}
		if gotSession.endCall.sessionID != "" {
			t.Fatalf("SubagentStop without an active child ended %q, want no-op", gotSession.endCall.sessionID)
		}
	})

	t.Run("handles compact payload variants", func(t *testing.T) {
		for _, tc := range []struct {
			name     string
			event    string
			payload  string
			wantBody string
		}{
			{name: "missing trigger falls back to generic marker", event: "post-compact", payload: `{"hook_event_name":"PostCompact","session_id":"session_00000000-0000-4000-8000-000000000001","cwd":"/workspace/kimi-contract-probe"}`, wantBody: "compact triggered"},
			{name: "manual trigger passes through", event: "post-compact", payload: `{"hook_event_name":"PostCompact","session_id":"session_00000000-0000-4000-8000-000000000001","cwd":"/workspace/kimi-contract-probe","trigger":"manual","estimated_token_count":42}`, wantBody: "manual"},
			{name: "numeric token count without trigger", event: "pre-compact", payload: `{"hook_event_name":"PreCompact","session_id":"session_00000000-0000-4000-8000-000000000001","cwd":"/workspace/kimi-contract-probe","token_count":636}`, wantBody: ""},
		} {
			t.Run(tc.name, func(t *testing.T) {
				stdout, eventStub, _ := runKimiHook(t, tc.event, tc.payload, nil, nil)

				if stdout != "" {
					t.Fatalf("%s output = %q, want empty passive-hook output", tc.event, stdout)
				}
				if tc.wantBody == "" {
					return
				}
				if got := eventStub.logCall.message; got != tc.wantBody {
					t.Fatalf("%s body = %q, want %q", tc.event, got, tc.wantBody)
				}
			})
		}
	})

	t.Run("handles Agent PreToolUse payload variants", func(t *testing.T) {
		for _, tc := range []struct {
			name        string
			payload     string
			wantChildID types.SessionID
			wantAgent   types.Agent
		}{
			{
				name:        "missing tool_call_id does not start a child",
				payload:     `{"hook_event_name":"PreToolUse","session_id":"session_00000000-0000-4000-8000-000000000001","cwd":"/workspace/kimi-contract-probe","tool_name":"Agent","tool_input":{"subagent_type":"explore","prompt":"p"}}`,
				wantChildID: "",
			},
			{
				name:        "missing subagent_type defaults to task",
				payload:     `{"hook_event_name":"PreToolUse","session_id":"session_22222222-2222-4222-8222-222222222222","cwd":"/workspace/kimi-contract-probe","tool_name":"Agent","tool_call_id":"tool_variant_1","tool_input":{"prompt":"p"}}`,
				wantChildID: "session_22222222-2222-4222-8222-222222222222:sub:tool_variant_1",
				wantAgent:   "kimi/task",
			},
		} {
			t.Run(tc.name, func(t *testing.T) {
				sessionStub := &sessionUsecaseStub{}

				stdout, _, gotSession := runKimiHook(t, "pre-tool-use", tc.payload, nil, sessionStub)

				if stdout != "" {
					t.Fatalf("PreToolUse(Agent) output = %q, want empty passive-hook output", stdout)
				}
				if got := gotSession.startChildCall.childID; got != tc.wantChildID {
					t.Fatalf("child = %q, want %q", got, tc.wantChildID)
				}
				if tc.wantAgent != "" {
					if got := gotSession.startChildCall.agent; got != tc.wantAgent {
						t.Fatalf("agent = %q, want %q", got, tc.wantAgent)
					}
				}
			})
		}
	})

	t.Run("SubagentStop ends the latest active child when multiple are active", func(t *testing.T) {
		t.Setenv("TRACEARY_HOOK_STATE_KEY", "kimi-multi-active")
		sessionStub := &sessionUsecaseStub{}
		// Use a distinct parent session id so the leftover active child does
		// not leak into sibling subtests that share the fixture parent id.
		parentID := "session_11111111-1111-4111-8111-111111111111"
		withParent := func(name string) string {
			return strings.Replace(readKimiFixture(t, name), "session_00000000-0000-4000-8000-000000000001", parentID, 1)
		}
		older := strings.Replace(withParent("pre_tool_use_agent.json"), "tool_0000000000000000000000AA", "tool_older", 1)
		latest := strings.Replace(withParent("pre_tool_use_agent.json"), "tool_0000000000000000000000AA", "tool_latest", 1)

		_, _, sessionStub = runKimiHook(t, "pre-tool-use", older, nil, sessionStub)
		_, _, sessionStub = runKimiHook(t, "pre-tool-use", latest, nil, sessionStub)

		stdout, _, gotSession := runKimiHook(t, "subagent-stop", withParent("subagent_stop.json"), nil, sessionStub)

		if stdout != "" {
			t.Fatalf("SubagentStop output = %q, want empty passive-hook output", stdout)
		}
		// Pins the documented latest-active-child fallback semantics for
		// parallel subagents under one parent (same as Claude).
		wantLatest := types.SessionID(parentID + ":sub:tool_latest")
		if got := gotSession.endCall.sessionID; got != wantLatest {
			t.Fatalf("SubagentStop ended %q, want the latest active child %q", got, wantLatest)
		}
	})

	t.Run("keeps a repeated SubagentStop idempotent", func(t *testing.T) {
		t.Setenv("TRACEARY_HOOK_STATE_KEY", "kimi-repeated-stop")
		sessionStub := &sessionUsecaseStub{}

		_, _, sessionStub = runKimiHook(t, "pre-tool-use", readKimiFixture(t, "pre_tool_use_agent.json"), nil, sessionStub)
		_, _, sessionStub = runKimiHook(t, "subagent-stop", readKimiFixture(t, "subagent_stop.json"), nil, sessionStub)
		if sessionStub.endCall.sessionID == "" {
			t.Fatal("first SubagentStop must end the active child")
		}
		sessionStub.endCall.sessionID = ""

		stdout, _, gotSession := runKimiHook(t, "subagent-stop", readKimiFixture(t, "subagent_stop.json"), nil, sessionStub)

		if stdout != "" {
			t.Fatalf("repeated SubagentStop output = %q, want empty passive-hook output", stdout)
		}
		if gotSession.endCall.sessionID != "" {
			t.Fatalf("repeated SubagentStop ended %q, want no duplicate end", gotSession.endCall.sessionID)
		}
	})

	t.Run("keeps malformed compact payloads fail open", func(t *testing.T) {
		stdout, eventStub, _ := runKimiHook(t, "post-compact", "not json", nil, nil)

		if stdout != "" {
			t.Fatalf("malformed PostCompact output = %q, want empty fail-open output", stdout)
		}
		if eventStub.logCall.kind != "" {
			t.Fatalf("malformed PostCompact recorded %q, want fail-open skip", eventStub.logCall.kind)
		}
	})

	t.Run("keeps malformed payloads fail open", func(t *testing.T) {
		stdout, eventStub, _ := runKimiHook(t, "post-tool-use", "not json", nil, nil)

		if stdout != "" {
			t.Fatalf("malformed PostToolUse output = %q, want empty fail-open output", stdout)
		}
		if eventStub.auditCall.command != "" {
			t.Fatalf("malformed payload recorded audit %q, want fail-open skip", eventStub.auditCall.command)
		}
	})
}

func TestRootCLI_HookKimiUsageBoundaries(t *testing.T) {
	t.Setenv("TRACEARY_HOOK_STATE_DIR", t.TempDir())
	t.Setenv("TRACEARY_HOOK_STATE_KEY", "kimi-usage-boundaries")
	t.Setenv("TRACEARY_WORKSPACE", "github.com/duck8823/traceary")

	usage := &kimiUsageCaptureStub{}
	for _, tc := range []struct {
		event    string
		fixture  string
		boundary usecase.KimiUsageBoundary
	}{
		{event: "stop", fixture: "stop.json", boundary: usecase.KimiUsageBoundaryStop},
		{event: "session-end", fixture: "session_end.json", boundary: usecase.KimiUsageBoundarySessionEnd},
	} {
		t.Run(tc.event, func(t *testing.T) {
			usage.inputs = nil
			runKimiHook(
				t,
				tc.event,
				readKimiFixture(t, tc.fixture),
				nil,
				&sessionUsecaseStub{},
				cli.WithKimiUsage(usage),
			)
			if len(usage.inputs) != 1 {
				t.Fatalf("usage captures = %d, want 1", len(usage.inputs))
			}
			input := usage.inputs[0]
			if input.SessionID != "session_00000000-0000-4000-8000-000000000001" ||
				input.ProviderSessionID != "session_00000000-0000-4000-8000-000000000001" ||
				input.Boundary != tc.boundary {
				t.Fatalf("usage input = %+v", input)
			}
		})
	}
}

func runKimiHook(
	t *testing.T,
	event string,
	payload string,
	eventStub *eventUsecaseStub,
	sessionStub *sessionUsecaseStub,
	opts ...cli.RootCLIOption,
) (string, *eventUsecaseStub, *sessionUsecaseStub) {
	t.Helper()
	if eventStub == nil {
		eventStub = &eventUsecaseStub{}
	}
	if sessionStub == nil {
		sessionStub = &sessionUsecaseStub{}
	}

	baseOptions := []cli.RootCLIOption{
		cli.WithStoreManagement(&storeManagementUsecaseStub{}),
		cli.WithEvent(eventStub),
		cli.WithSession(sessionStub),
	}
	rootCmd := newTestRootCLI(append(baseOptions, opts...)...).Command()
	stdout := &bytes.Buffer{}
	rootCmd.SetOut(stdout)
	rootCmd.SetErr(&bytes.Buffer{})
	rootCmd.SetIn(strings.NewReader(payload))
	rootCmd.SetArgs([]string{"hook", "kimi", event})
	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("Execute(hook kimi %s) error = %v", event, err)
	}
	return stdout.String(), eventStub, sessionStub
}

func readKimiFixture(t *testing.T, name string) string {
	t.Helper()
	path := filepath.Join("testdata", "kimi_hooks", "v0.27.0", name)
	payload, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read Kimi fixture %s: %v", path, err)
	}
	return string(payload)
}

// seedKimiSession seeds a fake Kimi home with a session index entry and a
// wire log for the session, exercising the transcript side channel.
func seedKimiSession(t *testing.T, homeDir, sessionID string, wireRows []string) {
	t.Helper()
	kimiHome := filepath.Join(homeDir, ".kimi-code")
	sessionDir := filepath.Join(kimiHome, "sessions", "wd_probe_000000000000", sessionID)
	if err := os.MkdirAll(filepath.Join(sessionDir, "agents", "main"), 0o755); err != nil {
		t.Fatalf("mkdir wire log dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(sessionDir, "agents", "main", "wire.jsonl"), []byte(strings.Join(wireRows, "\n")+"\n"), 0o600); err != nil {
		t.Fatalf("write wire log: %v", err)
	}
	writeKimiSessionIndex(t, homeDir, sessionID, sessionDir)
}

// seedKimiSessionFromFixture seeds the fake Kimi home by copying a sanitized
// wire log fixture captured from a real Kimi Code session.
func seedKimiSessionFromFixture(t *testing.T, homeDir, sessionID, fixtureName string) {
	t.Helper()
	wireBytes, err := os.ReadFile(filepath.Join("testdata", "kimi_hooks", "v0.27.0", fixtureName))
	if err != nil {
		t.Fatalf("read wire fixture %s: %v", fixtureName, err)
	}
	seedKimiSession(t, homeDir, sessionID, strings.Split(strings.TrimRight(string(wireBytes), "\n"), "\n"))
}

// writeKimiSessionIndex writes the session index entry (and guarantees the
// sessions root exists for containment checks).
func writeKimiSessionIndex(t *testing.T, homeDir, sessionID, sessionDir string) {
	t.Helper()
	kimiHome := filepath.Join(homeDir, ".kimi-code")
	if err := os.MkdirAll(filepath.Join(kimiHome, "sessions"), 0o755); err != nil {
		t.Fatalf("mkdir sessions root: %v", err)
	}
	index := `{"sessionId":"` + sessionID + `","sessionDir":"` + sessionDir + `","workDir":"/workspace/kimi-contract-probe"}` + "\n"
	if err := os.WriteFile(filepath.Join(kimiHome, "session_index.jsonl"), []byte(index), 0o600); err != nil {
		t.Fatalf("write session index: %v", err)
	}
}
