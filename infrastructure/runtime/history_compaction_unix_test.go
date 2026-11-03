//go:build darwin || linux

package runtime_test

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/duck8823/traceary/application"
	app "github.com/duck8823/traceary/application/runtime"
	apptypes "github.com/duck8823/traceary/application/types"
	"github.com/duck8823/traceary/application/usecase"
	"github.com/duck8823/traceary/domain/model"
	domain "github.com/duck8823/traceary/domain/runtime"
	"github.com/duck8823/traceary/domain/types"
	infra "github.com/duck8823/traceary/infrastructure/runtime"
	"github.com/duck8823/traceary/infrastructure/sqlite"
	"golang.org/x/xerrors"
)

// This gate owns the REAL exclusive lease; it only pauses the maintenance
// caller after actual acquisition, never replaces SQLite/flock with a fake.
type gatedHistoryCompactionLease struct {
	admitted chan struct{}
	proceed  <-chan struct{}
}

func (g gatedHistoryCompactionLease) AcquireExclusive(ctx context.Context, path string) (func(), error) {
	release, err := (sqlite.StoreLeaseCoordinator{}).AcquireExclusive(ctx, path)
	if err != nil {
		return nil, xerrors.Errorf("acquire real fixture maintenance lease: %w", err)
	}
	close(g.admitted)
	select {
	case <-g.proceed:
		return release, nil
	case <-ctx.Done():
		release()
		return nil, xerrors.Errorf("release canceled fixture maintenance gate: %w", ctx.Err())
	}
}

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
	metadataRoot := filepath.Join(filepath.Dir(f.path), "metadata")
	metadata, err := infra.OpenRegistryStore(metadataRoot, infra.MaxRegistryBytes)
	if err != nil {
		t.Fatal(err)
	}
	registry, err := app.NewRegistry(metadata, infra.RegistryResolver{})
	if err != nil {
		t.Fatal(err)
	}
	journal, err := infra.OpenExecutionStore(metadataRoot, infra.MaxRegistryBytes)
	if err != nil {
		t.Fatal(err)
	}
	result := filepath.Join(filepath.Dir(f.path), "maintenance-child.json")
	releaseChild := result + ".release"
	launcher, err := infra.NewPTYLauncher(registry, []string{"TRACEARY_PTY_FIXTURE=1", "FIXTURE_OUTPUT_MODE=maintenance", "FIXTURE_RESULT=" + result, "FIXTURE_RELEASE=" + releaseChild, "GORACE=atexit_sleep_ms=0"})
	if err != nil {
		t.Fatal(err)
	}
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	profile, err := launcher.InspectProfile(ctx, "codex-fixture", "codex", executable, "fixture 1")
	if err != nil {
		t.Fatal(err)
	}
	if err = journal.RegisterProfile(ctx, profile); err != nil {
		t.Fatal(err)
	}
	epoch := strings.Repeat("3", 32)
	owner, err := app.NewRuntime(registry, journal, launcher, journal, epoch)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		owner.DisableLaunches()
		_ = os.WriteFile(releaseChild, []byte("release finite owned fixture"), 0600)
		cleanup, stop := context.WithTimeout(context.Background(), 5*time.Second)
		defer stop()
		if e := owner.AwaitIdle(cleanup); e != nil {
			t.Errorf("owned PTY cleanup did not reconcile: %v", e)
		}
	}()
	run, err := owner.Launch(ctx, app.LaunchRequest{Epoch: epoch, Worktree: f.request.Worktree, Profile: profile.ID(), RequestID: "history-real-maintenance"})
	if err != nil {
		t.Fatal(err)
	}
	readyDeadline := time.Now().Add(3 * time.Second)
	for {
		if _, e := os.Stat(result + ".maintenance-ready"); e == nil {
			break
		}
		if time.Now().After(readyDeadline) {
			t.Fatal("finite maintenance PTY fixture was not ready")
		}
		time.Sleep(5 * time.Millisecond)
	}
	assertOwnedLive := func() {
		t.Helper()
		runs := owner.List()
		if len(runs) != 1 || runs[0].Identity != run.Identity || runs[0].State != domain.RunLive {
			t.Fatal("maintenance closed/replaced the actual owned PTY")
		}
	}
	assertOwnedLive()
	oldPage, err := f.history.Page(ctx, f.request)
	if err != nil {
		t.Fatal(err)
	}
	oldFile, err := os.Stat(f.path)
	if err != nil {
		t.Fatal(err)
	}
	scanCtx, stopScan := context.WithCancel(ctx)
	scanDone := make(chan error, 1)
	scanReady := make(chan struct{})
	var scanReadyOnce sync.Once
	var successes, busy atomic.Int64
	go func() {
		defer close(scanDone)
		for {
			_, e := f.history.Page(scanCtx, f.request)
			if e == nil {
				successes.Add(1)
				scanReadyOnce.Do(func() { close(scanReady) })
			} else if errors.Is(e, app.ErrRuntimeBusy) {
				busy.Add(1)
			} else if errors.Is(e, context.Canceled) || errors.Is(e, context.DeadlineExceeded) {
				scanDone <- nil
				return
			} else if !errors.Is(e, domain.ErrChanged) {
				scanDone <- e
				return
			}
			select {
			case <-scanCtx.Done():
				scanDone <- nil
				return
			case <-time.After(time.Millisecond):
			}
		}
	}()
	defer func() {
		stopScan()
		select {
		case e := <-scanDone:
			if e != nil {
				t.Errorf("continuous actual history read failed: %v", e)
			}
		case <-time.After(3 * time.Second):
			t.Error("history reader did not release/join")
		}
	}()
	select {
	case <-scanReady:
	case e := <-scanDone:
		t.Fatal("continuous reader stopped before maintenance", e)
	case <-ctx.Done():
		t.Fatal("continuous reader was not ready")
	}
	admitted := make(chan struct{})
	proceed := make(chan struct{})
	var proceedOnce sync.Once
	maintenanceCtx, stopMaintenance := context.WithTimeout(ctx, 25*time.Second)
	compactor := usecase.NewStoreCompactionUsecase(f.path, &sqlite.CompactionFileJournal{Dir: filepath.Join(filepath.Dir(f.path), "compaction-journal")}, &sqlite.SQLiteCompactionBuilder{}, sqlite.StoreReplacementFiles{CallerHoldsExclusiveLease: true}, gatedHistoryCompactionLease{admitted, proceed})
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
	assertOwnedLive()
	proceedOnce.Do(func() { close(proceed) })
	var compact completedCompaction
	select {
	case compact = <-completed:
	case <-maintenanceCtx.Done():
		t.Fatal("actual compaction did not finish under continuous history queries")
	}
	if compact.err != nil || compact.result.CompactStrategy != application.CompactStrategyReplica {
		t.Fatal("did not perform actual replica compaction", compact.result.CompactStrategy, compact.err)
	}
	newFile, err := os.Stat(f.path)
	if err != nil || os.SameFile(oldFile, newFile) {
		t.Fatal("compaction did not publish a different physical store", err)
	}
	assertOwnedLive()
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
	if err = registry.Revalidate(ctx, run.Target, string(profile.ID())); !errors.Is(err, domain.ErrChanged) {
		t.Fatal("history renewed native target authority after store replacement", err)
	}
	// Explicitly stop/join the continuous consumer before inspecting counts.
	stopScan()
	select {
	case err = <-scanDone:
	case <-time.After(3 * time.Second):
		t.Fatal("history consumer failed to join")
	}
	if err != nil {
		t.Fatal(err)
	}
	if successes.Load() == 0 {
		t.Fatal("continuous reader never read a real committed page")
	}
	// busy is advisory timing evidence; the three synchronous calls above are
	// the deterministic actual-lease proof, not a scheduling-dependent count.
	t.Logf("actual history pages=%d maintenance busy=%d", successes.Load(), busy.Load())
	if err = os.WriteFile(releaseChild, []byte("release after actual compaction"), 0600); err != nil {
		t.Fatal(err)
	}
	if err = owner.AwaitIdle(ctx); err != nil {
		t.Fatal(err)
	}
	runs := owner.List()
	if len(runs) != 1 || runs[0].State != domain.RunExited || runs[0].ExitCode != 7 {
		t.Fatal("finite PTY did not survive through real compaction to owned exit")
	}
	sub, err := owner.Attach(app.OutputSelection{Identity: run.Identity, Worktree: f.request.Worktree, Profile: profile.ID()}, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer sub.Close()
	var captured bytes.Buffer
	ended := false
	for frames := 0; frames < 8; frames++ {
		frame, e := sub.Next(ctx)
		if e != nil {
			t.Fatal(e)
		}
		if frame.Kind == "gap" {
			t.Fatal("maintenance lost finite PTY capture")
		}
		_, _ = captured.Write(frame.Data)
		if frame.Kind == "end" {
			ended = true
			break
		}
	}
	if !ended || !bytes.Contains(captured.Bytes(), []byte("BEFORE")) || !bytes.Contains(captured.Bytes(), []byte("AFTER")) {
		t.Fatal("actual PTY output did not survive across real store exchange")
	}
}
