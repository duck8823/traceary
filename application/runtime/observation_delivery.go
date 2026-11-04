package runtime

import (
	"context"
	"errors"
	domain "github.com/duck8823/traceary/domain/runtime"
	"time"
)

// ObservationDeliveryBudget is one total optional budget, never a per-phase allowance.
const ObservationDeliveryBudget = 250 * time.Millisecond

// ObservationOutcome is telemetry delivery only, never native binding or effect authority.
type ObservationOutcome string

// Closed optional outcomes contain no submitted metadata or dependency error text.
const (
	ObservationQueued      ObservationOutcome = "queued"
	ObservationUnavailable ObservationOutcome = "unavailable"
	ObservationUnsafe      ObservationOutcome = "unsafe"
	ObservationUnsupported ObservationOutcome = "unsupported"
	ObservationBusy        ObservationOutcome = "busy"
	ObservationRejected    ObservationOutcome = "rejected"
	ObservationChanged     ObservationOutcome = "changed"
	ObservationNotFound    ObservationOutcome = "not_found"
	ObservationInvalid     ObservationOutcome = "invalid"
	ObservationTimeout     ObservationOutcome = "timeout"
)

// One process-wide retained derivation, shared across separately constructed services.
var observationDeliverySlot = make(chan struct{}, 1)

// ObservationDelivery contains the existing typed client, not a new worker queue or spool.
type ObservationDelivery struct{ client SelectedObservationClient }

// NewObservationDelivery creates no worker, file, daemon or native process.
func NewObservationDelivery(client SelectedObservationClient) *ObservationDelivery {
	return &ObservationDelivery{client: client}
}

func observationOutcome(err error) ObservationOutcome {
	switch {
	case err == nil:
		return ObservationQueued
	case errors.Is(err, context.DeadlineExceeded):
		return ObservationTimeout
	case errors.Is(err, context.Canceled):
		return ObservationUnavailable
	case errors.Is(err, ErrRuntimeUnsupported) || errors.Is(err, domain.ErrUnsupported):
		return ObservationUnsupported
	case errors.Is(err, ErrUnsafeRuntime) || errors.Is(err, domain.ErrUnsafe):
		return ObservationUnsafe
	case errors.Is(err, ErrRuntimeBusy) || errors.Is(err, domain.ErrLimit):
		return ObservationBusy
	case errors.Is(err, ErrRuntimeRejected):
		return ObservationRejected
	case errors.Is(err, domain.ErrChanged):
		return ObservationChanged
	case errors.Is(err, domain.ErrNotFound):
		return ObservationNotFound
	case errors.Is(err, ErrRuntimeProtocol) || errors.Is(err, domain.ErrInvalid):
		return ObservationInvalid
	default:
		return ObservationUnavailable
	}
}

// Deliver contains optional derivation and transport under one caller deadline.
// A dependency ignoring cancellation retains the process slot until actual return;
// late derivation never sends, panic never escapes, and no work is retried/replayed.
// Derive must borrow immutable input and must not write primary hook output/state.
func (d *ObservationDelivery) Deliver(parent context.Context, root string, derive func(context.Context) (SelectedObservation, error)) ObservationOutcome {
	ctx, cancel := context.WithTimeout(parent, ObservationDeliveryBudget)
	defer cancel()
	if err := ctx.Err(); err != nil {
		return observationOutcome(err)
	}
	if d == nil || d.client == nil {
		return ObservationUnsupported
	}
	if derive == nil {
		return ObservationInvalid
	}
	select {
	case observationDeliverySlot <- struct{}{}:
	default:
		return ObservationBusy
	}
	result := make(chan ObservationOutcome, 1)
	go func() {
		outcome := ObservationUnavailable
		defer func() {
			if recover() != nil {
				outcome = ObservationUnavailable
			}
			<-observationDeliverySlot
			result <- outcome
		}()
		value, err := derive(ctx)
		if err == nil {
			err = validateSelectedObservation(value)
		}
		if err != nil {
			outcome = observationOutcome(err)
			return
		}
		if err = ctx.Err(); err != nil {
			outcome = observationOutcome(err)
			return
		}
		err = d.client.ObserveSelected(ctx, root, value)
		if deadlineErr := ctx.Err(); deadlineErr != nil {
			err = deadlineErr
		}
		outcome = observationOutcome(err)
	}()
	select {
	case outcome := <-result:
		return outcome
	case <-ctx.Done():
		return observationOutcome(ctx.Err())
	}
}
