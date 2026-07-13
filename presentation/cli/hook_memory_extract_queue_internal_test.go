package cli

import (
	"os"
	"path/filepath"
	"strings"
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

	check := inspectHookMemoryExtractDiagnostics(now)
	if check.Name != "hook-memory-extract" || check.Status != doctorStatusWarn {
		t.Fatalf("check = %+v, want warning", check)
	}
	for _, want := range []string{"1 pending", "1 previously failed", "1 unreadable", "3m0s"} {
		if !strings.Contains(check.Message, want) {
			t.Fatalf("check message = %q, want %q", check.Message, want)
		}
	}
}

func TestInspectHookMemoryExtractDiagnosticsPassesWithoutJobs(t *testing.T) {
	t.Setenv(hookStateDirEnvKey, t.TempDir())
	check := inspectHookMemoryExtractDiagnostics(time.Now().UTC())
	if check.Status != doctorStatusPass {
		t.Fatalf("check = %+v, want pass", check)
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
