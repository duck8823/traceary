package sqlite_test

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/duck8823/traceary/application"
	"github.com/duck8823/traceary/application/queryservice"
	apptypes "github.com/duck8823/traceary/application/types"
	"github.com/duck8823/traceary/application/usecase"
	"github.com/duck8823/traceary/domain"
	"github.com/duck8823/traceary/domain/types"
	infra "github.com/duck8823/traceary/infrastructure/sqlite"
)

func TestDefaultCompactCompletesSearchProjectionSoSearchDoesNotHitBudget(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "traceary.db")
	migrations := onDiskSQLiteMigrations(t)
	database := infra.NewDatabase(dbPath, migrations)
	if err := infra.NewStoreManagementDatasource(database).Initialize(ctx); err != nil {
		t.Fatalf("Initialize() error = %v", err)
	}
	const matchToken = "uniquetokenxyz"
	seedHistoricalSearchEvents(t, dbPath, 4200, func(index int) string {
		if index == 100 {
			return matchToken + " unique token body"
		}
		return "fillerword fillerword fillerword"
	})

	svc := usecase.NewStoreCompactionUsecase(
		dbPath,
		&infra.CompactionFileJournal{Dir: filepath.Join(dir, ".traceary-compaction")},
		&infra.SQLiteCompactionBuilder{},
		infra.StoreReplacementFiles{CallerHoldsExclusiveLease: true},
		infra.StoreLeaseCoordinator{},
	)
	usecase.BindCompactionProjectionComplete(svc, func(ctx context.Context, store string) error {
		return usecase.NewSearchProjectionUsecase(infra.NewDatabase(store, migrations)).CompleteGeneration(
			ctx, apptypes.DefaultSearchProjectionBudget(), time.Now(),
		)
	})
	result, err := svc.Compact(ctx, application.CompactInput{Source: dbPath, Now: time.Now()})
	if err != nil {
		t.Fatalf("Compact() error = %v", err)
	}
	if result.Run.Phase != domain.CompactionCommitted {
		t.Fatalf("phase=%s, want committed", result.Run.Phase)
	}
	if result.CompactStrategy == "" {
		t.Fatal("compact_strategy is empty")
	}

	after := infra.NewDatabase(dbPath, migrations)
	status, err := usecase.NewSearchProjectionUsecase(after).ControlStatus(ctx)
	if err != nil {
		t.Fatalf("ControlStatus() error = %v", err)
	}
	if status.State != "complete" {
		t.Fatalf("search-projection state=%s phase=%s, want complete", status.State, status.Phase)
	}

	events, err := infra.NewEventDatasource(after).Search(
		ctx,
		matchToken,
		types.Workspace("github.com/duck8823/traceary"),
		"",
		"",
		"",
		"",
		time.Time{},
		time.Time{},
		5,
		0,
		false,
	)
	if err != nil {
		var unavailable *queryservice.EventSearchUnavailableError
		if errors.As(err, &unavailable) {
			t.Fatalf("Search() hit index-incomplete budget after default compact: %v", err)
		}
		t.Fatalf("Search() error = %v", err)
	}
	if len(events) == 0 {
		t.Fatal("Search() returned no events for the seeded token")
	}

	sessionPage, err := infra.NewEventDatasource(after).SearchSessionPage(
		ctx,
		apptypes.NewEventSearchCriteriaBuilder(5).
			Query(matchToken).
			Workspace(types.Workspace("github.com/duck8823/traceary")).
			Build(),
		nil,
	)
	if err != nil {
		t.Fatalf("SearchSessionPage() error = %v", err)
	}
	if sessionPage.State() == "" {
		t.Fatal("SearchSessionPage() session-tier state is empty on a complete generation")
	}
}
