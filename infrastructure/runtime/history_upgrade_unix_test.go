//go:build darwin || linux

package runtime_test

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"sync"
	"testing"
	"testing/fstest"
	"time"

	"github.com/duck8823/traceary/application"
	app "github.com/duck8823/traceary/application/runtime"
	apptypes "github.com/duck8823/traceary/application/types"
	"github.com/duck8823/traceary/application/usecase"
	storeDomain "github.com/duck8823/traceary/domain"
	domain "github.com/duck8823/traceary/domain/runtime"
	"github.com/duck8823/traceary/domain/types"
	"github.com/duck8823/traceary/infrastructure/sqlite"
	schema "github.com/duck8823/traceary/schema/sqlite"
)

func TestHistoryRealPreparedUpgradePreservesOwnedPTYAndReconcilesNewStore(t *testing.T) {
	all, err := schema.Migrations()
	if err != nil {
		t.Fatal(err)
	}
	paths, err := fs.Glob(all, "*.sql")
	if err != nil {
		t.Fatal(err)
	}
	legacy := fstest.MapFS{}
	for _, path := range paths {
		var version int
		if _, err = fmt.Sscanf(filepath.Base(path), "%06d_", &version); err != nil {
			t.Fatal(err)
		}
		if version >= 87 {
			continue
		}
		data, e := fs.ReadFile(all, path)
		if e != nil {
			t.Fatal(e)
		}
		legacy[path] = &fstest.MapFile{Data: data, Mode: 0600}
	}
	f := historySQLiteWithMigrations(t, legacy)
	f.save(t, "before-upgrade-a", f.base.Add(10*time.Minute), f.repo)
	f.save(t, "before-upgrade-b", f.base.Add(20*time.Minute), f.repo)
	expected := f.walk(t)
	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Second)
	defer cancel()
	native := startHistoryMaintenancePTY(ctx, t, f)
	native.assertLive(t)
	oldPage, err := f.history.Page(ctx, f.request)
	if err != nil {
		t.Fatal(err)
	}
	oldFile, err := os.Stat(f.path)
	if err != nil {
		t.Fatal(err)
	}
	scan := startHistoryMaintenanceScan(ctx, t, f)
	admitted := make(chan struct{})
	proceed := make(chan struct{})
	var once sync.Once
	lease := gatedHistoryMaintenanceLease{admitted, proceed}
	journal := &sqlite.PreparedStoreUpgradeFileJournal{Dir: filepath.Join(filepath.Dir(f.path), "upgrade-journal")}
	recipe := &sqlite.PreparedUpgradeMigrationRecipe{PreparedMigrationCandidateRecipe: sqlite.PreparedMigrationCandidateRecipe{Migrations: all, Verifier: sqlite.PreparedMigrationVerifier{Migrations: all}}}
	service := usecase.NewPreparedStoreUpgradeUsecase(f.path, journal, sqlite.PreparedStoreUpgradeFiles{CallerHoldsExclusiveLease: true}, lease, map[storeDomain.PreparedStoreUpgradeOperation]application.PreparedCandidateRecipe{storeDomain.PreparedStoreUpgradeOperationOfflineMigrationUpgrade: recipe})
	command := application.PreparedStoreUpgradeCommand{Operation: storeDomain.PreparedStoreUpgradeOperationOfflineMigrationUpgrade, TargetPath: f.path, ConsumerBinding: "runtime-history-fixture", Budget: storeDomain.PreparedStoreUpgradeBudget{WallTimeLimit: 30 * time.Second, PublishLockLimit: 25 * time.Second, OwnedDiskByteLimit: 128 << 20, WALByteLimit: 32 << 20, TemporaryByteLimit: 64 << 20, SafetyMarginBytes: 4 << 20}}
	upgradeCtx, stop := context.WithCancel(ctx)
	type result struct {
		receipt application.PreparedStoreUpgradeReceipt
		err     error
	}
	done := make(chan result, 1)
	go func() { receipt, e := service.RunUpgrade(upgradeCtx, command); done <- result{receipt, e}; close(done) }()
	defer func() {
		once.Do(func() { close(proceed) })
		stop()
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Error("real upgrade did not release/join")
		}
	}()
	select {
	case <-admitted:
	case r := <-done:
		t.Fatal("upgrade failed before real exclusive admission", r.err)
	case <-ctx.Done():
		t.Fatal("real upgrade could not acquire exclusive lease")
	}
	for i := 0; i < 3; i++ {
		if _, err = f.history.Page(ctx, f.request); !errors.Is(err, app.ErrRuntimeBusy) {
			t.Fatal("history bypassed actual upgrade lease", err)
		}
	}
	native.assertLive(t)
	once.Do(func() { close(proceed) })
	var upgraded result
	select {
	case upgraded = <-done:
	case <-ctx.Done():
		t.Fatal("real prepared upgrade failed to finish")
	}
	if upgraded.err != nil {
		t.Fatal(upgraded.err)
	}
	receipt := upgraded.receipt
	run, err := journal.Load(ctx, receipt.RunID)
	if err != nil || run.Phase != storeDomain.PreparedStoreUpgradeCommitted || receipt.Evidence.MigrationSetDigest == "" || receipt.Evidence.MigrationSetDigest != run.PlanDigest {
		t.Fatal("upgrade lacked durable committed/bound migration evidence", err)
	}
	newFile, err := os.Stat(f.path)
	if err != nil || os.SameFile(oldFile, newFile) {
		t.Fatal("actual upgrade did not exchange physical source", err)
	}
	rollback, err := os.Stat(receipt.RollbackPath)
	if err != nil || !os.SameFile(oldFile, rollback) {
		t.Fatal("actual upgrade did not retain original rollback inode", err)
	}
	native.assertLive(t)
	// Read-only assertion only AFTER production publication released its lease.
	check, err := sql.Open("sqlite", "file:"+filepath.ToSlash(f.path)+"?mode=ro")
	if err != nil {
		t.Fatal(err)
	}
	var latest, columns int
	err = check.QueryRowContext(ctx, `SELECT MAX(version) FROM schema_migrations`).Scan(&latest)
	if err == nil {
		err = check.QueryRowContext(ctx, `SELECT COUNT(*) FROM pragma_table_info('consolidation_requests') WHERE name='prompt_state'`).Scan(&columns)
	}
	closeErr := check.Close()
	if err != nil || closeErr != nil || latest != 87 || columns != 1 {
		t.Fatal("actual pending migration87 did not apply", err, closeErr, latest, columns)
	}
	if got := f.walk(t); !reflect.DeepEqual(got, expected) {
		t.Fatal("actual upgrade changed committed requested-view membership", got, expected)
	}
	fresh, err := f.history.Page(ctx, f.request)
	if err != nil || !fresh.Source().StoreIdentityChanged() {
		t.Fatal("fresh history did not expose actual upgraded source", err)
	}
	stale := f.request
	stale.ExpectedWitness = oldPage.Source().Witness()
	if _, err = f.history.Page(ctx, stale); !errors.Is(err, domain.ErrChanged) {
		t.Fatal("old source witness survived real upgrade", err)
	}
	if err = native.registry.Revalidate(ctx, native.run.Target, string(native.run.Profile.ID())); !errors.Is(err, domain.ErrChanged) {
		t.Fatal("history renewed native target after upgrade", err)
	}
	// Current CLI composition uses the full current migration set after upgrade.
	f.events = sqlite.NewEventDatasource(sqlite.NewDatabase(f.path, all))
	f.save(t, "deep-late-after-upgrade", f.base.Add(time.Minute), f.repo)
	expected = append(expected, "deep-late-after-upgrade")
	got := f.walk(t)
	if !reflect.DeepEqual(got, expected) {
		t.Fatal("hint-free post-upgrade deep late commit was lost", got, expected)
	}
	rows, err := f.events.ListRecentMetadata(ctx, apptypes.NewEventListCriteriaBuilder(64).Workspace(types.Workspace(f.repo)).From(f.request.Selection.From()).To(f.request.Selection.To()).Build())
	if err != nil {
		t.Fatal(err)
	}
	var ids []string
	for _, row := range rows {
		ids = append(ids, row.EventID().String())
	}
	if !reflect.DeepEqual(got, ids) {
		t.Fatal("runtime/CLI membership diverged after actual upgrade")
	}
	scan.stop(t)
	if scan.successes.Load() == 0 {
		t.Fatal("continuous reader never read a committed real source")
	}
	t.Logf("actual upgrade history pages=%d maintenance busy=%d", scan.successes.Load(), scan.busy.Load())
	native.finish(ctx, t)
}
