package cli

import (
	"context"
	"database/sql"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/xerrors"

	apptypes "github.com/duck8823/traceary/application/types"
	"github.com/duck8823/traceary/application/usecase"
	"github.com/duck8823/traceary/domain/types"
	sqliteinfra "github.com/duck8823/traceary/infrastructure/sqlite"
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
			if tc.replayWrapper == "" {
				t.Setenv(runtimeModeEnvKey, "")
			}
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

// Store initialization can be deferred independently of a real scratch SQLite adapter.
type passiveDeferredRealStore struct {
	usecase.StoreManagementUsecase
	pending bool
}

func (s *passiveDeferredRealStore) Initialize(ctx context.Context) error {
	if s.pending {
		return &apptypes.StoreMaintenancePendingError{StorePath: "store.db"}
	}
	if err := s.StoreManagementUsecase.Initialize(ctx); err != nil {
		return xerrors.Errorf("failed to initialize scratch passive store: %w", err)
	}
	return nil
}

func TestPassiveSpoolPinsAcquiredStoreRouting(t *testing.T) {
	for _, replayEnv := range []string{"default", "other store"} {
		t.Run(replayEnv, func(t *testing.T) {
			dir := t.TempDir()
			a := filepath.Join(dir, "a.db")
			b := filepath.Join(dir, "b.db")
			home := t.TempDir()
			SetUserHomeDirFunc(func() (string, error) { return home, nil })
			t.Cleanup(ResetUserHomeDirFunc)
			state := t.TempDir()
			t.Setenv(hookStateDirEnvKey, state)
			t.Setenv(dbPathEnvKey, a)
			db := sqliteinfra.NewDatabase(a, os.DirFS(filepath.Join("..", "..", "schema", "sqlite", "migrations")))
			realStore := usecase.NewStoreManagementUsecase(sqliteinfra.NewStoreManagementDatasource(db))
			if err := realStore.Initialize(context.Background()); err != nil {
				t.Fatal(err)
			}
			sessionDS := sqliteinfra.NewSessionDatasource(db)
			sessions := usecase.NewSessionUsecase(nil, sessionDS, sessionDS, nil)
			if _, err := sessions.Start(context.Background(), "hook", "codex", "store-session", "/tmp", ""); err != nil {
				t.Fatal(err)
			}
			events := sqliteinfra.NewEventDatasource(db)
			store := &passiveDeferredRealStore{StoreManagementUsecase: realStore, pending: true}
			root := NewRootCLI(WithStoreManagement(store), WithEvent(usecase.NewEventUsecase(events, events)), WithDatabasePathSetter(db.SetPath))
			if err := root.runPassiveHookDurably(context.Background(), strings.NewReader(`{"session_id":"store-session","cwd":"/tmp","event_id":"delivery-a"}`), "codex", "interrupt", ""); err != nil {
				t.Fatal(err)
			}
			files, err := filepath.Glob(filepath.Join(state, "spool", "*.json"))
			if err != nil || len(files) != 1 {
				t.Fatalf("spool=%v err=%v", files, err)
			}
			raw, err := os.ReadFile(files[0])
			if err != nil {
				t.Fatal(err)
			}
			var record hookSpoolRecord
			if err := json.Unmarshal(raw, &record); err != nil {
				t.Fatal(err)
			}
			if record.DBPath != a {
				t.Errorf("spooled db=%q want=%q", record.DBPath, a)
			}
			if replayEnv == "default" {
				t.Setenv(dbPathEnvKey, "")
			} else {
				t.Setenv(dbPathEnvKey, b)
			}
			store.pending = false
			if err := root.replayHookSpoolRecord(context.Background(), record); err != nil {
				t.Fatal(err)
			}
			sqlDB, err := sql.Open("sqlite", a)
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = sqlDB.Close() }()
			var count int
			if err := sqlDB.QueryRow(`SELECT COUNT(*) FROM events WHERE kind='note' AND session_id='store-session'`).Scan(&count); err != nil {
				t.Fatal(err)
			}
			if count != 1 {
				t.Fatalf("notes in acquired store=%d want=1", count)
			}
			for _, wrong := range []string{b, filepath.Join(home, ".config", "traceary", "traceary.db")} {
				if _, err := os.Stat(wrong); !os.IsNotExist(err) {
					t.Fatalf("replay touched other store %q", wrong)
				}
			}
		})
	}
}

func TestPassiveAcquiredRelativeFlagPrecedesEnvironment(t *testing.T) {
	state := t.TempDir()
	t.Setenv(hookStateDirEnvKey, state)
	t.Setenv(dbPathEnvKey, filepath.Join(t.TempDir(), "env.db"))
	relative := filepath.Join("relative-store", "flag.db")
	want, err := filepath.Abs(relative)
	if err != nil {
		t.Fatal(err)
	}
	root := NewRootCLI(WithEvent(&passiveAttributionEventStub{}), WithStoreManagement(&maintenancePendingStoreStub{err: &apptypes.StoreMaintenancePendingError{StorePath: "store.db"}}))
	if err := root.runPassiveHookDurably(context.Background(), strings.NewReader(`{"session_id":"relative-session","cwd":"/tmp"}`), "codex", "interrupt", relative); err != nil {
		t.Fatal(err)
	}
	files, err := filepath.Glob(filepath.Join(state, "spool", "*.json"))
	if err != nil || len(files) != 1 {
		t.Fatalf("spool=%v err=%v", files, err)
	}
	raw, err := os.ReadFile(files[0])
	if err != nil {
		t.Fatal(err)
	}
	var record hookSpoolRecord
	if err := json.Unmarshal(raw, &record); err != nil {
		t.Fatal(err)
	}
	if record.DBPath != want {
		t.Fatalf("db=%q want absolute flag=%q", record.DBPath, want)
	}
}
