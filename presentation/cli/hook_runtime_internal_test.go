package cli

import "testing"

func TestResolveHookStateKey_UsesGrandparentProcessIdentity(t *testing.T) {
	t.Setenv("TRACEARY_HOOK_STATE_KEY", "")

	originalLookup := hookParentPIDLookup
	hookParentPIDLookup = func(pid int) (int, error) {
		if pid <= 0 {
			t.Fatalf("pid = %d, want > 0", pid)
		}
		return 4242, nil
	}
	t.Cleanup(func() {
		hookParentPIDLookup = originalLookup
	})

	if got, want := resolveHookStateKey(), "4242"; got != want {
		t.Fatalf("resolveHookStateKey() = %q, want %q", got, want)
	}
}
