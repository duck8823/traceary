package sqlite_test

import (
	"context"
	"database/sql"
	"fmt"
	"path/filepath"
	"sync"
	"testing"
	"time"

	_ "modernc.org/sqlite"

	apptypes "github.com/duck8823/traceary/application/types"
	"github.com/duck8823/traceary/application/usecase"
	"github.com/duck8823/traceary/domain/model"
	"github.com/duck8823/traceary/domain/types"
)

func TestEventDatasource_HookDeliveryIdentity(t *testing.T) {
	t.Parallel()

	t.Run("exact redelivery keeps one event and one attribution", func(t *testing.T) {
		t.Parallel()
		dbPath, eventDS := newHookDeliveryTestStore(t)
		first := hookDeliveryTestEvent(t, "event-1", "session-1", "/repo", "/repo", "same body", "event_id:delivery-1")
		retry := hookDeliveryTestEvent(t, "event-2", "session-1", "/repo", "/repo", "same body", "event_id:delivery-1")

		if err := eventDS.Save(context.Background(), first); err != nil {
			t.Fatalf("Save(first) error = %v", err)
		}
		if err := eventDS.Save(context.Background(), retry); err != nil {
			t.Fatalf("Save(retry) error = %v", err)
		}

		assertSQLiteCount(t, dbPath, "events", 1)
		assertSQLiteCount(t, dbPath, "hook_deliveries", 1)
		assertSQLiteCount(t, dbPath, "hook_delivery_attempts", 2)
		assertSQLiteCountWhere(t, dbPath, "hook_delivery_attempts", "outcome = 'accepted'", 1)
		assertSQLiteCountWhere(t, dbPath, "hook_delivery_attempts", "outcome = 'exact_redelivery'", 1)
		assertSQLiteCountWhere(t, dbPath, "session_workspace_observations", "delivery_record_id IS NOT NULL", 1)

		db := openHookDeliveryTestDB(t, dbPath)
		if _, err := db.Exec(`DELETE FROM events WHERE id = 'event-1'`); err != nil {
			t.Fatalf("delete archived event: %v", err)
		}
		if err := db.Close(); err != nil {
			t.Fatalf("close archive DB: %v", err)
		}
		assertSQLiteCount(t, dbPath, "hook_deliveries", 1)
		assertSQLiteCountWhere(t, dbPath, "session_workspace_observations", "observed_event_id = 'event-1'", 1)
	})

	t.Run("redelivery may add attribution without rewriting the event", func(t *testing.T) {
		t.Parallel()
		dbPath, eventDS := newHookDeliveryTestStore(t)
		first := hookDeliveryTestEvent(t, "event-1", "session-1", "/repo", "/repo", "same body", "event_id:delivery-1")
		retry := hookDeliveryTestEvent(t, "event-2", "session-1", "/repo/sub", "/repo/sub", "same body", "event_id:delivery-1")

		if err := eventDS.Save(context.Background(), first); err != nil {
			t.Fatalf("Save(first) error = %v", err)
		}
		if err := eventDS.Save(context.Background(), retry); err != nil {
			t.Fatalf("Save(retry) error = %v", err)
		}

		assertSQLiteCount(t, dbPath, "events", 1)
		assertSQLiteCount(t, dbPath, "hook_delivery_attempts", 2)
		assertSQLiteCountWhere(t, dbPath, "session_workspace_observations", "delivery_record_id IS NOT NULL", 2)
		db := openHookDeliveryTestDB(t, dbPath)
		defer func() { _ = db.Close() }()
		var workspace string
		if err := db.QueryRow(`SELECT workspace FROM events WHERE id = 'event-1'`).Scan(&workspace); err != nil {
			t.Fatalf("read persisted workspace: %v", err)
		}
		if workspace != "/repo" {
			t.Fatalf("persisted event workspace = %q, want immutable /repo", workspace)
		}
		var supplementalRelationship string
		if err := db.QueryRow(`SELECT observed_relationship FROM session_workspace_observations WHERE observation_kind = 'supplemental'`).Scan(&supplementalRelationship); err != nil {
			t.Fatalf("read supplemental relationship: %v", err)
		}
		if supplementalRelationship != "unknown" {
			t.Fatalf("supplemental relationship = %q, want unknown without canonical session", supplementalRelationship)
		}
		var supplementalRaw string
		if err := db.QueryRow(`SELECT raw_workspace FROM session_workspace_observations WHERE observation_kind = 'supplemental'`).Scan(&supplementalRaw); err != nil {
			t.Fatalf("read supplemental raw workspace: %v", err)
		}
		if supplementalRaw != "/repo/sub" {
			t.Fatalf("supplemental raw workspace = %q, want retained host evidence", supplementalRaw)
		}
	})

	t.Run("reused native ID with changed semantics is explicit conflict", func(t *testing.T) {
		t.Parallel()
		dbPath, eventDS := newHookDeliveryTestStore(t)
		accepted := hookDeliveryTestEvent(t, "event-1", "session-1", "/repo", "/repo", "first body", "event_id:delivery-1")
		conflict := hookDeliveryTestEvent(t, "event-2", "session-1", "/repo", "/repo", "changed body", "event_id:delivery-1")
		conflictRetry := hookDeliveryTestEvent(t, "event-3", "session-1", "/repo", "/repo", "changed body", "event_id:delivery-1")

		for _, event := range []*model.Event{accepted, conflict, conflictRetry} {
			if err := eventDS.Save(context.Background(), event); err != nil {
				t.Fatalf("Save(%s) error = %v", event.EventID(), err)
			}
		}

		assertSQLiteCount(t, dbPath, "events", 2)
		assertSQLiteCount(t, dbPath, "hook_deliveries", 2)
		assertSQLiteCount(t, dbPath, "hook_delivery_attempts", 3)
		assertSQLiteCountWhere(t, dbPath, "hook_deliveries", "identity_status = 'accepted'", 1)
		assertSQLiteCountWhere(t, dbPath, "hook_deliveries", "identity_status = 'conflict'", 1)
		assertSQLiteCountWhere(t, dbPath, "session_workspace_observations", "diagnostic_reason = 'delivery_identity_conflict'", 1)
	})

	t.Run("same native ID is isolated by session", func(t *testing.T) {
		t.Parallel()
		dbPath, eventDS := newHookDeliveryTestStore(t)
		for _, event := range []*model.Event{
			hookDeliveryTestEvent(t, "event-1", "session-1", "/repo", "/repo", "same body", "event_id:delivery-1"),
			hookDeliveryTestEvent(t, "event-2", "session-2", "/repo", "/repo", "same body", "event_id:delivery-1"),
		} {
			if err := eventDS.Save(context.Background(), event); err != nil {
				t.Fatalf("Save(%s) error = %v", event.EventID(), err)
			}
		}
		assertSQLiteCount(t, dbPath, "events", 2)
		assertSQLiteCount(t, dbPath, "hook_deliveries", 2)
		assertSQLiteCount(t, dbPath, "hook_delivery_attempts", 2)
	})

	t.Run("same native ID is isolated by hook kind", func(t *testing.T) {
		t.Parallel()
		dbPath, eventDS := newHookDeliveryTestStore(t)
		first := hookDeliveryTestEvent(t, "event-hook-a", "session-hooks", "/repo", "/repo", "same body", "event_id:shared")
		second := hookDeliveryTestEvent(t, "event-hook-b", "session-hooks", "/repo", "/repo", "same body", "event_id:shared")
		second.SetSourceHook("stop")
		evidence, err := model.NewHookDeliveryEvidence(second, "event_id:shared", "/repo")
		if err != nil {
			t.Fatalf("NewHookDeliveryEvidence(second) error = %v", err)
		}
		second.SetDeliveryEvidence(evidence)

		for _, event := range []*model.Event{first, second} {
			if err := eventDS.Save(context.Background(), event); err != nil {
				t.Fatalf("Save(%s) error = %v", event.EventID(), err)
			}
		}
		assertSQLiteCount(t, dbPath, "events", 2)
		assertSQLiteCount(t, dbPath, "hook_deliveries", 2)
		assertSQLiteCount(t, dbPath, "hook_delivery_attempts", 2)
		assertSQLiteCountWhere(t, dbPath, "hook_deliveries", "identity_status = 'accepted'", 2)
	})

	t.Run("concurrent spool replay converges after retry", func(t *testing.T) {
		t.Parallel()
		dbPath, eventDS := newHookDeliveryTestStore(t)
		const attempts = 8
		events := make([]*model.Event, attempts)
		for i := range events {
			events[i] = hookDeliveryTestEvent(t, "event-replay-"+string(rune('a'+i)), "session-replay", "/repo", "/repo", "same body", "event_id:spooled-delivery")
		}

		errs := make([]error, attempts)
		var wg sync.WaitGroup
		wg.Add(attempts)
		for i, event := range events {
			go func(index int, candidate *model.Event) {
				defer wg.Done()
				errs[index] = eventDS.Save(context.Background(), candidate)
			}(i, event)
		}
		wg.Wait()

		succeeded := 0
		for i, err := range errs {
			if err == nil {
				succeeded++
				continue
			}
			if retryErr := eventDS.Save(context.Background(), events[i]); retryErr != nil {
				t.Fatalf("Save(retry %d) error = %v (initial error: %v)", i, retryErr, err)
			}
		}
		if succeeded == 0 {
			t.Fatal("all concurrent delivery attempts failed before spool retry")
		}
		assertSQLiteCount(t, dbPath, "events", 1)
		assertSQLiteCount(t, dbPath, "hook_deliveries", 1)
		assertSQLiteCount(t, dbPath, "hook_delivery_attempts", attempts)
		assertSQLiteCountWhere(t, dbPath, "session_workspace_observations", "delivery_record_id IS NOT NULL", 1)
	})

	t.Run("concurrent identity conflict converges after retry", func(t *testing.T) {
		t.Parallel()
		dbPath, eventDS := newHookDeliveryTestStore(t)
		events := []*model.Event{
			hookDeliveryTestEvent(t, "event-conflict-a", "session-conflict", "/repo", "/repo", "first body", "event_id:reused-delivery"),
			hookDeliveryTestEvent(t, "event-conflict-b", "session-conflict", "/repo", "/repo", "changed body", "event_id:reused-delivery"),
		}
		errs := make([]error, len(events))
		start := make(chan struct{})
		var wg sync.WaitGroup
		wg.Add(len(events))
		for i, event := range events {
			go func(index int, candidate *model.Event) {
				defer wg.Done()
				<-start
				errs[index] = eventDS.Save(context.Background(), candidate)
			}(i, event)
		}
		close(start)
		wg.Wait()

		for i, err := range errs {
			if err != nil {
				if retryErr := eventDS.Save(context.Background(), events[i]); retryErr != nil {
					t.Fatalf("Save(retry %d) error = %v (initial error: %v)", i, retryErr, err)
				}
			}
		}
		assertSQLiteCount(t, dbPath, "events", 2)
		assertSQLiteCount(t, dbPath, "hook_deliveries", 2)
		assertSQLiteCount(t, dbPath, "hook_delivery_attempts", 2)
		assertSQLiteCountWhere(t, dbPath, "hook_deliveries", "identity_status = 'accepted'", 1)
		assertSQLiteCountWhere(t, dbPath, "hook_deliveries", "identity_status = 'conflict'", 1)
	})

	t.Run("reviewed alias is diagnostic and never rewrites canonical workspace", func(t *testing.T) {
		t.Parallel()
		dbPath, eventDS := newHookDeliveryTestStore(t)
		db := openHookDeliveryTestDB(t, dbPath)
		if _, err := db.Exec(`
			INSERT INTO sessions (session_id, started_at, client, agent, workspace)
			VALUES ('session-alias', '2026-07-22T00:00:00Z', 'hook', 'codex', 'github.com/duck8823/traceary')`); err != nil {
			t.Fatalf("insert canonical session: %v", err)
		}
		if _, err := db.Exec(`
			INSERT INTO session_workspace_aliases (session_id, alias_workspace, reviewed_at, reviewed_by, note)
			VALUES ('session-alias', '/repo/traceary', '2026-07-22T00:00:01Z', 'operator', 'reviewed checkout')`); err != nil {
			t.Fatalf("insert reviewed alias: %v", err)
		}
		if err := db.Close(); err != nil {
			t.Fatalf("close setup DB: %v", err)
		}

		event := hookDeliveryTestEvent(t, "event-alias", "session-alias", "/repo/traceary", "/repo/traceary", "worktree prompt", "event_id:alias-delivery")
		if err := eventDS.Save(context.Background(), event); err != nil {
			t.Fatalf("Save(alias event) error = %v", err)
		}

		db = openHookDeliveryTestDB(t, dbPath)
		defer func() { _ = db.Close() }()
		var canonical, effective, relationship string
		if err := db.QueryRow(`SELECT workspace FROM sessions WHERE session_id = 'session-alias'`).Scan(&canonical); err != nil {
			t.Fatalf("read canonical workspace: %v", err)
		}
		if err := db.QueryRow(`
			SELECT workspace, observed_relationship
			  FROM session_workspace_observations
			 WHERE observed_event_id = 'event-alias'`).Scan(&effective, &relationship); err != nil {
			t.Fatalf("read alias observation: %v", err)
		}
		if canonical != "github.com/duck8823/traceary" || effective != "/repo/traceary" || relationship != "explicit_alias" {
			t.Fatalf("canonical/effective/relationship = %q/%q/%q", canonical, effective, relationship)
		}
	})
}

func TestSessionDatasource_HookBoundaryRedeliveryIsIdempotent(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "traceary.db")
	eventDS, sessionDS, storeManager := newFullDatasources(t, dbPath, onDiskSQLiteMigrations(t))
	if err := storeManager.Initialize(ctx); err != nil {
		t.Fatalf("Initialize() error = %v", err)
	}
	sessions := usecase.NewSessionUsecase(eventDS, sessionDS, sessionDS, eventDS)
	startCtx := apptypes.WithSourceHook(ctx, "session_start")
	startCtx = apptypes.WithHookDelivery(startCtx, apptypes.HookDeliveryInputOf("session_id:session-1", "/repo"))
	for i := 0; i < 2; i++ {
		if _, err := sessions.Start(startCtx, types.Client("hook"), types.Agent("codex"), types.SessionID("session-1"), types.Workspace("/repo"), ""); err != nil {
			t.Fatalf("Start(attempt %d) error = %v", i+1, err)
		}
	}

	endCtx := apptypes.WithSourceHook(ctx, "session_end")
	endCtx = apptypes.WithHookDelivery(endCtx, apptypes.HookDeliveryInputOf("session_id:session-1", "/repo"))
	for i := 0; i < 2; i++ {
		if _, err := sessions.End(endCtx, types.Client("hook"), types.Agent("codex"), types.SessionID("session-1"), types.Workspace("/repo"), "done"); err != nil {
			t.Fatalf("End(attempt %d) error = %v", i+1, err)
		}
	}

	assertSQLiteCount(t, dbPath, "sessions", 1)
	assertSQLiteCount(t, dbPath, "events", 2)
	assertSQLiteCount(t, dbPath, "hook_deliveries", 2)
	assertSQLiteCount(t, dbPath, "hook_delivery_attempts", 4)
	assertSQLiteCountWhere(t, dbPath, "session_workspace_observations", "delivery_record_id IS NOT NULL", 2)
}

func TestSessionDatasource_HookBoundaryRejectsChangedLifecycleSemantics(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "traceary.db")
	eventDS, sessionDS, storeManager := newFullDatasources(t, dbPath, onDiskSQLiteMigrations(t))
	if err := storeManager.Initialize(ctx); err != nil {
		t.Fatalf("Initialize() error = %v", err)
	}
	sessions := usecase.NewSessionUsecase(eventDS, sessionDS, sessionDS, eventDS)
	for _, parentID := range []types.SessionID{"parent-a", "parent-b"} {
		if _, err := sessions.Start(ctx, types.Client("hook"), types.Agent("codex"), parentID, types.Workspace("/repo"), ""); err != nil {
			t.Fatalf("Start(%s) error = %v", parentID, err)
		}
	}

	startCtx := apptypes.WithSourceHook(ctx, "session_start")
	startCtx = apptypes.WithHookDelivery(startCtx, apptypes.HookDeliveryInputOf("session_id:target", "/repo"))
	if _, err := sessions.Start(startCtx, types.Client("hook"), types.Agent("codex"), types.SessionID("target"), types.Workspace("/repo"), types.SessionID("parent-a")); err != nil {
		t.Fatalf("Start(target) error = %v", err)
	}
	if _, err := sessions.Start(startCtx, types.Client("hook"), types.Agent("codex"), types.SessionID("target"), types.Workspace("/repo"), types.SessionID("parent-a")); err != nil {
		t.Fatalf("Start(exact retry) error = %v", err)
	}
	if _, err := sessions.Start(startCtx, types.Client("hook"), types.Agent("codex"), types.SessionID("target"), types.Workspace("/repo"), types.SessionID("parent-b")); err == nil {
		t.Fatal("Start(changed parent) error = nil, want lifecycle rejection")
	}

	endCtx := apptypes.WithSourceHook(ctx, "session_end")
	endCtx = apptypes.WithHookDelivery(endCtx, apptypes.HookDeliveryInputOf("session_id:target", "/repo"))
	if _, err := sessions.End(endCtx, types.Client("hook"), types.Agent("codex"), types.SessionID("target"), types.Workspace("/repo"), "original summary"); err != nil {
		t.Fatalf("End(target) error = %v", err)
	}
	if _, err := sessions.End(endCtx, types.Client("hook"), types.Agent("codex"), types.SessionID("target"), types.Workspace("/repo"), "original summary"); err != nil {
		t.Fatalf("End(exact retry) error = %v", err)
	}
	if _, err := sessions.End(endCtx, types.Client("hook"), types.Agent("codex"), types.SessionID("target"), types.Workspace("/repo"), "changed summary"); err == nil {
		t.Fatal("End(changed summary) error = nil, want lifecycle rejection")
	}
	differentEndCtx := apptypes.WithSourceHook(ctx, "session_end")
	differentEndCtx = apptypes.WithHookDelivery(differentEndCtx, apptypes.HookDeliveryInputOf("event_id:different-end", "/repo"))
	if _, err := sessions.End(differentEndCtx, types.Client("hook"), types.Agent("codex"), types.SessionID("target"), types.Workspace("/repo"), "original summary"); err == nil {
		t.Fatal("End(different delivery) error = nil, want lifecycle rejection")
	}

	childCtx := apptypes.WithSourceHook(ctx, "subagent_start")
	childCtx = apptypes.WithHookDelivery(childCtx, apptypes.HookDeliveryInputOf("tool_use_id:child-target", "/repo"))
	childStartedAt := time.Date(2026, 7, 22, 0, 2, 0, 0, time.UTC)
	startChild := func(spawnEventID types.EventID, kind string) error {
		_, err := sessions.StartChild(
			childCtx,
			types.SessionID("parent-a"),
			types.SessionID("child-target"),
			types.Agent("codex/worker"),
			types.Workspace("/repo"),
			spawnEventID,
			kind,
			childStartedAt,
		)
		if err != nil {
			return fmt.Errorf("start child: %w", err)
		}
		return nil
	}
	if err := startChild(types.EventID("spawn-a"), "worker"); err != nil {
		t.Fatalf("StartChild(first) error = %v", err)
	}
	if err := startChild(types.EventID("spawn-a"), "worker"); err != nil {
		t.Fatalf("StartChild(exact retry) error = %v", err)
	}
	if err := startChild(types.EventID("spawn-b"), "worker"); err == nil {
		t.Fatal("StartChild(changed spawn event) error = nil, want lifecycle rejection")
	}
	if err := startChild(types.EventID("spawn-a"), "reviewer"); err == nil {
		t.Fatal("StartChild(changed kind) error = nil, want lifecycle rejection")
	}

	db := openHookDeliveryTestDB(t, dbPath)
	defer func() { _ = db.Close() }()
	var parentID, summary string
	if err := db.QueryRow(`SELECT parent_session_id, summary FROM sessions WHERE session_id = 'target'`).Scan(&parentID, &summary); err != nil {
		t.Fatalf("read target session: %v", err)
	}
	if parentID != "parent-a" || summary != "original summary" {
		t.Fatalf("parent/summary = %q/%q, want immutable parent-a/original summary", parentID, summary)
	}
	var targetEvents, targetDeliveries, targetAttempts int
	if err := db.QueryRow(`SELECT COUNT(*) FROM events WHERE session_id = 'target'`).Scan(&targetEvents); err != nil {
		t.Fatalf("count target events: %v", err)
	}
	if err := db.QueryRow(`SELECT COUNT(*) FROM hook_deliveries WHERE session_id = 'target'`).Scan(&targetDeliveries); err != nil {
		t.Fatalf("count target deliveries: %v", err)
	}
	if err := db.QueryRow(`
		SELECT COUNT(*) FROM hook_delivery_attempts a
		JOIN hook_deliveries d ON d.delivery_record_id = a.delivery_record_id
		WHERE d.session_id = 'target'`).Scan(&targetAttempts); err != nil {
		t.Fatalf("count target attempts: %v", err)
	}
	if targetEvents != 2 || targetDeliveries != 2 || targetAttempts != 4 {
		t.Fatalf("target events/deliveries/attempts = %d/%d/%d, want two logical boundaries and two exact retries", targetEvents, targetDeliveries, targetAttempts)
	}
	var childEvents, childDeliveries, childAttempts int
	if err := db.QueryRow(`SELECT COUNT(*) FROM events WHERE session_id = 'child-target'`).Scan(&childEvents); err != nil {
		t.Fatalf("count child events: %v", err)
	}
	if err := db.QueryRow(`SELECT COUNT(*) FROM hook_deliveries WHERE session_id = 'child-target'`).Scan(&childDeliveries); err != nil {
		t.Fatalf("count child deliveries: %v", err)
	}
	if err := db.QueryRow(`
		SELECT COUNT(*) FROM hook_delivery_attempts a
		JOIN hook_deliveries d ON d.delivery_record_id = a.delivery_record_id
		WHERE d.session_id = 'child-target'`).Scan(&childAttempts); err != nil {
		t.Fatalf("count child attempts: %v", err)
	}
	if childEvents != 1 || childDeliveries != 1 || childAttempts != 2 {
		t.Fatalf("child events/deliveries/attempts = %d/%d/%d, want one logical start and one exact retry", childEvents, childDeliveries, childAttempts)
	}
}

func TestSessionDatasource_HookBoundaryWithoutNativeIDUsesLifecycleGuard(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "traceary.db")
	eventDS, sessionDS, storeManager := newFullDatasources(t, dbPath, onDiskSQLiteMigrations(t))
	if err := storeManager.Initialize(ctx); err != nil {
		t.Fatalf("Initialize() error = %v", err)
	}
	sessions := usecase.NewSessionUsecase(eventDS, sessionDS, sessionDS, eventDS)
	startCtx := apptypes.WithSourceHook(ctx, "session_start")
	startCtx = apptypes.WithHookDelivery(startCtx, apptypes.HookDeliveryInputOf("", "/repo"))
	if _, err := sessions.Start(startCtx, types.Client("hook"), types.Agent("gemini"), types.SessionID("session-no-id"), types.Workspace("/repo"), ""); err != nil {
		t.Fatalf("Start(first) error = %v", err)
	}
	if _, err := sessions.Start(startCtx, types.Client("hook"), types.Agent("gemini"), types.SessionID("session-no-id"), types.Workspace("/repo"), ""); err == nil {
		t.Fatal("Start(retry) error = nil, want lifecycle rejection without stable delivery ID")
	}

	endCtx := apptypes.WithSourceHook(ctx, "session_end")
	endCtx = apptypes.WithHookDelivery(endCtx, apptypes.HookDeliveryInputOf("", "/repo"))
	if _, err := sessions.End(endCtx, types.Client("hook"), types.Agent("gemini"), types.SessionID("session-no-id"), types.Workspace("/repo"), "done"); err != nil {
		t.Fatalf("End(first) error = %v", err)
	}
	if _, err := sessions.End(endCtx, types.Client("hook"), types.Agent("gemini"), types.SessionID("session-no-id"), types.Workspace("/repo"), "done"); err == nil {
		t.Fatal("End(retry) error = nil, want lifecycle rejection without stable delivery ID")
	}

	assertSQLiteCount(t, dbPath, "sessions", 1)
	assertSQLiteCount(t, dbPath, "events", 2)
	assertSQLiteCount(t, dbPath, "hook_deliveries", 0)
}

func TestEventDatasource_HookAuditUsesFullSemanticDeliveryFingerprint(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "traceary.db")
	eventDS, storeManager := newEventDatasource(t, dbPath, onDiskSQLiteMigrations(t))
	if err := storeManager.Initialize(ctx); err != nil {
		t.Fatalf("Initialize() error = %v", err)
	}
	events := usecase.NewEventUsecase(eventDS, eventDS)
	deliveryCtx := apptypes.WithSourceHook(ctx, "post_tool_use")
	deliveryCtx = apptypes.WithHookDelivery(deliveryCtx, apptypes.HookDeliveryInputOf("tool_use_id:tool-1", "/repo"))

	audit := func(failed bool) {
		t.Helper()
		if _, _, err := events.Audit(deliveryCtx, apptypes.AuditInput{
			Command:   "go test ./...",
			Output:    "same output",
			Client:    types.Client("hook"),
			Agent:     types.Agent("codex"),
			SessionID: types.SessionID("session-audit"),
			Workspace: types.Workspace("/repo"),
			ExitCode:  types.None[int](),
			Failed:    failed,
		}, apptypes.NewAuditRedactionBuilder().Build()); err != nil {
			t.Fatalf("Audit(failed=%t) error = %v", failed, err)
		}
	}

	audit(false)
	audit(false)
	assertSQLiteCount(t, dbPath, "events", 1)
	assertSQLiteCount(t, dbPath, "command_audits", 1)

	// The host reused its native ID for a semantically different failure.
	// The event body is otherwise equal, so this proves the structural failed
	// flag participates in the delivery fingerprint.
	audit(true)
	audit(true)
	assertSQLiteCount(t, dbPath, "events", 2)
	assertSQLiteCount(t, dbPath, "command_audits", 2)
	assertSQLiteCountWhere(t, dbPath, "hook_deliveries", "identity_status = 'conflict'", 1)
}

func newHookDeliveryTestStore(t *testing.T) (string, interface {
	Save(context.Context, *model.Event) error
}) {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "traceary.db")
	eventDS, storeManager := newEventDatasource(t, dbPath, onDiskSQLiteMigrations(t))
	if err := storeManager.Initialize(context.Background()); err != nil {
		t.Fatalf("Initialize() error = %v", err)
	}
	return dbPath, eventDS
}

func hookDeliveryTestEvent(t *testing.T, eventID, sessionID, workspace, rawWorkspace, body, nativeID string) *model.Event {
	t.Helper()
	event := model.EventOfWithSourceHook(
		types.EventID(eventID),
		types.EventKindPrompt,
		types.Client("hook"),
		types.Agent("codex"),
		types.SessionID(sessionID),
		types.Workspace(workspace),
		body,
		time.Date(2026, 7, 22, 0, 0, 0, 0, time.UTC),
		"user_prompt_submit",
	)
	event.SetRawWorkspace(rawWorkspace)
	evidence, err := model.NewHookDeliveryEvidence(event, nativeID, rawWorkspace)
	if err != nil {
		t.Fatalf("NewHookDeliveryEvidence() error = %v", err)
	}
	event.SetDeliveryEvidence(evidence)
	return event
}

func openHookDeliveryTestDB(t *testing.T, dbPath string) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", "file:"+filepath.ToSlash(dbPath)+"?_pragma=foreign_keys(1)")
	if err != nil {
		t.Fatalf("sql.Open() error = %v", err)
	}
	return db
}

func assertSQLiteCount(t *testing.T, dbPath, table string, want int) {
	t.Helper()
	assertSQLiteCountWhere(t, dbPath, table, "1 = 1", want)
}

func assertSQLiteCountWhere(t *testing.T, dbPath, table, predicate string, want int) {
	t.Helper()
	db := openHookDeliveryTestDB(t, dbPath)
	defer func() { _ = db.Close() }()
	var got int
	query := "SELECT COUNT(*) FROM " + table + " WHERE " + predicate //nolint:gosec // Test-only identifiers are constants at call sites.
	if err := db.QueryRow(query).Scan(&got); err != nil {
		t.Fatalf("count %s: %v", table, err)
	}
	if got != want {
		t.Fatalf("%s count = %d, want %d (where %s)", table, got, want, predicate)
	}
}
