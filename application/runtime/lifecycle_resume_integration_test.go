package runtime_test

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	app "github.com/duck8823/traceary/application/runtime"
	domain "github.com/duck8823/traceary/domain/runtime"
)

// Compose existing fake ports, not another runtime or a native execution profile.
type lifecycleResumeJournal struct {
	*resumeJournal
	lifecycle                *updateJournal
	liveEntered, liveRelease chan struct{}
	liveOnce                 sync.Once
}

func (j *lifecycleResumeJournal) ReserveShutdown(ctx context.Context, b domain.ShutdownBinding) error {
	return j.lifecycle.ReserveShutdown(ctx, b)
}
func (j *lifecycleResumeJournal) SaveShutdown(ctx context.Context, r app.ShutdownResult) error {
	return j.lifecycle.SaveShutdown(ctx, r)
}
func (j *lifecycleResumeJournal) RecordEpochStart(ctx context.Context, epoch string) (app.LifecycleSnapshot, error) {
	return j.lifecycle.RecordEpochStart(ctx, epoch)
}
func (j *lifecycleResumeJournal) ReadLifecycle(ctx context.Context) (app.LifecycleSnapshot, error) {
	return j.lifecycle.ReadLifecycle(ctx)
}
func (j *lifecycleResumeJournal) RecordCleanShutdown(ctx context.Context, epoch string) error {
	return j.lifecycle.RecordCleanShutdown(ctx, epoch)
}
func (j *lifecycleResumeJournal) SaveUpdateDeferral(ctx context.Context, epoch string, s domain.UpdateDeferralState) error {
	return j.lifecycle.SaveUpdateDeferral(ctx, epoch, s)
}
func (j *lifecycleResumeJournal) ClearUpdateDeferral(ctx context.Context, epoch string) error {
	return j.lifecycle.ClearUpdateDeferral(ctx, epoch)
}
func (j *lifecycleResumeJournal) SetAutoStartOptIn(ctx context.Context, enabled bool) error {
	return j.lifecycle.SetAutoStartOptIn(ctx, enabled)
}
func (j *lifecycleResumeJournal) SaveLaunch(ctx context.Context, run domain.RunSnapshot) error {
	if run.State == domain.RunLive && j.liveEntered != nil {
		j.liveOnce.Do(func() { close(j.liveEntered) })
		select {
		case <-j.liveRelease:
		case <-ctx.Done():
			return fmt.Errorf("synthetic resume publication: %w", ctx.Err())
		}
	}
	return j.resumeJournal.SaveLaunch(ctx, run)
}

func lifecycleResumeUninitializedFixture(t *testing.T) (*app.Runtime, app.ResumeRequest, *nativeResumeFactory, *lifecycleResumeJournal) {
	t.Helper()
	_, request, factory, recovery, registry := resumeFixture(t)
	lifecycle := &updateJournal{lifecycleTestJournal: &lifecycleTestJournal{shutdownJournal: &shutdownJournal{controlJournal: recovery.controlJournal}}}
	journal := &lifecycleResumeJournal{resumeJournal: recovery, lifecycle: lifecycle}
	profile := recovery.records[0].Binding.Snapshot().Profile
	owner, err := app.NewRuntime(registry, launchProfiles{profile}, factory, journal, request.Epoch)
	if err != nil {
		t.Fatal(err)
	}
	return owner, request, factory, journal
}

func lifecycleResumeFixture(t *testing.T) (*app.Runtime, app.ResumeRequest, *nativeResumeFactory, *lifecycleResumeJournal) {
	t.Helper()
	owner, request, factory, journal := lifecycleResumeUninitializedFixture(t)
	if _, err := owner.InitializeLifecycle(context.Background()); err != nil {
		t.Fatal(err)
	}
	return owner, request, factory, journal
}

func TestLifecyclePreparingRefusesNewResumeButPreservesCompletedReceipt(t *testing.T) {
	for _, duplicate := range []bool{false, true} {
		t.Run(map[bool]string{false: "new resume refused", true: "completed receipt remains read only"}[duplicate], func(t *testing.T) {
			owner, request, factory, journal := lifecycleResumeFixture(t)
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			if duplicate {
				if _, err := owner.Resume(ctx, request); err != nil {
					t.Fatal(err)
				}
				factory.child.finish()
				if err := owner.AwaitIdle(ctx); err != nil {
					t.Fatal(err)
				}
			} else {
				t.Cleanup(factory.child.finish)
			}
			preview, err := owner.PreviewShutdown(ctx)
			if err != nil {
				t.Fatal(err)
			}
			book := journal.lifecycle.shutdownJournal
			book.reserveEntered, book.reserveRelease = make(chan struct{}), make(chan struct{})
			var release sync.Once
			unblock := func() { release.Do(func() { close(book.reserveRelease) }) }
			defer unblock()
			done := make(chan struct{})
			go func() {
				_, _ = owner.ConfirmShutdown(ctx, app.ShutdownConfirmation{Binding: preview.Binding, WorkLossAcknowledged: true})
				close(done)
			}()
			select {
			case <-book.reserveEntered:
			case <-ctx.Done():
				t.Fatal("shutdown did not reach its preparing fence")
			}
			before := factory.calls.Load()
			result, resumeErr := owner.Resume(ctx, request)
			unblock()
			select {
			case <-done:
			case <-ctx.Done():
				t.Fatal("shutdown coordinator did not join")
			}
			if factory.calls.Load() != before {
				t.Fatal("preparing shutdown allowed a new native resume")
			}
			if duplicate {
				if resumeErr != nil || result.Phase != domain.ResumeConfirmed {
					t.Fatalf("completed receipt was lost or replayed: %v", resumeErr)
				}
			} else if !errors.Is(resumeErr, app.ErrRuntimeBusy) {
				t.Fatalf("new resume crossed the preparing fence: %v", resumeErr)
			}
		})
	}
}

func TestLifecycleCountsAttachedResumeUntilDurablePublicationCompletes(t *testing.T) {
	owner, request, factory, journal := lifecycleResumeFixture(t)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	journal.liveEntered, journal.liveRelease = make(chan struct{}), make(chan struct{})
	var release sync.Once
	unblock := func() { release.Do(func() { close(journal.liveRelease) }) }
	defer unblock()
	t.Cleanup(factory.child.finish)
	done := make(chan error, 1)
	go func() { _, err := owner.Resume(ctx, request); done <- err }()
	select {
	case <-journal.liveEntered:
	case <-ctx.Done():
		t.Fatal("resume did not reach durable publication")
	}
	preview, err := owner.PreviewShutdown(ctx)
	if err != nil {
		t.Fatal(err)
	}
	status, statusErr := owner.UpdateStatus(ctx)
	unblock()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-ctx.Done():
		t.Fatal("resume publication did not complete")
	}
	if preview.Binding.TotalRuns != 1 || preview.Binding.PendingLaunches != 1 {
		t.Fatal("attached but unpublished resume disappeared from shutdown counters")
	}
	if statusErr != nil || status.Quiescent {
		t.Fatal("publishing resume was exposed as update quiescence")
	}
}

func TestLifecycleUnknownResumeWithoutTransferredHandleCannotPublishCleanMarker(t *testing.T) {
	owner, request, factory, journal := lifecycleResumeFixture(t)
	factory.noChild, factory.startErr = true, app.ErrLaunchUnknown
	result, err := owner.Resume(context.Background(), request)
	if !errors.Is(err, app.ErrLaunchUnknown) || result.Phase != domain.ResumeUnknown || result.Owned {
		t.Fatalf("fixture lost its unknown possible-child result: %v", err)
	}
	if err := owner.PublishCleanShutdown(context.Background()); !errors.Is(err, app.ErrShutdownUnknown) && !errors.Is(err, app.ErrRuntimeBusy) {
		t.Fatalf("unknown resume became a clean daemon marker: %v", err)
	}
	if journal.lifecycle.cleans.Load() != 0 {
		t.Fatal("uncertain resume reached clean-marker persistence")
	}
}

func TestUnboundTelemetryWakesReadersWithoutStalingLifecyclePreview(t *testing.T) {
	owner, request, child, _ := shutdownFixture(t)
	t.Cleanup(child.finish)
	run, err := owner.Launch(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	preview, err := owner.PreviewShutdown(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	view, err := owner.SubscribeObservations(app.OutputSelection{Identity: run.Identity, Worktree: run.Target.Worktree().ID(), Profile: run.Profile.ID()})
	if err != nil {
		t.Fatal(err)
	}
	if err = owner.RecordObservation(context.Background(), selectedObservation(t, run, "lifecycle-unbound")); err != nil {
		t.Fatal(err)
	}
	select {
	case <-view.Changed:
	default:
		t.Fatal("metadata update did not wake projection readers")
	}
	current, err := owner.ReadShutdownPreview(preview.Binding)
	if err != nil || current.Binding != preview.Binding {
		t.Fatalf("unbound telemetry changed authoritative shutdown preview: %v", err)
	}
}

func TestLifecycleCannotEnrollAfterAnyResumeAdmission(t *testing.T) {
	for _, mode := range []string{"active resumed child", "completed receipt after exit", "known pre-start refusal"} {
		t.Run(mode, func(t *testing.T) {
			owner, request, factory, journal := lifecycleResumeUninitializedFixture(t)
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			if mode == "known pre-start refusal" {
				factory.fail = true
			}
			_, resumeErr := owner.Resume(ctx, request)
			if mode == "known pre-start refusal" {
				if !errors.Is(resumeErr, domain.ErrChanged) {
					t.Fatal(resumeErr)
				}
			} else {
				if resumeErr != nil {
					t.Fatal(resumeErr)
				}
				if mode == "completed receipt after exit" {
					factory.child.finish()
					if err := owner.AwaitIdle(ctx); err != nil {
						t.Fatal(err)
					}
				} else {
					t.Cleanup(factory.child.finish)
				}
			}
			_, err := owner.InitializeLifecycle(ctx)
			if !errors.Is(err, app.ErrRuntimeBusy) || journal.lifecycle.starts.Load() != 0 {
				t.Fatalf("late lifecycle enrollment reached durable epoch publication after %s: %v", mode, err)
			}
		})
	}
}
