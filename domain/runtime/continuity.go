package runtime

import (
	"math"
	"time"
)

// PowerWitness is a native adapter's last sleep/wake pair, not an agent event.
// Both zero means no observed cycle. It never proves that any process exited.
type PowerWitness struct {
	Sleep, Wake time.Time
}

// ContinuitySample comes from a trusted local watcher, never hook metadata.
// Available describes the native read, not agent activity or native causality.
type ContinuitySample struct {
	At        time.Time
	Power     PowerWitness
	Available bool
}

// ContinuityState fences observation intake across watcher discontinuities.
// Available permits taking a NEW intake stamp; it never restores Freshness.Known.
type ContinuityState struct {
	Revision  uint64
	Available bool
	Reason    FreshnessReason
	Exhausted bool
}

// ObservationContinuity owns daemon-wide continuity, not a managed run's
// interaction knowledge. Its caller serializes sampling and intake stamping.
type ObservationContinuity struct {
	state    ContinuityState
	maxGap   time.Duration
	last     ContinuitySample
	observed bool
}

// NewObservationContinuity starts closed. Composition supplies a conservative
// watcher gap budget; this is NOT a timeout on agent events or hook silence.
func NewObservationContinuity(maxGap time.Duration) (*ObservationContinuity, error) {
	if maxGap <= 0 {
		return nil, ErrInvalid
	}
	return &ObservationContinuity{state: ContinuityState{Revision: 1}, maxGap: maxGap}, nil
}

// Snapshot is non-consuming and carries no native session/input authority.
func (c *ObservationContinuity) Snapshot() ContinuityState { return c.state }

// Observe invalidates old intake stamps on native power changes, watcher gaps,
// unavailable evidence or clock reversal. A healthy sample can immediately open
// a NEW fence; only new independently verified native evidence may restore a
// run's interaction knowledge. Invalid samples are represented by closed state.
func (c *ObservationContinuity) Observe(sample ContinuitySample) error {
	if c.state.Exhausted {
		return ErrLimit
	}
	available, reason := sample.continuity()
	if !available {
		if c.state.Available {
			if err := c.advance(); err != nil {
				return err
			}
		}
		c.state.Available = false
		// Repeated unavailable samples do not overwrite the initial loss reason.
		if c.state.Reason == "" || !c.observed || c.last.Available {
			c.state.Reason = reason
		}
		c.last = ContinuitySample{}
		c.observed = true
		return nil
	}
	if c.observed {
		if !c.state.Available {
			if err := c.advance(); err != nil {
				return err
			}
		} else if reason = c.discontinuity(sample); reason != "" {
			if err := c.advance(); err != nil {
				return err
			}
			c.state.Reason = reason
		}
	}
	c.state.Available = true
	c.last = sample
	c.observed = true
	return nil
}

func (c *ObservationContinuity) advance() error {
	if c.state.Revision == math.MaxUint64 {
		c.state.Available = false
		c.state.Exhausted = true
		return ErrLimit
	}
	c.state.Revision++
	return nil
}

func (c *ObservationContinuity) discontinuity(next ContinuitySample) FreshnessReason {
	if !next.Power.Sleep.Round(0).Equal(c.last.Power.Sleep.Round(0)) || !next.Power.Wake.Round(0).Equal(c.last.Power.Wake.Round(0)) {
		return FreshnessSleepWake
	}
	// Sub retains Go's monotonic clock where available. Stripping monotonic
	// readings separately detects wall-clock jumps that Sub would otherwise hide.
	elapsed := next.At.Sub(c.last.At)
	wallElapsed := next.At.Round(0).Sub(c.last.At.Round(0))
	if elapsed < 0 || elapsed >= c.maxGap || wallElapsed < 0 || wallElapsed >= c.maxGap {
		return FreshnessCommunicationGap
	}
	return ""
}

func (s ContinuitySample) continuity() (bool, FreshnessReason) {
	if !s.Available || s.At.IsZero() {
		return false, FreshnessCommunicationGap
	}
	sleep, wake := s.Power.Sleep.Round(0), s.Power.Wake.Round(0)
	if sleep.IsZero() && wake.IsZero() {
		return true, ""
	}
	// Compare native wall stamps with wall time, not a Go monotonic component.
	at := s.At.Round(0)
	start := time.Unix(0, 0)
	for _, stamp := range []time.Time{sleep, wake} {
		if !stamp.IsZero() && (stamp.Before(start) || stamp.After(at)) {
			return false, FreshnessCommunicationGap
		}
	}
	if sleep.IsZero() {
		return false, FreshnessCommunicationGap
	}
	if wake.IsZero() || sleep.After(wake) {
		return false, FreshnessSleepWake
	}
	return true, ""
}
