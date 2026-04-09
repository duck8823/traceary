package sqlite

import (
	"context"
	"log/slog"
	"database/sql"
	"errors"

	"golang.org/x/xerrors"

	"github.com/duck8823/traceary/application/queryservice"
	"github.com/duck8823/traceary/domain/model"
	"github.com/duck8823/traceary/domain/types"
)

var _ queryservice.EventDetailsFinder = (*Datasource)(nil)

// GetEventDetails returns the details for the given event ID.
func (d *Datasource) GetEventDetails(
	ctx context.Context,
	dbPath string,
	eventID string,
) (*queryservice.EventDetails, error) {
	db, err := d.openDB(ctx, dbPath)
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
		`SELECT e.id, e.kind, e.client, e.agent, e.session_id, e.repo, e.body, e.created_at,
		        ca.command_text, ca.input_text, ca.output_text, ca.input_truncated, ca.output_truncated
		   FROM events AS e
		   LEFT JOIN command_audits AS ca
		     ON ca.event_id = e.id
		  WHERE e.id = ?`,
		eventID,
	)

	var (
		commandTextValue     sql.NullString
		inputTextValue       sql.NullString
		outputTextValue      sql.NullString
		inputTruncatedValue  sql.NullBool
		outputTruncatedValue sql.NullBool
	)

	event, err := d.scanEventWithAudit(
		row,
		&commandTextValue,
		&inputTextValue,
		&outputTextValue,
		&inputTruncatedValue,
		&outputTruncatedValue,
	)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, xerrors.Errorf("event not found: %s", eventID)
		}
		return nil, xerrors.Errorf("failed to restore event details row: %w", err)
	}

	var commandAudit *model.CommandAudit
	if commandTextValue.Valid {
		eventIDValue, err := types.EventIDOf(eventID)
		if err != nil {
			return nil, xerrors.Errorf("failed to restore event ID: %w", err)
		}
		commandAudit = model.CommandAuditOf(
			eventIDValue,
			commandTextValue.String,
			inputTextValue.String,
			outputTextValue.String,
			inputTruncatedValue.Bool,
			outputTruncatedValue.Bool,
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
