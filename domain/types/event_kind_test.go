package types_test

import (
	"strings"
	"testing"

	"github.com/duck8823/traceary/domain/types"
)

func TestEventKindOf(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		value   string
		want    types.EventKind
		wantErr bool
	}{
		{
			name:    "accepts known event kind",
			value:   "note",
			want:    types.EventKindNote,
			wantErr: false,
		},
		{
			name:    "returns error for unknown event kind",
			value:   "unknown",
			wantErr: true,
		},
		{
			name:    "returns error for empty value",
			value:   " ",
			wantErr: true,
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got, err := types.EventKindOf(tt.value)
			if (err != nil) != tt.wantErr {
				t.Fatalf("EventKindOf() error = %v, wantErr %v", err, tt.wantErr)
			}
			if tt.wantErr {
				if tt.name == "returns error for unknown event kind" &&
					!strings.Contains(err.Error(), "allowed values: note, command_executed, reviewed, session_started, session_ended") {
					t.Fatalf("error = %q, want valid kind list", err.Error())
				}
				return
			}
			if got != tt.want {
				t.Fatalf("EventKindOf() = %v, want %v", got, tt.want)
			}
		})
	}
}
