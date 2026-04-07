package types_test

import (
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
				return
			}
			if got != tt.want {
				t.Fatalf("EventKindOf() = %v, want %v", got, tt.want)
			}
		})
	}
}
