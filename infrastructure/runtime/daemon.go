package runtime

import (
	"sync/atomic"
	"time"

	app "github.com/duck8823/traceary/application/runtime"
)

// Fixed R0 transport admission limits (bytes exclude the four-byte prefix).
const (
	RuntimeProtocolVersion = 1
	MaxRuntimeFrameBytes   = 65536
	MaxRuntimeConnections  = 32
	MaxObservationFrames   = 256
	MaxObservationBytes    = 8 * 1024 * 1024
	OptionalRuntimeBudget  = 250 * time.Millisecond
	MaxRuntimeIOTimeout    = time.Second
)

// DaemonOptions can tighten, never expand, the fixed admission limits.
type DaemonOptions struct {
	ManagementConnections, ObservationConnections, ObservationFrames, ObservationBytes int
	IOTimeout                                                                          time.Duration
}

// LocalDaemon is a foreground transport owner and a bounded, non-starting client.
// Its epoch/capabilities describe this transport, never current agent activity.
type LocalDaemon struct {
	receiver app.ObservationReceiver
	options  DaemonOptions
	serving  atomic.Bool
}

// NewLocalDaemon creates transport dependencies without opening files or core data.
func NewLocalDaemon(receiver app.ObservationReceiver, o DaemonOptions) (*LocalDaemon, error) {
	for _, v := range []struct {
		value *int
		max   int
	}{{&o.ManagementConnections, MaxRuntimeConnections}, {&o.ObservationConnections, MaxRuntimeConnections}, {&o.ObservationFrames, MaxObservationFrames}, {&o.ObservationBytes, MaxObservationBytes}} {
		if *v.value == 0 {
			*v.value = v.max
		}
		if *v.value < 1 || *v.value > v.max {
			return nil, app.ErrRuntimeProtocol
		}
	}
	if o.IOTimeout == 0 {
		o.IOTimeout = MaxRuntimeIOTimeout
	}
	if o.IOTimeout < time.Millisecond || o.IOTimeout > MaxRuntimeIOTimeout {
		return nil, app.ErrRuntimeProtocol
	}
	return &LocalDaemon{receiver: receiver, options: o}, nil
}
