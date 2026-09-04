package application_test

import (
	"strings"
	"testing"
	"time"

	"github.com/duck8823/traceary/application"
)

func TestBodyGateMustRefuse(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name            string
		gate            application.BodyGate
		refuseUnrefined bool
		refuse          bool
	}{
		{name: "nothing to discard", gate: application.BodyGate{}},
		{name: "only covered material", gate: application.BodyGate{CoveredCount: 3, CoveredBytes: 10}},
		{
			name: "unrefined only without refuse",
			gate: application.BodyGate{UnrefinedSessions: 12, UnrefinedBytes: 100},
		},
		{
			name:            "unrefined only with refuse",
			gate:            application.BodyGate{UnrefinedSessions: 12, UnrefinedBytes: 100},
			refuseUnrefined: true,
			refuse:          true,
		},
		{
			name: "partial fold leaves unrefined",
			gate: application.BodyGate{CoveredCount: 1, UnrefinedSessions: 11, UnrefinedBytes: 90},
		},
		{
			name:            "partial fold with refuse",
			gate:            application.BodyGate{CoveredCount: 1, UnrefinedSessions: 11},
			refuseUnrefined: true,
		},
	} {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := tc.gate.MustRefuse(tc.refuseUnrefined); got != tc.refuse {
				t.Fatalf("MustRefuse() = %v, want %v", got, tc.refuse)
			}
		})
	}
}

func TestUnrefinedMaterialErrorNamesSkillAndRefuseCost(t *testing.T) {
	t.Parallel()
	err := application.UnrefinedMaterialError{Sessions: 1240, Bytes: 8 * 1024 * 1024 * 1024}
	got := err.Error()
	for _, want := range []string{
		"1,240 sessions have no refinement",
		"8.0 GiB",
		application.SessionRefineSkillName,
		"traceary session refine",
		"--refuse-unrefined",
		"why",
	} {
		if !strings.Contains(got, want) && want == "1,240 sessions have no refinement" {
			if !strings.Contains(got, "1240 sessions have no refinement") {
				t.Fatalf("error %q does not name the session count", got)
			}
			continue
		}
		if !strings.Contains(got, want) {
			t.Fatalf("error %q does not contain %q", got, want)
		}
	}
}

func TestCompactStepsHaveNoDedupeArchive(t *testing.T) {
	t.Parallel()
	steps := application.CompactSteps{
		{Name: application.CompactStepProjectionReclaim},
		{Name: application.CompactStepMechanicalCover},
	}
	if _, ok := steps.Find("dedupe_archive"); ok {
		t.Fatal(`CompactSteps.Find("dedupe_archive") must be absent`)
	}
	if _, ok := steps.Find("audit_encode"); ok {
		t.Fatal(`CompactSteps.Find("audit_encode") must be absent`)
	}
}

func TestForceCoverSafeToDelete(t *testing.T) {
	t.Parallel()
	cutoff := time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC)
	older := cutoff.Add(-time.Hour)
	newer := cutoff.Add(time.Hour)
	for _, tc := range []struct {
		name                string
		hasMore             bool
		earliestUnprocessed *time.Time
		skipped             int
		earliestSkipped     *time.Time
		wantErr             bool
	}{
		{name: "complete"},
		{
			name:    "has more but all newer than cutoff",
			hasMore: true, earliestUnprocessed: &newer,
		},
		{
			name:    "has more and unknown earliest time",
			hasMore: true, wantErr: true,
		},
		{
			name:    "has more and unprocessed older than cutoff",
			hasMore: true, earliestUnprocessed: &older, wantErr: true,
		},
		{
			name:    "skipped but newer than cutoff",
			skipped: 2, earliestSkipped: &newer,
		},
		{
			name:    "skipped with unknown earliest time",
			skipped: 2, wantErr: true,
		},
		{
			name:    "skipped older than cutoff",
			skipped: 2, earliestSkipped: &older, wantErr: true,
		},
		{
			name:                "both incomplete signals but both newer than cutoff",
			hasMore:             true,
			earliestUnprocessed: &newer,
			skipped:             1,
			earliestSkipped:     &newer,
		},
	} {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			err := application.ForceCoverSafeToDelete(
				tc.hasMore, tc.earliestUnprocessed, tc.skipped, tc.earliestSkipped, cutoff,
			)
			if tc.wantErr && err == nil {
				t.Fatal("ForceCoverSafeToDelete() error = nil, want incomplete cover")
			}
			if !tc.wantErr && err != nil {
				t.Fatalf("ForceCoverSafeToDelete() error = %v", err)
			}
		})
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
