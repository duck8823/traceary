package cli

import (
	"context"
	"fmt"
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
	checks := root.inspectStoreGrowthBudgetWithClock(context.Background(), path, inspectStoreFileSnapshot(path, os.Stat), func() time.Time { value := times[index]; index++; return value })
	if len(checks) != 3 || checks[0].Status != doctorStatusWarn || !strings.Contains(checks[0].Message, "latency") {
		t.Fatalf("checks=%#v", checks)
	}
	if checks[1].Name != "store-capacity" || checks[1].Status != doctorStatusPass || !strings.Contains(checks[1].Message, "database=128.0 MiB") {
		t.Fatalf("capacity check=%#v", checks[1])
	}
	if checks[2].Name != "legacy-search-index" || checks[2].Status != doctorStatusPass {
		t.Fatalf("legacy check=%#v", checks[2])
	}
}

// TestInspectStoreGrowthBudgetSeparatesRetiredIndexFromProjection covers the
// classification, which is easy to get subtly wrong: dbstat names the FTS5
// shadow tables and the implicit UNIQUE index in ways that do not share a
// prefix, and the live projection's own objects also contain "search".
func TestInspectStoreGrowthBudgetSeparatesRetiredIndexFromProjection(t *testing.T) {
	path := filepath.Join(t.TempDir(), "store.db")
	if err := os.WriteFile(path, []byte("fixture"), 0o600); err != nil {
		t.Fatal(err)
	}
	root := &RootCLI{capacityInspector: growthCapacityStub{report: apptypes.CapacityReport{
		DatabaseBytes: 128 << 20,
		Objects: []apptypes.CapacityObject{
			{Name: "event_search_documents", Bytes: 3 << 30},
			{Name: "event_search_fts_data", Bytes: 5 << 30},
			{Name: "sqlite_autoindex_event_search_documents_1", Bytes: 1 << 30},
			{Name: "events", Bytes: 2 << 30},
		},
	}}}
	checks := root.inspectStoreGrowthBudget(context.Background(), path, inspectStoreFileSnapshot(path, os.Stat))
	if len(checks) != 5 {
		t.Fatalf("checks=%#v", checks)
	}
	if checks[1].Name != "store-capacity" || !strings.Contains(checks[1].Message, "top=[") {
		t.Fatalf("capacity check=%#v", checks[1])
	}
	if checks[3].Name != "compact-rollback-copy" || checks[3].Status != doctorStatusPass {
		t.Fatalf("rollback check=%#v", checks[3])
	}
	if checks[4].Name != "compact-in-flight" || checks[4].Status != doctorStatusPass {
		t.Fatalf("in-flight check=%#v", checks[4])
	}
	legacy := checks[2]
	if legacy.Name != "legacy-search-index" || legacy.Status != doctorStatusWarn {
		t.Fatalf("legacy check=%#v", legacy)
	}
	// 3 + 5 + 1 GiB, including the shadow table and the implicit index.
	if !strings.Contains(legacy.Message, "9.0 GiB") {
		t.Fatalf("legacy message = %q, want the summed family bytes", legacy.Message)
	}
	if !strings.Contains(legacy.FixCommand, "store compact") {
		t.Fatalf("legacy fix = %q", legacy.FixCommand)
	}
	// Retired family bytes belong on legacy-search-index, not store-size.
	if strings.Contains(checks[0].Message, "9.0 GiB") {
		t.Fatalf("store-size message = %q, absorbed retired index bytes", checks[0].Message)
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
	if !strings.Contains(check.Hint, "store compact") {
		t.Fatalf("hint = %q", check.Hint)
	}
	if strings.Contains(strings.ToLower(check.Hint), "preview `") {
		t.Fatalf("hint still calls the rewrite a preview: %q", check.Hint)
	}
	if !strings.Contains(check.Hint, "rollback") {
		t.Fatalf("hint = %q, want rollback RUN_ID", check.Hint)
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
			if !strings.HasPrefix(check.FixCommand, "traceary store compact") {
				t.Fatalf("fix=%q", check.FixCommand)
			}
			if strings.Contains(strings.ToLower(check.Hint), "preview `") {
				t.Fatalf("hint still calls the rewrite a preview: %q", check.Hint)
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

func TestStoreSnapshotSurvivesDeleteAfterSingleStat(t *testing.T) {
	path := filepath.Join(t.TempDir(), "large.db")
	file, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := file.Truncate(2 << 30); err != nil {
		t.Fatal(err)
	}
	_ = file.Close()
	calls := 0
	snapshot := inspectStoreFileSnapshot(path, func(name string) (os.FileInfo, error) {
		calls++
		info, statErr := os.Stat(name)
		if statErr == nil {
			_ = os.Remove(name)
		}
		if statErr != nil {
			return nil, fmt.Errorf("stat test store: %w", statErr)
		}
		return info, nil
	})
	if calls != 1 {
		t.Fatalf("stat calls=%d", calls)
	}
	if !isLargeStoreForBoundedDoctor(snapshot) {
		t.Fatal("snapshot lost large-store decision after delete")
	}
	check := boundedLargeStoreDoctorCheck(snapshot, "/tmp/other store.db", false)
	if check.Status != doctorStatusWarn || !strings.Contains(check.Message, "were not read") {
		t.Fatalf("check=%#v", check)
	}
	// The metadata-only mode cannot know whether the retired legacy index is
	// present, so it advises unconditionally — and must advise on the store the
	// operator actually named, not the default path.
	if !strings.Contains(check.Hint, "store compact") || !strings.Contains(check.FixCommand, shellQuote("/tmp/other store.db")) {
		t.Fatalf("check=%#v", check)
	}
}

func TestStoreGrowthMessageDistinguishesUnknownAndMeasuredZeroFree(t *testing.T) {
	unknown := evaluateStoreGrowthBudget(storeGrowthEvidence{DatabaseBytes: 1})
	zero := evaluateStoreGrowthBudget(storeGrowthEvidence{DatabaseBytes: 1, FilesystemFreeAvailable: true})
	if !strings.Contains(unknown.Message, "free=unknown") {
		t.Fatalf("unknown=%q", unknown.Message)
	}
	if !strings.Contains(zero.Message, "free=0 B") {
		t.Fatalf("zero=%q", zero.Message)
	}
}

func TestEvaluateLargeStoreGrowthBudgetReportsReclaimable(t *testing.T) {
	pass := evaluateLargeStoreGrowthBudget(3<<30, storeGrowthEvidence{
		DatabaseBytes:           3 << 30,
		ReclaimableBytes:        64 << 20,
		FilesystemFreeBytes:     20 << 30,
		FilesystemFreeAvailable: true,
	})
	if pass.Status != doctorStatusPass || !strings.Contains(pass.Message, "reclaimable=64.0 MiB") {
		t.Fatalf("pass=%#v", pass)
	}
	if strings.Contains(pass.Message, "unknowable") || strings.Contains(pass.Message, "不明") {
		t.Fatalf("pass still treats growth as unknown: %q", pass.Message)
	}
	warn := evaluateLargeStoreGrowthBudget(3<<30, storeGrowthEvidence{
		DatabaseBytes:           3 << 30,
		ReclaimableBytes:        400 << 20,
		FilesystemFreeBytes:     20 << 30,
		FilesystemFreeAvailable: true,
	})
	if warn.Status != doctorStatusWarn || !strings.Contains(warn.Message, "reclaimable") {
		t.Fatalf("warn=%#v", warn)
	}
	if !strings.Contains(warn.Message, "reclaimable=400.0 MiB") {
		t.Fatalf("warn message = %q", warn.Message)
	}
}

func TestLargeStoreSizeCheckFailSoftWhenInspectorErrors(t *testing.T) {
	snapshot := storeFileSnapshot{Size: 2 << 30, Regular: true, Exists: true}
	check := largeStoreSizeCheck(snapshot, "/tmp/large.db", apptypes.StorePageMetadata{}, errLargeStorePageMetadataUnavailable)
	if check.Status != doctorStatusWarn || !strings.Contains(check.Message, "unknown") {
		t.Fatalf("check=%#v", check)
	}
}

func TestRatioAtLeastIsOverflowSafe(t *testing.T) {
	maximum := int64(^uint64(0) >> 1)
	for _, tc := range []struct {
		name                     string
		part, total, denominator int64
		want                     bool
	}{
		{"max denominator one equal", maximum, maximum, 1, true},
		{"max denominator one below", maximum - 1, maximum, 1, false},
		{"max denominator ten equal", maximum/10 + 1, maximum, 10, true},
		{"max denominator ten below", maximum / 10, maximum, 10, false},
		{"max minus nine exact", (maximum-9)/10 + 1, maximum - 9, 10, true},
		{"max minus nine below", (maximum - 9) / 10, maximum - 9, 10, false},
		{"zero denominator", 1, maximum, 0, false},
		{"negative total", 1, -1, 10, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := ratioAtLeast(tc.part, tc.total, tc.denominator); got != tc.want {
				t.Fatalf("got=%t want=%t", got, tc.want)
			}
		})
	}
}
