package sqlite_test

import (
	"context"
	"database/sql"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	apptypes "github.com/duck8823/traceary/application/types"
	"github.com/duck8823/traceary/application/usecase"
	"github.com/duck8823/traceary/domain/model"
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

func TestReportDatasource_SessionTotalsUseTheSameHalfOpenIntervalAndFilters(t *testing.T) {
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
	sessionID := types.SessionID("report-filter-session")
	started, err := sessionUsecase.Start(ctx, "codex", "codex", sessionID, "workspace", "")
	if err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	auditEvent, _, err := eventUsecase.Audit(ctx, apptypes.AuditInput{
		Command: "go test ./...", Client: "codex", Agent: "codex",
		SessionID: sessionID, Workspace: "workspace", ExitCode: types.Some(0),
		FailureReason: types.CommandFailureReasonNone,
	}, apptypes.NewAuditRedactionBuilder().Build())
	if err != nil {
		t.Fatalf("Audit() error = %v", err)
	}
	otherClient, err := eventUsecase.Log(
		ctx, "other client", types.EventKindNote, "claude", "claude",
		sessionID, "workspace", apptypes.NewLogRedactionBuilder().Build(),
	)
	if err != nil {
		t.Fatalf("Log() error = %v", err)
	}

	from := time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC)
	to := from.Add(time.Hour)
	conn, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("sql.Open() error = %v", err)
	}
	defer func() { _ = conn.Close() }()
	if _, err := conn.ExecContext(ctx, "UPDATE sessions SET started_at = ? WHERE session_id = ?", from.Format(time.RFC3339Nano), sessionID.String()); err != nil {
		t.Fatalf("update session timestamp: %v", err)
	}
	updates := []struct {
		id types.EventID
		at time.Time
	}{
		{id: started.EventID(), at: from},
		{id: auditEvent.EventID(), at: to},
		{id: otherClient.EventID(), at: from.Add(30 * time.Minute)},
	}
	for _, update := range updates {
		if _, err := conn.ExecContext(ctx, "UPDATE events SET created_at = ? WHERE id = ?", update.at.Format(time.RFC3339Nano), update.id.String()); err != nil {
			t.Fatalf("update event %s timestamp: %v", update.id, err)
		}
	}

	criteria, err := apptypes.ReportCriteriaFrom(
		from.Format(time.RFC3339Nano), to.Format(time.RFC3339Nano), "UTC", to,
		"workspace", "codex", 1, 0,
	)
	if err != nil {
		t.Fatalf("ReportCriteriaFrom() error = %v", err)
	}
	window, err := sqlite.NewReportDatasource(db).LoadReportWindow(ctx, criteria)
	if err != nil {
		t.Fatalf("LoadReportWindow() error = %v", err)
	}
	if len(window.Sessions) != 1 || window.Sessions[0].TotalEvents != 1 || window.Sessions[0].CommandCount != 0 {
		t.Fatalf("session rows = %+v, want one in-filter boundary event", window.Sessions)
	}
	if len(window.Events) != 1 || window.Events[0].EventID() != started.EventID() {
		t.Fatalf("event rows = %+v, want from-inclusive codex event only", window.Events)
	}
	if len(window.Commands) != 0 {
		t.Fatalf("command rows = %+v, want to-exclusive audit omitted", window.Commands)
	}
}

func TestReportDatasource_UsageSelectsCurrentSnapshotAndExposesCapAndFilters(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "traceary.db")
	db := sqlite.NewDatabase(dbPath, onDiskSQLiteMigrations(t))
	store := sqlite.NewStoreManagementDatasource(db)
	if err := store.Initialize(ctx); err != nil {
		t.Fatalf("Initialize() error = %v", err)
	}
	events := sqlite.NewEventDatasource(db)
	sessions := sqlite.NewSessionDatasource(db)
	sessionUsecase := usecase.NewSessionUsecase(events, sessions, sessions, events)
	if _, err := sessionUsecase.Start(ctx, "codex", "codex", types.SessionID("session-1"), "workspace", ""); err != nil {
		t.Fatalf("Start() error = %v", err)
	}

	usage := sqlite.NewUsageObservationDatasource(db)
	observations := []*model.UsageObservation{
		sqliteFinalizedUsage(t, sqliteUsageDescriptor(t, "report-additive"), 10),
		sqliteSnapshotObservation(t, "report-snapshot-1", 1, types.None[types.UsageObservationID](), 20),
	}
	supersededID := observations[1].Descriptor().ObservationID()
	observations = append(observations,
		sqliteSnapshotObservation(t, "report-snapshot-2", 2, types.Some(supersededID), 40),
	)
	for _, observation := range observations {
		if _, err := usage.Record(ctx, observation); err != nil {
			t.Fatalf("Record(%s) error = %v", observation.Descriptor().ObservationID(), err)
		}
	}
	if _, err := usage.Record(ctx, observations[0]); err != nil {
		t.Fatalf("Record(idempotent replay) error = %v", err)
	}

	cappedCriteria, err := apptypes.ReportCriteriaFrom(
		"2000-01-01T00:00:00Z", "2100-01-01T00:00:00Z", "UTC", time.Now().UTC(),
		"", "", 1, 1,
	)
	if err != nil {
		t.Fatalf("ReportCriteriaFrom(capped) error = %v", err)
	}
	capped, err := sqlite.NewReportDatasource(db).LoadReportWindow(ctx, cappedCriteria)
	if err != nil {
		t.Fatalf("LoadReportWindow(capped) error = %v", err)
	}
	if len(capped.Usage) != 1 || capped.Usage[0].ObservationID != "report-snapshot-2" {
		t.Fatalf("capped usage = %+v, want current snapshot head", capped.Usage)
	}
	if extent := capped.Extents.Usage; extent.Coverage != apptypes.ReportCoveragePartial ||
		!extent.ResponseTruncated || extent.TruncationReason != "result_cap" ||
		extent.ObservedCount != 1 || extent.PageSize != 1 || extent.ResultCap != 1 {
		t.Fatalf("capped usage extent = %+v", extent)
	}

	filteredCriteria, err := apptypes.ReportCriteriaFrom(
		"2000-01-01T00:00:00Z", "2100-01-01T00:00:00Z", "UTC", time.Now().UTC(),
		"workspace", "codex", 1, 0,
	)
	if err != nil {
		t.Fatalf("ReportCriteriaFrom(filtered) error = %v", err)
	}
	filtered, err := sqlite.NewReportDatasource(db).LoadReportWindow(ctx, filteredCriteria)
	if err != nil {
		t.Fatalf("LoadReportWindow(filtered) error = %v", err)
	}
	if len(filtered.Usage) != 1 || filtered.Usage[0].ObservationID != "report-additive" {
		t.Fatalf("filtered usage = %+v, want matching session usage only", filtered.Usage)
	}
	if filtered.Extents.Usage.Coverage != apptypes.ReportCoverageComplete {
		t.Fatalf("filtered usage extent = %+v", filtered.Extents.Usage)
	}
}

func TestReportDatasource_UsageWorkspaceTallyUsesTheSameInterval(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "traceary.db")
	db := sqlite.NewDatabase(dbPath, onDiskSQLiteMigrations(t))
	store := sqlite.NewStoreManagementDatasource(db)
	if err := store.Initialize(ctx); err != nil {
		t.Fatalf("Initialize() error = %v", err)
	}
	events := sqlite.NewEventDatasource(db)
	sessions := sqlite.NewSessionDatasource(db)
	sessionUsecase := usecase.NewSessionUsecase(events, sessions, sessions, events)
	if _, err := sessionUsecase.Start(ctx, "codex", "codex", types.SessionID("session-1"), "workspace", ""); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	usage := sqlite.NewUsageObservationDatasource(db)
	from := time.Date(2026, 7, 23, 0, 0, 0, 0, time.UTC)
	to := from.Add(20 * time.Hour)
	inside := sqliteExcludedUsageAt(t, "tally-inside", from.Add(time.Hour))
	outside := sqliteExcludedUsageAt(t, "tally-outside", to.Add(time.Hour))
	for _, observation := range []*model.UsageObservation{inside, outside} {
		if _, err := usage.Record(ctx, observation); err != nil {
			t.Fatalf("Record(%s) error = %v", observation.Descriptor().ObservationID(), err)
		}
	}

	windowed, err := apptypes.ReportCriteriaFrom(
		from.Format(time.RFC3339Nano), to.Format(time.RFC3339Nano), "UTC", to,
		"workspace", "codex", 100, 0,
	)
	if err != nil {
		t.Fatalf("ReportCriteriaFrom(windowed) error = %v", err)
	}
	got, err := sqlite.NewReportDatasource(db).LoadReportWindow(ctx, windowed)
	if err != nil {
		t.Fatalf("LoadReportWindow(windowed) error = %v", err)
	}
	if got.UsageWorkspaceTally.Excluded != 1 {
		t.Fatalf("windowed tally = %+v, want excluded=1 (in-window only)", got.UsageWorkspaceTally)
	}
	if len(got.Usage) != 1 || got.Usage[0].ObservationID != "tally-inside" {
		t.Fatalf("windowed usage = %+v, want in-window excluded row", got.Usage)
	}

	unbounded, err := apptypes.ReportCriteriaFrom(
		"2000-01-01T00:00:00Z", "2100-01-01T00:00:00Z", "UTC", to,
		"workspace", "codex", 100, 0,
	)
	if err != nil {
		t.Fatalf("ReportCriteriaFrom(unbounded) error = %v", err)
	}
	wide, err := sqlite.NewReportDatasource(db).LoadReportWindow(ctx, unbounded)
	if err != nil {
		t.Fatalf("LoadReportWindow(unbounded) error = %v", err)
	}
	if wide.UsageWorkspaceTally.Excluded != 2 {
		t.Fatalf("unbounded tally = %+v, want excluded=2", wide.UsageWorkspaceTally)
	}
}

func sqliteExcludedUsageAt(t *testing.T, id string, observedAt time.Time) *model.UsageObservation {
	t.Helper()
	source, err := types.UsageSourceOf("codex", "headless_stream", "0.145.0", "openai", "model-1")
	if err != nil {
		t.Fatal(err)
	}
	observationID, err := types.UsageObservationIDFrom(id)
	if err != nil {
		t.Fatal(err)
	}
	descriptor, err := model.NewUsageObservationDescriptor(
		observationID, types.SessionID("session-1"), source, types.UsageScopeCall,
		types.UsageAccountingExcluded, observedAt,
	)
	if err != nil {
		t.Fatal(err)
	}
	input, err := types.KnownUsageValue(10)
	if err != nil {
		t.Fatal(err)
	}
	zero, err := types.KnownUsageValue(0)
	if err != nil {
		t.Fatal(err)
	}
	counters, err := types.UsageCountersOf(input, zero, zero, zero, zero, input)
	if err != nil {
		t.Fatal(err)
	}
	observation, err := model.NewFinalizedUsageObservation(
		descriptor, counters, types.UnavailableUsageCost(), types.UsageTerminalSuccess, observedAt,
	)
	if err != nil {
		t.Fatal(err)
	}
	return observation
}

func TestReportDatasource_KeysetPagesMatchASingleLargeLimit(t *testing.T) {
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
	for i := 1; i <= 5; i++ {
		sessionID := types.SessionID(fmt.Sprintf("keyset-session-%d", i))
		if _, err := sessionUsecase.Start(ctx, "codex", "codex", sessionID, "workspace", ""); err != nil {
			t.Fatalf("Start(%d) error = %v", i, err)
		}
		if _, _, err := eventUsecase.Audit(ctx, apptypes.AuditInput{
			Command: "go test ./...", Client: "codex", Agent: "codex",
			SessionID: sessionID, Workspace: "workspace", ExitCode: types.Some(0),
			FailureReason: types.CommandFailureReasonNone,
		}, apptypes.NewAuditRedactionBuilder().Build()); err != nil {
			t.Fatalf("Audit(%d) error = %v", i, err)
		}
	}

	paged, err := apptypes.ReportCriteriaFrom(
		"2000-01-01T00:00:00Z", "2100-01-01T00:00:00Z", "UTC", time.Now().UTC(),
		"workspace", "codex", 1, 0,
	)
	if err != nil {
		t.Fatalf("ReportCriteriaFrom(paged) error = %v", err)
	}
	got, err := sqlite.NewReportDatasource(db).LoadReportWindow(ctx, paged)
	if err != nil {
		t.Fatalf("LoadReportWindow(paged) error = %v", err)
	}
	single, err := apptypes.ReportCriteriaFrom(
		"2000-01-01T00:00:00Z", "2100-01-01T00:00:00Z", "UTC", time.Now().UTC(),
		"workspace", "codex", 100, 0,
	)
	if err != nil {
		t.Fatalf("ReportCriteriaFrom(single) error = %v", err)
	}
	want, err := sqlite.NewReportDatasource(db).LoadReportWindow(ctx, single)
	if err != nil {
		t.Fatalf("LoadReportWindow(single) error = %v", err)
	}
	if len(got.Sessions) != 5 || len(want.Sessions) != 5 {
		t.Fatalf("session counts paged=%d single=%d", len(got.Sessions), len(want.Sessions))
	}
	for i := range want.Sessions {
		if got.Sessions[i].SessionID != want.Sessions[i].SessionID {
			t.Fatalf("session[%d] paged=%s single=%s", i, got.Sessions[i].SessionID, want.Sessions[i].SessionID)
		}
	}
	if len(got.Commands) != 5 || len(want.Commands) != 5 {
		t.Fatalf("command counts paged=%d single=%d", len(got.Commands), len(want.Commands))
	}
	for i := range want.Commands {
		if got.Commands[i].EventID != want.Commands[i].EventID {
			t.Fatalf("command[%d] paged=%s single=%s", i, got.Commands[i].EventID, want.Commands[i].EventID)
		}
	}
}
