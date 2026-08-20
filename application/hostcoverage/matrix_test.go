package hostcoverage_test

import (
	"strings"
	"testing"

	"github.com/duck8823/traceary/application/hostcoverage"
)

func TestLoad_ParsesEmbeddedMatrix(t *testing.T) {
	t.Parallel()

	m, err := hostcoverage.Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if m.LastVerified == "" {
		t.Fatal("LastVerified is empty")
	}
	if len(m.Hosts) < 5 {
		t.Fatalf("Hosts = %d, want at least claude/codex/gemini/antigravity/grok", len(m.Hosts))
	}
	wired := m.WiredLifecycleEvents("claude")
	if len(wired) == 0 {
		t.Fatal("claude wired lifecycle events is empty")
	}
	if !m.ExpectsSessionEnrichment("antigravity") {
		t.Fatal("antigravity should expect session enrichment")
	}
	table := m.RenderMatrixTable("en")
	if !strings.Contains(table, "session_started") || !strings.Contains(table, "Claude Code") {
		t.Fatalf("EN table missing expected content:\n%s", table)
	}
	ja := m.RenderMatrixTable("ja")
	if !strings.Contains(ja, "確認方法") {
		t.Fatalf("JA table missing 確認方法 column:\n%s", ja)
	}
}

func TestLoad_GrokSessionEndedIsAvailableNotWired(t *testing.T) {
	t.Parallel()

	m, err := hostcoverage.Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	host, ok := m.HostByDoctorClient("grok")
	if !ok {
		t.Fatal("missing grok host")
	}
	cell := host.Events["session_ended"]
	if cell.Status != hostcoverage.StatusAvailable {
		t.Fatalf("grok session_ended status = %q, want available", cell.Status)
	}
}

func TestLoad_GeminiPromptStaysWiredWithIneligibleTierCaveat(t *testing.T) {
	t.Parallel()

	m, err := hostcoverage.Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	host, ok := m.HostByDoctorClient("gemini")
	if !ok {
		t.Fatal("missing gemini host")
	}
	// The 2026-08-21 dogfood probe on an individual (free-tier) account failed
	// with IneligibleTierError and recorded only session_started/session_ended.
	// The wiring itself is intact (eligible accounts still capture prompt), so
	// the cell stays wired but the summary must carry the ineligible-tier
	// caveat — silently staying "wired" was the defect behind #2237.
	cell := host.Events["prompt"]
	if cell.Status != hostcoverage.StatusWired {
		t.Fatalf("gemini prompt status = %q, want wired (wiring is intact; eligibility is per-account)", cell.Status)
	}
	if !strings.Contains(cell.Summary.EN, "IneligibleTierError") || !strings.Contains(cell.Summary.JA, "IneligibleTierError") {
		t.Fatalf("gemini prompt summary lost the ineligible-tier caveat: en=%q ja=%q", cell.Summary.EN, cell.Summary.JA)
	}
}

func TestLoad_KimiCoreEventsAreWired(t *testing.T) {
	t.Parallel()

	m, err := hostcoverage.Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	host, ok := m.HostByDoctorClient("kimi")
	if !ok {
		t.Fatal("missing kimi host")
	}
	for _, id := range []string{"session_started", "prompt", "command_executed", "transcript", "compact_summary"} {
		if cell := host.Events[id]; cell.Status != hostcoverage.StatusWired {
			t.Fatalf("kimi %s status = %q, want wired", id, cell.Status)
		}
	}
	if !m.ExpectsSessionEnrichment("kimi") {
		t.Fatal("kimi should expect session enrichment once core capture is wired")
	}
}

func TestLoad_KimiSessionEndedIsNotWired(t *testing.T) {
	t.Parallel()

	m, err := hostcoverage.Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	host, ok := m.HostByDoctorClient("kimi")
	if !ok {
		t.Fatal("missing kimi host")
	}
	// Kimi Code 0.38.0 `-p` probes (2026-08-21, isolated store) recorded
	// session_started/prompt/transcript but the host never dispatched
	// SessionEnd, so the cell must not claim capture. It stays `available`
	// while the packaged plugin still declares SessionEnd.
	cell := host.Events["session_ended"]
	if cell.Status != hostcoverage.StatusAvailable {
		t.Fatalf("kimi session_ended status = %q, want available (never wired without a live 0.38.0 capture)", cell.Status)
	}
}
