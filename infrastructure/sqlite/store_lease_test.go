package sqlite

import (
	"context"
	"database/sql/driver"
	"path/filepath"
	"testing"
	"time"
)

func TestStoreLeaseConnector_HoldsSharedLeaseForPhysicalConnection(t *testing.T) {
	path := filepath.Join(t.TempDir(), "store.db")
	db := openCoordinatedDB(path, sqliteDSN(path))
	db.SetMaxIdleConns(0)
	conn, err := db.Conn(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	acquired := make(chan struct{})
	go func() {
		lease := coordinatedStoreLease(path)
		lease.Lock()
		close(acquired)
		lease.Unlock()
	}()
	select {
	case <-acquired:
		t.Fatal("exclusive lease acquired while physical connection was open")
	case <-time.After(20 * time.Millisecond):
	}
	if err := conn.Close(); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	select {
	case <-acquired:
	case <-time.After(time.Second):
		t.Fatal("exclusive lease did not acquire after close")
	}
}

func TestDatabaseOpenParticipatesInExclusiveStoreLease(t *testing.T) {
	path := filepath.Join(t.TempDir(), "store.db")
	database := NewDatabase(path, nil)
	db, err := database.openAt(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxIdleConns(0)
	conn, err := db.Conn(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	acquired := make(chan struct{})
	go func() { lease := coordinatedStoreLease(path); lease.Lock(); close(acquired); lease.Unlock() }()
	select {
	case <-acquired:
		t.Fatal("exclusive lease bypassed a normal Database connection")
	case <-time.After(20 * time.Millisecond):
	}
	if err := conn.Close(); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	select {
	case <-acquired:
	case <-time.After(time.Second):
		t.Fatal("exclusive lease did not acquire")
	}
}

func TestStoreLeaseConn_PreservesOptionalInterfaces(t *testing.T) {
	var conn any = &storeLeaseConn{}
	for name, ok := range map[string]bool{
		"ConnBeginTx":        implements[driver.ConnBeginTx](conn),
		"ConnPrepareContext": implements[driver.ConnPrepareContext](conn),
		"ExecerContext":      implements[driver.ExecerContext](conn),
		"QueryerContext":     implements[driver.QueryerContext](conn),
		"Pinger":             implements[driver.Pinger](conn),
		"SessionResetter":    implements[driver.SessionResetter](conn),
		"Validator":          implements[driver.Validator](conn),
		"NamedValueChecker":  implements[driver.NamedValueChecker](conn),
	} {
		if !ok {
			t.Errorf("optional interface %s was lost", name)
		}
	}
}

func implements[T any](value any) bool { _, ok := value.(T); return ok }
