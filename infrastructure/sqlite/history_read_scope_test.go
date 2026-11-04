//go:build darwin || linux

package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"golang.org/x/xerrors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/duck8823/traceary/application"
	"github.com/duck8823/traceary/application/queryservice"
	apptypes "github.com/duck8823/traceary/application/types"
)

func historyReadFixture(t *testing.T, schema string) (*Database, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "history.db")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(schema); err != nil {
		_ = db.Close()
		t.Fatal(err)
	}
	if err = db.Close(); err != nil {
		t.Fatal(err)
	}
	path, err = filepath.EvalSymlinks(path)
	if err != nil {
		t.Fatal(err)
	}
	return NewDatabase(path, nil), path
}

const supportedHistoryReadSchema = `CREATE TABLE store_format_state(singleton INTEGER PRIMARY KEY, minimum_reader_version INTEGER);
INSERT INTO store_format_state VALUES(1,42);
CREATE TABLE event_metadata_projection(id TEXT PRIMARY KEY,kind TEXT,client TEXT,agent TEXT,session_id TEXT,workspace TEXT,source_hook TEXT,created_at TEXT,created_at_norm TEXT,body_original_bytes INTEGER,body_stored_bytes INTEGER,body_ingest_truncated INTEGER,body_storage_truncated INTEGER,body_metadata_version INTEGER,command_audit_event_id TEXT,command_exit_code INTEGER,command_failed INTEGER);`

func TestYieldingHistoryReadClosesPhysicalConnectionAndRejectsWrites(t *testing.T) {
	d, path := historyReadFixture(t, supportedHistoryReadSchema)
	var used *sql.DB
	ctx := application.WithStoreOpenContext(context.Background(), context.Background())
	err := d.WithYieldingReadScope(ctx, func(ctx context.Context) error {
		deadline, ok := ctx.Deadline()
		if !ok || time.Until(deadline) > 250*time.Millisecond {
			t.Fatal("history read lost its total budget")
		}
		var err error
		used, err = d.openReadOnly(ctx)
		if err != nil {
			return err
		}
		var one int
		if err = used.QueryRowContext(ctx, "SELECT 1").Scan(&one); err != nil {
			return xerrors.Errorf("read history fixture: %w", err)
		}
		if _, err = used.ExecContext(ctx, "CREATE TABLE forbidden(value TEXT)"); err == nil {
			t.Fatal("yielding scope is not read-only")
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if used == nil || used.Stats().OpenConnections != 0 || used.Ping() == nil {
		t.Fatal("reader retained a physical connection")
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	release, err := (StoreLeaseCoordinator{}).AcquireExclusive(ctx, path)
	if err != nil {
		t.Fatal("completed history read blocked maintenance", err)
	}
	release()
}

func TestYieldingHistoryReadRejectsNoncanonicalStoreBeforeOpeningLease(t *testing.T) {
	_, path := historyReadFixture(t, supportedHistoryReadSchema)
	alias := path + ".alias"
	if err := os.Symlink(path, alias); err != nil {
		t.Fatal(err)
	}
	called := false
	err := NewDatabase(alias, nil).WithYieldingReadScope(context.Background(), func(context.Context) error { called = true; return nil })
	if !errors.Is(err, queryservice.ErrHistoryReadUnsupported) || called {
		t.Fatal("history followed noncanonical store alias", err)
	}
	for _, value := range []string{"", "relative.db", filepath.Dir(path), path + "/../history.db"} {
		if err = NewDatabase(value, nil).WithYieldingReadScope(context.Background(), func(context.Context) error { t.Fatal("invalid path entered read scope"); return nil }); !errors.Is(err, queryservice.ErrHistoryReadUnsupported) {
			t.Fatal("invalid store path not rejected", err)
		}
	}
}

func TestYieldingHistoryReadRefusesUninitializedPre36AndUnsupportedStores(t *testing.T) {
	for _, test := range []struct{ name, schema string }{
		{"empty", "PRAGMA user_version=0"},
		{"pre36", "CREATE TABLE schema_migrations(version INTEGER); INSERT INTO schema_migrations VALUES(35);"},
		{"missing projection", "CREATE TABLE store_format_state(singleton INTEGER PRIMARY KEY,minimum_reader_version INTEGER); INSERT INTO store_format_state VALUES(1,42);"},
		{"incomplete projection", "CREATE TABLE store_format_state(singleton INTEGER PRIMARY KEY,minimum_reader_version INTEGER); INSERT INTO store_format_state VALUES(1,42); CREATE TABLE event_metadata_projection(id TEXT);"},
		{"future reader", strings.ReplaceAll(supportedHistoryReadSchema, "VALUES(1,42)", "VALUES(1,9999)")},
	} {
		t.Run(test.name, func(t *testing.T) {
			d, path := historyReadFixture(t, test.schema)
			before, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			called := false
			err = d.WithYieldingReadScope(context.Background(), func(context.Context) error { called = true; return nil })
			if !errors.Is(err, queryservice.ErrHistoryReadUnsupported) || called {
				t.Fatal("unsupported history entered callback", err)
			}
			after, err := os.ReadFile(path)
			if err != nil || string(before) != string(after) {
				t.Fatal("read initialized or migrated store", err)
			}
			if test.name == "empty" || test.name == "pre36" {
				ordinary, openErr := d.openReadOnly(context.Background())
				if openErr != nil {
					t.Fatal("ordinary compatibility bootstrap behavior changed", openErr)
				}
				if closeErr := ordinary.Close(); closeErr != nil {
					t.Fatal(closeErr)
				}
			}
		})
	}
}

func TestYieldingHistoryReadMaintenanceCancelsScopeAndReleasesLease(t *testing.T) {
	d, path := historyReadFixture(t, supportedHistoryReadSchema)
	entered := make(chan struct{})
	readDone := make(chan error, 1)
	go func() {
		readDone <- d.WithYieldingReadScope(context.Background(), func(ctx context.Context) error {
			close(entered)
			<-ctx.Done()
			return ctx.Err()
		})
	}()
	select {
	case <-entered:
	case err := <-readDone:
		t.Fatal("history scope failed before admission", err)
	case <-time.After(2 * time.Second):
		t.Fatal("history scope admission did not finish")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	release, err := (StoreLeaseCoordinator{}).AcquireExclusive(ctx, path)
	if err != nil {
		t.Fatal("active history scope starved exclusive maintenance", err)
	}
	select {
	case err = <-readDone:
		var pending *apptypes.StoreMaintenancePendingError
		if !errors.As(err, &pending) || !errors.Is(err, queryservice.ErrHistoryReadBusy) {
			release()
			t.Fatal("maintenance cancellation hidden", err)
		}
	case <-ctx.Done():
		release()
		t.Fatal("history read did not join before exclusive maintenance")
	}
	called := false
	err = d.WithYieldingReadScope(ctx, func(context.Context) error { called = true; return nil })
	if !errors.Is(err, queryservice.ErrHistoryReadBusy) || called {
		release()
		t.Fatal("history borrowed maintenance's exclusive connection", err)
	}
	release()
	if err = d.WithYieldingReadScope(ctx, func(context.Context) error { return nil }); err != nil {
		t.Fatal("new read could not retry after maintenance", err)
	}
}

func TestYieldingHistoryReadRefusesMarkerAndPreservesOrdinaryReadBehavior(t *testing.T) {
	d, path := historyReadFixture(t, supportedHistoryReadSchema)
	if err := writeCompactPendingMarker(path); err != nil {
		t.Fatal(err)
	}
	defer clearCompactPendingMarker(path)
	called := false
	err := d.WithYieldingReadScope(context.Background(), func(context.Context) error { called = true; return nil })
	var pending *apptypes.StoreMaintenancePendingError
	if !errors.Is(err, queryservice.ErrHistoryReadBusy) || !errors.As(err, &pending) || called {
		t.Fatal("marker did not stop new history reader", err)
	}
	ordinary, err := d.openReadOnly(context.Background())
	if err != nil {
		t.Fatal("ordinary CLI reader semantics changed", err)
	}
	if err = ordinary.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestYieldingHistoryReadActualSQLCancellationDoesNotRetainLease(t *testing.T) {
	d, path := historyReadFixture(t, supportedHistoryReadSchema)
	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Millisecond)
	defer cancel()
	err := d.WithYieldingReadScope(ctx, func(ctx context.Context) error {
		db, err := d.openReadOnly(ctx)
		if err != nil {
			return err
		}
		var value int64
		return db.QueryRowContext(ctx, `WITH RECURSIVE count(x) AS (VALUES(0) UNION ALL SELECT x+1 FROM count WHERE x<1000000000) SELECT SUM(x) FROM count`).Scan(&value)
	})
	if !errors.Is(err, queryservice.ErrHistoryReadBusy) || !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal("actual query ignored bounded deadline", err)
	}
	acquire, cancelAcquire := context.WithTimeout(context.Background(), time.Second)
	defer cancelAcquire()
	release, err := (StoreLeaseCoordinator{}).AcquireExclusive(acquire, path)
	if err != nil {
		t.Fatal("canceled SQL retained its lease", err)
	}
	release()
}

func TestYieldingHistoryReadCannotReuseOrdinaryOrImmutableScopes(t *testing.T) {
	d, _ := historyReadFixture(t, supportedHistoryReadSchema)
	for _, ctx := range []context.Context{context.WithValue(context.Background(), readScopeKey{}, &readScope{})} {
		if err := d.WithYieldingReadScope(ctx, func(context.Context) error { t.Fatal("nested scope bypassed yielding policy"); return nil }); !errors.Is(err, queryservice.ErrHistoryReadUnsupported) {
			t.Fatal(err)
		}
	}
	d.sharedReadOnly = &sql.DB{}
	if err := d.WithYieldingReadScope(context.Background(), func(context.Context) error { t.Fatal("immutable benchmark scope reused"); return nil }); !errors.Is(err, queryservice.ErrHistoryReadUnsupported) {
		t.Fatal(err)
	}
	if err := NewDatabase("", nil).WithYieldingReadScope(context.Background(), nil); err == nil {
		t.Fatal("missing callback accepted")
	}
}

func TestYieldingHistoryReadMissingAndCanceledInputNeverCreatesStore(t *testing.T) {
	path := filepath.Join(t.TempDir(), "missing.db")
	d := NewDatabase(path, nil)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := d.WithYieldingReadScope(ctx, func(context.Context) error { t.Fatal("canceled request read store"); return nil }); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if err := d.WithYieldingReadScope(context.Background(), func(context.Context) error { t.Fatal("missing store initialized"); return nil }); err == nil {
		t.Fatal("missing store read succeeded")
	}
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("history created missing store", err)
	}
	if _, err := os.Stat(path + storeLeaseSuffix); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("missing store created a lease file", err)
	}
}

func TestMetadataBorrowedScopeHandleSurvivesUntilOwnerReturns(t *testing.T) {
	for _, scopeKind := range []string{"yielding", "ordinary", "per-call"} {
		for _, firstKind := range []string{"recent", "window", "context", "timestamp"} {
			t.Run(scopeKind+"/"+firstKind, func(t *testing.T) {
				d, path := historyReadFixture(t, supportedHistoryReadSchema+`INSERT INTO event_metadata_projection(id,kind,client,agent,session_id,workspace,source_hook,created_at,created_at_norm,body_original_bytes,body_stored_bytes,body_ingest_truncated,body_storage_truncated,body_metadata_version,command_failed) VALUES('fixture-metadata','note','codex','fixture-agent','fixture-session','/fixture','fixture-hook','2026-11-04T00:00:00Z','2026-11-04T00:00:00.000000000Z',0,0,0,0,1,0);`)
				datasource := NewEventDatasource(d)
				criteria := apptypes.NewEventListCriteriaBuilder(2).Build()
				var borrowed *sql.DB
				scope := d.WithYieldingReadScope
				switch scopeKind {
				case "ordinary":
					scope = d.WithReadScope
				case "per-call":
					scope = func(ctx context.Context, fn func(context.Context) error) error { return fn(ctx) }
				}
				err := scope(context.Background(), func(ctx context.Context) error {
					var e error
					if scopeKind != "per-call" {
						borrowed, e = d.openReadOnly(ctx)
						if e != nil {
							return e
						}
					}
					count := 0
					switch firstKind {
					case "recent":
						rows, e := datasource.ListRecentMetadata(ctx, criteria)
						if e != nil {
							return e
						}
						count = len(rows)
					case "window":
						rows, e := datasource.ListWindowMetadata(ctx, criteria)
						if e != nil {
							return e
						}
						count = len(rows)
					case "context":
						rows, e := datasource.GetContextMetadata(ctx, apptypes.NewEventContextCriteriaBuilder(2).Build())
						if e != nil {
							return e
						}
						count = len(rows)
					case "timestamp":
						rows, e := datasource.ListRecentTimestampKinds(ctx, apptypes.NewEventListCriteriaBuilder(1).Build())
						if e != nil {
							return e
						}
						count = len(rows)
					}
					if count != 1 {
						return xerrors.Errorf("first metadata query returned %d rows", count)
					}
					rows, e := datasource.ListRecentMetadata(ctx, criteria)
					if e != nil {
						return xerrors.Errorf("second metadata query lost borrowed scope handle: %w", e)
					}
					if len(rows) != 1 || rows[0].EventID().String() != "fixture-metadata" {
						return errors.New("second query changed metadata membership")
					}
					if borrowed != nil {
						var one int
						if e = borrowed.QueryRowContext(ctx, "SELECT 1").Scan(&one); e != nil {
							return xerrors.Errorf("read retained scope handle: %w", e)
						}
					}
					return nil
				})
				if err != nil {
					t.Fatal(err)
				}
				if scopeKind != "per-call" && (borrowed == nil || borrowed.Stats().OpenConnections != 0 || borrowed.Ping() == nil) {
					t.Fatal("scope owner retained the physical connection")
				}
				ctx, cancel := context.WithTimeout(context.Background(), time.Second)
				defer cancel()
				release, err := (StoreLeaseCoordinator{}).AcquireExclusive(ctx, path)
				if err != nil {
					t.Fatal("completed callback retained maintenance lease", err)
				}
				release()
			})
		}
	}
}
