package cli

import (
	"testing"
	"time"
)

func TestParseFlexibleTime(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name         string
		value        string
		endExclusive bool
		wantTime     time.Time
		wantErr      bool
	}{
		{
			name:     "empty string returns zero time",
			value:    "",
			wantTime: time.Time{},
		},
		{
			name:     "whitespace-only returns zero time",
			value:    "  ",
			wantTime: time.Time{},
		},
		{
			name:     "YYYY-MM-DD returns midnight UTC",
			value:    "2026-04-11",
			wantTime: time.Date(2026, 4, 11, 0, 0, 0, 0, time.UTC),
		},
		{
			name:         "YYYY-MM-DD with endExclusive adds one day",
			value:        "2026-04-11",
			endExclusive: true,
			wantTime:     time.Date(2026, 4, 12, 0, 0, 0, 0, time.UTC),
		},
		{
			name:     "RFC3339 is parsed and converted to UTC",
			value:    "2026-04-11T15:30:00Z",
			wantTime: time.Date(2026, 4, 11, 15, 30, 0, 0, time.UTC),
		},
		{
			name:     "RFC3339 with timezone offset is normalized to UTC",
			value:    "2026-04-11T15:30:00+09:00",
			wantTime: time.Date(2026, 4, 11, 6, 30, 0, 0, time.UTC),
		},
		{
			name:         "RFC3339 with endExclusive does not add one day",
			value:        "2026-04-11T15:30:00Z",
			endExclusive: true,
			wantTime:     time.Date(2026, 4, 11, 15, 30, 0, 0, time.UTC),
		},
		{
			name:    "invalid format returns error",
			value:   "not-a-date",
			wantErr: true,
		},
		{
			name:    "invalid month returns error",
			value:   "2026-13-01",
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got, err := parseFlexibleTime(tt.value, tt.endExclusive)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("expected error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if !got.Equal(tt.wantTime) {
				t.Errorf("got %v, want %v", got, tt.wantTime)
			}
		})
	}
}

func TestParseFlexibleTimeOptional(t *testing.T) {
	t.Parallel()

	t.Run("empty string returns empty Optional", func(t *testing.T) {
		t.Parallel()

		got, err := parseFlexibleTimeOptional("", false)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got.IsPresent() {
			t.Errorf("expected empty Optional, got %v", got.Get())
		}
	})

	t.Run("valid date returns present Optional", func(t *testing.T) {
		t.Parallel()

		got, err := parseFlexibleTimeOptional("2026-04-11", false)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !got.IsPresent() {
			t.Fatal("expected present Optional")
		}
		want := time.Date(2026, 4, 11, 0, 0, 0, 0, time.UTC)
		if !got.Get().Equal(want) {
			t.Errorf("got %v, want %v", got.Get(), want)
		}
	})

	t.Run("invalid value returns error", func(t *testing.T) {
		t.Parallel()

		_, err := parseFlexibleTimeOptional("bad", false)
		if err == nil {
			t.Fatal("expected error, got nil")
		}
	})
}
