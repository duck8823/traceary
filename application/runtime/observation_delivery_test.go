package runtime_test

import (
	"context"
	app "github.com/duck8823/traceary/application/runtime"
	domain "github.com/duck8823/traceary/domain/runtime"
	"sync/atomic"
	"testing"
	"time"
)

type observationDeliveryClient struct {
	calls atomic.Int32
	run   func(context.Context) error
}

func (c *observationDeliveryClient) ObserveSelected(ctx context.Context, _ string, _ app.SelectedObservation) error {
	c.calls.Add(1)
	if c.run != nil {
		return c.run(ctx)
	}
	return nil
}
func deliverySample(t *testing.T) app.SelectedObservation {
	t.Helper()
	owner, req, factory, _ := runtimeFixture(t, false)
	snapshot, err := owner.Launch(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	sample := selectedObservation(t, snapshot, "spool:delivery")
	factory.child.finish()
	if err = owner.AwaitIdle(context.Background()); err != nil {
		t.Fatal(err)
	}
	return sample
}
func TestObservationDeliveryRetainsOneGlobalSlotUntilIgnoredDerivationActuallyExits(t *testing.T) {
	sample := deliverySample(t)
	client := &observationDeliveryClient{}
	delivery := app.NewObservationDelivery(client)
	entered := make(chan struct{})
	release := make(chan struct{})
	finished := make(chan struct{})
	done := make(chan app.ObservationOutcome, 1)
	go func() {
		done <- delivery.Deliver(context.Background(), "/root", func(context.Context) (app.SelectedObservation, error) {
			close(entered)
			<-release
			close(finished)
			return sample, nil
		})
	}()
	<-entered
	if got := <-done; got != app.ObservationTimeout {
		close(release)
		t.Fatalf("ignored derivation outcome=%s", got)
	}
	second := app.NewObservationDelivery(client)
	for range 20 {
		if got := second.Deliver(context.Background(), "/root", func(context.Context) (app.SelectedObservation, error) {
			t.Error("replacement derivation spawned")
			return sample, nil
		}); got != app.ObservationBusy {
			close(release)
			t.Fatalf("retained slot outcome=%s", got)
		}
	}
	close(release)
	<-finished
	deadline := time.Now().Add(time.Second)
	for {
		got := second.Deliver(context.Background(), "/root", func(context.Context) (app.SelectedObservation, error) { return sample, nil })
		if got == app.ObservationQueued {
			break
		}
		if got != app.ObservationBusy || time.Now().After(deadline) {
			t.Fatal("derivation slot not released", got)
		}
		time.Sleep(time.Millisecond)
	}
	if client.calls.Load() != 1 {
		t.Fatal("late derivation sent metadata after deadline", client.calls.Load())
	}
}
func TestObservationDeliveryClosedOutcomesPanicAndNoRetry(t *testing.T) {
	sample := deliverySample(t)
	for _, tt := range []struct {
		name string
		err  error
		want app.ObservationOutcome
	}{
		{"queued", nil, app.ObservationQueued}, {"unsupported", app.ErrRuntimeUnsupported, app.ObservationUnsupported}, {"unavailable", app.ErrDaemonUnavailable, app.ObservationUnavailable}, {"unsafe", app.ErrUnsafeRuntime, app.ObservationUnsafe}, {"invalid", app.ErrRuntimeProtocol, app.ObservationInvalid}, {"changed", domain.ErrChanged, app.ObservationChanged}, {"not_found", domain.ErrNotFound, app.ObservationNotFound}, {"rejected", app.ErrRuntimeRejected, app.ObservationRejected}, {"busy", app.ErrRuntimeBusy, app.ObservationBusy},
	} {
		t.Run(tt.name, func(t *testing.T) {
			client := &observationDeliveryClient{run: func(context.Context) error { return tt.err }}
			got := app.NewObservationDelivery(client).Deliver(context.Background(), "/root", func(context.Context) (app.SelectedObservation, error) { return sample, nil })
			if got != tt.want || client.calls.Load() != 1 {
				t.Fatal("optional outcome/retry changed", got, client.calls.Load())
			}
		})
	}
	client := &observationDeliveryClient{}
	delivery := app.NewObservationDelivery(client)
	if got := delivery.Deliver(context.Background(), "/root", func(context.Context) (app.SelectedObservation, error) { panic("synthetic derivation failure") }); got != app.ObservationUnavailable {
		t.Fatal("panic escaped optional boundary", got)
	}
	if got := delivery.Deliver(context.Background(), "/root", func(context.Context) (app.SelectedObservation, error) { return sample, domain.ErrInvalid }); got != app.ObservationInvalid {
		t.Fatal("derivation error", got)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if got := delivery.Deliver(ctx, "/root", func(context.Context) (app.SelectedObservation, error) {
		t.Error("canceled derivation entered")
		return sample, nil
	}); got != app.ObservationUnavailable {
		t.Fatal("cancellation outcome", got)
	}
	if client.calls.Load() != 0 {
		t.Fatal("failed derivation sent metadata")
	}
}

func TestObservationDeliveryOneBudgetAndIgnoredClientRetainSlot(t *testing.T) {
	sample := deliverySample(t)
	entered := make(chan struct{})
	release := make(chan struct{})
	finished := make(chan struct{})
	client := &observationDeliveryClient{run: func(context.Context) error { close(entered); <-release; close(finished); return nil }}
	delivery := app.NewObservationDelivery(client)
	done := make(chan app.ObservationOutcome, 1)
	go func() {
		done <- delivery.Deliver(context.Background(), "/root", func(context.Context) (app.SelectedObservation, error) { return sample, nil })
	}()
	<-entered
	if got := <-done; got != app.ObservationTimeout {
		close(release)
		t.Fatal("ignored client escaped deadline", got)
	}
	if got := app.NewObservationDelivery(client).Deliver(context.Background(), "/root", func(context.Context) (app.SelectedObservation, error) {
		t.Error("replacement callback entered")
		return sample, nil
	}); got != app.ObservationBusy {
		close(release)
		t.Fatal("ignored client released slot early", got)
	}
	close(release)
	<-finished
	probe := &observationDeliveryClient{}
	deadline := time.Now().Add(time.Second)
	for {
		got := app.NewObservationDelivery(probe).Deliver(context.Background(), "/root", func(context.Context) (app.SelectedObservation, error) { return sample, nil })
		if got == app.ObservationQueued {
			break
		}
		if got != app.ObservationBusy || time.Now().After(deadline) {
			t.Fatal("actual client exit did not release slot", got)
		}
		time.Sleep(time.Millisecond)
	}
	client = &observationDeliveryClient{run: func(context.Context) error { time.Sleep(180 * time.Millisecond); return nil }}
	got := app.NewObservationDelivery(client).Deliver(context.Background(), "/root", func(context.Context) (app.SelectedObservation, error) {
		time.Sleep(130 * time.Millisecond)
		return sample, nil
	})
	if got != app.ObservationTimeout {
		t.Fatal("derivation and client received separate budgets", got)
	}
	deadline = time.Now().Add(time.Second)
	for {
		got = app.NewObservationDelivery(probe).Deliver(context.Background(), "/root", func(context.Context) (app.SelectedObservation, error) { return sample, nil })
		if got == app.ObservationQueued {
			break
		}
		if got != app.ObservationBusy || time.Now().After(deadline) {
			t.Fatal("late client slot retained", got)
		}
		time.Sleep(time.Millisecond)
	}
}
