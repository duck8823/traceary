//go:build darwin || linux

package runtime

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	domain "github.com/duck8823/traceary/domain/runtime"
)

func TestOwnedNativeMetadataFinishUsesLatestInvalidationNotEarlierCache(t *testing.T) {
	plan, env, data := nativeStartFixture()
	ctx := context.Background()
	slot, err := newNativeStartSlot(ctx, plan)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = slot.close(ctx) }()
	sink := NativeSessionMarkerSink{hints: env}
	if err := sink.Observe(ctx, slot.metadata.root, bytes.NewReader(data)); err != nil {
		t.Fatal(err)
	}
	owned := &ownedClaudeRecovery{slot: slot}
	if _, err := owned.event(ctx); err != nil {
		t.Fatal(err)
	}
	changed := bytes.Replace(data, []byte("\"source\":\"resume\""), []byte("\"source\":\"clear\""), 1)
	if err := sink.Observe(ctx, slot.metadata.root, bytes.NewReader(changed)); !errors.Is(err, domain.ErrChanged) {
		t.Fatal("latest invalidation absent")
	}
	// This method is called by the actual Wait owner in production. This test
	// checks metadata lifetime only and does not attest an OS process exit.
	owned.finish()
	if _, err := owned.event(ctx); !errors.Is(err, domain.ErrChanged) {
		t.Fatalf("earlier cached binding survived: %v", err)
	}
	if _, err := os.Stat(slot.metadata.root); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("own consumed slot not cleaned")
	}
	owned.finish()
	if _, err := owned.event(ctx); !errors.Is(err, domain.ErrChanged) {
		t.Fatal("repeated finish rearmed invalidated source")
	}
}

func TestOwnedNativeMetadataFinishRetainsOnlyFinalReducedEventAndNoLateWriter(t *testing.T) {
	plan, env, data := nativeStartFixture()
	ctx := context.Background()
	slot, err := newNativeStartSlot(ctx, plan)
	if err != nil {
		t.Fatal(err)
	}
	sink := NativeSessionMarkerSink{hints: env}
	if err := sink.Observe(ctx, slot.metadata.root, bytes.NewReader(data)); err != nil {
		t.Fatal(err)
	}
	owned := &ownedClaudeRecovery{slot: slot}
	owned.finish()
	event, err := owned.event(ctx)
	if err != nil || event.Plan != plan {
		t.Fatalf("final reduced event lost: %+v %v", event, err)
	}
	if err := sink.Observe(ctx, slot.metadata.root, bytes.NewReader(data)); err == nil {
		t.Fatal("late callback recreated a consumed intent")
	}
	if _, err := os.Stat(slot.metadata.root); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("late callback recreated root")
	}
	again, err := owned.event(ctx)
	if err != nil || again != event {
		t.Fatal("late callback changed final event")
	}
}

func TestOwnedNativeMetadataMissingOrUnknownFilesNeverBecomeBindingEvidence(t *testing.T) {
	for _, kind := range []string{"missing marker", "unknown file"} {
		t.Run(kind, func(t *testing.T) {
			plan, env, data := nativeStartFixture()
			ctx := context.Background()
			slot, err := newNativeStartSlot(ctx, plan)
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = os.RemoveAll(slot.metadata.root) }() // own generated test root only
			if kind == "unknown file" {
				if err := (NativeSessionMarkerSink{hints: env}).Observe(ctx, slot.metadata.root, bytes.NewReader(data)); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(slot.metadata.root, "foreign"), []byte("keep"), 0600); err != nil {
					t.Fatal(err)
				}
			}
			owned := &ownedClaudeRecovery{slot: slot}
			owned.finish()
			if _, err := owned.event(ctx); err == nil {
				t.Fatal("missing/uncertain final evidence accepted")
			}
			if kind == "unknown file" {
				if _, err := os.Stat(filepath.Join(slot.metadata.root, "foreign")); err != nil {
					t.Fatal("unrecognized file removed")
				}
			}
		})
	}
	if _, err := (&ptyChild{}).RecoveryBinding(context.Background()); !errors.Is(err, domain.ErrUnsupported) {
		t.Fatal("ordinary unqualified child gained native authority")
	}
}
