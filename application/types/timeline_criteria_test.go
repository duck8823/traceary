package types_test

import (
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"

	apptypes "github.com/duck8823/traceary/application/types"
	domtypes "github.com/duck8823/traceary/domain/types"
)

func TestTimelineCriteriaBuilder_DefaultsLimitOnly(t *testing.T) {
	t.Parallel()

	criteria := apptypes.NewTimelineCriteriaBuilder(20).Build()

	if diff := cmp.Diff(20, criteria.Limit()); diff != "" {
		t.Errorf("Limit() mismatch (-want +got):\n%s", diff)
	}
	if diff := cmp.Diff(domtypes.Workspace(""), criteria.Workspace()); diff != "" {
		t.Errorf("Workspace() mismatch (-want +got):\n%s", diff)
	}
	if !criteria.From().IsZero() {
		t.Errorf("From() = %v, want zero", criteria.From())
	}
	if !criteria.To().IsZero() {
		t.Errorf("To() = %v, want zero", criteria.To())
	}
	if diff := cmp.Diff(0, criteria.GapSeconds()); diff != "" {
		t.Errorf("GapSeconds() mismatch (-want +got):\n%s", diff)
	}
}

func TestTimelineCriteriaBuilder_AllSettersChained(t *testing.T) {
	t.Parallel()

	from := time.Date(2024, time.January, 1, 0, 0, 0, 0, time.UTC)
	to := time.Date(2024, time.January, 2, 0, 0, 0, 0, time.UTC)

	criteria := apptypes.NewTimelineCriteriaBuilder(50).
		Workspace(domtypes.Workspace("github.com/org/repo")).
		From(from).
		To(to).
		GapSeconds(1800).
		Build()

	if diff := cmp.Diff(50, criteria.Limit()); diff != "" {
		t.Errorf("Limit() mismatch (-want +got):\n%s", diff)
	}
	if diff := cmp.Diff(domtypes.Workspace("github.com/org/repo"), criteria.Workspace()); diff != "" {
		t.Errorf("Workspace() mismatch (-want +got):\n%s", diff)
	}
	if !criteria.From().Equal(from) {
		t.Errorf("From() = %v, want %v", criteria.From(), from)
	}
	if !criteria.To().Equal(to) {
		t.Errorf("To() = %v, want %v", criteria.To(), to)
	}
	if diff := cmp.Diff(1800, criteria.GapSeconds()); diff != "" {
		t.Errorf("GapSeconds() mismatch (-want +got):\n%s", diff)
	}
}
