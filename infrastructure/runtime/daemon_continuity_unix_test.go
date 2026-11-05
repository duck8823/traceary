//go:build darwin || linux

package runtime

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

// Trusted sampler/consumer fixtures test real daemon transport and intake
// fencing, NOT native host binding, process liveness, or physical sleep cycles.
type daemonPowerFixture struct {
	mu        sync.Mutex
	power     domain.PowerWitness
	available bool
}

func (p *daemonPowerFixture) Sample(context.Context) domain.ContinuitySample {
	p.mu.Lock()
	defer p.mu.Unlock()
	return domain.ContinuitySample{At: time.Now(), Power: p.power, Available: p.available}
}
func (p *daemonPowerFixture) wake() {
	p.mu.Lock()
	defer p.mu.Unlock()
	now := time.Now()
	p.power = domain.PowerWitness{Sleep: now.Add(-time.Second), Wake: now}
}

type causalReceiverFunc func(context.Context, app.ObservationMetadata, app.ObservationStamp) error

func (f causalReceiverFunc) ConsumeCausalObservation(ctx context.Context, m app.ObservationMetadata, s app.ObservationStamp) error {
	return f(ctx, m, s)
}

type historyCount struct{ calls atomic.Int32 }

func (h *historyCount) ConsumeObservation(context.Context, app.ObservationMetadata) error {
	h.calls.Add(1)
	return nil
}

func serveContinuityFixture(t *testing.T, d *LocalDaemon, root string) string {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { defer close(done); done <- d.Serve(ctx, root) }()
	t.Cleanup(func() {
		cancel()
		select {
		case err := <-done:
			if err != nil {
				t.Errorf("serve fixture: %v", err)
			}
		case <-time.After(3 * time.Second):
			t.Error("continuity daemon did not stop")
		}
	})
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		status, err := d.Status(context.Background(), root)
		if err == nil {
			return status.Epoch
		}
		select {
		case err = <-done:
			t.Fatalf("continuity daemon startup: %v", err)
		default:
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("continuity daemon readiness timeout")
	return ""
}

func TestDaemonContinuityWatcherRevokesIdleScopeWithoutAnotherObservation(t *testing.T) {
	items := make(chan app.ObservationStamp, 1)
	d, err := NewLocalDaemon(nil, DaemonOptions{CausalReceiver: causalReceiverFunc(func(_ context.Context, _ app.ObservationMetadata, s app.ObservationStamp) error {
		items <- s
		return nil
	})})
	if err != nil {
		t.Fatal(err)
	}
	power := &daemonPowerFixture{available: true}
	d.sampler = power
	root := receiptRoot(t)
	epoch := serveContinuityFixture(t, d, root)
	if err = d.Observe(context.Background(), root, causalMetadata(epoch, "idle")); err != nil {
		t.Fatal(err)
	}
	var stamp app.ObservationStamp
	select {
	case stamp = <-items:
	case <-time.After(time.Second):
		t.Fatal("idle intake not consumed")
	}
	power.wake()
	select {
	case <-stamp.Context().Done():
	case <-time.After(3 * time.Second):
		t.Fatal("idle watcher did not revoke old native scope")
	}
}

type blockedDaemonSampler struct {
	entered, release chan struct{}
	calls            atomic.Int32
}

func (s *blockedDaemonSampler) Sample(context.Context) domain.ContinuitySample {
	if s.calls.Add(1) == 1 {
		close(s.entered)
	}
	<-s.release
	return domain.ContinuitySample{At: time.Now(), Available: true}
}

func TestDaemonHungNativeReadDoesNotBlockManagementShutdownOrRelease(t *testing.T) {
	d, err := NewLocalDaemon(nil, DaemonOptions{CausalReceiver: causalReceiverFunc(func(context.Context, app.ObservationMetadata, app.ObservationStamp) error { return nil })})
	if err != nil {
		t.Fatal(err)
	}
	sampler := &blockedDaemonSampler{entered: make(chan struct{}), release: make(chan struct{})}
	d.sampler = sampler
	defer close(sampler.release)
	root := receiptRoot(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- d.Serve(ctx, root) }()
	select {
	case <-sampler.entered:
	case err = <-done:
		t.Fatalf("daemon startup failed: %v", err)
	case <-time.After(3 * time.Second):
		t.Fatal("watcher did not begin native read")
	}
	status, err := d.Status(context.Background(), root)
	if err != nil || status.State != "listening" {
		t.Fatalf("hung sampler blocked management: %v", err)
	}
	cancel()
	select {
	case err = <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("daemon joined an uninterruptible native read")
	}
	if sampler.calls.Load() != 1 {
		t.Fatal("hung native read multiplied")
	}
	// Reacquire only this test's private daemon lease/root, not any process handle.
	next, err := NewLocalDaemon(nil, DaemonOptions{})
	if err != nil {
		t.Fatal(err)
	}
	prepared, err := prepareDaemon(context.Background(), next, root)
	if err != nil {
		t.Fatalf("hung read retained unrelated endpoint/lease: %v", err)
	}
	prepared.shutdown()
}
func causalMetadata(epoch, event string) app.ObservationMetadata {
	return app.ObservationMetadata{Host: "codex", HostVersion: "fixture", RunID: "run", Generation: "generation", Epoch: epoch, TargetID: "target", EventID: event, NativeSessionID: "fixture-session", Kind: "turn_boundary"}
}

func TestDaemonCausalIPCOriginalStampWakeAndNormalDisconnect(t *testing.T) {
	items := make(chan app.ObservationStamp, 8)
	history := &historyCount{}
	d, err := NewLocalDaemon(history, DaemonOptions{CausalReceiver: causalReceiverFunc(func(_ context.Context, _ app.ObservationMetadata, s app.ObservationStamp) error {
		items <- s
		return nil
	})})
	if err != nil {
		t.Fatal(err)
	}
	power := &daemonPowerFixture{available: true}
	d.sampler = power
	root := receiptRoot(t)
	epoch := serveContinuityFixture(t, d, root)
	readStamp := func(event string) app.ObservationStamp {
		t.Helper()
		if err := d.Observe(context.Background(), root, causalMetadata(epoch, event)); err != nil {
			t.Fatal(err)
		}
		select {
		case s := <-items:
			return s
		case <-time.After(time.Second):
			t.Fatal("causal consumer not called")
			return app.ObservationStamp{}
		}
	}
	old := readStamp("one")
	if err = old.WithCurrent(context.Background(), func() error { return nil }); err != nil {
		t.Fatal(err)
	}
	// Every SDK call closes its one-shot connection. This is not a native gap.
	_ = readStamp("two")
	if err = old.WithCurrent(context.Background(), func() error { return nil }); err != nil {
		t.Fatal("normal SDK disconnect revoked causal scope", err)
	}
	power.wake()
	if err = old.WithCurrent(context.Background(), func() error { t.Fatal("pre-wake knowledge read as current"); return nil }); !errors.Is(err, app.ErrObservationUnknown) {
		t.Fatal(err)
	}
	if old.Context().Err() == nil {
		t.Fatal("wake did not revoke in-flight intake")
	}
	fresh := readStamp("three")
	if err = fresh.WithCurrent(context.Background(), func() error { return nil }); err != nil {
		t.Fatal("fresh post-wake intake rejected", err)
	}
	if err = d.Observe(context.Background(), root, causalMetadata(strings.Repeat("e", 32), "foreign")); !errors.Is(err, domain.ErrChanged) {
		t.Fatalf("foreign epoch was not known rejection: %v", err)
	}
	if len(items) != 0 || history.calls.Load() != 0 {
		t.Fatal("foreign epoch or both-receiver fallback delivered metadata")
	}
}

func TestDaemonUnsupportedPowerPreservesHistoryButRefusesCausalFallback(t *testing.T) {
	for _, causal := range []bool{false, true} {
		t.Run(map[bool]string{false: "history", true: "causal"}[causal], func(t *testing.T) {
			history := &historyCount{}
			var causalCalls atomic.Int32
			opts := DaemonOptions{}
			if causal {
				opts.CausalReceiver = causalReceiverFunc(func(context.Context, app.ObservationMetadata, app.ObservationStamp) error {
					causalCalls.Add(1)
					return nil
				})
			}
			d, err := NewLocalDaemon(history, opts)
			if err != nil {
				t.Fatal(err)
			}
			d.sampler = &daemonPowerFixture{available: false}
			root := receiptRoot(t)
			epoch := serveContinuityFixture(t, d, root)
			err = d.Observe(context.Background(), root, causalMetadata(epoch, "unavailable"))
			if causal {
				if !errors.Is(err, app.ErrRuntimeBusy) || causalCalls.Load() != 0 || history.calls.Load() != 0 {
					t.Fatalf("unsupported causal path fell back: %v", err)
				}
			} else {
				if err != nil {
					t.Fatal(err)
				}
				deadline := time.Now().Add(time.Second)
				for history.calls.Load() == 0 && time.Now().Before(deadline) {
					time.Sleep(time.Millisecond)
				}
				if history.calls.Load() != 1 {
					t.Fatal("history delivery lost on unsupported power platform")
				}
			}
			status, err := d.Status(context.Background(), root)
			if err != nil {
				t.Fatal(err)
			}
			want := "configured"
			if causal {
				want = "degraded"
			}
			if status.ObservationDelivery != want {
				t.Fatalf("wrong delivery health: %s", status.ObservationDelivery)
			}
		})
	}
}

func TestDaemonCausalQueueLossRevokesInflightAndQueuedWithoutRestamping(t *testing.T) {
	for _, mode := range []string{"overflow", "consumer-error"} {
		t.Run(mode, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			entered := make(chan app.ObservationStamp, 1)
			release := make(chan struct{})
			var releaseOnce sync.Once
			releaseValidation := func() { releaseOnce.Do(func() { close(release) }) }
			defer releaseValidation()
			finished := make(chan struct{}, 1)
			var staleCalls, commits atomic.Int32
			receiver := causalReceiverFunc(func(_ context.Context, m app.ObservationMetadata, stamp app.ObservationStamp) error {
				switch m.EventID {
				case "one":
					entered <- stamp
					<-release
					if mode == "consumer-error" {
						return errors.New("fixture consumer failed")
					}
					return stamp.WithCurrent(context.Background(), func() error { commits.Add(1); return nil })
				case "two":
					staleCalls.Add(1)
				case "fresh":
					finished <- struct{}{}
				}
				return nil
			})
			d, err := NewLocalDaemon(nil, DaemonOptions{ObservationFrames: 1, CausalReceiver: receiver})
			if err != nil {
				t.Fatal(err)
			}
			epoch := strings.Repeat("a", 32)
			gate, err := app.NewObservationGate(ctx, &daemonPowerFixture{available: true}, epoch)
			if err != nil {
				t.Fatal(err)
			}
			defer gate.Close()
			s := &daemonServer{daemon: d, ctx: ctx, cancel: cancel, epoch: epoch, gate: gate, notify: make(chan struct{}, 1), workerDone: make(chan struct{})}
			go s.consume()
			t.Cleanup(func() {
				cancel()
				select {
				case <-s.workerDone:
				case <-time.After(time.Second):
					t.Error("causal worker did not stop")
				}
			})
			if !s.enqueue(causalMetadata(epoch, "one"), 1) {
				t.Fatal("initial causal intake refused")
			}
			first := <-entered
			if !s.enqueue(causalMetadata(epoch, "two"), 1) {
				t.Fatal("second causal intake refused")
			}
			s.queueMu.Lock()
			queued := s.queue[0].stamp
			s.queueMu.Unlock()
			if mode == "overflow" && s.enqueue(causalMetadata(epoch, "overflow"), 1) {
				t.Fatal("bounded queue overflow accepted")
			}
			releaseValidation()
			select {
			case <-first.Context().Done():
			case <-time.After(time.Second):
				t.Fatal("loss did not revoke in-flight intake")
			}
			if queued.Context().Err() == nil {
				t.Fatal("queued old revision survived loss")
			}
			deadline := time.Now().Add(time.Second)
			for {
				s.queueMu.Lock()
				empty := len(s.queue) == 0
				s.queueMu.Unlock()
				if empty {
					break
				}
				if time.Now().After(deadline) {
					t.Fatal("old queue did not drain")
				}
				time.Sleep(time.Millisecond)
			}
			if !s.enqueue(causalMetadata(epoch, "fresh"), 1) {
				t.Fatal("new causal intake refused after old queue drained")
			}
			select {
			case <-finished:
			case <-time.After(time.Second):
				t.Fatal("new revision not consumable after loss")
			}
			if staleCalls.Load() != 0 || commits.Load() != 0 {
				t.Fatal("stale queued/in-flight evidence was committed")
			}
		})
	}
}
