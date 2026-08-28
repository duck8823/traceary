package cli_test

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"golang.org/x/xerrors"

	"github.com/duck8823/traceary/application/usecase"
	"github.com/duck8823/traceary/domain/model"
	"github.com/duck8823/traceary/domain/types"
	sqliteinfra "github.com/duck8823/traceary/infrastructure/sqlite"
	cli "github.com/duck8823/traceary/presentation/cli"
)

func TestSessionRefine_StampsConsolidationOutcome(t *testing.T) {
	t.Parallel()

	t.Run("refine after a request stamps accepted with reason created", func(t *testing.T) {
		fx := newRefineLedgerFixture(t, "sess-created")
		seedOpenRequest(t, fx, "sess-created", "evt-1")
		if err := fx.refine(t, "sess-created", "evt-1", "agent", false); err != nil {
			t.Fatalf("refine error = %v", err)
		}
		row := mustConsolidationRow(t, fx.dbPath, "sess-created")
		if !row.outcome.Valid || row.outcome.String != "accepted" || row.reason != usecase.ConsolidationReasonCreated {
			t.Fatalf("stamp = %+v", row)
		}
	})

	t.Run("an unchanged replay stamps rejected with reason unchanged", func(t *testing.T) {
		fx := newRefineLedgerFixture(t, "sess-unchanged")
		seedOpenRequest(t, fx, "sess-unchanged", "evt-1")
		if err := fx.refine(t, "sess-unchanged", "evt-1", "agent", false); err != nil {
			t.Fatal(err)
		}
		if _, err := fx.requestUC.Record(context.Background(), usecase.ConsolidationRequestInput{
			SessionID:      "sess-unchanged",
			Client:         "claude",
			AtEventID:      "evt-2",
			Signal:         usecase.ConsolidationSignalBodyBytes,
			PressureValue:  100,
			ThresholdValue: 50,
			Delivery:       types.ConsolidationDeliveryStopExit2,
		}); err != nil {
			t.Fatal(err)
		}
		if err := fx.refine(t, "sess-unchanged", "evt-1", "agent", false); err != nil {
			t.Fatal(err)
		}
		rows := listConsolidationRequests(t, fx.dbPath)
		if len(rows) != 2 {
			t.Fatalf("rows = %d, want 2", len(rows))
		}
		last := rows[1]
		if !last.outcome.Valid || last.outcome.String != "rejected" || last.reason != usecase.ConsolidationReasonUnchanged {
			t.Fatalf("second stamp = %+v", last)
		}
	})

	t.Run("refine failure stamps rejected with a fixed token", func(t *testing.T) {
		fx := newRefineLedgerFixture(t, "sess-err")
		seedOpenRequest(t, fx, "sess-err", "evt-1")
		err := fx.refine(t, "sess-err", "evt-missing", "agent", false)
		if err == nil {
			t.Fatal("refine error = nil, want usecase error")
		}
		row := mustConsolidationRow(t, fx.dbPath, "sess-err")
		if !row.outcome.Valid || row.outcome.String != "rejected" || row.reason != usecase.ConsolidationReasonUsecaseError {
			t.Fatalf("stamp = %+v", row)
		}
		if strings.Contains(row.reason, " ") {
			t.Fatalf("reason stored error text: %q", row.reason)
		}
	})

	t.Run("invalid covers-to stamps rejected invalid_covers_to", func(t *testing.T) {
		fx := newRefineLedgerFixture(t, "sess-bad-id")
		seedOpenRequest(t, fx, "sess-bad-id", "evt-1")
		err := fx.refine(t, "sess-bad-id", "   ", "agent", false)
		if err == nil {
			t.Fatal("refine error = nil, want invalid covers-to")
		}
		row := mustConsolidationRow(t, fx.dbPath, "sess-bad-id")
		if !row.outcome.Valid || row.outcome.String != "rejected" || row.reason != usecase.ConsolidationReasonInvalidCoversTo {
			t.Fatalf("stamp = %+v", row)
		}
	})

	t.Run("refine without a prior request leaves the ledger untouched and exits 0", func(t *testing.T) {
		fx := newRefineLedgerFixture(t, "sess-none")
		if err := fx.refine(t, "sess-none", "evt-1", "agent", false); err != nil {
			t.Fatalf("refine error = %v", err)
		}
		if countConsolidationRequests(t, fx.dbPath) != 0 {
			t.Fatal("ledger grew without a prior request")
		}
	})

	t.Run("refine output and json are byte-identical with and without the ledger wired", func(t *testing.T) {
		withLedger := captureRefineJSON(t, true)
		withoutLedger := captureRefineJSON(t, false)
		if diff := cmp.Diff(withoutLedger, withLedger); diff != "" {
			t.Fatalf("json mismatch (-without +with):\n%s", diff)
		}
	})
}

type refineLedgerFixture struct {
	dbPath    string
	db        *sqliteinfra.Database
	storeUC   usecase.StoreManagementUsecase
	refineUC  usecase.SessionRefinementUsecase
	requestUC usecase.ConsolidationRequestUsecase
	ledger    *sqliteinfra.ConsolidationRequestDatasource
}

func newRefineLedgerFixture(t *testing.T, sessionID string) *refineLedgerFixture {
	t.Helper()
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
		types.EventID(sessionID+"-start"), types.EventKindSessionStarted, "cli", "claude",
		types.SessionID(sessionID), "ws", "start",
		fixedClock{at: base},
	)
	if err != nil {
		t.Fatal(err)
	}
	if err := sessionDS.SaveBoundary(ctx, session, start); err != nil {
		t.Fatal(err)
	}
	note, err := model.NewEventWithClock(
		"evt-1", types.EventKindNote, "cli", "claude",
		types.SessionID(sessionID), "ws", "note",
		fixedClock{at: base.Add(time.Second)},
	)
	if err != nil {
		t.Fatal(err)
	}
	if err := eventDS.Save(ctx, note); err != nil {
		t.Fatal(err)
	}
	return &refineLedgerFixture{
		dbPath:    dbPath,
		db:        db,
		storeUC:   storeUC,
		refineUC:  usecase.NewSessionRefinementUsecase(sessionDS, refinementDS, eventDS, types.SystemClock{}),
		requestUC: usecase.NewConsolidationRequestUsecase(ledger, types.SystemClock{}),
		ledger:    ledger,
	}
}

func seedOpenRequest(t *testing.T, fx *refineLedgerFixture, sessionID, eventID string) {
	t.Helper()
	if _, err := fx.requestUC.Record(context.Background(), usecase.ConsolidationRequestInput{
		SessionID:      types.SessionID(sessionID),
		Client:         "claude",
		AtEventID:      types.EventID(eventID),
		Signal:         usecase.ConsolidationSignalBodyBytes,
		PressureValue:  100,
		ThresholdValue: 50,
		Delivery:       types.ConsolidationDeliveryStopExit2,
	}); err != nil {
		t.Fatalf("Record() error = %v", err)
	}
}

func (fx *refineLedgerFixture) refine(t *testing.T, sessionID, coversTo, producedBy string, asJSON bool) error {
	t.Helper()
	rootCmd := cli.NewRootCLI(
		cli.WithStoreManagement(fx.storeUC),
		cli.WithSessionRefinement(fx.refineUC),
		cli.WithConsolidationRequest(fx.requestUC),
		cli.WithDatabasePathSetter(fx.db.SetPath),
	).Command()
	rootCmd.SetOut(&bytes.Buffer{})
	rootCmd.SetErr(&bytes.Buffer{})
	args := []string{
		"session", "refine", sessionID,
		"--summary", "folded",
		"--covers-to", coversTo,
		"--produced-by", producedBy,
		"--db-path", fx.dbPath,
	}
	if asJSON {
		args = append(args, "--json")
	}
	rootCmd.SetArgs(args)
	if err := rootCmd.Execute(); err != nil {
		return xerrors.Errorf("session refine: %w", err)
	}
	return nil
}

func captureRefineJSON(t *testing.T, withLedger bool) string {
	t.Helper()
	producedAt := time.Date(2026, 8, 8, 12, 0, 0, 0, time.UTC)
	refinement, err := model.NewSessionRefinement(
		"sess-1", 1, "evt-from", "evt-to", "summary text", "kw", "agent", producedAt, false,
	)
	if err != nil {
		t.Fatal(err)
	}
	result, err := model.SessionRefineResultOf(model.SessionRefineOutcomeCreated, refinement)
	if err != nil {
		t.Fatal(err)
	}
	opts := []cli.RootCLIOption{
		cli.WithStoreManagement(&storeManagementUsecaseStub{}),
		cli.WithSessionRefinement(&sessionRefinementUsecaseStub{result: result}),
	}
	if withLedger {
		opts = append(opts, cli.WithConsolidationRequest(&consolidationRequestUsecaseStub{}))
	}
	stdout := &bytes.Buffer{}
	rootCmd := cli.NewRootCLI(opts...).Command()
	rootCmd.SetOut(stdout)
	rootCmd.SetErr(&bytes.Buffer{})
	rootCmd.SetArgs([]string{
		"session", "refine", "sess-1",
		"--summary", "summary text",
		"--covers-to", "evt-to",
		"--keywords", "kw",
		"--produced-by", "agent",
		"--db-path", filepath.Join(t.TempDir(), "traceary.db"),
		"--json",
	})
	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	return stdout.String()
}
