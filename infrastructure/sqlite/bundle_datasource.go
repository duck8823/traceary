package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"log/slog"
	"time"

	"golang.org/x/xerrors"

	sqlitelib "modernc.org/sqlite"

	apptypes "github.com/duck8823/traceary/application/types"
	"github.com/duck8823/traceary/application/usecase"
	"github.com/duck8823/traceary/domain/model"
	"github.com/duck8823/traceary/domain/types"
)

// SQLite constraint codes emitted by modernc.org/sqlite. See
// sqlite3.h — SQLITE_CONSTRAINT_PRIMARYKEY (1555) and
// SQLITE_CONSTRAINT_UNIQUE (2067). We match both so the idempotency
// check survives a future schema that promotes a UNIQUE index to
// PRIMARY KEY (or vice versa).
const (
	sqliteCodePrimaryKeyConflict = 1555
	sqliteCodeUniqueConflict     = 2067
)

// BundleDatasource implements usecase.BundleEventRepository with the
// SQLite-backed Traceary store. Kept as a thin adapter on top of
// EventDatasource + the schema_migrations table so the bundle
// usecase stays infrastructure-agnostic.
type BundleDatasource struct {
	db         *Database
	eventStore *EventDatasource
}

// NewBundleDatasource constructs a BundleDatasource.
func NewBundleDatasource(db *Database, eventStore *EventDatasource) *BundleDatasource {
	return &BundleDatasource{db: db, eventStore: eventStore}
}

var _ usecase.BundleEventRepository = (*BundleDatasource)(nil)

// SchemaVersion returns the max version recorded in
// schema_migrations. 0 means "no migrations have been applied".
func (d *BundleDatasource) SchemaVersion(ctx context.Context) (int, error) {
	db, err := d.db.open(ctx)
	if err != nil {
		return 0, xerrors.Errorf("failed to open DB for schema version lookup: %w", err)
	}
	defer func() {
		if err := db.Close(); err != nil {
			slog.Debug("failed to close resource", "error", err)
		}
	}()
	var version int
	err = db.QueryRowContext(ctx, `SELECT COALESCE(MAX(version), 0) FROM schema_migrations`).Scan(&version)
	if err != nil {
		return 0, xerrors.Errorf("failed to read schema_migrations: %w", err)
	}
	return version, nil
}

// ListBundleMemories returns every durable memory with refs for bundle export.
func (d *BundleDatasource) ListBundleMemories(ctx context.Context) ([]apptypes.MemoryDetails, error) {
	db, err := d.db.open(ctx)
	if err != nil {
		return nil, xerrors.Errorf("failed to open DB for bundle memory export: %w", err)
	}
	defer func() {
		if err := db.Close(); err != nil {
			slog.Debug("failed to close resource", "error", err)
		}
	}()
	rows, err := db.QueryContext(ctx, selectMemorySummaryColumnsQuery+`
ORDER BY
  CASE WHEN m.supersedes_memory_id IS NULL THEN 0 ELSE 1 END,
  m.supersedes_memory_id,
  m.id`)
	if err != nil {
		return nil, xerrors.Errorf("failed to query memories for bundle export: %w", err)
	}
	defer func() {
		if err := rows.Close(); err != nil {
			slog.Debug("failed to close resource", "error", err)
		}
	}()
	out := []apptypes.MemoryDetails{}
	for rows.Next() {
		summary, err := scanMemorySummary(rows)
		if err != nil {
			return nil, xerrors.Errorf("failed to scan bundle memory row: %w", err)
		}
		memory, err := findMemoryByID(ctx, db, summary.MemoryID())
		if err != nil {
			return nil, xerrors.Errorf("failed to load memory refs for %s: %w", summary.MemoryID(), err)
		}
		details, err := apptypes.MemoryDetailsFrom(memory)
		if err != nil {
			return nil, xerrors.Errorf("failed to build memory details for %s: %w", summary.MemoryID(), err)
		}
		out = append(out, details)
	}
	if err := rows.Err(); err != nil {
		return nil, xerrors.Errorf("failed to iterate bundle memory rows: %w", err)
	}
	return out, nil
}

// ListBundleMemoryEdges returns every memory graph edge for bundle export.
func (d *BundleDatasource) ListBundleMemoryEdges(ctx context.Context) ([]*model.MemoryEdge, error) {
	db, err := d.db.open(ctx)
	if err != nil {
		return nil, xerrors.Errorf("failed to open DB for bundle memory edge export: %w", err)
	}
	defer func() {
		if err := db.Close(); err != nil {
			slog.Debug("failed to close resource", "error", err)
		}
	}()
	rows, err := db.QueryContext(ctx, `
SELECT id, from_memory_id, to_memory_id, relation_type, valid_from, valid_to, created_at
  FROM memory_edges
 ORDER BY valid_from, id`)
	if err != nil {
		return nil, xerrors.Errorf("failed to query memory edges for bundle export: %w", err)
	}
	defer func() {
		if err := rows.Close(); err != nil {
			slog.Debug("failed to close resource", "error", err)
		}
	}()
	edges := []*model.MemoryEdge{}
	for rows.Next() {
		edge, err := scanMemoryEdge(rows)
		if err != nil {
			return nil, xerrors.Errorf("failed to scan bundle memory edge row: %w", err)
		}
		edges = append(edges, edge)
	}
	if err := rows.Err(); err != nil {
		return nil, xerrors.Errorf("failed to iterate bundle memory edge rows: %w", err)
	}
	return edges, nil
}

// BeginBundleImport starts the transaction shared by every table
// importer in a bundle. v2 only registers events, but sessions /
// memories / edges can join this transaction in follow-up issues.
func (d *BundleDatasource) BeginBundleImport(ctx context.Context) (usecase.BundleImportTransaction, error) {
	db, err := d.db.open(ctx)
	if err != nil {
		return nil, xerrors.Errorf("failed to open DB for bundle import: %w", err)
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		if closeErr := db.Close(); closeErr != nil {
			slog.Debug("failed to close resource", "error", closeErr)
		}
		return nil, xerrors.Errorf("failed to begin bundle import transaction: %w", err)
	}
	return &bundleImportTx{db: db, tx: tx}, nil
}

type bundleImportTx struct {
	db *sql.DB
	tx *sql.Tx
}

// ImportEvent inserts or replaces the event according to policy. For
// skip, a unique-constraint violation on the event id returns
// (false, nil) so re-importing the same bundle remains idempotent and
// surfaces events_skipped. For error, the same collision is returned as
// a failure so the whole bundle transaction rolls back.
func (t *bundleImportTx) ImportEvent(ctx context.Context, event *model.Event, policy usecase.BundleConflictPolicy) (bool, error) {
	if event == nil {
		return false, xerrors.Errorf("event must not be nil")
	}
	query := insertEventQuery
	if policy == usecase.BundleConflictReplace {
		query = `INSERT INTO events(id, kind, client, agent, session_id, workspace, body, created_at, source_hook)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT(id) DO UPDATE SET
  kind = excluded.kind,
  client = excluded.client,
  agent = excluded.agent,
  session_id = excluded.session_id,
  workspace = excluded.workspace,
  body = excluded.body,
  created_at = excluded.created_at,
  source_hook = excluded.source_hook`
	}
	_, err := t.tx.ExecContext(
		ctx,
		query,
		event.EventID().String(),
		event.Kind().String(),
		event.Client().String(),
		event.Agent().String(),
		event.SessionID().String(),
		event.Workspace().String(),
		event.Body(),
		formatTimestamp(event.CreatedAt()),
		nullableString(event.SourceHook()),
	)
	if err == nil {
		return true, nil
	}
	if policy == usecase.BundleConflictSkip && isSQLiteUniqueOrPKConflict(err) {
		return false, nil
	}
	return false, xerrors.Errorf("failed to import event %s: %w", event.EventID(), err)
}

// ImportMemory inserts or replaces a durable memory according to policy.
func (t *bundleImportTx) ImportMemory(ctx context.Context, memory *model.Memory, policy usecase.BundleConflictPolicy) (bool, error) {
	if memory == nil {
		return false, xerrors.Errorf("memory must not be nil")
	}
	exists, err := t.MemoryExists(ctx, memory.MemoryID())
	if err != nil {
		return false, err
	}
	if exists {
		switch policy {
		case usecase.BundleConflictSkip:
			return false, nil
		case usecase.BundleConflictError:
			return false, xerrors.Errorf("memory conflict")
		}
	}
	if err := persistMemoryTx(ctx, t.tx, memory); err != nil {
		return false, xerrors.Errorf("failed to import memory %s: %w", memory.MemoryID(), err)
	}
	return true, nil
}

// ImportMemoryEdge inserts or replaces a memory graph edge according to policy.
func (t *bundleImportTx) ImportMemoryEdge(ctx context.Context, edge *model.MemoryEdge, policy usecase.BundleConflictPolicy) (bool, error) {
	if edge == nil {
		return false, xerrors.Errorf("memory edge must not be nil")
	}
	validToValue := nullableString("")
	if to, ok := edge.ValidTo().Value(); ok {
		validToValue = nullableString(formatMemoryValidityTimestamp(to))
	}
	query := insertMemoryEdgeQuery
	if policy == usecase.BundleConflictReplace {
		query = `INSERT INTO memory_edges (id, from_memory_id, to_memory_id, relation_type, valid_from, valid_to, created_at)
VALUES (?, ?, ?, ?, ?, ?, ?)
ON CONFLICT(id) DO UPDATE SET
  from_memory_id = excluded.from_memory_id,
  to_memory_id = excluded.to_memory_id,
  relation_type = excluded.relation_type,
  valid_from = excluded.valid_from,
  valid_to = excluded.valid_to,
  created_at = excluded.created_at`
	}
	_, err := t.tx.ExecContext(
		ctx,
		query,
		edge.EdgeID().String(),
		edge.FromMemoryID().String(),
		edge.ToMemoryID().String(),
		edge.RelationType().String(),
		formatMemoryValidityTimestamp(edge.ValidFrom()),
		validToValue,
		edge.CreatedAt().UTC().Format(time.RFC3339Nano),
	)
	if err == nil {
		return true, nil
	}
	if policy == usecase.BundleConflictSkip && isSQLiteUniqueOrPKConflict(err) {
		return false, nil
	}
	return false, xerrors.Errorf("failed to import memory edge %s: %w", edge.EdgeID(), err)
}

func (t *bundleImportTx) MemoryExists(ctx context.Context, memoryID types.MemoryID) (bool, error) {
	var value int
	err := t.tx.QueryRowContext(ctx, `SELECT 1 FROM memories WHERE id = ?`, memoryID.String()).Scan(&value)
	if err == nil {
		return true, nil
	}
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	return false, xerrors.Errorf("failed to check memory conflict %s: %w", memoryID, err)
}

func (t *bundleImportTx) MemoryEdgeExists(ctx context.Context, edgeID types.MemoryEdgeID) (bool, error) {
	var value int
	err := t.tx.QueryRowContext(ctx, `SELECT 1 FROM memory_edges WHERE id = ?`, edgeID.String()).Scan(&value)
	if err == nil {
		return true, nil
	}
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	return false, xerrors.Errorf("failed to check memory edge conflict %s: %w", edgeID, err)
}

func (t *bundleImportTx) Commit(context.Context) error {
	if err := t.tx.Commit(); err != nil {
		_ = t.db.Close()
		return xerrors.Errorf("failed to commit bundle import transaction: %w", err)
	}
	if err := t.db.Close(); err != nil {
		return xerrors.Errorf("failed to close DB after bundle import: %w", err)
	}
	return nil
}

func (t *bundleImportTx) Rollback(context.Context) error {
	rollbackErr := t.tx.Rollback()
	closeErr := t.db.Close()
	if rollbackErr != nil && !errors.Is(rollbackErr, sql.ErrTxDone) {
		return xerrors.Errorf("failed to rollback bundle import transaction: %w", rollbackErr)
	}
	if closeErr != nil {
		return xerrors.Errorf("failed to close DB after bundle import rollback: %w", closeErr)
	}
	return nil
}

// isSQLiteUniqueOrPKConflict reports whether err is a
// modernc.org/sqlite typed error whose Code() identifies a
// constraint violation the bundle usecase should treat as
// "duplicate, skip". We match on the typed error's Code() rather
// than the Error() message so a future driver upgrade that changes
// the text cannot silently flip the behaviour from "skip" to
// "fail".
func isSQLiteUniqueOrPKConflict(err error) bool {
	if err == nil {
		return false
	}
	var sqliteErr *sqlitelib.Error
	if !errors.As(err, &sqliteErr) {
		return false
	}
	code := sqliteErr.Code()
	return code == sqliteCodePrimaryKeyConflict || code == sqliteCodeUniqueConflict
}

// ensure sql import stays referenced; datasource uses it indirectly
// through Database.open().
var _ = sql.ErrNoRows
