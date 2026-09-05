package application_test

import (
	"testing"
	"time"

	"github.com/duck8823/traceary/application"
)

func TestCompactStepsHaveNoDedupeArchive(t *testing.T) {
	t.Parallel()
	steps := application.CompactSteps{
		{Name: application.CompactStepProjectionReclaim},
	}
	if _, ok := steps.Find("dedupe_archive"); ok {
		t.Fatal(`CompactSteps.Find("dedupe_archive") must be absent`)
	}
	if _, ok := steps.Find("audit_encode"); ok {
		t.Fatal(`CompactSteps.Find("audit_encode") must be absent`)
	}
	if _, ok := steps.Find("mechanical_cover"); ok {
		t.Fatal(`CompactSteps.Find("mechanical_cover") must be absent`)
	}
}

func TestCompactCutoffUsesDefaultKeepDays(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 8, 14, 0, 0, 0, 0, time.UTC)
	got := application.CompactCutoff(now, 0)
	want := now.AddDate(0, 0, -application.DefaultCompactKeepDays)
	if !got.Equal(want) {
		t.Fatalf("CompactCutoff() = %s, want %s", got, want)
	}
}
