package cli

import (
	"strings"
	"testing"
)

// doctorCheckSurfaceMapping pins every remaining doctor check to a kept
// pillar or operational capability (acceptance 2). Host-specific names use
// suffix rules below rather than enumerating every client prefix.
var doctorCheckSurfaceMapping = map[string]string{
	"audit-reliability":             "pillar:command-audit",
	"retry-loops":                   "pillar:command-audit",
	"sensitive-access-audit":        "pillar:command-audit",
	"content-event-reliability":     "pillar:session-events",
	"stale-active-sessions":         "pillar:session-events",
	"workspace-aliases":             "pillar:session-events",
	"workspace-observations":        "pillar:session-events",
	"codex-capture":                 "pillar:session-events",
	"memory-inbox-saturation":       "pillar:durable-memory",
	"consolidation-conversion":      "pillar:durable-memory",
	"hook-spool":                    "capability:hook-delivery-and-spool",
	"hook-state-residue":            "capability:hook-delivery-and-spool",
	"hook-memory-extract":           "capability:hook-delivery-and-spool",
	"hook-grok-transcript":          "capability:hook-delivery-and-spool",
	"claude-hook-cancellations":     "capability:hook-delivery-and-spool",
	"db":                            "capability:store-integrity-and-migration",
	"db-path":                       "capability:store-integrity-and-migration",
	"db-write":                      "capability:store-integrity-and-migration",
	"store-size":                    "capability:store-integrity-and-migration",
	"store-capacity":                "capability:store-integrity-and-migration",
	"legacy-search-index":           "capability:store-integrity-and-migration",
	"offline-migrations":            "capability:store-integrity-and-migration",
	"one-off-repairs":               "capability:store-integrity-and-migration",
	"unavailable-retention":         "capability:store-integrity-and-migration",
	"store-operator-cost":           "capability:store-integrity-and-migration",
	"large-store-diagnostics":       "capability:store-integrity-and-migration",
	"compact-in-flight":             "capability:compaction-and-backup",
	"compact-rollback-copy":         "capability:compaction-and-backup",
	"attestation-anchor":            "capability:bundle-export-import",
	"path":                          "capability:hook-delivery-and-spool",
	"config":                        "capability:hook-delivery-and-spool",
	"version":                       "capability:hook-delivery-and-spool",
	"project-dir":                   "capability:hook-delivery-and-spool",
	"stale-processes":               "capability:hook-delivery-and-spool",
	"claude-plugin-cache":           "capability:hook-delivery-and-spool",
	"claude-plugin-local-leftovers": "capability:hook-delivery-and-spool",
	"gemini-compact-coverage":       "capability:session-refinement",
	"gemini-host-eligibility":       "capability:hook-delivery-and-spool",
	"antigravity-capability":        "capability:hook-delivery-and-spool",
	"antigravity-capture-levels":    "capability:hook-delivery-and-spool",
	"antigravity-headless-hooks":    "capability:hook-delivery-and-spool",
	"antigravity-cli-plugin":        "capability:hook-delivery-and-spool",
	"antigravity-hooks":             "capability:hook-delivery-and-spool",
	"antigravity-hooks-workspace":   "capability:hook-delivery-and-spool",
	"antigravity-hooks-user":        "capability:hook-delivery-and-spool",
	"grok-cli":                      "capability:hook-delivery-and-spool",
	"grok-plugin":                   "capability:hook-delivery-and-spool",
	"grok-plugin-resolution":        "capability:hook-delivery-and-spool",
	"grok-hook-trust":               "capability:hook-delivery-and-spool",
	"grok-hooks":                    "capability:hook-delivery-and-spool",
	"grok-hooks-user":               "capability:hook-delivery-and-spool",
	"grok-hooks-routes":             "capability:hook-delivery-and-spool",
	"grok-skills":                   "capability:hook-delivery-and-spool",
	"kimi-cli":                      "capability:hook-delivery-and-spool",
	"kimi-plugin":                   "capability:hook-delivery-and-spool",
	"kimi-hooks":                    "capability:hook-delivery-and-spool",
	"kimi-skills":                   "capability:hook-delivery-and-spool",
	"codex-config":                  "capability:hook-delivery-and-spool",
	"codex-plugin-hooks":            "capability:hook-delivery-and-spool",
	"claude-config":                 "capability:hook-delivery-and-spool",
}

func TestDoctorCheckSurfaceMappingCoversRemainingChecks(t *testing.T) {
	t.Parallel()
	for name, mapping := range doctorCheckSurfaceMapping {
		if mapping == "" {
			t.Fatalf("%s has empty mapping", name)
		}
		if !strings.HasPrefix(mapping, "pillar:") && !strings.HasPrefix(mapping, "capability:") {
			t.Fatalf("%s maps to %q; want pillar:* or capability:*", name, mapping)
		}
	}
	for _, names := range doctorInspectCallCheckNames {
		for _, name := range names {
			if mapped := doctorCheckSurfaceCategory(name); mapped == "" {
				t.Fatalf("bounded-deferred check %q has no acceptance-2 mapping", name)
			}
		}
	}
}

func TestDoctorCheckSurfaceCategorySuffixes(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		want string
	}{
		{"claude-event-coverage", "pillar:session-events"},
		{"codex-event-coverage", "pillar:session-events"},
		{"gemini-event-coverage", "pillar:session-events"},
		{"antigravity-event-coverage", "pillar:session-events"},
		{"kimi-event-coverage", "pillar:session-events"},
		{"claude-memory-activation", "pillar:durable-memory"},
		{"codex-memory-activation", "pillar:durable-memory"},
		{"gemini-memory-activation", "pillar:durable-memory"},
		{"claude-plugin-version", "capability:hook-delivery-and-spool"},
		{"grok-inspect", "capability:hook-delivery-and-spool"},
		{"kimi-inspect", "capability:hook-delivery-and-spool"},
		{"gemini-config", "capability:hook-delivery-and-spool"},
		{"gemini-host-capabilities", "capability:hook-delivery-and-spool"},
		{"claude-host-capabilities", "capability:hook-delivery-and-spool"},
		{"claude-hooks", "capability:hook-delivery-and-spool"},
		{"codex-plugin", "capability:hook-delivery-and-spool"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := doctorCheckSurfaceCategory(tc.name)
			if got != tc.want {
				t.Fatalf("category(%s) = %q, want %q", tc.name, got, tc.want)
			}
		})
	}
}

func doctorCheckSurfaceCategory(name string) string {
	if mapped, ok := doctorCheckSurfaceMapping[name]; ok {
		return mapped
	}
	switch {
	case strings.HasSuffix(name, "-event-coverage"):
		return "pillar:session-events"
	case strings.HasSuffix(name, "-memory-activation"):
		return "pillar:durable-memory"
	case strings.HasSuffix(name, "-plugin-version"),
		strings.HasSuffix(name, "-inspect"),
		strings.HasSuffix(name, "-host-capabilities"),
		strings.HasSuffix(name, "-config"),
		strings.HasSuffix(name, "-hooks"),
		strings.HasSuffix(name, "-plugin"):
		return "capability:hook-delivery-and-spool"
	default:
		return ""
	}
}
