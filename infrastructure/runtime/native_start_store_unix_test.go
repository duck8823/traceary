//go:build darwin || linux

package runtime

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	domain "github.com/duck8823/traceary/domain/runtime"
)

func TestNativeStartSlotCapturesOnlyItsOwnReducedMetadataAndInvalidatesChanges(t *testing.T) {
	plan, env, data := nativeStartFixture()
	ctx := context.Background()
	slot, err := newNativeStartSlot(ctx, plan)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = slot.close(ctx) }()
	if _, err := slot.read(ctx); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("absent callback became evidence: %v", err)
	}
	sink := NativeSessionMarkerSink{hints: env}
	if err := sink.Observe(ctx, slot.metadata.root, bytes.NewReader(data)); err != nil {
		t.Fatal(err)
	}
	event, err := slot.read(ctx)
	if err != nil || event.Plan != plan {
		t.Fatalf("own callback lost: %+v %v", event, err)
	}
	info, err := os.Stat(filepath.Join(slot.metadata.root, "source.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := sink.Observe(ctx, slot.metadata.root, bytes.NewReader(data)); err != nil {
		t.Fatal(err)
	}
	again, err := os.Stat(filepath.Join(slot.metadata.root, "source.json"))
	if err != nil || !os.SameFile(info, again) {
		t.Fatal("duplicate callback rewrote source")
	}
	compact := bytes.Replace(data, []byte(`"source":"resume"`), []byte(`"source":"compact"`), 1)
	if err := sink.Observe(ctx, slot.metadata.root, bytes.NewReader(compact)); err != nil {
		t.Fatalf("same conversation compaction invalidated bound source: %v", err)
	}
	if _, err := slot.read(ctx); err != nil {
		t.Fatal(err)
	}
	changed := bytes.Replace(data, []byte(`"source":"resume"`), []byte(`"source":"clear"`), 1)
	if err := sink.Observe(ctx, slot.metadata.root, bytes.NewReader(changed)); !errors.Is(err, domain.ErrChanged) {
		t.Fatalf("conversation change ignored: %v", err)
	}
	if _, err := slot.read(ctx); !errors.Is(err, domain.ErrChanged) {
		t.Fatalf("old binding survived identity change: %v", err)
	}
	if err := sink.Observe(ctx, slot.metadata.root, bytes.NewReader(data)); !errors.Is(err, domain.ErrChanged) {
		t.Fatal("invalid source rearmed")
	}
}
func TestNativeStartSlotForeignHintsCannotPoisonAnotherRun(t *testing.T) {
	plan, env, data := nativeStartFixture()
	ctx := context.Background()
	slot, err := newNativeStartSlot(ctx, plan)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = slot.close(ctx) }()
	env["TRACEARY_RUNTIME_RUN_ID"] = strings.Repeat("9", 32)
	if err := (NativeSessionMarkerSink{hints: env}).Observe(ctx, slot.metadata.root, bytes.NewReader(data)); !errors.Is(err, domain.ErrChanged) {
		t.Fatalf("foreign hint accepted: %v", err)
	}
	if _, err := slot.read(ctx); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("foreign callback mutated source: %v", err)
	}
}
func TestNativeStartSlotNoArbitraryWriterAndPrivateFileGuards(t *testing.T) {
	plan, env, data := nativeStartFixture()
	ctx := context.Background()
	sink := NativeSessionMarkerSink{hints: env}
	if err := sink.Observe(ctx, t.TempDir(), bytes.NewReader(data)); err == nil {
		t.Fatal("arbitrary root accepted")
	}
	for _, kind := range []string{"intent symlink", "intent hardlink", "public intent", "marker symlink"} {
		t.Run(kind, func(t *testing.T) {
			slot, err := newNativeStartSlot(ctx, plan)
			if err != nil {
				t.Fatal(err)
			}
			root := slot.metadata.root
			t.Cleanup(func() { _ = os.RemoveAll(root) }) // generated test root only
			intent := filepath.Join(root, "intent.json")
			other := filepath.Join(t.TempDir(), "other")
			switch kind {
			case "intent symlink":
				if err := os.Rename(intent, other); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(other, intent); err != nil {
					t.Fatal(err)
				}
			case "intent hardlink":
				if err := os.Link(intent, other); err != nil {
					t.Fatal(err)
				}
			case "public intent":
				if err := os.Chmod(intent, 0644); err != nil {
					t.Fatal(err)
				}
			case "marker symlink":
				if err := os.WriteFile(other, []byte("untouched"), 0600); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(other, filepath.Join(root, "source.json")); err != nil {
					t.Fatal(err)
				}
			}
			if err := sink.Observe(ctx, root, bytes.NewReader(data)); err == nil {
				t.Fatal("unsafe file accepted")
			}
			if kind == "marker symlink" {
				got, err := os.ReadFile(other)
				if err != nil || string(got) != "untouched" {
					t.Fatal("arbitrary file overwritten")
				}
			}
		})
	}
}
func TestNativeStartSlotCleanupDoesNotRemoveUnrecognizedFiles(t *testing.T) {
	plan, _, _ := nativeStartFixture()
	ctx := context.Background()
	slot, err := newNativeStartSlot(ctx, plan)
	if err != nil {
		t.Fatal(err)
	}
	root := slot.metadata.root
	foreign := filepath.Join(root, "unrecognized")
	if err := os.WriteFile(foreign, []byte("keep"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := slot.close(ctx); err == nil {
		t.Fatal("blind recursive cleanup accepted")
	}
	got, err := os.ReadFile(foreign)
	if err != nil || string(got) != "keep" {
		t.Fatal("unknown file removed")
	}
	_ = os.Remove(foreign)
	if err := slot.close(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(root); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("own empty root not removed: %v", err)
	}
}

func TestNativeStartSlotInvalidInitialMetadataNeverEstablishesBinding(t *testing.T) {
	for _, kind := range []string{"compact", "oversize", "truncated", "foreign session"} {
		t.Run(kind, func(t *testing.T) {
			plan, env, data := nativeStartFixture()
			ctx := context.Background()
			slot, err := newNativeStartSlot(ctx, plan)
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = slot.close(ctx) }()
			switch kind {
			case "compact":
				data = bytes.Replace(data, []byte(`"source":"resume"`), []byte(`"source":"compact"`), 1)
			case "oversize":
				data = bytes.Repeat([]byte(" "), int(maxNativeStartBytes)+1)
			case "truncated":
				data = data[:len(data)-1]
			case "foreign session":
				data = bytes.ReplaceAll(data, []byte(plan.SessionID), []byte("12345678-1234-4234-8234-123456789def"))
			}
			sink := NativeSessionMarkerSink{hints: env}
			if err := sink.Observe(ctx, slot.metadata.root, bytes.NewReader(data)); err == nil {
				t.Fatal("invalid first observation accepted")
			}
			if _, err := slot.read(ctx); !errors.Is(err, domain.ErrChanged) {
				t.Fatalf("invalid source not sticky: %v", err)
			}
			_, _, valid := nativeStartFixture()
			if err := sink.Observe(ctx, slot.metadata.root, bytes.NewReader(valid)); !errors.Is(err, domain.ErrChanged) {
				t.Fatalf("invalid source rearmed: %v", err)
			}
		})
	}
}

func TestNativeStartSlotCancellationDoesNotMutateBoundSource(t *testing.T) {
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
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	changed := bytes.Replace(data, []byte(`"source":"resume"`), []byte(`"source":"clear"`), 1)
	if err := sink.Observe(canceled, slot.metadata.root, bytes.NewReader(changed)); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation ignored: %v", err)
	}
	if err := slot.close(canceled); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled cleanup accepted: %v", err)
	}
	if _, err := slot.read(ctx); err != nil {
		t.Fatalf("canceled operation changed source: %v", err)
	}
}

func TestNativeStartSlotExchangedRootAndChangedIntentCannotBeCleaned(t *testing.T) {
	for _, kind := range []string{"root", "intent"} {
		t.Run(kind, func(t *testing.T) {
			plan, _, _ := nativeStartFixture()
			ctx := context.Background()
			slot, err := newNativeStartSlot(ctx, plan)
			if err != nil {
				t.Fatal(err)
			}
			root := slot.metadata.root
			t.Cleanup(func() { _ = os.RemoveAll(root) })
			if kind == "root" {
				moved := root + "-original"
				if err := os.Rename(root, moved); err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { _ = os.RemoveAll(moved) })
				if err := os.Mkdir(root, 0700); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(root, "intent.json"), []byte("replacement"), 0600); err != nil {
					t.Fatal(err)
				}
			} else {
				path := filepath.Join(root, "intent.json")
				data, err := os.ReadFile(path)
				if err != nil {
					t.Fatal(err)
				}
				data = bytes.ReplaceAll(data, []byte(plan.Identity.Run), []byte(strings.Repeat("9", 32)))
				if err := os.WriteFile(path, data, 0600); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := slot.read(ctx); !errors.Is(err, domain.ErrChanged) {
				t.Fatalf("exchanged evidence accepted: %v", err)
			}
			if err := slot.close(ctx); !errors.Is(err, domain.ErrChanged) {
				t.Fatalf("exchanged metadata cleanup accepted: %v", err)
			}
			if _, err := os.Stat(filepath.Join(root, "intent.json")); err != nil {
				t.Fatalf("unowned metadata removed: %v", err)
			}
		})
	}
}

func TestNativeStartSlotConcurrentCallbacksCannotRearmInvalidatedSource(t *testing.T) {
	plan, env, data := nativeStartFixture()
	ctx := context.Background()
	slot, err := newNativeStartSlot(ctx, plan)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = slot.close(ctx) }()
	sink := NativeSessionMarkerSink{hints: env}
	changed := bytes.Replace(data, []byte(`"source":"resume"`), []byte(`"source":"clear"`), 1)
	var wg sync.WaitGroup
	for i := 0; i < 12; i++ {
		wg.Add(1)
		go func(invalid bool) {
			defer wg.Done()
			payload := data
			if invalid {
				payload = changed
			}
			_ = sink.Observe(ctx, slot.metadata.root, bytes.NewReader(payload))
		}(i%3 == 0)
	}
	wg.Wait()
	if _, err := slot.read(ctx); !errors.Is(err, domain.ErrChanged) {
		t.Fatalf("concurrent valid callback rearmed source: %v", err)
	}
}

type failedNativeInput struct{}

func (failedNativeInput) Read([]byte) (int, error) { return 0, io.ErrUnexpectedEOF }

func TestNativeStartSlotIncompleteLatestInputInvalidatesEarlierEvidence(t *testing.T) {
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
	input := io.MultiReader(bytes.NewReader(data), failedNativeInput{})
	if err := sink.Observe(ctx, slot.metadata.root, input); !errors.Is(err, domain.ErrInvalid) {
		t.Fatalf("partial input accepted: %v", err)
	}
	if _, err := slot.read(ctx); !errors.Is(err, domain.ErrChanged) {
		t.Fatalf("stale evidence survived incomplete callback: %v", err)
	}
}
