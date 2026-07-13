package cli

import (
	"strings"
	"testing"
)

// TestBuildAntigravityCaptureLevelsCheck is the regression test for the
// current Antigravity hook surface. Configured capture levels remain a PASS;
// runtime transcript persistence is judged by antigravity-event-coverage.
func TestBuildAntigravityCaptureLevelsCheck(t *testing.T) {
	check := buildAntigravityCaptureLevelsCheck()

	if check.Name != antigravityCaptureLevelsCheck {
		t.Fatalf("Name = %q, want %q", check.Name, antigravityCaptureLevelsCheck)
	}
	if check.Status != doctorStatusPass {
		t.Fatalf("Status = %q, want %q", check.Status, doctorStatusPass)
	}

	t.Run("english surfaces every capture-level token and the print-mode caveat", func(t *testing.T) {
		t.Setenv("TRACEARY_LANG", "en")
		msg := buildAntigravityCaptureLevelsCheck().Message
		wantContains := []string{
			antigravityCaptureStartSupported,
			antigravityCaptureToolAuditSupported,
			antigravityCaptureFinalTurnSupported,
			"agy --print",
			"antigravity-event-coverage",
			// route installation health is owned by antigravity-hooks, not this check.
			antigravityRouteSummaryCheck,
		}
		for _, want := range wantContains {
			if !strings.Contains(msg, want) {
				t.Fatalf("message missing %q: %q", want, msg)
			}
		}
	})

	t.Run("japanese surfaces every capture-level token and the print-mode caveat", func(t *testing.T) {
		t.Setenv("TRACEARY_LANG", "ja")
		msg := buildAntigravityCaptureLevelsCheck().Message
		wantContains := []string{
			antigravityCaptureStartSupported,
			antigravityCaptureToolAuditSupported,
			antigravityCaptureFinalTurnSupported,
			"agy --print",
			"antigravity-event-coverage",
			antigravityRouteSummaryCheck,
		}
		for _, want := range wantContains {
			if !strings.Contains(msg, want) {
				t.Fatalf("message missing %q: %q", want, msg)
			}
		}
	})
}
