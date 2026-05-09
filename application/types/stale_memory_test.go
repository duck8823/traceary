package types_test

import (
	"testing"
	"time"

	apptypes "github.com/duck8823/traceary/application/types"
	domtypes "github.com/duck8823/traceary/domain/types"
)

func TestStaleMemoryReasonFrom(t *testing.T) {
	t.Parallel()

	got, err := apptypes.StaleMemoryReasonFrom("overlap")
	if err != nil {
		t.Fatalf("StaleMemoryReasonFrom() error = %v", err)
	}
	if got != apptypes.StaleMemoryReasonOverlap {
		t.Fatalf("StaleMemoryReasonFrom() = %q, want overlap", got)
	}

	if _, err := apptypes.StaleMemoryReasonFrom("unknown"); err == nil {
		t.Fatal("StaleMemoryReasonFrom(unknown) error = nil, want error")
	}
}

func TestStaleMemoryListResultOfClonesItems(t *testing.T) {
	t.Parallel()

	summary, err := apptypes.MemorySummaryOf(
		domtypes.MemoryID("mem-stale"),
		domtypes.MemoryTypeDecision,
		domtypes.WorkspaceScopeOf(domtypes.Workspace("duck8823/traceary")),
		"stale memory fact",
		domtypes.MemoryStatusSuperseded,
		domtypes.ConfidenceMedium,
		domtypes.MemorySourceManual,
		domtypes.None[domtypes.MemoryID](),
		domtypes.None[time.Time](),
		time.Date(2026, 4, 10, 12, 0, 0, 0, time.UTC),
		domtypes.None[time.Time](),
		time.Date(2026, 4, 10, 12, 0, 0, 0, time.UTC),
		time.Date(2026, 4, 10, 12, 0, 0, 0, time.UTC),
	)
	if err != nil {
		t.Fatalf("MemorySummaryOf() error = %v", err)
	}
	row, err := apptypes.StaleMemoryRowOf(summary, apptypes.StaleMemoryReasonSuperseded)
	if err != nil {
		t.Fatalf("StaleMemoryRowOf() error = %v", err)
	}
	items := []apptypes.StaleMemoryRow{row}
	result, err := apptypes.StaleMemoryListResultOf(2, items)
	if err != nil {
		t.Fatalf("StaleMemoryListResultOf() error = %v", err)
	}
	items[0], _ = apptypes.StaleMemoryRowOf(summary, apptypes.StaleMemoryReasonExpired)

	gotItems := result.Items()
	if gotItems[0].Reason() != apptypes.StaleMemoryReasonSuperseded {
		t.Fatalf("result item reason = %q, want superseded", gotItems[0].Reason())
	}
	gotItems[0], _ = apptypes.StaleMemoryRowOf(summary, apptypes.StaleMemoryReasonExpired)
	if result.Items()[0].Reason() != apptypes.StaleMemoryReasonSuperseded {
		t.Fatalf("Items() did not return a defensive copy")
	}
}
