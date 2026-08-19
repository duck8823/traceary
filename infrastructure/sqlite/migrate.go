package sqlite

import (
	"context"
	"database/sql"
	"log/slog"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	"golang.org/x/xerrors"

	apptypes "github.com/duck8823/traceary/application/types"
)

func (d *Database) migrate(ctx context.Context, db *sql.DB) error {
	return d.migrateWithOptions(ctx, db, false)
}

func (d *Database) migrateAuthorized(ctx context.Context, db *sql.DB) error {
	return d.migrateWithOptions(ctx, db, true)
}

func (d *Database) migrateWithOptions(ctx context.Context, db *sql.DB, allowOffline bool) error {
	migrations, err := inventoryEmbeddedMigrations(d.migrations)
	if err != nil {
		return xerrors.Errorf("failed to inventory exact migration catalog: %w", err)
	}

	applied, err := loadAppliedMigrations(ctx, db)
	if err != nil {
		if !schemaMigrationsTableMissing(err) {
			return xerrors.Errorf("failed to load applied migrations: %w", err)
		}
		if err := ensureSchemaMigrationsTable(ctx, db); err != nil {
			return xerrors.Errorf("failed to create schema_migrations table: %w", err)
		}
		applied, err = loadAppliedMigrations(ctx, db)
		if err != nil {
			return xerrors.Errorf("failed to load applied migrations: %w", err)
		}
	}

	if err := rejectForeignAppliedMigrations(applied, migrations); err != nil {
		return err
	}
	if catalogFullyApplied(applied, migrations) {
		return nil
	}

	if err := ensureSchemaMigrationsTable(ctx, db); err != nil {
		return xerrors.Errorf("failed to create schema_migrations table: %w", err)
	}

	for _, migration := range migrations {
		if _, exists := applied[migration.version]; exists {
			continue
		}
		if !allowOffline && isDataDependentOfflineMigration(migration.version) {
			hasEvents, inspectErr := storeHasSourceEvents(ctx, db)
			if inspectErr != nil {
				return inspectErr
			}
			if hasEvents {
				return &apptypes.OfflineMigrationsRequiredError{Versions: pendingOfflineMigrations(migrations, applied)}
			}
		}
		if allowOffline && isDataDependentOfflineMigration(migration.version) {
			slog.Info("applying data-dependent migration", "version", migration.version, "name", migration.name)
		}
		if err := executeExactMigration(ctx, db, migration); err != nil {
			return xerrors.Errorf("failed to apply migration: %w", err)
		}
		applied[migration.version] = migration.name
	}

	return nil
}

func isDataDependentOfflineMigration(version int64) bool {
	entry, ok := preparedMigrationManifest[version]
	return ok && entry.Class == MigrationDataDependentOffline
}

func catalogFullyApplied(applied map[int64]string, catalog []embeddedMigration) bool {
	if len(catalog) == 0 {
		return false
	}
	for _, migration := range catalog {
		if _, exists := applied[migration.version]; !exists {
			return false
		}
	}
	return true
}

func schemaMigrationsTableMissing(err error) bool {
	if err == nil {
		return false
	}
	message := strings.ToLower(err.Error())
	return strings.Contains(message, "no such table") && strings.Contains(message, "schema_migrations")
}

func sqliteBusyError(err error) bool {
	if err == nil {
		return false
	}
	message := strings.ToLower(err.Error())
	return strings.Contains(message, "sqlite_busy") || strings.Contains(message, "database is locked") || strings.Contains(message, "database table is locked")
}

func pendingOfflineMigrations(migrations []embeddedMigration, applied map[int64]string) []int64 {
	var versions []int64
	for _, migration := range migrations {
		if _, exists := applied[migration.version]; exists {
			continue
		}
		if isDataDependentOfflineMigration(migration.version) {
			versions = append(versions, migration.version)
		}
	}
	return versions
}

func storeHasSourceEvents(ctx context.Context, db *sql.DB) (bool, error) {
	var name string
	err := db.QueryRowContext(ctx, `SELECT name FROM sqlite_master WHERE type='table' AND name='events'`).Scan(&name)
	if err == sql.ErrNoRows {
		return false, nil
	}
	if err != nil {
		return false, xerrors.Errorf("inspect events table: %w", err)
	}
	var id string
	err = db.QueryRowContext(ctx, `SELECT id FROM events LIMIT 1`).Scan(&id)
	if err == sql.ErrNoRows {
		return false, nil
	}
	if err != nil {
		return false, xerrors.Errorf("inspect events rows: %w", err)
	}
	return true, nil
}

// rejectForeignAppliedMigrations fails initialization when the ledger records
// a migration this catalog replaced or never contained at or below the
// catalog's maximum version: continuing would silently skip a same-version
// replacement migration (an abandoned development catalog necessarily recorded
// the replaced version under its old name, because migrations apply in version
// order). A missing version stays applicable because released history contains
// gap upgrades, and versions above the catalog maximum stay tolerated so a
// store upgraded by a newer release remains openable after a binary rollback;
// store_format_state gates truly incompatible stores.
func rejectForeignAppliedMigrations(applied map[int64]string, catalog []embeddedMigration) error {
	names := make(map[int64]string, len(catalog))
	for _, migration := range catalog {
		names[migration.version] = migration.name
	}
	maximum := catalog[len(catalog)-1].version
	versions := make([]int64, 0, len(applied))
	for version := range applied {
		versions = append(versions, version)
	}
	slices.Sort(versions)
	for _, version := range versions {
		if version > maximum {
			continue
		}
		name, known := names[version]
		if !known {
			return xerrors.Errorf("schema_migrations records version %d (%q), which this migration catalog does not contain; the store was initialized by an incompatible catalog", version, applied[version])
		}
		if applied[version] != name {
			return xerrors.Errorf("schema_migrations records version %d as %q, but this migration catalog defines it as %q; the store was initialized by an incompatible catalog", version, applied[version], name)
		}
	}
	return nil
}

func ensureSchemaMigrationsTable(ctx context.Context, db *sql.DB) error {
	const query = `
CREATE TABLE IF NOT EXISTS schema_migrations (
    version INTEGER PRIMARY KEY,
    name TEXT NOT NULL,
    applied_at TEXT NOT NULL
);`

	if _, err := db.ExecContext(ctx, query); err != nil {
		return xerrors.Errorf("failed to execute schema_migrations creation query: %w", err)
	}
	return nil
}

func loadAppliedMigrations(ctx context.Context, db *sql.DB) (map[int64]string, error) {
	rows, err := db.QueryContext(ctx, `SELECT version, name FROM schema_migrations;`)
	if err != nil {
		return nil, xerrors.Errorf("failed to query schema_migrations: %w", err)
	}
	defer func() {
		if err := rows.Close(); err != nil {
			slog.Debug("failed to close resource", "error", err)
		}
	}()

	applied := make(map[int64]string)
	for rows.Next() {
		var version int64
		var name string
		if err := rows.Scan(&version, &name); err != nil {
			return nil, xerrors.Errorf("failed to scan schema_migrations row: %w", err)
		}
		applied[version] = name
	}
	if err := rows.Err(); err != nil {
		return nil, xerrors.Errorf("failed to iterate schema_migrations rows: %w", err)
	}
	return applied, nil
}

func parseMigrationVersion(path string) (int64, error) {
	baseName := filepath.Base(path)
	versionPart, _, found := strings.Cut(baseName, "_")
	if !found {
		return 0, xerrors.Errorf("invalid migration filename: %s", baseName)
	}
	version, err := strconv.ParseInt(versionPart, 10, 64)
	if err != nil {
		return 0, xerrors.Errorf("invalid migration version (%s): %w", versionPart, err)
	}
	return version, nil
}

func applyMigration(ctx context.Context, db *sql.DB, version int64, name string, migrationSQL string) (err error) {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return xerrors.Errorf("failed to begin transaction: %w", err)
	}
	defer func() {
		if err != nil {
			if rollbackErr := tx.Rollback(); rollbackErr != nil {
				err = xerrors.Errorf("failed to roll back migration transaction: %w (original error: %v)", rollbackErr, err)
			}
		}
	}()

	if _, err := tx.ExecContext(ctx, migrationSQL); err != nil {
		return xerrors.Errorf("failed to execute migration SQL (version=%d): %w", version, err)
	}
	if _, err := tx.ExecContext(
		ctx,
		`INSERT INTO schema_migrations(version, name, applied_at) VALUES (?, ?, ?);`,
		version,
		name,
		time.Now().UTC().Format(time.RFC3339Nano),
	); err != nil {
		return xerrors.Errorf("failed to insert schema_migrations record (version=%d): %w", version, err)
	}
	if err := tx.Commit(); err != nil {
		return xerrors.Errorf("failed to commit migration (version=%d): %w", version, err)
	}

	return nil
}
