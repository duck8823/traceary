package mcpserver_test

import (
	"testing"
	"time"

	"github.com/duck8823/traceary/presentation/mcpserver"
)

func TestParseFlexibleTime(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name         string
		value        string
		endExclusive bool
		want         time.Time
		wantErr      bool
	}{
		{
			name:  "empty string returns zero time",
			value: "",
			want:  time.Time{},
		},
		{
			name:  "RFC3339 format parsed correctly",
			value: "2026-04-10T12:00:00Z",
			want:  time.Date(2026, 4, 10, 12, 0, 0, 0, time.UTC),
		},
		{
			name:  "YYYY-MM-DD format parsed as start of day",
			value: "2026-04-10",
			want:  time.Date(2026, 4, 10, 0, 0, 0, 0, time.UTC),
		},
		{
			name:         "YYYY-MM-DD with endExclusive returns next day",
			value:        "2026-04-10",
			endExclusive: true,
			want:         time.Date(2026, 4, 11, 0, 0, 0, 0, time.UTC),
		},
		{
			name:    "invalid format returns error",
			value:   "not-a-date",
			wantErr: true,
		},
		{
			name:  "whitespace is trimmed",
			value: "  2026-04-10  ",
			want:  time.Date(2026, 4, 10, 0, 0, 0, 0, time.UTC),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got, err := mcpserver.ParseFlexibleTime(tt.value, tt.endExclusive)
			if (err != nil) != tt.wantErr {
				t.Fatalf("parseFlexibleTime(%q) error = %v, wantErr %v", tt.value, err, tt.wantErr)
			}
			if !tt.wantErr && !got.Equal(tt.want) {
				t.Errorf("parseFlexibleTime(%q) = %v, want %v", tt.value, got, tt.want)
			}
		})
	}
}

func TestResolveLimit(t *testing.T) {
	t.Parallel()

	if got := mcpserver.ResolveLimit(5, 10); got != 5 {
		t.Errorf("ResolveLimit(5, 10) = %d, want 5", got)
	}
	if got := mcpserver.ResolveLimit(0, 10); got != 10 {
		t.Errorf("ResolveLimit(0, 10) = %d, want 10", got)
	}
	if got := mcpserver.ResolveLimit(-1, 10); got != 10 {
		t.Errorf("ResolveLimit(-1, 10) = %d, want 10", got)
	}
}

func TestResolveOffset(t *testing.T) {
	t.Parallel()

	if got := mcpserver.ResolveOffset(5); got != 5 {
		t.Errorf("ResolveOffset(5) = %d, want 5", got)
	}
	if got := mcpserver.ResolveOffset(0); got != 0 {
		t.Errorf("ResolveOffset(0) = %d, want 0", got)
	}
	if got := mcpserver.ResolveOffset(-1); got != 0 {
		t.Errorf("ResolveOffset(-1) = %d, want 0", got)
	}
}
