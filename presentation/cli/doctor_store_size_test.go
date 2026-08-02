package cli

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	apptypes "github.com/duck8823/traceary/application/types"
)

func TestInspectStoreSizeBudget_MissingFilePasses(t *testing.T) {
	check := inspectStoreSizeBudget(filepath.Join(t.TempDir(), "missing.db"))
	if check.Status != doctorStatusPass {
		t.Fatalf("check = %#v", check)
	}
}

type growthCapacityStub struct{ report apptypes.CapacityReport }

func (s growthCapacityStub) InspectCapacity(context.Context) (apptypes.CapacityReport, error) {
	return s.report, nil
}

func TestInspectStoreGrowthBudgetMeasuresCompletedInspectionLatency(t *testing.T) {
	path := filepath.Join(t.TempDir(), "store.db")
	if err := os.WriteFile(path, []byte("fixture"), 0o600); err != nil {
		t.Fatal(err)
	}
	root := &RootCLI{capacityInspector: growthCapacityStub{report: apptypes.CapacityReport{DatabaseBytes: 128 << 20}}}
	times := []time.Time{time.Unix(0, 0), time.Unix(0, 0).Add(1600 * time.Millisecond)}
	index := 0
	check := root.inspectStoreGrowthBudgetWithClock(context.Background(), path, func() time.Time { value := times[index]; index++; return value })
	if check.Status != doctorStatusWarn || !strings.Contains(check.Message, "latency") {
		t.Fatalf("check=%#v", check)
	}
}

func TestInspectStoreSizeBudget_SmallFilePasses(t *testing.T) {
	path := filepath.Join(t.TempDir(), "small.db")
	if err := os.WriteFile(path, []byte("tiny"), 0o600); err != nil {
		t.Fatal(err)
	}
	check := inspectStoreSizeBudget(path)
	if check.Status != doctorStatusPass {
		t.Fatalf("check = %#v", check)
	}
	if !strings.Contains(check.Message, "within") {
		t.Fatalf("message = %q", check.Message)
	}
}

func TestInspectStoreSizeBudget_LargeFileWarns(t *testing.T) {
	path := filepath.Join(t.TempDir(), "large.db")
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	// Sparse-ish: Seek past the warn threshold then write one byte.
	if _, err := f.Seek(storeSizeWarnBytes, 0); err != nil {
		t.Fatal(err)
	}
	if _, err := f.Write([]byte{1}); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	check := inspectStoreSizeBudget(path)
	if check.Status != doctorStatusWarn {
		t.Fatalf("check = %#v", check)
	}
	if !strings.Contains(check.Message, "large") {
		t.Fatalf("message = %q", check.Message)
	}
	if !strings.Contains(check.Hint, "store compact plan") {
		t.Fatalf("hint = %q", check.Hint)
	}
}

func TestFormatByteSize(t *testing.T) {
	if got := formatByteSize(512); got != "512 B" {
		t.Fatalf("got %q", got)
	}
	if got := formatByteSize(storeSizeWarnBytes); !strings.Contains(got, "GiB") {
		t.Fatalf("got %q", got)
	}
}

func TestEvaluateStoreGrowthBudgetUsesIndependentSignals(t *testing.T) {
	base := storeGrowthEvidence{DatabaseBytes: 256 << 20, EventPayloadBytes: 64 << 20, ProjectionBytes: 32 << 20, FilesystemFreeBytes: 4 << 30, FilesystemFreeAvailable: true, MeasuredLatency: 100 * time.Millisecond}
	for _, tc := range []struct {
		name   string
		mutate func(*storeGrowthEvidence)
	}{
		{"event payload", func(e *storeGrowthEvidence) { e.EventPayloadBytes = 512 << 20 }},
		{"projection", func(e *storeGrowthEvidence) { e.ProjectionBytes = 256 << 20 }},
		{"headroom", func(e *storeGrowthEvidence) { e.FilesystemFreeBytes = e.DatabaseBytes }},
		{"latency", func(e *storeGrowthEvidence) { e.MeasuredLatency = 2 * time.Second }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			evidence := base
			tc.mutate(&evidence)
			check := evaluateStoreGrowthBudget(evidence)
			if check.Status != doctorStatusWarn {
				t.Fatalf("check=%#v", check)
			}
			if !strings.HasPrefix(check.FixCommand, "traceary store compact plan") {
				t.Fatalf("fix=%q", check.FixCommand)
			}
		})
	}
	if check := evaluateStoreGrowthBudget(base); check.Status != doctorStatusPass {
		t.Fatalf("healthy=%#v", check)
	}
}

func TestStoreGrowthHeadroomAvailabilityAndOverflow(t *testing.T) {
	threshold := compactionHeadroomThreshold(512 << 20)
	for _, free := range []int64{0, 1, threshold - 1, threshold} {
		check := evaluateStoreGrowthBudget(storeGrowthEvidence{DatabaseBytes: 512 << 20, FilesystemFreeBytes: free, FilesystemFreeAvailable: true})
		if check.Status != doctorStatusWarn {
			t.Fatalf("free=%d check=%#v", free, check)
		}
	}
	if check := evaluateStoreGrowthBudget(storeGrowthEvidence{DatabaseBytes: 512 << 20, FilesystemFreeAvailable: false}); check.Status != doctorStatusWarn {
		t.Fatalf("unavailable=%#v", check)
	}
	if got := compactionHeadroomThreshold(int64(^uint64(0) >> 1)); got != int64(^uint64(0)>>1) {
		t.Fatalf("overflow threshold=%d", got)
	}
}
