package cli

import (
	"path/filepath"
	"testing"
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
