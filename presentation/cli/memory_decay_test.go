package cli_test

import (
	"bytes"
	"context"
	"strings"
	"testing"
	"time"

	apptypes "github.com/duck8823/traceary/application/types"
	"github.com/duck8823/traceary/presentation/cli"
)

type memoryDecayStub struct {
	memoryUsecaseStub
	decayResult apptypes.MemoryDecayResult
	decayErr    error
	decayCall   apptypes.MemoryDecayCriteria
}

func (s *memoryDecayStub) Decay(_ context.Context, criteria apptypes.MemoryDecayCriteria) (apptypes.MemoryDecayResult, error) {
	s.decayCall = criteria
	return s.decayResult, s.decayErr
}

func TestRootCLI_MemoryDecay_DryRun(t *testing.T) {
	stub := &memoryDecayStub{
		decayResult: apptypes.MemoryDecayResult{
			ExpiredIDs:     []string{"mem-1"},
			Scanned:        3,
			RemainingAfter: 0,
			OlderThan:      720 * time.Hour,
			Applied:        false,
		},
	}
	rootCmd := newTestRootCLI(
		cli.WithStoreManagement(&storeManagementUsecaseStub{}),
		cli.WithMemory(stub),
	).Command()
	stdout := &bytes.Buffer{}
	rootCmd.SetOut(stdout)
	rootCmd.SetErr(&bytes.Buffer{})
	rootCmd.SetArgs([]string{"memory", "decay", "--db-path", t.TempDir() + "/x.db"})
	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if stub.decayCall.Apply {
		t.Fatal("default must be dry-run")
	}
	if !strings.Contains(stdout.String(), "dry-run") || !strings.Contains(stdout.String(), "expired=1") {
		t.Fatalf("stdout = %q", stdout.String())
	}
}
