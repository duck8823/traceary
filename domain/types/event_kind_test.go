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
			name:    "既知の event kind を受け付ける",
			value:   "note",
			want:    types.EventKindNote,
			wantErr: false,
		},
		{
			name:    "未知の event kind はエラー",
			value:   "unknown",
			wantErr: true,
		},
		{
			name:    "空文字はエラー",
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
				if tt.name == "未知の event kind はエラー" &&
					!strings.Contains(err.Error(), "有効な値: note, command_executed, reviewed, session_started, session_ended") {
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
