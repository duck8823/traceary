package sqlite

import (
	"context"
	"database/sql"
	"io/fs"
	"log/slog"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"golang.org/x/xerrors"
)

func (d *Database) migrate(ctx context.Context, db *sql.DB) error {
	if err := ensureSchemaMigrationsTable(ctx, db); err != nil {
		return xerrors.Errorf("failed to create schema_migrations table: %w", err)
	}

	appliedVersions, err := loadAppliedVersions(ctx, db)
	if err != nil {
		return xerrors.Errorf("failed to load applied migrations: %w", err)
	}

	migrationPaths, err := fs.Glob(d.migrations, "*.sql")
	if err != nil {
		return xerrors.Errorf("failed to list migration files: %w", err)
	}
	if len(migrationPaths) == 0 {
		return xerrors.Errorf("no migration files found")
	}

	migrations := make([]migrationFile, 0, len(migrationPaths))
	seenVersions := make(map[int64]struct{}, len(migrationPaths))
	for _, migrationPath := range migrationPaths {
		version, err := parseMigrationVersion(migrationPath)
		if err != nil {
			return xerrors.Errorf("failed to parse migration version: %w", err)
		}
		if _, exists := seenVersions[version]; exists {
			return xerrors.Errorf("duplicate migration version: %d", version)
		}
		seenVersions[version] = struct{}{}
		migrations = append(migrations, migrationFile{
			path:    migrationPath,
			version: version,
		})
	}

	sort.Slice(migrations, func(i int, j int) bool {
		return migrations[i].version < migrations[j].version
	})

	for _, migration := range migrations {
		if _, exists := appliedVersions[migration.version]; exists {
			continue
		}

		migrationSQL, err := fs.ReadFile(d.migrations, migration.path)
		if err != nil {
			return xerrors.Errorf("failed to read migration file: %w", err)
		}

		if err := applyMigration(
			ctx,
			db,
			migration.version,
			filepath.Base(migration.path),
			string(migrationSQL),
		); err != nil {
			return xerrors.Errorf("failed to apply migration: %w", err)
		}
	}

	return nil
}

type migrationFile struct {
	path    string
	version int64
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

func loadAppliedVersions(ctx context.Context, db *sql.DB) (map[int64]struct{}, error) {
	rows, err := db.QueryContext(ctx, `SELECT version FROM schema_migrations;`)
	if err != nil {
		return nil, xerrors.Errorf("failed to query schema_migrations: %w", err)
	}
	defer func() {
		if err := rows.Close(); err != nil {
			slog.Debug("failed to close resource", "error", err)
		}
	}()

	versions := make(map[int64]struct{})
	for rows.Next() {
		var version int64
		if err := rows.Scan(&version); err != nil {
			return nil, xerrors.Errorf("failed to scan schema_migrations row: %w", err)
		}
		versions[version] = struct{}{}
	}
	if err := rows.Err(); err != nil {
		return nil, xerrors.Errorf("failed to iterate schema_migrations rows: %w", err)
	}
	return versions, nil
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
