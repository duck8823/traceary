package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"log/slog"

	"golang.org/x/xerrors"

	sqlitelib "modernc.org/sqlite"

	"github.com/duck8823/traceary/application/usecase"
	"github.com/duck8823/traceary/domain/model"
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
