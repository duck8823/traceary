package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

var (
	testDefaultHookStateDir    string
	testDefaultUserHomeDir     string
	testDefaultUserHomeDirFunc func() (string, error)
)

const (
	testHookStateDirEnvKey = "TRACEARY_TEST_HOOK_STATE_DIR"
	testDBPathEnvKey       = "TRACEARY_TEST_DB_PATH"
)

// TestMain pins the CLI locale to English for this package's tests unless the
// environment already sets TRACEARY_LANG. Locale resolution falls back to the
// operator's ~/.config/traceary/config.json ui.language when TRACEARY_LANG is
// unset (see i18n.go), so golden-snapshot tests that assert English View()
// output otherwise fail on a machine configured for Japanese even though CI
// (which has no such config) stays green. Pinning here makes `go test` for the
// package hermetic with respect to the operator's config and OS locale.
//
// Per-test overrides via t.Setenv(cliLanguageEnvKey, ...) still take effect:
// they run after this default is applied and restore the prior value when the
// test completes.
func TestMain(m *testing.M) {
	if _, ok := os.LookupEnv(cliLanguageEnvKey); !ok {
		_ = os.Setenv(cliLanguageEnvKey, "en")
	}

	var err error
	testDefaultUserHomeDir, err = os.MkdirTemp("", "traceary-cli-test-home-")
	if err != nil {
		fmt.Fprintf(os.Stderr, "create isolated Traceary CLI test home: %v\n", err)
		os.Exit(1)
	}
	testDefaultUserHomeDirFunc = func() (string, error) { return testDefaultUserHomeDir, nil }
	userHomeDirFunc = testDefaultUserHomeDirFunc

	if stateDir := strings.TrimSpace(os.Getenv(testHookStateDirEnvKey)); stateDir != "" {
		testDefaultHookStateDir = stateDir
	} else {
		testDefaultHookStateDir, err = os.MkdirTemp("", "traceary-cli-test-hooks-")
		if err != nil {
			_ = os.RemoveAll(testDefaultUserHomeDir)
			fmt.Fprintf(os.Stderr, "create isolated Traceary CLI hook state: %v\n", err)
			os.Exit(1)
		}
	}
	if err := os.Setenv(hookStateDirEnvKey, testDefaultHookStateDir); err != nil {
		_ = os.RemoveAll(testDefaultHookStateDir)
		_ = os.RemoveAll(testDefaultUserHomeDir)
		fmt.Fprintf(os.Stderr, "configure isolated Traceary CLI hook state: %v\n", err)
		os.Exit(1)
	}
	if dbPath := strings.TrimSpace(os.Getenv(testDBPathEnvKey)); dbPath != "" {
		if err := os.Setenv(dbPathEnvKey, dbPath); err != nil {
			fmt.Fprintf(os.Stderr, "configure isolated Traceary CLI database: %v\n", err)
			os.Exit(1)
		}
	} else if err := os.Unsetenv(dbPathEnvKey); err != nil {
		fmt.Fprintf(os.Stderr, "clear ambient Traceary CLI database: %v\n", err)
		os.Exit(1)
	}
	code := m.Run()
	if os.Getenv(testHookStateDirEnvKey) == "" {
		_ = os.RemoveAll(testDefaultHookStateDir)
	}
	_ = os.RemoveAll(testDefaultUserHomeDir)
	os.Exit(code)
}

func TestMain_UsesTestOwnedHookStateDirectory(t *testing.T) {
	relativePath, err := filepath.Rel(os.TempDir(), testDefaultHookStateDir)
	if err != nil || relativePath == ".." || strings.HasPrefix(relativePath, ".."+string(os.PathSeparator)) {
		t.Fatalf("testDefaultHookStateDir = %q, want a test-owned temporary directory", testDefaultHookStateDir)
	}
}
