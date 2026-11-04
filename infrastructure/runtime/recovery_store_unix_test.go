//go:build darwin || linux

package runtime

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	app "github.com/duck8823/traceary/application/runtime"
	domain "github.com/duck8823/traceary/domain/runtime"
)

var _ app.RecoveryJournal = (*ExecutionStore)(nil)

func TestRecoveryJournalQualifiedExitedSuccessorReleasesOnlyConversationReservation(t *testing.T) {
	for _, phase := range []string{"reserved", "started", "unknown", "unknown wrong model", "confirmed final model", "confirmed no native exit"} {
		t.Run(phase, func(t *testing.T) {
			audit, root := recoveryMetadataFixture(t)
			store := audit.execution
			ctx := context.Background()
			records, err := store.ListRecovery(ctx)
			if err != nil {
				t.Fatal(err)
			}
			origin, err := domain.RecoveryRecordFrom(records[0])
			if err != nil {
				t.Fatal(err)
			}
			next := domain.LaunchIdentity{Run: strings.Repeat("4", 32), Generation: strings.Repeat("5", 32), Epoch: strings.Repeat("6", 32)}
			attempt, err := domain.NewResumeAttempt("first", origin, next)
			if err != nil {
				t.Fatal(err)
			}
			before := attempt.Snapshot()
			if _, fresh, err := store.ReserveResume(ctx, before); err != nil || !fresh {
				t.Fatalf("reserve: %v %v", fresh, err)
			}
			binding := origin.Snapshot().Binding.Snapshot()
			child, err := domain.LaunchDescriptorOf(next, binding.Target, binding.Profile)
			if err != nil {
				t.Fatal(err)
			}
			if err := store.ReserveLaunch(ctx, child.Snapshot()); err != nil {
				t.Fatal(err)
			}
			if err := child.Started(999); err != nil {
				t.Fatal(err)
			}
			if err := store.SaveLaunch(ctx, child.Snapshot()); err != nil {
				t.Fatal(err)
			}
			if phase != "reserved" {
				if err := attempt.MarkStarted(); err != nil {
					t.Fatal(err)
				}
				if err := store.SaveResume(ctx, before, attempt.Snapshot()); err != nil {
					t.Fatal(err)
				}
			}
			binding.Identity, binding.PID = next, 999
			started, err := domain.RecoveryBindingOf(binding)
			if err != nil {
				t.Fatal(err)
			}
			if phase == "unknown" || phase == "unknown wrong model" {
				before = attempt.Snapshot()
				if err := attempt.MarkUnknown(); err != nil {
					t.Fatal(err)
				}
				if err := store.SaveResume(ctx, before, attempt.Snapshot()); err != nil {
					t.Fatal(err)
				}
			}
			if strings.HasPrefix(phase, "confirmed") {
				before = attempt.Snapshot()
				if err := attempt.Confirm(started); err != nil {
					t.Fatal(err)
				}
				if err := store.SaveResume(ctx, before, attempt.Snapshot()); err != nil {
					t.Fatal(err)
				}
			}
			successor, err := domain.NewRecoveryRecord(started)
			if err != nil {
				t.Fatal(err)
			}
			if err := store.SaveRecovery(ctx, successor.Snapshot()); err != nil {
				t.Fatal(err)
			}
			if phase == "unknown wrong model" || phase == "confirmed final model" {
				binding.Model = "actual-final-model"
			}
			final, err := domain.RecoveryBindingOf(binding)
			if err != nil {
				t.Fatal(err)
			}
			premature, err := domain.NewRecoveryRecord(final)
			if err != nil {
				t.Fatal(err)
			}
			if err := store.SaveRecovery(ctx, premature.Snapshot()); (phase == "confirmed final model" || phase == "unknown wrong model") && !errors.Is(err, domain.ErrChanged) {
				t.Fatal("live model metadata was silently replaced")
			}
			if err := child.Exit(143); err != nil {
				t.Fatal(err)
			}
			if err := store.SaveLaunch(ctx, child.Snapshot()); err != nil {
				t.Fatal(err)
			}
			if err := successor.ObserveQualifiedOwnedExit(next, 143, final); err != nil {
				t.Fatal(err)
			}
			if phase != "confirmed no native exit" {
				if err := store.SaveRecovery(ctx, successor.Snapshot()); err != nil {
					t.Fatal(err)
				}
			}
			reopened, err := OpenExecutionStore(root, MaxRegistryBytes)
			if err != nil {
				t.Fatal(err)
			}
			laterID := domain.LaunchIdentity{Run: strings.Repeat("7", 32), Generation: strings.Repeat("8", 32), Epoch: next.Epoch}
			later, err := domain.NewResumeAttempt("new-explicit", successor, laterID)
			if err != nil {
				t.Fatal(err)
			}
			_, fresh, err := reopened.ReserveResume(ctx, later.Snapshot())
			if phase == "unknown wrong model" {
				if fresh || !errors.Is(err, domain.ErrChanged) {
					t.Fatalf("foreign unconfirmed binding released reservation: %v %v", fresh, err)
				}
			} else if phase == "confirmed no native exit" {
				if fresh || !errors.Is(err, domain.ErrInvalid) {
					t.Fatalf("new request accepted without durable native exit: %v %v", fresh, err)
				}
			} else if err != nil || !fresh {
				t.Fatalf("qualified owned exit still locked: %v %v", fresh, err)
			}
			saved, fresh, err := reopened.ReserveResume(ctx, domain.ResumeAttemptState{RequestID: "first", Binding: attempt.Snapshot().Binding, PredecessorExitCode: attempt.Snapshot().PredecessorExitCode, Next: next, Phase: domain.ResumeReserved})
			if err != nil || fresh || saved != attempt.Snapshot() {
				t.Fatalf("old request rearmed/rewritten: %v %v", fresh, err)
			}
		})
	}
}

func TestRecoveryConversationActivityNeedsBothExactOwnedExitReceipts(t *testing.T) {
	doc, _ := recoveryCodecFixture(t)
	origin, err := doc.Records[0].domainRecord()
	if err != nil {
		t.Fatal(err)
	}
	id := domain.LaunchIdentity{Run: strings.Repeat("4", 32), Generation: strings.Repeat("5", 32), Epoch: strings.Repeat("6", 32)}
	attempt, err := domain.NewResumeAttempt("unknown", origin, id)
	if err != nil {
		t.Fatal(err)
	}
	if err := attempt.MarkUnknown(); err != nil {
		t.Fatal(err)
	}
	b := origin.Snapshot().Binding.Snapshot()
	b.Identity = id
	b.PID = 999
	binding, err := domain.RecoveryBindingOf(b)
	if err != nil {
		t.Fatal(err)
	}
	child, err := domain.LaunchDescriptorOf(id, b.Target, b.Profile)
	if err != nil {
		t.Fatal(err)
	}
	if err := child.Started(999); err != nil {
		t.Fatal(err)
	}
	live := child.Snapshot()
	if err := child.Exit(143); err != nil {
		t.Fatal(err)
	}
	exited := child.Snapshot()
	receipt, err := domain.NewRecoveryRecord(binding)
	if err != nil {
		t.Fatal(err)
	}
	unobserved := receipt.Snapshot()
	if err := receipt.ObserveOwnedExit(id, 143); err != nil {
		t.Fatal(err)
	}
	qualified := receipt.Snapshot()
	for _, kind := range []string{"missing execution", "live execution", "missing native receipt", "no native exit", "wrong code", "wrong model", "wrong generation", "wrong pid"} {
		t.Run(kind, func(t *testing.T) {
			run, record := exited, qualified
			switch kind {
			case "missing execution":
				run = domain.RunSnapshot{}
			case "live execution":
				run = live
			case "missing native receipt":
				record = domain.RecoveryRecordState{}
			case "no native exit":
				record = unobserved
			case "wrong code":
				record.ExitCode = 0
			default:
				s := record.Binding.Snapshot()
				switch kind {
				case "wrong model":
					s.Model = "foreign"
				case "wrong generation":
					s.Identity.Generation = strings.Repeat("9", 32)
				case "wrong pid":
					s.PID++
				}
				value, err := domain.RecoveryBindingOf(s)
				if err != nil {
					t.Fatal(err)
				}
				record.Binding = value
			}
			if !recoveryConversationActive(attempt.Snapshot(), run, record) {
				t.Fatal("uncertain/foreign saved metadata unlocked conversation")
			}
		})
	}
	if recoveryConversationActive(attempt.Snapshot(), exited, qualified) {
		t.Fatal("exact durable qualified successor exit did not release new explicit recovery")
	}
}

func TestRecoveryJournalConcurrentReserveAndReopenNeverRearms(t *testing.T) {
	audit, root := recoveryMetadataFixture(t)
	store := audit.execution
	ctx := context.Background()
	records, err := store.ListRecovery(ctx)
	if err != nil || len(records) != 1 {
		t.Fatalf("records: %v %v", records, err)
	}
	record, err := domain.RecoveryRecordFrom(records[0])
	if err != nil {
		t.Fatal(err)
	}
	next := domain.LaunchIdentity{Run: strings.Repeat("4", 32), Generation: strings.Repeat("5", 32), Epoch: strings.Repeat("6", 32)}
	attempt, err := domain.NewResumeAttempt("resume-request", record, next)
	if err != nil {
		t.Fatal(err)
	}
	other, err := OpenExecutionStore(root, MaxRegistryBytes)
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	created := make(chan bool, 16)
	for i := 0; i < 16; i++ {
		wg.Add(1)
		selected := store
		if i%2 == 0 {
			selected = other
		}
		go func() {
			defer wg.Done()
			saved, fresh, err := selected.ReserveResume(ctx, attempt.Snapshot())
			if err != nil || saved != attempt.Snapshot() {
				t.Errorf("reserve changed: %+v %v", saved, err)
			}
			created <- fresh
		}()
	}
	wg.Wait()
	close(created)
	count := 0
	for fresh := range created {
		if fresh {
			count++
		}
	}
	if count != 1 {
		t.Fatalf("%d new reservations", count)
	}
	reopened, err := OpenExecutionStore(root, MaxRegistryBytes)
	if err != nil {
		t.Fatal(err)
	}
	proposal := attempt.Snapshot()
	proposal.Next.Run = strings.Repeat("7", 32)
	saved, fresh, err := reopened.ReserveResume(ctx, proposal)
	if err != nil || fresh || saved != attempt.Snapshot() {
		t.Fatalf("reopen rearmed reservation: %+v %v %v", saved, fresh, err)
	}
	proposal.RequestID = "another-request"
	if _, _, err := reopened.ReserveResume(ctx, proposal); !errors.Is(err, domain.ErrChanged) {
		t.Fatalf("second managed recovery accepted: %v", err)
	}
	if err := attempt.MarkUnknown(); err != nil {
		t.Fatal(err)
	}
	before := saved
	if err := reopened.SaveResume(ctx, before, attempt.Snapshot()); err != nil {
		t.Fatal(err)
	}
	if err := reopened.SaveResume(ctx, attempt.Snapshot(), before); !errors.Is(err, domain.ErrChanged) {
		t.Fatalf("unknown rearmed: %v", err)
	}
	proposal.RequestID = "resume-request"
	saved, fresh, err = reopened.ReserveResume(ctx, proposal)
	if err != nil || fresh || saved.Phase != domain.ResumeUnknown {
		t.Fatalf("unknown replay lost: %+v %v %v", saved, fresh, err)
	}
	listed, err := reopened.ListResumeAttempts(ctx)
	if err != nil || len(listed) != 1 || listed[0] != saved {
		t.Fatalf("lost persisted attempt inspection: %+v %v", listed, err)
	}
	listed[0].Phase = domain.ResumeReserved
	listed, err = reopened.ListResumeAttempts(ctx)
	if err != nil || listed[0].Phase != domain.ResumeUnknown {
		t.Fatal("inspection mutated stored outcome")
	}

}

func TestRecoveryJournalImmutableReceiptAndCanceledReservation(t *testing.T) {
	audit, _ := recoveryMetadataFixture(t)
	store := audit.execution
	ctx := context.Background()
	records, err := store.ListRecovery(ctx)
	if err != nil {
		t.Fatal(err)
	}
	before, err := audit.Export(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.SaveRecovery(ctx, records[0]); err != nil {
		t.Fatal(err)
	}
	unchanged, err := audit.Export(ctx)
	if err != nil || before.Digest != unchanged.Digest {
		t.Fatalf("idempotent save rewrote metadata: %v", err)
	}
	changed := records[0]
	changed.ExitObserved = false
	if err := store.SaveRecovery(ctx, changed); !errors.Is(err, domain.ErrChanged) {
		t.Fatalf("exit receipt regressed: %v", err)
	}
	state := records[0].Binding.Snapshot()
	state.Model = "other-model"
	changed.Binding, err = domain.RecoveryBindingOf(state)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.SaveRecovery(ctx, changed); !errors.Is(err, domain.ErrChanged) {
		t.Fatalf("binding replaced: %v", err)
	}
	record, err := domain.RecoveryRecordFrom(records[0])
	if err != nil {
		t.Fatal(err)
	}
	attempt, err := domain.NewResumeAttempt("canceled", record, domain.LaunchIdentity{Run: strings.Repeat("4", 32), Generation: strings.Repeat("5", 32), Epoch: strings.Repeat("6", 32)})
	if err != nil {
		t.Fatal(err)
	}
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	if _, fresh, err := store.ReserveResume(canceled, attempt.Snapshot()); !errors.Is(err, context.Canceled) || fresh {
		t.Fatalf("canceled reservation admitted: %v %v", fresh, err)
	}
	after, err := audit.Export(ctx)
	if err != nil || before.Digest != after.Digest {
		t.Fatalf("rejected mutations changed metadata: %v", err)
	}
}

func TestRecoveryJournalRealLaunchLinkageAndMonotonicResult(t *testing.T) {
	audit, root := recoveryMetadataFixture(t)
	store := audit.execution
	ctx := context.Background()
	records, err := store.ListRecovery(ctx)
	if err != nil {
		t.Fatal(err)
	}
	record, err := domain.RecoveryRecordFrom(records[0])
	if err != nil {
		t.Fatal(err)
	}
	next := domain.LaunchIdentity{Run: strings.Repeat("4", 32), Generation: strings.Repeat("5", 32), Epoch: strings.Repeat("6", 32)}
	attempt, err := domain.NewResumeAttempt("linked-request", record, next)
	if err != nil {
		t.Fatal(err)
	}
	before := attempt.Snapshot()
	if _, fresh, err := store.ReserveResume(ctx, before); err != nil || !fresh {
		t.Fatalf("reserve: %v %v", fresh, err)
	}
	if err := attempt.MarkStarted(); err != nil {
		t.Fatal(err)
	}
	if err := store.SaveResume(ctx, before, attempt.Snapshot()); !errors.Is(err, domain.ErrInvalid) {
		t.Fatalf("started without actual execution accepted: %v", err)
	}
	binding := record.Snapshot().Binding.Snapshot()
	descriptor, err := domain.LaunchDescriptorOf(next, binding.Target, binding.Profile)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.ReserveLaunch(ctx, descriptor.Snapshot()); err != nil {
		t.Fatal(err)
	}
	if err := descriptor.Started(999); err != nil {
		t.Fatal(err)
	}
	if err := store.SaveLaunch(ctx, descriptor.Snapshot()); err != nil {
		t.Fatal(err)
	}
	if err := store.SaveResume(ctx, before, attempt.Snapshot()); err != nil {
		t.Fatal(err)
	}
	before = attempt.Snapshot()
	binding.Identity, binding.PID = next, 999
	confirmed, err := domain.RecoveryBindingOf(binding)
	if err != nil {
		t.Fatal(err)
	}
	if err := attempt.Confirm(confirmed); err != nil {
		t.Fatal(err)
	}
	if err := store.SaveResume(ctx, before, attempt.Snapshot()); err != nil {
		t.Fatal(err)
	}
	if err := store.SaveResume(ctx, before, attempt.Snapshot()); err != nil {
		t.Fatalf("lost response replay not idempotent: %v", err)
	}
	successor, err := domain.NewRecoveryRecord(confirmed)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.SaveRecovery(ctx, successor.Snapshot()); err != nil {
		t.Fatal(err)
	}
	if err := successor.ObserveOwnedExit(next, -1); err != nil {
		t.Fatal(err)
	}
	if err := store.SaveRecovery(ctx, successor.Snapshot()); !errors.Is(err, domain.ErrInvalid) {
		t.Fatalf("exit receipt admitted before durable execution exit: %v", err)
	}
	if err := descriptor.Exit(-1); err != nil {
		t.Fatal(err)
	}
	if err := store.SaveLaunch(ctx, descriptor.Snapshot()); err != nil {
		t.Fatal(err)
	}
	if err := store.SaveRecovery(ctx, successor.Snapshot()); err != nil {
		t.Fatal(err)
	}
	reopened, err := OpenExecutionStore(root, MaxRegistryBytes)
	if err != nil {
		t.Fatal(err)
	}
	loaded, err := reopened.ListRecovery(ctx)
	if err != nil || len(loaded) != 2 || !loaded[1].ExitObserved || loaded[1].ExitCode != -1 {
		t.Fatalf("lost owned signal exit: %+v %v", loaded, err)
	}
	next.Run, next.Generation = strings.Repeat("7", 32), strings.Repeat("8", 32)
	later, err := domain.NewResumeAttempt("later-request", successor, next)
	if err != nil {
		t.Fatal(err)
	}
	if _, fresh, err := reopened.ReserveResume(ctx, later.Snapshot()); err != nil || !fresh {
		t.Fatalf("successor could not recover after actual observed exit: %v %v", fresh, err)
	}
	if _, fresh, err := reopened.ReserveResume(ctx, attempt.Snapshot()); !errors.Is(err, domain.ErrInvalid) || fresh {
		t.Fatalf("non-reserved proposal admitted: %v %v", fresh, err)
	}
}

func TestRecoveryJournalRejectsExistingSuccessorAndOverflowWithoutEffects(t *testing.T) {
	for _, kind := range []string{"existing successor", "revision overflow", "byte budget"} {
		t.Run(kind, func(t *testing.T) {
			audit, root := recoveryMetadataFixture(t)
			store := audit.execution
			ctx := context.Background()
			records, err := store.ListRecovery(ctx)
			if err != nil {
				t.Fatal(err)
			}
			record, err := domain.RecoveryRecordFrom(records[0])
			if err != nil {
				t.Fatal(err)
			}
			next := domain.LaunchIdentity{Run: strings.Repeat("4", 32), Generation: strings.Repeat("5", 32), Epoch: strings.Repeat("6", 32)}
			attempt, err := domain.NewResumeAttempt("failure-request", record, next)
			if err != nil {
				t.Fatal(err)
			}
			want := domain.ErrLimit
			switch kind {
			case "existing successor":
				binding := records[0].Binding.Snapshot()
				descriptor, err := domain.LaunchDescriptorOf(next, binding.Target, binding.Profile)
				if err != nil {
					t.Fatal(err)
				}
				if err := store.ReserveLaunch(ctx, descriptor.Snapshot()); err != nil {
					t.Fatal(err)
				}
				want = domain.ErrChanged
			case "revision overflow":
				doc, _ := recoveryCodecFixture(t)
				*doc.Revision = math.MaxUint64
				data, err := json.Marshal(doc)
				if err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(root, "recovery.json"), data, 0600); err != nil {
					t.Fatal(err)
				}
			case "byte budget":
				store, err = OpenExecutionStore(root, 64)
				if err != nil {
					t.Fatal(err)
				}
			}
			before, err := audit.Export(ctx)
			if err != nil {
				t.Fatal(err)
			}
			if _, fresh, err := store.ReserveResume(ctx, attempt.Snapshot()); !errors.Is(err, want) || fresh {
				t.Fatalf("invalid admission: %v %v", fresh, err)
			}
			after, err := audit.Export(ctx)
			if err != nil || before.Digest != after.Digest {
				t.Fatalf("failure changed durable state: %v", err)
			}
		})
	}
}
