//go:build darwin || linux

package runtime_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/duck8823/traceary/application"
	app "github.com/duck8823/traceary/application/runtime"
	apptypes "github.com/duck8823/traceary/application/types"
	"github.com/duck8823/traceary/application/usecase"
	storeDomain "github.com/duck8823/traceary/domain"
	"github.com/duck8823/traceary/domain/model"
	domain "github.com/duck8823/traceary/domain/runtime"
	"github.com/duck8823/traceary/domain/types"
	"github.com/duck8823/traceary/infrastructure/sqlite"
)

func TestHistoryRealCompactionYieldsReadersAndPreservesOwnedPTYWithoutRenewingNativeTarget(t *testing.T) {
	f := historySQLite(t)
	for i, id := range []string{"a", "b", "c", "d", "e"} {
		f.save(t, id, f.base.Add(time.Duration(i+10)*time.Minute), f.repo)
	}
	deliveryEvent := func(id string) *model.Event {
		e := model.EventOfWithSourceHook(types.EventID(id), types.EventKindPrompt, "hook", "codex", "fixture-session", types.Workspace(f.repo), "synthetic prompt receipt", f.base.Add(20*time.Minute), "user_prompt_submit")
		e.SetRawWorkspace(f.repo)
		evidence, err := model.NewHookDeliveryEvidence(e, "event_id:history-compaction-delivery", f.repo)
		if err != nil {
			t.Fatal(err)
		}
		e.SetDeliveryEvidence(evidence)
		return e
	}
	originalDelivery := deliveryEvent("original-hook")
	if err := f.events.Save(context.Background(), originalDelivery); err != nil {
		t.Fatal(err)
	}
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
	var proceedOnce sync.Once
	maintenanceCtx, stopMaintenance := context.WithTimeout(ctx, 25*time.Second)
	compactor := usecase.NewStoreCompactionUsecase(f.path, &sqlite.CompactionFileJournal{Dir: filepath.Join(filepath.Dir(f.path), "compaction-journal")}, &sqlite.SQLiteCompactionBuilder{}, sqlite.StoreReplacementFiles{CallerHoldsExclusiveLease: true}, gatedHistoryMaintenanceLease{admitted, proceed})
	type completedCompaction struct {
		result application.CompactResult
		err    error
	}
	completed := make(chan completedCompaction, 1)
	go func() {
		result, e := compactor.Compact(maintenanceCtx, application.CompactInput{Source: f.path})
		completed <- completedCompaction{result, e}
		close(completed)
	}()
	defer func() {
		proceedOnce.Do(func() { close(proceed) })
		stopMaintenance()
		select {
		case <-completed:
		case <-time.After(5 * time.Second):
			t.Error("actual compaction did not release/join")
		}
	}()
	select {
	case <-admitted:
	case done := <-completed:
		t.Fatal("real compaction failed before lease admission", done.err)
	case <-maintenanceCtx.Done():
		t.Fatal("continuous readers prevented real exclusive maintenance admission")
	}
	for i := 0; i < 3; i++ {
		if _, err = f.history.Page(ctx, f.request); !errors.Is(err, app.ErrRuntimeBusy) {
			t.Fatal("history borrowed or bypassed actual exclusive maintenance", err)
		}
	}
	native.assertLive(t)
	proceedOnce.Do(func() { close(proceed) })
	var compact completedCompaction
	select {
	case compact = <-completed:
	case <-maintenanceCtx.Done():
		t.Fatal("actual compaction did not finish under continuous history queries")
	}
	if compact.err != nil || compact.result.CompactStrategy != application.CompactStrategyReplica || compact.result.Run.Phase != storeDomain.CompactionCommitted {
		t.Fatal("did not perform actual replica compaction", compact.result.CompactStrategy, compact.err)
	}
	newFile, err := os.Stat(f.path)
	if err != nil || os.SameFile(oldFile, newFile) {
		t.Fatal("compaction did not publish a different physical store", err)
	}
	native.assertLive(t)
	if got := f.walk(t); !reflect.DeepEqual(got, expected) {
		t.Fatal("actual compaction/history reconciliation changed legacy membership", got, expected)
	}
	redelivery := deliveryEvent("retry-proposed-new-id")
	if err = f.events.Save(ctx, redelivery); err != nil || redelivery.PersistInserted() || redelivery.EventID() != originalDelivery.EventID() {
		t.Fatal("real compaction changed canonical hook receipt idempotency", err)
	}
	// This late row is older than every row in the pre-compaction completed
	// scan, but still in the requested window. No hint is ever emitted here.
	f.save(t, "late-after-compaction", f.base.Add(time.Minute), f.repo)
	expected = append(expected, "late-after-compaction")
	got := f.walk(t)
	if !reflect.DeepEqual(got, expected) {
		t.Fatal("post-compaction deep late commit/exact redelivery changed history membership", got, expected)
	}
	legacy, err := f.events.ListRecentMetadata(ctx, apptypes.NewEventListCriteriaBuilder(64).Workspace(types.Workspace(f.repo)).From(f.request.Selection.From()).To(f.request.Selection.To()).Build())
	if err != nil {
		t.Fatal(err)
	}
	legacyIDs := make([]string, 0, len(legacy))
	for _, row := range legacy {
		legacyIDs = append(legacyIDs, row.EventID().String())
	}
	if !reflect.DeepEqual(got, legacyIDs) {
		t.Fatal("runtime history diverged from existing CLI metadata query after real compaction")
	}
	fresh, err := f.history.Page(ctx, f.request)
	if err != nil || !fresh.Source().StoreIdentityChanged() {
		t.Fatal("post-compaction read-only source did not expose real replacement", err)
	}
	stale := f.request
	stale.ExpectedWitness = oldPage.Source().Witness()
	if _, err = f.history.Page(ctx, stale); !errors.Is(err, domain.ErrChanged) {
		t.Fatal("stale source continuation survived real exchange", err)
	}
	if err = native.registry.Revalidate(ctx, native.run.Target, string(native.run.Profile.ID())); !errors.Is(err, domain.ErrChanged) {
		t.Fatal("history renewed native target authority after store replacement", err)
	}
	scan.stop(t)
	if scan.successes.Load() == 0 {
		t.Fatal("continuous reader never read a real committed page")
	}
	t.Logf("actual history pages=%d maintenance busy=%d", scan.successes.Load(), scan.busy.Load())
	native.finish(ctx, t)
}
