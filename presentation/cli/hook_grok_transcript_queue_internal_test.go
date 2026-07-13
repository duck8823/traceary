package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestInspectHookGrokTranscriptDiagnosticsReportsPendingFailureMetadata(t *testing.T) {
	stateDir := t.TempDir()
	t.Setenv(hookStateDirEnvKey, stateDir)
	now := time.Date(2026, 7, 14, 12, 0, 0, 0, time.UTC)
	payload := []byte(`{"session_id":"private-session","prompt_id":"prompt-1","transcript_path":"/private/transcript/updates.jsonl"}`)
	path, err := enqueueHookGrokTranscript(payload, filepath.Join(t.TempDir(), "traceary.db"), now.Add(-3*time.Minute))
	if err != nil {
		t.Fatalf("enqueueHookGrokTranscript() error = %v", err)
	}
	job, err := readHookGrokTranscriptJob(path)
	if err != nil {
		t.Fatalf("readHookGrokTranscriptJob() error = %v", err)
	}
	job.Attempts = 2
	job.LastError = "transcript unavailable"
	if err := writeHookGrokTranscriptJob(path, job); err != nil {
		t.Fatalf("writeHookGrokTranscriptJob() error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(stateDir, "grok-transcript", "broken.json"), []byte("{"), 0o600); err != nil {
		t.Fatalf("WriteFile(broken job) error = %v", err)
	}

	check := inspectHookGrokTranscriptDiagnostics(now)
	if check.Name != "hook-grok-transcript" || check.Status != doctorStatusWarn {
		t.Fatalf("check = %+v, want warning", check)
	}
	for _, want := range []string{"1 pending", "1 previously failed", "1 unreadable", "3m0s"} {
		if !strings.Contains(check.Message, want) {
			t.Fatalf("check message = %q, want %q", check.Message, want)
		}
	}
	for _, private := range []string{"private-session", "/private/transcript", path} {
		if strings.Contains(check.Message+check.Hint, private) {
			t.Fatalf("doctor output exposed private job data %q: %+v", private, check)
		}
	}
}

func TestInspectHookGrokTranscriptDiagnosticsPassesWithoutJobs(t *testing.T) {
	t.Setenv(hookStateDirEnvKey, t.TempDir())
	check := inspectHookGrokTranscriptDiagnostics(time.Now().UTC())
	if check.Status != doctorStatusPass {
		t.Fatalf("check = %+v, want pass", check)
	}
}
