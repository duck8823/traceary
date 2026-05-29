package cli

import (
	"os"
	"testing"
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

	os.Exit(m.Run())
}
