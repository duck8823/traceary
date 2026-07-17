package cli

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gofrs/flock"

	"github.com/duck8823/traceary/domain/types"
)

func TestInspectHookMemoryExtractDiagnosticsReportsPendingFailureMetadata(t *testing.T) {
	stateDir := t.TempDir()
	t.Setenv(hookStateDirEnvKey, stateDir)
	now := time.Date(2026, 7, 13, 12, 0, 0, 0, time.UTC)
	request := hookMemoryExtractRequest{
		SessionID:      types.SessionID("session-1"),
		Workspace:      types.Workspace("traceary"),
		DBPath:         filepath.Join(t.TempDir(), "traceary.db"),
		SourceBoundary: "turn_boundary",
	}
	path, err := enqueueHookMemoryExtract(request, now.Add(-3*time.Minute))
	if err != nil {
		t.Fatalf("enqueueHookMemoryExtract() error = %v", err)
	}
	job, err := readHookMemoryExtractJob(path)
	if err != nil {
		t.Fatalf("readHookMemoryExtractJob() error = %v", err)
	}
	job.Attempts = 2
	job.LastError = "simulated failure"
	if err := writeHookMemoryExtractJob(path, job); err != nil {
		t.Fatalf("writeHookMemoryExtractJob() error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(stateDir, "memory-extract", "broken.json"), []byte("{"), 0o600); err != nil {
		t.Fatalf("WriteFile(broken job) error = %v", err)
	}

	check := (&RootCLI{}).inspectHookMemoryExtractDiagnostics(now)
	if check.Name != "hook-memory-extract" || check.Status != doctorStatusWarn {
		t.Fatalf("check = %+v, want warning", check)
	}
	for _, want := range []string{"1 pending", "1 previously failed", "0 terminal", "1 unreadable", "3m0s"} {
		if !strings.Contains(check.Message, want) {
			t.Fatalf("check message = %q, want %q", check.Message, want)
		}
	}
	if !check.AutoFixAvailable || check.FixFunc == nil {
		t.Fatalf("doctor check must expose auto-fix drain, got %#v", check)
	}
	if !strings.Contains(check.Hint, "doctor --fix") {
		t.Fatalf("hint = %q", check.Hint)
	}
}

func TestInspectHookMemoryExtractDiagnosticsPassesWithoutJobs(t *testing.T) {
	t.Setenv(hookStateDirEnvKey, t.TempDir())
	check := (&RootCLI{}).inspectHookMemoryExtractDiagnostics(time.Now().UTC())
	if check.Status != doctorStatusPass {
		t.Fatalf("check = %+v, want pass", check)
	}
}

func TestDrainHookMemoryExtractQueue_LaunchesOtherSessionJobs(t *testing.T) {
	t.Setenv(hookStateDirEnvKey, t.TempDir())
	now := time.Date(2026, 7, 13, 12, 0, 0, 0, time.UTC)

	// Ended session that will never re-hit scheduleHookMemoryExtract.
	ended := hookMemoryExtractRequest{
		SessionID:      types.SessionID("ended-session"),
		Workspace:      types.Workspace("traceary"),
		DBPath:         filepath.Join(t.TempDir(), "traceary.db"),
		SourceBoundary: "session_end",
	}
	endedPath, err := enqueueHookMemoryExtract(ended, now.Add(-time.Hour))
	if err != nil {
		t.Fatalf("enqueue ended: %v", err)
	}

	var mu sync.Mutex
	launched := []string{}
	root := NewRootCLI(WithHookMemoryExtractLauncher(func(jobPath string) error {
		mu.Lock()
		defer mu.Unlock()
		launched = append(launched, jobPath)
		return nil
	}))

	gotLaunched, removed := root.drainHookMemoryExtractQueue(now, 5)
	if gotLaunched != 1 || removed != 0 {
		t.Fatalf("launched=%d removed=%d want 1/0", gotLaunched, removed)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(launched) != 1 || launched[0] != endedPath {
		t.Fatalf("launcher paths = %#v, want %q", launched, endedPath)
	}
}

func TestDrainHookMemoryExtractQueue_GCsTerminalJobsPastRetention(t *testing.T) {
	t.Setenv(hookStateDirEnvKey, t.TempDir())
	now := time.Date(2026, 7, 13, 12, 0, 0, 0, time.UTC)
	request := hookMemoryExtractRequest{
		SessionID:      types.SessionID("terminal-session"),
		Workspace:      types.Workspace("traceary"),
		DBPath:         filepath.Join(t.TempDir(), "traceary.db"),
		SourceBoundary: "session_end",
	}
	path, err := enqueueHookMemoryExtract(request, now.Add(-48*time.Hour))
	if err != nil {
		t.Fatalf("enqueue: %v", err)
	}
	job, err := readHookMemoryExtractJob(path)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	job.Attempts = hookMemoryExtractMaxAttempts
	last := now.Add(-hookMemoryExtractTerminalRetention - time.Hour)
	job.LastAttemptAt = &last
	job.LastError = "context canceled"
	if err := writeHookMemoryExtractJob(path, job); err != nil {
		t.Fatalf("write: %v", err)
	}

	root := NewRootCLI(WithHookMemoryExtractLauncher(func(string) error {
		t.Fatal("terminal jobs must not be relaunched")
		return nil
	}))
	launched, removed := root.drainHookMemoryExtractQueue(now, 5)
	if launched != 0 || removed != 1 {
		t.Fatalf("launched=%d removed=%d want 0/1", launched, removed)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("terminal job must be GC'd, stat err=%v", err)
	}
}

func TestDrainHookMemoryExtractQueue_RetainsTerminalWithinRetention(t *testing.T) {
	t.Setenv(hookStateDirEnvKey, t.TempDir())
	now := time.Date(2026, 7, 13, 12, 0, 0, 0, time.UTC)
	request := hookMemoryExtractRequest{
		SessionID:      types.SessionID("recent-terminal"),
		Workspace:      types.Workspace("traceary"),
		DBPath:         filepath.Join(t.TempDir(), "traceary.db"),
		SourceBoundary: "session_end",
	}
	path, err := enqueueHookMemoryExtract(request, now.Add(-time.Hour))
	if err != nil {
		t.Fatalf("enqueue: %v", err)
	}
	job, err := readHookMemoryExtractJob(path)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	job.Attempts = hookMemoryExtractMaxAttempts
	last := now.Add(-time.Minute)
	job.LastAttemptAt = &last
	if err := writeHookMemoryExtractJob(path, job); err != nil {
		t.Fatalf("write: %v", err)
	}

	root := NewRootCLI(WithHookMemoryExtractLauncher(func(string) error {
		t.Fatal("terminal jobs within retention must not be relaunched")
		return nil
	}))
	launched, removed := root.drainHookMemoryExtractQueue(now, 5)
	if launched != 0 || removed != 0 {
		t.Fatalf("launched=%d removed=%d want 0/0", launched, removed)
	}
	check := root.inspectHookMemoryExtractDiagnostics(now)
	if !strings.Contains(check.Message, "1 terminal") {
		t.Fatalf("message = %q", check.Message)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("must retain for doctor visibility: %v", err)
	}
}

func TestInspectHookMemoryExtractDiagnostics_FixFuncDrains(t *testing.T) {
	t.Setenv(hookStateDirEnvKey, t.TempDir())
	now := time.Now().UTC()
	request := hookMemoryExtractRequest{
		SessionID:      types.SessionID("fix-session"),
		Workspace:      types.Workspace("traceary"),
		DBPath:         filepath.Join(t.TempDir(), "traceary.db"),
		SourceBoundary: "session_end",
	}
	path, err := enqueueHookMemoryExtract(request, now.Add(-time.Hour))
	if err != nil {
		t.Fatalf("enqueue: %v", err)
	}

	var launched int
	root := NewRootCLI(WithHookMemoryExtractLauncher(func(jobPath string) error {
		if jobPath == path {
			launched++
		}
		return nil
	}))
	check := root.inspectHookMemoryExtractDiagnostics(now)
	if check.FixFunc == nil {
		t.Fatal("FixFunc is nil")
	}
	if _, err := check.FixFunc(context.Background(), true); err != nil {
		t.Fatalf("dry-run: %v", err)
	}
	msg, err := check.FixFunc(context.Background(), false)
	if err != nil {
		t.Fatalf("apply: %v", err)
	}
	if !strings.Contains(msg, "launched=1") {
		t.Fatalf("apply msg = %q", msg)
	}
	if launched != 1 {
		t.Fatalf("launched = %d", launched)
	}
}

func TestRunHookMemoryExtractWorker_SkipsTerminalJobs(t *testing.T) {
	t.Setenv(hookStateDirEnvKey, t.TempDir())
	request := hookMemoryExtractRequest{
		SessionID:      types.SessionID("skip-terminal"),
		Workspace:      types.Workspace("traceary"),
		DBPath:         filepath.Join(t.TempDir(), "traceary.db"),
		SourceBoundary: "session_end",
	}
	path, err := enqueueHookMemoryExtract(request, time.Now().UTC())
	if err != nil {
		t.Fatalf("enqueue: %v", err)
	}
	job, err := readHookMemoryExtractJob(path)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	job.Attempts = hookMemoryExtractMaxAttempts
	job.LastError = "already terminal"
	if err := writeHookMemoryExtractJob(path, job); err != nil {
		t.Fatalf("write: %v", err)
	}

	// No store/memory wired: would fail if extract ran.
	root := &RootCLI{}
	if err := root.runHookMemoryExtractWorker(context.Background(), path); err != nil {
		t.Fatalf("terminal worker must no-op, got %v", err)
	}
	got, err := readHookMemoryExtractJob(path)
	if err != nil {
		t.Fatalf("read after: %v", err)
	}
	if got.Attempts != hookMemoryExtractMaxAttempts || got.LastError != "already terminal" {
		t.Fatalf("terminal job mutated: %#v", got)
	}
}

func TestEnqueueHookMemoryExtractQuarantinesUnreadableJob(t *testing.T) {
	t.Setenv(hookStateDirEnvKey, t.TempDir())
	request := hookMemoryExtractRequest{
		SessionID:      types.SessionID("corrupt-session"),
		Workspace:      types.Workspace("traceary"),
		DBPath:         filepath.Join(t.TempDir(), "traceary.db"),
		SourceBoundary: "session_end",
	}
	path, err := enqueueHookMemoryExtract(request, time.Now().UTC())
	if err != nil {
		t.Fatalf("first enqueueHookMemoryExtract() error = %v", err)
	}
	if err := os.WriteFile(path, []byte("{"), 0o600); err != nil {
		t.Fatalf("WriteFile(corrupt job) error = %v", err)
	}
	if _, err := enqueueHookMemoryExtract(request, time.Now().UTC().Add(time.Second)); err != nil {
		t.Fatalf("second enqueueHookMemoryExtract() error = %v", err)
	}
	if _, err := readHookMemoryExtractJob(path); err != nil {
		t.Fatalf("replacement job error = %v", err)
	}
	jobs, unreadable, err := scanHookMemoryExtractJobs()
	if err != nil {
		t.Fatalf("scanHookMemoryExtractJobs() error = %v", err)
	}
	if len(jobs) != 1 || len(unreadable) != 1 {
		t.Fatalf("jobs=%d unreadable=%d, want one replacement and one quarantined file", len(jobs), len(unreadable))
	}
}

func TestEnqueueHookMemoryExtractPreservesOldestRequestTime(t *testing.T) {
	t.Setenv(hookStateDirEnvKey, t.TempDir())
	request := hookMemoryExtractRequest{
		SessionID:      types.SessionID("oldest-session"),
		Workspace:      types.Workspace("traceary"),
		DBPath:         filepath.Join(t.TempDir(), "traceary.db"),
		SourceBoundary: "turn_boundary",
	}
	first := time.Date(2026, 7, 13, 10, 0, 0, 0, time.UTC)
	path, err := enqueueHookMemoryExtract(request, first)
	if err != nil {
		t.Fatalf("first enqueueHookMemoryExtract() error = %v", err)
	}
	if _, err := enqueueHookMemoryExtract(request, first.Add(time.Hour)); err != nil {
		t.Fatalf("second enqueueHookMemoryExtract() error = %v", err)
	}
	job, err := readHookMemoryExtractJob(path)
	if err != nil {
		t.Fatalf("readHookMemoryExtractJob() error = %v", err)
	}
	if !job.RequestedAt.Equal(first) {
		t.Fatalf("requested_at = %s, want oldest %s", job.RequestedAt, first)
	}
}

func TestEnqueueHookMemoryExtractPreservesOldestContendedRerunTime(t *testing.T) {
	t.Setenv(hookStateDirEnvKey, t.TempDir())
	request := hookMemoryExtractRequest{
		SessionID:      types.SessionID("contended-session"),
		Workspace:      types.Workspace("traceary"),
		DBPath:         filepath.Join(t.TempDir(), "traceary.db"),
		SourceBoundary: "turn_boundary",
	}
	first := time.Date(2026, 7, 13, 10, 0, 0, 0, time.UTC)
	path, err := enqueueHookMemoryExtract(request, first)
	if err != nil {
		t.Fatalf("first enqueueHookMemoryExtract() error = %v", err)
	}
	jobLock := flock.New(path + ".lock")
	if err := jobLock.Lock(); err != nil {
		t.Fatalf("Lock() error = %v", err)
	}
	t.Cleanup(func() { _ = jobLock.Unlock() })
	oldestRerun := first.Add(time.Minute)
	if _, err := enqueueHookMemoryExtract(request, oldestRerun); err != nil {
		t.Fatalf("contended enqueueHookMemoryExtract() error = %v", err)
	}
	if _, err := enqueueHookMemoryExtract(request, oldestRerun.Add(time.Minute)); err != nil {
		t.Fatalf("second contended enqueueHookMemoryExtract() error = %v", err)
	}
	got := readHookMemoryExtractRerunTime(path+".rerun", oldestRerun.Add(time.Hour))
	if !got.Equal(oldestRerun) {
		t.Fatalf("rerun requested_at = %s, want oldest %s", got, oldestRerun)
	}
}

func TestPublishHookMemoryExtractRerunIsCompleteBeforeVisibility(t *testing.T) {
	path := filepath.Join(t.TempDir(), "job.rerun")
	requestedAt := time.Date(2026, 7, 13, 11, 0, 0, 123, time.UTC)
	if err := publishHookMemoryExtractRerunWithHook(path, requestedAt, func() {
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Fatalf("marker became visible before atomic publish: %v", err)
		}
	}); err != nil {
		t.Fatalf("publishHookMemoryExtractRerunWithHook() error = %v", err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile(marker) error = %v", err)
	}
	if got := strings.TrimSpace(string(data)); got != requestedAt.Format(time.RFC3339Nano) {
		t.Fatalf("marker = %q, want %q", got, requestedAt.Format(time.RFC3339Nano))
	}
}
