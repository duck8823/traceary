package runtime_test

import (
	"context"
	"errors"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	app "github.com/duck8823/traceary/application/runtime"
	domain "github.com/duck8823/traceary/domain/runtime"
)

// This local sampler exercises the trusted native-sampling contract without
// claiming physical sleep, native hook binding, or process liveness evidence.
type continuitySampler struct {
	mu        sync.Mutex
	power     domain.PowerWitness
	available bool
}

func (s *continuitySampler) Sample(context.Context) domain.ContinuitySample {
	s.mu.Lock()
	defer s.mu.Unlock()
	return domain.ContinuitySample{At: time.Now(), Power: s.power, Available: s.available}
}
func (s *continuitySampler) wake() {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now()
	s.power = domain.PowerWitness{Sleep: now.Add(-time.Second), Wake: now}
}

func TestObservationGateRefreshesIntakeAndGuardedReadsAfterWake(t *testing.T) {
	sampler := &continuitySampler{available: true}
	gate, err := app.NewObservationGate(context.Background(), sampler, strings.Repeat("a", 32))
	if err != nil {
		t.Fatal(err)
	}
	defer gate.Close()
	stamp, err := gate.Stamp(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	called := false
	if err = stamp.WithCurrent(context.Background(), func() error { called = true; return nil }); err != nil || !called {
		t.Fatalf("healthy stamp refused: %v", err)
	}
	samplerRevision := gate.Snapshot().Revision
	sampler.wake() // No poll: the guarded read itself must discover this wake.
	called = false
	if err = stamp.WithCurrent(context.Background(), func() error { called = true; return nil }); !errors.Is(err, app.ErrObservationUnknown) || called {
		t.Fatalf("pre-wake projection remained current: %v", err)
	}
	if stamp.Context().Err() == nil || gate.Snapshot().Revision <= samplerRevision {
		t.Fatal("wake failed to revoke original intake scope")
	}
	fresh, err := gate.Stamp(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if err = fresh.WithCurrent(context.Background(), func() error { return nil }); err != nil {
		t.Fatalf("new intake could not reconcile after wake: %v", err)
	}
}

func TestObservationGateRejectsDelayedAndForeignIntakeWithoutRevisionConflation(t *testing.T) {
	sampler := &continuitySampler{available: true}
	gate, err := app.NewObservationGate(context.Background(), sampler, strings.Repeat("a", 32))
	if err != nil {
		t.Fatal(err)
	}
	defer gate.Close()
	old, err := gate.Stamp(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	gate.InvalidateGap()
	current, err := gate.Stamp(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if gate.Snapshot().Revision <= 1 {
		t.Fatal("missing global continuity advancement")
	}
	id := domain.LaunchIdentity{Epoch: strings.Repeat("a", 32), Run: strings.Repeat("b", 32), Generation: strings.Repeat("c", 32)}
	binding := domain.FreshnessBinding{Identity: id, Worktree: domain.WorktreeID("w_" + strings.Repeat("d", 64)), Profile: "codex"}
	freshness, err := domain.NewFreshness(binding)
	if err != nil {
		t.Fatal(err)
	}
	// Native validation is a test fixture only. Capture the per-run revision
	// independently; never use global gate revision as Freshness.Revision.
	evidence := domain.CausalFreshnessEvidence{Binding: binding, Revision: freshness.Snapshot().Revision, NativeSession: "verified-fixture", NativeEvent: "new-event"}
	if evidence.Revision != 1 {
		t.Fatal("new binding did not start at its own revision")
	}
	if err = old.WithCurrent(context.Background(), func() error { return freshness.Reconcile(evidence) }); !errors.Is(err, app.ErrObservationUnknown) || freshness.Snapshot().Known {
		t.Fatalf("old intake committed after gap: %v", err)
	}
	if err = current.WithCurrent(context.Background(), func() error { return freshness.Reconcile(evidence) }); err != nil || !freshness.Snapshot().Known {
		t.Fatalf("orthogonal revision spaces blocked new causal evidence: %v", err)
	}
	if err = freshness.Invalidate(domain.FreshnessProcessExit); err != nil {
		t.Fatal(err)
	}
	evidence.Revision = freshness.Snapshot().Revision
	if err = current.WithCurrent(context.Background(), func() error { return freshness.Reconcile(evidence) }); !errors.Is(err, domain.ErrChanged) {
		t.Fatal("gate resurrected permanently exited binding")
	}
	var zero app.ObservationStamp
	if zero.Context().Err() == nil || !errors.Is(zero.WithCurrent(context.Background(), func() error { t.Fatal("zero stamp executed"); return nil }), app.ErrObservationUnknown) {
		t.Fatal("zero/forged stamp was current")
	}
	other, err := app.NewObservationGate(context.Background(), sampler, strings.Repeat("e", 32))
	if err != nil {
		t.Fatal(err)
	}
	defer other.Close()
	if _, err = other.Stamp(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err = other.WithCurrent(context.Background(), current, func() error { t.Fatal("foreign gate executed"); return nil }); !errors.Is(err, app.ErrObservationUnknown) {
		t.Fatal("foreign gate accepted original stamp")
	}
}

type hangingContinuitySampler struct {
	started chan struct{}
	release chan struct{}
	calls   atomic.Int32
}

func (s *hangingContinuitySampler) Sample(context.Context) domain.ContinuitySample {
	if s.calls.Add(1) == 1 {
		close(s.started)
		<-s.release // Deliberately models an OS read ignoring context cancellation.
	}
	return domain.ContinuitySample{At: time.Now(), Available: true}
}

func TestObservationGateBoundsHungSamplerAndDiscardsLateResult(t *testing.T) {
	sampler := &hangingContinuitySampler{started: make(chan struct{}), release: make(chan struct{})}
	gate, err := app.NewObservationGate(context.Background(), sampler, strings.Repeat("a", 32))
	if err != nil {
		t.Fatal(err)
	}
	defer gate.Close()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { _, e := gate.Stamp(ctx); done <- e }()
	<-sampler.started
	cancel()
	select {
	case err = <-done:
		if !errors.Is(err, app.ErrObservationUnknown) || gate.Snapshot().Available {
			t.Fatalf("canceled refresh remained authoritative: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("sampler held gate memory lock indefinitely")
	}
	// More callers cannot fan a hung OS read out into unbounded goroutines.
	for range 3 {
		ctx, cancel = context.WithTimeout(context.Background(), time.Millisecond)
		_, err = gate.Stamp(ctx)
		cancel()
		if !errors.Is(err, app.ErrObservationUnknown) || sampler.calls.Load() != 1 {
			t.Fatalf("hung sampler multiplied or accepted: %v %d", err, sampler.calls.Load())
		}
	}
	closed := make(chan struct{})
	go func() { gate.Close(); close(closed) }()
	select {
	case <-closed:
	case <-time.After(time.Second):
		t.Fatal("close waited for uninterruptible OS read")
	}
	close(sampler.release)
	if _, err = gate.Stamp(context.Background()); !errors.Is(err, app.ErrObservationUnknown) || gate.Snapshot().Available {
		t.Fatalf("late result reopened a closed gate: %v", err)
	}
}

func TestObservationGateConcurrentRefreshIsOrderedAndUnavailableCannotCommit(t *testing.T) {
	sampler := &continuitySampler{available: true}
	gate, err := app.NewObservationGate(context.Background(), sampler, strings.Repeat("a", 32))
	if err != nil {
		t.Fatal(err)
	}
	defer gate.Close()
	var group sync.WaitGroup
	for range 16 {
		group.Go(func() {
			if _, e := gate.Stamp(context.Background()); e != nil {
				t.Errorf("ordered healthy read failed: %v", e)
			}
		})
	}
	group.Wait()
	if got := gate.Snapshot(); !got.Available || got.Revision != 1 {
		t.Fatalf("concurrent samples invented clock reversal: %+v", got)
	}
	stamp, err := gate.Stamp(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	sampler.mu.Lock()
	sampler.available = false
	sampler.mu.Unlock()
	if err = stamp.WithCurrent(context.Background(), func() error { t.Fatal("unsupported native read committed"); return nil }); !errors.Is(err, app.ErrObservationUnknown) || stamp.Context().Err() == nil {
		t.Fatalf("unavailable power evidence stayed current: %v", err)
	}
}

func TestObservationGateDefaultDeadlineAndInflightValidationCannotCommit(t *testing.T) {
	sampler := &hangingContinuitySampler{started: make(chan struct{}), release: make(chan struct{})}
	gate, err := app.NewObservationGate(context.Background(), sampler, strings.Repeat("a", 32))
	if err != nil {
		t.Fatal(err)
	}
	defer gate.Close()
	defer close(sampler.release)
	done := make(chan error, 1)
	go func() { _, e := gate.Stamp(context.Background()); done <- e }()
	select {
	case err = <-done:
		if !errors.Is(err, app.ErrObservationUnknown) || gate.Snapshot().Available {
			t.Fatalf("unbounded caller got current evidence from hung OS read: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("refresh did not impose the fixed optional budget")
	}

	healthy, err := app.NewObservationGate(context.Background(), &continuitySampler{available: true}, strings.Repeat("b", 32))
	if err != nil {
		t.Fatal(err)
	}
	defer healthy.Close()
	stamp, err := healthy.Stamp(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	validating, release := make(chan struct{}), make(chan struct{})
	var committed atomic.Bool
	go func() {
		close(validating)
		<-release // Native validation can ignore cancellation; commit still cannot.
		done <- stamp.WithCurrent(context.Background(), func() error { committed.Store(true); return nil })
	}()
	<-validating
	healthy.InvalidateGap()
	if _, err = healthy.Stamp(context.Background()); err != nil {
		t.Fatal(err)
	}
	close(release)
	if err = <-done; !errors.Is(err, app.ErrObservationUnknown) || committed.Load() {
		t.Fatalf("delayed validation acquired a new dequeue stamp: %v", err)
	}
}
