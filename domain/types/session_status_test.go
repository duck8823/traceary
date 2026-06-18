package types_test

import (
	"testing"

	"github.com/duck8823/traceary/domain/types"
)

func TestSessionStatusFrom(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		input   string
		want    types.SessionStatus
		wantErr bool
	}{
		{name: "active", input: "active", want: types.SessionStatusActive},
		{name: "stale", input: "stale", want: types.SessionStatusStale},
		{name: "ended", input: "ended", want: types.SessionStatusEnded},
		{name: "ended_with_late_events", input: "ended_with_late_events", want: types.SessionStatusEndedWithLateEvents},
		{name: "rejects empty", input: "", wantErr: true},
		{name: "rejects unknown", input: "reopened", wantErr: true},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, err := types.SessionStatusFrom(tt.input)
			if (err != nil) != tt.wantErr {
				t.Fatalf("SessionStatusFrom() error = %v, wantErr %v", err, tt.wantErr)
			}
			if !tt.wantErr && got != tt.want {
				t.Fatalf("SessionStatusFrom() = %v, want %v", got, tt.want)
			}
		})
	}
}
