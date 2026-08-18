package cli

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/duck8823/traceary/domain"
)

func TestCLICompactionProgress_OnPhaseWritesStderrOnlyShape(t *testing.T) {
	t.Parallel()
	var buf bytes.Buffer
	p := newCLICompactionProgress(&buf)
	p.OnPhase(domain.CompactionCandidatePrepared, 35*1024*1024*1024)
	got := buf.String()
	if !strings.Contains(got, "compact: candidate_prepared") || !strings.Contains(got, "35.0 GiB") {
		t.Fatalf("phase line = %q", got)
	}
}

func TestCLICompactionProgress_WatchBuildReportsPercentAndCapsOverEstimate(t *testing.T) {
	dir := t.TempDir()
	candidate := filepath.Join(dir, "replica")
	if err := os.WriteFile(candidate, bytes.Repeat([]byte("x"), 200), 0o600); err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	p := newCLICompactionProgress(&buf)
	orig := compactBuildProgressInterval
	compactBuildProgressInterval = 20 * time.Millisecond
	t.Cleanup(func() { compactBuildProgressInterval = orig })
	stop := p.WatchBuild(context.Background(), candidate, 100)
	time.Sleep(50 * time.Millisecond)
	stop()
	got := buf.String()
	if !strings.Contains(got, ">100%") {
		t.Fatalf("progress = %q, want >100%% cap", got)
	}
}

func TestFormatCompactPercent(t *testing.T) {
	t.Parallel()
	if got := formatCompactPercent(50, 100); got != "50%" {
		t.Fatalf("got %q", got)
	}
	if got := formatCompactPercent(100, 100); got != ">100%" {
		t.Fatalf("got %q", got)
	}
}
