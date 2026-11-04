//go:build darwin || linux

package runtime

import (
	"bytes"
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	domain "github.com/duck8823/traceary/domain/runtime"
)

func TestClaudeNativeArgvUsesOnlyPlannedConversationAndRecordedModel(t *testing.T) {
	p, _, _ := nativeStartFixture()
	slot := &nativeStartSlot{intent: nativeStartIntent{Plan: recordNativeStartPlan(p)}}
	plan := &claudeLaunchPlan{slot: slot, settings: "/private/generated/settings.json", model: "claude-opus-5-5"}
	want := []string{"--resume", p.SessionID, "--model", "claude-opus-5-5", "--settings", plan.settings}
	if !reflect.DeepEqual(plan.args(), want) {
		t.Fatalf("unexpected resume argv: %q", plan.args())
	}
	p.Source = "startup"
	slot.intent.Plan = recordNativeStartPlan(p)
	want = []string{"--session-id", p.SessionID, "--settings", plan.settings}
	if !reflect.DeepEqual(plan.args(), want) {
		t.Fatalf("unexpected normal argv: %q", plan.args())
	}
	// These methods accept no prompt/tool/approval/continue/print/fork payload.
	for _, arg := range plan.args() {
		if strings.Contains(arg, "print") || strings.Contains(arg, "continue") || strings.Contains(arg, "fork") {
			t.Fatal("alternate execution/replay mode added")
		}
	}
}

func TestClaudeNativeContinuationOverrideIsSingleAndDoesNotMutateHostEnvironment(t *testing.T) {
	original := []string{"HOME=/home/example", "CLAUDE_CODE_RESUME_INTERRUPTED_TURN=1", "TERM=xterm", "CLAUDE_CODE_RESUME_INTERRUPTED_TURN=1"}
	before := append([]string{}, original...)
	got := interruptedContinuationDisabled(original)
	want := []string{"HOME=/home/example", "TERM=xterm", "CLAUDE_CODE_RESUME_INTERRUPTED_TURN=0"}
	if !reflect.DeepEqual(got, want) || !reflect.DeepEqual(original, before) {
		t.Fatal("continuation policy duplicated or host environment mutated")
	}
}

func TestOwnedNativeConfirmationWaitsOnlyForItsOwnBoundedMetadata(t *testing.T) {
	p, env, data := nativeStartFixture()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	slot, err := newNativeStartSlot(ctx, p)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = slot.close(context.Background()) }()
	owned := &ownedClaudeRecovery{slot: slot}
	result := make(chan error, 1)
	go func() { _, err := owned.confirmedEvent(ctx); result <- err }()
	select {
	case err := <-result:
		t.Fatalf("missing startup metadata did not wait: %v", err)
	case <-time.After(35 * time.Millisecond):
	}
	if err := (NativeSessionMarkerSink{hints: env}).Observe(ctx, slot.metadata.root, bytes.NewReader(data)); err != nil {
		t.Fatal(err)
	}
	if err := <-result; err != nil {
		t.Fatal(err)
	}
	owned.finish()
	if _, err := owned.confirmedEvent(ctx); err != nil {
		t.Fatal(err)
	}
}

func TestOwnedNativeConfirmationMissingMetadataCancelsAndExitedChildDoesNotWait(t *testing.T) {
	p, _, _ := nativeStartFixture()
	slot, err := newNativeStartSlot(context.Background(), p)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = slot.close(context.Background()) }()
	owned := &ownedClaudeRecovery{slot: slot}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := owned.confirmedEvent(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("unbounded missing startup wait: %v", err)
	}
	owned.finish()
	if _, err := owned.confirmedEvent(context.Background()); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("missing final metadata became binding: %v", err)
	}
}
