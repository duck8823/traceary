package types

import (
	"testing"
	"time"
)

func TestRequestedIntervalFrom_DateOnlyUsesExplicitTimezone(t *testing.T) {
	t.Parallel()

	interval, err := RequestedIntervalFrom("2026-07-16", "2026-07-16", "Asia/Tokyo", time.Time{})
	if err != nil {
		t.Fatalf("RequestedIntervalFrom() error = %v", err)
	}
	assertTimeEqual(t, interval.EffectiveFromInclusive(), time.Date(2026, 7, 15, 15, 0, 0, 0, time.UTC))
	assertTimeEqual(t, interval.EffectiveToExclusive(), time.Date(2026, 7, 16, 15, 0, 0, 0, time.UTC))
	if interval.RequestedTo() != "2026-07-16" || !interval.ToIsDateOnly() {
		t.Fatalf("requested date metadata = %q, dateOnly=%v", interval.RequestedTo(), interval.ToIsDateOnly())
	}
}

func TestRequestedIntervalFrom_DateOnlyUsesCalendarArithmeticAcrossDST(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		date string
		want time.Duration
	}{
		{name: "spring forward", date: "2026-03-08", want: 23 * time.Hour},
		{name: "fall back", date: "2026-11-01", want: 25 * time.Hour},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			interval, err := RequestedIntervalFrom(tt.date, tt.date, "America/New_York", time.Time{})
			if err != nil {
				t.Fatalf("RequestedIntervalFrom() error = %v", err)
			}
			if got := interval.EffectiveToExclusive().Sub(interval.EffectiveFromInclusive()); got != tt.want {
				t.Fatalf("effective duration = %s, want %s", got, tt.want)
			}
		})
	}
}

func TestRequestedIntervalFrom_RFC3339EndRemainsExactExclusiveInstant(t *testing.T) {
	t.Parallel()

	interval, err := RequestedIntervalFrom("2026-07-16T00:00:00+09:00", "2026-07-16T12:34:56.123+09:00", "America/New_York", time.Time{})
	if err != nil {
		t.Fatalf("RequestedIntervalFrom() error = %v", err)
	}
	assertTimeEqual(t, interval.EffectiveToExclusive(), time.Date(2026, 7, 16, 3, 34, 56, 123000000, time.UTC))
	if interval.ToIsDateOnly() {
		t.Fatal("RFC3339 upper bound must not be marked date-only")
	}
}

func TestRequestedIntervalFrom_OmittedEndUsesSnapshot(t *testing.T) {
	t.Parallel()

	snapshot := time.Date(2026, 7, 22, 10, 11, 12, 13, time.FixedZone("JST", 9*60*60))
	interval, err := RequestedIntervalFrom("2026-07-16", "", "", snapshot)
	if err != nil {
		t.Fatalf("RequestedIntervalFrom() error = %v", err)
	}
	assertTimeEqual(t, interval.EffectiveToExclusive(), snapshot.UTC())
	if interval.Timezone() != "UTC" || interval.HasRequestedTo() {
		t.Fatalf("timezone=%q requestedTo=%q", interval.Timezone(), interval.RequestedTo())
	}
}

func TestRequestedIntervalFrom_RejectsInvalidInput(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		from string
		to   string
		zone string
	}{
		{name: "timezone", from: "2026-07-16", to: "2026-07-17", zone: "Mars/Olympus"},
		{name: "date", from: "not-a-date", to: "2026-07-17", zone: "UTC"},
		{name: "instant order", from: "2026-07-17T00:00:00Z", to: "2026-07-17T00:00:00Z", zone: "UTC"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if _, err := RequestedIntervalFrom(tt.from, tt.to, tt.zone, time.Time{}); err == nil {
				t.Fatal("RequestedIntervalFrom() error = nil, want error")
			}
		})
	}
}

func assertTimeEqual(t *testing.T, got, want time.Time) {
	t.Helper()
	if !got.Equal(want) {
		t.Fatalf("time = %s, want %s", got.Format(time.RFC3339Nano), want.Format(time.RFC3339Nano))
	}
}
