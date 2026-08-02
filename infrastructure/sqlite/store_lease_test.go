package sqlite

import (
	"bufio"
	"context"
	"database/sql/driver"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
	"time"
)

func TestStoreLeaseMultiprocessContentionAndCrashRelease(t *testing.T) {
	if runtime.GOOS != "darwin" && runtime.GOOS != "linux" {
		t.Skip("advisory lease unsupported")
	}
	path := filepath.Join(t.TempDir(), "store.db")
	cmd := exec.Command(os.Args[0], "-test.run=^TestStoreLeaseHelperProcess$")
	cmd.Env = append(os.Environ(), "TRACEARY_STORE_LEASE_HELPER=1", "TRACEARY_STORE_LEASE_PATH="+path)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = stdin.Close() }()
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	scanner := bufio.NewScanner(stdout)
	ready := false
	for scanner.Scan() {
		if scanner.Text() == "ready" {
			ready = true
			break
		}
	}
	if !ready {
		t.Fatal("helper did not acquire shared lease")
	}
	candidate := path + ".candidate"
	if err := os.WriteFile(candidate, []byte("replacement"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := atomicExchange(path, candidate); err != nil {
		t.Skipf("atomic exchange unavailable: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	if _, err := (StoreLeaseCoordinator{}).AcquireExclusive(ctx, path); err == nil {
		t.Fatal("exclusive lease bypassed another process")
	}
	otherRelease, err := (StoreLeaseCoordinator{}).AcquireExclusive(context.Background(), path+".other")
	if err != nil {
		t.Fatalf("unrelated store was blocked: %v", err)
	}
	otherRelease()
	if err := cmd.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	_ = cmd.Wait()
	release, err := (StoreLeaseCoordinator{}).AcquireExclusive(context.Background(), path)
	if err != nil {
		t.Fatalf("crashed process retained lease: %v", err)
	}
	release()
}

func TestStoreLeaseHelperProcess(t *testing.T) {
	if os.Getenv("TRACEARY_STORE_LEASE_HELPER") != "1" {
		t.Skip("helper only")
	}
	path := os.Getenv("TRACEARY_STORE_LEASE_PATH")
	db := openCoordinatedDB(path, sqliteDSN(path))
	conn, err := db.Conn(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.Close() }()
	fmt.Println("ready")
	_, _ = io.ReadAll(os.Stdin)
}

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
		release, acquireErr := (StoreLeaseCoordinator{}).AcquireExclusive(context.Background(), path)
		if acquireErr != nil {
			return
		}
		close(acquired)
		release()
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
	go func() {
		release, acquireErr := (StoreLeaseCoordinator{}).AcquireExclusive(context.Background(), path)
		if acquireErr != nil {
			return
		}
		close(acquired)
		release()
	}()
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

func TestStoreLeaseExclusiveAcquisitionHonorsContext(t *testing.T) {
	path := filepath.Join(t.TempDir(), "store.db")
	db := openCoordinatedDB(path, sqliteDSN(path))
	conn, err := db.Conn(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	if _, err := (StoreLeaseCoordinator{}).AcquireExclusive(ctx, path); err == nil {
		t.Fatal("exclusive acquisition ignored shared holder")
	}
	_ = conn.Close()
	_ = db.Close()
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
