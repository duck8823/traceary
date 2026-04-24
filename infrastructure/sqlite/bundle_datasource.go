package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"log/slog"
	"strings"
	"time"

	"golang.org/x/xerrors"

	"github.com/duck8823/traceary/application/usecase"
	"github.com/duck8823/traceary/domain/model"
)

// BundleDatasource implements usecase.BundleEventRepository with the
// SQLite-backed Traceary store. Kept as a thin adapter on top of
// EventDatasource + the schema_migrations table so the bundle
// usecase stays infrastructure-agnostic.
type BundleDatasource struct {
	db          *Database
	eventStore  *EventDatasource
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
	if isSQLitePrimaryKeyConflict(err) {
		return false, nil
	}
	return false, xerrors.Errorf("failed to import event %s: %w", event.EventID(), err)
}

// isSQLitePrimaryKeyConflict reports whether err is the
// modernc.org/sqlite constraint error that fires when an event_id
// UNIQUE clash trips on re-import. The driver does not expose typed
// error sentinels, so we string-match the canonical message; the
// test suite in bundle_datasource_test.go pins the behaviour so a
// future driver upgrade that changes the text surfaces as a
// regression.
func isSQLitePrimaryKeyConflict(err error) bool {
	if err == nil {
		return false
	}
	// Unwrap via errors.As where possible; fall back to substring
	// match on the fully-wrapped message.
	msg := err.Error()
	if strings.Contains(msg, "UNIQUE constraint failed: events.id") {
		return true
	}
	if strings.Contains(msg, "PRIMARY KEY") && strings.Contains(msg, "events") {
		return true
	}
	// Common wrapper shapes.
	var sqlErr *sqliteErrorShape
	if errors.As(err, &sqlErr) && sqlErr.IsPrimaryKeyConflict() {
		return true
	}
	return false
}

// sqliteErrorShape is a stand-in — modernc.org/sqlite emits errors
// as *sqlite.Error but we do not want to pull that import path
// through the bundle datasource. Keeping this as an opaque shape
// lets the substring check above do the real work while leaving
// future typed-error detection easy to slot in.
type sqliteErrorShape struct{ _ time.Time }

func (s *sqliteErrorShape) Error() string              { return "" }
func (s *sqliteErrorShape) IsPrimaryKeyConflict() bool { return false }

// ensure sql import stays referenced; datasource uses it indirectly
// through Database.open().
var _ = sql.ErrNoRows
