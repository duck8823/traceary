package runtime_test

import (
	"errors"
	"fmt"
	domain "github.com/duck8823/traceary/domain/runtime"
	"strings"
	"testing"
)

func TestControlIntentRejectsAutomationMalformedUnionAndOversize(t *testing.T) {
	valid := domain.ControlIntentState{RequestID: "manual-1", Kind: domain.ControlInput, ManualTerminal: true, InputBytes: 3}
	for _, mutate := range []func(*domain.ControlIntentState){
		func(s *domain.ControlIntentState) { s.ManualTerminal = false },
		func(s *domain.ControlIntentState) { s.InputBytes = domain.MaxControlInputBytes + 1 },
		func(s *domain.ControlIntentState) { s.InputBytes = 0 },
		func(s *domain.ControlIntentState) { s.Rows = 24 },
		func(s *domain.ControlIntentState) { s.WorkLossAcknowledged = true },
		func(s *domain.ControlIntentState) { s.RequestID = "" },
		func(s *domain.ControlIntentState) { s.RequestID = strings.Repeat("x", 129) },
		func(s *domain.ControlIntentState) { s.Kind = "automatic_review" },
	} {
		state := valid
		mutate(&state)
		if _, err := domain.ControlIntentFrom(state); err == nil {
			t.Fatal("unsafe control intent accepted")
		}
	}
	if _, err := domain.ControlIntentFrom(valid); err != nil {
		t.Fatal(err)
	}
	resize := domain.ControlIntentState{RequestID: "resize-1", Kind: domain.ControlResize, ResizeSequence: 1, Rows: 30, Cols: 100}
	if _, err := domain.ControlIntentFrom(resize); err != nil {
		t.Fatal(err)
	}
	resize.ResizeSequence = 0
	if _, err := domain.ControlIntentFrom(resize); err == nil {
		t.Fatal("initial capture sequence reused for mutation")
	}
	stop := domain.ControlIntentState{RequestID: "stop-1", Kind: domain.ControlStop}
	if _, err := domain.ControlIntentFrom(stop); err == nil {
		t.Fatal("stop without work-loss acknowledgment accepted")
	}
	stop.WorkLossAcknowledged = true
	if _, err := domain.ControlIntentFrom(stop); err != nil {
		t.Fatal(err)
	}
}
func TestControlLedgerPartialUnknownAndDuplicateNeverReplay(t *testing.T) {
	id := domain.LaunchIdentity{Epoch: strings.Repeat("1", 32), Run: strings.Repeat("2", 32), Generation: strings.Repeat("3", 32)}
	ledger, err := domain.NewControlLedger(id)
	if err != nil {
		t.Fatal(err)
	}
	intent, err := domain.ControlIntentFrom(domain.ControlIntentState{RequestID: "manual-1", Kind: domain.ControlInput, ManualTerminal: true, InputBytes: 5})
	if err != nil {
		t.Fatal(err)
	}
	fingerprint := [32]byte{1}
	first, duplicate, err := ledger.Reserve(intent, fingerprint)
	if err != nil || duplicate || first.Outcome != domain.ControlPending || first.Identity != id {
		t.Fatal("intent reservation incorrect")
	}
	if _, err = ledger.Complete("manual-1", domain.ControlUnknown, 2); err != nil {
		t.Fatal(err)
	}
	existing, duplicate, err := ledger.Reserve(intent, fingerprint)
	if err != nil || !duplicate || existing.Outcome != domain.ControlUnknown || existing.WrittenBytes != 2 {
		t.Fatal("uncertain input could be replayed")
	}
	fingerprint[0]++
	if _, _, err = ledger.Reserve(intent, fingerprint); !errors.Is(err, domain.ErrChanged) {
		t.Fatal("conflicting duplicate payload accepted")
	}
	if _, err = ledger.Complete("manual-1", domain.ControlDelivered, 5); !errors.Is(err, domain.ErrChanged) {
		t.Fatal("unknown receipt rewritten as success")
	}
	if _, err = ledger.Complete("manual-1", domain.ControlUnknown, 2); err != nil {
		t.Fatal("exact receipt completion not idempotent")
	}
}
func TestControlLedgerKeepsLifecycleCapacityAndExistingReceipts(t *testing.T) {
	id := domain.LaunchIdentity{Epoch: strings.Repeat("1", 32), Run: strings.Repeat("2", 32), Generation: strings.Repeat("3", 32)}
	ledger, err := domain.NewControlLedger(id)
	if err != nil {
		t.Fatal(err)
	}
	input := func(name string) domain.ControlIntent {
		t.Helper()
		v, e := domain.ControlIntentFrom(domain.ControlIntentState{RequestID: name, Kind: domain.ControlInput, ManualTerminal: true, InputBytes: 1})
		if e != nil {
			t.Fatal(e)
		}
		return v
	}
	for i := 0; i < domain.MaxControlReceipts-domain.ReservedLifecycleReceipts; i++ {
		if _, _, err = ledger.Reserve(input(fmt.Sprintf("input-%d", i)), [32]byte{1}); err != nil {
			t.Fatal(err)
		}
	}
	if _, _, err = ledger.Reserve(input("overflow-input"), [32]byte{1}); !errors.Is(err, domain.ErrLimit) {
		t.Fatal("input consumed lifecycle reserve")
	}
	for i := 0; i < domain.ReservedLifecycleReceipts; i++ {
		stop, e := domain.ControlIntentFrom(domain.ControlIntentState{RequestID: fmt.Sprintf("stop-%d", i), Kind: domain.ControlStop, WorkLossAcknowledged: true})
		if e != nil {
			t.Fatal(e)
		}
		if _, _, err = ledger.Reserve(stop, [32]byte{1}); err != nil {
			t.Fatal("stop reserve unavailable")
		}
	}
	old, duplicate, err := ledger.Reserve(input("input-0"), [32]byte{1})
	if err != nil || !duplicate || old.Outcome != domain.ControlPending {
		t.Fatal("full ledger forgot original intent")
	}
	stop, _ := domain.ControlIntentFrom(domain.ControlIntentState{RequestID: "overflow-stop", Kind: domain.ControlStop, WorkLossAcknowledged: true})
	if _, _, err = ledger.Reserve(stop, [32]byte{1}); !errors.Is(err, domain.ErrLimit) {
		t.Fatal("receipt ledger exceeded fixed bound")
	}
}

func TestControlLedgerRefusesImpossibleByteResultsBeforeTerminalCommit(t *testing.T) {
	id := domain.LaunchIdentity{Epoch: strings.Repeat("1", 32), Run: strings.Repeat("2", 32), Generation: strings.Repeat("3", 32)}
	ledger, err := domain.NewControlLedger(id)
	if err != nil {
		t.Fatal(err)
	}
	intent, err := domain.ControlIntentFrom(domain.ControlIntentState{RequestID: "manual-1", Kind: domain.ControlInput, ManualTerminal: true, InputBytes: 4})
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err = ledger.Reserve(intent, [32]byte{1}); err != nil {
		t.Fatal(err)
	}
	for _, result := range []struct {
		outcome domain.ControlOutcome
		written int
	}{{domain.ControlDelivered, 3}, {domain.ControlRejected, 1}, {domain.ControlUnknown, 5}, {domain.ControlUnknown, -1}, {domain.ControlPending, 0}} {
		if _, err = ledger.Complete("manual-1", result.outcome, result.written); !errors.Is(err, domain.ErrInvalid) {
			t.Fatal("impossible receipt result accepted")
		}
	}
	if _, err = ledger.Complete("manual-1", domain.ControlDelivered, 4); err != nil {
		t.Fatal("invalid results changed original pending intent")
	}
	changed, err := domain.ControlIntentFrom(domain.ControlIntentState{RequestID: "manual-1", Kind: domain.ControlInput, ManualTerminal: true, InputBytes: 3})
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err = ledger.Reserve(changed, [32]byte{1}); !errors.Is(err, domain.ErrChanged) {
		t.Fatal("changed intent hid behind unchanged fingerprint")
	}
}

func TestControlLedgerRollbackCannotForgetUnknownAndOrderingPreservesQuota(t *testing.T) {
	id := domain.LaunchIdentity{Epoch: strings.Repeat("1", 32), Run: strings.Repeat("2", 32), Generation: strings.Repeat("3", 32)}
	ledger, err := domain.NewControlLedger(id)
	if err != nil {
		t.Fatal(err)
	}
	input, err := domain.ControlIntentFrom(domain.ControlIntentState{RequestID: "rollback", Kind: domain.ControlInput, ManualTerminal: true, InputBytes: 1})
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err = ledger.Reserve(input, [32]byte{1}); err != nil {
		t.Fatal(err)
	}
	if err = ledger.AbandonUncommitted("rollback"); err != nil {
		t.Fatal(err)
	}
	if _, err = ledger.Lookup("rollback"); !errors.Is(err, domain.ErrNotFound) {
		t.Fatal("clean rollback left a pending receipt")
	}
	if _, _, err = ledger.Reserve(input, [32]byte{1}); err != nil {
		t.Fatal(err)
	}
	if _, err = ledger.Complete("rollback", domain.ControlUnknown, 0); err != nil {
		t.Fatal(err)
	}
	if err = ledger.AbandonUncommitted("rollback"); !errors.Is(err, domain.ErrChanged) {
		t.Fatal("unknown receipt forgotten")
	}
	for i := 0; i < domain.ReservedLifecycleReceipts; i++ {
		stop, e := domain.ControlIntentFrom(domain.ControlIntentState{RequestID: fmt.Sprintf("stop-%d", i), Kind: domain.ControlStop, WorkLossAcknowledged: true})
		if e != nil {
			t.Fatal(e)
		}
		if _, _, err = ledger.Reserve(stop, [32]byte{1}); err != nil {
			t.Fatal(err)
		}
	}
	// One prior unknown input plus these1007 fills the independent1008 input quota.
	for i := 1; i < domain.MaxControlReceipts-domain.ReservedLifecycleReceipts; i++ {
		input, e := domain.ControlIntentFrom(domain.ControlIntentState{RequestID: fmt.Sprintf("input-%d", i), Kind: domain.ControlInput, ManualTerminal: true, InputBytes: 1})
		if e != nil {
			t.Fatal(e)
		}
		if _, _, err = ledger.Reserve(input, [32]byte{1}); err != nil {
			t.Fatal("earlier stop consumed non-lifecycle quota")
		}
	}
}

func TestControlResizeSequenceMustAdvanceExactlyWithoutOverflow(t *testing.T) {
	if err := domain.ValidateResizeSequence(0, 1); err != nil {
		t.Fatal(err)
	}
	for _, next := range []uint64{0, 2, 5} {
		if err := domain.ValidateResizeSequence(0, next); !errors.Is(err, domain.ErrChanged) {
			t.Fatal("resize sequence jumped or rewound")
		}
	}
	if err := domain.ValidateResizeSequence(^uint64(0), 0); !errors.Is(err, domain.ErrLimit) {
		t.Fatal("resize counter wrapped")
	}
}
