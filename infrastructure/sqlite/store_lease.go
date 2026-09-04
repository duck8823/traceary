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

	apptypes "github.com/duck8823/traceary/application/types"
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

// compactPendingActiveCheck is the stale-reaping marker consult used by Connect.
// Tests replace it to pause between the one-time consult and the shared acquire.
var compactPendingActiveCheck = CompactPendingActive

// afterMaintenanceMarkerConsult runs after the one-time RW marker consult and
// before the shared acquire (and before the post-pause re-consult).
var afterMaintenanceMarkerConsult func()

// afterSharedLeaseWouldBlock runs on each EWOULDBLOCK/EAGAIN of a shared
// acquire poll so tests can set the marker mid-poll.
var afterSharedLeaseWouldBlock func()

const storeMaintenanceInProgressMessage = "store maintenance in progress (upgrade/compaction holding exclusive lease); retry shortly"

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

func consultMaintenanceMarker(storePath string) error {
	if !compactPendingActiveCheck(storePath) {
		return nil
	}
	return &apptypes.StoreMaintenancePendingError{StorePath: storePath}
}

func selfDeadlockError(lockPath string) error {
	return fmt.Errorf("%w on %s: this process already holds the exclusive lease", errStoreLeaseSelfDeadlock, lockPath)
}

// heldExclusiveLease is attached to connections opened while this process
// already holds LOCK_EX. Close must not unlock that exclusive fd.
type heldExclusiveLease struct{}

func (heldExclusiveLease) Close() error { return nil }

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

// HoldsExclusive reports whether this process already holds LOCK_EX on path.
func (StoreLeaseCoordinator) HoldsExclusive(storePath string) bool {
	lockPath, err := canonicalLeasePath(storePath)
	if err != nil {
		return false
	}
	return processHoldsExclusiveLease(lockPath)
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
	return openCoordinatedDBMode(path, dsn, true)
}

func openCoordinatedReadOnlyDB(path, dsn string) *sql.DB {
	return openCoordinatedDBMode(path, dsn, false)
}

func openCoordinatedDBMode(path, dsn string, readWrite bool) *sql.DB {
	lockPath, lockPathErr := canonicalLeasePath(path)
	return sql.OpenDB(&storeLeaseConnector{driver: coordinatedDriver(), dsn: dsn, storePath: path, lockPath: lockPath, lockPathErr: lockPathErr, readWrite: readWrite})
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
	readWrite   bool
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
		// flock is per open file description. A second LOCK_SH on another
		// fd can never succeed while this process holds LOCK_EX (#2149).
		// Compact still needs SQLite connections for projection complete
		// (#2163). The exclusive fd already excludes cooperating processes.
		return c.openHeldExclusive(ctx)
	}
	if c.readWrite {
		if err := consultMaintenanceMarker(c.storePath); err != nil {
			return nil, err
		}
		if afterMaintenanceMarkerConsult != nil {
			afterMaintenanceMarkerConsult()
		}
		if err := consultMaintenanceMarker(c.storePath); err != nil {
			return nil, err
		}
	}
	ctx, cancel := bindSharedLeaseDeadline(ctx)
	defer cancel()
	var consult func() error
	if c.readWrite {
		consult = func() error { return consultMaintenanceMarker(c.storePath) }
	}
	lease, err := acquireAdvisoryLeaseConsulting(ctx, c.lockPath, false, consult)
	if err != nil {
		var pending *apptypes.StoreMaintenancePendingError
		if errors.As(err, &pending) {
			return nil, pending
		}
		if !c.readWrite && compactPendingActiveCheck(c.storePath) {
			return nil, fmt.Errorf("wait for shared store lease on %s: %w; inspect holders with lsof %s; %s", c.lockPath, err, c.lockPath, storeMaintenanceInProgressMessage)
		}
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

func (c *storeLeaseConnector) openHeldExclusive(ctx context.Context) (driver.Conn, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	conn, err := c.driver.Open(c.dsn)
	if err != nil {
		return nil, err
	}
	if err := validateStoreLinkIdentity(c.storePath); err != nil {
		_ = conn.Close()
		return nil, err
	}
	return &storeLeaseConn{Conn: conn, lease: heldExclusiveLease{}}, nil
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
