package runtime

import "context"

// SelectedObservationClient delivers selected untrusted metadata without starting a daemon.
// Acknowledgment is queue admission only, not native provenance, consumption or approval.
// This capability is separate from legacy Daemon so old clients do not silently claim it.
type SelectedObservationClient interface {
	ObserveSelected(context.Context, string, SelectedObservation) error
}
