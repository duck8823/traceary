package sqlite_test

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	_ "modernc.org/sqlite"

	"github.com/duck8823/traceary/application/usecase"
	"github.com/duck8823/traceary/domain/model"
	"github.com/duck8823/traceary/domain/types"
	"github.com/duck8823/traceary/infrastructure/sqlite"
)

func TestConsolidationReplayQuery_MatchesUsecase(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "traceary.db")
	database := sqlite.NewDatabase(dbPath, onDiskSQLiteMigrations(t))
	store := sqlite.NewStoreManagementDatasource(database)
	if err := store.Initialize(ctx); err != nil {
		t.Fatalf("Initialize() error = %v", err)
	}
	sessions := sqlite.NewSessionDatasource(database)
	events := sqlite.NewEventDatasource(database)
	refinements := sqlite.NewSessionRefinementDatasource(database)
	ledger := sqlite.NewConsolidationRequestDatasource(database)
	pressure := usecase.NewConsolidationPressureUsecase(events, refinements, sessions, ledger)
	policy := usecase.ConsolidationPolicy{MinCommands: 20, StopCadence: 8}
	august := time.Date(2026, 8, 8, 12, 0, 0, 0, time.UTC)
	july := time.Date(2026, 7, 15, 12, 0, 0, 0, time.UTC)

	seedSession := func(id, client, agent string, started time.Time, commands int) {
		t.Helper()
		session := model.NewSession(types.SessionID(id), started, types.Client(client), types.Agent(agent), "ws")
		start, err := model.NewEventWithClock(
			types.EventID(id+"-start"), types.EventKindSessionStarted, types.Client(client), types.Agent(agent),
			types.SessionID(id), "ws", "start",
			fixedEventClock{at: started},
		)
		if err != nil {
			t.Fatal(err)
		}
		if err := sessions.SaveBoundary(ctx, session, start); err != nil {
			t.Fatal(err)
		}
		for i := 0; i < commands; i++ {
			cmd, err := model.NewEventWithClock(
				types.EventID(id+"-cmd-"+strconv.Itoa(i)), types.EventKindCommandExecuted, types.Client(client), types.Agent(agent),
				types.SessionID(id), "ws", "cmd",
				fixedEventClock{at: started.Add(time.Duration(i+1) * time.Second)},
			)
			if err != nil {
				t.Fatal(err)
			}
			if err := events.Save(ctx, cmd); err != nil {
				t.Fatal(err)
			}
		}
	}

	seedSession("sess-due", "claude", "claude", august, 20)
	seedSession("sess-below", "claude", "claude", august, 5)
	seedSession("sess-cadence", "codex", "codex", august, 25)
	seedSession("sess-empty", "codex", "codex", august, 0)
	seedSession("sess-july", "claude", "claude", july, 40)
	seedSession("sess-slash", "claude", "claude/explore", august, 30)

	parent := model.NewSession("sess-parent", august, "cli", "claude", "ws")
	parentStart, err := model.NewEventWithClock(
		"sess-parent-start", types.EventKindSessionStarted, "cli", "claude",
		"sess-parent", "ws", "start",
		fixedEventClock{at: august},
	)
	if err != nil {
		t.Fatal(err)
	}
	if err := sessions.SaveBoundary(ctx, parent, parentStart); err != nil {
		t.Fatal(err)
	}
	child := model.NewChildSession(parent, "sess-child", august, "claude", "ws", "sess-parent-start", "explore", 1)
	childStart, err := model.NewEventWithClock(
		"sess-child-start", types.EventKindSessionStarted, "cli", "claude",
		"sess-child", "ws", "start",
		fixedEventClock{at: august.Add(time.Second)},
	)
	if err != nil {
		t.Fatal(err)
	}
	if err := sessions.SaveBoundary(ctx, child, childStart); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 30; i++ {
		cmd, err := model.NewEventWithClock(
			types.EventID("sess-child-cmd-"+strconv.Itoa(i)), types.EventKindCommandExecuted, "cli", "claude",
			"sess-child", "ws", "cmd",
			fixedEventClock{at: august.Add(time.Duration(i+2) * time.Second)},
		)
		if err != nil {
			t.Fatal(err)
		}
		if err := events.Save(ctx, cmd); err != nil {
			t.Fatal(err)
		}
	}

	anchor, err := model.NewEventWithClock(
		"sess-cadence-anchor", types.EventKindNote, "cli", "codex",
		"sess-cadence", "ws", "anchor",
		fixedEventClock{at: august.Add(40 * time.Second)},
	)
	if err != nil {
		t.Fatal(err)
	}
	if err := events.Save(ctx, anchor); err != nil {
		t.Fatal(err)
	}
	request, err := model.NewConsolidationRequest(
		"sess-cadence", "codex", august.Add(41*time.Second), "sess-cadence-anchor",
		usecase.ConsolidationSignalWork, 25, 20, false, types.ConsolidationDeliveryStopExit2,
	)
	if err != nil {
		t.Fatal(err)
	}
	if ok, err := ledger.Save(ctx, request); err != nil || !ok {
		t.Fatalf("Save() recorded=%v err=%v", ok, err)
	}

	query, err := os.ReadFile(filepath.Join("testdata", "consolidation_replay.sql"))
	if err != nil {
		t.Fatalf("read replay SQL: %v", err)
	}
	conn, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("sql.Open() error = %v", err)
	}
	defer func() { _ = conn.Close() }()
	if _, err := conn.Exec("PRAGMA query_only=1"); err != nil {
		t.Fatalf("PRAGMA query_only: %v", err)
	}
	rows, err := conn.Query(string(query))
	if err != nil {
		t.Fatalf("replay query: %v", err)
	}
	defer func() { _ = rows.Close() }()

	replayAsk := map[string]struct{}{}
	replayListed := map[string]struct{}{}
	for rows.Next() {
		var sessionID, client string
		var commands, wouldAsk int64
		if err := rows.Scan(&sessionID, &client, &commands, &wouldAsk); err != nil {
			t.Fatalf("scan: %v", err)
		}
		replayListed[sessionID] = struct{}{}
		if wouldAsk == 1 {
			replayAsk[sessionID] = struct{}{}
		}
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("rows: %v", err)
	}
	for _, excluded := range []string{"sess-july", "sess-slash", "sess-child"} {
		if _, ok := replayListed[excluded]; ok {
			t.Fatalf("replay listed %s, want excluded (window/subagent)", excluded)
		}
	}

	checkAsk := map[string]struct{}{}
	for id := range replayListed {
		result, err := pressure.Check(ctx, types.SessionID(id), policy)
		if err != nil {
			t.Fatalf("Check(%s) error = %v", id, err)
		}
		if result.Due || result.Skipped == "cadence" {
			checkAsk[id] = struct{}{}
		}
	}

	if diff := cmpSet(replayAsk, checkAsk); diff != "" {
		t.Fatalf("would_ask set mismatch: %s", diff)
	}
}

func cmpSet(got, want map[string]struct{}) string {
	for id := range want {
		if _, ok := got[id]; !ok {
			return "missing " + id
		}
	}
	for id := range got {
		if _, ok := want[id]; !ok {
			return "extra " + id
		}
	}
	return ""
}
