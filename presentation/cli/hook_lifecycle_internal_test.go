package cli

import (
	"context"
	"encoding/json"
	apptypes "github.com/duck8823/traceary/application/types"
	"github.com/duck8823/traceary/application/usecase"
	"github.com/duck8823/traceary/domain/types"
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

// Only Log attribution is observed; other EventUsecase methods are deliberately unavailable.
type passiveAttributionEventStub struct {
	usecase.EventUsecase
	sessions []types.SessionID
}

func (s *passiveAttributionEventStub) Log(_ context.Context, _ string, _ types.EventKind, _ types.Client, _ types.Agent, sessionID types.SessionID, _ types.Workspace, _ apptypes.LogRedaction) (apptypes.EventWriteResult, error) {
	s.sessions = append(s.sessions, sessionID)
	return apptypes.EventWriteResult{}, nil
}

func TestPassiveSpoolBindsAcquiredSessionAcrossWrapperEnvironments(t *testing.T) {
	for _, tc := range []struct{ name, captureWrapper, replayWrapper, native, want string }{
		{"wrapper A replayed outside wrapper", "wrapper-a", "", "native-n", "wrapper-a"},
		{"wrapper A replayed inside wrapper B", "wrapper-a", "wrapper-b", "native-n", "wrapper-a"},
		{"interactive native replayed inside wrapper B", "", "wrapper-b", "native-n", "native-n"},
		{"missing native does not infer wrapper", "wrapper-a", "wrapper-b", "", ""},
		{"blank native does not infer wrapper", "wrapper-a", "wrapper-b", "   ", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			state := t.TempDir()
			t.Setenv(hookStateDirEnvKey, state)
			t.Setenv(runtimeModeEnvKey, "one_shot")
			t.Setenv(runtimeSessionIDEnvKey, tc.captureWrapper)
			events := &passiveAttributionEventStub{}
			store := &maintenancePendingStoreStub{err: &apptypes.StoreMaintenancePendingError{StorePath: "store.db"}}
			root := NewRootCLI(WithEvent(events), WithStoreManagement(store))
			raw, err := json.Marshal(map[string]string{"session_id": tc.native, "cwd": "/tmp", "event_id": "native-delivery"})
			if err != nil {
				t.Fatal(err)
			}
			if err := root.runPassiveHookDurably(context.Background(), strings.NewReader(string(raw)), "codex", "interrupt", ""); err != nil {
				t.Fatal(err)
			}
			files, err := filepath.Glob(filepath.Join(state, "spool", "*.json"))
			if err != nil {
				t.Fatal(err)
			}
			if tc.want == "" {
				if len(files) != 0 || len(events.sessions) != 0 {
					t.Fatal("identity-free input persisted or attached")
				}
				return
			}
			if len(files) != 1 {
				t.Fatalf("pending records=%d", len(files))
			}
			body, err := os.ReadFile(files[0])
			if err != nil {
				t.Fatal(err)
			}
			var record hookSpoolRecord
			if err := json.Unmarshal(body, &record); err != nil {
				t.Fatal(err)
			}
			if got := hookPayloadString([]byte(record.Payload), "session_id", ""); got != tc.want {
				t.Errorf("spooled session=%q want=%q", got, tc.want)
			}
			t.Setenv(runtimeSessionIDEnvKey, tc.replayWrapper)
			store.err = nil
			if err := root.replayHookSpoolRecord(context.Background(), record); err != nil {
				t.Fatal(err)
			}
			if len(events.sessions) != 1 || events.sessions[0].String() != tc.want {
				t.Fatalf("replayed sessions=%v want=%s", events.sessions, tc.want)
			}
		})
	}
}
