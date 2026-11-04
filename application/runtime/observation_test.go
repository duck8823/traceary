package runtime_test

import (
	"context"
	"errors"
	app "github.com/duck8823/traceary/application/runtime"
	domain "github.com/duck8823/traceary/domain/runtime"
	"golang.org/x/xerrors"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func selectedObservation(t *testing.T, snapshot domain.RunSnapshot, event string) app.SelectedObservation {
	t.Helper()
	metadata, err := domain.DerivedObservationOf(domain.DerivedObservationState{Host: snapshot.Profile.Snapshot().Host, HostVersion: snapshot.Profile.Snapshot().Version, EventID: event, Kind: domain.ObservationPromptBoundary})
	if err != nil {
		t.Fatal(err)
	}
	return app.SelectedObservation{Selection: app.OutputSelection{Identity: snapshot.Identity, Worktree: snapshot.Target.Worktree().ID(), Profile: snapshot.Profile.ID()}, Metadata: metadata}
}

func TestSelectedObservationIsOwnedUntrustedAndClearsOnActualExit(t *testing.T) {
	owner, req, factory, _ := runtimeFixture(t, false)
	snapshot, err := owner.Launch(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	observation := selectedObservation(t, snapshot, "spool:first")
	if err = owner.CheckObservation(context.Background(), observation); err != nil {
		t.Fatal(err)
	}
	if err = owner.RecordObservation(context.Background(), observation); err != nil {
		t.Fatal(err)
	}
	for _, event := range []string{"spool:second", "spool:first", "spool:first"} {
		observation = selectedObservation(t, snapshot, event)
		if err = owner.RecordObservation(context.Background(), observation); err != nil {
			t.Fatal(err)
		}
		last, present, e := owner.LastObservation(observation.Selection)
		if e != nil || !present || last != observation.Metadata {
			t.Fatalf("last untrusted sample = %v, %v, %v", last, present, e)
		}
	}
	if got := owner.List(); len(got) != 1 || got[0] != snapshot {
		t.Fatal("telemetry changed process/launch metadata", got)
	}
	factory.child.finish()
	if err = owner.AwaitIdle(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, present, e := owner.LastObservation(observation.Selection); e != nil || present {
		t.Fatalf("sample retained after actual Wait: present=%v err=%v", present, e)
	}
	if e := owner.CheckObservation(context.Background(), observation); !errors.Is(e, app.ErrRuntimeRejected) {
		t.Fatalf("exited admission = %v", e)
	}
}

func TestSelectedObservationRejectsEveryMismatchedSelectionAndProfile(t *testing.T) {
	owner, req, _, _ := runtimeFixture(t, false)
	snapshot, err := owner.Launch(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	valid := selectedObservation(t, snapshot, "spool:receipt")
	for _, tt := range []struct {
		name   string
		mutate func(*app.SelectedObservation)
		want   error
	}{
		{"unknown_run", func(o *app.SelectedObservation) { o.Selection.Identity.Run = strings.Repeat("a", 32) }, domain.ErrNotFound},
		{"generation", func(o *app.SelectedObservation) { o.Selection.Identity.Generation = strings.Repeat("a", 32) }, domain.ErrChanged},
		{"epoch", func(o *app.SelectedObservation) { o.Selection.Identity.Epoch = strings.Repeat("a", 32) }, domain.ErrChanged},
		{"worktree", func(o *app.SelectedObservation) {
			o.Selection.Worktree = domain.WorktreeID("w_" + strings.Repeat("a", 64))
		}, domain.ErrChanged},
		{"profile", func(o *app.SelectedObservation) { o.Selection.Profile = "other" }, domain.ErrChanged},
		{"host", func(o *app.SelectedObservation) {
			s := o.Metadata.Snapshot()
			s.Host = "claude"
			o.Metadata, _ = domain.DerivedObservationOf(s)
		}, domain.ErrChanged},
		{"version", func(o *app.SelectedObservation) {
			s := o.Metadata.Snapshot()
			s.HostVersion = "other 2"
			o.Metadata, _ = domain.DerivedObservationOf(s)
		}, domain.ErrChanged},
		{"zero_metadata", func(o *app.SelectedObservation) { o.Metadata = domain.DerivedObservation{} }, domain.ErrInvalid},
	} {
		t.Run(tt.name, func(t *testing.T) {
			o := valid
			tt.mutate(&o)
			if e := owner.CheckObservation(context.Background(), o); !errors.Is(e, tt.want) {
				t.Fatalf("admission=%v want %v", e, tt.want)
			}
			if e := owner.RecordObservation(context.Background(), o); !errors.Is(e, tt.want) {
				t.Fatalf("consumption=%v want %v", e, tt.want)
			}
		})
	}
	if _, present, e := owner.LastObservation(valid.Selection); e != nil || present {
		t.Fatalf("invalid sample retained: %v %v", present, e)
	}
}

func TestSelectedObservationPhysicalChangeIsCheckedAtConsumptionOnly(t *testing.T) {
	owner, req, _, registry := runtimeFixture(t, false)
	snapshot, err := owner.Launch(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	o := selectedObservation(t, snapshot, "spool:receipt")
	if err = owner.RecordObservation(context.Background(), o); err != nil {
		t.Fatal(err)
	}
	registry.changed.Store(true)
	if err = owner.CheckObservation(context.Background(), o); err != nil {
		t.Fatal("admission performed physical lookup", err)
	}
	if err = owner.RecordObservation(context.Background(), o); !errors.Is(err, domain.ErrChanged) {
		t.Fatalf("changed physical target consumed: %v", err)
	}
	if _, present, e := owner.LastObservation(o.Selection); e != nil || present {
		t.Fatalf("stale sample retained: %v %v", present, e)
	}
}

func TestSelectedObservationCancellationAndOwnerDisableClearTelemetry(t *testing.T) {
	owner, req, _, _ := runtimeFixture(t, false)
	snapshot, err := owner.Launch(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	o := selectedObservation(t, snapshot, "spool:receipt")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if e := owner.CheckObservation(ctx, o); !errors.Is(e, context.Canceled) {
		t.Fatalf("canceled admission=%v", e)
	}
	if e := owner.RecordObservation(ctx, o); !errors.Is(e, context.Canceled) {
		t.Fatalf("canceled consumption=%v", e)
	}
	if e := owner.RecordObservation(context.Background(), o); e != nil {
		t.Fatal(e)
	}
	owner.DisableLaunches()
	if _, present, e := owner.LastObservation(o.Selection); e != nil || present {
		t.Fatalf("owner disabled sample retained: %v %v", present, e)
	}
	if e := owner.CheckObservation(context.Background(), o); !errors.Is(e, app.ErrRuntimeRejected) {
		t.Fatalf("disabled admission=%v", e)
	}
}

// This journal blocks the real Runtime's initial publication, not a copied reader model.
type observationPublicationJournal struct {
	entered chan domain.RunSnapshot
	release chan struct{}
	fail    bool
}

func (*observationPublicationJournal) ReserveLaunch(context.Context, domain.RunSnapshot) error {
	return nil
}
func (j *observationPublicationJournal) SaveLaunch(ctx context.Context, s domain.RunSnapshot) error {
	if s.State == domain.RunLive {
		j.entered <- s
		select {
		case <-j.release:
		case <-ctx.Done():
			return xerrors.Errorf("fixture publication canceled: %w", ctx.Err())
		}
		if j.fail {
			return domain.ErrUnsafe
		}
	}
	return nil
}

func TestSelectedObservationRequiresActualInitialPublication(t *testing.T) {
	for _, fail := range []bool{false, true} {
		name := "success"
		if fail {
			name = "failure"
		}
		t.Run(name, func(t *testing.T) {
			_, req, factory, registry := runtimeFixture(t, false)
			profile, err := domain.HostProfileFrom(domain.HostProfileState{ID: "codex-fixture", Host: "codex", Executable: "/owned/agent", ExecutableWitness: "fixture", Version: "fixture 1"})
			if err != nil {
				t.Fatal(err)
			}
			journal := &observationPublicationJournal{entered: make(chan domain.RunSnapshot, 1), release: make(chan struct{}), fail: fail}
			var release sync.Once
			t.Cleanup(func() { release.Do(func() { close(journal.release) }); factory.child.finish() })
			owner, err := app.NewRuntime(registry, launchProfiles{profile}, factory, journal, req.Epoch)
			if err != nil {
				t.Fatal(err)
			}
			done := make(chan error, 1)
			go func() { _, e := owner.Launch(context.Background(), req); done <- e }()
			var pending domain.RunSnapshot
			select {
			case pending = <-journal.entered:
			case <-time.After(2 * time.Second):
				t.Fatal("initial publication not reached")
			}
			o := selectedObservation(t, pending, "spool:receipt")
			if e := owner.CheckObservation(context.Background(), o); !errors.Is(e, app.ErrRuntimeRejected) {
				t.Errorf("admitted before SaveLaunch completed: %v", e)
			}
			if e := owner.RecordObservation(context.Background(), o); !errors.Is(e, app.ErrRuntimeRejected) {
				t.Errorf("consumed before SaveLaunch completed: %v", e)
			}
			release.Do(func() { close(journal.release) })
			select {
			case e := <-done:
				if fail && !errors.Is(e, app.ErrLaunchUnknown) {
					t.Fatalf("publication failure=%v", e)
				}
				if !fail && e != nil {
					t.Fatal(e)
				}
			case <-time.After(2 * time.Second):
				t.Fatal("launch publication did not complete")
			}
			if fail {
				if e := owner.CheckObservation(context.Background(), o); !errors.Is(e, app.ErrRuntimeRejected) {
					t.Fatalf("uncertain run admitted: %v", e)
				}
				if _, present, e := owner.LastObservation(o.Selection); e != nil || present {
					t.Fatalf("uncertain run sample: %v %v", present, e)
				}
			} else if e := owner.RecordObservation(context.Background(), o); e != nil {
				t.Fatal("published run not consumed", e)
			}
			factory.child.finish()
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			if e := owner.AwaitIdle(ctx); e != nil {
				t.Fatal(e)
			}
		})
	}
}

// Gate only the external physical revalidation port, after the actual launch committed.
type observationValidationRegistry struct {
	ignoreCancellation bool
	*launchRegistry
	blocked atomic.Bool
	entered chan struct{}
	release chan struct{}
	once    sync.Once
}

func (r *observationValidationRegistry) Revalidate(ctx context.Context, target domain.Target, profile string) error {
	if r.blocked.Load() {
		r.once.Do(func() { close(r.entered) })
		if r.ignoreCancellation {
			<-r.release
			return r.launchRegistry.Revalidate(ctx, target, profile)
		}
		select {
		case <-r.release:
		case <-ctx.Done():
			return xerrors.Errorf("fixture source validation canceled: %w", ctx.Err())
		}
	}
	return r.launchRegistry.Revalidate(ctx, target, profile)
}

func TestSelectedObservationDoesNotHoldOwnerLockDuringPhysicalValidation(t *testing.T) {
	for _, name := range []string{"actual_exit", "cancellation", "ignored_cancellation"} {
		cancelValidation := name != "actual_exit"
		t.Run(name, func(t *testing.T) {
			_, req, factory, base := runtimeFixture(t, false)
			registry := &observationValidationRegistry{launchRegistry: base, entered: make(chan struct{}), release: make(chan struct{}), ignoreCancellation: name == "ignored_cancellation"}
			var release sync.Once
			t.Cleanup(func() { release.Do(func() { close(registry.release) }); factory.child.finish() })
			profile, err := domain.HostProfileFrom(domain.HostProfileState{ID: "codex-fixture", Host: "codex", Executable: "/owned/agent", ExecutableWitness: "fixture", Version: "fixture 1"})
			if err != nil {
				t.Fatal(err)
			}
			owner, err := app.NewRuntime(registry, launchProfiles{profile}, factory, factory.journal, req.Epoch)
			if err != nil {
				t.Fatal(err)
			}
			snapshot, err := owner.Launch(context.Background(), req)
			if err != nil {
				t.Fatal(err)
			}
			o := selectedObservation(t, snapshot, "spool:first")
			if err = owner.RecordObservation(context.Background(), o); err != nil {
				t.Fatal(err)
			}
			registry.blocked.Store(true)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			late := selectedObservation(t, snapshot, "spool:late")
			done := make(chan error, 1)
			go func() { done <- owner.RecordObservation(ctx, late) }()
			select {
			case <-registry.entered:
			case <-time.After(2 * time.Second):
				t.Fatal("physical validation not entered")
			}
			admission := make(chan error, 1)
			go func() { admission <- owner.CheckObservation(context.Background(), o) }()
			select {
			case err = <-admission:
				if err != nil {
					t.Fatal(err)
				}
			case <-time.After(time.Second):
				t.Fatal("physical dependency retained owner lock")
			}
			if cancelValidation {
				cancel()
			} else {
				factory.child.finish()
				wait, cancelWait := context.WithTimeout(context.Background(), time.Second)
				if e := owner.AwaitIdle(wait); e != nil {
					cancelWait()
					t.Fatal("Wait retained behind physical validation", e)
				}
				cancelWait()
			}
			if !cancelValidation || registry.ignoreCancellation {
				release.Do(func() { close(registry.release) })
			}
			select {
			case err = <-done:
				expected := app.ErrRuntimeRejected
				if cancelValidation {
					expected = context.Canceled
				}
				if !errors.Is(err, expected) {
					t.Fatalf("late consumption=%v want %v", err, expected)
				}
			case <-time.After(2 * time.Second):
				t.Fatal("physical validation did not reconcile")
			}
			release.Do(func() { close(registry.release) })
			if _, present, e := owner.LastObservation(o.Selection); e != nil || present {
				t.Fatalf("invalidated sample retained: %v %v", present, e)
			}
		})
	}
}
