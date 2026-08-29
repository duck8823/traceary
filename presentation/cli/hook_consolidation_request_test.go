package cli_test

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	_ "modernc.org/sqlite"

	"github.com/duck8823/traceary/application/usecase"
	"github.com/duck8823/traceary/domain/model"
	"github.com/duck8823/traceary/domain/types"
	sqliteinfra "github.com/duck8823/traceary/infrastructure/sqlite"
	cli "github.com/duck8823/traceary/presentation/cli"
)

type consolidationRequestUsecaseStub struct {
	err      error
	calls    int
	recorded usecase.ConsolidationRequestRecorded
}

func (s *consolidationRequestUsecaseStub) Record(context.Context, usecase.ConsolidationRequestInput) (usecase.ConsolidationRequestRecorded, error) {
	s.calls++
	return s.recorded, s.err
}

func (s *consolidationRequestUsecaseStub) RecordRefineOutcome(context.Context, model.ConsolidationRefineStamp) (bool, error) {
	return false, nil
}

type latestEventOrderStub struct {
	id   types.EventID
	err  error
	none bool
}

func (s *latestEventOrderStub) EarliestEventID(context.Context, types.SessionID) (types.Optional[types.EventID], error) {
	return types.None[types.EventID](), nil
}

func (s *latestEventOrderStub) LatestEventID(context.Context, types.SessionID) (types.Optional[types.EventID], error) {
	if s.err != nil {
		return types.None[types.EventID](), s.err
	}
	if s.none {
		return types.None[types.EventID](), nil
	}
	return types.Some(s.id), nil
}

func (s *latestEventOrderStub) FindEventSessionID(context.Context, types.EventID) (types.Optional[types.SessionID], error) {
	return types.None[types.SessionID](), nil
}

func (s *latestEventOrderStub) EventIsStrictlyAfter(context.Context, types.EventID, types.EventID) (bool, error) {
	return false, nil
}

func TestHookTranscript_ConsolidationRequestLedger(t *testing.T) {
	const sessionID = "sess-ledger"

	t.Run("a due stop writes exactly one consolidation_requests row", func(t *testing.T) {
		fx := newConsolidationHookFixture(t, sessionID)
		code, _ := fx.runTranscript(t, "first")
		if code != 2 {
			t.Fatalf("exit = %d, want 2", code)
		}
		row := mustConsolidationRow(t, fx.dbPath, sessionID)
		if row.delivery != "stop_exit_2" || row.reRequest != 0 || row.signal != "work" {
			t.Fatalf("row = %+v", row)
		}
		if row.threshold != 20 || row.pressure < 20 {
			t.Fatalf("pressure/threshold = %d/%d", row.pressure, row.threshold)
		}
		if row.atEventID != queryLatestEventID(t, fx.dbPath, sessionID) {
			t.Fatalf("at_event_id = %s, want latest %s", row.atEventID, queryLatestEventID(t, fx.dbPath, sessionID))
		}
		if countConsolidationRequests(t, fx.dbPath) != 1 {
			t.Fatalf("rows = %d, want 1", countConsolidationRequests(t, fx.dbPath))
		}
	})

	t.Run("redelivering the same stop is suppressed and inserts no second row", func(t *testing.T) {
		fx := newConsolidationHookFixture(t, sessionID)
		fx.order = &latestEventOrderStub{id: "evt-start"}
		if code, _ := fx.runTranscript(t, "one"); code != 2 {
			t.Fatalf("first exit = %d", code)
		}
		code, message := fx.runTranscript(t, "two")
		if code != 0 {
			t.Fatalf("second exit = %d, want 0; %s", code, message)
		}
		if strings.Contains(message, "unrefined material") {
			t.Fatalf("second stop emitted a reason: %q", message)
		}
		if got := countConsolidationRequests(t, fx.dbPath); got != 1 {
			t.Fatalf("rows = %d, want 1", got)
		}
	})

	t.Run("a second due stop inside the cadence window exits 0 and inserts no row", func(t *testing.T) {
		fx := newConsolidationHookFixture(t, sessionID)
		if code, _ := fx.runTranscript(t, "one"); code != 2 {
			t.Fatalf("first exit = %d", code)
		}
		code, message := fx.runTranscript(t, "two")
		if code != 0 {
			t.Fatalf("second exit = %d, want 0; %s", code, message)
		}
		if strings.Contains(message, "unrefined material") {
			t.Fatalf("second stop emitted a reason: %q", message)
		}
		if got := countConsolidationRequests(t, fx.dbPath); got != 1 {
			t.Fatalf("rows = %d, want 1", got)
		}
	})

	t.Run("a due stop after the cadence window inserts a second row", func(t *testing.T) {
		fx := newConsolidationHookFixture(t, sessionID)
		if code, _ := fx.runTranscript(t, "one"); code != 2 {
			t.Fatalf("first exit = %d", code)
		}
		seedTranscriptsAfterNow(t, fx.eventDS, sessionID, "evt-gap", 8)
		if code, _ := fx.runTranscript(t, "after-window"); code != 2 {
			t.Fatalf("after cadence exit = %d, want 2", code)
		}
		if got := countConsolidationRequests(t, fx.dbPath); got != 2 {
			t.Fatalf("rows = %d, want 2", got)
		}
	})

	t.Run("a cadence lookup failure exits 0 without asking", func(t *testing.T) {
		fx := newConsolidationHookFixture(t, sessionID)
		fx.pressureUC = &consolidationPressureStub{err: errors.New("locked")}
		code, message := fx.runTranscript(t, "one")
		if code != 0 {
			t.Fatalf("exit = %d, want 0; %s", code, message)
		}
		if strings.Contains(message, "unrefined material") {
			t.Fatalf("unexpected reason: %q", message)
		}
	})

	t.Run("a due stop after the request was accepted marks re_request=0", func(t *testing.T) {
		fx := newConsolidationHookFixture(t, sessionID)
		if code, _ := fx.runTranscript(t, "one"); code != 2 {
			t.Fatalf("first exit = %d", code)
		}
		coversTo := queryLatestEventID(t, fx.dbPath, sessionID)
		refineSession(t, fx, sessionID, coversTo)
		fx.pressureUC = &consolidationPressureStub{
			result: usecase.ConsolidationPressureResult{Commands: 20, Due: true},
		}
		if code, _ := fx.runTranscript(t, "two"); code != 2 {
			t.Fatalf("second exit = %d", code)
		}
		rows := listConsolidationRequests(t, fx.dbPath)
		if len(rows) != 2 {
			t.Fatalf("rows = %+v, want 2", rows)
		}
		if rows[1].reRequest != 0 {
			t.Fatalf("second re_request = %d, want 0", rows[1].reRequest)
		}
	})

	t.Run("a ledger insert failure still exits 2", func(t *testing.T) {
		fx := newConsolidationHookFixture(t, sessionID)
		fx.request = &consolidationRequestUsecaseStub{err: errors.New("insert failed")}
		code, message := fx.runTranscript(t, "one")
		if code != 2 {
			t.Fatalf("exit = %d, want 2; %s", code, message)
		}
		if countConsolidationRequests(t, fx.dbPath) != 0 {
			t.Fatal("ledger wrote rows on insert failure")
		}
	})

	t.Run("a latest-event lookup failure still exits 2", func(t *testing.T) {
		fx := newConsolidationHookFixture(t, sessionID)
		fx.order = &latestEventOrderStub{err: errors.New("locked")}
		code, message := fx.runTranscript(t, "one")
		if code != 2 {
			t.Fatalf("exit = %d, want 2; %s", code, message)
		}
		if countConsolidationRequests(t, fx.dbPath) != 0 {
			t.Fatal("ledger wrote rows on lookup failure")
		}
	})

	t.Run("consolidation still exits 2 when the request usecase is not configured", func(t *testing.T) {
		fx := newConsolidationHookFixture(t, sessionID)
		fx.omitRequest = true
		code, message := fx.runTranscript(t, "one")
		if code != 2 {
			t.Fatalf("exit = %d, want 2; %s", code, message)
		}
	})
}

type consolidationHookFixture struct {
	sessionID   string
	dbPath      string
	db          *sqliteinfra.Database
	eventDS     *sqliteinfra.EventDatasource
	sessionDS   *sqliteinfra.SessionDatasource
	storeUC     usecase.StoreManagementUsecase
	eventUC     usecase.EventUsecase
	pressureUC  usecase.ConsolidationPressureUsecase
	requestUC   usecase.ConsolidationRequestUsecase
	refineUC    usecase.SessionRefinementUsecase
	request     *consolidationRequestUsecaseStub
	order       model.SessionEventOrderRepository
	omitRequest bool
}

func newConsolidationHookFixture(t *testing.T, sessionID string) *consolidationHookFixture {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("TRACEARY_HOOK_STATE_DIR", t.TempDir())
	t.Setenv("TRACEARY_WORKSPACE", "github.com/dogfood/test")

	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "traceary.db")
	db := sqliteinfra.NewDatabase(dbPath, os.DirFS(filepath.Join("..", "..", "schema", "sqlite", "migrations")))
	eventDS := sqliteinfra.NewEventDatasource(db)
	sessionDS := sqliteinfra.NewSessionDatasource(db)
	refinementDS := sqliteinfra.NewSessionRefinementDatasource(db)
	ledger := sqliteinfra.NewConsolidationRequestDatasource(db)
	storeUC := usecase.NewStoreManagementUsecase(sqliteinfra.NewStoreManagementDatasource(db))
	if err := storeUC.Initialize(ctx); err != nil {
		t.Fatalf("Initialize() error = %v", err)
	}
	base := time.Date(2026, 8, 8, 12, 0, 0, 0, time.UTC)
	session := model.NewSession(types.SessionID(sessionID), base, "cli", "claude", "ws")
	start, err := model.NewEventWithClock(
		"evt-start", types.EventKindSessionStarted, "cli", "claude",
		types.SessionID(sessionID), "ws", "start",
		fixedClock{at: base},
	)
	if err != nil {
		t.Fatal(err)
	}
	if err := sessionDS.SaveBoundary(ctx, session, start); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 20; i++ {
		cmd, err := model.NewEventWithClock(
			types.EventID("evt-cmd-"+strconv.Itoa(i)), types.EventKindCommandExecuted, "cli", "claude",
			types.SessionID(sessionID), "ws", "cmd",
			fixedClock{at: base.Add(time.Duration(i+2) * time.Second)},
		)
		if err != nil {
			t.Fatal(err)
		}
		if err := eventDS.Save(ctx, cmd); err != nil {
			t.Fatal(err)
		}
	}

	return &consolidationHookFixture{
		sessionID:  sessionID,
		dbPath:     dbPath,
		db:         db,
		eventDS:    eventDS,
		sessionDS:  sessionDS,
		storeUC:    storeUC,
		eventUC:    usecase.NewEventUsecase(eventDS, eventDS),
		pressureUC: usecase.NewConsolidationPressureUsecase(eventDS, refinementDS, sessionDS, ledger),
		requestUC:  usecase.NewConsolidationRequestUsecase(ledger, types.SystemClock{}),
		refineUC:   usecase.NewSessionRefinementUsecase(sessionDS, refinementDS, eventDS, types.SystemClock{}),
		order:      eventDS,
	}
}

func (fx *consolidationHookFixture) runTranscript(t *testing.T, turn string) (int, string) {
	t.Helper()
	return fx.runTranscriptClient(t, "claude", turn)
}

func (fx *consolidationHookFixture) runTranscriptClient(t *testing.T, client, turn string) (int, string) {
	t.Helper()
	payload := `{"session_id":"` + fx.sessionID + `","cwd":"/tmp","last_assistant_message":"` + turn + `","prompt_response":"` + turn + `"}`
	opts := []cli.RootCLIOption{
		cli.WithStoreManagement(fx.storeUC),
		cli.WithEvent(fx.eventUC),
		cli.WithConsolidationPressure(fx.pressureUC),
		cli.WithSessionEventOrder(fx.order),
		cli.WithDatabasePathSetter(fx.db.SetPath),
	}
	if fx.request != nil {
		opts = append(opts, cli.WithConsolidationRequest(fx.request))
	} else if !fx.omitRequest {
		opts = append(opts, cli.WithConsolidationRequest(fx.requestUC))
	}
	rootCmd := cli.NewRootCLI(opts...).Command()
	rootCmd.SetIn(strings.NewReader(payload))
	rootCmd.SetOut(&bytes.Buffer{})
	rootCmd.SetErr(&bytes.Buffer{})
	rootCmd.SetArgs([]string{"hook", "transcript", client, "--db-path", fx.dbPath})
	err := rootCmd.Execute()
	if err == nil {
		return 0, ""
	}
	var coder interface{ ExitCode() int }
	if errors.As(err, &coder) {
		return coder.ExitCode(), err.Error()
	}
	t.Fatalf("Execute() error = %v (type %T)", err, err)
	return 0, ""
}

func refineSession(t *testing.T, fx *consolidationHookFixture, sessionID, coversTo string) {
	t.Helper()
	rootCmd := cli.NewRootCLI(
		cli.WithStoreManagement(fx.storeUC),
		cli.WithSessionRefinement(fx.refineUC),
		cli.WithConsolidationRequest(fx.requestUC),
		cli.WithDatabasePathSetter(fx.db.SetPath),
	).Command()
	rootCmd.SetOut(&bytes.Buffer{})
	rootCmd.SetErr(&bytes.Buffer{})
	rootCmd.SetArgs([]string{
		"session", "refine", sessionID,
		"--summary", "folded",
		"--covers-to", coversTo,
		"--produced-by", "agent",
		"--db-path", fx.dbPath,
	})
	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("session refine error = %v", err)
	}
}

type consolidationRow struct {
	sessionID string
	signal    string
	pressure  int64
	threshold int64
	reRequest int
	delivery  string
	atEventID string
	outcome   sql.NullString
	reason    string
}

func mustConsolidationRow(t *testing.T, dbPath, sessionID string) consolidationRow {
	t.Helper()
	rows := listConsolidationRequests(t, dbPath)
	if len(rows) != 1 {
		t.Fatalf("rows = %d, want 1", len(rows))
	}
	if rows[0].sessionID != sessionID {
		t.Fatalf("session_id = %s, want %s", rows[0].sessionID, sessionID)
	}
	return rows[0]
}

func listConsolidationRequests(t *testing.T, dbPath string) []consolidationRow {
	t.Helper()
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("sql.Open() error = %v", err)
	}
	defer func() { _ = db.Close() }()
	result, err := db.Query(`SELECT session_id, signal, pressure_value, threshold_value, re_request, delivery, at_event_id, refine_outcome, refine_reason FROM consolidation_requests ORDER BY id`)
	if err != nil {
		t.Fatalf("query consolidation_requests: %v", err)
	}
	defer func() { _ = result.Close() }()
	var rows []consolidationRow
	for result.Next() {
		var row consolidationRow
		if err := result.Scan(&row.sessionID, &row.signal, &row.pressure, &row.threshold, &row.reRequest, &row.delivery, &row.atEventID, &row.outcome, &row.reason); err != nil {
			t.Fatalf("scan: %v", err)
		}
		rows = append(rows, row)
	}
	if err := result.Err(); err != nil {
		t.Fatalf("rows: %v", err)
	}
	return rows
}

func countConsolidationRequests(t *testing.T, dbPath string) int {
	t.Helper()
	return len(listConsolidationRequests(t, dbPath))
}

func seedTranscriptsAfterNow(t *testing.T, eventDS *sqliteinfra.EventDatasource, sessionID, prefix string, n int) {
	t.Helper()
	ctx := context.Background()
	base := time.Now().UTC().Add(time.Second)
	for i := 0; i < n; i++ {
		event, err := model.NewEventWithClock(
			types.EventID(prefix+"-"+strconv.Itoa(i)), types.EventKindTranscript, "cli", "claude",
			types.SessionID(sessionID), "ws", "gap",
			fixedClock{at: base.Add(time.Duration(i) * time.Second)},
		)
		if err != nil {
			t.Fatal(err)
		}
		if err := eventDS.Save(ctx, event); err != nil {
			t.Fatal(err)
		}
	}
}

func queryLatestEventID(t *testing.T, dbPath, sessionID string) string {
	t.Helper()
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("sql.Open() error = %v", err)
	}
	defer func() { _ = db.Close() }()
	var id string
	if err := db.QueryRow(`SELECT id FROM events WHERE session_id = ? ORDER BY created_at_norm DESC, id DESC LIMIT 1`, sessionID).Scan(&id); err != nil {
		t.Fatalf("latest event: %v", err)
	}
	return id
}
