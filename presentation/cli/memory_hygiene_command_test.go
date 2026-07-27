package cli_test

import (
	"bytes"
	"testing"
	"time"

	apptypes "github.com/duck8823/traceary/application/types"
	"github.com/duck8823/traceary/presentation/cli"
)

func TestMemoryHygieneScanCommand_PassesExplicitBoundsAndCursor(t *testing.T) {
	t.Parallel()

	memoryStub := &memoryUsecaseStub{
		scanResult: apptypes.MemoryHygieneScanResult{
			Complete:   true,
			StopReason: apptypes.MemoryHygieneStopReasonComplete,
		},
	}
	root := cli.NewRootCLI(
		cli.WithStoreManagement(&storeManagementUsecaseStub{}),
		cli.WithMemory(memoryStub),
	)
	cmd := root.Command()
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(&bytes.Buffer{})
	cmd.SetArgs([]string{
		"memory", "admin", "hygiene", "scan",
		"--db-path", t.TempDir() + "/traceary.db",
		"--workspace", "github.com/example/repo",
		"--expiry-days", "30",
		"--similarity", "0.75",
		"--include-hidden",
		"--cursor", "opaque-cursor",
		"--max-scan-rows", "12",
		"--max-scan-bytes", "4096",
		"--max-result-bytes", "2048",
		"--max-comparisons", "21",
		"--max-duration", "750ms",
		"--json",
	})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}

	got := memoryStub.scanCriteria
	if got.Cursor != "opaque-cursor" ||
		got.Budget.MaxRows() != 12 ||
		got.Budget.MaxScanBytes() != 4_096 ||
		got.Budget.MaxResultBytes() != 2_048 ||
		got.Budget.MaxComparisons() != 21 ||
		got.Budget.MaxDuration() != 750*time.Millisecond {
		t.Fatalf("scan criteria did not preserve explicit bounds: %#v", got)
	}
	if got.SimilarityThreshold != 0.75 || !got.IncludeHiddenCandidates {
		t.Fatalf("scan criteria = %#v, want similarity/include-hidden", got)
	}
	if !got.Now.IsZero() {
		t.Fatalf("continuation criteria Now = %s, want authenticated cursor time to remain authoritative", got.Now)
	}
	if len(got.Scopes) != 1 || got.Scopes[0].Key() != "github.com/example/repo" {
		t.Fatalf("Scopes = %#v, want explicit workspace", got.Scopes)
	}
}

func TestMemoryHygieneScanCommand_OwnsInitialScanTime(t *testing.T) {
	t.Parallel()

	memoryStub := &memoryUsecaseStub{
		scanResult: apptypes.MemoryHygieneScanResult{
			Complete:    true,
			StopReason:  apptypes.MemoryHygieneStopReasonComplete,
			Consistency: apptypes.MemoryHygieneScanConsistencyConsistent,
		},
	}
	root := cli.NewRootCLI(
		cli.WithStoreManagement(&storeManagementUsecaseStub{}),
		cli.WithMemory(memoryStub),
	)
	cmd := root.Command()
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(&bytes.Buffer{})
	cmd.SetArgs([]string{
		"memory", "admin", "hygiene", "scan",
		"--db-path", t.TempDir() + "/traceary.db",
		"--json",
	})
	before := time.Now().UTC()
	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	after := time.Now().UTC()
	if memoryStub.scanCriteria.Now.Before(before) || memoryStub.scanCriteria.Now.After(after) {
		t.Fatalf("initial scan Now = %s, want boundary-owned time in [%s,%s]",
			memoryStub.scanCriteria.Now,
			before,
			after,
		)
	}
}
