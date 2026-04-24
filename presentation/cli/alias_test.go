package cli_test

import (
	"bytes"
	"strings"
	"testing"

	"github.com/duck8823/traceary/presentation/cli"
)

// TestDeprecatedTopLevelAliasesStillWire confirms that the v0.8.x
// top-level entry points (init / gc / backup / handoff /
// compact-summary) still route through the CLI as aliases during the
// v0.9 migration window. Each alias is expected to:
//   - execute without error
//   - emit a cobra-generated "Command ... is deprecated" notice so
//     operators see the replacement path.
//
// v1.0 will drop these aliases (tracked via #696's removal plan).
func TestDeprecatedTopLevelAliasesStillWire(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name            string
		args            []string
		expectedReplace string
	}{
		{"init → store init", []string{"init", "--help"}, "store init"},
		{"gc → store gc", []string{"gc", "--help"}, "store gc"},
		{"backup → store backup", []string{"backup", "--help"}, "store backup"},
		{"handoff → session handoff", []string{"handoff", "--help"}, "session handoff"},
		{"compact-summary → session handoff --compact-only", []string{"compact-summary", "--help"}, "session handoff --compact-only"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			stdout := &bytes.Buffer{}
			stderr := &bytes.Buffer{}
			rootCmd := newTestRootCLI(cli.WithStoreManagement(&storeManagementUsecaseStub{})).Command()
			rootCmd.SetOut(stdout)
			rootCmd.SetErr(stderr)
			rootCmd.SetArgs(tc.args)
			if err := rootCmd.Execute(); err != nil {
				t.Fatalf("Execute() error = %v", err)
			}
			combined := stdout.String() + stderr.String()
			if !strings.Contains(combined, "is deprecated") {
				t.Fatalf("expected a deprecation notice in output; got %q", combined)
			}
			if !strings.Contains(combined, tc.expectedReplace) {
				t.Fatalf("expected replacement path %q in deprecation notice; got %q", tc.expectedReplace, combined)
			}
		})
	}
}
