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

//go:embed sql/select_recent_event_metadata.sql
var selectRecentEventMetadataQuery string

//go:embed sql/select_recent_event_metadata_by_source_hook.sql
var selectRecentEventMetadataBySourceHookQuery string

//go:embed sql/select_recent_event_metadata_by_source_hook_with_legacy.sql
var selectRecentEventMetadataBySourceHookWithLegacyQuery string

//go:embed sql/search_event_metadata.sql
var searchEventMetadataQuery string

//go:embed sql/get_context_event_metadata.sql
var getContextEventMetadataQuery string

var _ queryservice.EventMetadataQueryService = (*EventDatasource)(nil)

// ListRecentMetadata returns body-free event metadata in descending time order.
func (d *EventDatasource) ListRecentMetadata(
	ctx context.Context,
	criteria apptypes.EventListCriteria,
) ([]apptypes.EventMetadata, error) {
	if err := validateMetadataListCriteria(criteria, false); err != nil {
		return nil, err
	}

	db, err := d.db.open(ctx)
	if err != nil {
		return nil, xerrors.Errorf("failed to open DB for event metadata listing: %w", err)
	}
	defer closeMetadataResource(db)

	rows, err := queryRecentEventMetadata(
		ctx,
		db,
		criteria,
		formatOptionalTimestamp(criteria.From()),
		formatOptionalTimestamp(criteria.To()),
		criteria.Limit(),
		criteria.Offset(),
	)
	if err != nil {
		return nil, xerrors.Errorf("failed to query recent event metadata: %w", err)
	}
	return collectEventMetadata(rows, criteria.Limit(), "recent event metadata")
}

// ListWindowMetadata returns all matching body-free events under one read snapshot.
func (d *EventDatasource) ListWindowMetadata(
	ctx context.Context,
	criteria apptypes.EventListCriteria,
) ([]apptypes.EventMetadata, error) {
	if err := validateMetadataListCriteria(criteria, true); err != nil {
		return nil, err
	}

	db, err := d.db.open(ctx)
	if err != nil {
		return nil, xerrors.Errorf("failed to open DB for event metadata window: %w", err)
	}
	defer closeMetadataResource(db)

	tx, err := db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return nil, xerrors.Errorf("failed to begin event metadata window transaction: %w", err)
	}
	defer func() {
		if err := tx.Rollback(); err != nil && !errors.Is(err, sql.ErrTxDone) {
			slog.Debug("failed to rollback event metadata transaction", "error", err)
		}
	}()

	batch := criteria.Limit()
	metadata := make([]apptypes.EventMetadata, 0, batch)
	offset := 0
	for {
		rows, err := queryRecentEventMetadataTx(
			ctx,
			tx,
			criteria,
			formatOptionalTimestamp(criteria.From()),
			formatOptionalTimestamp(criteria.To()),
			batch,
			offset,
		)
		if err != nil {
			return nil, xerrors.Errorf("failed to query event metadata window page: %w", err)
		}

		page, err := collectEventMetadata(rows, batch, "event metadata window")
		if err != nil {
			return nil, err
		}
		metadata = append(metadata, page...)
		if d.onListWindowBatch != nil {
			d.onListWindowBatch(offset/batch, len(page))
		}
		if len(page) < batch {
			break
		}
		offset += len(page)
	}
	return metadata, nil
}

// SearchMetadata searches content in SQLite while returning only body-free rows.
func (d *EventDatasource) SearchMetadata(
	ctx context.Context,
	criteria apptypes.EventSearchCriteria,
) ([]apptypes.EventMetadata, error) {
	if criteria.Limit() <= 0 {
		return nil, xerrors.Errorf("limit must be greater than or equal to 1")
	}
	if criteria.Offset() < 0 {
		return nil, xerrors.Errorf("offset must be greater than or equal to 0")
	}
	if !criteria.From().IsZero() && !criteria.To().IsZero() && criteria.From().After(criteria.To()) {
		return nil, xerrors.Errorf("from must be earlier than to")
	}

	db, err := d.db.open(ctx)
	if err != nil {
		return nil, xerrors.Errorf("failed to open DB for event metadata search: %w", err)
	}
	defer closeMetadataResource(db)

	queryValue := strings.TrimSpace(criteria.Query())
	likeQuery := "%" + escapeLikeQuery(queryValue) + "%"
	rows, err := db.QueryContext(
		ctx,
		searchEventMetadataQuery,
		queryValue,
		likeQuery,
		likeQuery,
		likeQuery,
		likeQuery,
		criteria.Workspace().String(),
		criteria.Workspace().String(),
		criteria.SessionID().String(),
		criteria.SessionID().String(),
		criteria.Client().String(),
		criteria.Client().String(),
		criteria.Agent().String(),
		criteria.Agent().String(),
		criteria.Kind().String(),
		criteria.Kind().String(),
		formatOptionalTimestamp(criteria.From()),
		formatOptionalTimestamp(criteria.From()),
		formatOptionalTimestamp(criteria.To()),
		formatOptionalTimestamp(criteria.To()),
		boolToInt(criteria.FailuresOnly()),
		criteria.Limit(),
		criteria.Offset(),
	)
	if err != nil {
		return nil, xerrors.Errorf("failed to query event metadata search: %w", err)
	}
	return collectEventMetadata(rows, criteria.Limit(), "event metadata search")
}

// GetContextMetadata returns body-free context membership in descending time order.
func (d *EventDatasource) GetContextMetadata(
	ctx context.Context,
	criteria apptypes.EventContextCriteria,
) ([]apptypes.EventMetadata, error) {
	if criteria.Limit() <= 0 {
		return nil, xerrors.Errorf("limit must be greater than or equal to 1")
	}

	db, err := d.db.open(ctx)
	if err != nil {
		return nil, xerrors.Errorf("failed to open DB for event metadata context: %w", err)
	}
	defer closeMetadataResource(db)

	workspace := strings.TrimSpace(criteria.Workspace().String())
	sessionID := strings.TrimSpace(criteria.SessionID().String())
	rows, err := db.QueryContext(
		ctx,
		getContextEventMetadataQuery,
		workspace,
		workspace,
		sessionID,
		sessionID,
		criteria.Limit(),
	)
	if err != nil {
		return nil, xerrors.Errorf("failed to query event metadata context: %w", err)
	}
	return collectEventMetadata(rows, criteria.Limit(), "event metadata context")
}

func validateMetadataListCriteria(criteria apptypes.EventListCriteria, requireZeroOffset bool) error {
	if criteria.Limit() <= 0 {
		return xerrors.Errorf("limit must be greater than or equal to 1")
	}
	if requireZeroOffset && criteria.Offset() != 0 {
		return xerrors.Errorf("offset must be zero for ListWindowMetadata (paging is handled internally)")
	}
	if !requireZeroOffset && criteria.Offset() < 0 {
		return xerrors.Errorf("offset must be greater than or equal to 0")
	}
	if !criteria.From().IsZero() && !criteria.To().IsZero() && criteria.From().After(criteria.To()) {
		return xerrors.Errorf("from must be earlier than to")
	}
	return nil
}

func formatOptionalTimestamp(value time.Time) string {
	if value.IsZero() {
		return ""
	}
	return formatTimestamp(value)
}

func closeMetadataResource(db *sql.DB) {
	if err := db.Close(); err != nil {
		slog.Debug("failed to close event metadata resource", "error", err)
	}
}

func collectEventMetadata(
	rows *sql.Rows,
	capacity int,
	operation string,
) ([]apptypes.EventMetadata, error) {
	defer func() {
		if err := rows.Close(); err != nil {
			slog.Debug("failed to close event metadata rows", "error", err)
		}
	}()

	metadata := make([]apptypes.EventMetadata, 0, capacity)
	for rows.Next() {
		row, err := scanEventMetadata(rows)
		if err != nil {
			return nil, xerrors.Errorf("failed to restore %s row: %w", operation, err)
		}
		metadata = append(metadata, row)
	}
	if err := rows.Err(); err != nil {
		return nil, xerrors.Errorf("failed to iterate %s rows: %w", operation, err)
	}
	return metadata, nil
}

func scanEventMetadata(rowScanner interface{ Scan(dest ...any) error }) (apptypes.EventMetadata, error) {
	var (
		eventIDValue          string
		eventKindValue        string
		clientValue           string
		agentValue            string
		sessionIDValue        string
		workspaceValue        string
		sourceHookValue       sql.NullString
		createdAtValue        string
		originalBytesValue    sql.NullInt64
		storedBytesValue      sql.NullInt64
		ingestTruncatedValue  sql.NullBool
		storageTruncatedValue sql.NullBool
		metadataVersionValue  sql.NullInt64
		auditEventIDValue     sql.NullString
		exitCodeValue         sql.NullInt64
		failedValue           sql.NullBool
	)
	if err := rowScanner.Scan(
		&eventIDValue,
		&eventKindValue,
		&clientValue,
		&agentValue,
		&sessionIDValue,
		&workspaceValue,
		&sourceHookValue,
		&createdAtValue,
		&originalBytesValue,
		&storedBytesValue,
		&ingestTruncatedValue,
		&storageTruncatedValue,
		&metadataVersionValue,
		&auditEventIDValue,
		&exitCodeValue,
		&failedValue,
	); err != nil {
		return apptypes.EventMetadata{}, xerrors.Errorf("failed to scan event metadata row: %w", err)
	}

	eventID, err := types.EventIDFrom(eventIDValue)
	if err != nil {
		return apptypes.EventMetadata{}, xerrors.Errorf("failed to restore metadata event ID: %w", err)
	}
	eventKind, err := types.EventKindFrom(eventKindValue)
	if err != nil {
		return apptypes.EventMetadata{}, xerrors.Errorf("failed to restore metadata event kind: %w", err)
	}
	agent, err := types.AgentFrom(agentValue)
	if err != nil {
		return apptypes.EventMetadata{}, xerrors.Errorf("failed to restore metadata agent: %w", err)
	}
	sessionID, err := types.SessionIDFrom(sessionIDValue)
	if err != nil {
		return apptypes.EventMetadata{}, xerrors.Errorf("failed to restore metadata session ID: %w", err)
	}
	createdAt, err := time.Parse(time.RFC3339Nano, createdAtValue)
	if err != nil {
		return apptypes.EventMetadata{}, xerrors.Errorf("failed to restore metadata created_at: %w", err)
	}
	if !storedBytesValue.Valid {
		return apptypes.EventMetadata{}, xerrors.Errorf("stored body bytes are missing for event %s", eventID)
	}
	storedBytes, err := checkedInt(storedBytesValue.Int64, "stored body bytes")
	if err != nil {
		return apptypes.EventMetadata{}, err
	}

	originalBytes, err := optionalInt(originalBytesValue, "original body bytes")
	if err != nil {
		return apptypes.EventMetadata{}, err
	}
	metadataVersion, err := optionalInt(metadataVersionValue, "body metadata version")
	if err != nil {
		return apptypes.EventMetadata{}, err
	}
	bodyExtent, err := apptypes.EventBodyExtentOf(
		originalBytes,
		storedBytes,
		optionalBool(ingestTruncatedValue),
		optionalBool(storageTruncatedValue),
		metadataVersion,
	)
	if err != nil {
		return apptypes.EventMetadata{}, xerrors.Errorf("failed to restore event body extent: %w", err)
	}

	commandAudit := types.None[apptypes.CommandAuditMetadata]()
	if auditEventIDValue.Valid {
		if !failedValue.Valid {
			return apptypes.EventMetadata{}, xerrors.Errorf("failed flag is missing for command audit %s", auditEventIDValue.String)
		}
		exitCode, err := optionalInt(exitCodeValue, "command exit code")
		if err != nil {
			return apptypes.EventMetadata{}, err
		}
		commandAudit = types.Some(apptypes.CommandAuditMetadataOf(exitCode, failedValue.Bool))
	}

	metadata, err := apptypes.EventMetadataOf(
		eventID,
		eventKind,
		types.Client(clientValue),
		agent,
		sessionID,
		types.Workspace(workspaceValue),
		sourceHookValue.String,
		createdAt,
		bodyExtent,
		commandAudit,
	)
	if err != nil {
		return apptypes.EventMetadata{}, xerrors.Errorf("failed to build event metadata: %w", err)
	}
	return metadata, nil
}

func optionalInt(value sql.NullInt64, field string) (types.Optional[int], error) {
	if !value.Valid {
		return types.None[int](), nil
	}
	converted, err := checkedInt(value.Int64, field)
	if err != nil {
		return types.None[int](), err
	}
	return types.Some(converted), nil
}

func optionalBool(value sql.NullBool) types.Optional[bool] {
	if !value.Valid {
		return types.None[bool]()
	}
	return types.Some(value.Bool)
}

func checkedInt(value int64, field string) (int, error) {
	converted := int(value)
	if int64(converted) != value {
		return 0, xerrors.Errorf("%s exceeds platform integer range", field)
	}
	return converted, nil
}

func queryRecentEventMetadata(
	ctx context.Context,
	db *sql.DB,
	criteria apptypes.EventListCriteria,
	fromValue, toValue string,
	limit, offset int,
) (*sql.Rows, error) {
	return queryRecentEventMetadataWith(
		ctx,
		db.QueryContext,
		criteria,
		fromValue,
		toValue,
		limit,
		offset,
	)
}

func queryRecentEventMetadataTx(
	ctx context.Context,
	tx *sql.Tx,
	criteria apptypes.EventListCriteria,
	fromValue, toValue string,
	limit, offset int,
) (*sql.Rows, error) {
	return queryRecentEventMetadataWith(
		ctx,
		tx.QueryContext,
		criteria,
		fromValue,
		toValue,
		limit,
		offset,
	)
}

type metadataQueryContext func(context.Context, string, ...any) (*sql.Rows, error)

func queryRecentEventMetadataWith(
	ctx context.Context,
	query metadataQueryContext,
	criteria apptypes.EventListCriteria,
	fromValue, toValue string,
	limit, offset int,
) (*sql.Rows, error) {
	sourceHook := criteria.SourceHook()
	failuresFlag := boolToInt(criteria.FailuresOnly())
	if sourceHook == "" {
		rows, err := query(
			ctx,
			selectRecentEventMetadataQuery,
			criteria.Kind().String(), criteria.Kind().String(),
			criteria.Client().String(), criteria.Client().String(),
			criteria.Agent().String(), criteria.Agent().String(),
			criteria.SessionID().String(), criteria.SessionID().String(),
			criteria.Workspace().String(), criteria.Workspace().String(),
			failuresFlag,
			fromValue, fromValue,
			toValue, toValue,
			limit,
			offset,
		)
		if err != nil {
			return nil, xerrors.Errorf("query recent event metadata: %w", err)
		}
		return rows, nil
	}
	if sourceHookHasLegacyPrefix(sourceHook) {
		rows, err := query(
			ctx,
			selectRecentEventMetadataBySourceHookWithLegacyQuery,
			sourceHookLegacyQueryArgs(
				sourceHook,
				criteria.Kind(),
				criteria.Client(),
				criteria.Agent(),
				criteria.SessionID(),
				criteria.Workspace(),
				failuresFlag,
				fromValue,
				toValue,
				limit,
				offset,
			)...,
		)
		if err != nil {
			return nil, xerrors.Errorf("query recent event metadata by source hook with legacy: %w", err)
		}
		return rows, nil
	}
	rows, err := query(
		ctx,
		selectRecentEventMetadataBySourceHookQuery,
		sourceHookPrimaryQueryArgs(
			sourceHook,
			criteria.Kind(),
			criteria.Client(),
			criteria.Agent(),
			criteria.SessionID(),
			criteria.Workspace(),
			failuresFlag,
			fromValue,
			toValue,
			limit,
			offset,
		)...,
	)
	if err != nil {
		return nil, xerrors.Errorf("query recent event metadata by source hook: %w", err)
	}
	return rows, nil
}
