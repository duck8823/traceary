package cli

import (
	"strings"
	"testing"

	apptypes "github.com/duck8823/traceary/application/types"
)

func TestBuildOperatorCostCheckReportsThisStore(t *testing.T) {
	t.Setenv("TRACEARY_LANG", "en")
	check := buildOperatorCostCheck(apptypes.OperatorCostReport{
		ResidentBytes:                       7360,
		ResidentBytesPerEvent:               7360,
		ResidentBytesPerSession:             41700,
		UndiscardableBytesPerEvent:          2670,
		Amplification:                       2.55,
		EventsPerDay:                        10000,
		ProjectedUndiscardableBytesPerMonth: 800 << 20,
		Evidence:                            apptypes.OperatorCostEvidence{Status: "complete"},
	})
	if check.Name != "store-operator-cost" || check.Status != doctorStatusPass {
		t.Fatalf("check = %#v", check)
	}
	if !strings.Contains(check.Message, "this store") || strings.Contains(strings.ToLower(check.Message), "0.5 gib") {
		t.Fatalf("message = %q", check.Message)
	}
	if !strings.Contains(check.Hint, "this store") {
		t.Fatalf("hint = %q", check.Hint)
	}
}

func TestResidentOnlyOperatorCostDoesNotOpenStore(t *testing.T) {
	t.Setenv("TRACEARY_LANG", "en")
	report := residentOnlyOperatorCost(3 << 30)
	check := buildOperatorCostCheck(report)
	if check.Status != doctorStatusSkip {
		t.Fatalf("check = %#v", check)
	}
	if report.EventCount != 0 || report.Evidence.Method != "filesystem" {
		t.Fatalf("report = %+v", report)
	}
}
