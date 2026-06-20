package cli

import (
	"errors"
	"strings"
	"testing"
)

func TestAntigravityProbeToState(t *testing.T) {
	tests := []struct {
		name  string
		probe antigravityCapabilityProbe
		want  antigravityCapabilityState
	}{
		{
			name:  "not installed when no CLI and no bundle",
			probe: antigravityCapabilityProbe{},
			want:  antigravityStateNotInstalled,
		},
		{
			name:  "tool_unavailable when CLI found but no supported surface",
			probe: antigravityCapabilityProbe{CLIFound: true},
			want:  antigravityStateToolUnavailable,
		},
		{
			name:  "tool_unavailable when bundle found but no supported surface",
			probe: antigravityCapabilityProbe{BundleFound: true},
			want:  antigravityStateToolUnavailable,
		},
		{
			name:  "tool_unavailable when both CLI and bundle found but no supported surface",
			probe: antigravityCapabilityProbe{CLIFound: true, BundleFound: true},
			want:  antigravityStateToolUnavailable,
		},
		{
			name:  "not_authenticated when supported surface confirmed but not configured",
			probe: antigravityCapabilityProbe{CLIFound: true, SupportedSurfaceConfirmed: true, AuthenticatedOrConfigured: false},
			want:  antigravityStateNotAuthenticated,
		},
		{
			name:  "available when supported surface confirmed and authenticated",
			probe: antigravityCapabilityProbe{CLIFound: true, SupportedSurfaceConfirmed: true, AuthenticatedOrConfigured: true},
			want:  antigravityStateAvailable,
		},
		{
			name:  "available via bundle when supported surface confirmed and authenticated",
			probe: antigravityCapabilityProbe{BundleFound: true, SupportedSurfaceConfirmed: true, AuthenticatedOrConfigured: true},
			want:  antigravityStateAvailable,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := antigravityProbeToState(tt.probe)
			if got != tt.want {
				t.Fatalf("antigravityProbeToState(%+v) = %v, want %v", tt.probe, got, tt.want)
			}
		})
	}
}

func TestDetectAntigravityCapability(t *testing.T) {
	noPath := func(string) (string, error) { return "", errors.New("not found") }
	hasPath := func(cmd string) (string, error) {
		if cmd == "antigravity" {
			return "/usr/local/bin/antigravity", nil
		}
		return "", errors.New("not found")
	}
	noBundle := func(string) bool { return false }
	hasBundle := func(string) bool { return true }

	t.Run("not installed when no app bundle and no CLI", func(t *testing.T) {
		state := detectAntigravityCapability(noPath, noBundle)
		if state != antigravityStateNotInstalled {
			t.Fatalf("state = %v, want not_installed", state)
		}
	})

	t.Run("tool_unavailable when app bundle exists but no CLI", func(t *testing.T) {
		// Use detectAntigravityCapabilityWithBundlePaths with an explicit fake path so
		// bundle detection is exercised on all platforms (antigravityBundlePaths returns
		// nil on non-macOS, which would skip the hasBundle predicate).
		state := detectAntigravityCapabilityWithBundlePaths(noPath, hasBundle, []string{"/fake/Antigravity.app"})
		if state != antigravityStateToolUnavailable {
			t.Fatalf("state = %v, want tool_unavailable", state)
		}
	})

	t.Run("tool_unavailable when CLI on PATH (no confirmed hook contract)", func(t *testing.T) {
		// Even with a CLI present, no supported public hook/automation
		// contract is confirmed for Antigravity yet.
		state := detectAntigravityCapability(hasPath, noBundle)
		if state != antigravityStateToolUnavailable {
			t.Fatalf("state = %v, want tool_unavailable", state)
		}
	})

	t.Run("tool_unavailable when both CLI and bundle exist", func(t *testing.T) {
		state := detectAntigravityCapabilityWithBundlePaths(hasPath, hasBundle, []string{"/fake/Antigravity.app"})
		if state != antigravityStateToolUnavailable {
			t.Fatalf("state = %v, want tool_unavailable", state)
		}
	})
}

func TestBuildAntigravityCapabilityCheck(t *testing.T) {
	t.Setenv("TRACEARY_LANG", "en")

	t.Run("not_installed yields warn without tool_unavailable in message", func(t *testing.T) {
		check := buildAntigravityCapabilityCheck(antigravityStateNotInstalled)
		if check.Name != "antigravity-capability" {
			t.Fatalf("Name = %q, want antigravity-capability", check.Name)
		}
		if check.Status != doctorStatusWarn {
			t.Fatalf("Status = %q, want warn", check.Status)
		}
		if strings.Contains(check.Message, "tool_unavailable") {
			t.Fatalf("not_installed message must not contain 'tool_unavailable', got: %q", check.Message)
		}
		if !strings.Contains(check.Message, "not installed") && !strings.Contains(check.Message, "not_installed") {
			t.Fatalf("not_installed message should indicate not installed, got: %q", check.Message)
		}
	})

	t.Run("tool_unavailable yields warn with explicit no-package decision", func(t *testing.T) {
		check := buildAntigravityCapabilityCheck(antigravityStateToolUnavailable)
		if check.Name != "antigravity-capability" {
			t.Fatalf("Name = %q, want antigravity-capability", check.Name)
		}
		if check.Status != doctorStatusWarn {
			t.Fatalf("Status = %q, want warn", check.Status)
		}
		if !strings.Contains(check.Message, "tool_unavailable") {
			t.Fatalf("tool_unavailable message must contain 'tool_unavailable', got: %q", check.Message)
		}
		// v0.21.0 intentionally ships no Antigravity package; the message must state the
		// decision rather than tracking it as outstanding implementation work.
		if !strings.Contains(check.Message, "intentionally ships no Antigravity") {
			t.Fatalf("tool_unavailable message must state the intentional no-package decision, got: %q", check.Message)
		}
		if !strings.Contains(check.Message, "supported public CLI/hook contract") {
			t.Fatalf("tool_unavailable message must state future support requires a supported public CLI/hook contract, got: %q", check.Message)
		}
		if strings.Contains(check.Message, "#1196") {
			t.Fatalf("tool_unavailable message must not track package work in #1196, got: %q", check.Message)
		}
	})

	t.Run("not_authenticated yields warn with not_authenticated in message", func(t *testing.T) {
		check := buildAntigravityCapabilityCheck(antigravityStateNotAuthenticated)
		if check.Name != "antigravity-capability" {
			t.Fatalf("Name = %q, want antigravity-capability", check.Name)
		}
		if check.Status != doctorStatusWarn {
			t.Fatalf("Status = %q, want warn", check.Status)
		}
		if !strings.Contains(check.Message, "not_authenticated") {
			t.Fatalf("not_authenticated message must contain 'not_authenticated', got: %q", check.Message)
		}
		if strings.Contains(check.Message, "reads credentials") {
			t.Fatalf("not_authenticated message must not claim Traceary reads credentials, got: %q", check.Message)
		}
	})

	t.Run("available yields pass", func(t *testing.T) {
		check := buildAntigravityCapabilityCheck(antigravityStateAvailable)
		if check.Name != "antigravity-capability" {
			t.Fatalf("Name = %q, want antigravity-capability", check.Name)
		}
		if check.Status != doctorStatusPass {
			t.Fatalf("Status = %q, want pass", check.Status)
		}
	})
}
