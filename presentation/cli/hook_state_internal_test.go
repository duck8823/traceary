package cli

import (
	"errors"
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

func TestResolveHookStateDir_ErrorInjectedHomeCannotUseTestProcessHome(t *testing.T) {
	SetUserHomeDirFunc(func() (string, error) { return "", errors.New("home unavailable") })
	t.Cleanup(ResetUserHomeDirFunc)
	t.Setenv(hookStateDirEnvKey, "")

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
