package runtime

import (
	"context"
	"errors"
	domain "github.com/duck8823/traceary/domain/runtime"
)

// ErrControlUnknown means input/control may have committed; inspect, never replay automatically.
var ErrControlUnknown = errors.New("runtime control outcome uncertain; inspect the original receipt without replay")

// ErrControlCommitUncertain is the journal port's ambiguous durability contract.
// Infrastructure maps its native post-rename uncertainty here, without leaking adapter types.
var ErrControlCommitUncertain = errors.New("runtime control intent commit uncertain")

// ControlRequest carries explicit operator intent. Data is ephemeral untrusted input,
// never persisted/logged, and is copied before a detached operation can outlive its caller.
type ControlRequest struct {
	Selection OutputSelection
	Intent    domain.ControlIntent
	Data      []byte
}

// ControlResult exposes only safe correlation and delivery metadata.
type ControlResult struct {
	Selection OutputSelection
	Receipt   domain.ControlSnapshot
}

// ControlEffect is kernel evidence, not agent understanding or process exit.
type ControlEffect struct {
	Outcome      domain.ControlOutcome
	WrittenBytes int
}

// ControlledChild is an optional capability on an actual owned child, not a PID.
// ValidateControl checks current native/target/profile/sequence evidence before a
// durable reservation. ApplyControl rechecks admission under the sole control/reap barrier.
type ControlledChild interface {
	ValidateControl(context.Context, domain.ControlIntent) error
	ApplyControl(context.Context, domain.ControlIntent, []byte) (ControlEffect, error)
}

// ControlJournal shares private runtime metadata durability/aggregate bounds.
// ReserveControl errors MUST identify any possible commit with ErrControlCommitUncertain;
// other errors guarantee no intent committed. Neither method may persist raw input/hash.
type ControlJournal interface {
	ReserveControl(context.Context, OutputSelection, domain.ControlReceipt) error
	SaveControl(context.Context, OutputSelection, domain.ControlReceipt) error
}

// ControlClient exposes only explicit typed controls and receipt inspection.
type ControlClient interface {
	Control(context.Context, string, ControlRequest) (ControlResult, error)
	Receipt(context.Context, string, OutputSelection, string) (ControlResult, error)
}
