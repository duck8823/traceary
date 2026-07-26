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

//go:embed sql/select_recent_event_metadata_by_workspace.sql
var selectRecentEventMetadataByWorkspaceQuery string

//go:embed sql/select_recent_event_metadata_by_session.sql
var selectRecentEventMetadataBySessionQuery string

//go:embed sql/select_recent_event_metadata_by_workspace_session.sql
var selectRecentEventMetadataByWorkspaceSessionQuery string

//go:embed sql/select_latest_event_metadata_fast.sql
var selectLatestEventMetadataFastQuery string

//go:embed sql/select_latest_event_metadata_fast_by_workspace.sql
var selectLatestEventMetadataFastByWorkspaceQuery string

//go:embed sql/select_latest_event_timestamp_kind_by_workspace.sql
var selectLatestEventTimestampKindByWorkspaceQuery string

//go:embed sql/select_latest_event_timestamp_kind.sql
var selectLatestEventTimestampKindQuery string

//go:embed sql/select_recent_event_metadata_by_source_hook.sql
var selectRecentEventMetadataBySourceHookQuery string

//go:embed sql/select_recent_event_metadata_by_source_hook_with_legacy.sql
var selectRecentEventMetadataBySourceHookWithLegacyQuery string

//go:embed sql/search_event_metadata.sql
var searchEventMetadataQuery string

//go:embed sql/get_context_event_metadata.sql
var getContextEventMetadataQuery string

//go:embed sql/get_context_event_metadata_by_workspace.sql
var getContextEventMetadataByWorkspaceQuery string

//go:embed sql/get_context_event_metadata_by_session.sql
var getContextEventMetadataBySessionQuery string

//go:embed sql/get_context_event_metadata_by_workspace_session.sql
var getContextEventMetadataByWorkspaceSessionQuery string

var _ queryservice.EventMetadataQueryService = (*EventDatasource)(nil)

// ListRecentTimestampKinds returns the minimal compact-list projection.
func (d *EventDatasource) ListRecentTimestampKinds(ctx context.Context, criteria apptypes.EventListCriteria) ([]apptypes.EventTimestampKind, error) {
	if err := validateMetadataListCriteria(criteria, false); err != nil {
		return nil, err
	}
	// The CLI supplies a snapshot upper bound for an otherwise unbounded list.
	// This projection is invoked only after the presentation layer verified that
	// the operator did not request date filters, so ignore that internal bound.
	bounded, workspace := boundedLatestMetadataWorkspace(criteria, "", "", criteria.Limit(), criteria.Offset())
	if !bounded {
		return nil, xerrors.Errorf("timestamp/kind projection requires a bounded list")
	}
	db, err := d.db.openReadOnly(ctx)
	if err != nil {
		return nil, xerrors.Errorf("failed to open DB for event timestamp/kind listing: %w", err)
	}
	defer closeMetadataResource(db)
	query := selectLatestEventTimestampKindQuery
	args := []any(nil)
	if workspace != "" {
		query = selectLatestEventTimestampKindByWorkspaceQuery
		args = []any{workspace, workspace, workspace}
	}
	rows, err := db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, xerrors.Errorf("query latest event timestamp/kind: %w", err)
	}
	defer func() { _ = rows.Close() }()
	result := make([]apptypes.EventTimestampKind, 0, 1)
	for rows.Next() {
		var kindValue, createdAtValue string
		if err := rows.Scan(&kindValue, &createdAtValue); err != nil {
			return nil, xerrors.Errorf("scan latest event timestamp/kind: %w", err)
		}
		kind, err := types.EventKindFrom(kindValue)
		if err != nil {
			return nil, xerrors.Errorf("restore event kind: %w", err)
		}
		createdAt, err := time.Parse(time.RFC3339Nano, createdAtValue)
		if err != nil {
			return nil, xerrors.Errorf("parse event timestamp: %w", err)
		}
		value, err := apptypes.EventTimestampKindOf(createdAt, kind)
		if err != nil {
			return nil, xerrors.Errorf("build event timestamp/kind: %w", err)
		}
		result = append(result, value)
	}
	if err := rows.Err(); err != nil {
		return nil, xerrors.Errorf("iterate latest event timestamp/kind: %w", err)
	}
	return result, nil
}

// ListRecentMetadata returns body-free event metadata in descending time order.
func (d *EventDatasource) ListRecentMetadata(
	ctx context.Context,
	criteria apptypes.EventListCriteria,
) ([]apptypes.EventMetadata, error) {
	if err := validateMetadataListCriteria(criteria, false); err != nil {
		return nil, err
	}

	db, err := d.db.openReadOnly(ctx)
	if err != nil {
		return nil, xerrors.Errorf("failed to open DB for event metadata listing: %w", err)
	}
	defer closeMetadataResource(db)

	rows, err := queryRecentEventMetadata(
		ctx,
		db,
		criteria,
		formatMetadataOptionalTimestamp(criteria.From()),
		formatMetadataOptionalTimestamp(criteria.To()),
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

	db, err := d.db.openReadOnly(ctx)
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
			formatMetadataOptionalTimestamp(criteria.From()),
			formatMetadataOptionalTimestamp(criteria.To()),
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
	if !criteria.PageAnchor().IsZero() && criteria.Offset() != 0 {
		return nil, xerrors.Errorf("event page anchor cannot be combined with offset")
	}
	if !criteria.From().IsZero() && !criteria.To().IsZero() && criteria.From().After(criteria.To()) {
		return nil, xerrors.Errorf("from must be earlier than to")
	}

	db, err := d.db.openReadOnly(ctx)
	if err != nil {
		return nil, xerrors.Errorf("failed to open DB for event metadata search: %w", err)
	}
	defer closeMetadataResource(db)

	searchSchemaAvailable, err := eventSearchSchemaAvailable(ctx, db)
	if err != nil {
		return nil, err
	}
	if searchSchemaAvailable {
		tx, err := db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
		if err != nil {
			return nil, xerrors.Errorf("failed to begin indexed event metadata search: %w", err)
		}
		defer func() {
			if err := tx.Rollback(); err != nil && !errors.Is(err, sql.ErrTxDone) {
				slog.Debug("failed to rollback indexed event metadata search", "error", err)
			}
		}()
		candidateIDs, err := selectEventSearchCandidateIDs(ctx, tx, criteria)
		if err != nil {
			return nil, err
		}
		metadata, err := hydrateEventSearchMetadataCandidates(ctx, tx, criteria, candidateIDs)
		if err != nil {
			return nil, err
		}
		if err := tx.Commit(); err != nil {
			return nil, xerrors.Errorf("failed to finish indexed event metadata search: %w", err)
		}
		return metadata, nil
	}

	tx, err := db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return nil, xerrors.Errorf("failed to begin legacy event metadata search: %w", err)
	}
	defer func() {
		if err := tx.Rollback(); err != nil && !errors.Is(err, sql.ErrTxDone) {
			slog.Debug("failed to rollback legacy event metadata search", "error", err)
		}
	}()
	candidateIDs, err := queryLegacyEventIDs(ctx, tx, criteria, criteria.Query())
	if err != nil {
		return nil, err
	}
	metadata, err := hydrateEventSearchMetadataCandidates(ctx, tx, criteria, candidateIDs)
	if err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, xerrors.Errorf("failed to finish legacy event metadata search: %w", err)
	}
	return metadata, nil
}

// GetContextMetadata returns body-free context membership in descending time order.
func (d *EventDatasource) GetContextMetadata(
	ctx context.Context,
	criteria apptypes.EventContextCriteria,
) ([]apptypes.EventMetadata, error) {
	if criteria.Limit() <= 0 {
		return nil, xerrors.Errorf("limit must be greater than or equal to 1")
	}
	if criteria.Offset() < 0 {
		return nil, xerrors.Errorf("offset must be greater than or equal to 0")
	}
	if !criteria.PageAnchor().IsZero() && criteria.Offset() != 0 {
		return nil, xerrors.Errorf("event page anchor cannot be combined with offset")
	}

	db, err := d.db.openReadOnly(ctx)
	if err != nil {
		return nil, xerrors.Errorf("failed to open DB for event metadata context: %w", err)
	}
	defer closeMetadataResource(db)

	workspace := strings.TrimSpace(criteria.Workspace().String())
	sessionID := strings.TrimSpace(criteria.SessionID().String())
	query, args := contextEventMetadataQuery(criteria, workspace, sessionID)
	rows, err := db.QueryContext(ctx, query, args...)
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
	if !criteria.PageAnchor().IsZero() && criteria.Offset() != 0 {
		return xerrors.Errorf("event page anchor cannot be combined with offset")
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

// formatMetadataOptionalTimestamp matches events.created_at_norm, which is
// persisted in the fixed-width form used by its ordering indexes. Other event
// queries still normalize created_at inside SQLite and therefore keep the
// variable-width RFC3339Nano parameter format.
func formatMetadataOptionalTimestamp(value time.Time) string {
	if value.IsZero() {
		return ""
	}
	return formatMemoryValidityTimestamp(value)
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
	if boundedLatest, workspace := boundedLatestMetadataWorkspace(criteria, fromValue, toValue, limit, offset); boundedLatest {
		queryText := selectLatestEventMetadataFastQuery
		args := []any(nil)
		if workspace != "" {
			queryText = selectLatestEventMetadataFastByWorkspaceQuery
			args = []any{workspace, workspace, workspace}
		}
		rows, err := query(ctx, queryText, args...)
		if err != nil {
			return nil, xerrors.Errorf("query bounded latest event metadata: %w", err)
		}
		return rows, nil
	}
	if sourceHook == "" {
		queryText, args := scopedRecentEventMetadataQuery(criteria, failuresFlag, fromValue, toValue, limit, offset)
		rows, err := query(
			ctx,
			queryText,
			args...,
		)
		if err != nil {
			return nil, xerrors.Errorf("query recent event metadata: %w", err)
		}
		return rows, nil
	}
	if sourceHookHasLegacyPrefix(sourceHook) {
		queryText := metadataPageQuery(
			metadataTimeRangeQuery(selectRecentEventMetadataBySourceHookWithLegacyQuery, fromValue, toValue),
			criteria.PageAnchor(),
		)
		rows, err := query(
			ctx,
			queryText,
			metadataSourceHookLegacyQueryArgs(
				sourceHook,
				criteria.Kind(),
				criteria.Client(),
				criteria.Agent(),
				criteria.SessionID(),
				criteria.Workspace(),
				failuresFlag,
				fromValue,
				toValue,
				criteria.PageAnchor(),
				limit,
				offset,
			)...,
		)
		if err != nil {
			return nil, xerrors.Errorf("query recent event metadata by source hook with legacy: %w", err)
		}
		return rows, nil
	}
	queryText := metadataPageQuery(
		metadataTimeRangeQuery(selectRecentEventMetadataBySourceHookQuery, fromValue, toValue),
		criteria.PageAnchor(),
	)
	rows, err := query(
		ctx,
		queryText,
		metadataSourceHookPrimaryQueryArgs(
			sourceHook,
			criteria.Kind(),
			criteria.Client(),
			criteria.Agent(),
			criteria.SessionID(),
			criteria.Workspace(),
			failuresFlag,
			fromValue,
			toValue,
			criteria.PageAnchor(),
			limit,
			offset,
		)...,
	)
	if err != nil {
		return nil, xerrors.Errorf("query recent event metadata by source hook: %w", err)
	}
	return rows, nil
}

// scopedRecentEventMetadataQuery selects the most selective stable scope as a
// top-level predicate. The public optional-filter semantics remain unchanged,
// while SQLite can use the matching normalized-timestamp ordering index for a
// bounded metadata page instead of sorting all matching rows.
func scopedRecentEventMetadataQuery(
	criteria apptypes.EventListCriteria,
	failuresFlag int,
	fromValue, toValue string,
	limit, offset int,
) (string, []any) {
	workspace := criteria.Workspace().String()
	sessionID := criteria.SessionID().String()
	common := []any{
		criteria.Kind().String(), criteria.Kind().String(),
		criteria.Client().String(), criteria.Client().String(),
		criteria.Agent().String(), criteria.Agent().String(),
		failuresFlag,
	}
	common = append(common, metadataTimeRangeArgs(fromValue, toValue)...)
	common = append(common, metadataPageAnchorArgs(criteria.PageAnchor())...)
	common = append(common, metadataLimitOffsetArgs(criteria.PageAnchor(), limit, offset)...)
	switch {
	case workspace != "" && sessionID != "":
		return metadataPageQuery(metadataTimeRangeQuery(selectRecentEventMetadataByWorkspaceSessionQuery, fromValue, toValue), criteria.PageAnchor()), append([]any{workspace, sessionID}, common...)
	case workspace != "":
		// The workspace query retains session_id as an optional filter.
		args := []any{workspace,
			criteria.Kind().String(), criteria.Kind().String(),
			criteria.Client().String(), criteria.Client().String(),
			criteria.Agent().String(), criteria.Agent().String(),
			sessionID, sessionID,
			failuresFlag,
		}
		args = append(args, metadataTimeRangeArgs(fromValue, toValue)...)
		args = append(args, metadataPageAnchorArgs(criteria.PageAnchor())...)
		args = append(args, metadataLimitOffsetArgs(criteria.PageAnchor(), limit, offset)...)
		return metadataPageQuery(metadataTimeRangeQuery(selectRecentEventMetadataByWorkspaceQuery, fromValue, toValue), criteria.PageAnchor()), args
	case sessionID != "":
		args := []any{sessionID,
			criteria.Kind().String(), criteria.Kind().String(),
			criteria.Client().String(), criteria.Client().String(),
			criteria.Agent().String(), criteria.Agent().String(),
			workspace, workspace,
			failuresFlag,
		}
		args = append(args, metadataTimeRangeArgs(fromValue, toValue)...)
		args = append(args, metadataPageAnchorArgs(criteria.PageAnchor())...)
		args = append(args, metadataLimitOffsetArgs(criteria.PageAnchor(), limit, offset)...)
		return metadataPageQuery(metadataTimeRangeQuery(selectRecentEventMetadataBySessionQuery, fromValue, toValue), criteria.PageAnchor()), args
	default:
		args := []any{
			criteria.Kind().String(), criteria.Kind().String(),
			criteria.Client().String(), criteria.Client().String(),
			criteria.Agent().String(), criteria.Agent().String(),
			"", "", "", "",
			failuresFlag,
		}
		args = append(args, metadataTimeRangeArgs(fromValue, toValue)...)
		args = append(args, metadataPageAnchorArgs(criteria.PageAnchor())...)
		args = append(args, metadataLimitOffsetArgs(criteria.PageAnchor(), limit, offset)...)
		return metadataPageQuery(metadataTimeRangeQuery(selectRecentEventMetadataQuery, fromValue, toValue), criteria.PageAnchor()), args
	}
}

const (
	metadataOptionalFromPredicate = "AND (? = '' OR e.created_at_norm >= ?)"
	metadataOptionalToPredicate   = "AND (? = '' OR e.created_at_norm < ?)"
)

// metadataTimeRangeQuery selects a SQL variant with direct range predicates
// whenever a boundary is supplied. Direct predicates let SQLite seek within
// the persisted timestamp indexes instead of scanning an ordered index and
// evaluating optional range expressions row by row.
func metadataTimeRangeQuery(query, fromValue, toValue string) string {
	if fromValue != "" {
		query = strings.Replace(query, metadataOptionalFromPredicate, "AND e.created_at_norm >= ?", 1)
	}
	if toValue != "" {
		query = strings.Replace(query, metadataOptionalToPredicate, "AND e.created_at_norm < ?", 1)
	}
	return query
}

func metadataTimeRangeArgs(fromValue, toValue string) []any {
	args := make([]any, 0, 4)
	if fromValue == "" {
		args = append(args, "", "")
	} else {
		args = append(args, fromValue)
	}
	if toValue == "" {
		args = append(args, "", "")
	} else {
		args = append(args, toValue)
	}
	return args
}

func metadataPageAnchorArgs(anchor apptypes.EventPageAnchor) []any {
	if anchor.IsZero() {
		return []any{"", "", "", ""}
	}
	createdAt := formatMetadataOptionalTimestamp(anchor.CreatedAt())
	return []any{createdAt, createdAt, createdAt, anchor.EventID().String()}
}

func metadataPageQuery(query string, anchor apptypes.EventPageAnchor) string {
	if anchor.IsZero() {
		return query
	}
	return strings.Replace(query, "LIMIT ? OFFSET ?", "LIMIT ?", 1)
}

func metadataLimitOffsetArgs(
	anchor apptypes.EventPageAnchor,
	limit, offset int,
) []any {
	if anchor.IsZero() {
		return []any{limit, offset}
	}
	return []any{limit}
}

func metadataSourceHookPrimaryQueryArgs(
	sourceHook string,
	kind types.EventKind, client types.Client, agent types.Agent, sessionID types.SessionID, workspace types.Workspace,
	failuresFlag int,
	fromValue, toValue string,
	pageAnchor apptypes.EventPageAnchor,
	limit, offset int,
) []any {
	args := []any{sourceHook, kind.String(), kind.String(), client.String(), client.String(), agent.String(), agent.String(), sessionID.String(), sessionID.String(), workspace.String(), workspace.String(), failuresFlag}
	args = append(args, metadataTimeRangeArgs(fromValue, toValue)...)
	args = append(args, metadataPageAnchorArgs(pageAnchor)...)
	return append(args, metadataLimitOffsetArgs(pageAnchor, limit, offset)...)
}

func metadataSourceHookLegacyQueryArgs(
	sourceHook string,
	kind types.EventKind, client types.Client, agent types.Agent, sessionID types.SessionID, workspace types.Workspace,
	failuresFlag int,
	fromValue, toValue string,
	pageAnchor apptypes.EventPageAnchor,
	limit, offset int,
) []any {
	args := []any{sourceHook, sourceHook, sourceHook, kind.String(), kind.String(), client.String(), client.String(), agent.String(), agent.String(), sessionID.String(), sessionID.String(), workspace.String(), workspace.String(), failuresFlag}
	args = append(args, metadataTimeRangeArgs(fromValue, toValue)...)
	args = append(args, metadataPageAnchorArgs(pageAnchor)...)
	return append(args, metadataLimitOffsetArgs(pageAnchor, limit, offset)...)
}

func contextEventMetadataQuery(
	criteria apptypes.EventContextCriteria,
	workspace, sessionID string,
) (string, []any) {
	toValue := formatMetadataOptionalTimestamp(criteria.To())
	common := []any{toValue, toValue}
	common = append(common, metadataPageAnchorArgs(criteria.PageAnchor())...)
	common = append(common, metadataLimitOffsetArgs(criteria.PageAnchor(), criteria.Limit(), criteria.Offset())...)
	query := ""
	args := []any(nil)
	switch {
	case workspace != "" && sessionID != "":
		query = getContextEventMetadataByWorkspaceSessionQuery
		args = append([]any{workspace, sessionID}, common...)
	case workspace != "":
		query = getContextEventMetadataByWorkspaceQuery
		args = append([]any{workspace}, common...)
	case sessionID != "":
		query = getContextEventMetadataBySessionQuery
		args = append([]any{sessionID}, common...)
	default:
		query = getContextEventMetadataQuery
		args = append([]any{"", "", "", ""}, common...)
	}
	return metadataPageQuery(query, criteria.PageAnchor()), args
}

// boundedLatestMetadataWorkspace identifies the single-row, body-free CLI
// inspection intent that can use the existing created_at index without a
// store-wide timestamp normalization sort. The normal CLI resolves a current
// repository to Workspace, so that one implicit filter belongs to the bounded
// intent. Other filters retain the general, boundary-complete query path.
func boundedLatestMetadataWorkspace(criteria apptypes.EventListCriteria, fromValue, toValue string, limit, offset int) (bool, string) {
	bounded := limit == 1 && offset == 0 &&
		criteria.Kind() == "" &&
		criteria.Client() == "" &&
		criteria.Agent() == "" &&
		criteria.SessionID() == "" &&
		!criteria.FailuresOnly() &&
		criteria.SourceHook() == "" &&
		criteria.PageAnchor().IsZero() &&
		fromValue == "" && toValue == ""
	return bounded, criteria.Workspace().String()
}
