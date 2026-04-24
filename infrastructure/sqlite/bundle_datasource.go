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

// ImportEvent inserts the event; a unique-constraint violation on
// the event id is treated as "already present, skip" and returns
// (false, nil) so the caller can count skips without treating them
// as errors. This is the idempotency primitive the bundle usecase
// relies on — re-importing the same bundle into the same store is
// a no-op.
func (d *BundleDatasource) ImportEvent(ctx context.Context, event *model.Event) (bool, error) {
	err := d.eventStore.Save(ctx, event)
	if err == nil {
		return true, nil
	}
	if isSQLiteUniqueOrPKConflict(err) {
		return false, nil
	}
	return false, xerrors.Errorf("failed to import event %s: %w", event.EventID(), err)
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
