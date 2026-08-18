//nolint:revive,wrapcheck // Optional driver interfaces must preserve sentinel errors unchanged.
package sqlite

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"fmt"
	"path/filepath"
	"sync"
	"time"
)

const storeLeaseSuffix = ".traceary.lock"

// exclusiveLeaseAcquireTimeout bounds exclusive flock polling when the
// caller did not set a deadline. A same-process idle shared lease used
// to wait forever (#2120). Shared acquire uses the same default (#2149).
const exclusiveLeaseAcquireTimeout = 60 * time.Second

const sharedLeaseAcquireTimeout = exclusiveLeaseAcquireTimeout

// errStoreLeaseSelfDeadlock is returned when this process already holds
// the exclusive lease and then requests a shared flock on a second fd.
// flock is per open file description; that poll can never succeed (#2149).
var errStoreLeaseSelfDeadlock = errors.New("store lease self-deadlock")

var exclusiveLeaseWaitInterval = 10 * time.Second

var exclusiveLeaseWaitReporter func(waited time.Duration, lockPath string)

var sharedLeaseWaitReporter func(waited time.Duration, lockPath string)

// exclusiveHeldByProcess tracks lock paths whose exclusive fd is held
// by this process. Connect consults it before polling LOCK_SH.
var exclusiveHeldByProcess sync.Map

// SetExclusiveLeaseWaitReporter installs a stderr-style waiter hook.
func SetExclusiveLeaseWaitReporter(fn func(waited time.Duration, lockPath string)) {
	exclusiveLeaseWaitReporter = fn
}

// SetSharedLeaseWaitReporter installs a stderr-style waiter hook for
// shared flock polling.
func SetSharedLeaseWaitReporter(fn func(waited time.Duration, lockPath string)) {
	sharedLeaseWaitReporter = fn
}

func bindExclusiveLeaseDeadline(ctx context.Context) (context.Context, context.CancelFunc) {
	if _, hasDeadline := ctx.Deadline(); hasDeadline {
		return ctx, func() {}
	}
	return context.WithTimeout(ctx, exclusiveLeaseAcquireTimeout)
}

func bindSharedLeaseDeadline(ctx context.Context) (context.Context, context.CancelFunc) {
	if _, hasDeadline := ctx.Deadline(); hasDeadline {
		return ctx, func() {}
	}
	return context.WithTimeout(ctx, sharedLeaseAcquireTimeout)
}

func processHoldsExclusiveLease(lockPath string) bool {
	_, ok := exclusiveHeldByProcess.Load(lockPath)
	return ok
}

func selfDeadlockError(lockPath string) error {
	return fmt.Errorf("%w on %s: this process already holds the exclusive lease", errStoreLeaseSelfDeadlock, lockPath)
}

func canonicalStorePath(path string) (string, error) {
	absolute, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	resolved, err := filepath.EvalSymlinks(absolute)
	if err == nil {
		return resolved, nil
	}
	resolvedParent, parentErr := filepath.EvalSymlinks(filepath.Dir(absolute))
	if parentErr != nil {
		return "", parentErr
	}
	return filepath.Join(resolvedParent, filepath.Base(absolute)), nil
}

func canonicalLeasePath(path string) (string, error) {
	canonical, err := canonicalStorePath(path)
	if err != nil {
		return "", err
	}
	return canonical + storeLeaseSuffix, nil
}

// StoreLeaseCoordinator gives maintenance exclusive access across processes.
type StoreLeaseCoordinator struct{}

func (StoreLeaseCoordinator) AcquireExclusive(ctx context.Context, path string) (func(), error) {
	canonical, err := canonicalStorePath(path)
	if err != nil {
		return nil, err
	}
	lockPath := canonical + storeLeaseSuffix
	_ = writeCompactPendingMarker(canonical)
	ctx, cancel := bindExclusiveLeaseDeadline(ctx)
	defer cancel()
	lease, err := acquireAdvisoryLease(ctx, lockPath, true)
	if err != nil {
		clearCompactPendingMarker(canonical)
		if holders := describeStoreLeaseHolders(lockPath); holders != "" {
			return nil, fmt.Errorf("wait for exclusive store lease on %s: %w; held by %s", lockPath, err, holders)
		}
		return nil, fmt.Errorf("wait for exclusive store lease on %s: %w; inspect holders with lsof %s", lockPath, err, lockPath)
	}
	if err := validateStoreLinkIdentity(canonical); err != nil {
		clearCompactPendingMarker(canonical)
		_ = lease.Close()
		return nil, err
	}
	exclusiveHeldByProcess.Store(lockPath, struct{}{})
	var once sync.Once
	return func() {
		once.Do(func() {
			exclusiveHeldByProcess.Delete(lockPath)
			clearCompactPendingMarker(canonical)
			_ = lease.Close()
		})
	}, nil
}

func probeStoreLeaseCapability(ctx context.Context, path string) error {
	lockPath, err := canonicalLeasePath(path)
	if err != nil {
		return err
	}
	lease, err := acquireAdvisoryLease(ctx, lockPath, true)
	if err != nil {
		return err
	}
	return lease.Close()
}

// openCoordinatedDB makes the shared OS lease lifetime identical to each
// physical driver.Conn lifetime. The stable adjacent lock file survives the
// database inode exchange performed by compaction.
func openCoordinatedDB(path, dsn string) *sql.DB {
	lockPath, lockPathErr := canonicalLeasePath(path)
	return sql.OpenDB(&storeLeaseConnector{driver: coordinatedSQLiteDriver, dsn: dsn, storePath: path, lockPath: lockPath, lockPathErr: lockPathErr})
}

// OpenCoordinatedSQLite opens a live store through the shared physical-
// connection lease. Copied rehearsal databases and compactor-owned candidates
// intentionally use direct openers instead.
func OpenCoordinatedSQLite(path, dsn string) *sql.DB { return openCoordinatedDB(path, dsn) }

type storeLeaseConnector struct {
	driver      driver.Driver
	dsn         string
	storePath   string
	lockPath    string
	lockPathErr error
}

func (c *storeLeaseConnector) Driver() driver.Driver { return c.driver }

func (c *storeLeaseConnector) Connect(ctx context.Context) (driver.Conn, error) {
	if c.lockPathErr != nil {
		return nil, c.lockPathErr
	}
	if err := validateStoreLinkIdentity(c.storePath); err != nil {
		return nil, err
	}
	if processHoldsExclusiveLease(c.lockPath) {
		return nil, selfDeadlockError(c.lockPath)
	}
	ctx, cancel := bindSharedLeaseDeadline(ctx)
	defer cancel()
	lease, err := acquireAdvisoryLease(ctx, c.lockPath, false)
	if err != nil {
		return nil, fmt.Errorf("wait for shared store lease on %s: %w; inspect holders with lsof %s", c.lockPath, err, c.lockPath)
	}
	conn, err := c.driver.Open(c.dsn)
	if err != nil {
		_ = lease.Close()
		return nil, err
	}
	if err := validateStoreLinkIdentity(c.storePath); err != nil {
		_ = conn.Close()
		_ = lease.Close()
		return nil, err
	}
	return &storeLeaseConn{Conn: conn, lease: lease}, nil
}

type storeLeaseConn struct {
	driver.Conn
	once  sync.Once
	lease advisoryLease
}

func (c *storeLeaseConn) Close() error {
	err := c.Conn.Close()
	c.once.Do(func() { _ = c.lease.Close() })
	return err
}

func (c *storeLeaseConn) BeginTx(ctx context.Context, opts driver.TxOptions) (driver.Tx, error) {
	if conn, ok := c.Conn.(driver.ConnBeginTx); ok {
		return conn.BeginTx(ctx, opts)
	}
	return nil, driver.ErrSkip
}
func (c *storeLeaseConn) PrepareContext(ctx context.Context, query string) (driver.Stmt, error) {
	if conn, ok := c.Conn.(driver.ConnPrepareContext); ok {
		return conn.PrepareContext(ctx, query)
	}
	return nil, driver.ErrSkip
}
func (c *storeLeaseConn) ExecContext(ctx context.Context, query string, args []driver.NamedValue) (driver.Result, error) {
	if conn, ok := c.Conn.(driver.ExecerContext); ok {
		return conn.ExecContext(ctx, query, args)
	}
	return nil, driver.ErrSkip
}
func (c *storeLeaseConn) QueryContext(ctx context.Context, query string, args []driver.NamedValue) (driver.Rows, error) {
	if conn, ok := c.Conn.(driver.QueryerContext); ok {
		return conn.QueryContext(ctx, query, args)
	}
	return nil, driver.ErrSkip
}
func (c *storeLeaseConn) Ping(ctx context.Context) error {
	if conn, ok := c.Conn.(driver.Pinger); ok {
		return conn.Ping(ctx)
	}
	return driver.ErrSkip
}
func (c *storeLeaseConn) ResetSession(ctx context.Context) error {
	if conn, ok := c.Conn.(driver.SessionResetter); ok {
		return conn.ResetSession(ctx)
	}
	return nil
}
func (c *storeLeaseConn) IsValid() bool {
	if conn, ok := c.Conn.(driver.Validator); ok {
		return conn.IsValid()
	}
	return true
}
func (c *storeLeaseConn) CheckNamedValue(value *driver.NamedValue) error {
	if conn, ok := c.Conn.(driver.NamedValueChecker); ok {
		return conn.CheckNamedValue(value)
	}
	return driver.ErrSkip
}
