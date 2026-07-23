package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/duck8823/traceary/application"
	apptypes "github.com/duck8823/traceary/application/types"
	"github.com/duck8823/traceary/application/usecase"
	"github.com/duck8823/traceary/domain/model"
	"github.com/duck8823/traceary/domain/types"
)

func TestRunHookDurably_RemovesSpoolAfterSuccess(t *testing.T) {
	stateDir := t.TempDir()
	t.Setenv(hookStateDirEnvKey, stateDir)
	c := &RootCLI{}
	var got string

	err := c.runHookDurably(context.Background(), "prompt", hookInvocationSpec{Command: "prompt", Client: "claude"}, strings.NewReader(`{"prompt":"hello"}`), func(input io.Reader) error {
		payload, err := readHookPayload(input)
		got = string(payload)
		return err
	})
	if err != nil {
		t.Fatalf("runHookDurably() error = %v", err)
	}
	if got != `{"prompt":"hello"}` {
		t.Fatalf("payload = %q", got)
	}
	entries, err := os.ReadDir(filepath.Join(stateDir, "spool"))
	if err != nil {
		t.Fatalf("ReadDir() error = %v", err)
	}
	if len(entries) != 0 {
		t.Fatalf("spool entries = %d, want 0", len(entries))
	}
}

func TestRunHookDurably_RetainsSpoolAfterFailure(t *testing.T) {
	stateDir := t.TempDir()
	t.Setenv(hookStateDirEnvKey, stateDir)
	t.Setenv("TRACEARY_HOOK_INPUT", `{"prompt":"current-env"}`)
	c := &RootCLI{}

	if err := c.runHookDurably(context.Background(), "prompt", hookInvocationSpec{Command: "prompt", Client: "claude"}, strings.NewReader(`{"prompt":"stdin"}`), func(input io.Reader) error {
		payload, err := readHookPayload(input)
		if err != nil {
			return err
		}
		if string(payload) != `{"prompt":"current-env"}` {
			t.Fatalf("payload = %q", payload)
		}
		return errors.New("database busy")
	}); err != nil {
		t.Fatalf("runHookDurably() must remain fail-soft, error = %v", err)
	}

	records, unreadable, err := scanHookSpoolRecords([]string{"claude"})
	if err != nil {
		t.Fatalf("scanHookSpoolRecords() error = %v", err)
	}
	if len(unreadable) != 0 || len(records) != 1 {
		t.Fatalf("records=%d unreadable=%d, want 1/0", len(records), len(unreadable))
	}
	if records[0].Payload != `{"prompt":"current-env"}` || records[0].Command != "prompt" {
		t.Fatalf("record = %#v", records[0])
	}
	info, err := os.Stat(records[0].Path)
	if err != nil {
		t.Fatalf("Stat() error = %v", err)
	}
	if got := info.Mode().Perm(); got != 0o600 {
		t.Fatalf("spool mode = %o, want 600", got)
	}

	check := c.inspectHookSpoolDiagnostics([]string{"claude"})
	if check.Status != doctorStatusWarn || !strings.Contains(check.Message, "1 pending") {
		t.Fatalf("doctor check = %#v", check)
	}
	if !check.AutoFixAvailable || check.FixFunc == nil {
		t.Fatalf("doctor check must expose auto-fix drain, got %#v", check)
	}
	if !strings.Contains(check.Hint, "doctor --fix") {
		t.Fatalf("hint should mention doctor --fix, got %q", check.Hint)
	}
}

func TestDrainHookSpoolRecords_ReplaysAndRemoves(t *testing.T) {
	stateDir := t.TempDir()
	t.Setenv(hookStateDirEnvKey, stateDir)

	eventStub := &spoolEventUsecaseStub{}
	root := NewRootCLI(
		WithStoreManagement(&spoolStoreManagementStub{}),
		WithEvent(eventStub),
	)

	// Seed a timeout-killed prompt record.
	record := hookSpoolRecord{
		SchemaVersion: hookSpoolSchemaVersion,
		Command:       "prompt",
		Client:        "claude",
		Payload:       `{"prompt":"recover me","session_id":"session-spool-1","cwd":"/tmp"}`,
		CreatedAt:     time.Now().UTC().Add(-time.Minute),
	}
	path, err := persistHookSpoolRecord(record)
	if err != nil {
		t.Fatalf("persistHookSpoolRecord() error = %v", err)
	}

	if n, f := root.drainHookSpoolRecords(context.Background(), 0); n != 0 || f != 0 {
		t.Fatalf("limit 0: replayed=%d failed=%d", n, f)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("limit 0 must not touch spool: %v", err)
	}

	replayed, failed := root.drainHookSpoolRecords(context.Background(), 5)
	if replayed != 1 || failed != 0 {
		t.Fatalf("successful replay: replayed=%d failed=%d want 1/0", replayed, failed)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("successful replay must remove spool, stat err=%v", err)
	}
	if eventStub.logCalls != 1 || eventStub.lastMessage != "recover me" {
		t.Fatalf("event log calls=%d message=%q", eventStub.logCalls, eventStub.lastMessage)
	}

	// Unsupported command is fail-closed: retain the record.
	bad := hookSpoolRecord{
		SchemaVersion: hookSpoolSchemaVersion,
		Command:       "not-a-real-hook",
		Client:        "claude",
		Payload:       `{}`,
		CreatedAt:     time.Now().UTC().Add(-2 * time.Minute),
	}
	badPath, err := persistHookSpoolRecord(bad)
	if err != nil {
		t.Fatalf("persist bad: %v", err)
	}
	replayed, failed = root.drainHookSpoolRecords(context.Background(), 5)
	if replayed != 0 || failed != 1 {
		t.Fatalf("unsupported: replayed=%d failed=%d want 0/1", replayed, failed)
	}
	if _, err := os.Stat(badPath); err != nil {
		t.Fatalf("unsupported command must not delete spool: %v", err)
	}
}

func TestCodexUsageHookSpool_IsBodyFreeAndReplayable(t *testing.T) {
	stateDir := t.TempDir()
	t.Setenv(hookStateDirEnvKey, stateDir)
	usage := &spoolCodexUsageStub{err: errors.New("database busy")}
	root := NewRootCLI(
		WithStoreManagement(&spoolStoreManagementStub{}),
		WithCodexUsage(usage),
	)
	payload := `{"session_id":"codex-session","event_id":"stop-1","last_assistant_message":"private body must never enter spool"}`
	if err := root.runCodexUsageHookDurably(context.Background(), strings.NewReader(payload), "codex", "/tmp/traceary.db"); err != nil {
		t.Fatalf("runCodexUsageHookDurably() must remain fail-soft: %v", err)
	}
	records, unreadable, err := scanHookSpoolRecords([]string{"codex"})
	if err != nil {
		t.Fatalf("scanHookSpoolRecords() error = %v", err)
	}
	if len(unreadable) != 0 || len(records) != 1 {
		t.Fatalf("records=%d unreadable=%d", len(records), len(unreadable))
	}
	if records[0].Command != "usage" || records[0].Payload != `{"session_id":"codex-session","event_id":"stop-1"}` || strings.Contains(records[0].Payload, "private") {
		t.Fatalf("usage spool record = %#v", records[0])
	}
	usage.err = nil
	replayed, failed := root.drainHookSpoolRecords(context.Background(), 5)
	if replayed != 1 || failed != 0 || len(usage.inputs) != 2 {
		t.Fatalf("replay = %d/%d calls=%d", replayed, failed, len(usage.inputs))
	}
}

func TestClaudeUsageHookSpool_IsBodyAndPathFreeAndReplayable(t *testing.T) {
	stateDir := t.TempDir()
	t.Setenv(hookStateDirEnvKey, stateDir)
	usage := &spoolClaudeUsageStub{err: errors.New("database busy")}
	root := NewRootCLI(
		WithStoreManagement(&spoolStoreManagementStub{}),
		WithClaudeUsage(usage),
	)
	payload := `{"session_id":"claude-session","event_id":"stop-1","transcript_path":"/private/transcript","last_assistant_message":"private body"}`
	if err := root.runClaudeUsageHookDurably(
		context.Background(), strings.NewReader(payload), "claude", "/tmp/traceary.db",
	); err != nil {
		t.Fatalf("runClaudeUsageHookDurably() must remain fail-soft: %v", err)
	}
	records, unreadable, err := scanHookSpoolRecords([]string{"claude"})
	if err != nil {
		t.Fatalf("scanHookSpoolRecords() error = %v", err)
	}
	if len(unreadable) != 0 || len(records) != 1 {
		t.Fatalf("records=%d unreadable=%d", len(records), len(unreadable))
	}
	if records[0].Command != "usage" ||
		records[0].Payload != `{"session_id":"claude-session","event_id":"stop-1"}` ||
		strings.Contains(records[0].Payload, "private") ||
		strings.Contains(records[0].Payload, "transcript") {
		t.Fatalf("usage spool record = %#v", records[0])
	}
	usage.err = nil
	replayed, failed := root.drainHookSpoolRecords(context.Background(), 5)
	if replayed != 1 || failed != 0 || len(usage.inputs) != 2 {
		t.Fatalf("replay = %d/%d calls=%d", replayed, failed, len(usage.inputs))
	}
}

func TestGeminiUsageHookSpool_IsBodyAndPathFreeAndReplayable(t *testing.T) {
	stateDir := t.TempDir()
	t.Setenv(hookStateDirEnvKey, stateDir)
	usage := &spoolGeminiUsageStub{err: errors.New("database busy")}
	root := NewRootCLI(
		WithStoreManagement(&spoolStoreManagementStub{}),
		WithGeminiUsage(usage),
	)
	payload := `{"session_id":"gemini-session","timestamp":"2026-07-23T01:00:00Z","transcript_path":"/private/transcript","prompt":"private prompt","prompt_response":"private response"}`
	if err := root.runGeminiUsageHookDurably(
		context.Background(), strings.NewReader(payload), "gemini", "/tmp/traceary.db",
	); err != nil {
		t.Fatalf("runGeminiUsageHookDurably() must remain fail-soft: %v", err)
	}
	records, unreadable, err := scanHookSpoolRecords([]string{"gemini"})
	if err != nil {
		t.Fatalf("scanHookSpoolRecords() error = %v", err)
	}
	if len(unreadable) != 0 || len(records) != 1 {
		t.Fatalf("records=%d unreadable=%d", len(records), len(unreadable))
	}
	if records[0].Command != "usage" ||
		records[0].Payload != `{"session_id":"gemini-session","timestamp":"2026-07-23T01:00:00Z"}` ||
		strings.Contains(records[0].Payload, "private") ||
		strings.Contains(records[0].Payload, "transcript") ||
		strings.Contains(records[0].Payload, "prompt") {
		t.Fatalf("usage spool record = %#v", records[0])
	}
	usage.err = nil
	replayed, failed := root.drainHookSpoolRecords(context.Background(), 5)
	if replayed != 1 || failed != 0 || len(usage.inputs) != 2 {
		t.Fatalf("replay = %d/%d calls=%d", replayed, failed, len(usage.inputs))
	}
}

func TestGeminiUsageHookSpool_RejectsUntrustedTimestamp(t *testing.T) {
	for name, timestamp := range map[string]string{
		"missing":          "",
		"prompt":           "private prompt",
		"escaped newline":  "2026-07-23T01:00:00Z\nprivate prompt",
		"oversized":        strings.Repeat("x", 65),
		"invalid calendar": "2026-02-30T01:00:00Z",
	} {
		t.Run(name, func(t *testing.T) {
			stateDir := t.TempDir()
			t.Setenv(hookStateDirEnvKey, stateDir)
			usage := &spoolGeminiUsageStub{err: errors.New("database busy")}
			root := NewRootCLI(
				WithStoreManagement(&spoolStoreManagementStub{}),
				WithGeminiUsage(usage),
			)
			payload, err := json.Marshal(map[string]string{
				"session_id": "gemini-session",
				"timestamp":  timestamp,
				"prompt":     "private prompt",
			})
			if err != nil {
				t.Fatalf("Marshal() error = %v", err)
			}
			if err := root.runGeminiUsageHookDurably(
				context.Background(), bytes.NewReader(payload), "gemini", "/tmp/traceary.db",
			); err != nil {
				t.Fatalf("runGeminiUsageHookDurably() error = %v", err)
			}
			records, unreadable, err := scanHookSpoolRecords([]string{"gemini"})
			if err != nil {
				t.Fatalf("scanHookSpoolRecords() error = %v", err)
			}
			if len(records) != 0 || len(unreadable) != 0 || len(usage.inputs) != 0 {
				t.Fatalf("records=%#v unreadable=%#v calls=%d", records, unreadable, len(usage.inputs))
			}
		})
	}
}

func TestDrainHookSpoolRecords_BatchLimitAndOldestFirst(t *testing.T) {
	stateDir := t.TempDir()
	t.Setenv(hookStateDirEnvKey, stateDir)

	eventStub := &spoolEventUsecaseStub{}
	root := NewRootCLI(
		WithStoreManagement(&spoolStoreManagementStub{}),
		WithEvent(eventStub),
	)

	older := hookSpoolRecord{
		SchemaVersion: hookSpoolSchemaVersion,
		Command:       "prompt",
		Client:        "claude",
		Payload:       `{"prompt":"older","session_id":"s-old","cwd":"/tmp"}`,
		CreatedAt:     time.Now().UTC().Add(-2 * time.Minute),
	}
	newer := hookSpoolRecord{
		SchemaVersion: hookSpoolSchemaVersion,
		Command:       "prompt",
		Client:        "claude",
		Payload:       `{"prompt":"newer","session_id":"s-new","cwd":"/tmp"}`,
		CreatedAt:     time.Now().UTC().Add(-time.Minute),
	}
	if _, err := persistHookSpoolRecord(older); err != nil {
		t.Fatalf("persist older: %v", err)
	}
	if _, err := persistHookSpoolRecord(newer); err != nil {
		t.Fatalf("persist newer: %v", err)
	}

	replayed, failed := root.drainHookSpoolRecords(context.Background(), 1)
	if replayed != 1 || failed != 0 {
		t.Fatalf("batch 1: replayed=%d failed=%d", replayed, failed)
	}
	if eventStub.logCalls != 1 || eventStub.lastMessage != "older" {
		t.Fatalf("expected oldest-first drain, got calls=%d message=%q", eventStub.logCalls, eventStub.lastMessage)
	}

	remaining, unreadable, err := scanHookSpoolRecords(nil)
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if len(unreadable) != 0 || len(remaining) != 1 || remaining[0].Payload != newer.Payload {
		t.Fatalf("remaining=%#v unreadable=%#v", remaining, unreadable)
	}
}

func TestDrainHookSpoolRecords_StopsOnCancelledContext(t *testing.T) {
	stateDir := t.TempDir()
	t.Setenv(hookStateDirEnvKey, stateDir)

	root := NewRootCLI(
		WithStoreManagement(&spoolStoreManagementStub{}),
		WithEvent(&spoolEventUsecaseStub{}),
	)
	record := hookSpoolRecord{
		SchemaVersion: hookSpoolSchemaVersion,
		Command:       "prompt",
		Client:        "claude",
		Payload:       `{"prompt":"cancelled","session_id":"s-cancel","cwd":"/tmp"}`,
		CreatedAt:     time.Now().UTC().Add(-time.Minute),
	}
	path, err := persistHookSpoolRecord(record)
	if err != nil {
		t.Fatalf("persist: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	replayed, failed := root.drainHookSpoolRecords(ctx, 5)
	if replayed != 0 || failed != 0 {
		t.Fatalf("cancelled context: replayed=%d failed=%d want 0/0", replayed, failed)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("cancelled drain must retain spool: %v", err)
	}
}

func TestDrainHookSpoolRecords_ReplaysKimiRecordThroughAdapter(t *testing.T) {
	stateDir := t.TempDir()
	t.Setenv(hookStateDirEnvKey, stateDir)

	eventStub := &spoolEventUsecaseStub{}
	root := NewRootCLI(
		WithStoreManagement(&spoolStoreManagementStub{}),
		WithEvent(eventStub),
	)
	// A timeout-killed Kimi UserPromptSubmit record: the payload keeps the
	// host's content-block prompt shape and must be normalized on replay.
	record := hookSpoolRecord{
		SchemaVersion: hookSpoolSchemaVersion,
		Command:       "kimi",
		Client:        "kimi",
		Action:        "user-prompt-submit",
		Payload:       `{"hook_event_name":"UserPromptSubmit","session_id":"session_kimi-spool","cwd":"/tmp","prompt":[{"type":"text","text":"recover kimi"}]}`,
		CreatedAt:     time.Now().UTC().Add(-time.Minute),
	}
	path, err := persistHookSpoolRecord(record)
	if err != nil {
		t.Fatalf("persist: %v", err)
	}

	replayed, failed := root.drainHookSpoolRecords(context.Background(), 5)
	if replayed != 1 || failed != 0 {
		t.Fatalf("kimi replay: replayed=%d failed=%d want 1/0", replayed, failed)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("successful kimi replay must remove spool, stat err=%v", err)
	}
	if eventStub.logCalls != 1 || eventStub.lastMessage != "recover kimi" {
		t.Fatalf("kimi replay log calls=%d message=%q, want flattened prompt", eventStub.logCalls, eventStub.lastMessage)
	}
}

func TestInspectHookSpoolDiagnostics_FixFuncDrains(t *testing.T) {
	stateDir := t.TempDir()
	t.Setenv(hookStateDirEnvKey, stateDir)

	eventStub := &spoolEventUsecaseStub{}
	root := NewRootCLI(
		WithStoreManagement(&spoolStoreManagementStub{}),
		WithEvent(eventStub),
	)
	record := hookSpoolRecord{
		SchemaVersion: hookSpoolSchemaVersion,
		Command:       "prompt",
		Client:        "claude",
		Payload:       `{"prompt":"doctor-fix","session_id":"s-fix","cwd":"/tmp"}`,
		CreatedAt:     time.Now().UTC().Add(-time.Minute),
	}
	if _, err := persistHookSpoolRecord(record); err != nil {
		t.Fatalf("persist: %v", err)
	}

	check := root.inspectHookSpoolDiagnostics([]string{"claude"})
	if check.Status != doctorStatusWarn || check.FixFunc == nil {
		t.Fatalf("check = %#v", check)
	}
	dryMsg, err := check.FixFunc(context.Background(), true)
	if err != nil {
		t.Fatalf("dry-run fix: %v", err)
	}
	if !strings.Contains(dryMsg, "1") {
		t.Fatalf("dry-run message = %q", dryMsg)
	}
	applyMsg, err := check.FixFunc(context.Background(), false)
	if err != nil {
		t.Fatalf("apply fix: %v", err)
	}
	if !strings.Contains(applyMsg, "replayed=1") {
		t.Fatalf("apply message = %q", applyMsg)
	}
	records, _, err := scanHookSpoolRecords(nil)
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if len(records) != 0 {
		t.Fatalf("after doctor --fix, remaining=%d", len(records))
	}
}

func TestReadHookPayload_ExplicitReaderOverridesEnvironment(t *testing.T) {
	t.Setenv("TRACEARY_HOOK_INPUT", `{"source":"env"}`)
	payload, err := readHookPayload(newExplicitHookPayloadReader([]byte(`{"source":"spool"}`)))
	if err != nil {
		t.Fatalf("readHookPayload() error = %v", err)
	}
	if got := string(payload); got != `{"source":"spool"}` {
		t.Fatalf("payload = %q", got)
	}
}

func TestHookSpoolSurvivesSIGTERM(t *testing.T) {
	if os.Getenv("TRACEARY_HOOK_SPOOL_SIGNAL_HELPER") == "1" {
		ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM)
		defer stop()
		_ = (&RootCLI{}).runHookDurably(ctx, "prompt", hookInvocationSpec{Command: "prompt", Client: "claude"}, strings.NewReader(`{"prompt":"preserve me"}`), func(io.Reader) error {
			<-ctx.Done()
			return ctx.Err()
		})
		return
	}
	if runtime.GOOS == "windows" {
		t.Skip("SIGTERM process test is not supported on Windows")
	}
	stateDir := t.TempDir()
	t.Setenv(hookStateDirEnvKey, stateDir)
	cmd := exec.Command(os.Args[0], "-test.run=^TestHookSpoolSurvivesSIGTERM$")
	cmd.Env = append(os.Environ(), "TRACEARY_HOOK_SPOOL_SIGNAL_HELPER=1", hookStateDirEnvKey+"="+stateDir)
	if err := cmd.Start(); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	spoolDir := filepath.Join(stateDir, "spool")
	deadline := time.Now().Add(5 * time.Second)
	for {
		entries, _ := os.ReadDir(spoolDir)
		if len(entries) == 1 {
			break
		}
		if time.Now().After(deadline) {
			_ = cmd.Process.Kill()
			t.Fatal("spool record was not published before timeout")
		}
		time.Sleep(10 * time.Millisecond)
	}
	if err := cmd.Process.Signal(syscall.SIGTERM); err != nil {
		t.Fatalf("Signal(SIGTERM) error = %v", err)
	}
	if err := cmd.Wait(); err != nil {
		t.Fatalf("helper exit error = %v", err)
	}
	records, unreadable, err := scanHookSpoolRecords([]string{"claude"})
	if err != nil {
		t.Fatalf("scanHookSpoolRecords() error = %v", err)
	}
	if len(unreadable) != 0 || len(records) != 1 || records[0].Payload != `{"prompt":"preserve me"}` {
		t.Fatalf("records=%#v unreadable=%#v", records, unreadable)
	}
}

// Minimal stubs for spool drain tests (package cli internal).

type spoolStoreManagementStub struct{}

func (s *spoolStoreManagementStub) Initialize(context.Context) error { return nil }
func (s *spoolStoreManagementStub) CreateBackup(context.Context, string, bool) error {
	return nil
}
func (s *spoolStoreManagementStub) RestoreBackup(context.Context, string, bool) error {
	return nil
}
func (s *spoolStoreManagementStub) CollectGarbage(context.Context, time.Time, apptypes.GarbageCollectionTarget, bool) (apptypes.CollectGarbageResult, error) {
	return apptypes.CollectGarbageResult{}, nil
}
func (s *spoolStoreManagementStub) CloseStaleSessions(context.Context, time.Duration, bool, []types.SessionID) (apptypes.CloseStaleSessionsResult, error) {
	return apptypes.CloseStaleSessionsResult{}, nil
}
func (s *spoolStoreManagementStub) DedupeContentEvents(context.Context, apptypes.ContentEventDedupeParams) (apptypes.ContentEventDedupeResult, error) {
	return apptypes.ContentEventDedupeResult{}, nil
}
func (s *spoolStoreManagementStub) RestoreContentEventDedupeRun(context.Context, string) (apptypes.ContentEventDedupeRestoreResult, error) {
	return apptypes.ContentEventDedupeRestoreResult{}, nil
}
func (s *spoolStoreManagementStub) CreateStoreArchive(context.Context, apptypes.StoreArchiveCreateParams) (apptypes.StoreArchiveResult, error) {
	return apptypes.StoreArchiveResult{}, nil
}
func (s *spoolStoreManagementStub) VerifyStoreArchive(context.Context, string, []byte) error {
	return nil
}
func (s *spoolStoreManagementStub) RestoreStoreArchive(context.Context, string, []byte, bool) (apptypes.StoreArchiveRestoreResult, error) {
	return apptypes.StoreArchiveRestoreResult{}, nil
}

type spoolEventUsecaseStub struct {
	logCalls    int
	lastMessage string
	logErr      error
}

type spoolCodexUsageStub struct {
	inputs []usecase.CodexUsageCaptureInput
	err    error
}

type spoolClaudeUsageStub struct {
	inputs []usecase.ClaudeUsageCaptureInput
	err    error
}

type spoolGeminiUsageStub struct {
	inputs []usecase.GeminiUsageCaptureInput
	err    error
}

func (s *spoolGeminiUsageStub) CaptureHeadless(
	_ context.Context,
	input usecase.GeminiUsageCaptureInput,
	_ application.GeminiUsageLoadResult,
) (usecase.GeminiUsageCaptureResult, error) {
	s.inputs = append(s.inputs, input)
	return usecase.GeminiUsageCaptureResult{}, s.err
}

func (s *spoolGeminiUsageStub) CaptureInteractiveUnavailable(
	_ context.Context,
	input usecase.GeminiUsageCaptureInput,
) (usecase.GeminiUsageCaptureResult, error) {
	s.inputs = append(s.inputs, input)
	return usecase.GeminiUsageCaptureResult{}, s.err
}

func (s *spoolClaudeUsageStub) Capture(
	_ context.Context,
	input usecase.ClaudeUsageCaptureInput,
) (usecase.ClaudeUsageCaptureResult, error) {
	s.inputs = append(s.inputs, input)
	return usecase.ClaudeUsageCaptureResult{}, s.err
}

func (s *spoolClaudeUsageStub) CaptureHeadless(
	_ context.Context,
	input usecase.ClaudeUsageCaptureInput,
	_ application.ClaudeUsageLoadResult,
) (usecase.ClaudeUsageCaptureResult, error) {
	s.inputs = append(s.inputs, input)
	return usecase.ClaudeUsageCaptureResult{}, s.err
}

func (s *spoolCodexUsageStub) Capture(_ context.Context, input usecase.CodexUsageCaptureInput) (usecase.CodexUsageCaptureResult, error) {
	s.inputs = append(s.inputs, input)
	return usecase.CodexUsageCaptureResult{}, s.err
}

func (s *spoolCodexUsageStub) CaptureHeadless(_ context.Context, input usecase.CodexUsageCaptureInput, _ application.CodexUsageLoadResult) (usecase.CodexUsageCaptureResult, error) {
	s.inputs = append(s.inputs, input)
	return usecase.CodexUsageCaptureResult{}, s.err
}

func (s *spoolEventUsecaseStub) Log(_ context.Context, message string, _ types.EventKind, _ types.Client, _ types.Agent, _ types.SessionID, _ types.Workspace, _ apptypes.LogRedaction) (*model.Event, error) {
	s.logCalls++
	s.lastMessage = message
	return nil, s.logErr
}
func (s *spoolEventUsecaseStub) Audit(context.Context, apptypes.AuditInput, apptypes.AuditRedaction) (*model.Event, *model.CommandAudit, error) {
	return nil, nil, nil
}
func (s *spoolEventUsecaseStub) Search(context.Context, apptypes.EventSearchCriteria) ([]*model.Event, error) {
	return nil, nil
}
func (s *spoolEventUsecaseStub) List(context.Context, apptypes.EventListCriteria) ([]*model.Event, error) {
	return nil, nil
}
func (s *spoolEventUsecaseStub) ListWindow(context.Context, apptypes.EventListCriteria) ([]*model.Event, error) {
	return nil, nil
}
func (s *spoolEventUsecaseStub) Show(context.Context, types.EventID) (apptypes.EventDetails, error) {
	return apptypes.EventDetails{}, nil
}
func (s *spoolEventUsecaseStub) Context(context.Context, apptypes.EventContextCriteria) ([]*model.Event, error) {
	return nil, nil
}
func (s *spoolEventUsecaseStub) Timeline(context.Context, apptypes.TimelineCriteria) ([]apptypes.TimelineBlock, error) {
	return nil, nil
}
