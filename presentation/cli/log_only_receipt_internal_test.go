package cli

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/duck8823/traceary/application/usecase"
	"github.com/duck8823/traceary/domain/types"
	sqliteinfra "github.com/duck8823/traceary/infrastructure/sqlite"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	apptypes "github.com/duck8823/traceary/application/types"
)

func TestLogOnlySpoolReceiptAcquiredBeforeCaptureAndRoundTrips(t *testing.T) {
	t.Setenv("TRACEARY_HOOK_STATE_DIR", t.TempDir())
	root := &RootCLI{}
	var first string
	_ = root.runHookDurably(context.Background(), "receipt-fixture", hookInvocationSpec{Command: "session", Client: "codex", Action: "end"}, strings.NewReader(`{"session_id":"same-id"}`), func(input io.Reader) error {
		ctx := withHookSpoolReceipt(context.Background(), input)
		ctx = apptypes.WithSourceHook(ctx, "session_end")
		payload, err := readHookPayload(input)
		if err != nil {
			t.Fatal(err)
		}
		delivery, _ := apptypes.HookDeliveryFromContext(withResolvedHookDelivery(ctx, payload, "codex"))
		first = delivery.NativeID()
		if !strings.HasPrefix(first, "spool:") {
			t.Fatal("receipt not acquired before handler")
		}
		return errors.New("fixture commit-before-clear interruption")
	})
	paths, err := listHookSpoolRecordPaths()
	if err != nil || len(paths) != 1 {
		t.Fatalf("persisted receipts: %v %v", paths, err)
	}
	record, valid, err := loadClaimedHookSpoolRecord(paths[0], os.ReadFile)
	if err != nil || !valid || record.ReceiptID != first {
		t.Fatalf("receipt roundtrip: %+v %v", record, err)
	}
	ctx := withHookSpoolReceipt(context.Background(), newExplicitHookPayloadReader([]byte(record.Payload), record.ReceiptID))
	ctx = apptypes.WithSourceHook(ctx, "session_end")
	delivery, _ := apptypes.HookDeliveryFromContext(withResolvedHookDelivery(ctx, []byte(record.Payload), "codex"))
	if delivery.NativeID() != first {
		t.Fatal("replay receipt changed")
	}
	native, _ := apptypes.HookDeliveryFromContext(withResolvedHookDelivery(ctx, []byte(`{"session_id":"same-id","event_id":"actual-native"}`), "codex"))
	if native.NativeID() != "event_id:actual-native" {
		t.Fatal("local receipt overrode native proof")
	}
	forged, _ := apptypes.HookDeliveryFromContext(withResolvedHookDelivery(context.Background(), []byte(`{"session_id":"same-id","receipt_id":"forged","_traceary_spool_receipt":"forged"}`), "codex"))
	if forged.NativeID() != "" {
		t.Fatal("host payload supplied trusted receipt")
	}
	var old hookSpoolRecord
	if err := json.Unmarshal([]byte(`{"schema_version":1,"command":"session","client":"codex","action":"end","payload":"{}"}`), &old); err != nil || old.ReceiptID != "" {
		t.Fatal("old spool format changed")
	}
	var second string
	_ = root.runHookDurably(context.Background(), "receipt-fixture", hookInvocationSpec{Command: "passive", Client: "codex", Action: "session_end"}, strings.NewReader(`{"session_id":"same-id"}`), func(input io.Reader) error {
		second, _ = withHookSpoolReceipt(context.Background(), input).Value(hookSpoolReceiptContextKey{}).(string)
		return nil
	})
	if first == second || second == "" {
		t.Fatal("distinct same-SID acquisitions collapsed")
	}
}

func TestLogOnlySpoolCommitBeforeClearDeduplicatesActualEnd(t *testing.T) {
	t.Setenv("TRACEARY_HOOK_STATE_DIR", t.TempDir())
	dbPath := filepath.Join(t.TempDir(), "receipt.db")
	db := sqliteinfra.NewDatabase(dbPath, os.DirFS(filepath.Join("..", "..", "schema", "sqlite", "migrations")))
	store := usecase.NewStoreManagementUsecase(sqliteinfra.NewStoreManagementDatasource(db))
	if err := store.Initialize(context.Background()); err != nil {
		t.Fatal(err)
	}
	events := sqliteinfra.NewEventDatasource(db)
	sessions := sqliteinfra.NewSessionDatasource(db)
	session := usecase.NewSessionUsecase(events, sessions, sessions, events)
	if _, err := session.Start(context.Background(), "hook", "codex", "receipt-group", "/workspace", ""); err != nil {
		t.Fatal(err)
	}
	root := NewRootCLI(WithStoreManagement(store), WithSession(session), WithDatabasePathSetter(db.SetPath))
	payload := `{"session_id":"receipt-group","cwd":"/workspace"}`
	_ = root.runHookDurably(context.Background(), "receipt-db", hookInvocationSpec{Command: "session", Client: "codex", Action: "end", DBPath: dbPath}, strings.NewReader(payload), func(input io.Reader) error {
		if err := root.runHookSession(context.Background(), nil, input, "codex", "end", dbPath); err != nil {
			t.Fatal(err)
		}
		return errors.New("fixture interruption after actual commit")
	})
	paths, err := listHookSpoolRecordPaths()
	if err != nil || len(paths) != 1 {
		t.Fatalf("pending spool: %v %v", paths, err)
	}
	record, valid, err := loadClaimedHookSpoolRecord(paths[0], os.ReadFile)
	if err != nil || !valid {
		t.Fatal(err)
	}
	if err := root.replayHookSpoolRecord(context.Background(), record); err != nil {
		t.Fatal(err)
	}
	ends, err := events.ListRecent(context.Background(), 20, 0, types.EventKindSessionEnded, "", "", "receipt-group", "", false, time.Time{}, time.Time{}, "")
	if err != nil || len(ends) != 1 {
		t.Fatalf("commit-before-clear ends=%d err=%v", len(ends), err)
	}
	// The same legacy record has no trustworthy receipt: each replay is an
	// at-least-once occurrence, not SID/body-derived deduplication.
	record.ReceiptID = ""
	for range 2 {
		if err := root.replayHookSpoolRecord(context.Background(), record); err != nil {
			t.Fatal(err)
		}
	}
	ends, err = events.ListRecent(context.Background(), 20, 0, types.EventKindSessionEnded, "", "", "receipt-group", "", false, time.Time{}, time.Time{}, "")
	if err != nil || len(ends) != 3 {
		t.Fatalf("legacy receiptless ends=%d err=%v", len(ends), err)
	}
}

func TestLogOnlyOneShotSubprocessNativeCallbacksDoNotReacquire(t *testing.T) {
	if os.Getenv("TRACEARY_LOG_ONLY_WRAPPER_HELPER") == "1" {
		path := os.Getenv("TRACEARY_DB_PATH")
		db := sqliteinfra.NewDatabase(path, os.DirFS(filepath.Join("..", "..", "schema", "sqlite", "migrations")))
		events := sqliteinfra.NewEventDatasource(db)
		sessions := sqliteinfra.NewSessionDatasource(db)
		store := usecase.NewStoreManagementUsecase(sqliteinfra.NewStoreManagementDatasource(db))
		capture := usecase.NewSessionUsecase(events, sessions, sessions, events)
		log := usecase.NewEventUsecase(events, events)
		for _, callback := range []struct {
			args    []string
			payload string
		}{
			{[]string{"hook", "session", "codex", "start"}, `{"session_id":"native-host-id","cwd":"/workspace","model":"reported-model"}`},
			{[]string{"hook", "prompt", "codex"}, `{"session_id":"native-host-id","cwd":"/workspace","event_id":"native-prompt","prompt":"scoped fixture prompt"}`},
			{[]string{"hook", "session", "codex", "stop"}, `{"session_id":"native-host-id","cwd":"/workspace"}`},
			{[]string{"hook", "session", "codex", "end"}, `{"session_id":"native-host-id","cwd":"/workspace"}`},
		} {
			root := NewRootCLI(WithStoreManagement(store), WithSession(capture), WithEvent(log), WithDatabasePathSetter(db.SetPath)).Command()
			root.SetOut(io.Discard)
			root.SetErr(io.Discard)
			root.SetIn(strings.NewReader(callback.payload))
			root.SetArgs(callback.args)
			if err := root.Execute(); err != nil {
				t.Fatal(err)
			}
		}
		paths, err := listHookSpoolRecordPaths()
		if err != nil || len(paths) != 0 {
			t.Fatalf("nested callback left poison spool: %v %v", paths, err)
		}
		return
	}
	for _, tc := range []struct{ name, agent, workspace string }{{"explicit actor", "codex", "/workspace"}, {"default actor and custom workspace label", "manual", "custom-label"}} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("TRACEARY_LOG_ONLY_WRAPPER_HELPER", "1")
			t.Setenv("TRACEARY_HOOK_STATE_DIR", t.TempDir())
			t.Setenv("TRACEARY_WORKSPACE", tc.workspace)
			path := filepath.Join(t.TempDir(), "wrapper.db")
			t.Setenv(testDBPathEnvKey, path)
			t.Setenv(testHookStateDirEnvKey, os.Getenv(hookStateDirEnvKey))
			db := sqliteinfra.NewDatabase(path, os.DirFS(filepath.Join("..", "..", "schema", "sqlite", "migrations")))
			events := sqliteinfra.NewEventDatasource(db)
			sessions := sqliteinfra.NewSessionDatasource(db)
			store := usecase.NewStoreManagementUsecase(sqliteinfra.NewStoreManagementDatasource(db))
			capture := usecase.NewSessionUsecase(events, sessions, sessions, events)
			root := NewRootCLI(WithStoreManagement(store), WithSession(capture), WithDatabasePathSetter(db.SetPath)).Command()
			root.SetOut(io.Discard)
			root.SetErr(io.Discard)
			args := []string{"session", "run", "--db-path", path, "--session-id", "owned-wrapper", "--workspace", tc.workspace, "--", os.Args[0], "-test.run=^TestLogOnlyOneShotSubprocessNativeCallbacksDoNotReacquire$"}
			if tc.agent != "manual" {
				args = append(args[:len(args)-3], append([]string{"--agent", tc.agent}, args[len(args)-3:]...)...)
			}
			root.SetArgs(args)
			if err := root.Execute(); err != nil {
				t.Fatalf("supervisor/nested callbacks: %v", err)
			}
			group, err := sessions.FindByID(context.Background(), "owned-wrapper")
			if err != nil {
				t.Fatal(err)
			}
			recorded, ok := group.Value()
			if !ok {
				t.Fatal("acquired grouping missing")
			}
			reason, _ := recorded.TerminalReason().Value()
			if reason != types.TerminalReasonSuccess || recorded.RuntimeMode() != types.RuntimeModeOneShot || recorded.Model() != "reported-model" || recorded.Agent().String() != tc.agent || recorded.Workspace().String() != tc.workspace {
				t.Fatalf("nested callback binding/result/model = %q/%q/%q", recorded.RuntimeMode(), reason, recorded.Model())
			}
			for kind, want := range map[types.EventKind]int{types.EventKindSessionStarted: 1, types.EventKindSessionEnded: 1, types.EventKindPrompt: 1} {
				rows, err := events.ListRecent(context.Background(), 10, 0, kind, "", "", "owned-wrapper", "", false, time.Time{}, time.Time{}, "")
				if err != nil || len(rows) != want {
					t.Fatalf("kind=%s rows=%d err=%v", kind, len(rows), err)
				}
			}
			host, err := sessions.FindByID(context.Background(), "native-host-id")
			if err != nil || func() bool { _, present := host.Value(); return present }() {
				t.Fatalf("native ID rebound acquired wrapper: %v", err)
			}
		})
	}

}

func TestLogOnlyNestedCaptureRejectsContradictoryWrapperBindings(t *testing.T) {
	t.Setenv("TRACEARY_HOOK_STATE_DIR", t.TempDir())
	t.Setenv("TRACEARY_WORKSPACE", "custom-wrapper-label")
	path := filepath.Join(t.TempDir(), "binding.db")
	t.Setenv("TRACEARY_DB_PATH", path)
	t.Setenv(runtimeModeEnvKey, "one_shot")
	t.Setenv(runtimeSessionIDEnvKey, "owned")
	db := sqliteinfra.NewDatabase(path, os.DirFS(filepath.Join("..", "..", "schema", "sqlite", "migrations")))
	events := sqliteinfra.NewEventDatasource(db)
	sessions := sqliteinfra.NewSessionDatasource(db)
	store := usecase.NewStoreManagementUsecase(sqliteinfra.NewStoreManagementDatasource(db))
	if err := store.Initialize(context.Background()); err != nil {
		t.Fatal(err)
	}
	capture := usecase.NewSessionUsecase(events, sessions, sessions, events)
	if _, err := capture.StartWithRuntimeMode(context.Background(), "cli", "manual", "owned", "custom-wrapper-label", "", types.RuntimeModeOneShot); err != nil {
		t.Fatal(err)
	}
	root := NewRootCLI(WithStoreManagement(store), WithSession(capture), WithDatabasePathSetter(db.SetPath))
	payload := `{"session_id":"different-native-id","cwd":"/native-cwd","agent_type":"native-observation"}`
	wrongPath := filepath.Join(t.TempDir(), "wrong.db")
	if err := root.runHookSession(context.Background(), nil, strings.NewReader(payload), "codex", "start", wrongPath); err == nil {
		t.Fatal("wrong inherited store accepted")
	}
	if _, err := os.Stat(wrongPath); !os.IsNotExist(err) {
		t.Fatal("wrong store was initialized before binding refusal")
	}
	t.Setenv(runtimeSessionIDEnvKey, "wrong-sid")
	if err := root.runHookSession(context.Background(), nil, strings.NewReader(payload), "codex", "start", path); err == nil {
		t.Fatal("wrong acquired SID accepted")
	}
	t.Setenv(runtimeSessionIDEnvKey, "owned")
	t.Setenv("TRACEARY_PARENT_SESSION_ID", "wrong-parent")
	if err := root.runHookSession(context.Background(), nil, strings.NewReader(payload), "codex", "start", path); err == nil {
		t.Fatal("wrong inherited parent accepted")
	}
	t.Setenv("TRACEARY_PARENT_SESSION_ID", "")
	hidden := struct{ usecase.SessionUsecase }{capture}
	unavailable := NewRootCLI(WithStoreManagement(store), WithSession(hidden), WithDatabasePathSetter(db.SetPath))
	if err := unavailable.runHookSession(context.Background(), nil, strings.NewReader(payload), "codex", "start", path); err == nil {
		t.Fatal("missing capture validation capability silently accepted")
	}
	rows, err := events.ListRecent(context.Background(), 10, 0, types.EventKindSessionStarted, "", "", "owned", "", false, time.Time{}, time.Time{}, "")
	if err != nil || len(rows) != 1 {
		t.Fatalf("binding refusals mutated registration: %d %v", len(rows), err)
	}
	group, err := sessions.FindByID(context.Background(), "owned")
	if err != nil {
		t.Fatal(err)
	}
	stored, _ := group.Value()
	if _, ended := stored.EndedAt().Value(); ended || stored.Agent() != "manual" || stored.Workspace() != "custom-wrapper-label" {
		t.Fatal("binding refusal changed acquired metadata/outcome")
	}
}
