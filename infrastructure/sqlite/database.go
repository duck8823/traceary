package sqlite

import (
	"context"
	"database/sql"
	"fmt"
	"io/fs"
	"log/slog"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	_ "modernc.org/sqlite" // Required to register the SQLite driver.

	"golang.org/x/xerrors"
)

// Database wraps a SQLite path and provides connection and migration
// utilities shared by all per-aggregate datasources in this package.
//
// The dbPath is mutable via SetPath so the CLI can late-bind the path
// after resolving the --db-path flag / TRACEARY_DB_PATH environment
// variable inside each subcommand's RunE. The mutex protects concurrent
// path switches from a racing reader; every operation takes a path
// snapshot at entry and then works with the snapshot, so a path switch
// midway through cannot split the check and the use.
type Database struct {
	mu         sync.RWMutex
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
	d.mu.Lock()
	defer d.mu.Unlock()
	d.dbPath = strings.TrimSpace(dbPath)
}

// Path returns the current database file path.
func (d *Database) Path() string {
	d.mu.RLock()
	defer d.mu.RUnlock()
	return d.dbPath
}

// openAt opens a new SQLite connection at the given path and pings it.
// Callers snapshot Database.Path() at entry and pass the snapshot here
// so a racing SetPath cannot split the snapshot and the connection.
func (d *Database) openAt(ctx context.Context, dbPath string) (_ *sql.DB, err error) {
	db, err := sql.Open("sqlite", sqliteDSN(dbPath))
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

// open opens a new SQLite connection at the current Path() and pings it.
func (d *Database) open(ctx context.Context) (_ *sql.DB, err error) {
	return d.openAt(ctx, d.Path())
}

// initialize creates the store directory, ensures permissions, and applies
// pending migrations. It snapshots the current path at entry and
// delegates to initializeAt so a concurrent SetPath cannot split the
// snapshot and the subsequent open.
func (d *Database) initialize(ctx context.Context) error {
	return d.initializeAt(ctx, d.Path())
}

// initializeAt creates the store directory for the supplied path,
// ensures permissions, and applies pending migrations. Callers that
// already captured a path snapshot earlier in an operation (e.g.
// backup/restore that validated the snapshot before this call) should
// invoke this variant so every step of the operation targets the same
// path, even when SetPath races midway.
func (d *Database) initializeAt(ctx context.Context, snapshot string) (err error) {
	if snapshot == "" {
		return xerrors.Errorf("DB path must not be empty")
	}

	if err := os.MkdirAll(filepath.Dir(snapshot), 0o700); err != nil {
		return xerrors.Errorf("failed to create DB directory: %w", err)
	}

	db, err := d.openAt(ctx, snapshot)
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
	if chmodErr := os.Chmod(snapshot, 0o600); chmodErr != nil {
		slog.Debug("failed to set DB file permissions (best-effort)", "error", chmodErr)
	}

	if err := d.migrate(ctx, db); err != nil {
		return xerrors.Errorf("failed to run SQLite migrations: %w", err)
	}

	return nil
}

// sqliteBusyTimeout is the SQLite busy-wait window in milliseconds
// applied via the busy_timeout pragma. Five seconds is long enough to
// absorb routine hook/MCP write contention while still surfacing real
// deadlocks to the caller.
const sqliteBusyTimeout = 5000

func sqliteDSN(dbPath string) string {
	values := url.Values{}
	// WAL lets readers and writers proceed concurrently so tail polls
	// are not blocked by short-lived hook writes. synchronous=NORMAL is
	// the recommended pairing with WAL (fsyncs only on checkpoint).
	// busy_timeout lets SQLite auto-retry on transient lock contention
	// instead of failing immediately with SQLITE_BUSY.
	values.Add("_pragma", "journal_mode(WAL)")
	values.Add("_pragma", "synchronous(NORMAL)")
	values.Add("_pragma", fmt.Sprintf("busy_timeout(%d)", sqliteBusyTimeout))
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

// formatMemoryValidityTimestamp renders a time.Time as a fixed-width
// RFC3339 string with nine fractional-second digits, e.g.
// "2026-04-10T00:00:00.123000000Z". Unlike RFC3339Nano (which trims
// trailing zeros and therefore emits variable-width output), this
// representation is lexicographically ordered in the same direction
// as real time, so SQLite can compare memories.valid_from /
// memories.valid_to with a plain `<` / `>` against a bind parameter
// without wrapping the column in datetime() — which would both drop
// sub-second precision AND make the idx_memories_valid_window index
// unusable (see #664).
//
// The format is only used for the memory validity columns so other
// timestamps (created_at, updated_at, expires_at, event timestamps)
// keep the existing RFC3339Nano shape; migration 000010 backfills
// pre-v0.8.1 rows so the validity columns are consistent across
// historical and new data.
func formatMemoryValidityTimestamp(timestamp time.Time) string {
	return timestamp.UTC().Format("2006-01-02T15:04:05.000000000Z07:00")
}

func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}
