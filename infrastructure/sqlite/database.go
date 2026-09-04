package sqlite

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"fmt"
	"io/fs"
	"log/slog"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"modernc.org/sqlite" // Registers the SQLite driver and the ts_norm scalar function (see init).

	"golang.org/x/xerrors"

	"github.com/duck8823/traceary/application"
)

// sqlTimestampNormalizeFunc is the name of the SQLite scalar function that
// normalizes a stored RFC3339Nano timestamp to a lexically-orderable
// fixed-width form for boundary-correct TEXT comparisons. See
// normalizeRFC3339NanoForCompare and #1185.
const sqlTimestampNormalizeFunc = "ts_norm"

// sqlTimestampValidFunc is the name of the SQLite scalar function that reports
// whether a stored timestamp parses at all. See validTimestampSQLFunc.
const sqlTimestampValidFunc = "ts_valid"
const sqlPayloadDecodeFunc = "traceary_payload_decode"

const (
	currentReaderVersion        = 37
	maximumPayloadFormatVersion = 1
)

var (
	coordinatedSQLiteDriver   driver.Driver
	coordinatedSQLiteDriverMu sync.RWMutex
)

func coordinatedDriver() driver.Driver {
	coordinatedSQLiteDriverMu.RLock()
	defer coordinatedSQLiteDriverMu.RUnlock()
	return coordinatedSQLiteDriver
}

// init registers ts_norm on the modernc SQLite driver. Registration is global
// and applies to every connection opened afterwards, so the per-operation
// connections this package opens all expose the function. It is registered as
// deterministic so the query planner may cache its result for identical inputs.
func init() {
	sqlite.MustRegisterDeterministicScalarFunction(
		sqlTimestampNormalizeFunc,
		1,
		normalizeTimestampSQLFunc,
	)
	sqlite.MustRegisterDeterministicScalarFunction(sqlTimestampValidFunc, 1, validTimestampSQLFunc)
	sqlite.MustRegisterDeterministicScalarFunction(sqlPayloadDecodeFunc, 6, decodePayloadSQLFunc)
	probe, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		panic("registered SQLite driver unavailable: " + err.Error())
	}
	coordinatedSQLiteDriver = probe.Driver()
	_ = probe.Close()
}

// decodePayloadSQLFunc exposes the persisted payload contract to derived SQL
// projections. This keeps restored trigger-based writers codec-aware without
// teaching SQLite's JSON functions about compressed storage.
func decodePayloadSQLFunc(_ *sqlite.FunctionContext, args []driver.Value) (driver.Value, error) {
	if len(args) != 6 {
		return nil, xerrors.Errorf("%s expects exactly six arguments, got %d", sqlPayloadDecodeFunc, len(args))
	}
	stored, err := driverBytes(args[0])
	if err != nil {
		return nil, err
	}
	row := payloadRow{Stored: stored}
	if args[1] != nil {
		row.Codec = sql.NullString{String: fmt.Sprint(args[1]), Valid: true}
	}
	if args[2] != nil {
		row.FormatVersion = sql.NullInt64{Int64: args[2].(int64), Valid: true}
	}
	if args[3] != nil {
		row.PlaintextBytes = sql.NullInt64{Int64: args[3].(int64), Valid: true}
	}
	if args[4] != nil {
		row.StoredBytes = sql.NullInt64{Int64: args[4].(int64), Valid: true}
	}
	if args[5] != nil {
		row.SHA256 = sql.NullString{String: fmt.Sprint(args[5]), Valid: true}
	}
	plain, err := row.decode(maxDecodedPayloadBytes)
	if err != nil {
		return nil, err
	}
	return string(plain), nil
}

func driverBytes(value driver.Value) ([]byte, error) {
	switch typed := value.(type) {
	case string:
		return []byte(typed), nil
	case []byte:
		return typed, nil
	default:
		return nil, xerrors.Errorf("payload storage has unexpected SQL type %T", value)
	}
}

// normalizeTimestampSQLFunc adapts normalizeRFC3339NanoForCompare to the
// SQLite scalar-function signature. NULL and non-text arguments are returned
// unchanged so wrapping a column in ts_norm never alters its NULL-ness or
// errors a query over historical/malformed rows.
func normalizeTimestampSQLFunc(_ *sqlite.FunctionContext, args []driver.Value) (driver.Value, error) {
	if len(args) != 1 {
		return nil, xerrors.Errorf("ts_norm expects exactly one argument, got %d", len(args))
	}
	raw, ok := args[0].(string)
	if !ok {
		return args[0], nil
	}
	return normalizeRFC3339NanoForCompare(raw), nil
}

// validTimestampSQLFunc reports whether a column holds a timestamp whose age
// can actually be determined. ts_norm deliberately degrades a malformed value
// to lexical comparison so a read query never errors on a historical row, but
// a comparison that only happens to succeed is not a safe basis for discarding
// a body: "0" sorts before every real cutoff and would make an event of
// unknown age look old enough to discard. Guards on irreversible writes pair
// ts_norm with ts_valid so an undatable row fails closed instead.
func validTimestampSQLFunc(_ *sqlite.FunctionContext, args []driver.Value) (driver.Value, error) {
	if len(args) != 1 {
		return nil, xerrors.Errorf("%s expects exactly one argument, got %d", sqlTimestampValidFunc, len(args))
	}
	raw, ok := args[0].(string)
	if !ok {
		return int64(0), nil
	}
	if _, err := time.Parse(time.RFC3339Nano, raw); err != nil {
		return int64(0), nil
	}
	return int64(1), nil
}

// normalizeRFC3339NanoForCompare rewrites a variable-width RFC3339Nano
// timestamp (the shape formatTimestamp emits, which trims trailing fractional
// zeros) into the fixed-width nine-fractional-digit form used by
// formatMemoryValidityTimestamp. Variable-width RFC3339Nano is NOT
// lexicographically ordered the same as real time — e.g. "…00.5Z" sorts before
// "…00Z" because '.' (0x2E) < 'Z' (0x5A) — so a plain TEXT comparison over
// created_at / started_at / ended_at can drop an in-range row or include an
// out-of-range one near a fractional-second boundary. The fixed-width form is
// lexicographically ordered the same as real time, so TEXT comparisons over it
// are boundary-correct (see #1185). A value that does not parse as RFC3339 is
// returned unchanged so historical/malformed rows degrade to the previous
// lexical behavior rather than erroring the whole query.
func normalizeRFC3339NanoForCompare(raw string) string {
	parsed, err := time.Parse(time.RFC3339Nano, raw)
	if err != nil {
		return raw
	}
	return formatMemoryValidityTimestamp(parsed)
}

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
	mu             sync.RWMutex
	dbPath         string
	migrations     fs.FS
	sharedReadOnly *sql.DB
	// afterReadOnlyConnectionOpened runs each time openReadOnly or
	// WithReadScope opens a genuinely fresh read-only connection (i.e. not a
	// scope/shared-handle reuse). Tests use it to assert O(1) opens across a
	// read-scoped pass; production leaves it nil.
	afterReadOnlyConnectionOpened func()
	// afterCompatibilityCheck runs each time checkStoreCompatibility succeeds
	// inside openReadOnly or WithReadScope. Tests use it to assert the guard
	// runs once per scope; production leaves it nil.
	afterCompatibilityCheck func()
}

// readScopeKey is the unexported context key WithReadScope uses to carry its
// shared read-only handle. Unexported so no package outside sqlite can read
// or forge a scope value.
type readScopeKey struct{}

// readScope holds the read-only handle opened for the lifetime of a
// WithReadScope call.
type readScope struct {
	db *sql.DB
}

// WithReadScope opens one read-only handle for the duration of fn, running
// the store-compatibility guard exactly once at entry, and closes the handle
// when fn returns (including on panic, via defer). Datasource methods that
// call openReadOnly while ctx carries the scope reuse the shared handle
// instead of opening their own. Nesting is safe: a WithReadScope call made
// while ctx already carries a scope reuses the outer handle without opening
// a second connection or re-running the guard. Callers that never enter a
// scope are unaffected — openReadOnly keeps its current per-call behaviour.
func (d *Database) WithReadScope(ctx context.Context, fn func(context.Context) error) error {
	if _, ok := ctx.Value(readScopeKey{}).(*readScope); ok {
		return fn(ctx)
	}
	if d.sharedReadOnly != nil {
		// The benchmark-only shared immutable connection group already gives
		// every openReadOnly call the same handle; nothing further to scope.
		return fn(ctx)
	}

	dbPath := d.Path()
	db := openCoordinatedReadOnlyDB(dbPath, sqliteReadOnlyDSN(dbPath))
	pingCtx := application.StoreOpenContext(ctx)
	if err := db.PingContext(pingCtx); err != nil {
		_ = db.Close()
		return xerrors.Errorf("failed to ping read-only SQLite DB: %w", err)
	}
	if err := checkStoreCompatibility(pingCtx, db); err != nil {
		_ = db.Close()
		return err
	}
	if d.afterCompatibilityCheck != nil {
		d.afterCompatibilityCheck()
	}
	if d.afterReadOnlyConnectionOpened != nil {
		d.afterReadOnlyConnectionOpened()
	}
	defer func() {
		if closeErr := db.Close(); closeErr != nil {
			slog.Debug("failed to close resource", "error", closeErr)
		}
	}()

	return fn(context.WithValue(ctx, readScopeKey{}, &readScope{db: db}))
}

// NewImmutableReadDatabase opens one shared immutable connection group for benchmark orchestration.
func NewImmutableReadDatabase(ctx context.Context, dbPath string) (*Database, error) {
	db := openCoordinatedReadOnlyDB(dbPath, sqliteImmutableDSN(dbPath))
	if err := db.PingContext(ctx); err != nil {
		_ = db.Close()
		return nil, xerrors.Errorf("ping immutable SQLite store: %w", err)
	}
	// Immutable benchmark stores cannot change while the group is open. A
	// small pool permits row hydration while a metadata cursor is active.
	db.SetMaxOpenConns(4)
	if err := checkStoreCompatibility(ctx, db); err != nil {
		_ = db.Close()
		return nil, err
	}
	return &Database{dbPath: strings.TrimSpace(dbPath), sharedReadOnly: db}, nil
}

// CloseSharedReadOnly closes the benchmark-only shared immutable connection group.
func (d *Database) CloseSharedReadOnly() error {
	if d.sharedReadOnly == nil {
		return nil
	}
	if err := d.sharedReadOnly.Close(); err != nil {
		return xerrors.Errorf("close shared immutable SQLite store: %w", err)
	}
	return nil
}

func (d *Database) release(db *sql.DB) {
	if db != d.sharedReadOnly {
		_ = db.Close()
	}
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
var afterOpenAtHook func()

func (d *Database) openAt(ctx context.Context, dbPath string) (_ *sql.DB, err error) {
	if afterOpenAtHook != nil {
		afterOpenAtHook()
	}
	db := openCoordinatedDB(dbPath, sqliteDSN(dbPath))
	defer func() {
		if err != nil {
			if closeErr := db.Close(); closeErr != nil {
				slog.Debug("failed to close resource", "error", closeErr)
			}
		}
	}()

	pingCtx := application.StoreOpenContext(ctx)
	if err := db.PingContext(pingCtx); err != nil {
		return nil, xerrors.Errorf("failed to ping SQLite DB: %w", err)
	}
	if err := checkStoreCompatibility(pingCtx, db); err != nil {
		return nil, err
	}

	return db, nil
}

// open opens a new SQLite connection at the current Path() and pings it.
func (d *Database) open(ctx context.Context) (_ *sql.DB, err error) {
	if d.sharedReadOnly != nil {
		return d.sharedReadOnly, nil
	}
	return d.openAt(ctx, d.Path())
}

// openO1ReadOnly opens mode=ro with busy_timeout(0) and no coordinated
// lease, matching the large-store doctor page-metadata probe. A live
// writer fails the ping immediately instead of waiting on the shared lease.
func (d *Database) openO1ReadOnly(ctx context.Context) (_ *sql.DB, err error) {
	db, err := sql.Open("sqlite", sqliteO1ReadOnlyDSN(d.Path()))
	if err != nil {
		return nil, xerrors.Errorf("failed to open O(1) read-only SQLite DB: %w", err)
	}
	db.SetMaxOpenConns(1)
	if err := db.PingContext(ctx); err != nil {
		_ = db.Close()
		return nil, xerrors.Errorf("failed to ping O(1) read-only SQLite DB: %w", err)
	}
	return db, nil
}

// openReadOnly opens the current database without journal-mode or schema
// side effects. Preview commands use this path so a dry-run cannot create or
// migrate a store, change database content, or change file permissions. SQLite
// may still create WAL shared-memory sidecars while reading a WAL-mode store.
func (d *Database) openReadOnly(ctx context.Context) (_ *sql.DB, err error) {
	if scope, ok := ctx.Value(readScopeKey{}).(*readScope); ok {
		return scope.db, nil
	}
	if d.sharedReadOnly != nil {
		return d.sharedReadOnly, nil
	}
	dbPath := d.Path()
	db := openCoordinatedReadOnlyDB(dbPath, sqliteReadOnlyDSN(dbPath))
	defer func() {
		if err != nil {
			if closeErr := db.Close(); closeErr != nil {
				slog.Debug("failed to close resource", "error", closeErr)
			}
		}
	}()
	pingCtx := application.StoreOpenContext(ctx)
	if err := db.PingContext(pingCtx); err != nil {
		return nil, xerrors.Errorf("failed to ping read-only SQLite DB: %w", err)
	}
	if err := checkStoreCompatibility(pingCtx, db); err != nil {
		return nil, err
	}
	if d.afterCompatibilityCheck != nil {
		d.afterCompatibilityCheck()
	}
	if d.afterReadOnlyConnectionOpened != nil {
		d.afterReadOnlyConnectionOpened()
	}
	return db, nil
}

// VerifyStoreCompatibility applies the store-format policy to direct SQLite
// consumers that intentionally do not use Database. A missing state table
// denotes a supported legacy store so initialize can apply migration 36.
func VerifyStoreCompatibility(ctx context.Context, db *sql.DB) error {
	var exists int
	if err := db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM sqlite_master WHERE type='table' AND name='store_format_state')`).Scan(&exists); err != nil {
		return xerrors.Errorf("check store format state: %w", err)
	}
	if exists == 0 {
		return nil
	}
	var minimumReader, maximumPayload int
	if err := db.QueryRowContext(ctx, `SELECT minimum_reader_version, maximum_payload_format FROM store_format_state WHERE singleton = 1`).Scan(&minimumReader, &maximumPayload); err != nil {
		return xerrors.Errorf("read store format state: %w", err)
	}
	if minimumReader < 0 || maximumPayload < 0 {
		return xerrors.New("store format state contains invalid negative versions")
	}
	if minimumReader > currentReaderVersion {
		return xerrors.Errorf("store requires reader version %d; this reader supports %d", minimumReader, currentReaderVersion)
	}
	if maximumPayload > maximumPayloadFormatVersion {
		return xerrors.Errorf("store payload format %d is unsupported; maximum supported is %d", maximumPayload, maximumPayloadFormatVersion)
	}
	return nil
}

func checkStoreCompatibility(ctx context.Context, db *sql.DB) error {
	return VerifyStoreCompatibility(ctx, db)
}

func sqliteImmutableDSN(dbPath string) string {
	values := url.Values{}
	values.Add("mode", "ro")
	values.Add("immutable", "1")
	values.Add("_pragma", "query_only(1)")
	return (&url.URL{Scheme: "file", Path: dbPath, RawQuery: values.Encode()}).String()
}

// initialize creates the store directory, ensures permissions, and applies
// pending migrations. It snapshots the current path at entry and
// delegates to initializeAt so a concurrent SetPath cannot split the
// snapshot and the subsequent open.
func (d *Database) initialize(ctx context.Context) error {
	return d.initializeAt(ctx, d.Path(), false)
}

func (d *Database) initializeAuthorized(ctx context.Context) error {
	return d.initializeAt(ctx, d.Path(), true)
}

func (d *Database) previewOfflineMigrations(ctx context.Context, snapshot string) ([]int64, error) {
	migrations, err := inventoryEmbeddedMigrations(d.migrations)
	if err != nil {
		return nil, xerrors.Errorf("inventory migrations for offline preview: %w", err)
	}
	if snapshot == "" {
		return pendingOfflineMigrations(migrations, map[int64]string{}), nil
	}
	if _, statErr := os.Stat(snapshot); os.IsNotExist(statErr) {
		return pendingOfflineMigrations(migrations, map[int64]string{}), nil
	}
	db, err := openDirectReadOnly(ctx, snapshot)
	if err != nil {
		return nil, xerrors.Errorf("open store for offline preview: %w", err)
	}
	defer func() { _ = db.Close() }()
	applied, err := loadAppliedMigrations(ctx, db)
	if err != nil {
		if schemaMigrationsTableMissing(err) {
			return pendingOfflineMigrations(migrations, map[int64]string{}), nil
		}
		return nil, xerrors.Errorf("load applied migrations for offline preview: %w", err)
	}
	return pendingOfflineMigrations(migrations, applied), nil
}

// initializeAt creates the store directory for the supplied path,
// ensures permissions, and applies pending migrations. Callers that
// already captured a path snapshot earlier in an operation (e.g.
// backup/restore that validated the snapshot before this call) should
// invoke this variant so every step of the operation targets the same
// path, even when SetPath races midway.
func (d *Database) initializeAt(ctx context.Context, snapshot string, allowOffline bool) (err error) {
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

	if allowOffline {
		if err := d.migrateAuthorized(ctx, db); err != nil {
			return xerrors.Errorf("failed to run SQLite migrations: %w", err)
		}
	} else if err := d.migrate(ctx, db); err != nil {
		return xerrors.Errorf("failed to run SQLite migrations: %w", err)
	}
	normCatchUp, normCatchUpErr := catchUpReportWindowNorm(ctx, db, reportWindowNormCatchUpBatchSize)
	logReportWindowNormCatchUp(normCatchUp, normCatchUpErr)
	backfillResult, backfillErr := backfillWorkspaceObservationsBatch(ctx, db, workspaceObservationCatchUpBatchSize)
	if backfillErr != nil {
		// Backfill is additive diagnostic coverage. A malformed historical row
		// must not block normal ingestion after the schema migration succeeded;
		// the next initialization retries the still-missing batch.
		slog.Error("workspace observation backfill incomplete; retrying on next initialization",
			"selected", backfillResult.Selected,
			"inserted", backfillResult.Inserted,
			"retries", backfillResult.Retries,
			"more_pending", backfillResult.MorePending,
			"error", backfillErr,
		)
	} else if backfillResult.Selected > 0 || backfillResult.Retries > 0 {
		slog.Debug("workspace observation backfill completed",
			"selected", backfillResult.Selected,
			"inserted", backfillResult.Inserted,
			"retries", backfillResult.Retries,
			"more_pending", backfillResult.MorePending,
		)
	}

	return nil
}

// sqliteBusyTimeout is deliberately shorter than every packaged host hook
// budget. Contention therefore returns control while the hook process still
// has time to retain its write-ahead spool record instead of being killed at
// the same instant SQLite's wait expires. Operator CLI retries SQLITE_BUSY
// separately; do not raise this constant for search/report/list (#2186).
const sqliteBusyTimeout = 1000

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

func sqliteReadOnlyDSN(dbPath string) string {
	values := url.Values{}
	values.Add("mode", "ro")
	values.Add("_pragma", "query_only(1)")
	values.Add("_pragma", fmt.Sprintf("busy_timeout(%d)", sqliteBusyTimeout))
	values.Add("_pragma", "foreign_keys(1)")
	return (&url.URL{Scheme: "file", Path: dbPath, RawQuery: values.Encode()}).String()
}

// sqliteO1ReadOnlyDSN is mode=ro without a busy wait so a live writer
// turns the large-store doctor probe into an immediate fail-soft.
func sqliteO1ReadOnlyDSN(dbPath string) string {
	values := url.Values{}
	values.Add("mode", "ro")
	values.Add("_pragma", "query_only(1)")
	values.Add("_pragma", "busy_timeout(0)")
	values.Add("_pragma", "foreign_keys(1)")
	return (&url.URL{Scheme: "file", Path: dbPath, RawQuery: values.Encode()}).String()
}

func formatTimestamp(timestamp time.Time) string {
	return timestamp.UTC().Format(time.RFC3339Nano)
}

// nullableString converts a Go string to a value suitable for SQLite
// TEXT columns that distinguish "" from NULL. Empty strings become
// NULL; non-empty strings are bound as-is. Used for columns like
// events.source_hook where empty and NULL mean "no tag" and we want
// the persisted representation to be a single NULL rather than a
// mix of empty strings and NULLs.
func nullableString(value string) any {
	if value == "" {
		return nil
	}
	return value
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
