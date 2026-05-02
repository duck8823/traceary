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
	"github.com/duck8823/traceary/domain/model"
	"github.com/duck8823/traceary/domain/types"
)

//go:embed sql/upsert_memory.sql
var upsertMemoryQuery string

//go:embed sql/delete_memory_evidence_refs.sql
var deleteMemoryEvidenceRefsQuery string

//go:embed sql/delete_memory_artifact_refs.sql
var deleteMemoryArtifactRefsQuery string

//go:embed sql/insert_memory_evidence_ref.sql
var insertMemoryEvidenceRefQuery string

//go:embed sql/insert_memory_artifact_ref.sql
var insertMemoryArtifactRefQuery string

//go:embed sql/select_memory_row_by_id.sql
var selectMemoryRowByIDQuery string

//go:embed sql/select_memory_evidence_refs.sql
var selectMemoryEvidenceRefsQuery string

//go:embed sql/select_memory_artifact_refs.sql
var selectMemoryArtifactRefsQuery string

const selectMemorySummaryColumnsQuery = `
SELECT
    m.id,
    m.type,
    m.scope_kind,
    m.scope_value,
    m.fact,
    m.status,
    m.confidence,
    m.source,
    m.supersedes_memory_id,
    m.expires_at,
    m.valid_from,
    m.valid_to,
    m.created_at,
    m.updated_at
FROM memories m
WHERE 1 = 1`

// MemoryDatasource is the SQLite-backed implementation of the memory
// repository and memory query service.
type MemoryDatasource struct {
	db    *Database
	clock types.Clock
}

// NewMemoryDatasource creates a new MemoryDatasource bound to the given database.
func NewMemoryDatasource(db *Database) *MemoryDatasource {
	return NewMemoryDatasourceWithClock(db, types.SystemClock{})
}

// NewMemoryDatasourceWithClock creates a new MemoryDatasource using the provided clock.
func NewMemoryDatasourceWithClock(db *Database, clock types.Clock) *MemoryDatasource {
	if clock == nil {
		clock = types.SystemClock{}
	}
	return &MemoryDatasource{db: db, clock: clock}
}

var (
	_ model.MemoryRepository          = (*MemoryDatasource)(nil)
	_ queryservice.MemoryQueryService = (*MemoryDatasource)(nil)
)

// Save persists a memory aggregate together with its refs.
func (d *MemoryDatasource) Save(ctx context.Context, memory *model.Memory) error {
	if memory == nil {
		return xerrors.Errorf("memory must not be nil")
	}

	return d.runMemoryWriteTx(ctx, func(tx *sql.Tx) error {
		if err := persistMemoryTx(ctx, tx, memory); err != nil {
			return xerrors.Errorf("failed to persist memory: %w", err)
		}
		return nil
	})
}

// SaveSupersession persists a superseded memory state and its replacement in a
// single transaction.
func (d *MemoryDatasource) SaveSupersession(ctx context.Context, superseded *model.Memory, replacement *model.Memory) error {
	if superseded == nil {
		return xerrors.Errorf("superseded memory must not be nil")
	}
	if replacement == nil {
		return xerrors.Errorf("replacement memory must not be nil")
	}

	return d.runMemoryWriteTx(ctx, func(tx *sql.Tx) error {
		if err := persistMemoryTx(ctx, tx, superseded); err != nil {
			return xerrors.Errorf("failed to persist superseded memory: %w", err)
		}
		if err := persistMemoryTx(ctx, tx, replacement); err != nil {
			return xerrors.Errorf("failed to persist replacement memory: %w", err)
		}
		return nil
	})
}

func (d *MemoryDatasource) runMemoryWriteTx(ctx context.Context, fn func(tx *sql.Tx) error) error {
	db, err := d.db.open(ctx)
	if err != nil {
		return xerrors.Errorf("failed to open DB for memory save: %w", err)
	}
	defer func() {
		if err := db.Close(); err != nil {
			slog.Debug("failed to close resource", "error", err)
		}
	}()

	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return xerrors.Errorf("failed to begin memory save transaction: %w", err)
	}
	defer func() {
		if err := tx.Rollback(); err != nil && !errors.Is(err, sql.ErrTxDone) {
			slog.Debug("failed to rollback transaction", "error", err)
		}
	}()

	if err := fn(tx); err != nil {
		return err
	}

	if err := tx.Commit(); err != nil {
		return xerrors.Errorf("failed to commit memory save transaction: %w", err)
	}

	return nil
}

func persistMemoryTx(ctx context.Context, tx *sql.Tx, memory *model.Memory) error {
	var supersedesValue *string
	if supersedes, ok := memory.Supersedes().Value(); ok {
		value := supersedes.String()
		supersedesValue = &value
	}
	var expiresAtValue *string
	if expiresAt, ok := memory.ExpiresAt().Value(); ok {
		formatted := formatTimestamp(expiresAt)
		expiresAtValue = &formatted
	}
	validFromValue := formatMemoryValidityTimestamp(memory.ValidFrom())
	var validToValue *string
	if validTo, ok := memory.ValidTo().Value(); ok {
		formatted := formatMemoryValidityTimestamp(validTo)
		validToValue = &formatted
	}

	if _, err := tx.ExecContext(
		ctx,
		upsertMemoryQuery,
		memory.MemoryID().String(),
		memory.MemoryType().String(),
		memory.Scope().Kind().String(),
		memory.Scope().Key(),
		memory.Fact(),
		memory.Status().String(),
		memory.Confidence().String(),
		memory.Source().String(),
		supersedesValue,
		expiresAtValue,
		validFromValue,
		validToValue,
		formatTimestamp(memory.CreatedAt()),
		formatTimestamp(memory.UpdatedAt()),
	); err != nil {
		return xerrors.Errorf("failed to upsert memory: %w", err)
	}

	if _, err := tx.ExecContext(ctx, deleteMemoryEvidenceRefsQuery, memory.MemoryID().String()); err != nil {
		return xerrors.Errorf("failed to delete existing memory evidence refs: %w", err)
	}
	if _, err := tx.ExecContext(ctx, deleteMemoryArtifactRefsQuery, memory.MemoryID().String()); err != nil {
		return xerrors.Errorf("failed to delete existing memory artifact refs: %w", err)
	}

	for index, evidenceRef := range memory.EvidenceRefs() {
		if _, err := tx.ExecContext(
			ctx,
			insertMemoryEvidenceRefQuery,
			memory.MemoryID().String(),
			index,
			evidenceRef.Kind().String(),
			evidenceRef.Value(),
		); err != nil {
			return xerrors.Errorf("failed to insert memory evidence ref: %w", err)
		}
	}

	for index, artifactRef := range memory.ArtifactRefs() {
		if _, err := tx.ExecContext(
			ctx,
			insertMemoryArtifactRefQuery,
			memory.MemoryID().String(),
			index,
			artifactRef.Kind().String(),
			artifactRef.Value(),
		); err != nil {
			return xerrors.Errorf("failed to insert memory artifact ref: %w", err)
		}
	}

	return nil
}

// FindByID returns the memory for the given ID.
func (d *MemoryDatasource) FindByID(ctx context.Context, memoryID types.MemoryID) (types.Optional[*model.Memory], error) {
	db, err := d.db.open(ctx)
	if err != nil {
		return types.None[*model.Memory](), xerrors.Errorf("failed to open DB for memory lookup: %w", err)
	}
	defer func() {
		if err := db.Close(); err != nil {
			slog.Debug("failed to close resource", "error", err)
		}
	}()

	memory, err := findMemoryByID(ctx, db, memoryID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return types.None[*model.Memory](), nil
		}
		return types.None[*model.Memory](), xerrors.Errorf("failed to restore memory: %w", err)
	}

	return types.Some(memory), nil
}

// GetDetails returns the details for a single memory.
func (d *MemoryDatasource) GetDetails(ctx context.Context, memoryID types.MemoryID) (apptypes.MemoryDetails, error) {
	db, err := d.db.open(ctx)
	if err != nil {
		return apptypes.MemoryDetails{}, xerrors.Errorf("failed to open DB for memory details lookup: %w", err)
	}
	defer func() {
		if err := db.Close(); err != nil {
			slog.Debug("failed to close resource", "error", err)
		}
	}()

	memory, err := findMemoryByID(ctx, db, memoryID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return apptypes.MemoryDetails{}, xerrors.Errorf("memory not found: %s", memoryID)
		}
		return apptypes.MemoryDetails{}, xerrors.Errorf("failed to restore memory details: %w", err)
	}

	details, err := apptypes.MemoryDetailsFrom(memory)
	if err != nil {
		return apptypes.MemoryDetails{}, xerrors.Errorf("failed to build memory details: %w", err)
	}

	return details, nil
}

// List returns memory summaries matching the provided criteria.
func (d *MemoryDatasource) List(ctx context.Context, criteria apptypes.MemoryListCriteria) ([]apptypes.MemorySummary, error) {
	if criteria.Limit() <= 0 {
		return nil, xerrors.Errorf("limit must be greater than or equal to 1")
	}
	if criteria.Offset() < 0 {
		return nil, xerrors.Errorf("offset must be greater than or equal to 0")
	}

	db, err := d.db.open(ctx)
	if err != nil {
		return nil, xerrors.Errorf("failed to open DB for memory list: %w", err)
	}
	defer func() {
		if err := db.Close(); err != nil {
			slog.Debug("failed to close resource", "error", err)
		}
	}()

	query, args, err := buildMemoryListQuery(criteria, d.clock)
	if err != nil {
		return nil, xerrors.Errorf("failed to build memory list query: %w", err)
	}

	rows, err := db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, xerrors.Errorf("failed to query memories: %w", err)
	}
	defer func() {
		if err := rows.Close(); err != nil {
			slog.Debug("failed to close resource", "error", err)
		}
	}()

	summaries := make([]apptypes.MemorySummary, 0, criteria.Limit())
	for rows.Next() {
		summary, err := scanMemorySummary(rows)
		if err != nil {
			return nil, xerrors.Errorf("failed to scan memory summary row: %w", err)
		}
		summaries = append(summaries, summary)
	}
	if err := rows.Err(); err != nil {
		return nil, xerrors.Errorf("failed to iterate memory summary rows: %w", err)
	}

	return summaries, nil
}

// Search performs text search across durable memories and their refs.
func (d *MemoryDatasource) Search(ctx context.Context, criteria apptypes.MemorySearchCriteria) ([]apptypes.MemorySummary, error) {
	if criteria.Limit() <= 0 {
		return nil, xerrors.Errorf("limit must be greater than or equal to 1")
	}
	if criteria.Offset() < 0 {
		return nil, xerrors.Errorf("offset must be greater than or equal to 0")
	}

	db, err := d.db.open(ctx)
	if err != nil {
		return nil, xerrors.Errorf("failed to open DB for memory search: %w", err)
	}
	defer func() {
		if err := db.Close(); err != nil {
			slog.Debug("failed to close resource", "error", err)
		}
	}()

	query, args, err := buildMemorySearchQuery(criteria, d.clock)
	if err != nil {
		return nil, xerrors.Errorf("failed to build memory search query: %w", err)
	}

	rows, err := db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, xerrors.Errorf("failed to query memory search results: %w", err)
	}
	defer func() {
		if err := rows.Close(); err != nil {
			slog.Debug("failed to close resource", "error", err)
		}
	}()

	summaries := make([]apptypes.MemorySummary, 0, criteria.Limit())
	for rows.Next() {
		summary, err := scanMemorySummary(rows)
		if err != nil {
			return nil, xerrors.Errorf("failed to scan memory search row: %w", err)
		}
		summaries = append(summaries, summary)
	}
	if err := rows.Err(); err != nil {
		return nil, xerrors.Errorf("failed to iterate memory search rows: %w", err)
	}

	return summaries, nil
}

type memoryRow struct {
	memoryID   string
	memoryType string
	scopeKind  string
	scopeValue string
	fact       string
	status     string
	confidence string
	source     string
	supersedes sql.NullString
	expiresAt  sql.NullString
	validFrom  sql.NullString
	validTo    sql.NullString
	createdAt  string
	updatedAt  string
}

func scanMemoryRow(rowScanner interface {
	Scan(dest ...any) error
}) (memoryRow, error) {
	var row memoryRow
	if err := rowScanner.Scan(
		&row.memoryID,
		&row.memoryType,
		&row.scopeKind,
		&row.scopeValue,
		&row.fact,
		&row.status,
		&row.confidence,
		&row.source,
		&row.supersedes,
		&row.expiresAt,
		&row.validFrom,
		&row.validTo,
		&row.createdAt,
		&row.updatedAt,
	); err != nil {
		return memoryRow{}, xerrors.Errorf("failed to scan memory row: %w", err)
	}
	return row, nil
}

func scanMemorySummary(rowScanner interface {
	Scan(dest ...any) error
}) (apptypes.MemorySummary, error) {
	row, err := scanMemoryRow(rowScanner)
	if err != nil {
		return apptypes.MemorySummary{}, err
	}
	return restoreMemorySummary(row)
}

func restoreMemorySummary(row memoryRow) (apptypes.MemorySummary, error) {
	memoryID, err := types.MemoryIDFrom(row.memoryID)
	if err != nil {
		return apptypes.MemorySummary{}, xerrors.Errorf("failed to restore memory ID: %w", err)
	}
	memoryType, err := types.MemoryTypeFrom(row.memoryType)
	if err != nil {
		return apptypes.MemorySummary{}, xerrors.Errorf("failed to restore memory type: %w", err)
	}
	scope, err := types.MemoryScopeFrom(row.scopeKind, row.scopeValue)
	if err != nil {
		return apptypes.MemorySummary{}, xerrors.Errorf("failed to restore memory scope: %w", err)
	}
	status, err := types.MemoryStatusFrom(row.status)
	if err != nil {
		return apptypes.MemorySummary{}, xerrors.Errorf("failed to restore memory status: %w", err)
	}
	confidence, err := types.ConfidenceFrom(row.confidence)
	if err != nil {
		return apptypes.MemorySummary{}, xerrors.Errorf("failed to restore memory confidence: %w", err)
	}
	source, err := types.MemorySourceFrom(row.source)
	if err != nil {
		return apptypes.MemorySummary{}, xerrors.Errorf("failed to restore memory source: %w", err)
	}
	supersedes := types.None[types.MemoryID]()
	if row.supersedes.Valid {
		memoryIDValue, err := types.MemoryIDFrom(row.supersedes.String)
		if err != nil {
			return apptypes.MemorySummary{}, xerrors.Errorf("failed to restore superseded memory ID: %w", err)
		}
		supersedes = types.Some(memoryIDValue)
	}
	expiresAt := types.None[time.Time]()
	if row.expiresAt.Valid {
		expiresAtValue, err := time.Parse(time.RFC3339Nano, row.expiresAt.String)
		if err != nil {
			return apptypes.MemorySummary{}, xerrors.Errorf("failed to parse memory expires_at: %w", err)
		}
		expiresAt = types.Some(expiresAtValue)
	}
	validFrom, validTo, err := parseMemoryValidityWindow(row)
	if err != nil {
		return apptypes.MemorySummary{}, err
	}
	createdAt, err := time.Parse(time.RFC3339Nano, row.createdAt)
	if err != nil {
		return apptypes.MemorySummary{}, xerrors.Errorf("failed to parse memory created_at: %w", err)
	}
	updatedAt, err := time.Parse(time.RFC3339Nano, row.updatedAt)
	if err != nil {
		return apptypes.MemorySummary{}, xerrors.Errorf("failed to parse memory updated_at: %w", err)
	}

	summary, err := apptypes.MemorySummaryOf(
		memoryID,
		memoryType,
		scope,
		row.fact,
		status,
		confidence,
		source,
		supersedes,
		expiresAt,
		validFrom,
		validTo,
		createdAt,
		updatedAt,
	)
	if err != nil {
		return apptypes.MemorySummary{}, xerrors.Errorf("failed to build memory summary: %w", err)
	}
	return summary, nil
}

// parseMemoryValidityWindow resolves the validity columns out of a
// scanned row. Post-migration 000009 every row has a non-null
// valid_from back-filled from created_at, but we still accept a
// legacy NULL valid_from by falling back to created_at so the
// invariant "validFrom is never zero" holds even if a user upgrades
// with manually-edited rows.
func parseMemoryValidityWindow(row memoryRow) (time.Time, types.Optional[time.Time], error) {
	createdAt, err := time.Parse(time.RFC3339Nano, row.createdAt)
	if err != nil {
		return time.Time{}, types.None[time.Time](), xerrors.Errorf("failed to parse memory created_at: %w", err)
	}
	validFrom := createdAt
	if row.validFrom.Valid {
		parsed, err := time.Parse(time.RFC3339Nano, row.validFrom.String)
		if err != nil {
			return time.Time{}, types.None[time.Time](), xerrors.Errorf("failed to parse memory valid_from: %w", err)
		}
		validFrom = parsed
	}
	validTo := types.None[time.Time]()
	if row.validTo.Valid {
		parsed, err := time.Parse(time.RFC3339Nano, row.validTo.String)
		if err != nil {
			return time.Time{}, types.None[time.Time](), xerrors.Errorf("failed to parse memory valid_to: %w", err)
		}
		validTo = types.Some(parsed)
	}
	return validFrom, validTo, nil
}

func restoreMemoryAggregate(row memoryRow, evidenceRefs []types.EvidenceRef, artifactRefs []types.ArtifactRef) (*model.Memory, error) {
	summary, err := restoreMemorySummary(row)
	if err != nil {
		return nil, err
	}
	return model.MemoryOf(
		summary.MemoryID(),
		summary.MemoryType(),
		summary.Scope(),
		summary.Fact(),
		summary.Status(),
		summary.Confidence(),
		summary.Source(),
		evidenceRefs,
		artifactRefs,
		summary.Supersedes(),
		summary.ExpiresAt(),
		summary.ValidFrom(),
		summary.ValidTo(),
		summary.CreatedAt(),
		summary.UpdatedAt(),
	), nil
}

func findMemoryByID(ctx context.Context, db *sql.DB, memoryID types.MemoryID) (*model.Memory, error) {
	row := db.QueryRowContext(ctx, selectMemoryRowByIDQuery, memoryID.String())
	memoryRowValue, err := scanMemoryRow(row)
	if err != nil {
		return nil, err
	}

	evidenceRefs, err := loadMemoryEvidenceRefs(ctx, db, memoryID)
	if err != nil {
		return nil, xerrors.Errorf("failed to load memory evidence refs: %w", err)
	}
	artifactRefs, err := loadMemoryArtifactRefs(ctx, db, memoryID)
	if err != nil {
		return nil, xerrors.Errorf("failed to load memory artifact refs: %w", err)
	}

	memory, err := restoreMemoryAggregate(memoryRowValue, evidenceRefs, artifactRefs)
	if err != nil {
		return nil, xerrors.Errorf("failed to restore memory aggregate: %w", err)
	}

	return memory, nil
}

func loadMemoryEvidenceRefs(ctx context.Context, db *sql.DB, memoryID types.MemoryID) ([]types.EvidenceRef, error) {
	rows, err := db.QueryContext(ctx, selectMemoryEvidenceRefsQuery, memoryID.String())
	if err != nil {
		return nil, xerrors.Errorf("failed to query memory evidence refs: %w", err)
	}
	defer func() {
		if err := rows.Close(); err != nil {
			slog.Debug("failed to close resource", "error", err)
		}
	}()

	evidenceRefs := make([]types.EvidenceRef, 0)
	for rows.Next() {
		var kindValue, refValue string
		if err := rows.Scan(&kindValue, &refValue); err != nil {
			return nil, xerrors.Errorf("failed to scan memory evidence ref row: %w", err)
		}
		kind, err := types.EvidenceRefKindFrom(kindValue)
		if err != nil {
			return nil, xerrors.Errorf("failed to restore evidence ref kind: %w", err)
		}
		evidenceRef, err := types.EvidenceRefFrom(kind, refValue)
		if err != nil {
			return nil, xerrors.Errorf("failed to restore evidence ref: %w", err)
		}
		evidenceRefs = append(evidenceRefs, evidenceRef)
	}
	if err := rows.Err(); err != nil {
		return nil, xerrors.Errorf("failed to iterate memory evidence refs: %w", err)
	}

	return evidenceRefs, nil
}

func loadMemoryArtifactRefs(ctx context.Context, db *sql.DB, memoryID types.MemoryID) ([]types.ArtifactRef, error) {
	rows, err := db.QueryContext(ctx, selectMemoryArtifactRefsQuery, memoryID.String())
	if err != nil {
		return nil, xerrors.Errorf("failed to query memory artifact refs: %w", err)
	}
	defer func() {
		if err := rows.Close(); err != nil {
			slog.Debug("failed to close resource", "error", err)
		}
	}()

	artifactRefs := make([]types.ArtifactRef, 0)
	for rows.Next() {
		var kindValue, refValue string
		if err := rows.Scan(&kindValue, &refValue); err != nil {
			return nil, xerrors.Errorf("failed to scan memory artifact ref row: %w", err)
		}
		kind, err := types.ArtifactRefKindFrom(kindValue)
		if err != nil {
			return nil, xerrors.Errorf("failed to restore artifact ref kind: %w", err)
		}
		artifactRef, err := types.ArtifactRefFrom(kind, refValue)
		if err != nil {
			return nil, xerrors.Errorf("failed to restore artifact ref: %w", err)
		}
		artifactRefs = append(artifactRefs, artifactRef)
	}
	if err := rows.Err(); err != nil {
		return nil, xerrors.Errorf("failed to iterate memory artifact refs: %w", err)
	}

	return artifactRefs, nil
}

func buildMemoryListQuery(criteria apptypes.MemoryListCriteria, clock types.Clock) (string, []any, error) {
	var builder strings.Builder
	builder.WriteString(selectMemorySummaryColumnsQuery)
	args, err := appendMemoryFilters(&builder, nil, criteria.Scopes(), criteria.Statuses(), criteria.MemoryTypes(), criteria.Sources())
	if err != nil {
		return "", nil, err
	}
	args = appendMemoryValidityWindowFilter(&builder, args, criteria.AsOf(), criteria.IncludeExpiredByValidity(), clock)
	builder.WriteString(" ORDER BY ")
	if criteria.RememberIntentPriority() {
		// Pin remember-intent rows to the top of the result set BEFORE
		// LIMIT/OFFSET applies so pagination is consistent with the
		// displayed priority. Used by `memory inbox list` (#856).
		builder.WriteString("CASE WHEN m.source = '")
		builder.WriteString(types.MemorySourceRememberIntent.String())
		builder.WriteString("' THEN 0 ELSE 1 END, ")
	}
	builder.WriteString("m.updated_at DESC, m.id DESC LIMIT ? OFFSET ?")
	args = append(args, criteria.Limit(), criteria.Offset())
	return builder.String(), args, nil
}

func buildMemorySearchQuery(criteria apptypes.MemorySearchCriteria, clock types.Clock) (string, []any, error) {
	var builder strings.Builder
	builder.WriteString(selectMemorySummaryColumnsQuery)
	args := make([]any, 0)

	trimmedQuery := strings.TrimSpace(criteria.Query())
	if trimmedQuery != "" {
		likeQuery := "%" + escapeLikeQuery(trimmedQuery) + "%"
		builder.WriteString(`
  AND (
      m.fact LIKE ? ESCAPE '\'
      OR EXISTS (
          SELECT 1
          FROM memory_evidence_refs mer
          WHERE mer.memory_id = m.id
            AND mer.ref_value LIKE ? ESCAPE '\'
      )
      OR EXISTS (
          SELECT 1
          FROM memory_artifact_refs mar
          WHERE mar.memory_id = m.id
            AND mar.ref_value LIKE ? ESCAPE '\'
      )
  )`)
		args = append(args, likeQuery, likeQuery, likeQuery)
	}

	args, err := appendMemoryFilters(&builder, args, criteria.Scopes(), criteria.Statuses(), criteria.MemoryTypes(), criteria.Sources())
	if err != nil {
		return "", nil, err
	}
	args = appendMemoryValidityWindowFilter(&builder, args, criteria.AsOf(), criteria.IncludeExpiredByValidity(), clock)
	builder.WriteString(" ORDER BY m.updated_at DESC, m.id DESC LIMIT ? OFFSET ?")
	args = append(args, criteria.Limit(), criteria.Offset())
	return builder.String(), args, nil
}

// appendMemoryValidityWindowFilter narrows results to memories whose
// content validity window contains the evaluation timestamp, unless
// includeExpired is true. When criteria.AsOf() is None we use the
// current wall clock so callers that do not care about time travel
// still get "still-valid-right-now" semantics by default.
//
// Timestamps are persisted via formatMemoryValidityTimestamp, which
// always emits 9-digit fractional seconds so plain lex TEXT compare
// matches real temporal order. Migration 000010 backfilled pre-v0.8.1
// rows to the same shape, so the filter does not need to wrap
// columns in SQLite's datetime() (which would both truncate sub-
// second precision and force a SCAN instead of using
// idx_memories_valid_window). Backfill of NULL valid_from from
// created_at is handled by migration 000009, so COALESCE is no
// longer required and dropping it keeps the predicate sargable.
func appendMemoryValidityWindowFilter(
	builder *strings.Builder,
	args []any,
	asOf types.Optional[time.Time],
	includeExpired bool,
	clock types.Clock,
) []any {
	if includeExpired {
		return args
	}
	if clock == nil {
		clock = types.SystemClock{}
	}
	evaluationTime := clock.Now()
	if explicit, ok := asOf.Value(); ok {
		evaluationTime = explicit
	}
	formatted := formatMemoryValidityTimestamp(evaluationTime)
	builder.WriteString(" AND m.valid_from <= ?")
	builder.WriteString(" AND (m.valid_to IS NULL OR m.valid_to > ?)")
	return append(args, formatted, formatted)
}

func appendMemoryFilters(
	builder *strings.Builder,
	args []any,
	scopes []types.MemoryScope,
	statuses []types.MemoryStatus,
	memoryTypes []types.MemoryType,
	sources []types.MemorySource,
) ([]any, error) {
	normalizedStatuses := normalizeMemoryStatuses(statuses)
	builder.WriteString(" AND m.status IN (")
	for index, status := range normalizedStatuses {
		if index > 0 {
			builder.WriteString(", ")
		}
		builder.WriteString("?")
		args = append(args, status.String())
	}
	builder.WriteString(")")

	if len(scopes) > 0 {
		builder.WriteString(" AND (")
		for index, scope := range scopes {
			if scope == nil {
				return nil, xerrors.Errorf("memory scope must not be nil")
			}
			if index > 0 {
				builder.WriteString(" OR ")
			}
			builder.WriteString("(m.scope_kind = ? AND m.scope_value = ?)")
			args = append(args, scope.Kind().String(), scope.Key())
		}
		builder.WriteString(")")
	}

	if len(memoryTypes) > 0 {
		builder.WriteString(" AND m.type IN (")
		for index, memoryType := range memoryTypes {
			if index > 0 {
				builder.WriteString(", ")
			}
			builder.WriteString("?")
			args = append(args, memoryType.String())
		}
		builder.WriteString(")")
	}

	if len(sources) > 0 {
		builder.WriteString(" AND m.source IN (")
		for index, source := range sources {
			if index > 0 {
				builder.WriteString(", ")
			}
			builder.WriteString("?")
			args = append(args, source.String())
		}
		builder.WriteString(")")
	}

	return args, nil
}

func normalizeMemoryStatuses(statuses []types.MemoryStatus) []types.MemoryStatus {
	if len(statuses) == 0 {
		return apptypes.DefaultActiveMemoryStatuses()
	}
	return append([]types.MemoryStatus(nil), statuses...)
}
