package runtime

import (
	"context"
	"errors"
)

// Transport failures never include raw frames, credentials or native payloads.
var (
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

// Daemon is the typed local transport port used by CLI composition.
// Serve is explicit and foreground; Status/Observe never start a daemon or touch core data.
type Daemon interface {
	Serve(context.Context, string) error
	Status(context.Context, string) (DaemonStatus, error)
	Observe(context.Context, string, ObservationMetadata) error
}
