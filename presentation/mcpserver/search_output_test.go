package mcpserver

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"

	apptypes "github.com/duck8823/traceary/application/types"
)

func TestSearchOutputReasonsShadowSerializesEventAndSessionReasons(t *testing.T) {
	encoded, err := json.Marshal(searchOutput{
		eventsOutput: eventsOutput{Reasons: []string{"more_results"}},
		Reasons:      []string{"more_results", "session_matches"},
	})
	if err != nil {
		t.Fatalf("json.Marshal() error = %v", err)
	}
	var payload struct {
		Reasons []string `json:"reasons"`
	}
	if err := json.Unmarshal(encoded, &payload); err != nil {
		t.Fatalf("json.Unmarshal() error = %v", err)
	}
	if diff := cmp.Diff([]string{"more_results", "session_matches"}, payload.Reasons); diff != "" {
		t.Fatalf("serialized reasons mismatch (-want +got):\n%s", diff)
	}
}

func TestConvertSearchSessions(t *testing.T) {
	t.Parallel()

	startedAt := time.Date(2026, 8, 11, 9, 10, 11, 123000000, time.FixedZone("JST", 9*60*60))
	tests := []struct {
		name string
		hits []apptypes.SearchSessionHit
		want []searchSessionOutput
	}{
		{
			name: "converts the session trail fields",
			hits: []apptypes.SearchSessionHit{
				apptypes.SearchSessionHitOf("session-old", "summary with needle", 12, startedAt),
			},
			want: []searchSessionOutput{{
				SessionID: "session-old", Summary: "summary with needle", EventCount: 12,
				StartedAt: "2026-08-11T00:10:11.123Z",
			}},
		},
		{name: "empty hits stay omitted", hits: nil, want: nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if diff := cmp.Diff(tt.want, convertSearchSessions(tt.hits)); diff != "" {
				t.Fatalf("convertSearchSessions() mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

func TestSessionIDsFromEventOutputs(t *testing.T) {
	t.Parallel()

	got := sessionIDsFromEventOutputs([]eventOutput{
		{SessionID: "session-a"},
		{SessionID: ""},
		{SessionID: "session-a"},
		{SessionID: "session-b"},
	})
	want := []string{"session-a", "session-b"}
	ids := make([]string, 0, len(got))
	for _, id := range got {
		ids = append(ids, id.String())
	}
	if diff := cmp.Diff(want, ids); diff != "" {
		t.Fatalf("sessionIDsFromEventOutputs() mismatch (-want +got):\n%s", diff)
	}
}
