package cli

import (
	"strings"
	"testing"
)

func TestCompareTracearyVersion_Equal(t *testing.T) {
	t.Parallel()

	got := compareTracearyVersion("0.7.2", "0.7.2")

	if got.Status != doctorStatusPass {
		t.Errorf("Status = %q, want %q", got.Status, doctorStatusPass)
	}
	if !strings.Contains(got.Message, "0.7.2") {
		t.Errorf("Message = %q, want it to include 0.7.2", got.Message)
	}
}

func TestCompareTracearyVersion_OlderWarns(t *testing.T) {
	t.Parallel()

	got := compareTracearyVersion("0.7.1", "0.7.2")

	if got.Status != doctorStatusWarn {
		t.Errorf("Status = %q, want %q", got.Status, doctorStatusWarn)
	}
	if !strings.Contains(got.Message, "→ 0.7.2") {
		t.Errorf("Message = %q, want upgrade arrow to 0.7.2", got.Message)
	}
}

func TestCompareTracearyVersion_PseudoVersionNewerPasses(t *testing.T) {
	t.Parallel()

	current := "0.7.3-0.20260420223154-9a43e0847edd"
	got := compareTracearyVersion(current, "0.7.2")

	if got.Status != doctorStatusPass {
		t.Errorf("Status = %q, want %q (pseudo-version newer than latest release)", got.Status, doctorStatusPass)
	}
	if strings.Contains(got.Message, "brew upgrade") {
		t.Errorf("Message = %q, must not tell the user to downgrade their dev build", got.Message)
	}
	if !strings.Contains(got.Message, "development build") && !strings.Contains(got.Message, "development") {
		t.Errorf("Message = %q, expected a 'development build' hint", got.Message)
	}
}

func TestCompareTracearyVersion_PseudoVersionOlderStillWarns(t *testing.T) {
	t.Parallel()

	// A pseudo-version whose base is older than latest — e.g. a stale
	// branch built off 0.6.x while latest is 0.7.2 — should still warn.
	current := "0.6.1-0.20260301000000-deadbeefcafe"
	got := compareTracearyVersion(current, "0.7.2")

	if got.Status != doctorStatusWarn {
		t.Errorf("Status = %q, want %q", got.Status, doctorStatusWarn)
	}
}

func TestCompareTracearyVersion_InvalidFallsBackToStringCompare(t *testing.T) {
	t.Parallel()

	// "nightly" is not valid semver on either side — keep legacy string
	// behavior so we do not silently claim "up to date".
	got := compareTracearyVersion("nightly", "0.7.2")

	if got.Status != doctorStatusWarn {
		t.Errorf("Status = %q, want %q (string mismatch must warn)", got.Status, doctorStatusWarn)
	}
}
