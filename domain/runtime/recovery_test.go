package runtime_test

import (
	"errors"
	"reflect"
	"strings"
	"testing"

	domain "github.com/duck8823/traceary/domain/runtime"
)

func recoveryBindingFixture(t *testing.T) domain.RecoveryBinding {
	t.Helper()
	value, err := domain.RecoveryBindingOf(domain.RecoveryBindingState{
		Identity: domain.LaunchIdentity{Run: strings.Repeat("1", 32), Generation: strings.Repeat("2", 32), Epoch: strings.Repeat("3", 32)},
		Target:   launchTarget(t), Profile: launchProfile(t), PID: 321,
		NativeSessionID: "synthetic-native-session", NativeHistoryPath: "/native/session.jsonl",
		NativeNamespaceWitness: strings.Repeat("a", 64), Model: "synthetic-model", Provider: "synthetic-provider",
	})
	if err != nil {
		t.Fatal(err)
	}
	return value
}

func TestRecoveryRecordFinalModelCanChangeOnlyWhenSealingSameOwnedExit(t *testing.T) {
	initial := recoveryBindingFixture(t)
	record, err := domain.NewRecoveryRecord(initial)
	if err != nil {
		t.Fatal(err)
	}
	state := initial.Snapshot()
	state.Model = "actual-final-model"
	final, err := domain.RecoveryBindingOf(state)
	if err != nil {
		t.Fatal(err)
	}
	for _, kind := range []string{"provider", "native identity", "pid", "history", "namespace"} {
		changed := state
		switch kind {
		case "provider":
			changed.Provider = "other"
		case "native identity":
			changed.NativeSessionID = "other"
		case "pid":
			changed.PID++
		case "history":
			changed.NativeHistoryPath = "/other/history.jsonl"
		case "namespace":
			changed.NativeNamespaceWitness = strings.Repeat("b", 64)
		}
		value, err := domain.RecoveryBindingOf(changed)
		if err != nil {
			t.Fatal(err)
		}
		before := record.Snapshot()
		if err := record.ObserveQualifiedOwnedExit(state.Identity, 0, value); !errors.Is(err, domain.ErrChanged) {
			t.Fatalf("%s retargeted: %v", kind, err)
		}
		if record.Snapshot() != before {
			t.Fatal("failed exit mutated native identity")
		}
	}
	if err := record.ObserveQualifiedOwnedExit(state.Identity, 143, final); err != nil {
		t.Fatal(err)
	}
	if got := record.Snapshot(); got.Binding != final || !got.ExitObserved || got.ExitCode != 143 {
		t.Fatal("actual exit model not sealed")
	}
	if err := record.ObserveQualifiedOwnedExit(state.Identity, 143, final); err != nil {
		t.Fatal(err)
	}
	if err := record.ObserveQualifiedOwnedExit(state.Identity, 143, initial); !errors.Is(err, domain.ErrChanged) {
		t.Fatal("sealed final model replaced")
	}
}

func TestRecoveryBindingRejectsIncompleteOrUnsafeBookkeeping(t *testing.T) {
	binding := recoveryBindingFixture(t)
	for _, tc := range []struct {
		name string
		edit func(*domain.RecoveryBindingState)
	}{
		{"invalid identity", func(s *domain.RecoveryBindingState) { s.Identity = domain.LaunchIdentity{} }},
		{"missing target", func(s *domain.RecoveryBindingState) { s.Target = domain.Target{} }},
		{"missing profile", func(s *domain.RecoveryBindingState) { s.Profile = domain.HostProfile{} }},
		{"missing process", func(s *domain.RecoveryBindingState) { s.PID = 0 }},
		{"empty native id", func(s *domain.RecoveryBindingState) { s.NativeSessionID = "" }},
		{"native path injection", func(s *domain.RecoveryBindingState) { s.NativeSessionID = "../session" }},
		{"relative history", func(s *domain.RecoveryBindingState) { s.NativeHistoryPath = "relative" }},
		{"missing namespace", func(s *domain.RecoveryBindingState) { s.NativeNamespaceWitness = "" }},
		{"noncanonical namespace", func(s *domain.RecoveryBindingState) { s.NativeNamespaceWitness = strings.Repeat("A", 64) }},
		{"missing model", func(s *domain.RecoveryBindingState) { s.Model = "" }},
		{"missing provider", func(s *domain.RecoveryBindingState) { s.Provider = "" }},
		{"body in model", func(s *domain.RecoveryBindingState) { s.Model = "model\nbody" }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := binding.Snapshot()
			tc.edit(&s)
			if _, err := domain.RecoveryBindingOf(s); !errors.Is(err, domain.ErrInvalid) {
				t.Fatalf("invalid binding accepted: %v", err)
			}
		})
	}
	s := binding.Snapshot()
	s.Model = "changed"
	if binding.Snapshot().Model != "synthetic-model" {
		t.Fatal("snapshot changed the original binding")
	}
}

func TestRecoveryConversationKeyDoesNotAllowProfileOrModelAliases(t *testing.T) {
	binding := recoveryBindingFixture(t)
	state := binding.Snapshot()
	profileState := state.Profile.Snapshot()
	profileState.ID = "another-profile"
	profileState.Version = "another-version"
	profile, err := domain.HostProfileFrom(profileState)
	if err != nil {
		t.Fatal(err)
	}
	target, err := domain.TargetOf(state.Target.Worktree(), profileState.ID)
	if err != nil {
		t.Fatal(err)
	}
	state.Profile, state.Target = profile, target
	state.Model, state.Provider = "another-model", "another-provider"
	alias, err := domain.RecoveryBindingOf(state)
	if err != nil {
		t.Fatal(err)
	}
	if binding.ConversationKey() == "" || alias.ConversationKey() != binding.ConversationKey() {
		t.Fatal("selection aliases bypassed the same conversation reservation")
	}
	if (domain.RecoveryBinding{}).ConversationKey() != "" {
		t.Fatal("zero metadata acquired a conversation key")
	}
}

func TestRecoveryRecordRequiresAnExitObservedForTheExactOwnedRun(t *testing.T) {
	binding := recoveryBindingFixture(t)
	record, err := domain.NewRecoveryRecord(binding)
	if err != nil {
		t.Fatal(err)
	}
	if record.Snapshot().ExitObserved {
		t.Fatal("a saved PID was treated as an observed exit")
	}
	before := record.Snapshot()
	foreign := binding.Snapshot().Identity
	foreign.Generation = strings.Repeat("4", 32)
	if err := record.ObserveOwnedExit(foreign, 0); !errors.Is(err, domain.ErrChanged) || record.Snapshot() != before {
		t.Fatal("foreign exit changed the record")
	}
	if err := record.ObserveOwnedExit(binding.Snapshot().Identity, -1); err != nil {
		t.Fatal(err)
	}
	if !record.Snapshot().ExitObserved || record.Snapshot().ExitCode != -1 {
		t.Fatal("signaled owned exit was lost")
	}
	before = record.Snapshot()
	if err := record.ObserveOwnedExit(binding.Snapshot().Identity, -1); err != nil || record.Snapshot() != before {
		t.Fatal("identical exit was not idempotent")
	}
	if err := record.ObserveOwnedExit(binding.Snapshot().Identity, 0); !errors.Is(err, domain.ErrChanged) || record.Snapshot() != before {
		t.Fatal("observed exit was rewritten")
	}
	copyRecord, err := domain.RecoveryRecordFrom(record.Snapshot())
	if err != nil || copyRecord.Snapshot() != record.Snapshot() {
		t.Fatal("record reconstruction changed bookkeeping")
	}
	for _, state := range []domain.RecoveryRecordState{
		{Binding: binding, ExitCode: 1}, {Binding: binding, ExitObserved: true, ExitCode: -2}, {Binding: binding, ExitObserved: true, ExitCode: 256},
	} {
		if _, err := domain.RecoveryRecordFrom(state); !errors.Is(err, domain.ErrInvalid) {
			t.Fatalf("inconsistent exit accepted: %v", err)
		}
	}
}

func TestResumeAttemptNeverAcceptsAPIDInPlaceOfTheOwnedExitReceipt(t *testing.T) {
	binding := recoveryBindingFixture(t)
	record, err := domain.NewRecoveryRecord(binding)
	if err != nil {
		t.Fatal(err)
	}
	next := domain.LaunchIdentity{Run: strings.Repeat("4", 32), Generation: strings.Repeat("5", 32), Epoch: binding.Snapshot().Identity.Epoch}
	if _, err := domain.NewResumeAttempt("request-1", record, next); !errors.Is(err, domain.ErrChanged) {
		t.Fatalf("unobserved managed exit accepted: %v", err)
	}
	if err := record.ObserveOwnedExit(binding.Snapshot().Identity, 0); err != nil {
		t.Fatal(err)
	}
	for _, invalid := range []domain.LaunchIdentity{{}, binding.Snapshot().Identity,
		{Run: binding.Snapshot().Identity.Run, Generation: next.Generation, Epoch: next.Epoch},
		{Run: next.Run, Generation: binding.Snapshot().Identity.Generation, Epoch: next.Epoch},
	} {
		if _, err := domain.NewResumeAttempt("request-1", record, invalid); !errors.Is(err, domain.ErrInvalid) {
			t.Fatalf("reused successor identity accepted: %v", err)
		}
	}
	if _, err := domain.NewResumeAttempt("../request", record, next); !errors.Is(err, domain.ErrInvalid) {
		t.Fatalf("unsafe request accepted: %v", err)
	}
}

func TestResumeAttemptPreservesUnknownAndConfirmsOnlyTheSameConversationAndTarget(t *testing.T) {
	binding := recoveryBindingFixture(t)
	record, err := domain.NewRecoveryRecord(binding)
	if err != nil {
		t.Fatal(err)
	}
	if err := record.ObserveOwnedExit(binding.Snapshot().Identity, 0); err != nil {
		t.Fatal(err)
	}
	next := domain.LaunchIdentity{Run: strings.Repeat("4", 32), Generation: strings.Repeat("5", 32), Epoch: binding.Snapshot().Identity.Epoch}
	attempt, err := domain.NewResumeAttempt("request-1", record, next)
	if err != nil {
		t.Fatal(err)
	}
	successorState := binding.Snapshot()
	successorState.Identity = next
	// PID reuse is diagnostic only; the positive owned-exit receipt is required.
	successorState.PID = binding.Snapshot().PID
	successor, err := domain.RecoveryBindingOf(successorState)
	if err != nil {
		t.Fatal(err)
	}
	before := attempt.Snapshot()
	if err := attempt.Confirm(successor); !errors.Is(err, domain.ErrChanged) || attempt.Snapshot() != before {
		t.Fatal("spawn was inferred from an unissued reservation")
	}
	if err := attempt.MarkStarted(); err != nil {
		t.Fatal(err)
	}
	if err := attempt.MarkUnknown(); err != nil {
		t.Fatal(err)
	}
	before = attempt.Snapshot()
	if err := attempt.MarkStarted(); !errors.Is(err, domain.ErrChanged) || attempt.Snapshot() != before {
		t.Fatal("unknown attempt could be restarted")
	}
	if err := attempt.FailBeforeStart(); !errors.Is(err, domain.ErrChanged) || attempt.Snapshot() != before {
		t.Fatal("unknown attempt was rearmed as not started")
	}
	for _, tc := range []struct {
		name string
		edit func(*domain.RecoveryBindingState)
	}{
		{"different conversation", func(s *domain.RecoveryBindingState) { s.NativeSessionID = "another-session" }},
		{"different history", func(s *domain.RecoveryBindingState) { s.NativeHistoryPath = "/another/session.jsonl" }},
		{"different namespace", func(s *domain.RecoveryBindingState) { s.NativeNamespaceWitness = strings.Repeat("b", 64) }},
		{"different model", func(s *domain.RecoveryBindingState) { s.Model = "fallback-model" }},
		{"different provider", func(s *domain.RecoveryBindingState) { s.Provider = "fallback-provider" }},
		{"different target", func(s *domain.RecoveryBindingState) {
			worktree, err := domain.WorktreeOf(s.Target.Worktree().Project(), "/another", "/project/.git", "/another-store.db", "another-witness")
			if err != nil {
				t.Fatal(err)
			}
			target, err := domain.TargetOf(worktree, string(s.Profile.ID()))
			if err != nil {
				t.Fatal(err)
			}
			s.Target = target
		}},
		{"different source version", func(s *domain.RecoveryBindingState) {
			profile := s.Profile.Snapshot()
			profile.Version = "different version"
			value, err := domain.HostProfileFrom(profile)
			if err != nil {
				t.Fatal(err)
			}
			s.Profile = value
		}},
		{"old selection", func(s *domain.RecoveryBindingState) { s.Identity = binding.Snapshot().Identity }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			state := successor.Snapshot()
			tc.edit(&state)
			foreign, err := domain.RecoveryBindingOf(state)
			if err != nil {
				t.Fatal(err)
			}
			if err := attempt.Confirm(foreign); !errors.Is(err, domain.ErrChanged) || attempt.Snapshot() != before {
				t.Fatal("mismatch changed the uncertain outcome")
			}
		})
	}
	if err := attempt.Confirm(successor); err != nil {
		t.Fatal(err)
	}
	if attempt.Snapshot().Phase != domain.ResumeConfirmed || attempt.Snapshot().Confirmed != successor {
		t.Fatal("actual successor metadata was not retained")
	}
	confirmed := attempt.Snapshot()
	if err := attempt.Confirm(successor); err != nil || attempt.Snapshot() != confirmed {
		t.Fatal("confirmation replay changed the outcome")
	}
	if err := attempt.MarkUnknown(); !errors.Is(err, domain.ErrChanged) || attempt.Snapshot() != confirmed {
		t.Fatal("confirmed result was changed")
	}
	loaded, err := domain.ResumeAttemptFrom(confirmed)
	if err != nil || loaded.Snapshot() != confirmed {
		t.Fatal("attempt reconstruction changed the outcome")
	}
	for _, typ := range []reflect.Type{reflect.TypeFor[domain.RecoveryBindingState](), reflect.TypeFor[domain.RecoveryRecordState](), reflect.TypeFor[domain.ResumeAttemptState]()} {
		for _, field := range []string{"Data", "Input", "Prompt", "Command", "Env", "Approval"} {
			if _, exists := typ.FieldByName(field); exists {
				t.Fatalf("recovery acquired a replay field: %s", field)
			}
		}
	}
}

func TestResumeAttemptPreservesProvenPreStartFailureAndRejectsMalformedReloads(t *testing.T) {
	binding := recoveryBindingFixture(t)
	record, err := domain.NewRecoveryRecord(binding)
	if err != nil {
		t.Fatal(err)
	}
	if err := record.ObserveOwnedExit(binding.Snapshot().Identity, 0); err != nil {
		t.Fatal(err)
	}
	next := domain.LaunchIdentity{Run: strings.Repeat("4", 32), Generation: strings.Repeat("5", 32), Epoch: binding.Snapshot().Identity.Epoch}
	attempt, err := domain.NewResumeAttempt("request-1", record, next)
	if err != nil {
		t.Fatal(err)
	}
	reserved := attempt.Snapshot()
	loaded, err := domain.ResumeAttemptFrom(reserved)
	if err != nil || loaded.Snapshot() != reserved {
		t.Fatal("reload invented an ownership or outcome transition")
	}
	if err := attempt.FailBeforeStart(); err != nil {
		t.Fatal(err)
	}
	failed := attempt.Snapshot()
	if err := attempt.FailBeforeStart(); err != nil || attempt.Snapshot() != failed {
		t.Fatal("same no-start result was not stable")
	}
	if err := attempt.MarkStarted(); !errors.Is(err, domain.ErrChanged) || attempt.Snapshot() != failed {
		t.Fatal("failed request could execute again")
	}
	if err := attempt.MarkUnknown(); !errors.Is(err, domain.ErrChanged) || attempt.Snapshot() != failed {
		t.Fatal("known no-start result was rewritten")
	}
	for _, tc := range []struct {
		name string
		edit func(*domain.ResumeAttemptState)
	}{
		{"unknown phase", func(s *domain.ResumeAttemptState) { s.Phase = "future" }},
		{"bad request", func(s *domain.ResumeAttemptState) { s.RequestID = "" }},
		{"old identity", func(s *domain.ResumeAttemptState) { s.Next = binding.Snapshot().Identity }},
		{"bad exit", func(s *domain.ResumeAttemptState) { s.PredecessorExitCode = -2 }},
		{"invented confirmation", func(s *domain.ResumeAttemptState) { s.Phase = domain.ResumeConfirmed }},
		{"hidden native data", func(s *domain.ResumeAttemptState) { s.Confirmed = binding }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := reserved
			tc.edit(&s)
			if _, err := domain.ResumeAttemptFrom(s); !errors.Is(err, domain.ErrInvalid) {
				t.Fatalf("malformed reload accepted: %v", err)
			}
		})
	}
}

func TestRecoveryZeroReceiversDoNotPanicOrGrantAuthority(t *testing.T) {
	var record *domain.RecoveryRecord
	var attempt *domain.ResumeAttempt
	if record.Snapshot() != (domain.RecoveryRecordState{}) || attempt.Snapshot() != (domain.ResumeAttemptState{}) {
		t.Fatal("nil receiver returned invented metadata")
	}
	for _, call := range []func() error{
		func() error { return record.ObserveOwnedExit(domain.LaunchIdentity{}, 0) },
		attempt.MarkStarted, attempt.MarkUnknown, attempt.FailBeforeStart,
		func() error { return attempt.Confirm(domain.RecoveryBinding{}) },
	} {
		if err := call(); !errors.Is(err, domain.ErrInvalid) {
			t.Fatalf("nil receiver accepted operation: %v", err)
		}
	}
}
