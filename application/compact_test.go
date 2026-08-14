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
		name   string
		gate   application.BodyGate
		force  bool
		refuse bool
	}{
		{name: "nothing to discard", gate: application.BodyGate{}},
		{name: "only covered material", gate: application.BodyGate{CoveredCount: 3, CoveredBytes: 10}},
		{
			name:   "unrefined only without force",
			gate:   application.BodyGate{UnrefinedSessions: 12, UnrefinedBytes: 100},
			refuse: true,
		},
		{
			name:  "unrefined only with force",
			gate:  application.BodyGate{UnrefinedSessions: 12, UnrefinedBytes: 100},
			force: true,
		},
		{
			name: "partial fold leaves unrefined",
			gate: application.BodyGate{CoveredCount: 1, UnrefinedSessions: 11, UnrefinedBytes: 90},
		},
		{
			name:  "partial fold with force",
			gate:  application.BodyGate{CoveredCount: 1, UnrefinedSessions: 11},
			force: true,
		},
	} {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := tc.gate.MustRefuse(tc.force); got != tc.refuse {
				t.Fatalf("MustRefuse() = %v, want %v", got, tc.refuse)
			}
		})
	}
}

func TestUnrefinedMaterialErrorNamesSkillAndForceCost(t *testing.T) {
	t.Parallel()
	err := application.UnrefinedMaterialError{Sessions: 1240, Bytes: 8 * 1024 * 1024 * 1024}
	got := err.Error()
	for _, want := range []string{
		"1,240 sessions have no refinement",
		"8.0 GiB",
		application.SessionRefineSkillName,
		"traceary session refine",
		"--force",
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

func TestForceCoverMustComplete(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name    string
		hasMore bool
		skipped int
		wantErr bool
	}{
		{name: "complete"},
		{name: "has more leftover", hasMore: true, wantErr: true},
		{name: "skipped ranges", skipped: 2, wantErr: true},
		{name: "both incomplete signals", hasMore: true, skipped: 1, wantErr: true},
	} {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			err := application.ForceCoverMustComplete(tc.hasMore, tc.skipped)
			if tc.wantErr && err == nil {
				t.Fatal("ForceCoverMustComplete() error = nil, want incomplete cover")
			}
			if !tc.wantErr && err != nil {
				t.Fatalf("ForceCoverMustComplete() error = %v", err)
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
