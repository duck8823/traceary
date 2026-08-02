//nolint:revive,wrapcheck // Optional driver interfaces must preserve sentinel errors unchanged.
package sqlite

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"path/filepath"
	"sync"
)

const storeLeaseSuffix = ".traceary.lock"

func canonicalLeasePath(path string) string {
	if absolute, err := filepath.Abs(path); err == nil {
		return absolute + storeLeaseSuffix
	}
	return path + storeLeaseSuffix
}

// StoreLeaseCoordinator gives maintenance exclusive access across processes.
type StoreLeaseCoordinator struct{}

func (StoreLeaseCoordinator) AcquireExclusive(ctx context.Context, path string) (func(), error) {
	lease, err := acquireAdvisoryLease(ctx, canonicalLeasePath(path), true)
	if err != nil {
		return nil, err
	}
	var once sync.Once
	return func() { once.Do(func() { _ = lease.Close() }) }, nil
}

func probeStoreLeaseCapability(ctx context.Context, path string) error {
	lease, err := acquireAdvisoryLease(ctx, canonicalLeasePath(path), true)
	if err != nil {
		return err
	}
	return lease.Close()
}

// openCoordinatedDB makes the shared OS lease lifetime identical to each
// physical driver.Conn lifetime. The stable adjacent lock file survives the
// database inode exchange performed by compaction.
func openCoordinatedDB(path, dsn string) *sql.DB {
	return sql.OpenDB(&storeLeaseConnector{driver: coordinatedSQLiteDriver, dsn: dsn, lockPath: canonicalLeasePath(path)})
}

type storeLeaseConnector struct {
	driver   driver.Driver
	dsn      string
	lockPath string
}

func (c *storeLeaseConnector) Driver() driver.Driver { return c.driver }

func (c *storeLeaseConnector) Connect(ctx context.Context) (driver.Conn, error) {
	lease, err := acquireAdvisoryLease(ctx, c.lockPath, false)
	if err != nil {
		return nil, err
	}
	conn, err := c.driver.Open(c.dsn)
	if err != nil {
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
