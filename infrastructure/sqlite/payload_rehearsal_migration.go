package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"time"
)

type rehearsalMigrationIdentity struct {
	digest, schema                string
	pageSize, pageCount, freelist int64
}

//nolint:wrapcheck // internal preflight errors are classified at the Run boundary.
func inspectRehearsalMigrationIdentity(ctx context.Context, db *sql.DB, path string) (rehearsalMigrationIdentity, error) {
	digest, err := fileDigest(path)
	if err != nil {
		return rehearsalMigrationIdentity{}, err
	}
	schema, err := rehearsalSchemaFingerprint(ctx, db)
	if err != nil {
		return rehearsalMigrationIdentity{}, err
	}
	result := rehearsalMigrationIdentity{digest: digest, schema: schema}
	if err = db.QueryRowContext(ctx, `PRAGMA page_size`).Scan(&result.pageSize); err != nil {
		return rehearsalMigrationIdentity{}, err
	}
	if err = db.QueryRowContext(ctx, `PRAGMA page_count`).Scan(&result.pageCount); err != nil {
		return rehearsalMigrationIdentity{}, err
	}
	if err = db.QueryRowContext(ctx, `PRAGMA freelist_count`).Scan(&result.freelist); err != nil {
		return rehearsalMigrationIdentity{}, err
	}
	return result, nil
}

//nolint:wrapcheck // internal clone errors are classified at the Run boundary.
func exactFileClone(source, destination string) error {
	in, err := os.Open(source)
	if err != nil {
		return err
	}
	defer func() { _ = in.Close() }()
	out, err := os.OpenFile(destination, os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return err
	}
	if _, err = io.Copy(out, in); err != nil {
		_ = out.Close()
		return err
	}
	if err = out.Sync(); err != nil {
		_ = out.Close()
		return err
	}
	return out.Close()
}

// measureRehearsalMigrationWAL executes the exact migration set on a byte clone.
// The clone's uncheckpointed WAL is a strict reservation input for the target.
//
//nolint:wrapcheck // internal preflight errors are classified at the Run boundary.
func (d *Database) measureRehearsalMigrationWAL(ctx context.Context, target string, lock time.Duration) (int64, error) {
	clone, err := os.CreateTemp(filepath.Dir(target), ".traceary-migration-preflight-*.db")
	if err != nil {
		return 0, err
	}
	clonePath := clone.Name()
	if err = clone.Close(); err != nil {
		return 0, err
	}
	defer func() {
		for _, suffix := range []string{"", "-wal", "-shm"} {
			_ = os.Remove(clonePath + suffix)
		}
	}()
	if err = exactFileClone(target, clonePath); err != nil {
		return 0, err
	}
	db, err := sql.Open("sqlite", writableRehearsalDSN(clonePath, lock))
	if err != nil {
		return 0, err
	}
	defer func() { _ = db.Close() }()
	db.SetMaxOpenConns(1)
	if _, err = db.ExecContext(ctx, `PRAGMA wal_autocheckpoint=0`); err != nil {
		return 0, err
	}
	started := time.Now()
	if err = d.migrate(ctx, db); err != nil {
		return 0, err
	}
	if time.Since(started) > lock {
		return 0, errors.New("migration preflight lock duration cap exceeded")
	}
	info, err := os.Stat(clonePath + "-wal")
	if errors.Is(err, os.ErrNotExist) {
		return 0, nil
	}
	if err != nil || !info.Mode().IsRegular() {
		return 0, errors.New("cannot measure migration preflight WAL")
	}
	return info.Size(), nil
}

// migrateOnBudgetedConnection applies pending migrations inside the caller's
// BEGIN IMMEDIATE transaction. No migration may open a second transaction.
//
//nolint:wrapcheck // the caller provides the migration operation classification.
func (d *Database) migrateOnBudgetedConnection(ctx context.Context, conn *sql.Conn) error {
	if _, err := conn.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS schema_migrations (version INTEGER PRIMARY KEY,name TEXT NOT NULL,applied_at TEXT NOT NULL)`); err != nil {
		return err
	}
	rows, err := conn.QueryContext(ctx, `SELECT version FROM schema_migrations`)
	if err != nil {
		return err
	}
	applied := map[int64]struct{}{}
	for rows.Next() {
		var version int64
		if err = rows.Scan(&version); err != nil {
			_ = rows.Close()
			return err
		}
		applied[version] = struct{}{}
	}
	if err = rows.Close(); err != nil {
		return err
	}
	paths, err := fs.Glob(d.migrations, "*.sql")
	if err != nil {
		return err
	}
	sort.Slice(paths, func(i, j int) bool {
		vi, _ := parseMigrationVersion(paths[i])
		vj, _ := parseMigrationVersion(paths[j])
		return vi < vj
	})
	for _, path := range paths {
		version, parseErr := parseMigrationVersion(path)
		if parseErr != nil {
			return parseErr
		}
		if _, ok := applied[version]; ok {
			continue
		}
		body, readErr := fs.ReadFile(d.migrations, path)
		if readErr != nil {
			return readErr
		}
		if _, execErr := conn.ExecContext(ctx, string(body)); execErr != nil {
			return fmt.Errorf("migration %d: %w", version, execErr)
		}
		if _, execErr := conn.ExecContext(ctx, `INSERT INTO schema_migrations(version,name,applied_at) VALUES(?,?,?)`, version, filepath.Base(path), time.Now().UTC().Format(time.RFC3339Nano)); execErr != nil {
			return execErr
		}
	}
	return nil
}
