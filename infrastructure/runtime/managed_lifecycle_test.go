//go:build darwin || linux

package runtime_test

import (
	"context"
	"errors"
	"fmt"
	app "github.com/duck8823/traceary/application/runtime"
	domain "github.com/duck8823/traceary/domain/runtime"
	infra "github.com/duck8823/traceary/infrastructure/runtime"
	"os"
	"slices"
	"sync"
	"testing"
	"time"
)

func TestManagedDaemonPublishesNewEpochBeforeReadinessAndCleanMarkerAfterIdle(t *testing.T) {
	root := daemonRoot(t)
	ctx := context.Background()
	if _, err := (infra.LocalOperatorFactory{}).Create(ctx, root); err != nil {
		t.Fatal(err)
	}
	store, err := infra.OpenExecutionStore(root, infra.MaxRegistryBytes)
	if err != nil {
		t.Fatal(err)
	}
	previous := ""
	for range 2 {
		d := daemon(t, nil, infra.DaemonOptions{RuntimeFactory: infra.NewManagedRuntime})
		cancel, done := startDaemon(t, d, root)
		status, err := d.Status(ctx, root)
		if err != nil {
			t.Fatal(err)
		}
		marker, err := store.ReadLifecycle(ctx)
		if err != nil || marker.Epoch != status.Epoch || marker.Epoch == previous || marker.Clean || marker.PreviousEpoch != previous {
			cancel()
			t.Fatal("readiness preceded durable fresh epoch", err)
		}
		if previous != "" && !marker.PreviousClean {
			cancel()
			t.Fatal("normal idle shutdown not retained")
		}
		cancel()
		select {
		case err = <-done:
			if err != nil {
				t.Fatal(err)
			}
		case <-time.After(3 * time.Second):
			t.Fatal("idle daemon did not terminate")
		}
		marker, err = store.ReadLifecycle(ctx)
		if err != nil || !marker.Clean || marker.Epoch != status.Epoch {
			t.Fatal("actual idle shutdown not durably published", err)
		}
		previous = status.Epoch
	}
}

// The real daemon/socket/typed lifecycle SDK/native PTY chain is exercised here.
// Actual CLI process qualification and real-AI acceptance remain separate.
func TestManagedConfirmedMasterCloseKeepsLeaseAndManagementUntilActualExit(t *testing.T) {
	_, descriptor, resultPath := ptyFixture(t)
	ctx := context.Background()
	root := daemonRoot(t)
	services, err := (infra.LocalOperatorFactory{}).Create(ctx, root)
	if err != nil {
		t.Fatal(err)
	}
	project, err := services.Registry.RegisterProject(ctx, descriptor.Target().Worktree().Path())
	if err != nil {
		t.Fatal(err)
	}
	worktree, err := services.Registry.RegisterWorktree(ctx, project.ID(), descriptor.Target().Worktree().Path(), descriptor.Target().Worktree().Store())
	if err != nil {
		t.Fatal(err)
	}
	journal, err := infra.OpenExecutionStore(root, infra.MaxRegistryBytes)
	if err != nil {
		t.Fatal(err)
	}
	if err = journal.RegisterProfile(ctx, descriptor.Snapshot().Profile); err != nil {
		t.Fatal(err)
	}
	observed := &lifecycleObservedJournal{ExecutionStore: journal}
	owned := make(chan *app.Runtime, 1)
	factory := func(factoryCtx context.Context, _ string, epoch string) (*app.Runtime, error) {
		launcher, e := infra.NewPTYLauncher(services.Registry, []string{"TRACEARY_PTY_FIXTURE=1", "FIXTURE_RESULT=" + resultPath, "FIXTURE_OUTPUT_MODE=ignore_hup", "GORACE=atexit_sleep_ms=0"})
		if e != nil {
			return nil, fmt.Errorf("compose lifecycle fixture: %w", e)
		}
		owner, e := app.NewRuntime(services.Registry, journal, launcher, observed, epoch)
		if e != nil {
			return nil, fmt.Errorf("compose lifecycle fixture: %w", e)
		}
		if _, e = owner.InitializeLifecycle(factoryCtx); e != nil {
			return nil, fmt.Errorf("compose lifecycle fixture: %w", e)
		}
		owned <- owner
		return owner, nil
	}
	d := daemon(t, nil, infra.DaemonOptions{RuntimeFactory: factory})
	cancel, done := startDaemon(t, d, root)
	defer cancel()
	owner := <-owned
	status, err := d.Status(ctx, root)
	if err != nil {
		t.Fatal(err)
	}
	launch := app.LaunchRequest{Epoch: status.Epoch, Worktree: worktree.ID(), Profile: descriptor.Snapshot().Profile.ID(), RequestID: "hup-ignoring-owned"}
	run, err := d.Launch(ctx, root, launch)
	if err != nil {
		t.Fatal(err)
	}
	stop, err := domain.ControlIntentFrom(domain.ControlIntentState{RequestID: "explicit-lifecycle-stop", Kind: domain.ControlStop, WorkLossAcknowledged: true})
	if err != nil {
		t.Fatal(err)
	}
	selection := app.OutputSelection{Identity: run.Identity, Worktree: worktree.ID(), Profile: run.Profile.ID()}
	t.Cleanup(func() {
		_, _ = d.Control(context.Background(), root, app.ControlRequest{Selection: selection, Intent: stop})
	})
	readyDeadline := time.Now().Add(4 * time.Second)
	for {
		if _, e := os.Stat(resultPath + ".ready"); e == nil {
			break
		}
		if time.Now().After(readyDeadline) {
			t.Fatal("native HUP fixture not ready")
		}
		time.Sleep(5 * time.Millisecond)
	}
	updateStatus, updateErr := d.UpdateStatus(ctx, root, status.Epoch)
	if updateErr != nil || updateStatus.Quiescent {
		t.Fatal("live native owner falsely update-quiescent", updateErr)
	}
	deferred, updateErr := d.AttemptUpdate(ctx, root, app.UpdateAttemptRequest{Epoch: status.Epoch, Target: domain.UpdateTarget{Version: "fixture-v2", Protocol: 2}})
	if updateErr != nil || !deferred.Notice || deferred.Status.Quiescent {
		t.Fatal("live native update not deferred", updateErr)
	}
	if _, available := deferred.Preview.Value(); available {
		t.Fatal("live native update returned empty handoff")
	}
	stream, err := d.Attach(ctx, root, selection, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = stream.Next(ctx); err != nil {
		t.Fatal(err)
	}
	_ = stream.Close() // ordinary detach is not full shutdown
	preview, err := app.ReadShutdownPlan(ctx, d, root, status.Epoch)
	if err != nil {
		t.Fatal(err)
	}
	if err = d.CancelShutdown(ctx, root, preview.Binding); err != nil {
		t.Fatal(err)
	}
	preview, err = app.ReadShutdownPlan(ctx, d, root, status.Epoch)
	if err != nil {
		t.Fatal(err)
	}
	request := app.ShutdownConfirmation{Binding: preview.Binding, WorkLossAcknowledged: true}
	closed, err := d.ConfirmShutdown(ctx, root, request)
	if err != nil || closed.Outcome != domain.ShutdownDraining || closed.ClosedTerminals != 1 {
		t.Fatal("managed terminal closure failed", err)
	}
	receipt, receiptErr := d.ShutdownReceipt(ctx, root, preview.Binding)
	if receiptErr != nil || receipt != closed {
		t.Fatal("read-only shutdown receipt mismatch", receiptErr)
	}
	// Reconnect via the actual IPC decoder after capture closure; an in-memory
	// subscription alone would miss a wire gap-reason allowlist regression.
	ended, err := d.Attach(ctx, root, selection, 0)
	if err != nil {
		t.Fatal("closed capture reconnect failed", err)
	}
	defer func() { _ = ended.Close() }()
	readCtx, readCancel := context.WithTimeout(ctx, 2*time.Second)
	defer readCancel()
	sawGap, sawEnd := false, false
	for frames := 0; frames < 8 && !sawEnd; frames++ {
		frame, e := ended.Next(readCtx)
		if e != nil {
			t.Fatal("master-close IPC stream failed before gap/end", e)
		}
		if frame.Kind == "gap" {
			if frame.Gap == nil || frame.Gap.Reason != string(app.CaptureTerminalClosed) || !frame.Gap.DeliveryUnknown || frame.Gap.From != frame.Gap.To {
				t.Fatal("master-close IPC loss evidence changed")
			}
			sawGap = true
		}
		if frame.Kind == "end" {
			if !sawGap {
				t.Fatal("master-close IPC end hid delivery uncertainty")
			}
			sawEnd = true
		}
	}
	if !sawGap || !sawEnd {
		t.Fatal("master-close IPC capture never reached explicit gap and end")
	}
	status, err = d.Status(ctx, root)
	if err != nil || status.State != "draining" || slices.Contains(status.Capabilities, "input") || slices.Contains(status.Capabilities, "resize") || !slices.Contains(status.Capabilities, "stop") {
		t.Fatal("draining management/capability contract lost", err)
	}
	other := daemon(t, nil, infra.DaemonOptions{})
	if e := other.Serve(ctx, root); !errors.Is(e, app.ErrRuntimeBusy) {
		t.Fatal("live HUP-ignoring ownership lease released", e)
	}
	select {
	case e := <-done:
		t.Fatal("daemon abandoned actual live child", e)
	default:
	}
	launch.RequestID = "after-work-loss-confirmation"
	if _, e := d.Launch(ctx, root, launch); !errors.Is(e, app.ErrRuntimeBusy) {
		t.Fatal("new launch admitted during shutdown", e)
	}
	if _, err = d.Control(ctx, root, app.ControlRequest{Selection: selection, Intent: stop}); err != nil && !errors.Is(err, app.ErrControlUnknown) {
		t.Fatal("explicit Stop unavailable during live drain", err)
	}
	select {
	case err = <-done:
		if err != nil {
			observed.mu.Lock()
			exitJournalErr := observed.exitErr
			observed.mu.Unlock()
			views := owner.List()
			state, uncertain := domain.RunState("missing"), false
			if len(views) > 0 {
				state, uncertain = views[0].State, views[0].PublicationUncertain
			}
			t.Fatalf("confirmed daemon publication failed: %v; owned_state=%s uncertain=%t exit_journal_error=%v", err, state, uncertain, exitJournalErr)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("confirmed daemon failed to join actual exit")
	}
	lifecycle, err := journal.ReadLifecycle(ctx)
	if err != nil || !lifecycle.Clean || len(lifecycle.Shutdowns) != 1 || lifecycle.Shutdowns[0].Outcome != domain.ShutdownDraining {
		t.Fatal("managed clean marker/immutable closure receipt lost", err)
	}
	records, err := journal.ListLaunches(ctx)
	if err != nil || len(records) != 1 || records[0].State != domain.RunExited {
		t.Fatal("actual owned Wait not recorded", err)
	}
}

// Keep native integration failures attributable without exposing terminal/env
// contents. This observer does not alter or retry production journal calls.
type lifecycleObservedJournal struct {
	*infra.ExecutionStore
	mu      sync.Mutex
	exitErr error
}

func (j *lifecycleObservedJournal) SaveLaunch(ctx context.Context, snapshot domain.RunSnapshot) error {
	err := j.ExecutionStore.SaveLaunch(ctx, snapshot)
	if snapshot.State == domain.RunExited {
		j.mu.Lock()
		j.exitErr = err
		j.mu.Unlock()
	}
	if err != nil {
		return fmt.Errorf("observe synthetic exit journal: %w", err)
	}
	return nil
}
