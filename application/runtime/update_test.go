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

type updateClock struct {
	mu sync.Mutex
	at time.Time
}

func (c *updateClock) Now() time.Time   { c.mu.Lock(); defer c.mu.Unlock(); return c.at }
func (c *updateClock) set(at time.Time) { c.mu.Lock(); c.at = at; c.mu.Unlock() }

// This in-memory journal enforces the SAME continuous-start/notice monotonicity
// as ExecutionStore. It models metadata only, never a binary updater or native AI.
type updateJournal struct {
	*lifecycleTestJournal
	mu                             sync.Mutex
	state                          types.Optional[domain.UpdateDeferralState]
	saves                          int
	err                            error
	entered, release               chan struct{}
	enterOnce                      sync.Once
	handoffEntered, handoffRelease chan struct{}
	handoffErr                     error
	handoffs                       int
}

func (j *updateJournal) RecordEpochStart(_ context.Context, epoch string) (app.LifecycleSnapshot, error) {
	j.mu.Lock()
	defer j.mu.Unlock()
	j.starts.Add(1)
	return app.LifecycleSnapshot{Epoch: epoch, UpdateDeferral: j.state}, nil
}
func (j *updateJournal) SaveUpdateDeferral(ctx context.Context, _ string, s domain.UpdateDeferralState) error {
	j.mu.Lock()
	j.saves++
	failure, entered, release := j.err, j.entered, j.release
	j.mu.Unlock()
	if entered != nil {
		j.enterOnce.Do(func() { close(entered) })
		select {
		case <-release:
		case <-ctx.Done():
			return fmt.Errorf("synthetic update journal wait: %w", ctx.Err())
		}
	}
	if failure != nil {
		return failure
	}
	j.mu.Lock()
	defer j.mu.Unlock()
	if old, ok := j.state.Value(); ok && (!old.Started.Equal(s.Started) || old.Notified24h && !s.Notified24h) {
		return domain.ErrChanged
	}
	j.state = types.Some(s)
	return nil
}
func (j *updateJournal) snapshot() (domain.UpdateDeferralState, int) {
	j.mu.Lock()
	defer j.mu.Unlock()
	s, _ := j.state.Value()
	return s, j.saves
}

func updateFixture(t *testing.T, clock *updateClock) (*app.Runtime, app.LaunchRequest, *shutdownChild, *updateJournal) {
	t.Helper()
	_, launch, base, registry := runtimeFixture(t, false)
	profile, err := domain.HostProfileFrom(domain.HostProfileState{ID: "codex-fixture", Host: "codex", Executable: "/owned/agent", ExecutableWitness: "fixture", Version: "fixture 1"})
	if err != nil {
		t.Fatal(err)
	}
	child := &shutdownChild{controlChild: &controlChild{fakeChild: base.child, result: app.ControlEffect{Outcome: domain.ControlDelivered, WrittenBytes: 3}}}
	journal := &updateJournal{lifecycleTestJournal: &lifecycleTestJournal{shutdownJournal: &shutdownJournal{controlJournal: &controlJournal{launchJournal: base.journal}}}}
	owner, err := app.NewRuntimeWithClock(registry, launchProfiles{profile}, shutdownFactory{child}, journal, launch.Epoch, clock)
	if err != nil {
		t.Fatal(err)
	}
	return owner, launch, child, journal
}

func TestUpdateDefersWithoutNativeEffectsAndPublishesContinuous24HourNotice(t *testing.T) {
	start := time.Now()
	clock := &updateClock{at: start}
	owner, launch, child, journal := updateFixture(t, clock)
	if _, err := owner.InitializeLifecycle(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err := owner.Launch(context.Background(), launch); err != nil {
		t.Fatal(err)
	}
	first, err := owner.AttemptUpdate(context.Background(), domain.UpdateTarget{Version: "v2", Protocol: 2})
	if err != nil || !first.Notice || first.Status.Quiescent {
		t.Fatalf("active update not deferred: %+v %v", first, err)
	}
	state, saves := journal.snapshot()
	if state.Started != start.UTC().Round(0) || state.Notified24h || saves != 1 || child.effects.Load() != 0 || child.closes.Load() != 0 || owner.Disabled() {
		t.Fatal("deferral changed native/admission state or time")
	}
	clock.set(start.Add(24*time.Hour - time.Second))
	second, err := owner.AttemptUpdate(context.Background(), domain.UpdateTarget{Version: "v3", Protocol: 3})
	if err != nil || !second.Notice {
		t.Fatal("retarget attempt notice missing", err)
	}
	retarget, saves := journal.snapshot()
	if retarget.Started != state.Started || retarget.Notified24h || retarget.Target.Version != "v3" {
		t.Fatal("retarget reset continuous start")
	}
	clock.set(start.Add(24 * time.Hour))
	for range 2 {
		status, e := owner.UpdateStatus(context.Background())
		if e != nil || !status.Overdue24h {
			t.Fatal("read-only overdue status missing", e)
		}
	}
	_, afterReads := journal.snapshot()
	if afterReads != saves {
		t.Fatal("status consumed notice or persisted metadata")
	}
	if err = owner.PublishDueUpdateNotice(context.Background()); err != nil {
		t.Fatal(err)
	}
	notified, afterPublish := journal.snapshot()
	if !notified.Notified24h || afterPublish != saves+1 {
		t.Fatal("24h durable notice missing")
	}
	if err = owner.PublishDueUpdateNotice(context.Background()); err != nil {
		t.Fatal(err)
	}
	_, afterDuplicate := journal.snapshot()
	if afterDuplicate != afterPublish {
		t.Fatal("timer duplicated published notice")
	}
	third, err := owner.AttemptUpdate(context.Background(), domain.UpdateTarget{Version: "v4", Protocol: 4})
	if err != nil || !third.Notice || !third.Status.Overdue24h {
		t.Fatal("prior24h notice suppressed explicit attempt", err)
	}
	clock.set(start.Add(-time.Hour))
	status, err := owner.UpdateStatus(context.Background())
	if err != nil || status.Overdue24h {
		t.Fatal("rollback invented elapsed duration", err)
	}
	last, _ := journal.snapshot()
	if last.Started != state.Started || !last.Notified24h {
		t.Fatal("rollback reset durable history")
	}
	child.finish()
	if err = owner.AwaitIdle(context.Background()); err != nil {
		t.Fatal(err)
	}
	idle, err := owner.AttemptUpdate(context.Background(), domain.UpdateTarget{Version: "v4", Protocol: 4})
	preview, found := idle.Preview.Value()
	if err != nil || !idle.Status.Quiescent || !found || preview.TotalRuns != 0 || preview.PendingLaunches+preview.PendingControls+preview.PendingReconciliations != 0 {
		t.Fatalf("idle handoff snapshot missing: %+v %v", idle, err)
	}
	retained, _ := journal.snapshot()
	if retained.Started != state.Started {
		t.Fatal("idle snapshot retired deferral before explicit handoff")
	}
}

func TestUpdateRestoresDeferralAcrossEpochInitialization(t *testing.T) {
	start := time.Date(2026, time.January, 2, 3, 4, 5, 123000000, time.UTC)
	clock := &updateClock{at: start.Add(25 * time.Hour)}
	owner, _, child, journal := updateFixture(t, clock)
	journal.state = types.Some(domain.UpdateDeferralState{Target: domain.UpdateTarget{Version: "v2", Protocol: 2}, Started: start, Notified24h: true})
	if _, err := owner.InitializeLifecycle(context.Background()); err != nil {
		t.Fatal(err)
	}
	result, err := owner.AttemptUpdate(context.Background(), domain.UpdateTarget{Version: "v3", Protocol: 3})
	if err != nil || !result.Status.Quiescent || !result.Notice || !result.Status.Overdue24h {
		t.Fatal("restored deferral unavailable", err)
	}
	state, _ := journal.snapshot()
	if state.Started != start || !state.Notified24h || state.Target.Version != "v3" || child.effects.Load() != 0 || child.closes.Load() != 0 {
		t.Fatal("restart reset or invoked native effect")
	}
}

func TestUpdateMetadataCoordinatorInvalidatesOldPreviewWithoutBlockingOwnedControls(t *testing.T) {
	owner, launch, child, journal := updateFixture(t, &updateClock{at: time.Now()})
	if _, err := owner.InitializeLifecycle(context.Background()); err != nil {
		t.Fatal(err)
	}
	run, err := owner.Launch(context.Background(), launch)
	if err != nil {
		t.Fatal(err)
	}
	preview, err := owner.PreviewShutdown(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	journal.entered = make(chan struct{})
	journal.release = make(chan struct{})
	var once sync.Once
	release := func() { once.Do(func() { close(journal.release) }) }
	defer release()
	done := make(chan error, 1)
	go func() {
		_, e := owner.AttemptUpdate(context.Background(), domain.UpdateTarget{Version: "v2", Protocol: 2})
		done <- e
	}()
	<-journal.entered
	if _, err = owner.PreviewShutdown(context.Background()); !errors.Is(err, app.ErrRuntimeBusy) {
		t.Fatal("new shutdown raced metadata coordinator", err)
	}
	if _, err = owner.ConfirmShutdown(context.Background(), app.ShutdownConfirmation{Binding: preview.Binding, WorkLossAcknowledged: true}); err == nil || journal.reserves.Load() != 0 {
		t.Fatal("old preview survived metadata admission")
	}
	input, err := domain.ControlIntentFrom(domain.ControlIntentState{RequestID: "during-update-metadata", Kind: domain.ControlInput, ManualTerminal: true, InputBytes: 3})
	if err != nil {
		t.Fatal(err)
	}
	selection := app.OutputSelection{Identity: run.Identity, Worktree: run.Target.Worktree().ID(), Profile: run.Profile.ID()}
	if _, err = owner.Control(context.Background(), app.ControlRequest{Selection: selection, Intent: input, Data: []byte("raw")}); err != nil || child.effects.Load() != 1 {
		t.Fatal("mere update deferral froze independent owned control", err)
	}
	release()
	if err = <-done; err != nil {
		t.Fatal(err)
	}
	if owner.Disabled() || child.closes.Load() != 0 {
		t.Fatal("metadata coordinator disabled launches or closed terminal")
	}
	child.finish()
	if err = owner.AwaitIdle(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func TestUpdateDefiniteAndAmbiguousPersistenceNeverStopChildOrFalsifyClean(t *testing.T) {
	for _, failure := range []error{domain.ErrLimit, app.ErrLifecycleCommitUncertain} {
		t.Run(failure.Error(), func(t *testing.T) {
			owner, launch, child, journal := updateFixture(t, &updateClock{at: time.Now()})
			journal.err = failure
			if _, err := owner.InitializeLifecycle(context.Background()); err != nil {
				t.Fatal(err)
			}
			if _, err := owner.Launch(context.Background(), launch); err != nil {
				t.Fatal(err)
			}
			result, err := owner.AttemptUpdate(context.Background(), domain.UpdateTarget{Version: "v2", Protocol: 2})
			if !errors.Is(err, failure) || !result.Notice || child.effects.Load() != 0 || child.closes.Load() != 0 {
				t.Fatal("metadata failure hidden or caused native effect", err)
			}
			ambiguous := errors.Is(failure, app.ErrLifecycleCommitUncertain)
			if owner.Disabled() != ambiguous {
				t.Fatal("definite vs ambiguous admission conflated")
			}
			child.finish()
			if err = owner.AwaitIdle(context.Background()); err != nil {
				t.Fatal(err)
			}
			owner.DisableLaunches()
			err = owner.PublishCleanShutdown(context.Background())
			if ambiguous && (!errors.Is(err, app.ErrShutdownUnknown) || journal.cleans.Load() != 0) {
				t.Fatal("ambiguous update metadata fabricated clean", err)
			}
			if !ambiguous && (err != nil || journal.cleans.Load() != 1) {
				t.Fatal("definite no-effect failure became permanently unknown", err)
			}
		})
	}
}

func TestUpdateConcurrentAcceptedAttemptsKeepTheirOwnTargetSnapshot(t *testing.T) {
	owner, launch, child, _ := updateFixture(t, &updateClock{at: time.Now()})
	if _, err := owner.InitializeLifecycle(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err := owner.Launch(context.Background(), launch); err != nil {
		t.Fatal(err)
	}
	defer child.finish()
	var group sync.WaitGroup
	for i := range 64 {
		group.Go(func() {
			target := domain.UpdateTarget{Version: fmt.Sprintf("v%d", i), Protocol: 2}
			result, err := owner.AttemptUpdate(context.Background(), target)
			if errors.Is(err, app.ErrRuntimeBusy) {
				if !result.Notice {
					t.Error("busy attempt notice missing")
				}
				return
			}
			if err != nil {
				t.Error(err)
				return
			}
			state, found := result.Status.Deferral.Value()
			if !found || state.Target != target || result.Target != target || !result.Notice {
				t.Errorf("accepted attempt acknowledged another target: requested=%+v snapshot=%+v", target, state)
			}
			if _, found = result.Preview.Value(); found {
				t.Error("active owner returned empty update preview")
			}
		})
	}
	group.Wait()
	if owner.Disabled() || child.effects.Load() != 0 || child.closes.Load() != 0 {
		t.Fatal("concurrent metadata work changed native authority")
	}
}

func TestUpdateInvalidClockAndRestoredStateCannotAdmitMetadataOrLaunch(t *testing.T) {
	owner, _, child, journal := updateFixture(t, &updateClock{})
	if _, err := owner.InitializeLifecycle(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err := owner.AttemptUpdate(context.Background(), domain.UpdateTarget{Version: "v2", Protocol: 2}); !errors.Is(err, domain.ErrInvalid) {
		t.Fatal("zero lifecycle clock accepted", err)
	}
	_, saves := journal.snapshot()
	if saves != 0 || child.effects.Load() != 0 || child.closes.Load() != 0 {
		t.Fatal("invalid clock mutated state")
	}
	broken, launch, other, brokenJournal := updateFixture(t, &updateClock{at: time.Now()})
	brokenJournal.state = types.Some(domain.UpdateDeferralState{Target: domain.UpdateTarget{Version: "v2", Protocol: 2}})
	if _, err := broken.InitializeLifecycle(context.Background()); !errors.Is(err, domain.ErrInvalid) {
		t.Fatal("malformed restored deferral accepted", err)
	}
	if _, err := broken.Launch(context.Background(), launch); !errors.Is(err, app.ErrRuntimeBusy) || other.effects.Load() != 0 || other.closes.Load() != 0 {
		t.Fatal("invalid lifecycle startup retained launch authority", err)
	}
}

func TestUpdateIdleSnapshotAndUnsupportedOwnersNeverInventDeferredHistory(t *testing.T) {
	owner, _, child, journal := updateFixture(t, &updateClock{at: time.Now()})
	if _, err := owner.UpdateStatus(context.Background()); !errors.Is(err, domain.ErrUnsupported) {
		t.Fatal("unenrolled owner claimed update support", err)
	}
	if _, err := owner.AttemptUpdate(context.Background(), domain.UpdateTarget{Version: "v2", Protocol: 2}); !errors.Is(err, domain.ErrUnsupported) {
		t.Fatal("unenrolled owner mutated lifecycle", err)
	}
	if err := owner.PublishDueUpdateNotice(context.Background()); !errors.Is(err, domain.ErrUnsupported) {
		t.Fatal("unenrolled owner claimed notice publication", err)
	}
	if _, err := owner.InitializeLifecycle(context.Background()); err != nil {
		t.Fatal(err)
	}
	result, err := owner.AttemptUpdate(context.Background(), domain.UpdateTarget{Version: "v2", Protocol: 2})
	preview, found := result.Preview.Value()
	if err != nil || !result.Notice || !result.Status.Quiescent || !found || preview.TotalRuns != 0 {
		t.Fatal("idle handoff snapshot unavailable", err)
	}
	if _, found = result.Status.Deferral.Value(); found {
		t.Fatal("idle attempt invented continuous deferral")
	}
	_, saves := journal.snapshot()
	if saves != 0 || owner.Disabled() || child.effects.Load() != 0 || child.closes.Load() != 0 {
		t.Fatal("idle snapshot persisted deferral or granted native effect")
	}
	if _, err = owner.AttemptUpdate(context.Background(), domain.UpdateTarget{Version: "https://not-an-updater", Protocol: 2}); !errors.Is(err, domain.ErrInvalid) {
		t.Fatal("update metadata accepted executable/URL", err)
	}
	_, after := journal.snapshot()
	if after != saves {
		t.Fatal("invalid target wrote metadata")
	}
}

type updateStartBarrier struct {
	child            app.OwnedChild
	entered, release chan struct{}
}

func (b updateStartBarrier) Start(ctx context.Context, _ domain.LaunchDescriptor) (app.OwnedChild, error) {
	close(b.entered)
	select {
	case <-b.release:
		return b.child, nil
	case <-ctx.Done():
		return nil, fmt.Errorf("synthetic launch wait: %w", ctx.Err())
	}
}
func TestUpdateDefersWhileOnlyNativeLaunchReservationExists(t *testing.T) {
	_, launch, base, registry := runtimeFixture(t, false)
	profile, err := domain.HostProfileFrom(domain.HostProfileState{ID: "codex-fixture", Host: "codex", Executable: "/owned/agent", ExecutableWitness: "fixture", Version: "fixture 1"})
	if err != nil {
		t.Fatal(err)
	}
	child := &shutdownChild{controlChild: &controlChild{fakeChild: base.child, result: app.ControlEffect{Outcome: domain.ControlDelivered}}}
	journal := &updateJournal{lifecycleTestJournal: &lifecycleTestJournal{shutdownJournal: &shutdownJournal{controlJournal: &controlJournal{launchJournal: base.journal}}}}
	entered, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	unblock := func() { once.Do(func() { close(release) }) }
	defer unblock()
	defer child.finish()
	owner, err := app.NewRuntimeWithClock(registry, launchProfiles{profile}, updateStartBarrier{child: child, entered: entered, release: release}, journal, launch.Epoch, &updateClock{at: time.Now()})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = owner.InitializeLifecycle(context.Background()); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { _, e := owner.Launch(context.Background(), launch); done <- e }()
	<-entered
	result, err := owner.AttemptUpdate(context.Background(), domain.UpdateTarget{Version: "v2", Protocol: 2})
	if err != nil || result.Status.Quiescent || !result.Notice {
		t.Fatal("pending launch treated as idle", err)
	}
	found := false
	for _, reason := range result.Status.Blockers {
		if reason == "pending_launches" {
			found = true
		}
	}
	if !found {
		t.Fatal("pending native launch blocker disappeared")
	}
	if _, exists := result.Preview.Value(); exists || owner.Disabled() || child.closes.Load() != 0 {
		t.Fatal("pending launch permitted handoff or was stopped")
	}
	unblock()
	if err = <-done; err != nil {
		t.Fatal("deferral metadata invalidated admitted launch", err)
	}
	child.finish()
	if err = owner.AwaitIdle(context.Background()); err != nil {
		t.Fatal(err)
	}
}
