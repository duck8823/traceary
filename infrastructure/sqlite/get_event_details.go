package sqlite

import (
	"context"
	"database/sql"
	_ "embed"
	"errors"
	"log/slog"

	"golang.org/x/xerrors"

	"github.com/duck8823/traceary/application/queryservice"
	"github.com/duck8823/traceary/domain/model"
	"github.com/duck8823/traceary/domain/types"
)

//go:embed sql/get_event_details.sql
var getEventDetailsQuery string

// GetDetails returns the details for the given event ID.
func (d *Datasource) GetDetails(
	ctx context.Context,
	eventID types.EventID,
) (*queryservice.EventDetails, error) {
	db, err := d.openDB(ctx)
	if err != nil {
		return nil, xerrors.Errorf("failed to open DB for event details lookup: %w", err)
	}
	defer func() {
		if err := db.Close(); err != nil {
			slog.Debug("failed to close resource", "error", err)
		}
	}()

	row := db.QueryRowContext(
		ctx,
		getEventDetailsQuery,
		eventID.String(),
	)

	var (
		commandTextValue     sql.NullString
		inputTextValue       sql.NullString
		outputTextValue      sql.NullString
		inputTruncatedValue  sql.NullBool
		outputTruncatedValue sql.NullBool
		exitCodeValue        sql.NullInt64
	)

	event, err := d.scanEventWithAudit(
		row,
		&commandTextValue,
		&inputTextValue,
		&outputTextValue,
		&inputTruncatedValue,
		&outputTruncatedValue,
		&exitCodeValue,
	)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, xerrors.Errorf("event not found: %s", eventID)
		}
		return nil, xerrors.Errorf("failed to restore event details row: %w", err)
	}

	var commandAudit *model.CommandAudit
	if commandTextValue.Valid {
		var exitCode *int
		if exitCodeValue.Valid {
			v := int(exitCodeValue.Int64)
			exitCode = &v
		}
		commandAudit = model.CommandAuditOf(
			eventID,
			commandTextValue.String,
			inputTextValue.String,
			outputTextValue.String,
			inputTruncatedValue.Bool,
			outputTruncatedValue.Bool,
			exitCode,
		)
	}

	eventDetails, err := queryservice.NewEventDetails(event, commandAudit)
	if err != nil {
		return nil, xerrors.Errorf("failed to build event details: %w", err)
	}

	return eventDetails, nil
}

func (d *Datasource) scanEventWithAudit(
	rowScanner interface {
		Scan(dest ...any) error
	},
	commandTextValue *sql.NullString,
	inputTextValue *sql.NullString,
	outputTextValue *sql.NullString,
	inputTruncatedValue *sql.NullBool,
	outputTruncatedValue *sql.NullBool,
	exitCodeValue *sql.NullInt64,
) (*model.Event, error) {
	var (
		eventIDValue   string
		eventKindValue string
		clientValue    string
		agentValue     string
		sessionIDValue string
		repoValue      string
		bodyValue      string
		createdAtValue string
	)

	if err := rowScanner.Scan(
		&eventIDValue,
		&eventKindValue,
		&clientValue,
		&agentValue,
		&sessionIDValue,
		&repoValue,
		&bodyValue,
		&createdAtValue,
		commandTextValue,
		inputTextValue,
		outputTextValue,
		inputTruncatedValue,
		outputTruncatedValue,
		exitCodeValue,
	); err != nil {
		return nil, xerrors.Errorf("failed to scan event details row: %w", err)
	}

	return d.restoreEvent(
		eventIDValue,
		eventKindValue,
		clientValue,
		agentValue,
		sessionIDValue,
		repoValue,
		bodyValue,
		createdAtValue,
	)
}
