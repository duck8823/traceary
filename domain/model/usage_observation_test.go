package model_test

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/duck8823/traceary/domain/model"
	"github.com/duck8823/traceary/domain/types"
)

func TestUsageObservation_ReconcileFinalizesPendingOnce(t *testing.T) {
	t.Parallel()

	descriptor := usageDescriptor(t, "usage-1")
	pending, err := model.NewPendingUsageObservation(descriptor)
	if err != nil {
		t.Fatalf("NewPendingUsageObservation() error = %v", err)
	}
	finalized := finalizedUsageObservation(t, descriptor, 0)

	transition, err := pending.Reconcile(finalized)
	if err != nil {
		t.Fatalf("Reconcile(finalized) error = %v", err)
	}
	if transition != model.UsageObservationTransitionApplied || pending.Status() != types.UsageObservationFinalized {
		t.Fatalf("transition/status = %q/%q", transition, pending.Status())
	}
	input, ok := pending.Counters().Input().Value()
	if !ok || input != 0 {
		t.Fatalf("final input = %d, present %v", input, ok)
	}

	transition, err = pending.Reconcile(finalized)
	if err != nil {
		t.Fatalf("Reconcile(replay) error = %v", err)
	}
	if transition != model.UsageObservationTransitionAlreadyApplied {
		t.Fatalf("replay transition = %q", transition)
	}
}

func TestUsageObservationDescriptorRunIdentityIsExplicitAndSemantic(t *testing.T) {
	t.Parallel()
	id, _ := types.UsageObservationIDFrom("usage-run")
	sessionID, _ := types.SessionIDFrom("session-1")
	source, _ := types.UsageSourceOf("codex", "local", "1", "", "")
	runIdentity, _ := types.RunIdentityFrom("codex", "run-1")
	descriptor, err := model.NewUsageObservationDescriptorWithRunIdentity(id, sessionID, source, types.UsageScopeCall, types.UsageAccountingAdditive, time.Date(2026, 7, 23, 12, 0, 0, 0, time.UTC), runIdentity)
	if err != nil {
		t.Fatal(err)
	}
	stored, present := descriptor.RunIdentity().Value()
	if !present || stored != runIdentity {
		t.Fatalf("run identity = %#v, present=%v", stored, present)
	}
	legacy, err := model.NewUsageObservationDescriptor(id, sessionID, source, types.UsageScopeCall, types.UsageAccountingAdditive, time.Date(2026, 7, 23, 12, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	legacyObservation, _ := model.NewPendingUsageObservation(legacy)
	runObservation, _ := model.NewPendingUsageObservation(descriptor)
	if _, err := legacyObservation.Reconcile(runObservation); !errors.Is(err, model.ErrConflictingUsageObservation) {
		t.Fatalf("run attribution conflict error = %v", err)
	}
	otherHost, _ := types.RunIdentityFrom("claude", "run-1")
	if _, err := model.NewUsageObservationDescriptorWithRunIdentity(id, sessionID, source, types.UsageScopeCall, types.UsageAccountingAdditive, time.Now(), otherHost); err == nil {
		t.Fatal("cross-host attribution accepted")
	}
}

func TestUsageObservation_ReconcileRejectsConflictingTerminalData(t *testing.T) {
	t.Parallel()

	descriptor := usageDescriptor(t, "usage-conflict")
	current := finalizedUsageObservation(t, descriptor, 1)
	proposed := finalizedUsageObservation(t, descriptor, 2)

	_, err := current.Reconcile(proposed)
	if err == nil {
		t.Fatal("Reconcile(conflict) error = nil")
	}
	if !errors.Is(err, model.ErrConflictingUsageObservation) {
		t.Fatalf("Reconcile(conflict) error = %v", err)
	}
	var conflict *model.UsageObservationConflictError
	if !errors.As(err, &conflict) || conflict.ObservationID() != descriptor.ObservationID() {
		t.Fatalf("Reconcile(conflict) error type/id = %T/%q", err, conflict.ObservationID())
	}
	input, _ := current.Counters().Input().Value()
	if input != 1 {
		t.Fatalf("conflict mutated input = %d", input)
	}
}

func TestUsageObservation_ReconcileRejectsDifferentTerminalTimestamp(t *testing.T) {
	t.Parallel()

	descriptor := usageDescriptor(t, "usage-time-conflict")
	current := finalizedUsageObservation(t, descriptor, 1)
	proposed, err := model.NewFinalizedUsageObservation(
		descriptor,
		current.Counters(),
		current.Cost(),
		types.UsageTerminalSuccess,
		time.Date(2026, 7, 23, 12, 2, 0, 0, time.UTC),
	)
	if err != nil {
		t.Fatal(err)
	}

	if _, err := current.Reconcile(proposed); !errors.Is(err, model.ErrConflictingUsageObservation) {
		t.Fatalf("Reconcile(different finalized_at) error = %v", err)
	}
	finalizedAt, _ := current.FinalizedAt().Value()
	if !finalizedAt.Equal(time.Date(2026, 7, 23, 12, 1, 0, 0, time.UTC)) {
		t.Fatalf("conflict mutated finalized_at = %v", finalizedAt)
	}
}

func TestUsageObservation_finalizedRejectsUnknownUsage(t *testing.T) {
	t.Parallel()

	descriptor := usageDescriptor(t, "usage-unknown")
	_, err := model.NewFinalizedUsageObservation(
		descriptor,
		types.UnknownUsageCounters(),
		types.UnavailableUsageCost(),
		types.UsageTerminalSuccess,
		time.Date(2026, 7, 23, 12, 1, 0, 0, time.UTC),
	)
	if err == nil {
		t.Fatal("NewFinalizedUsageObservation(unknown) error = nil")
	}
}

func TestUsageObservation_snapshotMustBeFinalized(t *testing.T) {
	t.Parallel()

	source, err := types.UsageSourceOf("antigravity", "statusline", "1.1.5", "google", "gemini-2.5-pro")
	if err != nil {
		t.Fatal(err)
	}
	id, err := types.UsageObservationIDFrom("snapshot-1")
	if err != nil {
		t.Fatal(err)
	}
	descriptor, err := model.NewUsageSnapshotDescriptor(
		id,
		types.SessionID("conversation-1"),
		source,
		"antigravity:conversation-1:gemini-2.5-pro",
		1,
		types.None[types.UsageObservationID](),
		time.Date(2026, 7, 23, 12, 0, 0, 0, time.UTC),
	)
	if err != nil {
		t.Fatalf("NewUsageSnapshotDescriptor() error = %v", err)
	}
	if _, err := model.NewPendingUsageObservation(descriptor); err == nil {
		t.Fatal("NewPendingUsageObservation(snapshot) error = nil")
	}
}

func TestUsageObservation_SnapshotSuccessorRequiresSameSessionAndSource(t *testing.T) {
	t.Parallel()

	ts := time.Date(2026, 7, 23, 12, 0, 0, 0, time.UTC)
	source, err := types.UsageSourceOf("antigravity", "statusline", "1.1.5", "google", "model-1")
	if err != nil {
		t.Fatal(err)
	}
	rootID, _ := types.UsageObservationIDFrom("snapshot-lineage-root")
	rootDescriptor, err := model.NewUsageSnapshotDescriptor(
		rootID, types.SessionID("session-1"), source, "snapshot-series", 1,
		types.None[types.UsageObservationID](), ts,
	)
	if err != nil {
		t.Fatal(err)
	}
	makeFinalized := func(descriptor model.UsageObservationDescriptor) *model.UsageObservation {
		unavailable := types.UnavailableUsageValue()
		counters, err := types.UsageCountersOf(unavailable, unavailable, unavailable, unavailable, unavailable, unavailable)
		if err != nil {
			t.Fatal(err)
		}
		observation, err := model.NewFinalizedUsageObservation(
			descriptor, counters, types.UnavailableUsageCost(), types.UsageTerminalSuccess, descriptor.ObservedAt().Add(time.Second),
		)
		if err != nil {
			t.Fatal(err)
		}
		return observation
	}
	root := makeFinalized(rootDescriptor)

	otherSource, err := types.UsageSourceOf("antigravity", "statusline", "1.1.6", "google", "model-1")
	if err != nil {
		t.Fatal(err)
	}
	for _, tt := range []struct {
		name      string
		sessionID types.SessionID
		source    types.UsageSource
	}{
		{name: "different session", sessionID: types.SessionID("session-2"), source: source},
		{name: "different source", sessionID: types.SessionID("session-1"), source: otherSource},
	} {
		t.Run(tt.name, func(t *testing.T) {
			successorID, _ := types.UsageObservationIDFrom("snapshot-" + strings.ReplaceAll(tt.name, " ", "-"))
			descriptor, err := model.NewUsageSnapshotDescriptor(
				successorID, tt.sessionID, tt.source, "snapshot-series", 2, types.Some(rootID), ts.Add(time.Minute),
			)
			if err != nil {
				t.Fatal(err)
			}
			if err := makeFinalized(descriptor).ValidateSnapshotSuccessor(root); !errors.Is(err, model.ErrConflictingUsageObservation) {
				t.Fatalf("ValidateSnapshotSuccessor() error = %v", err)
			}
		})
	}
}

func usageDescriptor(t *testing.T, value string) model.UsageObservationDescriptor {
	t.Helper()
	source, err := types.UsageSourceOf("codex", "headless_stream", "0.145.0", "openai", "gpt-5.6")
	if err != nil {
		t.Fatal(err)
	}
	id, err := types.UsageObservationIDFrom(value)
	if err != nil {
		t.Fatal(err)
	}
	descriptor, err := model.NewUsageObservationDescriptor(
		id,
		types.SessionID("session-1"),
		source,
		types.UsageScopeCall,
		types.UsageAccountingAdditive,
		time.Date(2026, 7, 23, 12, 0, 0, 0, time.UTC),
	)
	if err != nil {
		t.Fatal(err)
	}
	return descriptor
}

func finalizedUsageObservation(t *testing.T, descriptor model.UsageObservationDescriptor, inputTokens int64) *model.UsageObservation {
	t.Helper()
	input, err := types.KnownUsageValue(inputTokens)
	if err != nil {
		t.Fatal(err)
	}
	zero, err := types.KnownUsageValue(0)
	if err != nil {
		t.Fatal(err)
	}
	counters, err := types.UsageCountersOf(input, zero, zero, zero, zero, input)
	if err != nil {
		t.Fatal(err)
	}
	observation, err := model.NewFinalizedUsageObservation(
		descriptor,
		counters,
		types.UnavailableUsageCost(),
		types.UsageTerminalSuccess,
		time.Date(2026, 7, 23, 12, 1, 0, 0, time.UTC),
	)
	if err != nil {
		t.Fatal(err)
	}
	return observation
}
