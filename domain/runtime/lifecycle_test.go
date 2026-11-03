package runtime_test

import (
	"errors"
	domain "github.com/duck8823/traceary/domain/runtime"
	"math"
	"strings"
	"testing"
	"time"
)

func TestShutdownBindingRequiresExactEpochRevisionAndWorkLossAcknowledgment(t *testing.T) {
	state := domain.ShutdownBindingState{Epoch: strings.Repeat("a", 32), PreviewID: strings.Repeat("b", 32), Revision: 17, RosterDigest: strings.Repeat("c", 64)}
	binding, err := domain.ShutdownBindingFrom(state)
	if err != nil {
		t.Fatal(err)
	}
	if err = binding.Confirm(state, false); !errors.Is(err, domain.ErrInvalid) {
		t.Fatalf("missing work-loss acknowledgment: %v", err)
	}
	if err = binding.Confirm(state, true); err != nil {
		t.Fatal(err)
	}
	for _, change := range []func(*domain.ShutdownBindingState){
		func(s *domain.ShutdownBindingState) { s.Epoch = strings.Repeat("c", 32) },
		func(s *domain.ShutdownBindingState) { s.PreviewID = strings.Repeat("d", 32) },
		func(s *domain.ShutdownBindingState) { s.Revision++ },
		func(s *domain.ShutdownBindingState) { s.RosterDigest = strings.Repeat("d", 64) },
		func(s *domain.ShutdownBindingState) { s.TotalRuns++ },
		func(s *domain.ShutdownBindingState) { s.PendingLaunches++ },
		func(s *domain.ShutdownBindingState) { s.PendingControls++ },
		func(s *domain.ShutdownBindingState) { s.PendingReconciliations++ },
	} {
		stale := state
		change(&stale)
		if err = binding.Confirm(stale, true); !errors.Is(err, domain.ErrChanged) {
			t.Fatalf("stale preview accepted: %v", err)
		}
	}
	for _, invalid := range []domain.ShutdownBindingState{
		{}, {Epoch: state.Epoch, PreviewID: strings.Repeat("B", 32)},
		{Epoch: "not-an-epoch", PreviewID: state.PreviewID},
	} {
		if _, err = domain.ShutdownBindingFrom(invalid); err == nil {
			t.Fatal("invalid shutdown binding accepted")
		}
	}
}

func TestFreshnessRejectsPreGapEvidenceAndNeverGrantsAuthority(t *testing.T) {
	id := domain.LaunchIdentity{Epoch: strings.Repeat("1", 32), Run: strings.Repeat("2", 32), Generation: strings.Repeat("3", 32)}
	lease, err := domain.NewFreshness(freshnessBinding(id))
	if err != nil {
		t.Fatal(err)
	}
	initial := lease.Snapshot()
	if initial.Known || initial.Binding != freshnessBinding(id) {
		t.Fatal("initial interaction fabricated")
	}
	if err = lease.Reconcile(freshnessEvidence(id, initial.Revision)); err != nil {
		t.Fatal(err)
	}
	if !lease.Snapshot().Known {
		t.Fatal("fresh trusted causal evidence not reflected")
	}
	for _, reason := range []domain.FreshnessReason{domain.FreshnessSleepWake, domain.FreshnessCommunicationGap, domain.FreshnessRestart, domain.FreshnessProcessExit} {
		lease, err = domain.NewFreshness(freshnessBinding(id))
		if err != nil {
			t.Fatal(err)
		}
		old := lease.Snapshot()
		if err = lease.Invalidate(reason); err != nil {
			t.Fatal(err)
		}
		if lease.Snapshot().Known || lease.Snapshot().Revision <= old.Revision {
			t.Fatal("stale interaction remained current")
		}
		if err = lease.Reconcile(freshnessEvidence(id, old.Revision)); !errors.Is(err, domain.ErrChanged) {
			t.Fatal("delayed pre-gap evidence restored freshness")
		}
		changed := id
		changed.Generation = strings.Repeat("4", 32)
		if err = lease.Reconcile(freshnessEvidence(changed, lease.Snapshot().Revision)); !errors.Is(err, domain.ErrChanged) {
			t.Fatal("cross-generation evidence accepted")
		}
		if reason != domain.FreshnessProcessExit && reason != domain.FreshnessRestart {
			if err = lease.Reconcile(freshnessEvidence(id, lease.Snapshot().Revision)); err != nil {
				t.Fatal(err)
			}
		} else if err = lease.Reconcile(freshnessEvidence(id, lease.Snapshot().Revision)); !errors.Is(err, domain.ErrChanged) {
			t.Fatal("ended epoch/process interaction resurrected")
		}
	}
}

func TestFreshnessInvalidReasonAndOverflowFailClosed(t *testing.T) {
	id := domain.LaunchIdentity{Epoch: strings.Repeat("1", 32), Run: strings.Repeat("2", 32), Generation: strings.Repeat("3", 32)}
	lease, err := domain.FreshnessFrom(domain.FreshnessState{Binding: freshnessBinding(id), Revision: math.MaxUint64, Known: true})
	if err != nil {
		t.Fatal(err)
	}
	if err = lease.Invalidate(domain.FreshnessSleepWake); !errors.Is(err, domain.ErrLimit) {
		t.Fatal("freshness revision overflow accepted")
	}
	if lease.Snapshot().Known {
		t.Fatal("overflow left stale interaction current")
	}
	if err = lease.Reconcile(freshnessEvidence(id, math.MaxUint64)); !errors.Is(err, domain.ErrChanged) {
		t.Fatal("overflow revalidation accepted")
	}
	other, err := domain.NewFreshness(freshnessBinding(id))
	if err != nil {
		t.Fatal(err)
	}
	if err = other.Invalidate("heartbeat"); !errors.Is(err, domain.ErrInvalid) {
		t.Fatal("unsupported invalidation accepted")
	}
}

func TestUpdateDeferralNotifiesEveryAttemptAndAt24HoursWithoutForcingUpdate(t *testing.T) {
	start := time.Date(2026, 11, 3, 12, 0, 0, 0, time.UTC)
	target := domain.UpdateTarget{Version: "0.99.0", Protocol: 2}
	deferred, err := domain.NewUpdateDeferral(target, start)
	if err != nil {
		t.Fatal(err)
	}
	if !deferred.Notice(start, domain.UpdateAttempt) {
		t.Fatal("explicit update attempt hidden")
	}
	if deferred.Notice(start.Add(24*time.Hour-time.Nanosecond), domain.UpdateStatus) {
		t.Fatal("premature automatic notice")
	}
	if !deferred.Notice(start.Add(24*time.Hour), domain.UpdateStatus) {
		t.Fatal("24-hour notice missing")
	}
	if deferred.Notice(start.Add(25*time.Hour), domain.UpdateStatus) {
		t.Fatal("automatic notice repeated")
	}
	for range 2 {
		if !deferred.Notice(start.Add(25*time.Hour), domain.UpdateAttempt) {
			t.Fatal("repeated attempt did not show defer")
		}
	}
	state := deferred.Snapshot()
	restored, err := domain.UpdateDeferralFrom(state)
	if err != nil {
		t.Fatal(err)
	}
	if restored.Snapshot().Started != start || restored.Snapshot().Target != target || restored.Notice(start.Add(30*time.Hour), domain.UpdateStatus) {
		t.Fatal("restart discarded/doubled deferral")
	}
	if restored.Notice(start.Add(-time.Hour), domain.UpdateStatus) {
		t.Fatal("backward clock generated time-based notice")
	}
	if !restored.Notice(start.Add(-time.Hour), domain.UpdateAttempt) {
		t.Fatal("clock rollback hid explicit defer")
	}
	for _, bad := range []domain.UpdateTarget{{}, {Version: "bad\ncommand", Protocol: 2}, {Version: "valid", Protocol: 0}, {Version: strings.Repeat("x", 129), Protocol: 2}} {
		if _, err = domain.NewUpdateDeferral(bad, start); err == nil {
			t.Fatal("unsafe update target accepted")
		}
	}
	if _, err = domain.NewUpdateDeferral(target, time.Time{}); err == nil {
		t.Fatal("missing deferral time accepted")
	}
}

func freshnessBinding(id domain.LaunchIdentity) domain.FreshnessBinding {
	return domain.FreshnessBinding{Identity: id, Worktree: domain.WorktreeID("w_" + strings.Repeat("a", 64)), Profile: "codex"}
}
func freshnessEvidence(id domain.LaunchIdentity, revision uint64) domain.CausalFreshnessEvidence {
	return domain.CausalFreshnessEvidence{Binding: freshnessBinding(id), Revision: revision, NativeSession: "native-session", NativeEvent: "native-event"}
}
func TestFreshnessRequiresFullSelectionAndNativeCausalWitness(t *testing.T) {
	id := domain.LaunchIdentity{Epoch: strings.Repeat("1", 32), Run: strings.Repeat("2", 32), Generation: strings.Repeat("3", 32)}
	lease, err := domain.NewFreshness(freshnessBinding(id))
	if err != nil {
		t.Fatal(err)
	}
	for _, mutate := range []func(*domain.CausalFreshnessEvidence){
		func(e *domain.CausalFreshnessEvidence) {
			e.Binding.Worktree = domain.WorktreeID("w_" + strings.Repeat("b", 64))
		},
		func(e *domain.CausalFreshnessEvidence) { e.Binding.Profile = "claude" },
		func(e *domain.CausalFreshnessEvidence) { e.NativeSession = "" },
		func(e *domain.CausalFreshnessEvidence) { e.NativeEvent = "" },
	} {
		e := freshnessEvidence(id, lease.Snapshot().Revision)
		mutate(&e)
		if err = lease.Reconcile(e); err == nil {
			t.Fatal("unbound or noncausal evidence accepted")
		}
		if lease.Snapshot().Known {
			t.Fatal("invalid evidence changed freshness")
		}
	}
}
func TestUpdateDeferralReadOnlyOverdueAndRetargetPreserveContinuousStart(t *testing.T) {
	start := time.Date(2026, 11, 3, 12, 0, 0, 0, time.UTC)
	d, err := domain.NewUpdateDeferral(domain.UpdateTarget{Version: "0.99.0", Protocol: 2}, start)
	if err != nil {
		t.Fatal(err)
	}
	before := d.Snapshot()
	if !d.Overdue24h(start.Add(24*time.Hour)) || !d.Overdue24h(start.Add(25*time.Hour)) || d.Snapshot() != before {
		t.Fatal("read-only overdue consumed notice")
	}
	if err = d.Retarget(domain.UpdateTarget{Version: "0.99.1", Protocol: 2}); err != nil {
		t.Fatal(err)
	}
	if d.Snapshot().Started != start || !d.Notice(start.Add(24*time.Hour), domain.UpdateStatus) {
		t.Fatal("version bump reset continuous deferral")
	}
	if !d.Overdue24h(start.Add(25 * time.Hour)) {
		t.Fatal("published notice hid overdue state")
	}
}
