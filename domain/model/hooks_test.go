package model_test

import (
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/duck8823/traceary/domain/model"
	"github.com/duck8823/traceary/domain/types"
)

func TestHooksOf(t *testing.T) {
	t.Parallel()

	command := model.HookCommandOf(
		"hook-name",
		"command",
		"echo hello",
		types.Of(1000),
		"example description",
		"example.sh:action",
	)
	entry := model.HookEntryOf(types.Of("matcher"), []model.HookCommand{command})
	hooks := model.HooksOf([]string{"Event"}, map[string][]model.HookEntry{
		"Event": {entry},
	})

	if diff := cmp.Diff([]string{"Event"}, hooks.EventOrder()); diff != "" {
		t.Fatalf("EventOrder() mismatch (-want +got):\n%s", diff)
	}

	t.Run("returns a copy when reading entries", func(t *testing.T) {
		t.Parallel()

		entries := hooks.Entries("Event")
		if got, want := len(entries), 1; got != want {
			t.Fatalf("len(entries) = %d, want %d", got, want)
		}
		got := entries[0].Commands()[0]
		if diff := cmp.Diff("echo hello", got.Command()); diff != "" {
			t.Fatalf("Command() mismatch (-want +got):\n%s", diff)
		}
		if diff := cmp.Diff("hook-name", got.Name()); diff != "" {
			t.Fatalf("Name() mismatch (-want +got):\n%s", diff)
		}
		timeout, ok := got.Timeout().Get()
		if !ok || timeout != 1000 {
			t.Fatalf("Timeout() = %d, present=%v, want 1000", timeout, ok)
		}
		if diff := cmp.Diff("example description", got.Description()); diff != "" {
			t.Fatalf("Description() mismatch (-want +got):\n%s", diff)
		}
		if diff := cmp.Diff("example.sh:action", got.ManagedKey()); diff != "" {
			t.Fatalf("ManagedKey() mismatch (-want +got):\n%s", diff)
		}
	})

	t.Run("Entries returns nil for missing events", func(t *testing.T) {
		t.Parallel()

		if got := hooks.Entries("Missing"); got != nil {
			t.Fatalf("Entries(Missing) = %v, want nil", got)
		}
	})

	t.Run("EventOrder returns a fresh slice", func(t *testing.T) {
		t.Parallel()

		got := hooks.EventOrder()
		got[0] = "mutated"
		again := hooks.EventOrder()
		if again[0] != "Event" {
			t.Fatalf("EventOrder() returned the internal slice; second read = %q", again[0])
		}
	})
}
