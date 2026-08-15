package cli

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/duck8823/traceary/domain/types"
)

func TestResolveHookStateDir_UsesHomeFallbackWhenEnvironmentIsEmpty(t *testing.T) {
	homeDir := t.TempDir()
	originalHomeDirFunc := userHomeDirFunc
	userHomeDirFunc = func() (string, error) { return homeDir, nil }
	t.Cleanup(func() { userHomeDirFunc = originalHomeDirFunc })
	t.Setenv(hookStateDirEnvKey, "")

	got, err := resolveHookStateDir()
	if err != nil {
		t.Fatalf("resolveHookStateDir() error = %v", err)
	}
	want := filepath.Join(homeDir, ".config", "traceary", "hooks")
	if got != want {
		t.Fatalf("resolveHookStateDir() = %q, want %q", got, want)
	}
}

func TestResolveHookStateDir_ErrorInjectedHomeCannotUseTestProcessHome(t *testing.T) {
	SetUserHomeDirFunc(func() (string, error) { return "", errors.New("home unavailable") })
	t.Cleanup(ResetUserHomeDirFunc)
	if _, configured := os.LookupEnv(hookStateDirEnvKey); configured {
		t.Fatal("SetUserHomeDirFunc(error) must clear the hook-state environment")
	}

	if _, err := resolveHookStateDir(); err == nil {
		t.Fatal("resolveHookStateDir() error = nil, want home lookup error")
	}
}

func TestResetUserHomeDirFunc_UsesTestHomeWhenStateEnvironmentIsBlank(t *testing.T) {
	ResetUserHomeDirFunc()
	t.Setenv(hookStateDirEnvKey, "")

	got, err := resolveHookStateDir()
	if err != nil {
		t.Fatalf("resolveHookStateDir() error = %v", err)
	}
	want := filepath.Join(testDefaultUserHomeDir, ".config", "traceary", "hooks")
	if got != want {
		t.Fatalf("resolveHookStateDir() = %q, want %q", got, want)
	}
}

func TestHookSessionBoundStatePaths_DoNotAliasSanitizedCollidingSessionIDs(t *testing.T) {
	t.Setenv(hookStateDirEnvKey, t.TempDir())
	const client = "grok"
	slash := types.SessionID("a/b")
	colon := types.SessionID("a:b")
	if sanitizeHookStateKey(slash.String()) != sanitizeHookStateKey(colon.String()) {
		t.Fatal("precondition: a/b and a:b must collide under sanitizeHookStateKey")
	}

	for _, name := range []string{"ended", "wake-injected", "active-subagents"} {
		var left, right string
		var err error
		switch name {
		case "ended":
			left, err = hookSessionEndMarkerPath(client, slash)
			if err != nil {
				t.Fatalf("ended a/b: %v", err)
			}
			right, err = hookSessionEndMarkerPath(client, colon)
		case "wake-injected":
			left, err = hookWakeInjectionMarkerPath(client, slash)
			if err != nil {
				t.Fatalf("wake a/b: %v", err)
			}
			right, err = hookWakeInjectionMarkerPath(client, colon)
		default:
			left, err = hookActiveSubagentStatePath(client, slash)
			if err != nil {
				t.Fatalf("active-subagents a/b: %v", err)
			}
			right, err = hookActiveSubagentStatePath(client, colon)
		}
		if err != nil {
			t.Fatalf("%s a:b: %v", name, err)
		}
		if left == right {
			t.Fatalf("%s paths alias: %q", name, left)
		}
		oldBase := client + "-" + sanitizeHookStateKey(slash.String())
		if filepath.Base(left) == oldBase || filepath.Base(right) == oldBase {
			t.Fatalf("%s still uses the old sanitized name: %q / %q", name, left, right)
		}
	}

	if err := markHookSessionEnded(client, slash); err != nil {
		t.Fatalf("markHookSessionEnded(a/b): %v", err)
	}
	hit, err := hookSessionEndAlreadyRecorded(client, colon)
	if err != nil {
		t.Fatalf("hookSessionEndAlreadyRecorded(a:b): %v", err)
	}
	if hit {
		t.Fatal("ending a/b must not mark a:b as already recorded")
	}
	if err := markHookWakeInjected(client, slash); err != nil {
		t.Fatalf("markHookWakeInjected(a/b): %v", err)
	}
	wakeHit, err := hookWakeInjectionAlreadyDone(client, colon)
	if err != nil {
		t.Fatalf("hookWakeInjectionAlreadyDone(a:b): %v", err)
	}
	if wakeHit {
		t.Fatal("wake-injecting a/b must not skip a:b")
	}
	if err := writeHookActiveSubagentState(client, slash, "tool-1", types.SessionID("child-slash")); err != nil {
		t.Fatalf("writeHookActiveSubagentState(a/b): %v", err)
	}
	child, ok, err := readHookActiveSubagentStateForTool(client, colon, "tool-1")
	if err != nil {
		t.Fatalf("readHookActiveSubagentStateForTool(a:b): %v", err)
	}
	if ok {
		t.Fatalf("active-subagent state for a/b leaked to a:b: %q", child)
	}
}

func TestHookSessionBoundStatePaths_OldSanitizedMarkerIsNotAHit(t *testing.T) {
	stateDir := t.TempDir()
	t.Setenv(hookStateDirEnvKey, stateDir)
	const client = "grok"
	slash := types.SessionID("a/b")
	colon := types.SessionID("a:b")
	oldName := client + "-" + sanitizeHookStateKey(slash.String())
	for _, dir := range []string{"ended", "wake-injected", "active-subagents"} {
		if err := os.MkdirAll(filepath.Join(stateDir, dir), 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", dir, err)
		}
		if err := os.WriteFile(filepath.Join(stateDir, dir, oldName), []byte("legacy"), 0o600); err != nil {
			t.Fatalf("write old %s marker: %v", dir, err)
		}
	}

	ended, err := hookSessionEndAlreadyRecorded(client, slash)
	if err != nil {
		t.Fatalf("ended a/b: %v", err)
	}
	endedColon, err := hookSessionEndAlreadyRecorded(client, colon)
	if err != nil {
		t.Fatalf("ended a:b: %v", err)
	}
	if ended || endedColon {
		t.Fatalf("old ended marker was a hit: a/b=%v a:b=%v", ended, endedColon)
	}

	wake, err := hookWakeInjectionAlreadyDone(client, slash)
	if err != nil {
		t.Fatalf("wake a/b: %v", err)
	}
	wakeColon, err := hookWakeInjectionAlreadyDone(client, colon)
	if err != nil {
		t.Fatalf("wake a:b: %v", err)
	}
	if wake || wakeColon {
		t.Fatalf("old wake marker was a hit: a/b=%v a:b=%v", wake, wakeColon)
	}

	child, ok, err := readHookActiveSubagentStateForTool(client, slash, "legacy")
	if err != nil {
		t.Fatalf("active-subagents a/b: %v", err)
	}
	if ok {
		t.Fatalf("old active-subagent file was a hit: %q", child)
	}
}
