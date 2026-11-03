package runtime

import (
	"math"
	"strings"
	"testing"
	"time"
)

func TestUpdateStatusExhaustedRevisionNeverAdvertisesQuiescence(t *testing.T) {
	owner := &Runtime{epoch: strings.Repeat("a", 32), revision: math.MaxUint64}
	status := owner.updateStatusLocked(time.Now())
	if status.Quiescent {
		t.Fatal("exhausted revision advertised a confirmable empty preview")
	}
	found := false
	for _, reason := range status.Blockers {
		if reason == "revision_exhausted" {
			found = true
		}
	}
	if !found {
		t.Fatal("exhausted revision was hidden from update status")
	}
}
