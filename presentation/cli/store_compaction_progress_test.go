package cli

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/duck8823/traceary/domain"
)

func TestCLICompactionProgress_OnWindowWritesPreCopyToken(t *testing.T) {
	t.Parallel()
	var buf bytes.Buffer
	p := newCLICompactionProgress(&buf)
	p.OnWindow("inspect_reclaim")
	if buf.String() != "compact: inspect_reclaim\n" {
		t.Fatalf("window line = %q", buf.String())
	}
}

// This writer exposes an actual in-flight callback, not a timing assumption
// about the worker having observed cancellation. It owns no global test state.
type blockedCompactionProgressWriter struct {
	entered chan struct{}
	release chan struct{}
	once    sync.Once
}

func (w *blockedCompactionProgressWriter) Write(data []byte) (int, error) {
	w.once.Do(func() { close(w.entered) })
	<-w.release
	return len(data), nil
}

func TestCLICompactionProgress_StopJoinsActualEmissionAndIsIdempotent(t *testing.T) {
	file := filepath.Join(t.TempDir(), "candidate")
	if err := os.WriteFile(file, []byte("fixture"), 0600); err != nil {
		t.Fatal(err)
	}
	w := &blockedCompactionProgressWriter{entered: make(chan struct{}), release: make(chan struct{})}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	stop := newCLICompactionProgress(w).WatchBuild(ctx, file, 100)
	var releaseOnce sync.Once
	defer func() { releaseOnce.Do(func() { close(w.release) }); stop() }()
	select {
	case <-w.entered:
	case <-time.After(time.Second):
		t.Fatal("progress emission never started")
	}
	stopped := make(chan struct{})
	go func() { stop(); close(stopped) }()
	select {
	case <-stopped:
		t.Fatal("stop returned before its owned emission actually completed")
	case <-time.After(20 * time.Millisecond):
	}
	releaseOnce.Do(func() { close(w.release) })
	select {
	case <-stopped:
	case <-time.After(time.Second):
		t.Fatal("stop did not join completed emission")
	}
	// These calls share cancellation but must all observe the same completion.
	var wait sync.WaitGroup
	for i := 0; i < 8; i++ {
		wait.Add(1)
		go func() { defer wait.Done(); stop() }()
	}
	joined := make(chan struct{})
	go func() { wait.Wait(); close(joined) }()
	select {
	case <-joined:
	case <-time.After(time.Second):
		t.Fatal("idempotent stop did not complete")
	}
	cancel()
	stop()
}

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
