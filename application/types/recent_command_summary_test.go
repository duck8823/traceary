package types_test

import (
	"testing"
	"time"

	apptypes "github.com/duck8823/traceary/application/types"
	"github.com/duck8823/traceary/domain/types"
)

func TestRecentCommandSummaryOf_RecordsReturnedExtent(t *testing.T) {
	t.Parallel()
	extent, err := apptypes.EventBodyExtentOf(types.None[int](), 100, types.None[bool](), types.None[bool](), types.None[int]())
	if err != nil {
		t.Fatalf("EventBodyExtentOf() error = %v", err)
	}
	createdAt := time.Date(2026, 7, 22, 4, 0, 0, 0, time.UTC)
	summary, err := apptypes.RecentCommandSummaryOf(types.EventID("event-1"), "go test ./...", true, extent, createdAt)
	if err != nil {
		t.Fatalf("RecentCommandSummaryOf() error = %v", err)
	}
	if summary.ReturnedBytes() != len("go test ./...") || !summary.ResponseTruncated() {
		t.Fatalf("summary extent = returned %d truncated %t", summary.ReturnedBytes(), summary.ResponseTruncated())
	}

	if _, err := apptypes.RecentCommandSummaryOf(types.EventID("event-1"), "", false, extent, time.Time{}); err == nil {
		t.Fatal("RecentCommandSummaryOf(zero createdAt) error = nil")
	}
}
