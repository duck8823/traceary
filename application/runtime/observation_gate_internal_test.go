package runtime

import (
	"context"
	"errors"
	"math"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	domain "github.com/duck8823/traceary/domain/runtime"
)

type lateContinuitySample struct {
	entered, release chan struct{}
}

type observationSamplerFunc func(context.Context) domain.ContinuitySample

func (f observationSamplerFunc) Sample(ctx context.Context) domain.ContinuitySample { return f(ctx) }

func TestObservationGateCounterOverflowCannotReuseScope(t *testing.T) {
	for _, counter := range []string{"request", "generation"} {
		t.Run(counter, func(t *testing.T) {
			gate, err := NewObservationGate(context.Background(), observationSamplerFunc(func(context.Context) domain.ContinuitySample {
				return domain.ContinuitySample{At: time.Now(), Available: true}
			}), strings.Repeat("a", 32))
			if err != nil {
				t.Fatal(err)
			}
			defer gate.Close()
			stamp, err := gate.Stamp(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			gate.mu.Lock()
			if counter == "request" {
				gate.request = math.MaxUint64
			} else {
				gate.generation = math.MaxUint64
			}
			gate.mu.Unlock()
			if counter == "generation" {
				gate.InvalidateGap()
			}
			if _, err = gate.Stamp(context.Background()); !errors.Is(err, ErrObservationUnknown) || stamp.Context().Err() == nil || gate.Snapshot().Available {
				t.Fatalf("exhausted %s counter retained authority: %v", counter, err)
			}
		})
	}
}

func TestObservationGateNewRequestCannotReuseAlreadyStartedRead(t *testing.T) {
	started, release := make(chan int32, 2), make(chan struct{})
	var count atomic.Int32
	gate, err := NewObservationGate(context.Background(), observationSamplerFunc(func(context.Context) domain.ContinuitySample {
		started <- count.Add(1)
		<-release
		return domain.ContinuitySample{At: time.Now(), Available: true}
	}), strings.Repeat("a", 32))
	if err != nil {
		t.Fatal(err)
	}
	defer gate.Close()
	defer close(release)
	done := make(chan error, 2)
	go func() { _, e := gate.Stamp(context.Background()); done <- e }()
	if read := <-started; read != 1 {
		t.Fatal("missing initial native read")
	}
	go func() { _, e := gate.Stamp(context.Background()); done <- e }()
	// Join actual request admission, not a sleep or a goroutine's pre-call signal.
	deadline := time.Now().Add(time.Second)
	for {
		gate.mu.Lock()
		admitted := gate.request >= 2
		gate.mu.Unlock()
		if admitted {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("second request did not enter refresh")
		}
		runtime.Gosched()
	}
	release <- struct{}{}
	if read := <-started; read != 2 {
		t.Fatal("later intake reused a pre-request native read")
	}
	release <- struct{}{}
	for range 2 {
		if err = <-done; err != nil {
			t.Fatal(err)
		}
	}
}

func (s *lateContinuitySample) Sample(context.Context) domain.ContinuitySample {
	close(s.entered)
	<-s.release
	return domain.ContinuitySample{At: time.Now(), Available: true}
}

func TestObservationGateLateSampleCannotReopenStillRunningGate(t *testing.T) {
	sampler := &lateContinuitySample{entered: make(chan struct{}), release: make(chan struct{})}
	gate, err := NewObservationGate(context.Background(), sampler, strings.Repeat("a", 32))
	if err != nil {
		t.Fatal(err)
	}
	defer gate.Close()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { _, e := gate.Stamp(ctx); done <- e }()
	<-sampler.entered
	// White-box join is necessary to observe actual late sample application,
	// not merely the fake's return or a wall-clock sleep guessed to be enough.
	gate.mu.Lock()
	flightDone := gate.flight.done
	gate.mu.Unlock()
	cancel()
	if err = <-done; !errors.Is(err, ErrObservationUnknown) {
		t.Fatal(err)
	}
	closed := gate.Snapshot()
	close(sampler.release)
	<-flightDone
	if got := gate.Snapshot(); got.Available || got != closed {
		t.Fatalf("late native read reopened pre-gap authority: %+v -> %+v", closed, got)
	}
}
