package runtime_test

import (
	"errors"
	"strconv"
	"strings"
	"testing"
	"time"

	domain "github.com/duck8823/traceary/domain/runtime"
)

// These fixtures model a trusted native adapter, not native host qualification.
func projectionFixture(t *testing.T) (*domain.ObservationProjection, domain.ProjectionSelection) {
	t.Helper()
	b := domain.ProjectionSelection{Identity: domain.LaunchIdentity{Run: strings.Repeat("1", 32), Generation: strings.Repeat("2", 32), Epoch: strings.Repeat("3", 32)}, Worktree: domain.WorktreeID("w_" + strings.Repeat("4", 64)), Profile: "host"}
	p, err := domain.NewObservationProjection(b, domain.RunLive)
	if err != nil {
		t.Fatal(err)
	}
	return p, b
}

func projectionClock(seconds int) domain.ProjectionClock {
	return domain.ProjectionClock{Elapsed: time.Duration(seconds) * time.Second, Wall: time.Unix(1700000000+int64(seconds), 0).UTC()}
}

func projectionEvent(t *testing.T, b domain.ProjectionSelection, id string, kind domain.ProjectionKind, seconds int) domain.ProjectionEvent {
	t.Helper()
	e, err := domain.ProjectionEventOf(domain.ProjectionEventState{Selection: b, Source: "codex", SourceVersion: "test-1", EventID: id, NativeSessionID: "native", TurnID: "turn", RequestID: "request", Kind: kind, Received: projectionClock(seconds)})
	if err != nil {
		t.Fatal(err)
	}
	return e
}

func TestProjectionBothInteractionStatesExpireWithoutDuplicateRenewal(t *testing.T) {
	for _, kind := range []domain.ProjectionKind{domain.ProjectionWorking, domain.ProjectionWaiting} {
		t.Run(string(kind), func(t *testing.T) {
			p, b := projectionFixture(t)
			e := projectionEvent(t, b, "first", kind, 0)
			n := domain.NativeProjectionEvidence{Event: e, Revision: 1, Sequence: 10}
			if err := p.ReconcileNative(n, projectionClock(0)); err != nil {
				t.Fatal(err)
			}
			first := p.Snapshot(projectionClock(0))
			if first.Interaction != kind {
				t.Fatal(first)
			}
			for _, at := range []int{59, 60, 61, 120} {
				if err := p.ReconcileNative(n, projectionClock(at)); err != nil {
					t.Fatal(err)
				}
				s := p.Snapshot(projectionClock(at))
				if at < 60 && s.Interaction != kind || at >= 60 && s.Interaction != domain.ProjectionUnknown {
					t.Fatalf("at %d: %+v", at, s)
				}
				if s.Attention != first.Attention {
					t.Fatal("duplicate emitted Attention")
				}
				if kind == domain.ProjectionWaiting && (s.LastWaiting.Received != e.Snapshot().Received || s.LastWaitingStale != (at >= 60)) {
					t.Fatal("last waiting changed", s)
				}
			}
		})
	}
}

func TestProjectionOriginalIntakeAgeNotDequeueOrWallClockDeterminesFreshness(t *testing.T) {
	p, b := projectionFixture(t)
	e := projectionEvent(t, b, "queued", domain.ProjectionWaiting, 0)
	now := projectionClock(61)
	now.Wall = projectionClock(-9000).Wall
	if err := p.ReconcileNative(domain.NativeProjectionEvidence{Event: e, Revision: 1, Sequence: 1}, now); err != nil {
		t.Fatal(err)
	}
	s := p.Snapshot(now)
	if s.Interaction != domain.ProjectionUnknown || !s.HasLastWaiting || !s.LastWaitingStale || s.Attention.EventID != "" {
		t.Fatal(s)
	}
}

func TestProjectionCausalOrderConflictAndForeignSelection(t *testing.T) {
	for _, test := range []string{"predecessor", "sequence_conflict", "event_conflict", "foreign_generation", "old_revision", "old_order"} {
		t.Run(test, func(t *testing.T) {
			p, b := projectionFixture(t)
			first := projectionEvent(t, b, "first", domain.ProjectionWorking, 0)
			if err := p.ReconcileNative(domain.NativeProjectionEvidence{Event: first, Revision: 1, Sequence: 5}, projectionClock(0)); err != nil {
				t.Fatal(err)
			}
			second := projectionEvent(t, b, "second", domain.ProjectionWaiting, 1)
			if err := p.ReconcileNative(domain.NativeProjectionEvidence{Event: second, Revision: 1, Sequence: 6, PreviousEventID: "first"}, projectionClock(1)); err != nil {
				t.Fatal(err)
			}
			n := domain.NativeProjectionEvidence{Event: projectionEvent(t, b, "third", domain.ProjectionWorking, 2), Revision: 1, Sequence: 7, PreviousEventID: "second"}
			switch test {
			case "predecessor":
				n.PreviousEventID = "missing"
			case "sequence_conflict":
				n.Sequence = 6
			case "event_conflict":
				n.Event = projectionEvent(t, b, "second", domain.ProjectionWorking, 2)
			case "foreign_generation":
				b.Identity.Generation = strings.Repeat("9", 32)
				n.Event = projectionEvent(t, b, "third", domain.ProjectionWorking, 2)
			case "old_revision":
				n.Revision = 0
			case "old_order":
				n.Sequence = 4
			}
			err := p.ReconcileNative(n, projectionClock(2))
			s := p.Snapshot(projectionClock(2))
			if test == "foreign_generation" || test == "old_order" {
				if s.Interaction != domain.ProjectionWaiting {
					t.Fatal("foreign/old changed current", s)
				}
			} else if s.Interaction != domain.ProjectionUnknown {
				t.Fatal("ambiguous evidence remained current", s)
			}
			if test != "old_order" && !errors.Is(err, domain.ErrChanged) {
				t.Fatalf("error=%v", err)
			}
			if s.Process != domain.RunLive {
				t.Fatal("telemetry changed process")
			}
		})
	}
}

func TestProjectionInvalidationAndReplayNeverResurrectWaiting(t *testing.T) {
	for _, reason := range []domain.ProjectionReason{domain.ProjectionGap, domain.ProjectionSleepWake, domain.ProjectionRestart} {
		t.Run(string(reason), func(t *testing.T) {
			p, b := projectionFixture(t)
			e := projectionEvent(t, b, "wait", domain.ProjectionWaiting, 0)
			n := domain.NativeProjectionEvidence{Event: e, Revision: 1, Sequence: 1}
			if err := p.ReconcileNative(n, projectionClock(0)); err != nil {
				t.Fatal(err)
			}
			p.Invalidate(reason)
			if err := p.ReconcileNative(n, projectionClock(1)); !errors.Is(err, domain.ErrChanged) {
				t.Fatal(err)
			}
			s := p.Snapshot(projectionClock(1))
			if s.Interaction != domain.ProjectionUnknown || !s.LastWaitingStale || s.LastWaiting.EventID != "wait" {
				t.Fatal(s)
			}
			next := projectionEvent(t, b, "new", domain.ProjectionWorking, 2)
			if err := p.ReconcileNative(domain.NativeProjectionEvidence{Event: next, Revision: s.ContinuityRevision, Sequence: 2, PreviousEventID: "wait"}, projectionClock(2)); err != nil {
				t.Fatal(err)
			}
			if p.Snapshot(projectionClock(2)).Interaction != domain.ProjectionWorking {
				t.Fatal("fresh new evidence could not restore")
			}
		})
	}
}

func TestProjectionUnboundStopAndApprovalCannotGrantInteractionOrProcessAuthority(t *testing.T) {
	p, b := projectionFixture(t)
	for i, kind := range []domain.ProjectionKind{domain.ProjectionWaiting, domain.ProjectionWorking, domain.ProjectionStop, domain.ProjectionApproval} {
		e := projectionEvent(t, b, string(rune('a'+i)), kind, i)
		if err := p.ObserveUnbound(e, projectionClock(i)); err != nil {
			t.Fatal(err)
		}
		s := p.Snapshot(projectionClock(i))
		if s.Interaction != domain.ProjectionUnknown || s.Process != domain.RunLive || s.HasLastWaiting || s.Attention.EventID != "" {
			t.Fatal(s)
		}
	}
	for i, kind := range []domain.ProjectionKind{domain.ProjectionStop, domain.ProjectionApproval} {
		q, c := projectionFixture(t)
		if err := q.ReconcileNative(domain.NativeProjectionEvidence{Event: projectionEvent(t, c, "native", kind, i), Revision: 1, Sequence: 1}, projectionClock(i)); err != nil {
			t.Fatal(err)
		}
		if s := q.Snapshot(projectionClock(i)); s.Process != domain.RunLive || s.Interaction != domain.ProjectionUnknown {
			t.Fatal(s)
		}
	}
}

func TestProjectionAttentionIsTransitionOnlyAndLastWaitingIsHistorical(t *testing.T) {
	p, b := projectionFixture(t)
	previous := ""
	for i, kind := range []domain.ProjectionKind{domain.ProjectionWaiting, domain.ProjectionWaiting, domain.ProjectionWorking} {
		id := string(rune('a' + i))
		e := projectionEvent(t, b, id, kind, i)
		if err := p.ReconcileNative(domain.NativeProjectionEvidence{Event: e, Revision: 1, Sequence: uint64(i + 1), PreviousEventID: previous}, projectionClock(i)); err != nil {
			t.Fatal(err)
		}
		previous = id
		s := p.Snapshot(projectionClock(i))
		if s.Attention.EventID != "a" {
			t.Fatal("repeated waiting emitted transition", s)
		}
		if i == 2 && (!s.LastWaitingStale || s.LastWaiting.EventID != "b") {
			t.Fatal(s)
		}
	}
	if err := p.UpdateProcess(domain.RunExited); err != nil {
		t.Fatal(err)
	}
	if s := p.Snapshot(projectionClock(3)); s.Process != domain.RunExited || s.LastWaiting.EventID != "b" || !s.LastWaitingStale {
		t.Fatal(s)
	}
}

func TestProjectionClockReversalAndLedgerExhaustionFailClosed(t *testing.T) {
	p, b := projectionFixture(t)
	if err := p.ReconcileNative(domain.NativeProjectionEvidence{Event: projectionEvent(t, b, "wait", domain.ProjectionWaiting, 10), Revision: 1, Sequence: 1}, projectionClock(10)); err != nil {
		t.Fatal(err)
	}
	if s := p.Snapshot(projectionClock(9)); s.Interaction != domain.ProjectionUnknown || !s.LastWaitingStale {
		t.Fatal(s)
	}
	q, c := projectionFixture(t)
	previous := ""
	for i := 0; i < domain.MaxProjectionEvents+1; i++ {
		e := projectionEvent(t, c, "event:"+strconv.Itoa(i), domain.ProjectionWorking, i)
		err := q.ReconcileNative(domain.NativeProjectionEvidence{Event: e, Revision: 1, Sequence: uint64(i + 1), PreviousEventID: previous}, projectionClock(i))
		previous = e.Snapshot().EventID
		if i == domain.MaxProjectionEvents && !errors.Is(err, domain.ErrLimit) {
			t.Fatal("ledger did not close", err)
		}
	}
	if s := q.Snapshot(projectionClock(400)); !s.Exhausted || s.Interaction != domain.ProjectionUnknown {
		t.Fatal(s)
	}
}

func TestProjectionUnboundArrivalsNeverConsumeNativeCausalLedger(t *testing.T) {
	p, b := projectionFixture(t)
	for i := 0; i < domain.MaxProjectionEvents*4; i++ {
		if err := p.ObserveUnbound(projectionEvent(t, b, "unbound:"+strconv.Itoa(i), domain.ProjectionUnknown, i), projectionClock(i)); err != nil {
			t.Fatalf("unbound arrival %d rejected: %v", i, err)
		}
	}
	now := domain.MaxProjectionEvents * 4
	if err := p.ReconcileNative(domain.NativeProjectionEvidence{Event: projectionEvent(t, b, "native-new", domain.ProjectionWaiting, now), Revision: 1, Sequence: 1}, projectionClock(now)); err != nil {
		t.Fatal(err)
	}
	if s := p.Snapshot(projectionClock(now)); s.Exhausted || s.Interaction != domain.ProjectionWaiting {
		t.Fatal(s)
	}
}

func TestProjectionInitialReorderCannotResurrectOlderKnowledgeAndNextFreshEdgeRecovers(t *testing.T) {
	p, b := projectionFixture(t)
	n := domain.NativeProjectionEvidence{Event: projectionEvent(t, b, "B", domain.ProjectionWaiting, 0), Revision: 1, Sequence: 2, PreviousEventID: "A"}
	if err := p.ReconcileNative(n, projectionClock(0)); !errors.Is(err, domain.ErrChanged) {
		t.Fatal(err)
	}
	if err := p.ReconcileNative(domain.NativeProjectionEvidence{Event: projectionEvent(t, b, "A", domain.ProjectionWorking, 1), Revision: 1, Sequence: 1}, projectionClock(1)); err != nil {
		t.Fatal(err)
	}
	if s := p.Snapshot(projectionClock(1)); s.Interaction != domain.ProjectionUnknown {
		t.Fatal("old A restored knowledge despite known B", s)
	}
	if err := p.ReconcileNative(domain.NativeProjectionEvidence{Event: projectionEvent(t, b, "C", domain.ProjectionWorking, 2), Revision: 1, Sequence: 3, PreviousEventID: "B"}, projectionClock(2)); err != nil {
		t.Fatal(err)
	}
	if s := p.Snapshot(projectionClock(2)); s.Interaction != domain.ProjectionWorking {
		t.Fatal("new verified edge did not recover", s)
	}
}

func TestProjectionNativeExhaustionDoesNotRejectUnboundHistory(t *testing.T) {
	p, b := projectionFixture(t)
	previous := ""
	for i := 0; i <= domain.MaxProjectionEvents; i++ {
		e := projectionEvent(t, b, "native:"+strconv.Itoa(i), domain.ProjectionWorking, i)
		err := p.ReconcileNative(domain.NativeProjectionEvidence{Event: e, Revision: 1, Sequence: uint64(i + 1), PreviousEventID: previous}, projectionClock(i))
		if i < domain.MaxProjectionEvents && err != nil {
			t.Fatal(err)
		}
		previous = e.Snapshot().EventID
	}
	if err := p.ObserveUnbound(projectionEvent(t, b, "still-capturing", domain.ProjectionUnknown, 300), projectionClock(300)); err != nil {
		t.Fatal(err)
	}
	if s := p.Snapshot(projectionClock(300)); !s.Exhausted || s.LastEvent.EventID != "still-capturing" || s.Interaction != domain.ProjectionUnknown {
		t.Fatal(s)
	}
}

func TestProjectionTrustedMetadataBoundariesAndReadDrivenExpiry(t *testing.T) {
	for _, test := range []string{"source", "session", "future_receipt", "host_time", "reserved", "attention_version"} {
		t.Run(test, func(t *testing.T) {
			p, b := projectionFixture(t)
			if test == "reserved" {
				var err error
				p, err = domain.NewObservationProjection(b, domain.RunReserved)
				if err != nil {
					t.Fatal(err)
				}
			}
			first := projectionEvent(t, b, "first", domain.ProjectionWorking, 0)
			if err := p.ReconcileNative(domain.NativeProjectionEvidence{Event: first, Revision: 1, Sequence: 1}, projectionClock(0)); err != nil {
				t.Fatal(err)
			}
			state := projectionEvent(t, b, "next", domain.ProjectionWaiting, 1).Snapshot()
			now := projectionClock(1)
			switch test {
			case "source":
				state.Source = "claude"
			case "session":
				state.NativeSessionID = "foreign"
			case "future_receipt":
				state.Received = projectionClock(100)
			case "host_time":
				state.HostTime = time.Unix(9000000000, 0)
				state.HasHostTime = true
				now.Wall = time.Unix(2, 0)
			}
			e, err := domain.ProjectionEventOf(state)
			if err != nil {
				t.Fatal(err)
			}
			err = p.ReconcileNative(domain.NativeProjectionEvidence{Event: e, Revision: 1, Sequence: 2, PreviousEventID: "first"}, now)
			s := p.Snapshot(now)
			switch test {
			case "source", "session", "future_receipt":
				if !errors.Is(err, domain.ErrChanged) || s.Interaction != domain.ProjectionUnknown {
					t.Fatal(err, s)
				}
			case "reserved":
				if err != nil || s.Interaction != domain.ProjectionUnknown || s.Reason != domain.ProjectionProcessInactive || s.Process != domain.RunReserved {
					t.Fatal(err, s)
				}
			default:
				if err != nil || s.Interaction != domain.ProjectionWaiting || s.RefreshAfter != 60*time.Second || s.Attention.Revision != s.Version {
					t.Fatal(err, s)
				}
				if later := p.Snapshot(projectionClock(30)); later.RefreshAfter != 31*time.Second {
					t.Fatal(later)
				}
				if expired := p.Snapshot(projectionClock(61)); expired.Interaction != domain.ProjectionUnknown || expired.RefreshAfter != 0 {
					t.Fatal(expired)
				}
			}
		})
	}
}

func FuzzProjectionCausalPermutationsNeverRestoreOlderState(f *testing.F) {
	f.Add([]byte{2, 0, 1, 2, 1, 0})
	f.Add([]byte{0, 1, 2, 0, 1, 2})
	f.Fuzz(func(t *testing.T, order []byte) {
		if len(order) > 200 {
			order = order[:200]
		}
		p, b := projectionFixture(t)
		highest := uint64(0)
		for i, v := range order {
			seq := uint64(v%3 + 1)
			id := "event:" + strconv.FormatUint(seq, 10)
			prev := ""
			if seq > 1 {
				prev = "event:" + strconv.FormatUint(seq-1, 10)
			}
			kind := domain.ProjectionWorking
			if seq == 2 {
				kind = domain.ProjectionWaiting
			}
			// Native order and the receipt/delivery order deliberately differ.
			err := p.ReconcileNative(domain.NativeProjectionEvidence{Event: projectionEvent(t, b, id, kind, i), Revision: 1, Sequence: seq, PreviousEventID: prev}, projectionClock(i))
			if err != nil && !errors.Is(err, domain.ErrChanged) {
				t.Fatal(err)
			}
			s := p.Snapshot(projectionClock(i))
			if seq < highest && s.Interaction != domain.ProjectionUnknown {
				want := domain.ProjectionWorking
				if highest == 2 {
					want = domain.ProjectionWaiting
				}
				if s.Interaction != want {
					t.Fatal("older reordered event restored state", seq, highest, s)
				}
			}
			if seq > highest {
				highest = seq
			}
			if s.Process != domain.RunLive {
				t.Fatal("reordered telemetry changed process", s)
			}
		}
	})
}

func TestProjectionHealthAndProcessAreIndependentOfInteraction(t *testing.T) {
	p, b := projectionFixture(t)
	e := projectionEvent(t, b, "waiting", domain.ProjectionWaiting, 0)
	if err := p.ReconcileNative(domain.NativeProjectionEvidence{Event: e, Revision: 1, Sequence: 1}, projectionClock(0)); err != nil {
		t.Fatal(err)
	}
	if err := p.UpdateCapture(domain.CaptureDegraded); err != nil {
		t.Fatal(err)
	}
	if err := p.UpdateConnection(domain.ConnectionDisconnected); err != nil {
		t.Fatal(err)
	}
	s := p.Snapshot(projectionClock(1))
	if s.CaptureHealth != domain.CaptureDegraded || s.ConnectionHealth != domain.ConnectionDisconnected || s.Process != domain.RunLive || s.Interaction != domain.ProjectionWaiting {
		t.Fatal(s)
	}
	if err := p.UpdateProcess(domain.RunExited); err != nil {
		t.Fatal(err)
	}
	if err := p.UpdateProcess(domain.RunLive); !errors.Is(err, domain.ErrChanged) {
		t.Fatal("exited process resurrected", err)
	}
	s = p.Snapshot(projectionClock(2))
	if s.Interaction != domain.ProjectionUnknown || !s.LastWaitingStale || s.CaptureHealth != domain.CaptureDegraded || s.ConnectionHealth != domain.ConnectionDisconnected {
		t.Fatal(s)
	}
}

func TestProjectionUnseenHistoricalArrivalCannotOverrideCausalLastWaiting(t *testing.T) {
	p, b := projectionFixture(t)
	for i, kind := range []domain.ProjectionKind{domain.ProjectionWaiting, domain.ProjectionWorking} {
		id := []string{"waiting", "working"}[i]
		previous := ""
		if i == 1 {
			previous = "waiting"
		}
		if err := p.ReconcileNative(domain.NativeProjectionEvidence{Event: projectionEvent(t, b, id, kind, i), Revision: 1, Sequence: uint64(i + 5), PreviousEventID: previous}, projectionClock(i)); err != nil {
			t.Fatal(err)
		}
	}
	old := projectionEvent(t, b, "older-unseen", domain.ProjectionWaiting, 2)
	if err := p.ReconcileNative(domain.NativeProjectionEvidence{Event: old, Revision: 1, Sequence: 3}, projectionClock(2)); err != nil {
		t.Fatal(err)
	}
	s := p.Snapshot(projectionClock(2))
	if s.Interaction != domain.ProjectionWorking || s.LastWaiting.EventID != "waiting" || !s.LastWaitingStale || s.LastEvent.EventID != "older-unseen" {
		t.Fatal(s)
	}
}

func FuzzProjectionDuplicateCannotRenewOrCreateAttention(f *testing.F) {
	f.Add([]byte{1, 59, 1, 1, 60}, true)
	f.Add([]byte{60, 0, 0, 1}, false)
	f.Fuzz(func(t *testing.T, steps []byte, waiting bool) {
		if len(steps) > 300 {
			steps = steps[:300]
		}
		p, b := projectionFixture(t)
		kind := domain.ProjectionWorking
		if waiting {
			kind = domain.ProjectionWaiting
		}
		e := projectionEvent(t, b, "one-event", kind, 0)
		n := domain.NativeProjectionEvidence{Event: e, Revision: 1, Sequence: 1}
		if err := p.ReconcileNative(n, projectionClock(0)); err != nil {
			t.Fatal(err)
		}
		original := p.Snapshot(projectionClock(0))
		seconds := 0
		for _, step := range steps {
			seconds += int(step)
			// An independently re-received duplicate has a later local receipt;
			// identity, not the replacement receipt, owns its original age.
			n.Event = projectionEvent(t, b, "one-event", kind, seconds)
			if err := p.ReconcileNative(n, projectionClock(seconds)); err != nil {
				t.Fatal(err)
			}
			s := p.Snapshot(projectionClock(seconds))
			if seconds >= 60 && s.Interaction != domain.ProjectionUnknown || seconds < 60 && s.Interaction != kind {
				t.Fatal(seconds, s)
			}
			if s.Attention != original.Attention || s.Process != domain.RunLive {
				t.Fatal("duplicate changed Attention/process", s)
			}
		}
	})
}
