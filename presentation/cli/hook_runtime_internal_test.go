package cli

import (
	"os"
	"strconv"
	"testing"

	"github.com/google/go-cmp/cmp"
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

	if diff := cmp.Diff("4242", resolveHookStateKey()); diff != "" {
		t.Fatalf("resolveHookStateKey() mismatch (-want +got):\n%s", diff)
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

	if diff := cmp.Diff(sanitizeHookStateKey(strconv.Itoa(os.Getppid())), resolveHookStateKey()); diff != "" {
		t.Fatalf("resolveHookStateKey() mismatch (-want +got):\n%s", diff)
	}
}
