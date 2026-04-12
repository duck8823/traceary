package sqlite

import (
	"context"
	"database/sql"
	"io/fs"
	"log/slog"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	_ "modernc.org/sqlite" // Required to register the SQLite driver.

	"golang.org/x/xerrors"
)

// Database wraps a SQLite path and provides connection and migration
// utilities shared by all per-aggregate datasources in this package.
//
// The dbPath is mutable via SetPath so the CLI can late-bind the path
// after resolving the --db-path flag / TRACEARY_DB_PATH environment
// variable inside each subcommand's RunE.
type Database struct {
	dbPath     string
	migrations fs.FS
}

// NewDatabase creates a new Database bound to the given database path.
func NewDatabase(dbPath string, migrations fs.FS) *Database {
	return &Database{dbPath: strings.TrimSpace(dbPath), migrations: migrations}
}

// SetPath updates the database file path. Call this after resolving the
// CLI --db-path flag / TRACEARY_DB_PATH environment variable so the
// datasources built from this Database open the user-specified path
// instead of the composition-root default.
func (d *Database) SetPath(dbPath string) {
	d.dbPath = strings.TrimSpace(dbPath)
}

// Path returns the database file path.
func (d *Database) Path() string {
	return d.dbPath
}

// open opens a new SQLite connection and pings it.
func (d *Database) open(ctx context.Context) (_ *sql.DB, err error) {
	db, err := sql.Open("sqlite", sqliteDSN(d.dbPath))
	if err != nil {
		return nil, xerrors.Errorf("failed to initialize SQLite connection: %w", err)
	}
	defer func() {
		if err != nil {
			if closeErr := db.Close(); closeErr != nil {
				slog.Debug("failed to close resource", "error", closeErr)
			}
		}
	}()

	if err := db.PingContext(ctx); err != nil {
		return nil, xerrors.Errorf("failed to ping SQLite DB: %w", err)
	}

	return db, nil
}

// initialize creates the store directory, ensures permissions, and applies
// pending migrations.
func (d *Database) initialize(ctx context.Context) (err error) {
	trimmedPath := d.dbPath
	if trimmedPath == "" {
		return xerrors.Errorf("DB path must not be empty")
	}

	if err := os.MkdirAll(filepath.Dir(trimmedPath), 0o700); err != nil {
		return xerrors.Errorf("failed to create DB directory: %w", err)
	}

	db, err := d.open(ctx)
	if err != nil {
		return xerrors.Errorf("failed to initialize SQLite connection: %w", err)
	}
	defer func() {
		if closeErr := db.Close(); closeErr != nil && err == nil {
			err = xerrors.Errorf("failed to close SQLite connection: %w", closeErr)
		}
	}()
	// chmod is best-effort; ignore errors on read-only filesystems or
	// when the DB file is owned by another user.
	if chmodErr := os.Chmod(trimmedPath, 0o600); chmodErr != nil {
		slog.Debug("failed to set DB file permissions (best-effort)", "error", chmodErr)
	}

	if err := d.migrate(ctx, db); err != nil {
		return xerrors.Errorf("failed to run SQLite migrations: %w", err)
	}

	return nil
}

func sqliteDSN(dbPath string) string {
	values := url.Values{}
	values.Add("_pragma", "foreign_keys(1)")

	return (&url.URL{
		Scheme:   "file",
		Path:     dbPath,
		RawQuery: values.Encode(),
	}).String()
}

func formatTimestamp(timestamp time.Time) string {
	return timestamp.UTC().Format(time.RFC3339Nano)
}

func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}
