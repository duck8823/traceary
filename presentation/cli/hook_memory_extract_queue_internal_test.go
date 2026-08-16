package cli

import (
	"context"
	"fmt"
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

func TestInspectHookMemoryExtractDiagnosticsWarnsOnOrphanSidecarsOnly(t *testing.T) {
	stateDir := t.TempDir()
	t.Setenv(hookStateDirEnvKey, stateDir)
	now := time.Date(2026, 7, 13, 12, 0, 0, 0, time.UTC)
	queueDir := filepath.Join(stateDir, "memory-extract")
	if err := os.MkdirAll(queueDir, 0o700); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	orphanLock := filepath.Join(queueDir, "orphan.json.lock")
	if err := os.WriteFile(orphanLock, []byte{}, 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	aged := now.Add(-hookMemoryExtractTerminalRetention - time.Hour)
	if err := os.Chtimes(orphanLock, aged, aged); err != nil {
		t.Fatalf("Chtimes: %v", err)
	}

	root := &RootCLI{}
	check := root.inspectHookMemoryExtractDiagnostics(now)
	if check.Status != doctorStatusWarn || !check.AutoFixAvailable || check.FixFunc == nil {
		t.Fatalf("check = %+v, want warn with FixFunc", check)
	}
	if !strings.Contains(check.Message, "1 orphan lock") {
		t.Fatalf("message = %q", check.Message)
	}
	dry, err := check.FixFunc(context.Background(), true)
	if err != nil {
		t.Fatalf("FixFunc(dry-run) error = %v", err)
	}
	if !strings.Contains(dry, "1 orphan lock") {
		t.Fatalf("dry-run = %q", dry)
	}
	applied, err := check.FixFunc(context.Background(), false)
	if err != nil {
		t.Fatalf("FixFunc() error = %v", err)
	}
	if !strings.Contains(applied, "removed=1") {
		t.Fatalf("applied = %q", applied)
	}
	if _, err := os.Stat(orphanLock); !os.IsNotExist(err) {
		t.Fatalf("orphan lock must be removed by doctor --fix, stat err=%v", err)
	}
}

func TestSweepHookMemoryExtractSidecarsSkipsHeldLock(t *testing.T) {
	stateDir := t.TempDir()
	t.Setenv(hookStateDirEnvKey, stateDir)
	now := time.Date(2026, 7, 13, 12, 0, 0, 0, time.UTC)
	queueDir := filepath.Join(stateDir, "memory-extract")
	if err := os.MkdirAll(queueDir, 0o700); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	held := filepath.Join(queueDir, "held.json.lock")
	if err := os.WriteFile(held, []byte{}, 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	aged := now.Add(-hookMemoryExtractTerminalRetention - time.Hour)
	if err := os.Chtimes(held, aged, aged); err != nil {
		t.Fatalf("Chtimes: %v", err)
	}
	lock := flock.New(held)
	ok, err := lock.TryLock()
	if err != nil || !ok {
		t.Fatalf("TryLock() ok=%v err=%v", ok, err)
	}
	t.Cleanup(func() { _ = lock.Unlock() })

	removed := sweepHookMemoryExtractSidecars(now, 5)
	if removed != 0 {
		t.Fatalf("removed = %d, want 0 while lock is held", removed)
	}
	if _, err := os.Stat(held); err != nil {
		t.Fatalf("held lock must survive sweep: %v", err)
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

func TestDrainHookMemoryExtractQueue_RemovesOrphanLocksPastRetention(t *testing.T) {
	stateDir := t.TempDir()
	t.Setenv(hookStateDirEnvKey, stateDir)
	now := time.Date(2026, 7, 13, 12, 0, 0, 0, time.UTC)
	queueDir := filepath.Join(stateDir, "memory-extract")
	if err := os.MkdirAll(queueDir, 0o700); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	orphanLock := filepath.Join(queueDir, "orphan.json.lock")
	if err := os.WriteFile(orphanLock, []byte{}, 0o600); err != nil {
		t.Fatalf("WriteFile(orphan lock): %v", err)
	}
	aged := now.Add(-hookMemoryExtractTerminalRetention - time.Hour)
	if err := os.Chtimes(orphanLock, aged, aged); err != nil {
		t.Fatalf("Chtimes: %v", err)
	}

	root := NewRootCLI(WithHookMemoryExtractLauncher(func(string) error {
		t.Fatal("no job to launch")
		return nil
	}))
	launched, removed := root.drainHookMemoryExtractQueue(now, 5)
	if launched != 0 || removed != 1 {
		t.Fatalf("launched=%d removed=%d want 0/1", launched, removed)
	}
	if _, err := os.Stat(orphanLock); !os.IsNotExist(err) {
		t.Fatalf("orphan lock must be removed, stat err=%v", err)
	}
}

func TestDrainHookMemoryExtractQueue_RetainsRecentOrphanLock(t *testing.T) {
	stateDir := t.TempDir()
	t.Setenv(hookStateDirEnvKey, stateDir)
	now := time.Date(2026, 7, 13, 12, 0, 0, 0, time.UTC)
	queueDir := filepath.Join(stateDir, "memory-extract")
	if err := os.MkdirAll(queueDir, 0o700); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	orphanLock := filepath.Join(queueDir, "fresh.json.lock")
	if err := os.WriteFile(orphanLock, []byte{}, 0o600); err != nil {
		t.Fatalf("WriteFile(orphan lock): %v", err)
	}
	recent := now.Add(-time.Minute)
	if err := os.Chtimes(orphanLock, recent, recent); err != nil {
		t.Fatalf("Chtimes: %v", err)
	}

	root := &RootCLI{}
	launched, removed := root.drainHookMemoryExtractQueue(now, 5)
	if launched != 0 || removed != 0 {
		t.Fatalf("launched=%d removed=%d want 0/0", launched, removed)
	}
	if _, err := os.Stat(orphanLock); err != nil {
		t.Fatalf("recent orphan lock must survive a mid-creation race: %v", err)
	}
}

func TestDrainHookMemoryExtractQueue_SweepsAgedTmpFiles(t *testing.T) {
	stateDir := t.TempDir()
	t.Setenv(hookStateDirEnvKey, stateDir)
	now := time.Date(2026, 7, 13, 12, 0, 0, 0, time.UTC)
	queueDir := filepath.Join(stateDir, "memory-extract")
	if err := os.MkdirAll(queueDir, 0o700); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	tmp := filepath.Join(queueDir, ".memory-extract-crashed123.tmp")
	if err := os.WriteFile(tmp, []byte("{"), 0o600); err != nil {
		t.Fatalf("WriteFile(tmp): %v", err)
	}
	aged := now.Add(-hookMemoryExtractTerminalRetention - time.Hour)
	if err := os.Chtimes(tmp, aged, aged); err != nil {
		t.Fatalf("Chtimes: %v", err)
	}

	root := &RootCLI{}
	launched, removed := root.drainHookMemoryExtractQueue(now, 5)
	if launched != 0 || removed != 1 {
		t.Fatalf("launched=%d removed=%d want 0/1", launched, removed)
	}
	if _, err := os.Stat(tmp); !os.IsNotExist(err) {
		t.Fatalf("aged tmp must be removed, stat err=%v", err)
	}
}

func TestDrainHookMemoryExtractQueue_KeepsLockWithLiveJobSibling(t *testing.T) {
	stateDir := t.TempDir()
	t.Setenv(hookStateDirEnvKey, stateDir)
	now := time.Date(2026, 7, 13, 12, 0, 0, 0, time.UTC)
	request := hookMemoryExtractRequest{
		SessionID:      types.SessionID("live-sibling"),
		Workspace:      types.Workspace("traceary"),
		DBPath:         filepath.Join(t.TempDir(), "traceary.db"),
		SourceBoundary: "session_end",
	}
	path, err := enqueueHookMemoryExtract(request, now.Add(-48*time.Hour))
	if err != nil {
		t.Fatalf("enqueue: %v", err)
	}
	lockPath := path + ".lock"
	aged := now.Add(-hookMemoryExtractTerminalRetention - time.Hour)
	if err := os.Chtimes(lockPath, aged, aged); err != nil {
		t.Fatalf("Chtimes: %v", err)
	}

	root := NewRootCLI(WithHookMemoryExtractLauncher(func(string) error { return nil }))
	if _, removed := root.drainHookMemoryExtractQueue(now, 5); removed != 0 {
		t.Fatalf("removed = %d, want 0: pending job's lock has a live sibling", removed)
	}
	if _, err := os.Stat(lockPath); err != nil {
		t.Fatalf("lock with live job sibling must survive: %v", err)
	}
}

func TestDrainHookMemoryExtractQueue_SidecarSweepIsBounded(t *testing.T) {
	stateDir := t.TempDir()
	t.Setenv(hookStateDirEnvKey, stateDir)
	now := time.Date(2026, 7, 13, 12, 0, 0, 0, time.UTC)
	queueDir := filepath.Join(stateDir, "memory-extract")
	if err := os.MkdirAll(queueDir, 0o700); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	aged := now.Add(-hookMemoryExtractTerminalRetention - time.Hour)
	const orphanCount = 20
	for i := 0; i < orphanCount; i++ {
		lockPath := filepath.Join(queueDir, fmt.Sprintf("orphan-%02d.json.lock", i))
		if err := os.WriteFile(lockPath, []byte{}, 0o600); err != nil {
			t.Fatalf("WriteFile: %v", err)
		}
		if err := os.Chtimes(lockPath, aged, aged); err != nil {
			t.Fatalf("Chtimes: %v", err)
		}
	}

	root := &RootCLI{}
	const limit = 5
	_, removed := root.drainHookMemoryExtractQueue(now, limit)
	if removed != limit {
		t.Fatalf("removed = %d, want bounded to limit=%d", removed, limit)
	}
	entries, err := os.ReadDir(queueDir)
	if err != nil {
		t.Fatalf("ReadDir: %v", err)
	}
	if len(entries) != orphanCount-limit {
		t.Fatalf("remaining entries = %d, want %d (one pass must not drain the whole directory)", len(entries), orphanCount-limit)
	}
}

func TestDrainHookMemoryExtractQueueUntilDrainsMultipleBatchesBeforeDeadline(t *testing.T) {
	stateDir := t.TempDir()
	t.Setenv(hookStateDirEnvKey, stateDir)
	now := time.Date(2026, 7, 13, 12, 0, 0, 0, time.UTC)
	queueDir := filepath.Join(stateDir, "memory-extract")
	if err := os.MkdirAll(queueDir, 0o700); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	aged := now.Add(-hookMemoryExtractTerminalRetention - time.Hour)
	const orphanCount = 12
	for i := 0; i < orphanCount; i++ {
		lockPath := filepath.Join(queueDir, fmt.Sprintf("orphan-%02d.json.lock", i))
		if err := os.WriteFile(lockPath, []byte{}, 0o600); err != nil {
			t.Fatalf("WriteFile: %v", err)
		}
		if err := os.Chtimes(lockPath, aged, aged); err != nil {
			t.Fatalf("Chtimes: %v", err)
		}
	}

	root := &RootCLI{}
	deadline := now.Add(time.Hour)
	launched, removed := root.drainHookMemoryExtractQueueUntil(now, deadline, 5, func() time.Time { return now })
	if launched != 0 || removed != orphanCount {
		t.Fatalf("launched=%d removed=%d, want 0/%d", launched, removed, orphanCount)
	}
	entries, err := os.ReadDir(queueDir)
	if err != nil {
		t.Fatalf("ReadDir: %v", err)
	}
	if len(entries) != 0 {
		t.Fatalf("remaining entries=%d, want 0", len(entries))
	}
}

func TestDrainHookMemoryExtractQueueUntilStopsWhenDeadlineHasPassed(t *testing.T) {
	stateDir := t.TempDir()
	t.Setenv(hookStateDirEnvKey, stateDir)
	now := time.Date(2026, 7, 13, 12, 0, 0, 0, time.UTC)
	queueDir := filepath.Join(stateDir, "memory-extract")
	if err := os.MkdirAll(queueDir, 0o700); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	aged := now.Add(-hookMemoryExtractTerminalRetention - time.Hour)
	const orphanCount = 12
	for i := 0; i < orphanCount; i++ {
		lockPath := filepath.Join(queueDir, fmt.Sprintf("orphan-%02d.json.lock", i))
		if err := os.WriteFile(lockPath, []byte{}, 0o600); err != nil {
			t.Fatalf("WriteFile: %v", err)
		}
		if err := os.Chtimes(lockPath, aged, aged); err != nil {
			t.Fatalf("Chtimes: %v", err)
		}
	}

	root := &RootCLI{}
	deadline := now.Add(-time.Second)
	launched, removed := root.drainHookMemoryExtractQueueUntil(now, deadline, 5, func() time.Time { return now })
	if launched != 0 || removed != 0 {
		t.Fatalf("launched=%d removed=%d, want 0/0", launched, removed)
	}
	entries, err := os.ReadDir(queueDir)
	if err != nil {
		t.Fatalf("ReadDir: %v", err)
	}
	if len(entries) != orphanCount {
		t.Fatalf("remaining entries=%d, want %d", len(entries), orphanCount)
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
