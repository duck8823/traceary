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
	"github.com/duck8823/traceary/domain/types"
)

// Atomic outcome+retirement in one memory critical section models the real
// single-envelope journal contract; never a second post-final clear operation.
func (j *updateJournal) SaveUpdateHandoff(ctx context.Context, result app.ShutdownResult, target domain.UpdateTarget) error {
	j.mu.Lock()
	j.handoffs++
	entered, release, failure := j.handoffEntered, j.handoffRelease, j.handoffErr
	j.mu.Unlock()
	if entered != nil {
		close(entered)
		select {
		case <-release:
		case <-ctx.Done():
			return fmt.Errorf("synthetic handoff journal wait: %w", ctx.Err())
		}
	}
	if failure != nil {
		return failure
	}
	j.mu.Lock()
	defer j.mu.Unlock()
	if state, ok := j.state.Value(); ok && state.Target != target {
		return domain.ErrChanged
	}
	if result.Outcome != domain.ShutdownDraining || result.ClosedTerminals != 0 || result.Binding.TotalRuns != 0 {
		return domain.ErrInvalid
	}
	j.state = types.None[domain.UpdateDeferralState]()
	return nil
}

func TestUpdateHandoffRetiresOnlyInsideJoinedAtomicEmptyShutdown(t *testing.T) {
	ctx := context.Background()
	owner, launch, child, journal := updateFixture(t, &updateClock{at: time.Now()})
	if _, err := owner.InitializeLifecycle(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := owner.Launch(ctx, launch); err != nil {
		t.Fatal(err)
	}
	target := domain.UpdateTarget{Version: "v2", Protocol: 2}
	if _, err := owner.AttemptUpdate(ctx, target); err != nil {
		t.Fatal(err)
	}
	child.finish()
	if err := owner.AwaitIdle(ctx); err != nil {
		t.Fatal(err)
	}
	idle, err := owner.AttemptUpdate(ctx, target)
	if err != nil {
		t.Fatal(err)
	}
	binding, ok := idle.Preview.Value()
	if !ok {
		t.Fatal("missing empty preview")
	}
	journal.handoffEntered = make(chan struct{})
	journal.handoffRelease = make(chan struct{})
	var once sync.Once
	release := func() { once.Do(func() { close(journal.handoffRelease) }) }
	defer release()
	request := app.UpdateHandoffRequest{Target: target, Binding: binding, DaemonExitAcknowledged: true}
	wrongTarget := request
	wrongTarget.Target.Version = "v3"
	if _, err = owner.HandoffUpdate(ctx, wrongTarget); !errors.Is(err, domain.ErrChanged) || journal.reserves.Load() != 0 {
		t.Fatal("handoff retired a different deferred target", err)
	}
	done := make(chan error, 1)
	go func() { _, e := owner.HandoffUpdate(ctx, request); done <- e }()
	<-journal.handoffEntered
	if err = owner.PublishCleanShutdown(ctx); !errors.Is(err, app.ErrRuntimeBusy) || journal.cleans.Load() != 0 {
		t.Fatal("clean overtook atomic retirement", err)
	}
	before, _ := journal.snapshot()
	if before.Started.IsZero() {
		t.Fatal("deferral retired before final commit")
	}
	if child.closes.Load() != 0 || child.effects.Load() != 0 {
		t.Fatal("empty handoff touched native child")
	}
	release()
	if err = <-done; err != nil {
		t.Fatal(err)
	}
	state, _ := journal.snapshot()
	if !state.Started.IsZero() {
		t.Fatal("known empty handoff did not retire deferral")
	}
	status, err := owner.UpdateStatus(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok = status.Deferral.Value(); ok {
		t.Fatal("cache retained retired deferral")
	}
	if err = owner.PublishCleanShutdown(ctx); err != nil || journal.cleans.Load() != 1 {
		t.Fatal("joined known empty handoff not clean", err)
	}
	result, err := owner.HandoffUpdate(ctx, request)
	if err != nil || result.Outcome != domain.ShutdownDraining || result.ClosedTerminals != 0 || journal.handoffs != 1 || journal.shutdownJournal.saves.Load() != 0 {
		t.Fatal("handoff replayed or used non-atomic finalization", err)
	}
	request.Target.Version = "v3"
	if _, err = owner.HandoffUpdate(ctx, request); !errors.Is(err, domain.ErrChanged) {
		t.Fatal("same ticket accepted changed update target", err)
	}
	if _, err = owner.ConfirmShutdown(ctx, app.ShutdownConfirmation{Binding: binding, WorkLossAcknowledged: true}); !errors.Is(err, domain.ErrChanged) {
		t.Fatal("manual operation joined different-purpose ticket", err)
	}
}

func TestUpdateHandoffAndConcurrentLaunchHaveOnlyOneAcceptedWinner(t *testing.T) {
	for range 32 {
		ctx := context.Background()
		owner, launch, child, journal := updateFixture(t, &updateClock{at: time.Now()})
		if _, err := owner.InitializeLifecycle(ctx); err != nil {
			t.Fatal(err)
		}
		target := domain.UpdateTarget{Version: "v2", Protocol: 2}
		idle, err := owner.AttemptUpdate(ctx, target)
		if err != nil {
			t.Fatal(err)
		}
		binding, _ := idle.Preview.Value()
		start := make(chan struct{})
		launched, handed := make(chan error, 1), make(chan error, 1)
		go func() { <-start; _, e := owner.Launch(ctx, launch); launched <- e }()
		go func() {
			<-start
			_, e := owner.HandoffUpdate(ctx, app.UpdateHandoffRequest{Target: target, Binding: binding, DaemonExitAcknowledged: true})
			handed <- e
		}()
		close(start)
		launchErr, handoffErr := <-launched, <-handed
		if (launchErr == nil) == (handoffErr == nil) {
			child.finish()
			t.Fatalf("launch/handoff accepted neither or both: launch=%v handoff=%v", launchErr, handoffErr)
		}
		if child.closes.Load() != 0 || child.effects.Load() != 0 {
			child.finish()
			t.Fatal("handoff touched concurrent native work")
		}
		if launchErr == nil && journal.reserves.Load() != 0 {
			child.finish()
			t.Fatal("stale empty handoff reserved after launch")
		}
		child.finish()
		if err = owner.AwaitIdle(ctx); err != nil {
			t.Fatal(err)
		}
	}
}

func TestUpdateHandoffCannotCloseLiveRunOrConsumeStaleEmptyPreview(t *testing.T) {
	ctx := context.Background()
	owner, launch, child, journal := updateFixture(t, &updateClock{at: time.Now()})
	if _, err := owner.InitializeLifecycle(ctx); err != nil {
		t.Fatal(err)
	}
	target := domain.UpdateTarget{Version: "v2", Protocol: 2}
	idle, err := owner.AttemptUpdate(ctx, target)
	if err != nil {
		t.Fatal(err)
	}
	old, _ := idle.Preview.Value()
	if _, err = owner.HandoffUpdate(ctx, app.UpdateHandoffRequest{Target: target, Binding: old}); !errors.Is(err, domain.ErrInvalid) {
		t.Fatal("missing daemon-exit consent accepted", err)
	}
	if _, err = owner.Launch(ctx, launch); err != nil {
		t.Fatal(err)
	}
	defer child.finish()
	if _, err = owner.HandoffUpdate(ctx, app.UpdateHandoffRequest{Target: target, Binding: old, DaemonExitAcknowledged: true}); !errors.Is(err, domain.ErrChanged) {
		t.Fatal("intervening launch survived stale empty handoff", err)
	}
	live, err := owner.PreviewShutdown(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = owner.HandoffUpdate(ctx, app.UpdateHandoffRequest{Target: target, Binding: live.Binding, DaemonExitAcknowledged: true}); !errors.Is(err, domain.ErrInvalid) {
		t.Fatal("nonempty update handoff accepted", err)
	}
	if journal.reserves.Load() != 0 || journal.handoffs != 0 || child.closes.Load() != 0 || child.effects.Load() != 0 || owner.Disabled() {
		t.Fatal("rejected handoff changed native/admission state")
	}
}

func TestUpdateHandoffFailuresPreserveDeferralAndUncertainty(t *testing.T) {
	for _, phase := range []string{"definite-reserve", "unknown-reserve", "unknown-final"} {
		t.Run(phase, func(t *testing.T) {
			ctx := context.Background()
			owner, _, child, journal := updateFixture(t, &updateClock{at: time.Now()})
			target := domain.UpdateTarget{Version: "v2", Protocol: 2}
			journal.state = types.Some(domain.UpdateDeferralState{Target: target, Started: time.Now().UTC().Add(-time.Hour)})
			if _, err := owner.InitializeLifecycle(ctx); err != nil {
				t.Fatal(err)
			}
			idle, err := owner.AttemptUpdate(ctx, target)
			if err != nil {
				t.Fatal(err)
			}
			binding, _ := idle.Preview.Value()
			switch phase {
			case "definite-reserve":
				journal.shutdownReserveErr = domain.ErrLimit
			case "unknown-reserve":
				journal.shutdownReserveErr = app.ErrLifecycleCommitUncertain
			case "unknown-final":
				journal.handoffErr = app.ErrLifecycleCommitUncertain
			}
			_, err = owner.HandoffUpdate(ctx, app.UpdateHandoffRequest{Target: target, Binding: binding, DaemonExitAcknowledged: true})
			if phase == "definite-reserve" {
				if !errors.Is(err, domain.ErrLimit) || owner.Disabled() {
					t.Fatal("definite failure did not restore admission", err)
				}
			} else if !errors.Is(err, app.ErrShutdownUnknown) || !owner.Disabled() {
				t.Fatal("unknown handoff not frozen", err)
			}
			state, _ := journal.snapshot()
			if state.Started.IsZero() || child.closes.Load() != 0 || child.effects.Load() != 0 {
				t.Fatal("failed handoff retired history or touched native process")
			}
			if phase != "definite-reserve" {
				if err = owner.PublishCleanShutdown(ctx); !errors.Is(err, app.ErrShutdownUnknown) || journal.cleans.Load() != 0 {
					t.Fatal("ambiguous empty handoff fabricated clean", err)
				}
			}
		})
	}
}
