package cli

import (
	"strings"
	"testing"
)

// TestBuildAntigravityCaptureLevelsCheck is the regression test for the
// documented Antigravity print-mode capture level: PreInvocation (start) and
// run_command (tool audit) are supported in every mode, but headless
// `agy --print` has no Stop/finalization hook, so its final turn is
// `final_turn_unavailable`. Because the host has no print-mode finalization
// hook, this is the smoke/regression coverage the issue asks for — it proves
// doctor surfaces the start/tool-audit-only print-mode behavior without warning
// on a healthy install.
func TestBuildAntigravityCaptureLevelsCheck(t *testing.T) {
	check := buildAntigravityCaptureLevelsCheck()

	if check.Name != antigravityCaptureLevelsCheck {
		t.Fatalf("Name = %q, want %q", check.Name, antigravityCaptureLevelsCheck)
	}
	// A working install must not warn merely because print mode cannot emit a
	// final turn: the start/tool-audit levels are genuinely captured, so PASS.
	if check.Status != doctorStatusPass {
		t.Fatalf("Status = %q, want %q (a working install must not warn for the print-mode final-turn gap)", check.Status, doctorStatusPass)
	}

	t.Run("english surfaces every capture-level token and the print-mode caveat", func(t *testing.T) {
		t.Setenv("TRACEARY_LANG", "en")
		msg := buildAntigravityCaptureLevelsCheck().Message
		wantContains := []string{
			antigravityCaptureStartSupported,
			antigravityCaptureToolAuditSupported,
			antigravityCaptureFinalTurnSupported,
			antigravityCaptureFinalTurnUnavailable,
			"agy --print",
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
			antigravityCaptureFinalTurnUnavailable,
			"agy --print",
			antigravityRouteSummaryCheck,
		}
		for _, want := range wantContains {
			if !strings.Contains(msg, want) {
				t.Fatalf("message missing %q: %q", want, msg)
			}
		}
	})
}
