package cli_test

import (
	"bytes"
	"testing"
	"time"

	"github.com/duck8823/traceary/application/usecase"
	"github.com/duck8823/traceary/presentation/cli"
)

func TestRootCLI_GCCommand(t *testing.T) {
	fixedNow := time.Date(2026, 4, 10, 0, 0, 0, 0, time.UTC)
	cli.SetGCNowFunc(func() time.Time { return fixedNow })
	defer cli.ResetGCNowFunc()

	t.Run("dry-run の件数を表示できる", func(t *testing.T) {
		storeMaint := &storeMaintenanceUsecaseStub{
			gcResult: &usecase.CollectGarbageResult{DeletedCount: 3, DryRun: true},
		}
		stdout := &bytes.Buffer{}
		rootCmd := cli.NewRootCLI(cli.RootCLIOptions{
			StoreMaintenance: storeMaint,
		}).Command()
		rootCmd.SetOut(stdout)
		rootCmd.SetErr(&bytes.Buffer{})
		rootCmd.SetArgs([]string{"gc", "--db-path", "/tmp/traceary.db", "--keep-days", "30", "--dry-run"})

		if err := rootCmd.Execute(); err != nil {
			t.Fatalf("Execute() error = %v", err)
		}
		if stdout.String() != "Candidates: 3\n" {
			t.Fatalf("stdout = %q, want %q", stdout.String(), "Candidates: 3\n")
		}
	})

	t.Run("displays deletion count", func(t *testing.T) {
		storeMaint := &storeMaintenanceUsecaseStub{
			gcResult: &usecase.CollectGarbageResult{DeletedCount: 2, DryRun: false},
		}
		stdout := &bytes.Buffer{}
		rootCmd := cli.NewRootCLI(cli.RootCLIOptions{
			StoreMaintenance: storeMaint,
		}).Command()
		rootCmd.SetOut(stdout)
		rootCmd.SetErr(&bytes.Buffer{})
		rootCmd.SetArgs([]string{"gc", "--db-path", "/tmp/traceary.db", "--keep-days", "30"})

		if err := rootCmd.Execute(); err != nil {
			t.Fatalf("Execute() error = %v", err)
		}
		if stdout.String() != "Deleted: 2\n" {
			t.Fatalf("stdout = %q, want %q", stdout.String(), "Deleted: 2\n")
		}
	})
}
