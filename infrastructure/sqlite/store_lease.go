package sqlite

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"path/filepath"
	"sync"

	modernsqlite "modernc.org/sqlite"
)

var storeLeaseRegistry = struct {
	sync.Mutex
	byPath map[string]*sync.RWMutex
}{byPath: make(map[string]*sync.RWMutex)}

func coordinatedStoreLease(path string) *sync.RWMutex {
	storeLeaseRegistry.Lock()
	defer storeLeaseRegistry.Unlock()
	path = canonicalLeasePath(path)
	if lease := storeLeaseRegistry.byPath[path]; lease != nil {
		return lease
	}
	lease := &sync.RWMutex{}
	storeLeaseRegistry.byPath[path] = lease
	return lease
}

// StoreLeaseCoordinator gives the maintenance use case exclusive access.
type StoreLeaseCoordinator struct{}

func (StoreLeaseCoordinator) AcquireExclusive(ctx context.Context, path string) (func(), error) {
	lease := coordinatedStoreLease(path)
	acquired := make(chan struct{})
	go func() { lease.Lock(); close(acquired) }()
	select {
	case <-ctx.Done():
		// The goroutine must eventually acquire and release; otherwise a canceled
		// waiter could permanently seize the process-local lease.
		go func() { <-acquired; lease.Unlock() }()
		return nil, ctx.Err()
	case <-acquired:
		var once sync.Once
		return func() { once.Do(lease.Unlock) }, nil
	}
}

func canonicalLeasePath(path string) string {
	if absolute, err := filepath.Abs(path); err == nil {
		return absolute
	}
	return path
}

// openCoordinatedDB makes the lease lifetime identical to each physical
// driver.Conn lifetime. Holding it only around sql.DB operations would be
// unsafe because database/sql pools connections after a call returns.
func openCoordinatedDB(path, dsn string) *sql.DB {
	return sql.OpenDB(&storeLeaseConnector{
		driver: &modernsqlite.Driver{},
		dsn:    dsn,
		lease:  coordinatedStoreLease(path),
	})
}

type storeLeaseConnector struct {
	driver driver.Driver
	dsn    string
	lease  *sync.RWMutex
}

func (c *storeLeaseConnector) Driver() driver.Driver { return c.driver }

func (c *storeLeaseConnector) Connect(_ context.Context) (driver.Conn, error) {
	c.lease.RLock()
	conn, err := c.driver.Open(c.dsn)
	if err != nil {
		c.lease.RUnlock()
		return nil, err
	}
	return &storeLeaseConn{Conn: conn, release: c.lease.RUnlock}, nil
}

type storeLeaseConn struct {
	driver.Conn
	once    sync.Once
	release func()
}

func (c *storeLeaseConn) Close() error {
	err := c.Conn.Close()
	c.once.Do(c.release)
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
