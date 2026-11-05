package runtime

import (
	"errors"
	"math"
	"testing"
	"time"
)

func TestObservationContinuityRevisionExhaustionPermanentlyClosesFence(t *testing.T) {
	owner, err := NewObservationContinuity(time.Second)
	if err != nil {
		t.Fatal(err)
	}
	base := time.Now()
	if err = owner.Observe(ContinuitySample{At: base, Available: true}); err != nil {
		t.Fatal(err)
	}
	// White-box setup exercises unreachable-in-practice overflow without adding
	// a production state-restore API to this deliberately nonpersisted owner.
	owner.state.Revision = math.MaxUint64
	if err = owner.Observe(ContinuitySample{At: base.Add(time.Second), Available: true}); !errors.Is(err, ErrLimit) {
		t.Fatalf("overflow was not rejected: %v", err)
	}
	if state := owner.Snapshot(); state.Available || !state.Exhausted || state.Revision != math.MaxUint64 {
		t.Fatalf("overflow preserved or reused intake fence: %+v", state)
	}
	closed := owner.Snapshot()
	for _, sample := range []ContinuitySample{{}, {At: base.Add(2 * time.Second), Available: true}} {
		if err = owner.Observe(sample); !errors.Is(err, ErrLimit) || owner.Snapshot() != closed {
			t.Fatalf("exhausted fence recovered or changed: %+v %v", owner.Snapshot(), err)
		}
	}
}
