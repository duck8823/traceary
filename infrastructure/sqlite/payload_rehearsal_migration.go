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

type rehearsalMigrationPlan struct {
	pending     bool
	walBytes    int64
	elapsed     time.Duration
	journalMode string
}

//nolint:wrapcheck // internal migration inventory is classified by callers.
func (d *Database) rehearsalMigrationsPending(ctx context.Context, db *sql.DB) (bool, error) {
	var migrationTable int
	if err := db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM sqlite_master WHERE type='table' AND name='schema_migrations')`).Scan(&migrationTable); err != nil {
		return false, err
	}
	if migrationTable == 0 {
		return true, nil
	}
	rows, err := db.QueryContext(ctx, `SELECT version FROM schema_migrations`)
	if err != nil {
		return false, err
	}
	defer func() { _ = rows.Close() }()
	applied := map[int64]struct{}{}
	for rows.Next() {
		var version int64
		if err = rows.Scan(&version); err != nil {
			return false, err
		}
		applied[version] = struct{}{}
	}
	if err = rows.Err(); err != nil {
		return false, err
	}
	paths, err := fs.Glob(d.migrations, "*.sql")
	if err != nil {
		return false, err
	}
	for _, path := range paths {
		version, parseErr := parseMigrationVersion(path)
		if parseErr != nil {
			return false, parseErr
		}
		if _, ok := applied[version]; !ok {
			return true, nil
		}
	}
	return false, nil
}

//nolint:wrapcheck // journal normalization is classified by callers.
func normalizeRehearsalJournal(ctx context.Context, db *sql.DB, limit time.Duration) (string, time.Duration, error) {
	if limit <= 0 || limit > time.Second {
		limit = time.Second
	}
	opCtx, cancel := context.WithTimeout(ctx, limit)
	defer cancel()
	started := time.Now()
	var mode string
	if err := db.QueryRowContext(opCtx, `PRAGMA journal_mode=WAL`).Scan(&mode); err != nil {
		return "", time.Since(started), err
	}
	elapsed := time.Since(started)
	if elapsed > limit {
		return mode, elapsed, errors.New("rehearsal journal normalization duration cap exceeded")
	}
	if mode != "wal" {
		return mode, elapsed, fmt.Errorf("rehearsal journal mode mismatch: %s", mode)
	}
	return mode, elapsed, nil
}

// measureRehearsalMigrationWAL executes the exact pending migration set in one
// BEGIN IMMEDIATE/COMMIT on a byte clone and observes its live WAL.
//
//nolint:wrapcheck // internal preflight errors are classified at the Run boundary.
func (d *Database) measureRehearsalMigrationWAL(ctx context.Context, target string, lock time.Duration) (rehearsalMigrationPlan, error) {
	plan := rehearsalMigrationPlan{}
	clone, err := os.CreateTemp(filepath.Dir(target), ".traceary-migration-preflight-*.db")
	if err != nil {
		return plan, err
	}
	clonePath := clone.Name()
	if err = clone.Close(); err != nil {
		return plan, err
	}
	defer func() {
		for _, suffix := range []string{"", "-wal", "-shm"} {
			_ = os.Remove(clonePath + suffix)
		}
	}()
	if err = exactFileClone(target, clonePath); err != nil {
		return plan, err
	}
	db, err := sql.Open("sqlite", writableRehearsalDSN(clonePath, lock))
	if err != nil {
		return plan, err
	}
	defer func() { _ = db.Close() }()
	db.SetMaxOpenConns(1)
	plan.pending, err = d.rehearsalMigrationsPending(ctx, db)
	if err != nil {
		return plan, err
	}
	plan.journalMode, _, err = normalizeRehearsalJournal(ctx, db, lock)
	if err != nil {
		return plan, err
	}
	if _, err = db.ExecContext(ctx, `PRAGMA wal_autocheckpoint=0`); err != nil {
		return plan, err
	}
	if !plan.pending {
		return plan, nil
	}
	conn, err := db.Conn(ctx)
	if err != nil {
		return plan, err
	}
	defer func() { _ = conn.Close() }()
	started := time.Now()
	if _, err = conn.ExecContext(ctx, `BEGIN IMMEDIATE`); err != nil {
		return plan, err
	}
	defer func() { _, _ = conn.ExecContext(context.WithoutCancel(ctx), `ROLLBACK`) }()
	if err = d.migrateOnBudgetedConnection(ctx, conn); err != nil {
		return plan, err
	}
	if _, err = conn.ExecContext(ctx, `COMMIT`); err != nil {
		return plan, err
	}
	plan.elapsed = time.Since(started)
	if plan.elapsed > lock {
		return plan, errors.New("migration preflight lock duration cap exceeded")
	}
	info, statErr := os.Stat(clonePath + "-wal")
	if statErr != nil || !info.Mode().IsRegular() || info.Size() <= 0 {
		return plan, errors.New("pending migration produced no valid WAL")
	}
	plan.walBytes = info.Size()
	var pageSize int64
	if err = conn.QueryRowContext(ctx, `PRAGMA page_size`).Scan(&pageSize); err != nil || pageSize <= 0 {
		return plan, errors.New("cannot inspect migration preflight WAL frame")
	}
	if plan.walBytes < 32+24+pageSize {
		return plan, errors.New("pending migration WAL is smaller than one frame")
	}
	return plan, nil
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
