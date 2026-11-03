package runtime_test

import (
	"errors"
	"testing"
	"time"

	domain "github.com/duck8823/traceary/domain/runtime"
)

func TestObservationContinuityNeverReusesPreGapFence(t *testing.T) {
	base := time.Date(2026, time.January, 2, 12, 0, 0, 0, time.UTC)
	for _, tc := range []struct {
		name   string
		change func(*domain.ContinuitySample)
		reason domain.FreshnessReason
	}{
		{"exact gap", func(s *domain.ContinuitySample) { s.At = base.Add(30 * time.Second) }, domain.FreshnessCommunicationGap},
		{"long gap", func(s *domain.ContinuitySample) { s.At = base.Add(time.Hour) }, domain.FreshnessCommunicationGap},
		{"clock reversal", func(s *domain.ContinuitySample) { s.At = base.Add(-time.Second) }, domain.FreshnessCommunicationGap},
		{"wake change", func(s *domain.ContinuitySample) {
			s.Power = domain.PowerWitness{Sleep: base.Add(-time.Minute), Wake: base}
		}, domain.FreshnessSleepWake},
	} {
		t.Run(tc.name, func(t *testing.T) {
			owner, err := domain.NewObservationContinuity(30 * time.Second)
			if err != nil {
				t.Fatal(err)
			}
			if initial := owner.Snapshot(); initial.Available || initial.Revision != 1 {
				t.Fatalf("fabricated startup continuity: %+v", initial)
			}
			if err = owner.Observe(domain.ContinuitySample{At: base, Available: true}); err != nil {
				t.Fatal(err)
			}
			old := owner.Snapshot()
			next := domain.ContinuitySample{At: base.Add(time.Second), Available: true}
			tc.change(&next)
			if err = owner.Observe(next); err != nil {
				t.Fatal(err)
			}
			got := owner.Snapshot()
			if !got.Available || got.Revision <= old.Revision || got.Reason != tc.reason {
				t.Fatalf("pre-gap fence remained current: old=%+v new=%+v", old, got)
			}
			if got != owner.Snapshot() {
				t.Fatal("read consumed continuity state")
			}
		})
	}
}

func TestObservationContinuityFailureAndRecoveryRemainDistinct(t *testing.T) {
	base := time.Date(2026, time.January, 2, 12, 0, 0, 0, time.UTC)
	for _, tc := range []struct {
		name   string
		sample domain.ContinuitySample
		reason domain.FreshnessReason
	}{
		{"unavailable", domain.ContinuitySample{At: base.Add(time.Second)}, domain.FreshnessCommunicationGap},
		{"missing clock", domain.ContinuitySample{Available: true}, domain.FreshnessCommunicationGap},
		{"future witness", domain.ContinuitySample{At: base.Add(time.Second), Available: true, Power: domain.PowerWitness{Sleep: base, Wake: base.Add(time.Hour)}}, domain.FreshnessCommunicationGap},
		{"wake without sleep", domain.ContinuitySample{At: base.Add(time.Second), Available: true, Power: domain.PowerWitness{Wake: base}}, domain.FreshnessCommunicationGap},
		{"negative timestamp", domain.ContinuitySample{At: base.Add(time.Second), Available: true, Power: domain.PowerWitness{Sleep: time.Unix(-2, 0), Wake: time.Unix(-1, 0)}}, domain.FreshnessCommunicationGap},
		{"sleep in progress", domain.ContinuitySample{At: base.Add(time.Second), Available: true, Power: domain.PowerWitness{Sleep: base}}, domain.FreshnessSleepWake},
		{"new sleep before wake", domain.ContinuitySample{At: base.Add(time.Second), Available: true, Power: domain.PowerWitness{Sleep: base, Wake: base.Add(-time.Minute)}}, domain.FreshnessSleepWake},
	} {
		t.Run(tc.name, func(t *testing.T) {
			owner, err := domain.NewObservationContinuity(30 * time.Second)
			if err != nil {
				t.Fatal(err)
			}
			if err = owner.Observe(domain.ContinuitySample{At: base, Available: true}); err != nil {
				t.Fatal(err)
			}
			old := owner.Snapshot()
			if err = owner.Observe(tc.sample); err != nil {
				t.Fatal(err)
			}
			lost := owner.Snapshot()
			if lost.Available || lost.Revision <= old.Revision || lost.Reason != tc.reason {
				t.Fatalf("failure preserved old fence: %+v", lost)
			}
			if err = owner.Observe(tc.sample); err != nil || owner.Snapshot() != lost {
				t.Fatalf("repeated failure churned state: %+v, %v", owner.Snapshot(), err)
			}
			if err = owner.Observe(domain.ContinuitySample{At: base.Add(2 * time.Second), Available: true}); err != nil {
				t.Fatal(err)
			}
			recovered := owner.Snapshot()
			if !recovered.Available || recovered.Revision <= lost.Revision {
				t.Fatalf("recovery reused pre-gap or unavailable fence: %+v", recovered)
			}
		})
	}
}

func TestObservationContinuityStableSamplesDoNotInventEvents(t *testing.T) {
	for _, gap := range []time.Duration{0, -time.Second} {
		if _, err := domain.NewObservationContinuity(gap); !errors.Is(err, domain.ErrInvalid) {
			t.Fatal("invalid maximum gap accepted")
		}
	}
	base := time.Now()
	owner, err := domain.NewObservationContinuity(30 * time.Second)
	if err != nil {
		t.Fatal(err)
	}
	// A historical native witness is a startup baseline, not a new wake event.
	power := domain.PowerWitness{Sleep: base.Add(-time.Hour), Wake: base.Add(-time.Minute)}
	for _, at := range []time.Time{base, base, base.Add(29 * time.Second)} {
		if err = owner.Observe(domain.ContinuitySample{At: at, Available: true, Power: power}); err != nil {
			t.Fatal(err)
		}
		if got := owner.Snapshot(); !got.Available || got.Revision != 1 || got.Reason != "" {
			t.Fatalf("stable sample invented native activity or invalidation: %+v", got)
		}
	}
}

func TestObservationContinuityUnavailableStartupCannotProvideFence(t *testing.T) {
	owner, err := domain.NewObservationContinuity(time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if err = owner.Observe(domain.ContinuitySample{}); err != nil || owner.Snapshot().Available {
		t.Fatalf("missing initial sample created continuity: %+v %v", owner.Snapshot(), err)
	}
	old := owner.Snapshot()
	if err = owner.Observe(domain.ContinuitySample{At: time.Now(), Available: true}); err != nil {
		t.Fatal(err)
	}
	if got := owner.Snapshot(); !got.Available || got.Revision <= old.Revision {
		t.Fatalf("initial unavailable revision reused: %+v", got)
	}
}

func TestObservationContinuityWitnessReversalCannotResurrectRevision(t *testing.T) {
	owner, err := domain.NewObservationContinuity(time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	base := time.Now().Round(0)
	old := domain.PowerWitness{Sleep: base.Add(-time.Hour), Wake: base.Add(-time.Minute)}
	changed := domain.PowerWitness{Sleep: base.Add(-30 * time.Second), Wake: base}
	var revision uint64
	for i, power := range []domain.PowerWitness{old, changed, old, changed} {
		if err = owner.Observe(domain.ContinuitySample{At: base.Add(time.Duration(i) * time.Second), Power: power, Available: true}); err != nil {
			t.Fatal(err)
		}
		state := owner.Snapshot()
		if !state.Available || state.Revision <= revision {
			t.Fatalf("repeated witness resurrected a revision: %+v", state)
		}
		revision = state.Revision
	}
	// Same wall timestamps in a different location are not a native power event.
	otherZone := time.FixedZone("fixture", 3600)
	changed.Sleep = changed.Sleep.In(otherZone)
	changed.Wake = changed.Wake.In(otherZone)
	if err = owner.Observe(domain.ContinuitySample{At: base.Add(4 * time.Second), Power: changed, Available: true}); err != nil {
		t.Fatal(err)
	}
	if owner.Snapshot().Revision != revision {
		t.Fatal("equivalent native wall timestamps invalidated continuity")
	}
}
