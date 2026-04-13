package cli

import (
	"os"
	"strconv"
	"testing"
)

func TestResolveHookStateKey_UsesGrandparentProcessIdentity(t *testing.T) {
	t.Setenv("TRACEARY_HOOK_STATE_KEY", "")

	originalLookup := hookParentProcessLookup
	hookParentProcessLookup = func(pid int) (hookParentProcessInfo, error) {
		if pid <= 0 {
			t.Fatalf("pid = %d, want > 0", pid)
		}
		return hookParentProcessInfo{parentPID: 4242, command: "/bin/bash"}, nil
	}
	t.Cleanup(func() {
		hookParentProcessLookup = originalLookup
	})

	if got, want := resolveHookStateKey(), "4242"; got != want {
		t.Fatalf("resolveHookStateKey() = %q, want %q", got, want)
	}
}

func TestResolveHookStateKey_UsesParentIdentityForNonShellParents(t *testing.T) {
	t.Setenv("TRACEARY_HOOK_STATE_KEY", "")

	originalLookup := hookParentProcessLookup
	hookParentProcessLookup = func(pid int) (hookParentProcessInfo, error) {
		if pid <= 0 {
			t.Fatalf("pid = %d, want > 0", pid)
		}
		return hookParentProcessInfo{parentPID: 4242, command: "/usr/bin/codex"}, nil
	}
	t.Cleanup(func() {
		hookParentProcessLookup = originalLookup
	})

	if got, want := resolveHookStateKey(), sanitizeHookStateKey(strconv.Itoa(os.Getppid())); got != want {
		t.Fatalf("resolveHookStateKey() = %q, want %q", got, want)
	}
}
