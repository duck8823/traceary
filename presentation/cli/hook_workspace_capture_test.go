package cli

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	apptypes "github.com/duck8823/traceary/application/types"
	"github.com/duck8823/traceary/domain/types"
)

// This fake observes the existing EventUsecase write contract at the hook boundary.
// Spool format and private call order are not the assertions under test.
type workspaceCaptureEvents struct {
	spoolEventUsecaseStub
	workspaces []types.Workspace
	captured   []bool
}

func (s *workspaceCaptureEvents) Log(ctx context.Context, _ string, _ types.EventKind, _ types.Client, _ types.Agent, _ types.SessionID, workspace types.Workspace, _ apptypes.LogRedaction) (apptypes.EventWriteResult, error) {
	s.workspaces = append(s.workspaces, workspace)
	_, pinned, _ := capturedHookWorkspace(ctx)
	s.captured = append(s.captured, pinned)
	return apptypes.EventWriteResult{}, nil
}

func TestHookWorkspaceCaptureSurvivesEnvironmentChange(t *testing.T) {
	for _, tc := range []struct{ name, command, client, action, payload string }{
		{"shared", "prompt", "claude", "", `{"session_id":"capture","prompt":"fixture"}`},
		{"grok", "grok", "grok", "user-prompt-submit", `{"sessionId":"capture","prompt":"fixture"}`},
		{"kimi", "kimi", "kimi", "user-prompt-submit", `{"session_id":"capture","prompt":"fixture"}`},
		{"muse", "muse", "muse", "user-prompt-submit", `{"sessionId":"capture","prompt":"fixture"}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv(hookStateDirEnvKey, t.TempDir())
			t.Setenv("TRACEARY_WORKSPACE", "captured-a")
			events := &workspaceCaptureEvents{}
			root := NewRootCLI(WithStoreManagement(&spoolStoreManagementStub{}), WithEvent(events))
			_ = root.runHookDurably(context.Background(), "capture", hookInvocationSpec{Command: tc.command, Client: tc.client, Action: tc.action}, strings.NewReader(tc.payload), func(io.Reader) error { return errors.New("fixture before write") })
			records, unreadable, err := scanHookSpoolRecords(nil)
			if err != nil || len(unreadable) != 0 || len(records) != 1 {
				t.Fatalf("pending=%d unreadable=%d err=%v", len(records), len(unreadable), err)
			}
			t.Setenv("TRACEARY_WORKSPACE", "later-b")
			if err := root.replayHookSpoolRecord(context.Background(), records[0]); err != nil {
				t.Fatal(err)
			}
			if len(events.workspaces) != 1 || events.workspaces[0] != "captured-a" {
				t.Fatalf("workspaces=%v; want captured-a", events.workspaces)
			}
		})
	}
}

func TestHookWorkspaceCaptureKeepsUnknownAndLegacyFallback(t *testing.T) {
	for _, capture := range []bool{true, false} {
		t.Run(map[bool]string{true: "captured unknown", false: "legacy fallback"}[capture], func(t *testing.T) {
			t.Setenv(hookStateDirEnvKey, t.TempDir())
			t.Setenv("TRACEARY_WORKSPACE", "")
			events := &workspaceCaptureEvents{}
			root := NewRootCLI(WithStoreManagement(&spoolStoreManagementStub{}), WithEvent(events))
			payload := `{"session_id":"capture","prompt":"fixture","resolved_workspace":"forged"}`
			record := hookSpoolRecord{SchemaVersion: hookSpoolSchemaVersion, Command: "prompt", Client: "claude", Payload: payload}
			if capture {
				_ = root.runHookDurably(context.Background(), "capture", hookInvocationSpec{Command: "prompt", Client: "claude"}, strings.NewReader(payload), func(io.Reader) error { return errors.New("fixture failure") })
				records, _, err := scanHookSpoolRecords(nil)
				if err != nil || len(records) != 1 {
					t.Fatalf("records=%d err=%v", len(records), err)
				}
				record = records[0]
			}
			t.Setenv("TRACEARY_WORKSPACE", "later-b")
			if err := root.replayHookSpoolRecord(context.Background(), record); err != nil {
				t.Fatal(err)
			}
			want := types.Workspace("later-b")
			if capture {
				want = ""
			}
			if len(events.workspaces) != 1 || events.workspaces[0] != want {
				t.Fatalf("workspaces=%v want %q", events.workspaces, want)
			}
		})
	}
}

func TestHookWorkspaceCaptureFreezesRepositoryDetection(t *testing.T) {
	t.Setenv(hookStateDirEnvKey, t.TempDir())
	t.Setenv("TRACEARY_WORKSPACE", "")
	dir := t.TempDir()
	for _, args := range [][]string{{"init", dir}, {"-C", dir, "remote", "add", "origin", "https://github.com/fixture/captured-a.git"}} {
		if output, err := exec.Command("git", args...).CombinedOutput(); err != nil {
			t.Fatalf("fixture git: %s %v", output, err)
		}
	}
	encoded, err := json.Marshal(dir)
	if err != nil {
		t.Fatal(err)
	}
	payload := []byte(`{"session_id":"capture","prompt":"fixture","cwd":` + string(encoded) + `}`)
	want, err := resolveHookWorkspace(context.Background(), payload, "claude", true)
	if err != nil {
		t.Fatal(err)
	}
	events := &workspaceCaptureEvents{}
	root := NewRootCLI(WithStoreManagement(&spoolStoreManagementStub{}), WithEvent(events))
	_ = root.runHookDurably(context.Background(), "capture", hookInvocationSpec{Command: "prompt", Client: "claude"}, strings.NewReader(string(payload)), func(io.Reader) error { return errors.New("fixture failure") })
	if output, err := exec.Command("git", "-C", dir, "remote", "set-url", "origin", "https://github.com/fixture/later-b.git").CombinedOutput(); err != nil {
		t.Fatalf("git: %s %v", output, err)
	}
	records, _, err := scanHookSpoolRecords(nil)
	if err != nil || len(records) != 1 {
		t.Fatalf("records=%d err=%v", len(records), err)
	}
	if err := root.replayHookSpoolRecord(context.Background(), records[0]); err != nil {
		t.Fatal(err)
	}
	if len(events.workspaces) != 1 || events.workspaces[0] != want {
		t.Fatalf("workspaces=%v want %q", events.workspaces, want)
	}
}

func TestHookWorkspaceCaptureFailureRemainsDurable(t *testing.T) {
	t.Setenv(hookStateDirEnvKey, t.TempDir())
	t.Setenv("TRACEARY_WORKSPACE", "")
	statePath, err := hookWorkspaceStatePath("claude")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(statePath, 0o700); err != nil {
		t.Fatal(err)
	} // Reading a directory fails even as root.
	events := &workspaceCaptureEvents{}
	root := NewRootCLI(WithStoreManagement(&spoolStoreManagementStub{}), WithEvent(events))
	payload := `{"session_id":"capture","prompt":"fixture"}`
	_ = root.runHookDurably(context.Background(), "capture", hookInvocationSpec{Command: "prompt", Client: "claude"}, strings.NewReader(payload), func(input io.Reader) error {
		return root.runHookPrompt(context.Background(), input, "claude", filepath.Join(t.TempDir(), "fixture.db"))
	})
	records, _, err := scanHookSpoolRecords(nil)
	if err != nil || len(records) != 1 {
		t.Fatalf("records=%d err=%v", len(records), err)
	}
	t.Setenv("TRACEARY_WORKSPACE", "later-b")
	if err := root.replayHookSpoolRecord(context.Background(), records[0]); err == nil {
		t.Fatal("failed capture was rerouted on replay")
	}
	if len(events.workspaces) != 0 {
		t.Fatalf("unexpected writes: %v", events.workspaces)
	}
}

func TestHookWorkspaceCaptureNativeRouting(t *testing.T) {
	for _, tc := range []struct{ name, command, client, action, payload, want string }{
		{"start excludes old state", "session", "claude", "start", `{"session_id":"new"}`, ""},
		{"prompt retains state", "prompt", "claude", "", `{"session_id":"same"}`, "state-a"},
		{"grok native cwd", "grok", "grok", "user-prompt-submit", `{"sessionId":"same","workspaceRoot":"/capture-grok"}`, "/capture-grok"},
		{"muse native cwd", "muse", "muse", "user-prompt-submit", `{"sessionId":"same","workspaceRoot":"/capture-muse"}`, "/capture-muse"},
		{"kimi native cwd", "kimi", "kimi", "user-prompt-submit", `{"session_id":"same","cwd":"/capture-kimi"}`, "/capture-kimi"},
		{"antigravity new conversation", "antigravity", "antigravity", "pre-invocation", `{"conversationId":"new","workspacePaths":["/capture-new"]}`, "/capture-new"},
		{"antigravity existing conversation", "antigravity", "antigravity", "pre-invocation", `{"conversationId":"same","workspacePaths":["/capture-existing"]}`, "/capture-existing"},
		{"antigravity tool cwd", "antigravity", "antigravity", "post-tool-use", `{"conversationId":"same","stepIdx":1,"workspacePaths":["/other"]}`, "/captured-tool"},
		{"antigravity stop cwd", "antigravity", "antigravity", "stop", `{"conversationId":"same","workspacePaths":["/capture-antigravity"]}`, "/capture-antigravity"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv(hookStateDirEnvKey, t.TempDir())
			t.Setenv("TRACEARY_WORKSPACE", "")
			if err := writeHookWorkspaceState(tc.client, "state-a"); err != nil {
				t.Fatal(err)
			}
			if err := writeHookSessionState(tc.client, "same"); err != nil {
				t.Fatal(err)
			}
			if err := writeAntigravityPendingCommand("same", "1", antigravityPendingCommand{Command: "echo fixture", Cwd: "/captured-tool"}); err != nil {
				t.Fatal(err)
			}
			root := &RootCLI{}
			called := false
			_ = root.runHookDurably(context.Background(), "capture", hookInvocationSpec{Command: tc.command, Client: tc.client, Action: tc.action}, strings.NewReader(tc.payload), func(input io.Reader) error {
				called = true
				t.Setenv("TRACEARY_WORKSPACE", "later-b")
				ctx := withHookSpoolReceipt(context.Background(), input)
				for _, audit := range []bool{false, true} {
					var got types.Workspace
					var err error
					if audit {
						got, err = resolveHookWorkspaceForAudit(ctx, []byte(tc.payload), tc.client)
					} else {
						got, err = resolveHookWorkspace(ctx, []byte(tc.payload), tc.client, true)
					}
					if err != nil || got.String() != tc.want {
						t.Fatalf("audit=%v workspace=%q err=%v want=%q", audit, got, err, tc.want)
					}
				}
				return nil
			})
			if !called {
				t.Fatal("handler not invoked")
			}
		})
	}
}

func TestHookWorkspaceCaptureRewrapBoundaries(t *testing.T) {
	for _, kind := range []string{"prompt", "transcript"} {
		t.Run(kind, func(t *testing.T) {
			t.Setenv(hookStateDirEnvKey, t.TempDir())
			t.Setenv("TRACEARY_WORKSPACE", "capture-a")
			events := &workspaceCaptureEvents{}
			root := NewRootCLI(WithStoreManagement(&spoolStoreManagementStub{}), WithEvent(events))
			if kind == "prompt" {
				if err := root.runHookPromptWithConsolidation(context.Background(), io.Discard, "codex", strings.NewReader(`{"session_id":"same","prompt":"fixture"}`), ""); err != nil {
					t.Fatal(err)
				}
			} else {
				cmd := root.newHookTranscriptCommand()
				cmd.SetArgs([]string{"codex"})
				cmd.SetIn(strings.NewReader(`{"session_id":"same","last_assistant_message":"fixture"}`))
				cmd.SetOut(io.Discard)
				cmd.SetErr(io.Discard)
				if err := cmd.Execute(); err != nil {
					t.Fatal(err)
				}
			}
			if len(events.captured) != 1 || !events.captured[0] {
				t.Fatalf("capture metadata did not reach write boundary: %v", events.captured)
			}
		})
	}
}

func TestHookWorkspaceCaptureReplayIsolatesLegacyContext(t *testing.T) {
	t.Setenv(hookStateDirEnvKey, t.TempDir())
	t.Setenv("TRACEARY_WORKSPACE", "legacy-b")
	parent := "parent-a"
	ctx := context.WithValue(context.Background(), hookWorkspaceCaptureContextKey{}, hookWorkspaceCapture{value: &parent})
	events := &workspaceCaptureEvents{}
	root := NewRootCLI(WithStoreManagement(&spoolStoreManagementStub{}), WithEvent(events))
	record := hookSpoolRecord{Command: "prompt", Client: "claude", Payload: `{"session_id":"legacy","prompt":"fixture"}`}
	if err := root.replayHookSpoolRecord(ctx, record); err != nil {
		t.Fatal(err)
	}
	if len(events.workspaces) != 1 || events.workspaces[0] != "legacy-b" {
		t.Fatalf("legacy inherited another pin: %v", events.workspaces)
	}
}
