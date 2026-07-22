package sqlite_test

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"path/filepath"
	"testing"
	"time"

	_ "modernc.org/sqlite"

	"github.com/duck8823/traceary/domain/model"
	"github.com/duck8823/traceary/domain/types"
	"github.com/duck8823/traceary/infrastructure/sqlite"
)

func TestUsageObservationDatasource_RecordFinalizesPendingIdempotently(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "traceary.db")
	db := sqlite.NewDatabase(dbPath, onDiskSQLiteMigrations(t))
	store := sqlite.NewStoreManagementDatasource(db)
	if err := store.Initialize(ctx); err != nil {
		t.Fatalf("Initialize() error = %v", err)
	}
	sut := sqlite.NewUsageObservationDatasource(db)

	descriptor := sqliteUsageDescriptor(t, "usage-late")
	pending, err := model.NewPendingUsageObservation(descriptor)
	if err != nil {
		t.Fatal(err)
	}
	transition, err := sut.Record(ctx, pending)
	if err != nil || transition != model.UsageObservationTransitionApplied {
		t.Fatalf("Record(pending) = %q, %v", transition, err)
	}
	assertUsageNumericNull(t, dbPath, "usage-late", "input_tokens")

	finalized := sqliteFinalizedUsage(t, descriptor, 0)
	transition, err = sut.Record(ctx, finalized)
	if err != nil || transition != model.UsageObservationTransitionApplied {
		t.Fatalf("Record(finalized) = %q, %v", transition, err)
	}
	transition, err = sut.Record(ctx, finalized)
	if err != nil || transition != model.UsageObservationTransitionAlreadyApplied {
		t.Fatalf("Record(replay) = %q, %v", transition, err)
	}

	stored, err := sut.FindByID(ctx, descriptor.ObservationID())
	if err != nil {
		t.Fatalf("FindByID() error = %v", err)
	}
	observation, present := stored.Value()
	if !present || observation.Status() != types.UsageObservationFinalized {
		t.Fatalf("stored observation present/status = %v/%v", present, observation)
	}
	input, known := observation.Counters().Input().Value()
	if !known || input != 0 {
		t.Fatalf("stored input = %d known=%v", input, known)
	}

	conflicting := sqliteFinalizedUsage(t, descriptor, 1)
	if _, err := sut.Record(ctx, conflicting); !errors.Is(err, model.ErrConflictingUsageObservation) {
		t.Fatalf("Record(conflict) error = %v", err)
	}
	stored, err = sut.FindByID(ctx, descriptor.ObservationID())
	if err != nil {
		t.Fatal(err)
	}
	observation, _ = stored.Value()
	input, _ = observation.Counters().Input().Value()
	if input != 0 {
		t.Fatalf("conflict mutated input = %d", input)
	}
}

func TestUsageObservationDatasource_RecordPersistsUnavailableWithoutZero(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "traceary.db")
	db := sqlite.NewDatabase(dbPath, onDiskSQLiteMigrations(t))
	if err := sqlite.NewStoreManagementDatasource(db).Initialize(ctx); err != nil {
		t.Fatal(err)
	}
	sut := sqlite.NewUsageObservationDatasource(db)
	descriptor := sqliteUsageDescriptor(t, "usage-unavailable")
	counters, err := types.UsageCountersOf(
		types.UnavailableUsageValue(), types.UnavailableUsageValue(), types.UnavailableUsageValue(),
		types.UnavailableUsageValue(), types.UnavailableUsageValue(), types.UnavailableUsageValue(),
	)
	if err != nil {
		t.Fatal(err)
	}
	observation, err := model.NewFinalizedUsageObservation(
		descriptor, counters, types.UnavailableUsageCost(), types.UsageTerminalFailure,
		time.Date(2026, 7, 23, 12, 1, 0, 0, time.UTC),
	)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := sut.Record(ctx, observation); err != nil {
		t.Fatalf("Record() error = %v", err)
	}
	assertUsageNumericNull(t, dbPath, "usage-unavailable", "input_tokens")
}

func TestUsageObservationDatasource_RecordPreservesCostProvenance(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "traceary.db")
	db := sqlite.NewDatabase(dbPath, onDiskSQLiteMigrations(t))
	if err := sqlite.NewStoreManagementDatasource(db).Initialize(ctx); err != nil {
		t.Fatal(err)
	}
	sut := sqlite.NewUsageObservationDatasource(db)
	estimated, err := types.EstimatedUsageCost(2500, "USD", "openai-2026-07-01")
	if err != nil {
		t.Fatal(err)
	}
	provider, err := types.ProviderReportedUsageCost(2400, "USD")
	if err != nil {
		t.Fatal(err)
	}

	for _, tt := range []struct {
		id          string
		cost        types.UsageCost
		wantOrigin  types.UsageCostOrigin
		wantVersion string
	}{
		{id: "usage-estimated", cost: estimated, wantOrigin: types.UsageCostEstimated, wantVersion: "openai-2026-07-01"},
		{id: "usage-provider", cost: provider, wantOrigin: types.UsageCostProviderReported},
	} {
		descriptor := sqliteUsageDescriptor(t, tt.id)
		base := sqliteFinalizedUsage(t, descriptor, 3)
		observation, err := model.NewFinalizedUsageObservation(
			descriptor, base.Counters(), tt.cost, types.UsageTerminalSuccess,
			time.Date(2026, 7, 23, 12, 1, 0, 0, time.UTC),
		)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := sut.Record(ctx, observation); err != nil {
			t.Fatalf("Record(%s) error = %v", tt.id, err)
		}
		stored, err := sut.FindByID(ctx, descriptor.ObservationID())
		if err != nil {
			t.Fatal(err)
		}
		value, present := stored.Value()
		if !present || value.Cost().Origin() != tt.wantOrigin || value.Cost().PriceTableVersion() != tt.wantVersion {
			t.Fatalf("stored cost %s = present %v origin %q version %q", tt.id, present, value.Cost().Origin(), value.Cost().PriceTableVersion())
		}
	}
}

func TestUsageObservationDatasource_RecordBuildsOneSnapshotChain(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "traceary.db")
	db := sqlite.NewDatabase(dbPath, onDiskSQLiteMigrations(t))
	if err := sqlite.NewStoreManagementDatasource(db).Initialize(ctx); err != nil {
		t.Fatal(err)
	}
	sut := sqlite.NewUsageObservationDatasource(db)

	first := sqliteSnapshotObservation(t, "snapshot-1", 1, types.None[types.UsageObservationID](), 100)
	if _, err := sut.Record(ctx, first); err != nil {
		t.Fatalf("Record(first) error = %v", err)
	}
	firstID := first.Descriptor().ObservationID()
	second := sqliteSnapshotObservation(t, "snapshot-2", 2, types.Some(firstID), 150)
	if _, err := sut.Record(ctx, second); err != nil {
		t.Fatalf("Record(second) error = %v", err)
	}

	branch := sqliteSnapshotObservation(t, "snapshot-branch", 3, types.Some(firstID), 175)
	if _, err := sut.Record(ctx, branch); !errors.Is(err, model.ErrConflictingUsageObservation) {
		t.Fatalf("Record(branch) error = %v", err)
	}

	sqlDB, err := sql.Open("sqlite", "file:"+dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = sqlDB.Close() }()
	var rows, heads int
	if err := sqlDB.QueryRow(`SELECT COUNT(*) FROM usage_observations WHERE snapshot_series = 'antigravity:conversation-1:model-1'`).Scan(&rows); err != nil {
		t.Fatal(err)
	}
	if err := sqlDB.QueryRow(`SELECT COUNT(*) FROM usage_observations AS candidate
		WHERE candidate.snapshot_series = 'antigravity:conversation-1:model-1'
		AND NOT EXISTS (SELECT 1 FROM usage_observations AS successor WHERE successor.supersedes_id = candidate.observation_id)`).Scan(&heads); err != nil {
		t.Fatal(err)
	}
	if rows != 2 || heads != 1 {
		t.Fatalf("snapshot rows/heads = %d/%d", rows, heads)
	}
}

func TestUsageObservationDatasource_RecordRejectsConcurrentConflictingFinals(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "traceary.db")
	db := sqlite.NewDatabase(dbPath, onDiskSQLiteMigrations(t))
	if err := sqlite.NewStoreManagementDatasource(db).Initialize(ctx); err != nil {
		t.Fatal(err)
	}
	sut := sqlite.NewUsageObservationDatasource(db)
	descriptor := sqliteUsageDescriptor(t, "usage-race")
	proposals := []*model.UsageObservation{
		sqliteFinalizedUsage(t, descriptor, 10),
		sqliteFinalizedUsage(t, descriptor, 20),
	}

	type result struct {
		transition model.UsageObservationTransition
		err        error
	}
	start := make(chan struct{})
	results := make(chan result, len(proposals))
	for _, proposal := range proposals {
		go func(observation *model.UsageObservation) {
			<-start
			transition, err := sut.Record(ctx, observation)
			results <- result{transition: transition, err: err}
		}(proposal)
	}
	close(start)
	applied, conflicts := 0, 0
	for range proposals {
		got := <-results
		if got.err == nil && got.transition == model.UsageObservationTransitionApplied {
			applied++
		} else if errors.Is(got.err, model.ErrConflictingUsageObservation) {
			conflicts++
		} else {
			t.Fatalf("unexpected concurrent result = %q, %v", got.transition, got.err)
		}
	}
	if applied != 1 || conflicts != 1 {
		t.Fatalf("applied/conflicts = %d/%d", applied, conflicts)
	}
}

func TestUsageObservationMigration_RejectsMalformedDirectRows(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "traceary.db")
	database := sqlite.NewDatabase(dbPath, onDiskSQLiteMigrations(t))
	if err := sqlite.NewStoreManagementDatasource(database).Initialize(ctx); err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite", "file:"+dbPath+"?_pragma=foreign_keys(1)")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()

	if err := insertRawFinalizedUsage(db, rawUsageRow{
		id: "invalid-known-cost", scope: "call", accounting: "additive",
		costState: "known", costAmount: 1,
	}); err == nil {
		t.Fatal("known cost without currency/origin was inserted")
	}
	if err := insertRawFinalizedUsage(db, rawUsageRow{
		id: "invalid-null-estimate-version", scope: "call", accounting: "additive",
		costState: "known", costAmount: 1, costCurrency: "USD", costOrigin: "estimated",
	}); err == nil {
		t.Fatal("estimated cost with a NULL price-table version was inserted")
	}
	if err := insertRawFinalizedUsage(db, rawUsageRow{
		id: "invalid-blank-estimate-version", scope: "call", accounting: "additive",
		costState: "known", costAmount: 1, costCurrency: "USD", costOrigin: "estimated", priceVersion: "   ",
	}); err == nil {
		t.Fatal("estimated cost with a blank price-table version was inserted")
	}
	if err := insertRawFinalizedUsage(db, rawUsageRow{
		id: "invalid-null-snapshot", scope: "session_snapshot", accounting: "latest_snapshot",
		costState: "unavailable",
	}); err == nil {
		t.Fatal("snapshot without series/revision was inserted")
	}
	if err := insertRawFinalizedUsage(db, rawUsageRow{
		id: "series-a-1", scope: "session_snapshot", accounting: "latest_snapshot",
		costState: "unavailable", snapshotSeries: "series-a", snapshotRevision: 1,
	}); err != nil {
		t.Fatalf("valid snapshot root insert error = %v", err)
	}
	if err := insertRawFinalizedUsage(db, rawUsageRow{
		id: "series-a-second-root", scope: "session_snapshot", accounting: "latest_snapshot",
		costState: "unavailable", snapshotSeries: "series-a", snapshotRevision: 2,
	}); err == nil {
		t.Fatal("second snapshot root was inserted")
	}
	if err := insertRawFinalizedUsage(db, rawUsageRow{
		id: "series-b-2", scope: "session_snapshot", accounting: "latest_snapshot",
		costState: "unavailable", snapshotSeries: "series-b", snapshotRevision: 2, supersedesID: "series-a-1",
	}); err == nil {
		t.Fatal("cross-series snapshot predecessor was inserted")
	}
	if err := insertRawFinalizedUsage(db, rawUsageRow{
		id: "series-a-0", scope: "session_snapshot", accounting: "latest_snapshot",
		costState: "unavailable", snapshotSeries: "series-a", snapshotRevision: 0, supersedesID: "series-a-1",
	}); err == nil {
		t.Fatal("non-increasing snapshot revision was inserted")
	}
}

type rawUsageRow struct {
	id               string
	scope            string
	accounting       string
	costState        string
	costAmount       any
	costCurrency     any
	costOrigin       any
	priceVersion     any
	snapshotSeries   any
	snapshotRevision any
	supersedesID     any
}

func insertRawFinalizedUsage(db *sql.DB, row rawUsageRow) error {
	_, err := db.Exec(`
INSERT INTO usage_observations (
    observation_id, session_id, host, source_name, source_version,
    scope, accounting, status, observed_at, finalized_at, terminal_code,
    input_state, cached_input_state, cache_write_input_state,
    output_state, reasoning_output_state, total_state,
    cost_state, cost_amount_micros, cost_currency, cost_origin, price_table_version,
    snapshot_series, snapshot_revision, supersedes_id
) VALUES (?, 'session-1', 'test-host', 'test-source', '1.0.0',
    ?, ?, 'finalized', '2026-07-23T12:00:00Z', '2026-07-23T12:01:00Z', 'success',
    'unavailable', 'unavailable', 'unavailable', 'unavailable', 'unavailable', 'unavailable',
    ?, ?, ?, ?, ?, ?, ?, ?)`,
		row.id, row.scope, row.accounting,
		row.costState, row.costAmount, row.costCurrency, row.costOrigin, row.priceVersion,
		row.snapshotSeries, row.snapshotRevision, row.supersedesID,
	)
	if err != nil {
		return fmt.Errorf("insert raw finalized usage: %w", err)
	}
	return nil
}

func sqliteUsageDescriptor(t *testing.T, value string) model.UsageObservationDescriptor {
	t.Helper()
	source, err := types.UsageSourceOf("codex", "headless_stream", "0.145.0", "openai", "model-1")
	if err != nil {
		t.Fatal(err)
	}
	id, err := types.UsageObservationIDFrom(value)
	if err != nil {
		t.Fatal(err)
	}
	descriptor, err := model.NewUsageObservationDescriptor(
		id, types.SessionID("session-1"), source, types.UsageScopeCall,
		types.UsageAccountingAdditive, time.Date(2026, 7, 23, 12, 0, 0, 0, time.UTC),
	)
	if err != nil {
		t.Fatal(err)
	}
	return descriptor
}

func sqliteFinalizedUsage(t *testing.T, descriptor model.UsageObservationDescriptor, inputTokens int64) *model.UsageObservation {
	t.Helper()
	input, err := types.KnownUsageValue(inputTokens)
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
		descriptor, counters, types.UnavailableUsageCost(), types.UsageTerminalSuccess,
		time.Date(2026, 7, 23, 12, 1, 0, 0, time.UTC),
	)
	if err != nil {
		t.Fatal(err)
	}
	return observation
}

func sqliteSnapshotObservation(
	t *testing.T,
	value string,
	revision int64,
	supersedes types.Optional[types.UsageObservationID],
	inputTokens int64,
) *model.UsageObservation {
	t.Helper()
	source, err := types.UsageSourceOf("antigravity", "statusline", "1.1.5", "google", "model-1")
	if err != nil {
		t.Fatal(err)
	}
	id, err := types.UsageObservationIDFrom(value)
	if err != nil {
		t.Fatal(err)
	}
	descriptor, err := model.NewUsageSnapshotDescriptor(
		id, types.SessionID("conversation-1"), source, "antigravity:conversation-1:model-1",
		revision, supersedes, time.Date(2026, 7, 23, 12, int(revision), 0, 0, time.UTC),
	)
	if err != nil {
		t.Fatal(err)
	}
	input, err := types.KnownUsageValue(inputTokens)
	if err != nil {
		t.Fatal(err)
	}
	output, err := types.KnownUsageValue(inputTokens / 2)
	if err != nil {
		t.Fatal(err)
	}
	u := types.UnavailableUsageValue()
	counters, err := types.UsageCountersOf(input, u, u, output, u, u)
	if err != nil {
		t.Fatal(err)
	}
	observation, err := model.NewFinalizedUsageObservation(
		descriptor, counters, types.UnavailableUsageCost(), types.UsageTerminalSuccess,
		time.Date(2026, 7, 23, 12, int(revision), 1, 0, time.UTC),
	)
	if err != nil {
		t.Fatal(err)
	}
	return observation
}

func assertUsageNumericNull(t *testing.T, dbPath, observationID, column string) {
	t.Helper()
	db, err := sql.Open("sqlite", "file:"+dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	var isNull int
	query := `SELECT ` + column + ` IS NULL FROM usage_observations WHERE observation_id = ?`
	if err := db.QueryRow(query, observationID).Scan(&isNull); err != nil {
		t.Fatal(err)
	}
	if isNull != 1 {
		t.Fatalf("%s.%s is not NULL", observationID, column)
	}
}
