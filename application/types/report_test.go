package types_test

import (
	"testing"
	"time"

	apptypes "github.com/duck8823/traceary/application/types"
	"github.com/duck8823/traceary/domain/types"
)

func TestReportCriteriaFrom_DefaultWindowPreservesOmittedBounds(t *testing.T) {
	t.Parallel()
	snapshot := time.Date(2026, 7, 22, 10, 11, 12, 13, time.UTC)
	criteria, err := apptypes.ReportCriteriaFrom("", "", "UTC", snapshot, "workspace", "codex", 500, 0)
	if err != nil {
		t.Fatalf("ReportCriteriaFrom() error = %v", err)
	}
	interval := criteria.Interval()
	if interval.HasRequestedFrom() || interval.HasRequestedTo() {
		t.Fatalf("requested bounds = [%q, %q), want omitted", interval.RequestedFrom(), interval.RequestedTo())
	}
	if !interval.EffectiveFromInclusive().Equal(snapshot.Add(-7*24*time.Hour)) || !interval.EffectiveToExclusive().Equal(snapshot) {
		t.Fatalf("effective interval = [%s, %s)", interval.EffectiveFromInclusive(), interval.EffectiveToExclusive())
	}
	if criteria.Workspace() != types.Workspace("workspace") || criteria.Client() != types.Client("codex") || criteria.PageSize() != 500 || criteria.ResultCap() != 0 {
		t.Fatalf("criteria = workspace=%q client=%q page=%d cap=%d", criteria.Workspace(), criteria.Client(), criteria.PageSize(), criteria.ResultCap())
	}
}

func TestReportCriteriaFrom_RejectsAmbiguousOrInvalidLimits(t *testing.T) {
	t.Parallel()
	snapshot := time.Date(2026, 7, 22, 10, 0, 0, 0, time.UTC)
	tests := []struct {
		name     string
		from     string
		to       string
		pageSize int
		cap      int
	}{
		{name: "upper bound without lower bound", to: "2026-07-22", pageSize: 1},
		{name: "zero page size", from: "2026-07-21", pageSize: 0},
		{name: "negative result cap", from: "2026-07-21", pageSize: 1, cap: -1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if _, err := apptypes.ReportCriteriaFrom(tt.from, tt.to, "UTC", snapshot, "", "", tt.pageSize, tt.cap); err == nil {
				t.Fatal("ReportCriteriaFrom() error = nil, want error")
			}
		})
	}
}

func TestReportSourceExtentOf_DistinguishesCompleteAndPartial(t *testing.T) {
	t.Parallel()
	earlier := time.Date(2026, 7, 20, 1, 0, 0, 123, time.UTC)
	later := time.Date(2026, 7, 21, 2, 0, 0, 456, time.UTC)

	complete, err := apptypes.ReportSourceExtentOf([]time.Time{later, earlier}, 100, 0, false)
	if err != nil {
		t.Fatalf("ReportSourceExtentOf(complete) error = %v", err)
	}
	if complete.Coverage != apptypes.ReportCoverageComplete || complete.ResponseTruncated || complete.ObservedCount != 2 {
		t.Fatalf("complete extent = %+v", complete)
	}
	if complete.ObservedEarliestAt != earlier.Format(time.RFC3339Nano) || complete.ObservedLatestAt != later.Format(time.RFC3339Nano) {
		t.Fatalf("complete observed range = [%q, %q]", complete.ObservedEarliestAt, complete.ObservedLatestAt)
	}

	partial, err := apptypes.ReportSourceExtentOf([]time.Time{later}, 100, 1, true)
	if err != nil {
		t.Fatalf("ReportSourceExtentOf(partial) error = %v", err)
	}
	if partial.Coverage != apptypes.ReportCoveragePartial || !partial.ResponseTruncated || partial.TruncationReason != "result_cap" || partial.ResultCap != 1 {
		t.Fatalf("partial extent = %+v", partial)
	}
}

func TestReportSourceExtentOf_RejectsTruncationWithoutObservedRows(t *testing.T) {
	t.Parallel()
	if _, err := apptypes.ReportSourceExtentOf(nil, 100, 1, true); err == nil {
		t.Fatal("ReportSourceExtentOf() error = nil, want error")
	}
}
