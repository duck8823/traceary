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
	"reflect"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/duck8823/traceary/application"
	"github.com/duck8823/traceary/application/queryservice"
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

func TestRunHookDurably_CommitsCurrentBeforeBacklogAndCancellation(t *testing.T) {
	stateDir := t.TempDir()
	t.Setenv(hookStateDirEnvKey, stateDir)
	eventStub := &spoolEventUsecaseStub{}
	root := NewRootCLI(
		WithStoreManagement(&spoolStoreManagementStub{}),
		WithEvent(eventStub),
	)
	backlog := hookSpoolRecord{
		SchemaVersion: hookSpoolSchemaVersion,
		Command:       "prompt",
		Client:        "claude",
		Payload:       `{"prompt":"backlog","session_id":"s-backlog","cwd":"/tmp"}`,
		CreatedAt:     time.Now().UTC().Add(-time.Minute),
	}
	backlogPath, err := persistHookSpoolRecord(backlog)
	if err != nil {
		t.Fatalf("persist backlog: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	err = root.runHookDurably(
		ctx,
		"prompt",
		hookInvocationSpec{Command: "prompt", Client: "claude"},
		strings.NewReader(`{"prompt":"current"}`),
		func(io.Reader) error {
			if eventStub.logCalls != 0 {
				t.Fatalf("backlog replayed before current delivery, calls=%d", eventStub.logCalls)
			}
			cancel()
			return nil
		},
	)
	if err != nil {
		t.Fatalf("runHookDurably() error = %v", err)
	}
	if _, err := os.Stat(backlogPath); err != nil {
		t.Fatalf("cancelled post-commit drain must retain backlog: %v", err)
	}
	records, unreadable, err := scanHookSpoolRecords(nil)
	if err != nil {
		t.Fatalf("scanHookSpoolRecords() error = %v", err)
	}
	if len(unreadable) != 0 || len(records) != 1 || records[0].Payload != backlog.Payload {
		t.Fatalf("records=%#v unreadable=%#v, want only backlog", records, unreadable)
	}
}

func TestRunHookDurably_ConcurrentDrainCannotConsumeActiveCurrentRecord(t *testing.T) {
	stateDir := t.TempDir()
	t.Setenv(hookStateDirEnvKey, stateDir)
	eventStub := &spoolEventUsecaseStub{}
	root := NewRootCLI(
		WithStoreManagement(&spoolStoreManagementStub{}),
		WithEvent(eventStub),
	)
	activeStarted := make(chan struct{})
	releaseActive := make(chan struct{})
	activeDone := make(chan struct{})
	go func() {
		defer close(activeDone)
		_ = root.runHookDurably(
			context.Background(),
			"prompt",
			hookInvocationSpec{Command: "prompt", Client: "claude"},
			strings.NewReader(`{"prompt":"active current","session_id":"s-active","cwd":"/tmp"}`),
			func(io.Reader) error {
				close(activeStarted)
				<-releaseActive
				return errors.New("active current failed")
			},
		)
	}()
	<-activeStarted

	if err := root.runHookDurably(
		context.Background(),
		"prompt",
		hookInvocationSpec{Command: "prompt", Client: "claude"},
		strings.NewReader(`{"prompt":"concurrent success","session_id":"s-concurrent","cwd":"/tmp"}`),
		func(io.Reader) error { return nil },
	); err != nil {
		t.Fatalf("concurrent runHookDurably() error = %v", err)
	}
	if eventStub.logCalls != 0 {
		t.Fatalf("concurrent drain replayed active current record, calls=%d", eventStub.logCalls)
	}
	close(releaseActive)
	<-activeDone

	records, unreadable, err := scanHookSpoolRecords(nil)
	if err != nil {
		t.Fatalf("scanHookSpoolRecords() error = %v", err)
	}
	if len(unreadable) != 0 || len(records) != 1 ||
		!strings.Contains(records[0].Payload, "active current") {
		t.Fatalf("records=%#v unreadable=%#v, want failed active current", records, unreadable)
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
	if _, err := os.Stat(badPath); !os.IsNotExist(err) {
		t.Fatalf("unsupported command must move to retry tail, stat err=%v", err)
	}
	remaining, unreadable, err := scanHookSpoolRecords(nil)
	if err != nil {
		t.Fatalf("scan retained unsupported record: %v", err)
	}
	if len(unreadable) != 0 || len(remaining) != 1 || remaining[0].Command != bad.Command {
		t.Fatalf("retained records=%#v unreadable=%#v", remaining, unreadable)
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
		"leading newline":  "\n2026-07-23T01:00:00Z",
		"trailing newline": "2026-07-23T01:00:00Z\r\n",
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

func TestGeminiUsageHookSpool_CanonicalizesTimestampToUTC(t *testing.T) {
	stateDir := t.TempDir()
	t.Setenv(hookStateDirEnvKey, stateDir)
	usage := &spoolGeminiUsageStub{err: errors.New("database busy")}
	root := NewRootCLI(
		WithStoreManagement(&spoolStoreManagementStub{}),
		WithGeminiUsage(usage),
	)
	payload := `{"session_id":"gemini-session","timestamp":"2026-07-23T10:00:00+09:00"}`
	if err := root.runGeminiUsageHookDurably(
		context.Background(), strings.NewReader(payload), "gemini", "/tmp/traceary.db",
	); err != nil {
		t.Fatalf("runGeminiUsageHookDurably() error = %v", err)
	}
	records, unreadable, err := scanHookSpoolRecords([]string{"gemini"})
	if err != nil {
		t.Fatalf("scanHookSpoolRecords() error = %v", err)
	}
	if len(unreadable) != 0 || len(records) != 1 ||
		records[0].Payload != `{"session_id":"gemini-session","timestamp":"2026-07-23T01:00:00Z"}` {
		t.Fatalf("records=%#v unreadable=%#v", records, unreadable)
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

func TestLoadHookSpoolReplayBatch_BoundsPayloadReads(t *testing.T) {
	stateDir := t.TempDir()
	t.Setenv(hookStateDirEnvKey, stateDir)
	spoolDir := filepath.Join(stateDir, "spool")
	if err := os.MkdirAll(spoolDir, 0o700); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	const pending = 10_000
	for i := range pending {
		path := filepath.Join(spoolDir, time.Unix(int64(i), 0).UTC().Format("20060102T150405.000000000Z")+"-claude-test.json")
		if err := os.WriteFile(path, []byte(`{"schema_version":1,"command":"not-a-real-hook","client":"claude","payload":"{}","created_at":"2026-07-24T00:00:00Z"}`), 0o600); err != nil {
			t.Fatalf("WriteFile(%d) error = %v", i, err)
		}
	}
	reads := 0
	records, unreadable, err := loadHookSpoolReplayBatch(5, func(path string) ([]byte, error) {
		reads++
		return os.ReadFile(path)
	})
	if err != nil {
		t.Fatalf("loadHookSpoolReplayBatch() error = %v", err)
	}
	if reads != 5 || len(records) != 5 || len(unreadable) != 0 {
		t.Fatalf("reads=%d records=%d unreadable=%d, want 5/5/0", reads, len(records), len(unreadable))
	}
}

func TestDrainHookSpoolRecords_DoesNotFollowExternalSymlink(t *testing.T) {
	stateDir := t.TempDir()
	t.Setenv(hookStateDirEnvKey, stateDir)
	spoolDir := filepath.Join(stateDir, "spool")
	if err := os.MkdirAll(spoolDir, 0o700); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	const privateMarker = "private sentinel must not be replayed"
	sentinel := filepath.Join(stateDir, "private-sentinel.json")
	sentinelRecord := `{"schema_version":1,"command":"prompt","client":"claude","payload":"{\"prompt\":\"` + privateMarker + `\",\"session_id\":\"s-private\",\"cwd\":\"/tmp\"}","created_at":"2026-07-24T00:00:00Z"}`
	if err := os.WriteFile(sentinel, []byte(sentinelRecord), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	link := filepath.Join(spoolDir, "00000000T000000.000000000Z-symlink.json")
	if err := os.Symlink(sentinel, link); err != nil {
		t.Skipf("symlink is unavailable: %v", err)
	}
	eventStub := &spoolEventUsecaseStub{}
	root := NewRootCLI(
		WithStoreManagement(&spoolStoreManagementStub{}),
		WithEvent(eventStub),
	)

	replayed, failed := root.drainHookSpoolRecords(context.Background(), 1)
	if replayed != 0 || failed != 1 || eventStub.logCalls != 0 {
		t.Fatalf("drain=%d/%d logCalls=%d, want 0/1/0", replayed, failed, eventStub.logCalls)
	}
	data, err := os.ReadFile(sentinel)
	if err != nil {
		t.Fatalf("sentinel must remain untouched: %v", err)
	}
	if string(data) != sentinelRecord {
		t.Fatal("sentinel content changed")
	}

	// Requeue of an unreadable symlink must not materialize a regular retry
	// JSON that would replay the external target on the next drain.
	if _, err := os.Lstat(link); !os.IsNotExist(err) {
		t.Fatalf("symlink must leave the pending spool, Lstat err=%v", err)
	}
	pendingJSON := listTopLevelSpoolJSON(t, spoolDir)
	if len(pendingJSON) != 0 {
		t.Fatalf("pending spool JSON after symlink requeue = %v, want none", pendingJSON)
	}
	for _, name := range pendingJSON {
		info, err := os.Lstat(filepath.Join(spoolDir, name))
		if err != nil {
			t.Fatalf("Lstat(%s): %v", name, err)
		}
		if info.Mode().IsRegular() {
			body, _ := os.ReadFile(filepath.Join(spoolDir, name))
			if strings.Contains(string(body), privateMarker) {
				t.Fatalf("requeue created replayable copy of external sentinel: %s", name)
			}
		}
	}

	replayed, failed = root.drainHookSpoolRecords(context.Background(), 5)
	if replayed != 0 || failed != 0 || eventStub.logCalls != 0 {
		t.Fatalf("second drain=%d/%d logCalls=%d, want 0/0/0 (no symlink target replay)", replayed, failed, eventStub.logCalls)
	}
	if eventStub.lastMessage == privateMarker || strings.Contains(eventStub.lastMessage, privateMarker) {
		t.Fatalf("second drain replayed sentinel payload: %q", eventStub.lastMessage)
	}
	data, err = os.ReadFile(sentinel)
	if err != nil {
		t.Fatalf("sentinel must remain after second drain: %v", err)
	}
	if string(data) != sentinelRecord {
		t.Fatal("sentinel content changed after second drain")
	}
}

func listTopLevelSpoolJSON(t *testing.T, spoolDir string) []string {
	t.Helper()
	entries, err := os.ReadDir(spoolDir)
	if err != nil {
		t.Fatalf("ReadDir(%s): %v", spoolDir, err)
	}
	var names []string
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		names = append(names, entry.Name())
	}
	return names
}

func TestInspectHookSpoolDiagnostics_DoesNotFollowExternalSymlink(t *testing.T) {
	stateDir := t.TempDir()
	t.Setenv(hookStateDirEnvKey, stateDir)
	spoolDir := filepath.Join(stateDir, "spool")
	if err := os.MkdirAll(spoolDir, 0o700); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	const privateMarker = "private doctor sentinel"
	sentinel := filepath.Join(stateDir, "private-sentinel.json")
	if err := os.WriteFile(sentinel, []byte(privateMarker), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	if err := os.Symlink(sentinel, filepath.Join(spoolDir, "external.json")); err != nil {
		t.Skipf("symlink is unavailable: %v", err)
	}

	check := (&RootCLI{}).inspectHookSpoolDiagnostics(nil)
	if check.Status != doctorStatusWarn || !strings.Contains(check.Message, "1 unreadable") {
		t.Fatalf("check=%#v", check)
	}
	if strings.Contains(check.Message, privateMarker) || strings.Contains(check.Hint, privateMarker) {
		t.Fatalf("doctor leaked sentinel content: %#v", check)
	}
}

func TestDrainHookSpoolRecords_RequeuesFailureBehindUnattemptedRecord(t *testing.T) {
	stateDir := t.TempDir()
	t.Setenv(hookStateDirEnvKey, stateDir)
	eventStub := &spoolEventUsecaseStub{}
	root := NewRootCLI(
		WithStoreManagement(&spoolStoreManagementStub{}),
		WithEvent(eventStub),
	)
	poisonPath, err := persistHookSpoolRecord(hookSpoolRecord{
		SchemaVersion: hookSpoolSchemaVersion,
		Command:       "not-a-real-hook",
		Client:        "claude",
		Payload:       `{}`,
		CreatedAt:     time.Now().UTC().Add(-2 * time.Minute),
	})
	if err != nil {
		t.Fatalf("persist poison: %v", err)
	}
	validPath, err := persistHookSpoolRecord(hookSpoolRecord{
		SchemaVersion: hookSpoolSchemaVersion,
		Command:       "prompt",
		Client:        "claude",
		Payload:       `{"prompt":"later valid","session_id":"s-later","cwd":"/tmp"}`,
		CreatedAt:     time.Now().UTC().Add(-time.Minute),
	})
	if err != nil {
		t.Fatalf("persist valid: %v", err)
	}

	replayed, failed := root.drainHookSpoolRecords(context.Background(), 1)
	if replayed != 0 || failed != 1 {
		t.Fatalf("first drain=%d/%d, want 0/1", replayed, failed)
	}
	if _, err := os.Stat(poisonPath); !os.IsNotExist(err) {
		t.Fatalf("poison path must move to retry tail, stat err=%v", err)
	}
	replayed, failed = root.drainHookSpoolRecords(context.Background(), 1)
	if replayed != 1 || failed != 0 {
		t.Fatalf("second drain=%d/%d, want 1/0", replayed, failed)
	}
	if _, err := os.Stat(validPath); !os.IsNotExist(err) {
		t.Fatalf("valid record must be removed, stat err=%v", err)
	}
	remaining, unreadable, err := scanHookSpoolRecords(nil)
	if err != nil {
		t.Fatalf("scanHookSpoolRecords() error = %v", err)
	}
	if len(unreadable) != 0 || len(remaining) != 1 || remaining[0].Command != "not-a-real-hook" {
		t.Fatalf("remaining=%#v unreadable=%#v", remaining, unreadable)
	}
}

func TestDrainHookSpoolRecords_RecoversStaleInflightRecord(t *testing.T) {
	stateDir := t.TempDir()
	t.Setenv(hookStateDirEnvKey, stateDir)
	eventStub := &spoolEventUsecaseStub{}
	root := NewRootCLI(
		WithStoreManagement(&spoolStoreManagementStub{}),
		WithEvent(eventStub),
	)
	path, err := persistCurrentHookSpoolRecord(hookSpoolRecord{
		SchemaVersion: hookSpoolSchemaVersion,
		Command:       "prompt",
		Client:        "claude",
		Payload:       `{"prompt":"stale inflight","session_id":"s-stale","cwd":"/tmp"}`,
		CreatedAt:     time.Now().UTC().Add(-2 * time.Minute),
	})
	if err != nil {
		t.Fatalf("persistCurrentHookSpoolRecord() error = %v", err)
	}
	staleAt := time.Now().Add(-2 * hookSpoolInflightStaleAge)
	if err := os.Chtimes(path, staleAt, staleAt); err != nil {
		t.Fatalf("Chtimes() error = %v", err)
	}

	replayed, failed := root.drainHookSpoolRecords(context.Background(), 1)
	if replayed != 1 || failed != 0 || eventStub.logCalls != 1 {
		t.Fatalf("drain=%d/%d logCalls=%d, want 1/0/1", replayed, failed, eventStub.logCalls)
	}
	remaining, err := countHookSpoolPendingPaths(time.Now().UTC())
	if err != nil {
		t.Fatalf("countHookSpoolPendingPaths() error = %v", err)
	}
	if remaining != 0 {
		t.Fatalf("remaining=%d, want 0", remaining)
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
	if check.Status != doctorStatusWarn || check.FixFunc == nil || check.StructuredFixFunc == nil {
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

func TestInspectHookSpoolDiagnostics_FixReportsUnreadableRemaining(t *testing.T) {
	stateDir := t.TempDir()
	t.Setenv(hookStateDirEnvKey, stateDir)
	eventStub := &spoolEventUsecaseStub{}
	root := NewRootCLI(
		WithStoreManagement(&spoolStoreManagementStub{}),
		WithEvent(eventStub),
	)
	if _, err := persistHookSpoolRecord(hookSpoolRecord{
		SchemaVersion: hookSpoolSchemaVersion,
		Command:       "prompt",
		Client:        "claude",
		Payload:       `{"prompt":"valid","session_id":"s-valid","cwd":"/tmp"}`,
		CreatedAt:     time.Now().UTC().Add(-time.Minute),
	}); err != nil {
		t.Fatalf("persist valid: %v", err)
	}
	spoolDir := filepath.Join(stateDir, "spool")
	if err := os.WriteFile(filepath.Join(spoolDir, "invalid.json"), []byte(`{"private":"must not be printed"`), 0o600); err != nil {
		t.Fatalf("write invalid: %v", err)
	}

	check := root.inspectHookSpoolDiagnostics([]string{"claude"})
	if check.FixFunc == nil || check.StructuredFixFunc == nil {
		t.Fatal("expected fix functions")
	}
	result, err := check.StructuredFixFunc(context.Background(), false)
	if err != nil {
		t.Fatalf("StructuredFixFunc() error = %v", err)
	}
	message := result.Action
	for _, expected := range []string{"replayed=1", "failed=0", "unreadable=1", "remaining=1"} {
		if !strings.Contains(message, expected) {
			t.Fatalf("message=%q, missing %q", message, expected)
		}
	}
	wantMetrics := map[string]int{"replayed": 1, "failed": 0, "remaining": 1, "unreadable": 1}
	if !reflect.DeepEqual(result.Metrics, wantMetrics) {
		t.Fatalf("metrics=%v, want %v", result.Metrics, wantMetrics)
	}
	if strings.Contains(message, "private") {
		t.Fatalf("message leaked payload body: %q", message)
	}
}

func TestApplyDoctorFixes_PreservesHookSpoolMetrics(t *testing.T) {
	root := NewRootCLI(WithStoreManagement(&spoolStoreManagementStub{}))
	check := doctorCheck{
		Name:             "hook-spool",
		Status:           doctorStatusWarn,
		Severity:         doctorSeverityWarn,
		AutoFixAvailable: true,
		FixFunc: func(context.Context, bool) (string, error) {
			return "legacy action", nil
		},
		StructuredFixFunc: func(context.Context, bool) (doctorFixResult, error) {
			return doctorFixResult{
				Action: "drained hook spool",
				Metrics: map[string]int{
					"replayed":   0,
					"failed":     2,
					"remaining":  2,
					"unreadable": 0,
				},
			}, nil
		},
	}

	fixes := root.applyDoctorFixes(context.Background(), &doctorReport{Checks: []doctorCheck{check}}, false)
	if len(fixes) != 1 {
		t.Fatalf("fixes=%v, want one", fixes)
	}
	want := map[string]int{"replayed": 0, "failed": 2, "remaining": 2, "unreadable": 0}
	if !reflect.DeepEqual(fixes[0].Metrics, want) {
		t.Fatalf("metrics=%v, want %v", fixes[0].Metrics, want)
	}
	encoded, err := json.Marshal(fixes[0])
	if err != nil {
		t.Fatalf("json.Marshal() error = %v", err)
	}
	for _, zeroField := range []string{`"replayed":0`, `"unreadable":0`} {
		if !bytes.Contains(encoded, []byte(zeroField)) {
			t.Fatalf("JSON=%s, missing zero-valued field %s", encoded, zeroField)
		}
	}
}

func TestApplyDoctorFixes_AcceptsStructuredOnlyFix(t *testing.T) {
	root := NewRootCLI(WithStoreManagement(&spoolStoreManagementStub{}))
	check := doctorCheck{
		Name:             "structured-only",
		Status:           doctorStatusWarn,
		Severity:         doctorSeverityWarn,
		AutoFixAvailable: true,
		StructuredFixFunc: func(context.Context, bool) (doctorFixResult, error) {
			return doctorFixResult{Action: "structured action"}, nil
		},
	}

	fixes := root.applyDoctorFixes(context.Background(), &doctorReport{Checks: []doctorCheck{check}}, false)
	if len(fixes) != 1 || fixes[0].Action != "structured action" {
		t.Fatalf("fixes=%v, want structured action", fixes)
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
	cmd.Env = append(os.Environ(), "TRACEARY_HOOK_SPOOL_SIGNAL_HELPER=1", testHookStateDirEnvKey+"="+stateDir)
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
func (s *spoolEventUsecaseStub) HydrateCommandAudits(context.Context, []*model.Event, queryservice.CommandAuditPayloadFields) error {
	return nil
}

func (s *spoolEventUsecaseStub) Timeline(context.Context, apptypes.TimelineCriteria) ([]apptypes.TimelineBlock, error) {
	return nil, nil
}

func (s *spoolStoreManagementStub) PurgeContentEventDedupeRun(context.Context, string) (apptypes.ContentEventDedupePurgeResult, error) {
	return apptypes.ContentEventDedupePurgeResult{}, nil
}

func (s *spoolStoreManagementStub) ListContentEventDedupeRuns(context.Context) ([]apptypes.ContentEventDedupeRun, error) {
	return nil, nil
}

func TestShouldDeadLetter(t *testing.T) {
	t.Parallel()
	cases := []struct {
		attempt int
		want    bool
	}{
		{0, false},
		{1, false},
		{2, false},
		{3, true},
		{4, true},
	}
	for _, tc := range cases {
		if got := shouldDeadLetter(tc.attempt); got != tc.want {
			t.Fatalf("shouldDeadLetter(%d) = %v, want %v", tc.attempt, got, tc.want)
		}
	}
}

func TestRequeueHookSpoolRecord_IncrementsAttemptUntilDeadLetter(t *testing.T) {
	stateDir := t.TempDir()
	t.Setenv(hookStateDirEnvKey, stateDir)
	root := NewRootCLI(
		WithStoreManagement(&spoolStoreManagementStub{}),
		WithEvent(&spoolEventUsecaseStub{}),
	)
	path, err := persistHookSpoolRecord(hookSpoolRecord{
		SchemaVersion: hookSpoolSchemaVersion,
		Command:       "not-a-real-hook",
		Client:        "claude",
		Payload:       `{}`,
		CreatedAt:     time.Now().UTC().Add(-time.Minute),
	})
	if err != nil {
		t.Fatalf("persist: %v", err)
	}

	// First two failures keep the record pending with incremented attempt_count.
	for wantAttempt := 1; wantAttempt < hookSpoolRetryLimit; wantAttempt++ {
		replayed, failed := root.drainHookSpoolRecords(context.Background(), 1)
		if replayed != 0 || failed != 1 {
			t.Fatalf("attempt %d: drain=%d/%d, want 0/1", wantAttempt, replayed, failed)
		}
		records, unreadable, err := scanHookSpoolRecords(nil)
		if err != nil {
			t.Fatalf("scan after attempt %d: %v", wantAttempt, err)
		}
		if len(unreadable) != 0 || len(records) != 1 {
			t.Fatalf("attempt %d: records=%#v unreadable=%#v", wantAttempt, records, unreadable)
		}
		if records[0].AttemptCount != wantAttempt {
			t.Fatalf("attempt %d: AttemptCount=%d, want %d", wantAttempt, records[0].AttemptCount, wantAttempt)
		}
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Fatalf("attempt %d: original path should have been renamed, stat err=%v", wantAttempt, err)
		}
		path = records[0].Path
	}

	// Final failure moves the record to spool/dead/ and excludes it from drain.
	replayed, failed := root.drainHookSpoolRecords(context.Background(), 1)
	if replayed != 0 || failed != 1 {
		t.Fatalf("cap drain=%d/%d, want 0/1", replayed, failed)
	}
	records, unreadable, err := scanHookSpoolRecords(nil)
	if err != nil {
		t.Fatalf("scan after cap: %v", err)
	}
	if len(records) != 0 || len(unreadable) != 0 {
		t.Fatalf("after cap pending records=%#v unreadable=%#v, want empty", records, unreadable)
	}
	deadDir := filepath.Join(stateDir, "spool", hookSpoolDeadDirName)
	deadEntries, err := os.ReadDir(deadDir)
	if err != nil {
		t.Fatalf("ReadDir(dead): %v", err)
	}
	if len(deadEntries) != 1 {
		t.Fatalf("dead entries=%d, want 1", len(deadEntries))
	}
	deadPath := filepath.Join(deadDir, deadEntries[0].Name())
	data, err := os.ReadFile(deadPath)
	if err != nil {
		t.Fatalf("ReadFile(dead): %v", err)
	}
	var dead hookSpoolRecord
	if err := json.Unmarshal(data, &dead); err != nil {
		t.Fatalf("Unmarshal dead: %v", err)
	}
	if dead.AttemptCount != hookSpoolRetryLimit {
		t.Fatalf("dead AttemptCount=%d, want %d", dead.AttemptCount, hookSpoolRetryLimit)
	}
	if dead.LastError == "" {
		t.Fatal("dead last_error must be set")
	}

	// Drain batch must not pick dead-letter files.
	batch, batchUnreadable, err := loadHookSpoolReplayBatch(5, os.ReadFile)
	if err != nil {
		t.Fatalf("loadHookSpoolReplayBatch: %v", err)
	}
	if len(batch) != 0 || len(batchUnreadable) != 0 {
		t.Fatalf("batch after dead-letter records=%#v unreadable=%#v, want empty", batch, batchUnreadable)
	}
	replayed, failed = root.drainHookSpoolRecords(context.Background(), 5)
	if replayed != 0 || failed != 0 {
		t.Fatalf("post-dead drain=%d/%d, want 0/0", replayed, failed)
	}
	// Terminal records are retained, not deleted.
	if _, err := os.Stat(deadPath); err != nil {
		t.Fatalf("dead-letter must be retained: %v", err)
	}
}

func TestRequeueHookSpoolRecord_MissingAttemptCountTreatedAsZero(t *testing.T) {
	stateDir := t.TempDir()
	t.Setenv(hookStateDirEnvKey, stateDir)
	spoolDir := filepath.Join(stateDir, "spool")
	if err := os.MkdirAll(spoolDir, 0o700); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	// Old schema_version:1 records omit attempt_count; treat as 0 then 1.
	path := filepath.Join(spoolDir, "20260101T000000.000000000Z-claude-legacy.json")
	legacy := `{"schema_version":1,"command":"not-a-real-hook","client":"claude","payload":"{}","created_at":"2026-01-01T00:00:00Z"}`
	if err := os.WriteFile(path, []byte(legacy+"\n"), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	if err := requeueHookSpoolRecord(path, "legacy fail"); err != nil {
		t.Fatalf("requeueHookSpoolRecord: %v", err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("legacy path must move, stat err=%v", err)
	}
	records, unreadable, err := scanHookSpoolRecords(nil)
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if len(unreadable) != 0 || len(records) != 1 {
		t.Fatalf("records=%#v unreadable=%#v", records, unreadable)
	}
	if records[0].AttemptCount != 1 {
		t.Fatalf("AttemptCount=%d, want 1 (missing field treated as 0 then incremented)", records[0].AttemptCount)
	}
	if records[0].LastError != "legacy fail" {
		t.Fatalf("LastError=%q, want legacy fail", records[0].LastError)
	}
}

func TestLoadHookSpoolReplayBatch_SkipsDeadLetterDirectory(t *testing.T) {
	stateDir := t.TempDir()
	t.Setenv(hookStateDirEnvKey, stateDir)
	spoolDir := filepath.Join(stateDir, "spool")
	deadDir := filepath.Join(spoolDir, hookSpoolDeadDirName)
	if err := os.MkdirAll(deadDir, 0o700); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	// A live pending record should still load.
	if _, err := persistHookSpoolRecord(hookSpoolRecord{
		SchemaVersion: hookSpoolSchemaVersion,
		Command:       "prompt",
		Client:        "claude",
		Payload:       `{"prompt":"live","session_id":"s-live","cwd":"/tmp"}`,
		CreatedAt:     time.Now().UTC().Add(-time.Minute),
	}); err != nil {
		t.Fatalf("persist live: %v", err)
	}
	// A dead-letter file must never appear in the replay batch.
	deadRecord := hookSpoolRecord{
		SchemaVersion: hookSpoolSchemaVersion,
		Command:       "not-a-real-hook",
		Client:        "claude",
		Payload:       `{}`,
		CreatedAt:     time.Now().UTC().Add(-2 * time.Minute),
		AttemptCount:  hookSpoolRetryLimit,
		LastError:     "poison",
	}
	deadPath := filepath.Join(deadDir, "dead-poison.json")
	if err := writeHookSpoolRecordAtomic(deadPath, deadRecord); err != nil {
		t.Fatalf("write dead: %v", err)
	}

	records, unreadable, err := loadHookSpoolReplayBatch(5, os.ReadFile)
	if err != nil {
		t.Fatalf("loadHookSpoolReplayBatch: %v", err)
	}
	if len(unreadable) != 0 || len(records) != 1 {
		t.Fatalf("records=%#v unreadable=%#v, want only live pending", records, unreadable)
	}
	if !strings.Contains(records[0].Payload, "live") {
		t.Fatalf("expected live payload, got %#v", records[0])
	}
	paths, err := listHookSpoolRecordPaths()
	if err != nil {
		t.Fatalf("listHookSpoolRecordPaths: %v", err)
	}
	for _, p := range paths {
		if strings.Contains(p, string(filepath.Separator)+hookSpoolDeadDirName+string(filepath.Separator)) {
			t.Fatalf("list included dead path %q", p)
		}
	}
}

func TestLoadHookSpoolReplayBatch_SkipsRecordsAtRetryCap(t *testing.T) {
	stateDir := t.TempDir()
	t.Setenv(hookStateDirEnvKey, stateDir)
	if _, err := persistHookSpoolRecord(hookSpoolRecord{
		SchemaVersion: hookSpoolSchemaVersion,
		Command:       "not-a-real-hook",
		Client:        "claude",
		Payload:       `{}`,
		CreatedAt:     time.Now().UTC().Add(-time.Minute),
		AttemptCount:  hookSpoolRetryLimit,
	}); err != nil {
		t.Fatalf("persist: %v", err)
	}
	records, unreadable, err := loadHookSpoolReplayBatch(5, os.ReadFile)
	if err != nil {
		t.Fatalf("loadHookSpoolReplayBatch: %v", err)
	}
	if len(records) != 0 || len(unreadable) != 0 {
		t.Fatalf("records=%#v unreadable=%#v, want skip at cap", records, unreadable)
	}
}

func TestInspectHookSpoolFilesystemMetadata_CountsWithoutReadingPayloads(t *testing.T) {
	stateDir := t.TempDir()
	t.Setenv(hookStateDirEnvKey, stateDir)
	spoolDir := filepath.Join(stateDir, "spool")
	deadDir := filepath.Join(spoolDir, hookSpoolDeadDirName)
	if err := os.MkdirAll(deadDir, 0o700); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	const secretPayload = "SECRET_PAYLOAD_MUST_NOT_APPEAR_IN_DOCTOR"
	pendingPath := filepath.Join(spoolDir, "20260101T000000.000000000Z-claude-pending.json")
	if err := os.WriteFile(pendingPath, []byte(`{"schema_version":1,"command":"prompt","client":"claude","payload":"`+secretPayload+`","created_at":"2026-01-01T00:00:00Z"}`+"\n"), 0o600); err != nil {
		t.Fatalf("WriteFile pending: %v", err)
	}
	deadPath := filepath.Join(deadDir, "dead-one.json")
	if err := os.WriteFile(deadPath, []byte(`{"schema_version":1,"attempt_count":3,"payload":"`+secretPayload+`"}`+"\n"), 0o600); err != nil {
		t.Fatalf("WriteFile dead: %v", err)
	}
	inflightPath := filepath.Join(spoolDir, "20260101T000001.000000000Z-claude.inflight")
	if err := os.WriteFile(inflightPath, []byte(`{"schema_version":1,"payload":"`+secretPayload+`"}`+"\n"), 0o600); err != nil {
		t.Fatalf("WriteFile inflight: %v", err)
	}
	staleAt := time.Now().Add(-2 * hookSpoolInflightStaleAge)
	if err := os.Chtimes(inflightPath, staleAt, staleAt); err != nil {
		t.Fatalf("Chtimes: %v", err)
	}

	stats, err := inspectHookSpoolFilesystemStats(time.Now().UTC())
	if err != nil {
		t.Fatalf("inspectHookSpoolFilesystemStats: %v", err)
	}
	if stats.PendingCount != 1 || stats.DeadCount != 1 || stats.StaleInflightCount != 1 {
		t.Fatalf("stats=%+v, want pending=1 dead=1 stale_inflight=1", stats)
	}
	if stats.PendingBytes <= 0 || stats.DeadBytes <= 0 || stats.StaleInflightBytes <= 0 {
		t.Fatalf("byte sizes must be positive: %+v", stats)
	}

	check := inspectHookSpoolFilesystemMetadata()
	if check.Status != doctorStatusWarn {
		t.Fatalf("status=%q, want warn", check.Status)
	}
	if !strings.Contains(check.Message, "pending=1") || !strings.Contains(check.Message, "dead=1") {
		t.Fatalf("message=%q, want pending and dead counts", check.Message)
	}
	if strings.Contains(check.Message, secretPayload) || strings.Contains(check.Hint, secretPayload) {
		t.Fatalf("doctor leaked payload body: %#v", check)
	}
}

func TestInspectHookSpoolDiagnostics_ReportsPendingAndTerminalCounts(t *testing.T) {
	stateDir := t.TempDir()
	t.Setenv(hookStateDirEnvKey, stateDir)
	if _, err := persistHookSpoolRecord(hookSpoolRecord{
		SchemaVersion: hookSpoolSchemaVersion,
		Command:       "prompt",
		Client:        "claude",
		Payload:       `{"prompt":"pending","session_id":"s-p","cwd":"/tmp"}`,
		CreatedAt:     time.Now().UTC().Add(-time.Minute),
	}); err != nil {
		t.Fatalf("persist: %v", err)
	}
	deadDir := filepath.Join(stateDir, "spool", hookSpoolDeadDirName)
	if err := os.MkdirAll(deadDir, 0o700); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := writeHookSpoolRecordAtomic(filepath.Join(deadDir, "dead.json"), hookSpoolRecord{
		SchemaVersion: hookSpoolSchemaVersion,
		Command:       "not-a-real-hook",
		Client:        "claude",
		Payload:       `{}`,
		CreatedAt:     time.Now().UTC(),
		AttemptCount:  hookSpoolRetryLimit,
	}); err != nil {
		t.Fatalf("write dead: %v", err)
	}

	check := (&RootCLI{}).inspectHookSpoolDiagnostics(nil)
	if check.Status != doctorStatusWarn {
		t.Fatalf("status=%q, want warn", check.Status)
	}
	if !strings.Contains(check.Message, "1 pending") || !strings.Contains(check.Message, "1 terminal") {
		t.Fatalf("message=%q, want pending and terminal counts", check.Message)
	}
}

func TestClaimHookSpoolRecord_ConcurrentExclusive(t *testing.T) {
	stateDir := t.TempDir()
	t.Setenv(hookStateDirEnvKey, stateDir)
	path, err := persistHookSpoolRecord(hookSpoolRecord{
		SchemaVersion: hookSpoolSchemaVersion,
		Command:       "not-a-real-hook",
		Client:        "claude",
		Payload:       `{}`,
		CreatedAt:     time.Now().UTC().Add(-time.Minute),
	})
	if err != nil {
		t.Fatalf("persist: %v", err)
	}

	const workers = 8
	start := make(chan struct{})
	var wg sync.WaitGroup
	var wins atomic.Int32
	var claimedPath atomic.Value
	for range workers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			got, ok, claimErr := claimHookSpoolRecord(path)
			if claimErr != nil {
				t.Errorf("claimHookSpoolRecord: %v", claimErr)
				return
			}
			if ok {
				wins.Add(1)
				claimedPath.Store(got)
			}
		}()
	}
	close(start)
	wg.Wait()

	if got := wins.Load(); got != 1 {
		t.Fatalf("claim winners=%d, want exactly 1", got)
	}
	gotPath, _ := claimedPath.Load().(string)
	if gotPath == "" || !isHookSpoolClaimFile(filepath.Base(gotPath)) {
		t.Fatalf("claimed path = %q, want *.json.claim-*", gotPath)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("original path must be gone after exclusive claim, stat err=%v", err)
	}
	// Exactly one claim file remains; no second copy of the original.
	entries, err := os.ReadDir(filepath.Dir(path))
	if err != nil {
		t.Fatalf("ReadDir: %v", err)
	}
	claimFiles := 0
	jsonFiles := 0
	for _, entry := range entries {
		switch {
		case isHookSpoolClaimFile(entry.Name()):
			claimFiles++
		case strings.HasSuffix(entry.Name(), ".json"):
			jsonFiles++
		}
	}
	if claimFiles != 1 || jsonFiles != 0 {
		t.Fatalf("claimFiles=%d jsonFiles=%d, want 1/0", claimFiles, jsonFiles)
	}
}

func TestDrainHookSpoolRecords_ConcurrentClaimSingleRecord(t *testing.T) {
	stateDir := t.TempDir()
	t.Setenv(hookStateDirEnvKey, stateDir)
	spoolDir := filepath.Join(stateDir, "spool")

	originalPath, err := persistHookSpoolRecord(hookSpoolRecord{
		SchemaVersion: hookSpoolSchemaVersion,
		Command:       "not-a-real-hook",
		Client:        "claude",
		Payload:       `{"marker":"concurrent-claim"}`,
		CreatedAt:     time.Now().UTC().Add(-time.Minute),
	})
	if err != nil {
		t.Fatalf("persist: %v", err)
	}

	// Two concurrent drains share one poison record. Regardless of scheduling
	// (second worker may or may not see the first worker's zz-retry), the
	// exclusive claim must keep a single retained file and never resurrect the
	// original basename as a duplicate replayable copy.
	const workers = 2
	start := make(chan struct{})
	var wg sync.WaitGroup
	results := make([]struct{ replayed, failed int }, workers)
	for i := range workers {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			root := NewRootCLI(
				WithStoreManagement(&spoolStoreManagementStub{}),
				WithEvent(&spoolEventUsecaseStub{}),
			)
			<-start
			results[i].replayed, results[i].failed = root.drainHookSpoolRecords(context.Background(), 5)
		}(i)
	}
	close(start)
	wg.Wait()

	totalReplayed := results[0].replayed + results[1].replayed
	if totalReplayed != 0 {
		t.Fatalf("replayed=%d, want 0 for poison record (results=%v)", totalReplayed, results)
	}
	if _, err := os.Stat(originalPath); !os.IsNotExist(err) {
		t.Fatalf("original path must be gone after claim, stat err=%v", err)
	}

	pending := listTopLevelSpoolJSON(t, spoolDir)
	deadDir := filepath.Join(spoolDir, hookSpoolDeadDirName)
	deadCount := 0
	if entries, err := os.ReadDir(deadDir); err == nil {
		for _, entry := range entries {
			if !entry.IsDir() && strings.HasSuffix(entry.Name(), ".json") {
				deadCount++
			}
		}
	} else if !os.IsNotExist(err) {
		t.Fatalf("ReadDir(dead): %v", err)
	}
	if len(pending)+deadCount != 1 {
		t.Fatalf("pending=%v dead=%d, want exactly one retained record (results=%v)", pending, deadCount, results)
	}
	if len(pending) == 1 {
		data, err := os.ReadFile(filepath.Join(spoolDir, pending[0]))
		if err != nil {
			t.Fatalf("ReadFile retry: %v", err)
		}
		var record hookSpoolRecord
		if err := json.Unmarshal(data, &record); err != nil {
			t.Fatalf("Unmarshal: %v", err)
		}
		if record.AttemptCount < 1 || record.AttemptCount > workers {
			t.Fatalf("AttemptCount=%d, want 1..%d", record.AttemptCount, workers)
		}
	}
	entries, err := os.ReadDir(spoolDir)
	if err != nil {
		t.Fatalf("ReadDir(spool): %v", err)
	}
	for _, entry := range entries {
		if isHookSpoolClaimFile(entry.Name()) {
			t.Fatalf("leftover claim file %q", entry.Name())
		}
	}
}

func TestRequeueHookSpoolRecord_SingleFileTransition(t *testing.T) {
	stateDir := t.TempDir()
	t.Setenv(hookStateDirEnvKey, stateDir)
	spoolDir := filepath.Join(stateDir, "spool")

	path, err := persistHookSpoolRecord(hookSpoolRecord{
		SchemaVersion: hookSpoolSchemaVersion,
		Command:       "not-a-real-hook",
		Client:        "claude",
		Payload:       `{"marker":"single-transition"}`,
		CreatedAt:     time.Now().UTC().Add(-time.Minute),
	})
	if err != nil {
		t.Fatalf("persist: %v", err)
	}

	if err := requeueHookSpoolRecord(path, "first failure"); err != nil {
		t.Fatalf("requeue: %v", err)
	}
	// After a successful requeue there must be exactly one replayable file
	// (the retry-tail rename of the original), not original+copy.
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("original path must be gone after requeue, stat err=%v", err)
	}
	pending := listTopLevelSpoolJSON(t, spoolDir)
	if len(pending) != 1 {
		t.Fatalf("pending JSON after requeue = %v, want exactly one retry file", pending)
	}
	if !strings.HasPrefix(pending[0], "zz-retry-") {
		t.Fatalf("retry name = %q, want zz-retry- prefix", pending[0])
	}
	// No leftover tmp from the in-place publish step.
	entries, err := os.ReadDir(spoolDir)
	if err != nil {
		t.Fatalf("ReadDir: %v", err)
	}
	for _, entry := range entries {
		if strings.HasSuffix(entry.Name(), ".tmp") {
			t.Fatalf("leftover tmp after requeue: %s", entry.Name())
		}
	}
	retryPath := filepath.Join(spoolDir, pending[0])
	data, err := os.ReadFile(retryPath)
	if err != nil {
		t.Fatalf("ReadFile retry: %v", err)
	}
	var record hookSpoolRecord
	if err := json.Unmarshal(data, &record); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if record.AttemptCount != 1 || record.LastError != "first failure" {
		t.Fatalf("record=%#v, want attempt=1 last_error=first failure", record)
	}

	// Drive remaining attempts to dead-letter; each step must keep exactly one file.
	for attempt := 1; attempt < hookSpoolRetryLimit; attempt++ {
		pending = listTopLevelSpoolJSON(t, spoolDir)
		if len(pending) != 1 {
			t.Fatalf("pending before attempt %d = %v, want exactly one", attempt+1, pending)
		}
		retryPath = filepath.Join(spoolDir, pending[0])
		if err := requeueHookSpoolRecord(retryPath, "again"); err != nil {
			t.Fatalf("requeue attempt %d: %v", attempt+1, err)
		}
	}
	if got := listTopLevelSpoolJSON(t, spoolDir); len(got) != 0 {
		t.Fatalf("pending after dead-letter = %v, want empty", got)
	}
	deadDir := filepath.Join(spoolDir, hookSpoolDeadDirName)
	deadEntries, err := os.ReadDir(deadDir)
	if err != nil {
		t.Fatalf("ReadDir(dead): %v", err)
	}
	jsonDead := 0
	for _, entry := range deadEntries {
		if !entry.IsDir() && strings.HasSuffix(entry.Name(), ".json") {
			jsonDead++
		}
	}
	if jsonDead != 1 {
		t.Fatalf("dead JSON count=%d, want exactly 1", jsonDead)
	}
}

func TestRequeueHookSpoolRecord_ClassifiedUnreplayableErrorsIncrementTowardDeadLetter(t *testing.T) {
	// The two observed poison classes must progress attempt_count via the real
	// requeue/dead-letter path. Validators stay strict; this only asserts
	// terminal retention after the cap.
	cases := []string{
		"invalid Kimi usage record metadata",
		"conflicting duplicate Claude assistant usage",
	}
	for _, lastError := range cases {
		t.Run(lastError, func(t *testing.T) {
			stateDir := t.TempDir()
			t.Setenv(hookStateDirEnvKey, stateDir)
			path, err := persistHookSpoolRecord(hookSpoolRecord{
				SchemaVersion: hookSpoolSchemaVersion,
				Command:       "usage",
				Client:        "claude",
				Payload:       `{}`,
				CreatedAt:     time.Now().UTC().Add(-time.Minute),
				AttemptCount:  hookSpoolRetryLimit - 1,
			})
			if err != nil {
				t.Fatalf("persist: %v", err)
			}
			if err := requeueHookSpoolRecord(path, lastError); err != nil {
				t.Fatalf("requeueHookSpoolRecord: %v", err)
			}
			deadDir := filepath.Join(stateDir, "spool", hookSpoolDeadDirName)
			entries, err := os.ReadDir(deadDir)
			if err != nil {
				t.Fatalf("ReadDir(dead): %v", err)
			}
			if len(entries) != 1 {
				t.Fatalf("dead entries=%d, want 1", len(entries))
			}
			data, err := os.ReadFile(filepath.Join(deadDir, entries[0].Name()))
			if err != nil {
				t.Fatalf("ReadFile: %v", err)
			}
			var dead hookSpoolRecord
			if err := json.Unmarshal(data, &dead); err != nil {
				t.Fatalf("Unmarshal: %v", err)
			}
			if dead.AttemptCount != hookSpoolRetryLimit {
				t.Fatalf("AttemptCount=%d, want %d", dead.AttemptCount, hookSpoolRetryLimit)
			}
			if dead.LastError != lastError {
				t.Fatalf("LastError=%q, want %q", dead.LastError, lastError)
			}
			batch, _, err := loadHookSpoolReplayBatch(5, os.ReadFile)
			if err != nil {
				t.Fatalf("loadHookSpoolReplayBatch: %v", err)
			}
			if len(batch) != 0 {
				t.Fatalf("dead-lettered poison still in batch: %#v", batch)
			}
		})
	}
}
