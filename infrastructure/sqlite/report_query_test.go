package sqlite_test

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	apptypes "github.com/duck8823/traceary/application/types"
	"github.com/duck8823/traceary/application/usecase"
	"github.com/duck8823/traceary/domain/types"
	"github.com/duck8823/traceary/infrastructure/sqlite"
)

func TestReportDatasource_LoadReportWindowSeparatesPageSizeAndResultCap(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "traceary.db")
	db := sqlite.NewDatabase(dbPath, onDiskSQLiteMigrations(t))
	events := sqlite.NewEventDatasource(db)
	sessions := sqlite.NewSessionDatasource(db)
	store := sqlite.NewStoreManagementDatasource(db)
	if err := store.Initialize(ctx); err != nil {
		t.Fatalf("Initialize() error = %v", err)
	}
	sessionUsecase := usecase.NewSessionUsecase(events, sessions, sessions, events)
	eventUsecase := usecase.NewEventUsecase(events, events)
	largeOutput := strings.Repeat("x", 256*1024)
	for i := 1; i <= 3; i++ {
		sessionID := types.SessionID(fmt.Sprintf("report-session-%d", i))
		if _, err := sessionUsecase.Start(ctx, "codex", "codex", sessionID, "workspace", ""); err != nil {
			t.Fatalf("Start(%d) error = %v", i, err)
		}
		if _, _, err := eventUsecase.Audit(ctx, apptypes.AuditInput{
			Command: "go test ./...", Output: largeOutput,
			Client: "codex", Agent: "codex", SessionID: sessionID, Workspace: "workspace",
			ExitCode: types.Some(0), FailureReason: types.CommandFailureReasonNone,
		}, apptypes.NewAuditRedactionBuilder().Build()); err != nil {
			t.Fatalf("Audit(%d) error = %v", i, err)
		}
	}

	criteria, err := apptypes.ReportCriteriaFrom(
		"2000-01-01T00:00:00Z", "2100-01-01T00:00:00Z", "UTC", time.Now().UTC(),
		"workspace", "codex", 1, 2,
	)
	if err != nil {
		t.Fatalf("ReportCriteriaFrom() error = %v", err)
	}
	window, err := sqlite.NewReportDatasource(db).LoadReportWindow(ctx, criteria)
	if err != nil {
		t.Fatalf("LoadReportWindow() error = %v", err)
	}
	if len(window.Sessions) != 2 || len(window.Events) != 2 || len(window.Commands) != 2 {
		t.Fatalf("capped rows = sessions:%d events:%d commands:%d", len(window.Sessions), len(window.Events), len(window.Commands))
	}
	for name, extent := range map[string]apptypes.ReportSourceExtent{
		"sessions": window.Extents.Sessions, "events": window.Extents.Events, "commands": window.Extents.Commands,
	} {
		if extent.Coverage != apptypes.ReportCoveragePartial || !extent.ResponseTruncated || extent.TruncationReason != "result_cap" || extent.PageSize != 1 || extent.ResultCap != 2 {
			t.Fatalf("%s extent = %+v", name, extent)
		}
		if extent.ObservedEarliestAt == "" || extent.ObservedLatestAt == "" {
			t.Fatalf("%s observed range missing: %+v", name, extent)
		}
	}

	completeCriteria, err := apptypes.ReportCriteriaFrom(
		"2000-01-01T00:00:00Z", "2100-01-01T00:00:00Z", "UTC", time.Now().UTC(),
		"workspace", "codex", 1, 0,
	)
	if err != nil {
		t.Fatalf("ReportCriteriaFrom(complete) error = %v", err)
	}
	complete, err := sqlite.NewReportDatasource(db).LoadReportWindow(ctx, completeCriteria)
	if err != nil {
		t.Fatalf("LoadReportWindow(complete) error = %v", err)
	}
	if len(complete.Sessions) != 3 || len(complete.Commands) != 3 || len(complete.Events) < 6 {
		t.Fatalf("complete rows = sessions:%d events:%d commands:%d", len(complete.Sessions), len(complete.Events), len(complete.Commands))
	}
	if complete.Extents.Sessions.Coverage != apptypes.ReportCoverageComplete || complete.Extents.Events.Coverage != apptypes.ReportCoverageComplete || complete.Extents.Commands.Coverage != apptypes.ReportCoverageComplete {
		t.Fatalf("complete extents = %+v", complete.Extents)
	}
}
