package runtime_test

import (
	"context"
	"errors"
	"fmt"
	app "github.com/duck8823/traceary/application/runtime"
	domain "github.com/duck8823/traceary/domain/runtime"
	"sync/atomic"
	"testing"
	"time"
)

func TestShutdownPreviewAndCancellationPreserveActualChildAndLaunchAdmission(t *testing.T) {
	owner, request, factory, _ := runtimeFixture(t, false)
	run, err := owner.Launch(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	preview, err := owner.PreviewShutdown(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(preview.Runs) != 1 || preview.Runs[0].Identity != run.Identity || preview.Binding.Epoch != request.Epoch {
		t.Fatal("shutdown preview did not identify actual child")
	}
	if owner.Disabled() {
		t.Fatal("preview disabled launches")
	}
	if err = owner.CancelShutdown(preview.Binding); err != nil {
		t.Fatal(err)
	}
	if owner.Disabled() {
		t.Fatal("cancellation disabled launches")
	}
	select {
	case <-factory.child.exited:
		t.Fatal("preview/cancel stopped child")
	default:
	}
	request.RequestID = "after-cancel"
	if _, err = owner.Launch(context.Background(), request); err != nil {
		t.Fatal("cancel changed admission", err)
	}
}

func TestShutdownStaleOrMissingAcknowledgmentCannotCommitEffects(t *testing.T) {
	owner, request, _, _ := runtimeFixture(t, false)
	preview, err := owner.PreviewShutdown(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if _, err = owner.ConfirmShutdown(context.Background(), app.ShutdownConfirmation{Binding: preview.Binding}); !errors.Is(err, domain.ErrInvalid) {
		t.Fatalf("missing acknowledgment: %v", err)
	}
	if owner.Disabled() {
		t.Fatal("unacknowledged confirmation disabled owner")
	}
	if _, err = owner.Launch(context.Background(), request); err != nil {
		t.Fatal(err)
	}
	if _, err = owner.ConfirmShutdown(context.Background(), app.ShutdownConfirmation{Binding: preview.Binding, WorkLossAcknowledged: true}); !errors.Is(err, domain.ErrChanged) {
		t.Fatalf("stale preview committed: %v", err)
	}
	if owner.Disabled() {
		t.Fatal("stale confirmation disabled owner")
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Millisecond)
	defer cancel()
	if err = owner.AwaitIdle(ctx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal("actual live ownership lost")
	}
}

// Synthetic lifecycle ports model no implicit child exit: master closure and
// actual Wait are intentionally independent, just as for a HUP-ignoring host.
type shutdownChild struct {
	*controlChild
	closes atomic.Int32
}

func (c *shutdownChild) ValidateTerminalClose(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("synthetic close validation: %w", err)
	}
	if c.invalid.Load() {
		return domain.ErrChanged
	}
	return nil
}
func (c *shutdownChild) CloseTerminal(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("synthetic close validation: %w", err)
	}
	c.closes.Add(1)
	return nil
}

type shutdownFactory struct{ child *shutdownChild }

func (f shutdownFactory) Start(context.Context, domain.LaunchDescriptor) (app.OwnedChild, error) {
	return f.child, nil
}

type shutdownJournal struct {
	exitSaveEntered, exitSaveRelease chan struct{}
	*controlJournal
	shutdownReserveErr, shutdownSaveErr error
	reserves, saves                     atomic.Int32
	reserveEntered, reserveRelease      chan struct{}
	saveEntered, saveRelease            chan struct{}
}

func (j *shutdownJournal) ReserveShutdown(ctx context.Context, _ domain.ShutdownBinding) error {
	j.reserves.Add(1)
	if j.reserveEntered != nil {
		close(j.reserveEntered)
		select {
		case <-j.reserveRelease:
		case <-ctx.Done():
			return fmt.Errorf("synthetic shutdown journal: %w", ctx.Err())
		}
	}
	return j.shutdownReserveErr
}
func (j *shutdownJournal) SaveShutdown(ctx context.Context, _ app.ShutdownResult) error {
	j.saves.Add(1)
	if j.saveEntered != nil {
		close(j.saveEntered)
		select {
		case <-j.saveRelease:
		case <-ctx.Done():
			return fmt.Errorf("synthetic shutdown journal: %w", ctx.Err())
		}
	}
	return j.shutdownSaveErr
}
func shutdownFixture(t *testing.T) (*app.Runtime, app.LaunchRequest, *shutdownChild, *shutdownJournal) {
	t.Helper()
	_, launch, base, registry := runtimeFixture(t, false)
	profile, err := domain.HostProfileFrom(domain.HostProfileState{ID: "codex-fixture", Host: "codex", Executable: "/owned/agent", ExecutableWitness: "fixture", Version: "fixture 1"})
	if err != nil {
		t.Fatal(err)
	}
	child := &shutdownChild{controlChild: &controlChild{fakeChild: base.child, result: app.ControlEffect{Outcome: domain.ControlDelivered, WrittenBytes: 3}}}
	journal := &shutdownJournal{controlJournal: &controlJournal{launchJournal: base.journal}}
	owner, err := app.NewRuntime(registry, launchProfiles{profile}, shutdownFactory{child}, journal, launch.Epoch)
	if err != nil {
		t.Fatal(err)
	}
	return owner, launch, child, journal
}
func TestConfirmedShutdownClosesOnceButRetainsOwnershipAndExplicitStop(t *testing.T) {
	owner, launch, child, _ := shutdownFixture(t)
	run, err := owner.Launch(context.Background(), launch)
	if err != nil {
		t.Fatal(err)
	}
	preview, err := owner.PreviewShutdown(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	confirmation := app.ShutdownConfirmation{Binding: preview.Binding, WorkLossAcknowledged: true}
	first, err := owner.ConfirmShutdown(context.Background(), confirmation)
	if err != nil || first.Outcome != domain.ShutdownDraining || first.ClosedTerminals != 1 {
		t.Fatalf("confirmed closure: %+v %v", first, err)
	}
	receipt, receiptErr := owner.ShutdownReceipt(preview.Binding)
	if receiptErr != nil || receipt != first || child.closes.Load() != 1 {
		t.Fatal("read-only shutdown receipt changed effects", receiptErr)
	}
	again, err := owner.ConfirmShutdown(context.Background(), confirmation)
	if err != nil || again != first || child.closes.Load() != 1 {
		t.Fatal("duplicate shutdown repeated closure")
	}
	if err = owner.CancelShutdown(preview.Binding); !errors.Is(err, domain.ErrChanged) {
		t.Fatal("cancel pretended irreversible shutdown was undone")
	}
	launch.RequestID = "after-shutdown"
	if _, err = owner.Launch(context.Background(), launch); !errors.Is(err, app.ErrRuntimeBusy) {
		t.Fatal("new launch raced committed shutdown")
	}
	select {
	case <-child.exited:
		t.Fatal("master closure implicitly killed child")
	default:
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Millisecond)
	defer cancel()
	if err = owner.AwaitIdle(ctx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal("owner abandoned live child")
	}
	selection := app.OutputSelection{Identity: run.Identity, Worktree: run.Target.Worktree().ID(), Profile: run.Profile.ID()}
	input, err := domain.ControlIntentFrom(domain.ControlIntentState{RequestID: "after-close-input", Kind: domain.ControlInput, ManualTerminal: true, InputBytes: 3})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = owner.Control(context.Background(), app.ControlRequest{Selection: selection, Intent: input, Data: []byte("raw")}); !errors.Is(err, app.ErrRuntimeBusy) {
		t.Fatal("input admitted while shutdown draining")
	}
	stop, err := domain.ControlIntentFrom(domain.ControlIntentState{RequestID: "explicit-stop", Kind: domain.ControlStop, WorkLossAcknowledged: true})
	if err != nil {
		t.Fatal(err)
	}
	child.result.WrittenBytes = 0
	if _, err = owner.Control(context.Background(), app.ControlRequest{Selection: selection, Intent: stop}); err != nil {
		t.Fatal("explicit original stop unavailable", err)
	}
	if child.effects.Load() != 1 {
		t.Fatal("unexpected native signal/input effect")
	}
	child.finish()
	if err = owner.AwaitIdle(context.Background()); err != nil {
		t.Fatal(err)
	}
}
func TestCompletedControlInvalidatesEarlierShutdownPreview(t *testing.T) {
	owner, launch, _, _ := shutdownFixture(t)
	run, err := owner.Launch(context.Background(), launch)
	if err != nil {
		t.Fatal(err)
	}
	preview, err := owner.PreviewShutdown(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	intent, err := domain.ControlIntentFrom(domain.ControlIntentState{RequestID: "input-between-plan-confirm", Kind: domain.ControlInput, ManualTerminal: true, InputBytes: 3})
	if err != nil {
		t.Fatal(err)
	}
	_, err = owner.Control(context.Background(), app.ControlRequest{Selection: app.OutputSelection{Identity: run.Identity, Worktree: run.Target.Worktree().ID(), Profile: run.Profile.ID()}, Intent: intent, Data: []byte("raw")})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = owner.ConfirmShutdown(context.Background(), app.ShutdownConfirmation{Binding: preview.Binding, WorkLossAcknowledged: true}); !errors.Is(err, domain.ErrChanged) {
		t.Fatal("post-preview completed control bypassed revision")
	}
}
func TestShutdownJournalFailuresNeverReplayOrHidePartialEffects(t *testing.T) {
	for _, kind := range []string{"definite-reserve", "uncertain-reserve", "after-effect-save"} {
		t.Run(kind, func(t *testing.T) {
			owner, launch, child, journal := shutdownFixture(t)
			switch kind {
			case "definite-reserve":
				journal.shutdownReserveErr = domain.ErrLimit
			case "uncertain-reserve":
				journal.shutdownReserveErr = app.ErrLifecycleCommitUncertain
			case "after-effect-save":
				journal.shutdownSaveErr = domain.ErrUnsafe
			}
			if _, err := owner.Launch(context.Background(), launch); err != nil {
				t.Fatal(err)
			}
			preview, err := owner.PreviewShutdown(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			request := app.ShutdownConfirmation{Binding: preview.Binding, WorkLossAcknowledged: true}
			result, err := owner.ConfirmShutdown(context.Background(), request)
			if kind == "definite-reserve" {
				if !errors.Is(err, domain.ErrLimit) || result.Outcome != domain.ShutdownRejected || owner.Disabled() {
					t.Fatal("clean failure wedged owner", err)
				}
			} else if !errors.Is(err, app.ErrShutdownUnknown) || result.Outcome != domain.ShutdownUnknown || !owner.Disabled() {
				t.Fatal("uncertain shutdown falsely rolled back", err)
			}
			expected := int32(0)
			if kind == "after-effect-save" {
				expected = 1
			}
			if child.closes.Load() != expected {
				t.Fatal("effect before known durable intent")
			}
			_, _ = owner.ConfirmShutdown(context.Background(), request)
			if child.closes.Load() != expected || journal.reserves.Load() != 1 {
				t.Fatal("failed/unknown shutdown replayed")
			}
		})
	}
}
func TestCallerCancellationCannotAbandonAcceptedShutdown(t *testing.T) {
	owner, launch, child, journal := shutdownFixture(t)
	journal.reserveEntered = make(chan struct{})
	journal.reserveRelease = make(chan struct{})
	if _, err := owner.Launch(context.Background(), launch); err != nil {
		t.Fatal(err)
	}
	preview, err := owner.PreviewShutdown(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	request := app.ShutdownConfirmation{Binding: preview.Binding, WorkLossAcknowledged: true}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { _, e := owner.ConfirmShutdown(ctx, request); done <- e }()
	<-journal.reserveEntered
	cancel()
	if err = <-done; !errors.Is(err, app.ErrShutdownUnknown) {
		t.Fatal("cancelled caller claimed known outcome", err)
	}
	receipt, receiptErr := owner.ShutdownReceipt(preview.Binding)
	if receiptErr != nil || receipt.Outcome != domain.ShutdownPending {
		t.Fatal("accepted pending decision not inspectable", receiptErr)
	}
	if !owner.Disabled() || child.closes.Load() != 0 {
		t.Fatal("accepted intent lost admission or closed before commit")
	}
	close(journal.reserveRelease)
	if result, e := owner.ConfirmShutdown(context.Background(), request); e != nil || result.Outcome != domain.ShutdownDraining || child.closes.Load() != 1 {
		t.Fatal("detached coordinator did not finish original confirmation", e)
	}
}
func TestShutdownAwaitIdleJoinsDurableReconciliationAfterActualExit(t *testing.T) {
	owner, launch, child, journal := shutdownFixture(t)
	journal.saveEntered = make(chan struct{})
	journal.saveRelease = make(chan struct{})
	if _, err := owner.Launch(context.Background(), launch); err != nil {
		t.Fatal(err)
	}
	preview, err := owner.PreviewShutdown(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() {
		_, e := owner.ConfirmShutdown(context.Background(), app.ShutdownConfirmation{Binding: preview.Binding, WorkLossAcknowledged: true})
		done <- e
	}()
	<-journal.saveEntered
	child.finish()
	ctx, cancel := context.WithTimeout(context.Background(), time.Millisecond)
	defer cancel()
	if err = owner.AwaitIdle(ctx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal("lifecycle journal coordinator abandoned")
	}
	close(journal.saveRelease)
	if err = <-done; err != nil {
		t.Fatal(err)
	}
	if err = owner.AwaitIdle(context.Background()); err != nil {
		t.Fatal(err)
	}
}
func TestShutdownPreviewFullRosterIsFrozenDefensiveAndNotReplacedByReads(t *testing.T) {
	owner, launch, _, _ := shutdownFixture(t)
	for i := 0; i < domain.MaxActiveRuns; i++ {
		launch.RequestID = fmt.Sprintf("preview-%d", i)
		if _, err := owner.Launch(context.Background(), launch); err != nil {
			t.Fatal(err)
		}
	}
	preview, err := owner.PreviewShutdown(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if preview.Binding.TotalRuns != domain.MaxActiveRuns || len(preview.Runs) != domain.MaxActiveRuns {
		t.Fatal("preview omitted active runs")
	}
	preview.Runs[0].Identity.Run = "tampered-copy"
	reread, err := owner.ReadShutdownPreview(preview.Binding)
	if err != nil || reread.Runs[0].Identity.Run == "tampered-copy" || reread.Binding != preview.Binding {
		t.Fatal("preview read replaced or aliased frozen roster", err)
	}
	if _, err = owner.PreviewShutdown(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err = owner.ReadShutdownPreview(preview.Binding); !errors.Is(err, domain.ErrChanged) {
		t.Fatal("superseded preview accepted")
	}
}

func (j *shutdownJournal) SaveLaunch(ctx context.Context, s domain.RunSnapshot) error {
	if s.State == domain.RunExited && j.exitSaveEntered != nil {
		close(j.exitSaveEntered)
		select {
		case <-j.exitSaveRelease:
		case <-ctx.Done():
			return fmt.Errorf("synthetic exit journal: %w", ctx.Err())
		}
	}
	return j.launchJournal.SaveLaunch(ctx, s)
}
func TestShutdownCannotSkipExitJournalReconciliationWithAnEmptyRoster(t *testing.T) {
	owner, launch, child, journal := shutdownFixture(t)
	journal.exitSaveEntered = make(chan struct{})
	journal.exitSaveRelease = make(chan struct{})
	if _, err := owner.Launch(context.Background(), launch); err != nil {
		t.Fatal(err)
	}
	child.finish()
	<-journal.exitSaveEntered
	preview, err := owner.PreviewShutdown(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(preview.Runs) != 0 || preview.Binding.PendingReconciliations != 1 {
		t.Fatal("exited-but-unreconciled ownership disappeared from preview")
	}
	if _, err = owner.ConfirmShutdown(context.Background(), app.ShutdownConfirmation{Binding: preview.Binding, WorkLossAcknowledged: true}); !errors.Is(err, domain.ErrChanged) {
		t.Fatal("shutdown skipped pending exit reconciliation", err)
	}
	if owner.Disabled() || child.closes.Load() != 0 || journal.reserves.Load() != 0 {
		t.Fatal("nonquiescent shutdown committed")
	}
	close(journal.exitSaveRelease)
	if err = owner.AwaitIdle(context.Background()); err != nil {
		t.Fatal(err)
	}
}

// This fake exposes explicit startup/clean publication, not persisted OS proofs.
type lifecycleTestJournal struct {
	*shutdownJournal
	startErr                   error
	startEntered, startRelease chan struct{}
	starts, cleans             atomic.Int32
}

func (j *lifecycleTestJournal) RecordEpochStart(ctx context.Context, epoch string) (app.LifecycleSnapshot, error) {
	j.starts.Add(1)
	if j.startEntered != nil {
		close(j.startEntered)
		select {
		case <-j.startRelease:
		case <-ctx.Done():
			return app.LifecycleSnapshot{}, fmt.Errorf("synthetic startup: %w", ctx.Err())
		}
	}
	return app.LifecycleSnapshot{Epoch: epoch}, j.startErr
}
func (j *lifecycleTestJournal) ReadLifecycle(context.Context) (app.LifecycleSnapshot, error) {
	return app.LifecycleSnapshot{}, nil
}
func (j *lifecycleTestJournal) RecordCleanShutdown(context.Context, string) error {
	j.cleans.Add(1)
	return nil
}
func (j *lifecycleTestJournal) SaveUpdateDeferral(context.Context, string, domain.UpdateDeferralState) error {
	return domain.ErrUnsupported
}
func (j *lifecycleTestJournal) ClearUpdateDeferral(context.Context, string) error {
	return domain.ErrUnsupported
}
func (j *lifecycleTestJournal) SetAutoStartOptIn(context.Context, bool) error {
	return domain.ErrUnsupported
}
func lifecycleFixture(t *testing.T) (*app.Runtime, app.LaunchRequest, *shutdownChild, *lifecycleTestJournal) {
	t.Helper()
	_, launch, base, registry := runtimeFixture(t, false)
	profile, err := domain.HostProfileFrom(domain.HostProfileState{ID: "codex-fixture", Host: "codex", Executable: "/owned/agent", ExecutableWitness: "fixture", Version: "fixture 1"})
	if err != nil {
		t.Fatal(err)
	}
	child := &shutdownChild{controlChild: &controlChild{fakeChild: base.child, result: app.ControlEffect{Outcome: domain.ControlDelivered, WrittenBytes: 3}}}
	journal := &lifecycleTestJournal{shutdownJournal: &shutdownJournal{controlJournal: &controlJournal{launchJournal: base.journal}}}
	owner, err := app.NewRuntime(registry, launchProfiles{profile}, shutdownFactory{child}, journal, launch.Epoch)
	if err != nil {
		t.Fatal(err)
	}
	return owner, launch, child, journal
}
func TestLifecycleStartupFailureRefusesNewLaunchesAndCannotEnrollAnExistingRun(t *testing.T) {
	for _, startErr := range []error{domain.ErrLimit, app.ErrLifecycleCommitUncertain} {
		owner, launch, child, journal := lifecycleFixture(t)
		journal.startErr = startErr
		if _, err := owner.InitializeLifecycle(context.Background()); !errors.Is(err, startErr) {
			t.Fatal("startup failure hidden", err)
		}
		if !owner.Disabled() {
			t.Fatal("failed/unknown epoch commit left launch admission open")
		}
		if _, err := owner.Launch(context.Background(), launch); !errors.Is(err, app.ErrRuntimeBusy) {
			t.Fatal("launch after failed startup", err)
		}
		if _, err := owner.InitializeLifecycle(context.Background()); !errors.Is(err, app.ErrRuntimeBusy) || journal.starts.Load() != 1 {
			t.Fatal("unknown startup automatically repeated")
		}
		if child.closes.Load() != 0 {
			t.Fatal("failed startup closed terminal")
		}
	}
	owner, launch, _, journal := lifecycleFixture(t)
	if _, err := owner.Launch(context.Background(), launch); err != nil {
		t.Fatal(err)
	}
	if _, err := owner.InitializeLifecycle(context.Background()); !errors.Is(err, app.ErrRuntimeBusy) || journal.starts.Load() != 0 {
		t.Fatal("lifecycle enrolled an already admitted run")
	}
}
func TestLifecycleStartupAdmissionBarrierAndCleanPublicationRequireActualIdle(t *testing.T) {
	owner, launch, child, journal := lifecycleFixture(t)
	journal.startEntered = make(chan struct{})
	journal.startRelease = make(chan struct{})
	initialized := make(chan error, 1)
	go func() { _, e := owner.InitializeLifecycle(context.Background()); initialized <- e }()
	<-journal.startEntered
	if _, err := owner.Launch(context.Background(), launch); !errors.Is(err, app.ErrRuntimeBusy) {
		t.Fatal("launch raced startup marker", err)
	}
	if _, err := owner.PreviewShutdown(context.Background()); !errors.Is(err, app.ErrRuntimeBusy) {
		t.Fatal("shutdown preview raced startup", err)
	}
	close(journal.startRelease)
	if err := <-initialized; err != nil {
		t.Fatal(err)
	}
	if _, err := owner.Launch(context.Background(), launch); err != nil {
		t.Fatal(err)
	}
	owner.DisableLaunches()
	if err := owner.PublishCleanShutdown(context.Background()); !errors.Is(err, app.ErrRuntimeBusy) || journal.cleans.Load() != 0 {
		t.Fatal("live child was published as joined")
	}
	child.finish()
	if err := owner.AwaitIdle(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := owner.PublishCleanShutdown(context.Background()); err != nil || journal.cleans.Load() != 1 {
		t.Fatal("actual idle clean publication missing", err)
	}
}

func TestLifecycleExitPublicationFailureRemainsUnknownAfterActualWait(t *testing.T) {
	owner, launch, child, journal := lifecycleFixture(t)
	if _, err := owner.InitializeLifecycle(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err := owner.Launch(context.Background(), launch); err != nil {
		t.Fatal(err)
	}
	journal.launchJournal.mu.Lock()
	journal.fail = true
	journal.launchJournal.mu.Unlock()
	owner.DisableLaunches()
	child.finish()
	if err := owner.AwaitIdle(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := owner.PublishCleanShutdown(context.Background()); !errors.Is(err, app.ErrShutdownUnknown) || journal.cleans.Load() != 0 {
		t.Fatal("failed exit journal fabricated clean marker", err)
	}
	views := owner.List()
	if len(views) != 1 || views[0].State != domain.RunExited || !views[0].PublicationUncertain {
		t.Fatal("actual exit and metadata uncertainty were conflated")
	}
}
