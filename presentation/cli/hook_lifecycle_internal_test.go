package cli

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestBoundedLifecycleDoesNotDrainBacklog(t *testing.T) {
	t.Setenv(hookStateDirEnvKey, t.TempDir())
	hookSpoolDrainEntryProbe = func() { t.Error("bounded hook entered backlog drain") }
	t.Cleanup(func() { hookSpoolDrainEntryProbe = nil })
	for _, spec := range []hookInvocationSpec{{Command: "passive", Client: "claude", Action: "stop_failure"}, {Command: "passive", Client: "codex", Action: "session_end"}} {
		c := &RootCLI{}
		if err := c.runHookDurably(context.Background(), "bounded", spec, strings.NewReader(`{"session_id":"s"}`), func(io.Reader) error { return nil }); err != nil {
			t.Fatal(err)
		}
	}
}

func TestPassiveSpoolDropsSensitiveFields(t *testing.T) {
	state := t.TempDir()
	t.Setenv(hookStateDirEnvKey, state)
	t.Setenv("TRACEARY_HOOK_INPUT", `{"session_id":"s","event_id":"native","error_details":"ENV_SECRET","last_assistant_message":"ENV_SECRET","unknown":"ENV_SECRET"}`)
	c := &RootCLI{} // unavailable dependencies deliberately retain a retry record
	if err := c.runPassiveHookDurably(context.Background(), strings.NewReader(`{"session_id":"s","event_id":"native","error_details":"RAW_SECRET","last_assistant_message":"RAW_SECRET","reason":"RAW_SECRET"}`), "claude", "stop_failure", ""); err != nil {
		t.Fatal(err)
	}
	files, err := filepath.Glob(filepath.Join(state, "spool", "*.json"))
	if err != nil {
		t.Fatal(err)
	}
	if len(files) == 0 {
		t.Fatal("missing durable retry")
	}
	for _, file := range files {
		body, err := os.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(body), "RAW_SECRET") || strings.Contains(string(body), "ENV_SECRET") {
			t.Fatal("raw error persisted")
		}
	}
}
