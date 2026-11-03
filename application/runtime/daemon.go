package runtime

import (
	"context"
	"errors"
)

// Transport failures never include raw frames, credentials or native payloads.
var (
	ErrRuntimeRejected    = errors.New("local runtime operation rejected")
	ErrDaemonUnavailable  = errors.New("local daemon unavailable")
	ErrUnsafeRuntime      = errors.New("unsafe local runtime endpoint")
	ErrRuntimeProtocol    = errors.New("invalid local runtime protocol")
	ErrRuntimeUnsupported = errors.New("unsupported local runtime protocol or operation")
	ErrRuntimeBusy        = errors.New("local runtime capacity exceeded")
)

// DaemonStatus reports transport readiness only, never agent liveness or permission.
type DaemonStatus struct {
	ProtocolVersion     int
	Epoch               string
	State               string
	Capabilities        []string
	ObservationDelivery string
}

// ObservationMetadata is derived untrusted metadata, not a native binding or authority.
// Its receiver must independently validate host/version/generation/causality.
type ObservationMetadata struct{ Host, HostVersion, RunID, Generation, Epoch, TargetID, EventID, NativeSessionID, NativeTurnID, Kind string }

// ObservationReceiver consumes optional metadata. It must honor cancellation and
// never infer input/approval authority from transport acceptance.
type ObservationReceiver interface {
	ConsumeObservation(context.Context, ObservationMetadata) error
}

// CausalObservationReceiver is explicitly trusted composition for live native
// projection adapters, separate from the legacy untrusted history receiver.
// Validate the complete descriptor/host/version/binding/native event causality
// independently, then use the ORIGINAL stamp to guard BOTH commit and current
// reads. Never replace it at dequeue or conflate gate and per-run revisions.
// Honor cancellation; intake acceptance grants no input/approval/resume rights.
type CausalObservationReceiver interface {
	ConsumeCausalObservation(context.Context, ObservationMetadata, ObservationStamp) error
}

// Daemon is the typed local transport port used by CLI composition.
// Serve is explicit and foreground; Status/Observe never start a daemon or touch core data.
type Daemon interface {
	Serve(context.Context, string) error
	Status(context.Context, string) (DaemonStatus, error)
	Observe(context.Context, string, ObservationMetadata) error
}
