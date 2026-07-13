package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

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
