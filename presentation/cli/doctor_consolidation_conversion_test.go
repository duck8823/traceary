package cli_test

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/duck8823/traceary/application/usecase"
	"github.com/duck8823/traceary/domain/model"
	"github.com/duck8823/traceary/domain/types"
	sqliteinfra "github.com/duck8823/traceary/infrastructure/sqlite"
	cli "github.com/duck8823/traceary/presentation/cli"
)

func TestDoctor_ConsolidationConversionEndToEnd(t *testing.T) {
	t.Run("request then refine then doctor reports 100 percent", func(t *testing.T) {
		const sessionID = "sess-doctor-e2e"
		fx := newConsolidationHookFixture(t, sessionID)
		if code, _ := fx.runTranscript(t, "turn"); code != 2 {
			t.Fatalf("transcript exit = %d, want 2", code)
		}
		coversTo := queryLatestEventID(t, fx.dbPath, sessionID)
		refineSession(t, fx, sessionID, coversTo)
		check := runDoctorConsolidationCheck(t, fx)
		if check["status"] != "pass" {
			t.Fatalf("status = %v message=%v", check["status"], check["message"])
		}
		message, _ := check["message"].(string)
		if !strings.Contains(message, "claude: 1 requests / 1 sessions asked / 1 sessions refined (100%)") {
			t.Fatalf("message = %q", message)
		}
	})

	t.Run("5 requests with 1 refined session warns below the 25 percent floor", func(t *testing.T) {
		fx := newDoctorLedgerFixture(t)
		now := time.Now().UTC()
		for i := 0; i < 5; i++ {
			sessionID := types.SessionID("sess-" + string(rune('a'+i)))
			if _, err := fx.requestUC.Record(context.Background(), usecase.ConsolidationRequestInput{
				SessionID: sessionID, Client: "claude", AtEventID: types.EventID("evt-" + string(rune('a'+i))),
				Signal: usecase.ConsolidationSignalBodyBytes, PressureValue: 10, ThresholdValue: 5,
				Delivery: types.ConsolidationDeliveryStopExit2,
			}); err != nil {
				t.Fatal(err)
			}
		}
		if _, err := fx.requestUC.RecordRefineOutcome(context.Background(), model.ConsolidationRefineStamp{
			SessionID: "sess-a", Outcome: types.ConsolidationRefineAccepted, Reason: "created", ProducedBy: "agent", Generation: types.Some(1), At: now,
		}); err != nil {
			t.Fatal(err)
		}
		check := runDoctorOnStore(t, fx.dbPath, fx.db, fx.storeUC, fx.ledger)
		if check["status"] != "warn" {
			t.Fatalf("status = %v message=%v", check["status"], check["message"])
		}
	})

	t.Run("4 requests with 0 refined passes because it is below the minimum-requests floor", func(t *testing.T) {
		fx := newDoctorLedgerFixture(t)
		for i := 0; i < 4; i++ {
			if _, err := fx.requestUC.Record(context.Background(), usecase.ConsolidationRequestInput{
				SessionID: types.SessionID("sess-" + string(rune('a'+i))), Client: "claude",
				AtEventID: types.EventID("evt-" + string(rune('a'+i))),
				Signal:    usecase.ConsolidationSignalBodyBytes, PressureValue: 10, ThresholdValue: 5,
				Delivery: types.ConsolidationDeliveryStopExit2,
			}); err != nil {
				t.Fatal(err)
			}
		}
		check := runDoctorOnStore(t, fx.dbPath, fx.db, fx.storeUC, fx.ledger)
		if check["status"] != "pass" {
			t.Fatalf("status = %v message=%v", check["status"], check["message"])
		}
	})
}

type doctorLedgerFixture struct {
	dbPath    string
	db        *sqliteinfra.Database
	storeUC   usecase.StoreManagementUsecase
	requestUC usecase.ConsolidationRequestUsecase
	ledger    *sqliteinfra.ConsolidationRequestDatasource
}

func newDoctorLedgerFixture(t *testing.T) *doctorLedgerFixture {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "traceary.db")
	db := sqliteinfra.NewDatabase(dbPath, os.DirFS(filepath.Join("..", "..", "schema", "sqlite", "migrations")))
	ledger := sqliteinfra.NewConsolidationRequestDatasource(db)
	storeUC := usecase.NewStoreManagementUsecase(sqliteinfra.NewStoreManagementDatasource(db))
	if err := storeUC.Initialize(context.Background()); err != nil {
		t.Fatalf("Initialize() error = %v", err)
	}
	return &doctorLedgerFixture{
		dbPath:    dbPath,
		db:        db,
		storeUC:   storeUC,
		requestUC: usecase.NewConsolidationRequestUsecase(ledger, types.SystemClock{}),
		ledger:    ledger,
	}
}

func runDoctorConsolidationCheck(t *testing.T, fx *consolidationHookFixture) map[string]any {
	t.Helper()
	return runDoctorOnStore(t, fx.dbPath, fx.db, fx.storeUC, sqliteinfra.NewConsolidationRequestDatasource(fx.db))
}

func runDoctorOnStore(
	t *testing.T,
	dbPath string,
	db *sqliteinfra.Database,
	storeUC usecase.StoreManagementUsecase,
	ledger *sqliteinfra.ConsolidationRequestDatasource,
) map[string]any {
	t.Helper()
	home := t.TempDir()
	projectDir := t.TempDir()
	t.Setenv("HOME", home)
	cli.SetUserHomeDirFunc(func() (string, error) { return home, nil })
	t.Cleanup(cli.ResetUserHomeDirFunc)

	stdout := &bytes.Buffer{}
	rootCmd := newTestRootCLI(
		cli.WithStoreManagement(storeUC),
		cli.WithConsolidationConversion(ledger),
		cli.WithDatabasePathSetter(db.SetPath),
	).Command()
	rootCmd.SetOut(stdout)
	rootCmd.SetErr(&bytes.Buffer{})
	rootCmd.SetArgs([]string{"doctor", "--db-path", dbPath, "--client", "claude", "--project-dir", projectDir, "--json"})
	_ = rootCmd.Execute()

	var payload map[string]any
	if err := json.Unmarshal(stdout.Bytes(), &payload); err != nil {
		t.Fatalf("json.Unmarshal() error = %v body=%s", err, stdout.String())
	}
	checks, _ := payload["checks"].([]any)
	for _, raw := range checks {
		check, _ := raw.(map[string]any)
		if check["name"] == "consolidation-conversion" {
			return check
		}
	}
	t.Fatalf("consolidation-conversion check missing: %s", stdout.String())
	return nil
}
