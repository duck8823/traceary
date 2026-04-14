package mcpserver_test

import (
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"

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
			if tt.wantErr {
				return
			}
			if diff := cmp.Diff(tt.want, got); diff != "" {
				t.Errorf("ParseFlexibleTime(%q) mismatch (-want +got):\n%s", tt.value, diff)
			}
		})
	}
}

func TestResolveLimit(t *testing.T) {
	t.Parallel()

	if diff := cmp.Diff(5, mcpserver.ResolveLimit(5, 10)); diff != "" {
		t.Errorf("ResolveLimit(5, 10) mismatch (-want +got):\n%s", diff)
	}
	if diff := cmp.Diff(10, mcpserver.ResolveLimit(0, 10)); diff != "" {
		t.Errorf("ResolveLimit(0, 10) mismatch (-want +got):\n%s", diff)
	}
	if diff := cmp.Diff(10, mcpserver.ResolveLimit(-1, 10)); diff != "" {
		t.Errorf("ResolveLimit(-1, 10) mismatch (-want +got):\n%s", diff)
	}
}

func TestResolveOffset(t *testing.T) {
	t.Parallel()

	if diff := cmp.Diff(5, mcpserver.ResolveOffset(5)); diff != "" {
		t.Errorf("ResolveOffset(5) mismatch (-want +got):\n%s", diff)
	}
	if diff := cmp.Diff(0, mcpserver.ResolveOffset(0)); diff != "" {
		t.Errorf("ResolveOffset(0) mismatch (-want +got):\n%s", diff)
	}
	if diff := cmp.Diff(0, mcpserver.ResolveOffset(-1)); diff != "" {
		t.Errorf("ResolveOffset(-1) mismatch (-want +got):\n%s", diff)
	}
}
