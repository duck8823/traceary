package cli_test

import (
	"bytes"
	"testing"
	"time"

	"github.com/duck8823/traceary/presentation/cli"
)

func TestRootCLI_BundleExportCommand_DateRangeAliases(t *testing.T) {
	t.Setenv("TRACEARY_BUNDLE_PASSPHRASE", "test-passphrase")

	stdout := &bytes.Buffer{}
	bundleStub := &bundleUsecaseStub{}
	rootCmd := cli.NewRootCLI(
		cli.WithStoreManagement(&storeManagementUsecaseStub{}),
		cli.WithBundle(bundleStub),
	).Command()
	rootCmd.SetOut(stdout)
	rootCmd.SetErr(&bytes.Buffer{})
	rootCmd.SetArgs([]string{
		"bundle", "export",
		"--db-path", "/tmp/test.db",
		"--out", t.TempDir() + "/traceary.bundle",
		"--from", "2026-04-10",
		"--since", "2026-04-10",
		"--to", "2026-04-11",
		"--until", "2026-04-11",
	})

	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if bundleStub.exportOptions.Since.IsZero() {
		t.Fatalf("expected --from/--since to set Since")
	}
	if bundleStub.exportOptions.Until.IsZero() {
		t.Fatalf("expected --to/--until to set Until")
	}
	if got, want := bundleStub.exportOptions.Since, time.Date(2026, 4, 10, 0, 0, 0, 0, time.UTC); !got.Equal(want) {
		t.Fatalf("Since = %s, want %s", got, want)
	}
	if got, want := bundleStub.exportOptions.Until, time.Date(2026, 4, 12, 0, 0, 0, 0, time.UTC); !got.Equal(want) {
		t.Fatalf("Until = %s, want %s", got, want)
	}
}
