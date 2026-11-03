package runtime_test

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	app "github.com/duck8823/traceary/application/runtime"
	domain "github.com/duck8823/traceary/domain/runtime"
)

type launchRegistry struct {
	target  domain.Target
	changed atomic.Bool
	gate    chan struct{}
	entered chan struct{}
	once    sync.Once
}

func (r *launchRegistry) Resolve(ctx context.Context, _ domain.WorktreeID, _ string) (domain.Target, error) {
	if r.gate != nil {
		r.once.Do(func() { close(r.entered) })
		select {
		case <-r.gate:
		case <-ctx.Done():
			return domain.Target{}, fmt.Errorf("fixture resolve: %w", ctx.Err())
		}
	}
	if r.changed.Load() {
		return domain.Target{}, domain.ErrChanged
	}
	return r.target, nil
}
func (r *launchRegistry) Revalidate(context.Context, domain.Target, string) error {
	if r.changed.Load() {
		return domain.ErrChanged
	}
	return nil
}

type launchProfiles struct{ profile domain.HostProfile }

func (p launchProfiles) LookupProfile(context.Context, domain.ProfileID) (domain.HostProfile, error) {
	return p.profile, nil
}

type launchJournal struct {
	fail  bool
	mu    sync.Mutex
	saved []domain.RunSnapshot
}

func (j *launchJournal) ReserveLaunch(context.Context, domain.RunSnapshot) error { return nil }
func (j *launchJournal) SaveLaunch(_ context.Context, s domain.RunSnapshot) error {
	j.mu.Lock()
	defer j.mu.Unlock()
	if j.fail {
		return domain.ErrUnsafe
	}
	j.saved = append(j.saved, s)
	return nil
}

type fakeChild struct{ exited chan struct{} }

func (c *fakeChild) PID() int           { return 321 }
func (c *fakeChild) Wait() (int, error) { <-c.exited; return 7, nil }

type launchFactory struct {
	calls atomic.Int32
	child *fakeChild
	fail  bool
	start func()
}

func (f *launchFactory) Start(ctx context.Context, _ domain.LaunchDescriptor) (app.OwnedChild, error) {
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("fixture start: %w", err)
	}
	f.calls.Add(1)
	if f.start != nil {
		f.start()
	}
	if f.fail {
		return nil, domain.ErrUnsafe
	}
	return f.child, nil
}
func runtimeFixture(t *testing.T, journalFailure bool) (*app.Runtime, app.LaunchRequest, *launchFactory, *launchRegistry) {
	t.Helper()
	p, err := domain.ProjectOf("/project", "/project/.git", "p")
	if err != nil {
		t.Fatal(err)
	}
	w, err := domain.WorktreeOf(p.ID(), "/project", "/project/.git", "/store.db", "w")
	if err != nil {
		t.Fatal(err)
	}
	registry := domain.NewRegistry()
	_ = registry.AddProject(p)
	_ = registry.AddWorktree(w)
	target, err := registry.Target(w.ID(), "codex-fixture")
	if err != nil {
		t.Fatal(err)
	}
	profile, err := domain.HostProfileFrom(domain.HostProfileState{ID: "codex-fixture", Host: "codex", Executable: "/owned/agent", ExecutableWitness: "fixture", Version: "fixture 1"})
	if err != nil {
		t.Fatal(err)
	}
	factory := &launchFactory{child: &fakeChild{exited: make(chan struct{})}}
	r := &launchRegistry{target: target}
	service, err := app.NewRuntime(r, launchProfiles{profile}, factory, &launchJournal{fail: journalFailure}, strings.Repeat("e", 32))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { close(factory.child.exited) })
	return service, app.LaunchRequest{Epoch: strings.Repeat("e", 32), Worktree: w.ID(), Profile: "codex-fixture", RequestID: "request"}, factory, r
}
func TestRuntimeConcurrentDuplicatesShareOneImmutableLaunch(t *testing.T) {
	service, req, factory, _ := runtimeFixture(t, false)
	const count = 20
	var wg sync.WaitGroup
	results := make(chan domain.RunSnapshot, count)
	errs := make(chan error, count)
	for range count {
		wg.Go(func() { result, err := service.Launch(context.Background(), req); results <- result; errs <- err })
	}
	wg.Wait()
	close(results)
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	var first domain.RunSnapshot
	for result := range results {
		if first.Identity.Run == "" {
			first = result
		}
		if result.Identity != first.Identity || result.Target != first.Target {
			t.Fatal("duplicate rebound launch")
		}
	}
	if factory.calls.Load() != 1 || len(service.List()) != 1 {
		t.Fatal("duplicate process started")
	}
	req.Profile = "other"
	if _, err := service.Launch(context.Background(), req); !errors.Is(err, domain.ErrChanged) {
		t.Fatalf("request rebind: %v", err)
	}
	req.Profile = "codex-fixture"
	req.RequestID = "second"
	next, err := service.Launch(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	if next.Identity == first.Identity || next.Target != first.Target {
		t.Fatal("same cwd runs conflated")
	}
}
func TestRuntimeKnownFailureIsNotRetriedAndStaleEpochDoesNotLaunch(t *testing.T) {
	service, req, factory, _ := runtimeFixture(t, false)
	req.Epoch = "stale"
	if _, err := service.Launch(context.Background(), req); !errors.Is(err, domain.ErrChanged) {
		t.Fatalf("stale epoch: %v", err)
	}
	req.Epoch = strings.Repeat("e", 32)
	factory.fail = true
	for range 2 {
		if _, err := service.Launch(context.Background(), req); err == nil {
			t.Fatal("failure became success")
		}
	}
	if factory.calls.Load() != 1 {
		t.Fatal("failed launch replayed")
	}
}
func TestRuntimeCancellationAfterSpawnDoesNotKillOrLoseOwnedChild(t *testing.T) {
	service, req, factory, _ := runtimeFixture(t, false)
	ctx, cancel := context.WithCancel(context.Background())
	factory.start = cancel
	result, err := service.Launch(ctx, req)
	// The caller may leave before publication completes, so delivery can be
	// unknown. The same explicit request must recover its receipt, not respawn.
	if err != nil && !errors.Is(err, app.ErrLaunchUnknown) {
		t.Fatal(err)
	}
	if result.Identity.Run == "" || !service.HasActive() {
		t.Fatal("started reservation lost on cancellation")
	}
	result, err = service.Launch(context.Background(), req)
	if err != nil || factory.calls.Load() != 1 {
		t.Fatalf("receipt lookup replayed child: %v", err)
	}
	if result.State != domain.RunLive || !service.HasActive() {
		t.Fatal("caller cancellation lost process ownership")
	}
	wrong := result.Identity
	wrong.Generation = strings.Repeat("9", 32)
	if _, err = service.AwaitExit(context.Background(), wrong); !errors.Is(err, domain.ErrChanged) {
		t.Fatalf("stale generation: %v", err)
	}
	ctx, stop := context.WithTimeout(context.Background(), time.Millisecond)
	defer stop()
	if _, err = service.AwaitExit(ctx, result.Identity); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("live child treated as exited: %v", err)
	}
}
func TestRuntimeJournalFailureRetainsUnknownLaunchWithoutReplay(t *testing.T) {
	service, req, factory, _ := runtimeFixture(t, true)
	first, err := service.Launch(context.Background(), req)
	if !errors.Is(err, app.ErrLaunchUnknown) || first.Identity.Run == "" || !first.PublicationUncertain || !service.HasActive() {
		t.Fatalf("spawn lost on journal failure: %v", err)
	}
	next, err := service.Launch(context.Background(), req)
	if !errors.Is(err, app.ErrLaunchUnknown) || next.Identity != first.Identity || factory.calls.Load() != 1 {
		t.Fatal("unknown outcome retried")
	}
}
func TestRuntimeChangedTargetAndPreCanceledCallerCannotStart(t *testing.T) {
	service, req, factory, registry := runtimeFixture(t, false)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := service.Launch(ctx, req); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled: %v", err)
	}
	registry.changed.Store(true)
	if _, err := service.Launch(context.Background(), req); !errors.Is(err, domain.ErrChanged) {
		t.Fatalf("changed: %v", err)
	}
	if factory.calls.Load() != 0 {
		t.Fatal("unsafe target launched")
	}
}

func TestRuntimeLedgerAndActiveBoundsStillPermitExistingReceiptLookup(t *testing.T) {
	service, req, factory, _ := runtimeFixture(t, false)
	original := req
	for i := range domain.MaxActiveRuns {
		req.RequestID = fmt.Sprintf("live-%d", i)
		if _, err := service.Launch(context.Background(), req); err != nil {
			t.Fatal(err)
		}
	}
	req.RequestID = "overflow"
	if _, err := service.Launch(context.Background(), req); !errors.Is(err, domain.ErrLimit) {
		t.Fatalf("active overflow: %v", err)
	}
	original.RequestID = "live-0"
	if _, err := service.Launch(context.Background(), original); err != nil {
		t.Fatalf("existing receipt hidden by capacity: %v", err)
	}
	if factory.calls.Load() != domain.MaxActiveRuns {
		t.Fatal("capacity admitted extra child")
	}
}
func TestRuntimeFailedRequestLedgerNeverEvictsOrReplaysAtCapacity(t *testing.T) {
	service, req, factory, _ := runtimeFixture(t, false)
	factory.fail = true
	for i := range domain.MaxLaunchRequests {
		req.RequestID = fmt.Sprintf("failed-%d", i)
		if _, err := service.Launch(context.Background(), req); !errors.Is(err, domain.ErrUnsafe) {
			t.Fatalf("failed request: %v", err)
		}
	}
	req.RequestID = "overflow"
	if _, err := service.Launch(context.Background(), req); !errors.Is(err, domain.ErrLimit) {
		t.Fatalf("ledger overflow: %v", err)
	}
	req.RequestID = "failed-0"
	if _, err := service.Launch(context.Background(), req); !errors.Is(err, domain.ErrUnsafe) {
		t.Fatalf("original receipt evicted: %v", err)
	}
	if factory.calls.Load() != domain.MaxLaunchRequests {
		t.Fatal("failed request replayed")
	}
}

type observedWaitContext struct {
	context.Context
	entered chan struct{}
	once    sync.Once
}

func (c *observedWaitContext) Done() <-chan struct{} {
	c.once.Do(func() { close(c.entered) })
	return c.Context.Done()
}
func TestRuntimeOneCanceledPreSpawnWaiterCannotPoisonAnother(t *testing.T) {
	service, req, factory, registry := runtimeFixture(t, false)
	registry.gate = make(chan struct{})
	registry.entered = make(chan struct{})
	first, cancel := context.WithCancel(context.Background())
	defer cancel()
	firstDone := make(chan error, 1)
	go func() { _, err := service.Launch(first, req); firstDone <- err }()
	<-registry.entered
	secondContext, stop := context.WithTimeout(context.Background(), 3*time.Second)
	defer stop()
	observed := &observedWaitContext{Context: secondContext, entered: make(chan struct{})}
	secondDone := make(chan error, 1)
	go func() { _, err := service.Launch(observed, req); secondDone <- err }()
	<-observed.entered
	cancel()
	if err := <-firstDone; !errors.Is(err, context.Canceled) {
		t.Fatalf("first cancellation: %v", err)
	}
	close(registry.gate)
	if err := <-secondDone; err != nil {
		t.Fatalf("live waiter poisoned: %v", err)
	}
	if factory.calls.Load() != 1 {
		t.Fatal("duplicate spawn after waiter cancellation")
	}
}
func TestRuntimeAllPreSpawnWaitersMayCancelWithoutPoisoningFutureRequest(t *testing.T) {
	service, req, factory, registry := runtimeFixture(t, false)
	registry.gate = make(chan struct{})
	registry.entered = make(chan struct{})
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { _, err := service.Launch(ctx, req); done <- err }()
	<-registry.entered
	cancel()
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatalf("cancel pending: %v", err)
	}
	close(registry.gate)
	if _, err := service.Launch(context.Background(), req); err != nil {
		t.Fatalf("pre-start abandonment poisoned request: %v", err)
	}
	if factory.calls.Load() != 1 {
		t.Fatal("canceled reservation started a second child")
	}
}
