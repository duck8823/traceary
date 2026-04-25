package cli

import (
	"bytes"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
)

func TestFormatJSONTime_UsesUTCAndRFC3339Nano(t *testing.T) {
	t.Parallel()

	loc := time.FixedZone("JST", 9*60*60)
	got := formatJSONTime(time.Date(2026, 4, 25, 12, 34, 56, 123456789, loc))
	want := "2026-04-25T03:34:56.123456789Z"
	if diff := cmp.Diff(want, got); diff != "" {
		t.Fatalf("formatJSONTime mismatch (-want +got):\n%s", diff)
	}
}

func TestNamedJSONRootShapes(t *testing.T) {
	t.Parallel()

	endedAt := "2026-04-25T03:35:56.123456789Z"
	duration := 60.5
	payload := []sessionSummaryOutput{{
		SessionID:       "session-1",
		Workspace:       "workspace-1",
		StartedAt:       "2026-04-25T03:34:55.623456789Z",
		EndedAt:         &endedAt,
		Status:          "ended",
		DurationSec:     &duration,
		TotalEvents:     2,
		CommandCount:    1,
		Agents:          []string{"codex"},
		ParentSessionID: "parent-1",
	}}

	var out bytes.Buffer
	if err := writeJSON(&out, payload); err != nil {
		t.Fatalf("writeJSON: %v", err)
	}
	want := `[
  {
    "session_id": "session-1",
    "workspace": "workspace-1",
    "parent_session_id": "parent-1",
    "started_at": "2026-04-25T03:34:55.623456789Z",
    "ended_at": "2026-04-25T03:35:56.123456789Z",
    "status": "ended",
    "duration_sec": 60.5,
    "total_events": 2,
    "command_count": 1,
    "agents": [
      "codex"
    ]
  }
]
`
	if diff := cmp.Diff(want, out.String()); diff != "" {
		t.Fatalf("session summary JSON shape mismatch (-want +got):\n%s", diff)
	}
}

func TestTimelineBlockOutputShape(t *testing.T) {
	t.Parallel()

	payload := []timelineBlockOutput{{
		Start:       "2026-04-25T03:00:00.123456789Z",
		End:         "2026-04-25T03:15:00.123456789Z",
		DurationSec: 900,
		EventCount:  3,
		Workspaces:  []string{"ws"},
		Agents:      []string{"codex"},
		KindCounts:  map[string]int{"note": 3},
		WorkspaceBreakdown: []timelineWorkspaceBreakdownOutput{{
			Workspace:     "ws",
			EventCount:    3,
			KindCounts:    map[string]int{"note": 3},
			Agents:        []string{"codex"},
			Summary:       "note: 3",
			SummarySource: "kind_counts",
		}},
	}}

	var out bytes.Buffer
	if err := writeJSON(&out, payload); err != nil {
		t.Fatalf("writeJSON: %v", err)
	}
	want := `[
  {
    "start": "2026-04-25T03:00:00.123456789Z",
    "end": "2026-04-25T03:15:00.123456789Z",
    "duration_sec": 900,
    "event_count": 3,
    "workspaces": [
      "ws"
    ],
    "agents": [
      "codex"
    ],
    "kind_counts": {
      "note": 3
    },
    "workspace_breakdown": [
      {
        "workspace": "ws",
        "event_count": 3,
        "kind_counts": {
          "note": 3
        },
        "agents": [
          "codex"
        ],
        "summary": "note: 3",
        "summary_source": "kind_counts"
      }
    ]
  }
]
`
	if diff := cmp.Diff(want, out.String()); diff != "" {
		t.Fatalf("timeline JSON shape mismatch (-want +got):\n%s", diff)
	}
}
