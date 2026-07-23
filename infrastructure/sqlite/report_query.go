package sqlite

import (
	"context"
	"database/sql"
	_ "embed"
	"errors"
	"log/slog"
	"time"

	"golang.org/x/xerrors"

	"github.com/duck8823/traceary/application/queryservice"
	apptypes "github.com/duck8823/traceary/application/types"
	"github.com/duck8823/traceary/domain/types"
)

//go:embed sql/list_report_sessions.sql
var listReportSessionsQuery string

//go:embed sql/list_report_usage.sql
var listReportUsageQuery string

var _ queryservice.ReportQueryService = (*ReportDatasource)(nil)

// ReportDatasource loads all report projections through one SQLite snapshot.
type ReportDatasource struct {
	db *Database
}

// NewReportDatasource creates a report query adapter for one database.
func NewReportDatasource(db *Database) *ReportDatasource {
	return &ReportDatasource{db: db}
}

// LoadReportWindow loads body-free aggregate inputs in one read transaction.
func (d *ReportDatasource) LoadReportWindow(ctx context.Context, criteria apptypes.ReportCriteria) (apptypes.ReportWindow, error) {
	if d == nil || d.db == nil {
		return apptypes.ReportWindow{}, xerrors.New("report database is not configured")
	}
	if criteria.PageSize() <= 0 {
		return apptypes.ReportWindow{}, xerrors.New("page size must be greater than or equal to 1")
	}
	if criteria.PageSize() > apptypes.MaxReportPageSize {
		return apptypes.ReportWindow{}, xerrors.Errorf("page size must be less than or equal to %d", apptypes.MaxReportPageSize)
	}
	if criteria.ResultCap() < 0 {
		return apptypes.ReportWindow{}, xerrors.New("result cap must be greater than or equal to 0")
	}
	db, err := d.db.open(ctx)
	if err != nil {
		return apptypes.ReportWindow{}, xerrors.Errorf("failed to open DB for report: %w", err)
	}
	defer closeMetadataResource(db)

	tx, err := db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return apptypes.ReportWindow{}, xerrors.Errorf("failed to begin report transaction: %w", err)
	}
	defer func() {
		if err := tx.Rollback(); err != nil && !errors.Is(err, sql.ErrTxDone) {
			slog.Debug("failed to rollback report transaction", "error", err)
		}
	}()

	sessions, sessionsTruncated, err := loadCappedReportRows(criteria.PageSize(), criteria.ResultCap(), func(limit, offset int) ([]apptypes.ReportSessionRecord, error) {
		return queryReportSessionPage(ctx, tx, criteria, limit, offset)
	})
	if err != nil {
		return apptypes.ReportWindow{}, xerrors.Errorf("failed to load report sessions: %w", err)
	}
	events, eventsTruncated, err := loadCappedReportRows(criteria.PageSize(), criteria.ResultCap(), func(limit, offset int) ([]apptypes.EventMetadata, error) {
		rows, err := queryRecentEventMetadataTx(
			ctx, tx, reportEventCriteria(criteria),
			formatOptionalTimestamp(criteria.Interval().EffectiveFromInclusive()),
			formatOptionalTimestamp(criteria.Interval().EffectiveToExclusive()),
			limit, offset,
		)
		if err != nil {
			return nil, err
		}
		return collectEventMetadata(rows, limit, "report event metadata")
	})
	if err != nil {
		return apptypes.ReportWindow{}, xerrors.Errorf("failed to load report events: %w", err)
	}
	commands, commandsTruncated, err := loadCappedReportRows(criteria.PageSize(), criteria.ResultCap(), func(limit, offset int) ([]apptypes.ReportCommandRecord, error) {
		return queryReportCommandPage(ctx, tx, criteria, limit, offset)
	})
	if err != nil {
		return apptypes.ReportWindow{}, xerrors.Errorf("failed to load report commands: %w", err)
	}
	usage, usageTruncated, err := loadCappedReportRows(criteria.PageSize(), criteria.ResultCap(), func(limit, offset int) ([]apptypes.ReportUsageRecord, error) {
		return queryReportUsagePage(ctx, tx, criteria, limit, offset)
	})
	if err != nil {
		return apptypes.ReportWindow{}, xerrors.Errorf("failed to load report usage: %w", err)
	}

	extents, err := buildReportSourceExtents(
		criteria,
		sessions, sessionsTruncated,
		events, eventsTruncated,
		commands, commandsTruncated,
		usage, usageTruncated,
	)
	if err != nil {
		return apptypes.ReportWindow{}, err
	}
	if err := tx.Commit(); err != nil {
		return apptypes.ReportWindow{}, xerrors.Errorf("failed to commit report transaction: %w", err)
	}
	return apptypes.ReportWindow{
		Sessions: sessions, Events: events, Commands: commands, Usage: usage, Extents: extents,
	}, nil
}

func loadCappedReportRows[T any](pageSize, resultCap int, loadPage func(limit, offset int) ([]T, error)) ([]T, bool, error) {
	target := 0
	if resultCap > 0 {
		target = resultCap + 1
	}
	rows := make([]T, 0, pageSize)
	for offset := 0; ; {
		limit := pageSize
		if target > 0 && target-len(rows) < limit {
			limit = target - len(rows)
		}
		page, err := loadPage(limit, offset)
		if err != nil {
			return nil, false, err
		}
		rows = append(rows, page...)
		if target > 0 && len(rows) >= target {
			break
		}
		if len(page) < limit {
			break
		}
		offset += len(page)
	}
	truncated := resultCap > 0 && len(rows) > resultCap
	if truncated {
		rows = rows[:resultCap]
	}
	return rows, truncated, nil
}

func queryReportSessionPage(ctx context.Context, tx *sql.Tx, criteria apptypes.ReportCriteria, limit, offset int) ([]apptypes.ReportSessionRecord, error) {
	from := formatOptionalTimestamp(criteria.Interval().EffectiveFromInclusive())
	to := formatOptionalTimestamp(criteria.Interval().EffectiveToExclusive())
	rows, err := tx.QueryContext(
		ctx, listReportSessionsQuery,
		criteria.Workspace().String(), criteria.Workspace().String(),
		criteria.Client().String(), criteria.Client().String(),
		from, from, to, to, limit, offset,
		criteria.Workspace().String(), criteria.Workspace().String(),
		criteria.Client().String(), criteria.Client().String(),
		from, from, to, to,
	)
	if err != nil {
		return nil, xerrors.Errorf("failed to query report session page: %w", err)
	}
	defer func() {
		if err := rows.Close(); err != nil {
			slog.Debug("failed to close report session rows", "error", err)
		}
	}()
	result := make([]apptypes.ReportSessionRecord, 0, limit)
	for rows.Next() {
		var client, startedAtValue string
		var totalEvents, commandCount int
		if err := rows.Scan(&client, &startedAtValue, &totalEvents, &commandCount); err != nil {
			return nil, xerrors.Errorf("failed to scan report session row: %w", err)
		}
		startedAt, err := time.Parse(time.RFC3339Nano, startedAtValue)
		if err != nil {
			return nil, xerrors.Errorf("failed to restore report session timestamp: %w", err)
		}
		result = append(result, apptypes.ReportSessionRecord{
			Client: types.Client(client), StartedAt: startedAt,
			TotalEvents: totalEvents, CommandCount: commandCount,
		})
	}
	if err := rows.Err(); err != nil {
		return nil, xerrors.Errorf("failed to iterate report session rows: %w", err)
	}
	return result, nil
}

func queryReportCommandPage(ctx context.Context, tx *sql.Tx, criteria apptypes.ReportCriteria, limit, offset int) ([]apptypes.ReportCommandRecord, error) {
	from := formatOptionalTimestamp(criteria.Interval().EffectiveFromInclusive())
	to := formatOptionalTimestamp(criteria.Interval().EffectiveToExclusive())
	rows, err := tx.QueryContext(
		ctx, listReportCommandAuditsQuery,
		criteria.Client().String(), criteria.Client().String(),
		"", "", "", "",
		criteria.Workspace().String(), criteria.Workspace().String(),
		from, from, to, to, limit, offset,
	)
	if err != nil {
		return nil, xerrors.Errorf("failed to query report command page: %w", err)
	}
	defer func() {
		if err := rows.Close(); err != nil {
			slog.Debug("failed to close report command rows", "error", err)
		}
	}()
	result := make([]apptypes.ReportCommandRecord, 0, limit)
	for rows.Next() {
		record, err := scanReportCommandRecord(rows)
		if err != nil {
			return nil, err
		}
		result = append(result, record)
	}
	if err := rows.Err(); err != nil {
		return nil, xerrors.Errorf("failed to iterate report command rows: %w", err)
	}
	return result, nil
}

func queryReportUsagePage(
	ctx context.Context,
	tx *sql.Tx,
	criteria apptypes.ReportCriteria,
	limit, offset int,
) ([]apptypes.ReportUsageRecord, error) {
	from := formatOptionalTimestamp(criteria.Interval().EffectiveFromInclusive())
	to := formatOptionalTimestamp(criteria.Interval().EffectiveToExclusive())
	rows, err := tx.QueryContext(
		ctx, listReportUsageQuery,
		criteria.Workspace().String(), criteria.Workspace().String(),
		criteria.Client().String(), criteria.Client().String(),
		from, from, to, to, limit, offset,
	)
	if err != nil {
		return nil, xerrors.Errorf("failed to query report usage page: %w", err)
	}
	defer func() {
		if err := rows.Close(); err != nil {
			slog.Debug("failed to close report usage rows", "error", err)
		}
	}()
	result := make([]apptypes.ReportUsageRecord, 0, limit)
	for rows.Next() {
		record, err := scanReportUsageRecord(rows)
		if err != nil {
			return nil, err
		}
		result = append(result, record)
	}
	if err := rows.Err(); err != nil {
		return nil, xerrors.Errorf("failed to iterate report usage rows: %w", err)
	}
	return result, nil
}

func scanReportUsageRecord(scanner interface{ Scan(...any) error }) (apptypes.ReportUsageRecord, error) {
	var (
		record                                                apptypes.ReportUsageRecord
		observedAtValue, accountingValue, terminalCodeValue   string
		inputState, cachedState, cacheWriteState, outputState string
		reasoningState, totalState, costState, costOrigin     string
		input, cached, cacheWrite, output, reasoning, total   sql.NullInt64
		costAmount, pullRequest, packetBytes, toolOutputBytes sql.NullInt64
		costCurrency, priceTableVersion                       string
	)
	if err := scanner.Scan(
		&record.ObservationID,
		&observedAtValue,
		&record.Engine,
		&record.Provider,
		&record.Model,
		&accountingValue,
		&terminalCodeValue,
		&inputState, &input,
		&cachedState, &cached,
		&cacheWriteState, &cacheWrite,
		&outputState, &output,
		&reasoningState, &reasoning,
		&totalState, &total,
		&costState, &costAmount, &costCurrency, &costOrigin, &priceTableVersion,
		&record.RunHost, &record.RunID, &record.Repository, &record.TicketRef,
		&pullRequest, &record.BatchID, &packetBytes, &toolOutputBytes,
	); err != nil {
		return apptypes.ReportUsageRecord{}, xerrors.Errorf("failed to scan report usage row: %w", err)
	}
	observedAt, err := time.Parse(time.RFC3339Nano, observedAtValue)
	if err != nil {
		return apptypes.ReportUsageRecord{}, xerrors.Errorf("failed to restore report usage timestamp: %w", err)
	}
	accounting, err := types.UsageAccountingFrom(accountingValue)
	if err != nil {
		return apptypes.ReportUsageRecord{}, xerrors.Errorf("failed to restore report usage accounting: %w", err)
	}
	terminalCode, err := types.UsageTerminalCodeFrom(terminalCodeValue)
	if err != nil {
		return apptypes.ReportUsageRecord{}, xerrors.Errorf("failed to restore report usage terminal code: %w", err)
	}
	rawValues := []struct {
		state string
		value sql.NullInt64
	}{
		{state: inputState, value: input},
		{state: cachedState, value: cached},
		{state: cacheWriteState, value: cacheWrite},
		{state: outputState, value: output},
		{state: reasoningState, value: reasoning},
		{state: totalState, value: total},
	}
	values := make([]types.UsageValue, 0, len(rawValues))
	for _, raw := range rawValues {
		value, err := types.UsageValueFrom(raw.state, optionalInt64(raw.value))
		if err != nil {
			return apptypes.ReportUsageRecord{}, xerrors.Errorf("failed to restore report usage value: %w", err)
		}
		values = append(values, value)
	}
	counters, err := types.UsageCountersOf(
		values[0], values[1], values[2], values[3], values[4], values[5],
	)
	if err != nil {
		return apptypes.ReportUsageRecord{}, xerrors.Errorf("failed to restore report usage counters: %w", err)
	}
	cost, err := types.UsageCostFrom(
		costState, optionalInt64(costAmount), costCurrency, costOrigin, priceTableVersion,
	)
	if err != nil {
		return apptypes.ReportUsageRecord{}, xerrors.Errorf("failed to restore report usage cost: %w", err)
	}
	record.ObservedAt = observedAt
	record.Accounting = accounting
	record.TerminalCode = terminalCode
	record.Counters = counters
	record.Cost = cost
	record.PullRequest = optionalInt64(pullRequest)
	record.PacketBytes = optionalInt64(packetBytes)
	record.ToolOutputBytes = optionalInt64(toolOutputBytes)
	return record, nil
}

func reportEventCriteria(criteria apptypes.ReportCriteria) apptypes.EventListCriteria {
	return apptypes.NewEventListCriteriaBuilder(criteria.PageSize()).
		Workspace(criteria.Workspace()).
		Client(criteria.Client()).
		From(criteria.Interval().EffectiveFromInclusive()).
		To(criteria.Interval().EffectiveToExclusive()).
		Build()
}

func buildReportSourceExtents(
	criteria apptypes.ReportCriteria,
	sessions []apptypes.ReportSessionRecord,
	sessionsTruncated bool,
	events []apptypes.EventMetadata,
	eventsTruncated bool,
	commands []apptypes.ReportCommandRecord,
	commandsTruncated bool,
	usage []apptypes.ReportUsageRecord,
	usageTruncated bool,
) (apptypes.ReportSourceExtents, error) {
	sessionTimes := make([]time.Time, 0, len(sessions))
	for _, session := range sessions {
		sessionTimes = append(sessionTimes, session.StartedAt)
	}
	eventTimes := make([]time.Time, 0, len(events))
	for _, event := range events {
		eventTimes = append(eventTimes, event.CreatedAt())
	}
	commandTimes := make([]time.Time, 0, len(commands))
	for _, command := range commands {
		commandTimes = append(commandTimes, command.CreatedAt)
	}
	usageTimes := make([]time.Time, 0, len(usage))
	for _, observation := range usage {
		usageTimes = append(usageTimes, observation.ObservedAt)
	}
	sessionExtent, err := apptypes.ReportSourceExtentOf(sessionTimes, criteria.PageSize(), criteria.ResultCap(), sessionsTruncated)
	if err != nil {
		return apptypes.ReportSourceExtents{}, xerrors.Errorf("failed to build report session extent: %w", err)
	}
	eventExtent, err := apptypes.ReportSourceExtentOf(eventTimes, criteria.PageSize(), criteria.ResultCap(), eventsTruncated)
	if err != nil {
		return apptypes.ReportSourceExtents{}, xerrors.Errorf("failed to build report event extent: %w", err)
	}
	commandExtent, err := apptypes.ReportSourceExtentOf(commandTimes, criteria.PageSize(), criteria.ResultCap(), commandsTruncated)
	if err != nil {
		return apptypes.ReportSourceExtents{}, xerrors.Errorf("failed to build report command extent: %w", err)
	}
	usageExtent, err := apptypes.ReportSourceExtentOf(
		usageTimes, criteria.PageSize(), criteria.ResultCap(), usageTruncated,
	)
	if err != nil {
		return apptypes.ReportSourceExtents{}, xerrors.Errorf("failed to build report usage extent: %w", err)
	}
	return apptypes.ReportSourceExtents{
		Sessions: sessionExtent, Events: eventExtent, Commands: commandExtent, Usage: usageExtent,
	}, nil
}
