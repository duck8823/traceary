package runtime_test

import (
	"context"
	"errors"
	"fmt"
	app "github.com/duck8823/traceary/application/runtime"
	domain "github.com/duck8823/traceary/domain/runtime"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// These fakes expose the production journal/native boundaries without storing raw input.
type controlChild struct {
	*fakeChild
	effects          atomic.Int32
	invalid          atomic.Bool
	entered, release chan struct{}
	once             sync.Once
	result           app.ControlEffect
}

func (c *controlChild) ValidateControl(context.Context, domain.ControlIntent) error {
	if c.invalid.Load() {
		return domain.ErrChanged
	}
	return nil
}
func (c *controlChild) ApplyControl(ctx context.Context, _ domain.ControlIntent, _ []byte) (app.ControlEffect, error) {
	select {
	case <-c.exited:
		return app.ControlEffect{Outcome: domain.ControlUnknown}, domain.ErrChanged
	default:
	}
	c.effects.Add(1)
	if c.entered != nil {
		c.once.Do(func() { close(c.entered) })
		select {
		case <-c.release:
		case <-ctx.Done():
			return app.ControlEffect{Outcome: domain.ControlUnknown}, fmt.Errorf("synthetic effect: %w", ctx.Err())
		}
	}
	return c.result, nil
}

type controlFactory struct{ child *controlChild }

func (f controlFactory) Start(context.Context, domain.LaunchDescriptor) (app.OwnedChild, error) {
	return f.child, nil
}

type controlJournal struct {
	*launchJournal
	mu                  sync.Mutex
	reserved, saved     []domain.ControlSnapshot
	reserveErr, saveErr error
	reserveHook         func()
}

func (j *controlJournal) ReserveControl(_ context.Context, _ app.OutputSelection, r domain.ControlReceipt) error {
	j.mu.Lock()
	defer j.mu.Unlock()
	if j.reserveErr != nil {
		return j.reserveErr
	}
	j.reserved = append(j.reserved, r.Snapshot())
	if j.reserveHook != nil {
		j.reserveHook()
	}
	return nil
}
func (j *controlJournal) SaveControl(_ context.Context, _ app.OutputSelection, r domain.ControlReceipt) error {
	j.mu.Lock()
	defer j.mu.Unlock()
	j.saved = append(j.saved, r.Snapshot())
	return j.saveErr
}
func controlFixture(t *testing.T) (*app.Runtime, app.ControlRequest, *controlChild, *controlJournal, *launchRegistry) {
	t.Helper()
	_, launch, base, registry := runtimeFixture(t, false)
	// The registered immutable profile is available from the same canonical launch fixture.
	state := domain.HostProfileState{ID: "codex-fixture", Host: "codex", Executable: "/owned/agent", ExecutableWitness: "fixture", Version: "fixture 1"}
	profile, err := domain.HostProfileFrom(state)
	if err != nil {
		t.Fatal(err)
	}
	child := &controlChild{fakeChild: base.child, result: app.ControlEffect{Outcome: domain.ControlDelivered, WrittenBytes: 3}}
	journal := &controlJournal{launchJournal: base.journal}
	owner, err := app.NewRuntime(registry, launchProfiles{profile}, controlFactory{child}, journal, launch.Epoch)
	if err != nil {
		t.Fatal(err)
	}
	run, err := owner.Launch(context.Background(), launch)
	if err != nil {
		t.Fatal(err)
	}
	intent, err := domain.ControlIntentFrom(domain.ControlIntentState{RequestID: "manual-1", Kind: domain.ControlInput, ManualTerminal: true, InputBytes: 3})
	if err != nil {
		t.Fatal(err)
	}
	selection := app.OutputSelection{Identity: run.Identity, Worktree: run.Target.Worktree().ID(), Profile: run.Profile.ID()}
	return owner, app.ControlRequest{Selection: selection, Intent: intent, Data: []byte("raw")}, child, journal, registry
}
func TestControlRuntimeDuplicatesInspectWithoutReplayAfterTargetChangeAndExit(t *testing.T) {
	owner, request, child, journal, registry := controlFixture(t)
	first, err := owner.Control(context.Background(), request)
	if err != nil || first.Receipt.Outcome != domain.ControlDelivered {
		t.Fatalf("explicit input: %v", err)
	}
	registry.changed.Store(true)
	child.invalid.Store(true)
	child.finish()
	if _, err = owner.AwaitExit(context.Background(), request.Selection.Identity); err != nil {
		t.Fatal(err)
	}
	again, err := owner.Control(context.Background(), request)
	if err != nil || again != first || child.effects.Load() != 1 {
		t.Fatal("receipt inspection replayed or revalidated an already completed operation")
	}
	receipt, err := owner.Receipt(request.Selection, "manual-1")
	if err != nil || receipt != first {
		t.Fatal("receipt unavailable after exit")
	}
	altered := request
	altered.Data = []byte("new")
	if _, err = owner.Control(context.Background(), altered); !errors.Is(err, domain.ErrChanged) {
		t.Fatal("conflicting duplicate accepted")
	}
	journal.mu.Lock()
	defer journal.mu.Unlock()
	if len(journal.reserved) != 1 || len(journal.saved) != 1 {
		t.Fatal("duplicate altered durable evidence")
	}
}
func TestControlRuntimeCleanReservationFailureAndPartialUnknown(t *testing.T) {
	owner, request, child, journal, _ := controlFixture(t)
	journal.reserveErr = domain.ErrLimit
	if _, err := owner.Control(context.Background(), request); !errors.Is(err, domain.ErrLimit) {
		t.Fatal("clean storage failure not rejected")
	}
	if child.effects.Load() != 0 {
		t.Fatal("input written without durable intent")
	}
	journal.reserveErr = nil
	child.result = app.ControlEffect{Outcome: domain.ControlUnknown, WrittenBytes: 1}
	got, err := owner.Control(context.Background(), request)
	if !errors.Is(err, app.ErrControlUnknown) || got.Receipt.WrittenBytes != 1 {
		t.Fatalf("partial delivery falsely confirmed: %v", err)
	}
	again, err := owner.Control(context.Background(), request)
	if !errors.Is(err, app.ErrControlUnknown) || again != got || child.effects.Load() != 1 {
		t.Fatal("unknown input replayed")
	}
}
func TestControlRuntimeChangedWitnessRefusesBeforeReservation(t *testing.T) {
	owner, request, child, journal, registry := controlFixture(t)
	registry.changed.Store(true)
	if _, err := owner.Control(context.Background(), request); !errors.Is(err, domain.ErrChanged) {
		t.Fatal("changed target allowed input")
	}
	registry.changed.Store(false)
	child.invalid.Store(true)
	if _, err := owner.Control(context.Background(), request); !errors.Is(err, domain.ErrChanged) {
		t.Fatal("changed native witness allowed input")
	}
	journal.mu.Lock()
	defer journal.mu.Unlock()
	if len(journal.reserved) != 0 || child.effects.Load() != 0 {
		t.Fatal("refused witness consumed a durable slot/effect")
	}
}
func TestControlRuntimeCallerCancelKeepsCoordinatorAndAwaitIdleOwnership(t *testing.T) {
	owner, request, child, _, _ := controlFixture(t)
	child.entered = make(chan struct{})
	child.release = make(chan struct{})
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { _, err := owner.Control(ctx, request); done <- err }()
	t.Cleanup(func() {
		select {
		case <-child.release:
		default:
			close(child.release)
		}
	})
	select {
	case <-child.entered:
	case err := <-done:
		t.Fatalf("control failed before effect: %v", err)
	case <-time.After(3 * time.Second):
		t.Fatal("control never reached effect")
	}
	cancel()
	if err := <-done; !errors.Is(err, app.ErrControlUnknown) {
		t.Fatalf("post-dispatch cancellation: %v", err)
	}
	child.finish()
	if _, err := owner.AwaitExit(context.Background(), request.Selection.Identity); err != nil {
		t.Fatal(err)
	}
	waitCtx, waitCancel := context.WithTimeout(context.Background(), time.Millisecond)
	defer waitCancel()
	if err := owner.AwaitIdle(waitCtx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal("idle released ownership while admitted control unresolved")
	}
	close(child.release)
	finalCtx, finalCancel := context.WithTimeout(context.Background(), time.Second)
	defer finalCancel()
	if err := owner.AwaitIdle(finalCtx); err != nil {
		t.Fatal(err)
	}
	receipt, err := owner.Receipt(request.Selection, "manual-1")
	if err != nil || receipt.Receipt.Outcome != domain.ControlDelivered || child.effects.Load() != 1 {
		t.Fatal("cancelled caller lost actual coordinator reconciliation")
	}
}

func TestControlRuntimeUncertainCommitOrPublicationNeverReplaysInput(t *testing.T) {
	for _, mode := range []string{"intent_commit_uncertain", "post_effect_save_failure"} {
		t.Run(mode, func(t *testing.T) {
			owner, request, child, journal, _ := controlFixture(t)
			expectedWrites := int32(0)
			expectedBytes := 0
			if mode == "intent_commit_uncertain" {
				journal.reserveErr = app.ErrControlCommitUncertain
			} else {
				journal.saveErr = domain.ErrUnsafe
				expectedWrites = 1
				expectedBytes = 3
			}
			got, err := owner.Control(context.Background(), request)
			if !errors.Is(err, app.ErrControlUnknown) || got.Receipt.Outcome != domain.ControlUnknown || got.Receipt.WrittenBytes != expectedBytes || child.effects.Load() != expectedWrites {
				t.Fatalf("uncertain intent/publication misreported: %v", err)
			}
			again, err := owner.Control(context.Background(), request)
			if !errors.Is(err, app.ErrControlUnknown) || again != got || child.effects.Load() != expectedWrites {
				t.Fatal("uncertain operation replayed")
			}
		})
	}
}
func TestControlRuntimeForeignSelectionRefusesBeforeJournal(t *testing.T) {
	owner, request, child, journal, _ := controlFixture(t)
	for _, mutate := range []func(*app.OutputSelection){
		func(s *app.OutputSelection) { s.Identity.Epoch = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa" },
		func(s *app.OutputSelection) { s.Identity.Generation = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa" },
		func(s *app.OutputSelection) { s.Identity.Run = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa" },
		func(s *app.OutputSelection) {
			s.Worktree = "w_aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
		},
		func(s *app.OutputSelection) { s.Profile = "foreign" },
	} {
		changed := request
		mutate(&changed.Selection)
		if _, err := owner.Control(context.Background(), changed); !errors.Is(err, domain.ErrChanged) && !errors.Is(err, domain.ErrNotFound) {
			t.Fatalf("foreign control binding accepted: %v", err)
		}
	}
	journal.mu.Lock()
	defer journal.mu.Unlock()
	if len(journal.reserved) != 0 || child.effects.Load() != 0 {
		t.Fatal("foreign request reserved or delivered")
	}
}

func TestControlRuntimeExitAfterDurableIntentCompletesUnknown(t *testing.T) {
	owner, request, child, journal, _ := controlFixture(t)
	journal.reserveHook = child.finish
	result, err := owner.Control(context.Background(), request)
	if !errors.Is(err, app.ErrControlUnknown) || result.Receipt.Outcome != domain.ControlUnknown || result.Receipt.WrittenBytes != 0 || child.effects.Load() != 0 {
		t.Fatalf("intent-to-exit window misreported: %v", err)
	}
	journal.mu.Lock()
	defer journal.mu.Unlock()
	if len(journal.saved) != 1 || journal.saved[0].Outcome != domain.ControlUnknown {
		t.Fatal("committed intent stranded pending after exit")
	}
}

type burstControlChild struct {
	*fakeChild
	entered chan struct{}
	release <-chan struct{}
}

func (c burstControlChild) ValidateControl(context.Context, domain.ControlIntent) error { return nil }
func (c burstControlChild) ApplyControl(ctx context.Context, _ domain.ControlIntent, _ []byte) (app.ControlEffect, error) {
	c.entered <- struct{}{}
	select {
	case <-c.release:
		return app.ControlEffect{Outcome: domain.ControlDelivered, WrittenBytes: 1}, nil
	case <-ctx.Done():
		return app.ControlEffect{Outcome: domain.ControlUnknown}, fmt.Errorf("synthetic control budget: %w", ctx.Err())
	}
}

type burstControlFactory struct {
	mu       sync.Mutex
	children []*fakeChild
	entered  chan struct{}
	release  <-chan struct{}
}

func (f *burstControlFactory) Start(context.Context, domain.LaunchDescriptor) (app.OwnedChild, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	child := &fakeChild{exited: make(chan struct{})}
	f.children = append(f.children, child)
	return burstControlChild{fakeChild: child, entered: f.entered, release: f.release}, nil
}
func TestControlRuntimeGlobalAdmissionBoundAndPerRunIsolation(t *testing.T) {
	_, launch, base, registry := runtimeFixture(t, false)
	profile, err := domain.HostProfileFrom(domain.HostProfileState{ID: "codex-fixture", Host: "codex", Executable: "/owned/agent", ExecutableWitness: "fixture", Version: "fixture 1"})
	if err != nil {
		t.Fatal(err)
	}
	entered := make(chan struct{}, domain.MaxControlCoordinators)
	release := make(chan struct{})
	var closeOnce sync.Once
	t.Cleanup(func() { closeOnce.Do(func() { close(release) }) })
	factory := &burstControlFactory{entered: entered, release: release}
	journal := &controlJournal{launchJournal: base.journal}
	owner, err := app.NewRuntime(registry, launchProfiles{profile}, factory, journal, launch.Epoch)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		factory.mu.Lock()
		defer factory.mu.Unlock()
		for _, child := range factory.children {
			child.finish()
		}
	})
	intent, _ := domain.ControlIntentFrom(domain.ControlIntentState{RequestID: "manual", Kind: domain.ControlInput, ManualTerminal: true, InputBytes: 1})
	requests := make([]app.ControlRequest, domain.MaxControlCoordinators+1)
	for i := range requests {
		candidate := launch
		candidate.RequestID = fmt.Sprintf("launch-%d", i)
		run, e := owner.Launch(context.Background(), candidate)
		if e != nil {
			t.Fatal(e)
		}
		requests[i] = app.ControlRequest{Selection: app.OutputSelection{Identity: run.Identity, Worktree: run.Target.Worktree().ID(), Profile: run.Profile.ID()}, Intent: intent, Data: []byte("x")}
	}
	results := make(chan error, domain.MaxControlCoordinators)
	for i := 0; i < domain.MaxControlCoordinators; i++ {
		go func(request app.ControlRequest) { _, e := owner.Control(context.Background(), request); results <- e }(requests[i])
	}
	deadline := time.After(3 * time.Second)
	for i := 0; i < domain.MaxControlCoordinators; i++ {
		select {
		case <-entered:
		case <-deadline:
			t.Fatal("bounded coordinators did not enter")
		}
	}
	overflowCtx, overflowCancel := context.WithTimeout(context.Background(), time.Second)
	defer overflowCancel()
	if _, err = owner.Control(overflowCtx, requests[domain.MaxControlCoordinators]); !errors.Is(err, app.ErrRuntimeBusy) {
		t.Fatal("coordinator limit expanded or queued unboundedly")
	}
	closeOnce.Do(func() { close(release) })
	for i := 0; i < domain.MaxControlCoordinators; i++ {
		select {
		case e := <-results:
			if e != nil {
				t.Fatal(e)
			}
		case <-time.After(3 * time.Second):
			t.Fatal("coordinator failed to reconcile")
		}
	}
	journal.mu.Lock()
	defer journal.mu.Unlock()
	if len(journal.reserved) != domain.MaxControlCoordinators {
		t.Fatal("busy overflow consumed a durable receipt")
	}
}

type changingControlProfiles struct {
	profile domain.HostProfile
	changed atomic.Bool
}

func (c *changingControlProfiles) LookupProfile(context.Context, domain.ProfileID) (domain.HostProfile, error) {
	if !c.changed.Load() {
		return c.profile, nil
	}
	state := c.profile.Snapshot()
	state.ExecutableWitness = "changed"
	profile, err := domain.HostProfileFrom(state)
	if err != nil {
		return domain.HostProfile{}, fmt.Errorf("synthetic profile: %w", err)
	}
	return profile, nil
}
func TestControlRuntimeStoredProfileChangeRefusesBeforeReservation(t *testing.T) {
	_, launch, base, registry := runtimeFixture(t, false)
	profile, err := domain.HostProfileFrom(domain.HostProfileState{ID: "codex-fixture", Host: "codex", Executable: "/owned/agent", ExecutableWitness: "fixture", Version: "fixture 1"})
	if err != nil {
		t.Fatal(err)
	}
	catalog := &changingControlProfiles{profile: profile}
	child := &controlChild{fakeChild: base.child, result: app.ControlEffect{Outcome: domain.ControlDelivered, WrittenBytes: 1}}
	journal := &controlJournal{launchJournal: base.journal}
	owner, err := app.NewRuntime(registry, catalog, controlFactory{child}, journal, launch.Epoch)
	if err != nil {
		t.Fatal(err)
	}
	run, err := owner.Launch(context.Background(), launch)
	if err != nil {
		t.Fatal(err)
	}
	catalog.changed.Store(true)
	intent, _ := domain.ControlIntentFrom(domain.ControlIntentState{RequestID: "refused", Kind: domain.ControlInput, ManualTerminal: true, InputBytes: 1})
	request := app.ControlRequest{Selection: app.OutputSelection{Identity: run.Identity, Worktree: run.Target.Worktree().ID(), Profile: run.Profile.ID()}, Intent: intent, Data: []byte("x")}
	if _, err = owner.Control(context.Background(), request); !errors.Is(err, domain.ErrChanged) {
		t.Fatal("changed stored profile allowed control")
	}
	journal.mu.Lock()
	defer journal.mu.Unlock()
	if len(journal.reserved) != 0 || child.effects.Load() != 0 {
		t.Fatal("changed profile consumed intent or effect")
	}
}
