package cli

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"golang.org/x/xerrors"
)

func TestInspectHookGrokTranscriptDiagnosticsReportsPendingFailureMetadata(t *testing.T) {
	stateDir := t.TempDir()
	t.Setenv(hookStateDirEnvKey, stateDir)
	now := time.Date(2026, 7, 14, 12, 0, 0, 0, time.UTC)
	payload := []byte(`{"session_id":"private-session","prompt_id":"prompt-1","transcript_path":"/private/transcript/updates.jsonl"}`)
	path, shouldLaunch, err := enqueueHookGrokTranscript(payload, filepath.Join(t.TempDir(), "traceary.db"), now.Add(-3*time.Minute))
	if err != nil {
		t.Fatalf("enqueueHookGrokTranscript() error = %v", err)
	}
	if !shouldLaunch {
		t.Fatal("initial enqueue must request a worker launch")
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

	check := (&RootCLI{}).inspectHookGrokTranscriptDiagnostics(now)
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
	check := (&RootCLI{}).inspectHookGrokTranscriptDiagnostics(time.Now().UTC())
	if check.Status != doctorStatusPass {
		t.Fatalf("check = %+v, want pass", check)
	}
}

func TestInspectHookGrokTranscriptDiagnosticsReportsBodyFreeTerminalPartialState(t *testing.T) {
	stateDir := t.TempDir()
	t.Setenv(hookStateDirEnvKey, stateDir)
	now := time.Date(2026, 7, 25, 12, 0, 0, 0, time.UTC)
	jobPath := filepath.Join(stateDir, "grok-transcript", strings.Repeat("a", 64)+".json")
	if err := writeHookGrokTranscriptTerminal(jobPath, "unavailable", now); err != nil {
		t.Fatalf("writeHookGrokTranscriptTerminal() error = %v", err)
	}
	terminalDir, err := hookGrokTranscriptTerminalDir()
	if err != nil {
		t.Fatalf("hookGrokTranscriptTerminalDir() error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(terminalDir, "broken.json"), []byte("{"), 0o600); err != nil {
		t.Fatalf("WriteFile(broken terminal) error = %v", err)
	}

	check := (&RootCLI{}).inspectHookGrokTranscriptDiagnostics(now)
	if check.Status != doctorStatusWarn {
		t.Fatalf("check = %+v, want warning", check)
	}
	for _, want := range []string{"1 partial final-turn disposition", "1 unavailable", "1 unreadable disposition marker"} {
		if !strings.Contains(check.Message, want) {
			t.Fatalf("check message = %q, want %q", check.Message, want)
		}
	}
	for _, private := range []string{jobPath, stateDir, strings.Repeat("a", 64)} {
		if strings.Contains(check.Message+check.Hint, private) {
			t.Fatalf("doctor output exposed terminal input %q: %+v", private, check)
		}
	}
}

func TestInspectHookGrokTranscriptDiagnosticsPassesWithRecordedTerminalOnly(t *testing.T) {
	stateDir := t.TempDir()
	t.Setenv(hookStateDirEnvKey, stateDir)
	now := time.Date(2026, 7, 25, 12, 0, 0, 0, time.UTC)
	jobPath := filepath.Join(stateDir, "grok-transcript", strings.Repeat("b", 64)+".json")
	if err := writeHookGrokTranscriptTerminal(jobPath, "recorded", now); err != nil {
		t.Fatalf("writeHookGrokTranscriptTerminal() error = %v", err)
	}

	check := (&RootCLI{}).inspectHookGrokTranscriptDiagnostics(now)
	if check.Status != doctorStatusPass {
		t.Fatalf("check = %+v, want pass for recorded-only terminal", check)
	}
	for _, want := range []string{"no pending", "1 final-turn transcript"} {
		if !strings.Contains(check.Message, want) {
			t.Fatalf("check message = %q, want %q", check.Message, want)
		}
	}
}

func TestScanHookGrokTranscriptJobsRejectsInvalidMetadata(t *testing.T) {
	stateDir := t.TempDir()
	t.Setenv(hookStateDirEnvKey, stateDir)
	dir := filepath.Join(stateDir, "grok-transcript")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	for name, contents := range map[string]string{
		"zero-time.json":         `{"schema_version":1,"payload":"{}"}`,
		"negative-attempts.json": `{"schema_version":1,"payload":"{}","requested_at":"2026-07-14T12:00:00Z","attempts":-1}`,
	} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(contents), 0o600); err != nil {
			t.Fatalf("WriteFile(%s) error = %v", name, err)
		}
	}

	jobs, unreadable, err := scanHookGrokTranscriptJobs()
	if err != nil {
		t.Fatalf("scanHookGrokTranscriptJobs() error = %v", err)
	}
	if len(jobs) != 0 || len(unreadable) != 2 {
		t.Fatalf("jobs=%d unreadable=%d, want zero jobs and two unreadable", len(jobs), len(unreadable))
	}
}

func TestRequeueHookGrokTranscriptJobRequeuesBeforeMaxAttempts(t *testing.T) {
	stateDir := t.TempDir()
	t.Setenv(hookStateDirEnvKey, stateDir)
	now := time.Date(2026, 7, 14, 12, 0, 0, 0, time.UTC)
	payload := []byte(`{"session_id":"session-a","prompt_id":"prompt-1","transcript_path":"/private/transcript/updates.jsonl"}`)
	path, _, err := enqueueHookGrokTranscript(payload, "", now)
	if err != nil {
		t.Fatalf("enqueueHookGrokTranscript() error = %v", err)
	}
	job, err := readHookGrokTranscriptJob(path)
	if err != nil {
		t.Fatalf("readHookGrokTranscriptJob() error = %v", err)
	}

	c := &RootCLI{}
	if err := c.requeueHookGrokTranscriptJob(path, job, xerrors.New("transcript remained delayed")); err == nil {
		t.Fatal("requeueHookGrokTranscriptJob() error = nil, want pending-requeue error")
	}

	requeued, err := readHookGrokTranscriptJob(path)
	if err != nil {
		t.Fatalf("readHookGrokTranscriptJob(requeued) error = %v", err)
	}
	if requeued.Attempts != 1 {
		t.Fatalf("requeued.Attempts = %d, want 1", requeued.Attempts)
	}
	if requeued.LastAttemptAt.IsZero() {
		t.Fatal("requeued.LastAttemptAt is zero, want set")
	}
	terminals, err := filepath.Glob(filepath.Join(stateDir, "grok-transcript-terminal", "*.json"))
	if err != nil || len(terminals) != 0 {
		t.Fatalf("terminal disposition files = %v, %v; want none while below the attempt ceiling", terminals, err)
	}
}

func TestRequeueHookGrokTranscriptJobFinalizesAtMaxAttempts(t *testing.T) {
	stateDir := t.TempDir()
	t.Setenv(hookStateDirEnvKey, stateDir)
	now := time.Date(2026, 7, 14, 12, 0, 0, 0, time.UTC)
	payload := []byte(`{"session_id":"session-b","prompt_id":"prompt-1","transcript_path":"/private/transcript/updates.jsonl"}`)
	path, _, err := enqueueHookGrokTranscript(payload, "", now)
	if err != nil {
		t.Fatalf("enqueueHookGrokTranscript() error = %v", err)
	}
	job, err := readHookGrokTranscriptJob(path)
	if err != nil {
		t.Fatalf("readHookGrokTranscriptJob() error = %v", err)
	}
	job.Attempts = hookGrokTranscriptMaxAttempts - 1
	if err := writeHookGrokTranscriptJob(path, job); err != nil {
		t.Fatalf("writeHookGrokTranscriptJob() error = %v", err)
	}

	c := &RootCLI{}
	if err := c.requeueHookGrokTranscriptJob(path, job, xerrors.New("transcript remained delayed")); err == nil {
		t.Fatal("requeueHookGrokTranscriptJob() error = nil, want exhausted-attempts error")
	}

	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("job at attempt ceiling still exists: %v", err)
	}
	terminals, err := filepath.Glob(filepath.Join(stateDir, "grok-transcript-terminal", "*.json"))
	if err != nil || len(terminals) != 1 {
		t.Fatalf("terminal disposition files = %v, %v; want one", terminals, err)
	}
	terminal, err := readHookGrokTranscriptTerminal(terminals[0])
	if err != nil {
		t.Fatalf("readHookGrokTranscriptTerminal() error = %v", err)
	}
	if terminal.Disposition != "unavailable" {
		t.Fatalf("terminal.Disposition = %q, want unavailable", terminal.Disposition)
	}
}

func TestDrainHookGrokTranscriptQueueGCsTerminalsPastRetentionWithLockSidecars(t *testing.T) {
	stateDir := t.TempDir()
	t.Setenv(hookStateDirEnvKey, stateDir)
	now := time.Date(2026, 7, 25, 12, 0, 0, 0, time.UTC)
	name := strings.Repeat("a", 64) + ".json"
	jobPath := filepath.Join(stateDir, "grok-transcript", name)
	if err := writeHookGrokTranscriptTerminal(jobPath, "unavailable", now.Add(-hookGrokTranscriptTerminalRetention-time.Hour)); err != nil {
		t.Fatalf("writeHookGrokTranscriptTerminal() error = %v", err)
	}
	if err := os.MkdirAll(filepath.Dir(jobPath), 0o700); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	if err := os.WriteFile(jobPath+".lock", []byte{}, 0o600); err != nil {
		t.Fatalf("WriteFile(lock sidecar) error = %v", err)
	}

	c := &RootCLI{}
	launched, removed := c.drainHookGrokTranscriptQueue(now, hookGrokTranscriptDoctorFixLimit)
	if launched != 0 || removed != 1 {
		t.Fatalf("drainHookGrokTranscriptQueue() = (%d, %d), want (0, 1)", launched, removed)
	}
	if _, err := os.Stat(jobPath + ".lock"); !os.IsNotExist(err) {
		t.Fatalf("leftover .lock sidecar still exists: %v", err)
	}
	terminalDir, err := hookGrokTranscriptTerminalDir()
	if err != nil {
		t.Fatalf("hookGrokTranscriptTerminalDir() error = %v", err)
	}
	if _, err := os.Stat(filepath.Join(terminalDir, name)); !os.IsNotExist(err) {
		t.Fatalf("terminal disposition marker still exists: %v", err)
	}
}

func TestDrainHookGrokTranscriptQueueRelaunchesDueJobsAndSkipsBackoffAndSkipPaths(t *testing.T) {
	stateDir := t.TempDir()
	t.Setenv(hookStateDirEnvKey, stateDir)
	now := time.Date(2026, 7, 25, 12, 0, 0, 0, time.UTC)

	dueJobPayload := []byte(`{"session_id":"due-session","prompt_id":"prompt-1","transcript_path":"/private/transcript/updates.jsonl"}`)
	duePath, _, err := enqueueHookGrokTranscript(dueJobPayload, "", now.Add(-time.Minute))
	if err != nil {
		t.Fatalf("enqueueHookGrokTranscript(due) error = %v", err)
	}

	skippedPayload := []byte(`{"session_id":"skipped-session","prompt_id":"prompt-1","transcript_path":"/private/transcript/updates.jsonl"}`)
	skippedPath, _, err := enqueueHookGrokTranscript(skippedPayload, "", now.Add(-time.Minute))
	if err != nil {
		t.Fatalf("enqueueHookGrokTranscript(skipped) error = %v", err)
	}

	backedOffPayload := []byte(`{"session_id":"backed-off-session","prompt_id":"prompt-1","transcript_path":"/private/transcript/updates.jsonl"}`)
	backedOffPath, _, err := enqueueHookGrokTranscript(backedOffPayload, "", now.Add(-time.Minute))
	if err != nil {
		t.Fatalf("enqueueHookGrokTranscript(backed off) error = %v", err)
	}
	backedOffJob, err := readHookGrokTranscriptJob(backedOffPath)
	if err != nil {
		t.Fatalf("readHookGrokTranscriptJob(backed off) error = %v", err)
	}
	backedOffJob.Attempts = 1
	backedOffJob.LastAttemptAt = now.Add(-time.Second)
	if err := writeHookGrokTranscriptJob(backedOffPath, backedOffJob); err != nil {
		t.Fatalf("writeHookGrokTranscriptJob(backed off) error = %v", err)
	}

	var launchedPaths []string
	c := &RootCLI{}
	c.hookGrokTranscriptLauncher = func(path string) error {
		launchedPaths = append(launchedPaths, path)
		return nil
	}

	launched, removed := c.drainHookGrokTranscriptQueue(now, hookGrokTranscriptDoctorFixLimit, skippedPath)
	if removed != 0 {
		t.Fatalf("removed = %d, want 0", removed)
	}
	if launched != 1 {
		t.Fatalf("launched = %d, want 1", launched)
	}
	if len(launchedPaths) != 1 || launchedPaths[0] != duePath {
		t.Fatalf("launchedPaths = %v, want only the due job %q", launchedPaths, duePath)
	}
}

func TestInspectHookGrokTranscriptDiagnosticsFixFuncDrainsAndGCs(t *testing.T) {
	stateDir := t.TempDir()
	t.Setenv(hookStateDirEnvKey, stateDir)
	now := time.Date(2026, 7, 25, 12, 0, 0, 0, time.UTC)

	payload := []byte(`{"session_id":"fix-session","prompt_id":"prompt-1","transcript_path":"/private/transcript/updates.jsonl"}`)
	if _, _, err := enqueueHookGrokTranscript(payload, "", now.Add(-time.Minute)); err != nil {
		t.Fatalf("enqueueHookGrokTranscript() error = %v", err)
	}

	var launched []string
	c := &RootCLI{}
	c.hookGrokTranscriptLauncher = func(path string) error {
		launched = append(launched, path)
		return nil
	}

	check := c.inspectHookGrokTranscriptDiagnostics(now)
	if check.Status != doctorStatusWarn {
		t.Fatalf("check = %+v, want warning", check)
	}
	if !check.AutoFixAvailable || check.FixFunc == nil || check.FixCommand != "traceary doctor --fix" {
		t.Fatalf("check = %+v, want an auto-fixable check with the doctor --fix command", check)
	}

	dryRunMessage, err := check.FixFunc(context.Background(), true)
	if err != nil {
		t.Fatalf("FixFunc(dryRun) error = %v", err)
	}
	if !strings.Contains(dryRunMessage, "would drain") {
		t.Fatalf("dryRunMessage = %q, want a would-drain preview", dryRunMessage)
	}
	if len(launched) != 0 {
		t.Fatalf("dry run launched %d worker(s), want zero", len(launched))
	}

	fixMessage, err := check.FixFunc(context.Background(), false)
	if err != nil {
		t.Fatalf("FixFunc(apply) error = %v", err)
	}
	if !strings.Contains(fixMessage, "launched=1") {
		t.Fatalf("fixMessage = %q, want launched=1", fixMessage)
	}
	if len(launched) != 1 {
		t.Fatalf("launched = %v, want exactly one relaunch", launched)
	}
}
