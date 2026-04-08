package cli_test

import (
	"bytes"
	"context"
	"testing"
	"time"

	"github.com/duck8823/traceary/application/usecase"
	"github.com/duck8823/traceary/presentation/cli"
)

type collectGarbageUsecaseStub struct {
	receivedInput usecase.CollectGarbageInput
	called        bool
	result        *usecase.CollectGarbageResult
	err           error
}

func (s *collectGarbageUsecaseStub) Run(
	_ context.Context,
	input usecase.CollectGarbageInput,
) (*usecase.CollectGarbageResult, error) {
	s.called = true
	s.receivedInput = input
	return s.result, s.err
}

var _ usecase.CollectGarbageUsecase = (*collectGarbageUsecaseStub)(nil)

func TestRootCLI_GCCommand(t *testing.T) {
	fixedNow := time.Date(2026, 4, 10, 0, 0, 0, 0, time.UTC)
	cli.SetGCNowFunc(func() time.Time { return fixedNow })
	defer cli.ResetGCNowFunc()

	t.Run("dry-run の件数を表示できる", func(t *testing.T) {
		stub := &collectGarbageUsecaseStub{
			result: &usecase.CollectGarbageResult{DeletedCount: 3, DryRun: true},
		}
		initStub := &initializeStoreUsecaseStub{}
		stdout := &bytes.Buffer{}
		rootCmd := cli.NewRootCLI(cli.RootCLIOptions{
			InitializeStoreUsecase: initStub,
			CollectGarbageUsecase:  stub,
		}).Command()
		rootCmd.SetOut(stdout)
		rootCmd.SetErr(&bytes.Buffer{})
		rootCmd.SetArgs([]string{"gc", "--db-path", "/tmp/traceary.db", "--keep-days", "30", "--dry-run"})

		if err := rootCmd.Execute(); err != nil {
			t.Fatalf("Execute() error = %v", err)
		}
		if !stub.called {
			t.Fatalf("CollectGarbageUsecase.Run() was not called")
		}
		if !initStub.called {
			t.Fatalf("InitializeStoreUsecase.Run() was not called")
		}
		wantCutoff := fixedNow.AddDate(0, 0, -30)
		if !stub.receivedInput.Before.Equal(wantCutoff) {
			t.Fatalf("Before = %v, want %v", stub.receivedInput.Before, wantCutoff)
		}
		if stdout.String() != "Candidates: 3\n" {
			t.Fatalf("stdout = %q, want %q", stdout.String(), "Candidates: 3\n")
		}
	})

	t.Run("削除件数を表示できる", func(t *testing.T) {
		stub := &collectGarbageUsecaseStub{
			result: &usecase.CollectGarbageResult{DeletedCount: 2, DryRun: false},
		}
		initStub := &initializeStoreUsecaseStub{}
		stdout := &bytes.Buffer{}
		rootCmd := cli.NewRootCLI(cli.RootCLIOptions{
			InitializeStoreUsecase: initStub,
			CollectGarbageUsecase:  stub,
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
