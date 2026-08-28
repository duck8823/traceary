package cli

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	apptypes "github.com/duck8823/traceary/application/types"
)

func TestDoctorInventoriesDedupeArchiveRuns(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 8, 29, 0, 0, 0, 0, time.UTC)
	internal := apptypes.ContentEventDedupeRun{
		RunID:            "compact-copy-filter-deadbeef",
		OldestArchivedAt: "2026-08-18T00:00:00Z",
		QuarantinedRows:  12,
		BodyBytes:        4096,
		Internal:         true,
	}
	operator := apptypes.ContentEventDedupeRun{
		RunID:            "run-1",
		OldestArchivedAt: "2026-08-20T00:00:00Z",
		QuarantinedRows:  3,
		BodyBytes:        128,
		Internal:         false,
	}
	warn := dedupeArchiveRunsDoctorCheck([]apptypes.ContentEventDedupeRun{internal, operator}, nil, now)
	if warn.Name != "dedupe-archive-runs" {
		t.Fatalf("name=%q", warn.Name)
	}
	if warn.Status != doctorStatusWarn {
		t.Fatalf("status=%q, want warn", warn.Status)
	}
	if !strings.Contains(warn.Hint, "traceary store compact") {
		t.Fatalf("hint=%q", warn.Hint)
	}
	raw, err := json.Marshal(warn)
	if err != nil {
		t.Fatal(err)
	}
	var payload struct {
		DedupeArchiveRuns []doctorDedupeArchiveRun `json:"dedupe_archive_runs"`
	}
	if err := json.Unmarshal(raw, &payload); err != nil {
		t.Fatal(err)
	}
	if len(payload.DedupeArchiveRuns) != 2 {
		t.Fatalf("inventory=%d, want 2: %s", len(payload.DedupeArchiveRuns), raw)
	}
	byID := map[string]doctorDedupeArchiveRun{}
	for _, run := range payload.DedupeArchiveRuns {
		byID[run.RunID] = run
	}
	got := byID["compact-copy-filter-deadbeef"]
	if got.Rows != 12 || got.Bytes != 4096 || got.OldestArchivedAt == "" || got.PlannedRelease != "next replica/external store compact" {
		t.Fatalf("internal inventory=%+v", got)
	}
	op := byID["run-1"]
	if op.PlannedRelease == "" || !strings.HasPrefix(op.PlannedRelease, "2026-11-18") {
		t.Fatalf("operator planned_release=%q", op.PlannedRelease)
	}

	pass := dedupeArchiveRunsDoctorCheck([]apptypes.ContentEventDedupeRun{operator}, nil, now)
	if pass.Status != doctorStatusPass {
		t.Fatalf("operator-only status=%q, want pass", pass.Status)
	}
	if len(pass.DedupeArchiveRuns) != 1 {
		t.Fatalf("operator-only inventory=%d, want 1", len(pass.DedupeArchiveRuns))
	}
}
