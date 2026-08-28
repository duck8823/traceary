package sqlite

import "testing"

func TestPayloadLanesUnchanged(t *testing.T) {
	t.Parallel()
	if len(backfillLanes) != 4 {
		t.Fatalf("backfillLanes len = %d, want 4", len(backfillLanes))
	}
	want := []backfillLane{
		{Table: "events", Field: "body", Column: "body", CodecPrefix: "body", PKColumn: "id", Ord: 0},
		{Table: "command_audits", Field: "command", Column: "command_text", CodecPrefix: "command", PKColumn: "event_id", Ord: 1},
		{Table: "command_audits", Field: "input", Column: "input_text", CodecPrefix: "input", PKColumn: "event_id", Ord: 2},
		{Table: "command_audits", Field: "output", Column: "output_text", CodecPrefix: "output", PKColumn: "event_id", Ord: 3},
	}
	for i, lane := range backfillLanes {
		if lane != want[i] {
			t.Fatalf("backfillLanes[%d] = %+v, want %+v", i, lane, want[i])
		}
	}
	audits := commandAuditLanes()
	if len(audits) != 3 {
		t.Fatalf("commandAuditLanes len = %d, want 3", len(audits))
	}
	for i, lane := range audits {
		if lane.Table != "command_audits" {
			t.Fatalf("commandAuditLanes()[%d].Table = %q, want command_audits", i, lane.Table)
		}
		if lane != want[i+1] {
			t.Fatalf("commandAuditLanes()[%d] = %+v, want %+v", i, lane, want[i+1])
		}
	}
}
