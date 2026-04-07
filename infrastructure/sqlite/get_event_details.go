package sqlite

import (
	"context"
	"database/sql"
	"errors"

	"golang.org/x/xerrors"

	"github.com/duck8823/traceary/application/queryservice"
	"github.com/duck8823/traceary/domain/model"
	"github.com/duck8823/traceary/domain/types"
)

var _ queryservice.EventDetailsFinder = (*Datasource)(nil)

// GetEventDetails は指定 event ID の詳細を返します。
func (d *Datasource) GetEventDetails(
	ctx context.Context,
	dbPath string,
	eventID string,
) (*queryservice.EventDetails, error) {
	db, err := d.openDB(ctx, dbPath)
	if err != nil {
		return nil, xerrors.Errorf("イベント詳細取得用の DB オープンに失敗しました: %w", err)
	}
	defer func() { _ = db.Close() }()

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
			return nil, xerrors.Errorf("指定された event は存在しません: %s", eventID)
		}
		return nil, xerrors.Errorf("イベント詳細行の復元に失敗しました: %w", err)
	}

	var commandAudit *model.CommandAudit
	if commandTextValue.Valid {
		eventIDValue, err := types.EventIDOf(eventID)
		if err != nil {
			return nil, xerrors.Errorf("event ID の復元に失敗しました: %w", err)
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
		return nil, xerrors.Errorf("イベント詳細の生成に失敗しました: %w", err)
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
		return nil, xerrors.Errorf("イベント詳細行の scan に失敗しました: %w", err)
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
