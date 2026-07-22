package sqlite

import (
	"context"
	"database/sql"
	_ "embed"
	"errors"
	"log/slog"
	"strings"
	"time"

	"golang.org/x/xerrors"

	"github.com/duck8823/traceary/application/queryservice"
	apptypes "github.com/duck8823/traceary/application/types"
	"github.com/duck8823/traceary/domain/types"
)

//go:embed sql/list_report_command_audits.sql
var listReportCommandAuditsQuery string

var _ queryservice.CommandAuditQueryService = (*EventDatasource)(nil)

// ListReportWindow returns body-free command records under one read snapshot.
func (d *EventDatasource) ListReportWindow(ctx context.Context, criteria apptypes.EventListCriteria) ([]apptypes.ReportCommandRecord, error) {
	batch := criteria.Limit()
	if batch <= 0 {
		return nil, xerrors.New("limit must be greater than or equal to 1")
	}
	if criteria.Offset() != 0 {
		return nil, xerrors.New("offset must be zero for ListReportWindow")
	}
	db, err := d.db.open(ctx)
	if err != nil {
		return nil, xerrors.Errorf("failed to open DB for report command audit listing: %w", err)
	}
	defer func() {
		if err := db.Close(); err != nil {
			slog.Debug("failed to close resource", "error", err)
		}
	}()
	tx, err := db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return nil, xerrors.Errorf("failed to begin report command audit transaction: %w", err)
	}
	defer func() {
		if err := tx.Rollback(); err != nil && !errors.Is(err, sql.ErrTxDone) {
			slog.Debug("failed to rollback transaction", "error", err)
		}
	}()

	from, to := "", ""
	if !criteria.From().IsZero() {
		from = formatTimestamp(criteria.From())
	}
	if !criteria.To().IsZero() {
		to = formatTimestamp(criteria.To())
	}
	records := make([]apptypes.ReportCommandRecord, 0, batch)
	for offset := 0; ; {
		rows, err := tx.QueryContext(ctx, listReportCommandAuditsQuery,
			criteria.Client().String(), criteria.Client().String(),
			criteria.Agent().String(), criteria.Agent().String(),
			criteria.SessionID().String(), criteria.SessionID().String(),
			criteria.Workspace().String(), criteria.Workspace().String(),
			from, from, to, to, batch, offset,
		)
		if err != nil {
			return nil, xerrors.Errorf("failed to query report command audit page: %w", err)
		}
		pageCount := 0
		for rows.Next() {
			record, err := scanReportCommandRecord(rows)
			if err != nil {
				_ = rows.Close()
				return nil, err
			}
			records = append(records, record)
			pageCount++
		}
		if err := rows.Err(); err != nil {
			_ = rows.Close()
			return nil, xerrors.Errorf("failed to iterate report command audit page: %w", err)
		}
		_ = rows.Close()
		if pageCount < batch {
			break
		}
		offset += pageCount
	}
	if err := tx.Commit(); err != nil {
		return nil, xerrors.Errorf("failed to commit report command audit read transaction: %w", err)
	}
	return records, nil
}

func scanReportCommandRecord(row interface{ Scan(...any) error }) (apptypes.ReportCommandRecord, error) {
	var (
		eventID, client, agent, sessionID, workspace string
		wrapper, commandName, failureReason          string
		exitCode                                     sql.NullInt64
		failed                                       bool
		createdAtValue                               string
	)
	if err := row.Scan(&eventID, &client, &agent, &sessionID, &workspace, &wrapper, &commandName, &exitCode, &failed, &failureReason, &createdAtValue); err != nil {
		return apptypes.ReportCommandRecord{}, xerrors.Errorf("failed to scan report command audit: %w", err)
	}
	reason, err := types.CommandFailureReasonFrom(failureReason)
	if err != nil {
		return apptypes.ReportCommandRecord{}, xerrors.Errorf("failed to restore report command failure reason: %w", err)
	}
	createdAt, err := time.Parse(time.RFC3339Nano, createdAtValue)
	if err != nil {
		return apptypes.ReportCommandRecord{}, xerrors.Errorf("failed to restore report command timestamp: %w", err)
	}
	wrapperValue := types.None[types.CommandName]()
	if strings.TrimSpace(wrapper) != "" {
		wrapperValue = types.Some(types.CommandName(wrapper))
	}
	return apptypes.ReportCommandRecord{
		EventID: types.EventID(eventID), Client: types.Client(client), Agent: types.Agent(agent),
		SessionID: types.SessionID(sessionID), Workspace: types.Workspace(workspace),
		Wrapper: wrapperValue, CommandName: types.CommandName(commandName),
		ExitCode: optionalIntFromNullInt64(exitCode), Failed: failed,
		FailureReason: reason, CreatedAt: createdAt,
	}, nil
}
