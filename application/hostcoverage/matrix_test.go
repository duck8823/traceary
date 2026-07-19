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

func TestLoad_KimiIsProbedButNotWired(t *testing.T) {
	t.Parallel()

	m, err := hostcoverage.Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	host, ok := m.HostByDoctorClient("kimi")
	if !ok {
		t.Fatal("missing kimi host")
	}
	if len(m.WiredLifecycleEvents("kimi")) != 0 {
		t.Fatal("kimi must not report wired lifecycle events before capture lands")
	}
	for id, cell := range host.Events {
		if cell.Status != hostcoverage.StatusAvailable {
			t.Fatalf("kimi %s status = %q, want available (probed, not wired)", id, cell.Status)
		}
	}
	if m.ExpectsSessionEnrichment("kimi") {
		t.Fatal("kimi must not expect session enrichment before capture lands")
	}
}

