package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
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
	if check.Status != doctorStatusWarn || !strings.Contains(check.Message, "1 decoded pending") {
		t.Fatalf("doctor check = %#v", check)
	}
	if !check.AutoFixAvailable || check.FixFunc == nil {
		t.Fatalf("doctor check must expose auto-fix drain, got %#v", check)
	}
	if !strings.Contains(check.Hint, "doctor --fix") {
		t.Fatalf("hint should mention doctor --fix, got %q", check.Hint)
	}
}

func TestHookSpoolDrainAllowance(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name      string
		remaining time.Duration
		want      int
	}{
		{name: "1.5s in reserve", remaining: 1500 * time.Millisecond, want: 0},
		{name: "exactly drainReserve", remaining: hookSpoolDrainReserve, want: 0},
		{name: "3s low headroom", remaining: 3 * time.Second, want: 1},
		{name: "just under 4s", remaining: 4*time.Second - time.Nanosecond, want: 1},
		{name: "exactly 4s full batch", remaining: 4 * time.Second, want: hookSpoolReplayBatchLimit},
		{name: "8s full batch", remaining: 8 * time.Second, want: hookSpoolReplayBatchLimit},
		{name: "zero remaining", remaining: 0, want: 0},
		{name: "negative remaining", remaining: -time.Second, want: 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := hookSpoolDrainAllowance(tc.remaining); got != tc.want {
				t.Fatalf("hookSpoolDrainAllowance(%v) = %d, want %d", tc.remaining, got, tc.want)
			}
		})
	}
}

func TestHookSpoolBacklogDrainLimit(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name      string
		pending   int
		remaining time.Duration
		want      int
	}{
		{name: "reserve wins over backlog", pending: 3000, remaining: 2 * time.Second, want: 0},
		{name: "low headroom stays 1", pending: 3000, remaining: 3 * time.Second, want: 1},
		{name: "empty backlog uses minimum batch", pending: 0, remaining: 8 * time.Second, want: hookSpoolReplayBatchLimit},
		{name: "small backlog uses minimum batch", pending: 5, remaining: 8 * time.Second, want: hookSpoolReplayBatchLimit},
		{name: "500 pending still minimum", pending: 500, remaining: 8 * time.Second, want: hookSpoolReplayBatchLimit},
		{name: "3000 pending scales to 30", pending: 3000, remaining: 8 * time.Second, want: 30},
		{name: "huge backlog hits cap", pending: 10000, remaining: 8 * time.Second, want: hookSpoolReplayBacklogCap},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := hookSpoolBacklogDrainLimit(tc.pending, tc.remaining); got != tc.want {
				t.Fatalf("hookSpoolBacklogDrainLimit(%d, %v) = %d, want %d", tc.pending, tc.remaining, got, tc.want)
			}
		})
	}
}

func TestHookSpoolDrainRemaining_NoDeadlineUsesPackagedBudget(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 8, 13, 12, 0, 0, 0, time.UTC)
	// startedAt 9s ago → remaining 1s → allowance 0
	startedAt := now.Add(-9 * time.Second)
	remaining := hookSpoolDrainRemaining(context.Background(), startedAt, now)
	if remaining != time.Second {
		t.Fatalf("remaining = %v, want 1s", remaining)
	}
	if got := hookSpoolDrainAllowance(remaining); got != 0 {
		t.Fatalf("allowance for startedAt 9s ago = %d, want 0", got)
	}
}

func TestHookSpoolDrainRemaining_PrefersContextDeadline(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 8, 13, 12, 0, 0, 0, time.UTC)
	deadline := now.Add(1500 * time.Millisecond)
	ctx, cancel := context.WithDeadline(context.Background(), deadline)
	defer cancel()
	// startedAt is fresh, but ctx only has 1.5s left → reserve window.
	remaining := hookSpoolDrainRemaining(ctx, now, now)
	if remaining != 1500*time.Millisecond {
		t.Fatalf("remaining = %v, want 1.5s", remaining)
	}
	if got := hookSpoolDrainAllowance(remaining); got != 0 {
		t.Fatalf("allowance = %d, want 0", got)
	}
}

func TestRunHookDurably_SkipsDrainWhenDeadlineInReserve(t *testing.T) {
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

	// Leave only reserve-window headroom so opportunistic drain must skip.
	ctx, cancel := context.WithDeadline(context.Background(), time.Now().Add(1500*time.Millisecond))
	defer cancel()

	if err := root.runHookDurably(
		ctx,
		"prompt",
		hookInvocationSpec{Command: "prompt", Client: "claude"},
		strings.NewReader(`{"prompt":"current"}`),
		func(io.Reader) error { return nil },
	); err != nil {
		t.Fatalf("runHookDurably() error = %v (current delivery must still succeed)", err)
	}
	if eventStub.logCalls != 0 {
		t.Fatalf("drain ran under reserve budget, logCalls=%d", eventStub.logCalls)
	}
	if _, err := os.Stat(backlogPath); err != nil {
		t.Fatalf("backlog must remain untouched when drain skipped: %v", err)
	}
	records, unreadable, err := scanHookSpoolRecords(nil)
	if err != nil {
		t.Fatalf("scanHookSpoolRecords() error = %v", err)
	}
	if len(unreadable) != 0 || len(records) != 1 || records[0].Payload != backlog.Payload {
		t.Fatalf("records=%#v unreadable=%#v, want only untouched backlog", records, unreadable)
	}
}

func TestRunHookDurably_DrainsWhenBudgetAllows(t *testing.T) {
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
	if _, err := persistHookSpoolRecord(backlog); err != nil {
		t.Fatalf("persist backlog: %v", err)
	}

	// Explicit deadline well above low-headroom so a full batch is allowed.
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()

	if err := root.runHookDurably(
		ctx,
		"prompt",
		hookInvocationSpec{Command: "prompt", Client: "claude"},
		strings.NewReader(`{"prompt":"current"}`),
		func(io.Reader) error { return nil },
	); err != nil {
		t.Fatalf("runHookDurably() error = %v", err)
	}
	if eventStub.logCalls != 1 {
		t.Fatalf("logCalls=%d, want 1 (backlog replayed)", eventStub.logCalls)
	}
	records, unreadable, err := scanHookSpoolRecords(nil)
	if err != nil {
		t.Fatalf("scanHookSpoolRecords() error = %v", err)
	}
	if len(unreadable) != 0 || len(records) != 0 {
		t.Fatalf("records=%#v unreadable=%#v, want empty after drain", records, unreadable)
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

func TestDrainHookSpoolRecordsUntil_LoopsPastRoundLimit(t *testing.T) {
	stateDir := t.TempDir()
	t.Setenv(hookStateDirEnvKey, stateDir)
	eventStub := &spoolEventUsecaseStub{}
	root := NewRootCLI(
		WithStoreManagement(&spoolStoreManagementStub{}),
		WithEvent(eventStub),
	)
	total := hookSpoolDoctorDrainRoundLimit + 5
	base := time.Now().UTC().Add(-time.Minute)
	for i := 0; i < total; i++ {
		record := hookSpoolRecord{
			SchemaVersion: hookSpoolSchemaVersion,
			Command:       "prompt",
			Client:        "claude",
			Payload:       fmt.Sprintf(`{"prompt":"until-%d","session_id":"s-%d","cwd":"/tmp"}`, i, i),
			CreatedAt:     base.Add(time.Duration(i) * time.Millisecond),
		}
		if _, err := persistHookSpoolRecord(record); err != nil {
			t.Fatalf("persist %d: %v", i, err)
		}
	}
	result := root.drainHookSpoolRecordsUntil(context.Background(), total, hookSpoolDeadRequeueNow().Add(hookSpoolDeadRequeueDoctorWall))
	if result.Err != nil {
		t.Fatalf("until: %v", result.Err)
	}
	if result.Replayed != total {
		t.Fatalf("replayed=%d remaining=%d, want %d", result.Replayed, result.Remaining, total)
	}
	if eventStub.logCalls != total {
		t.Fatalf("logCalls=%d, want %d", eventStub.logCalls, total)
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
	wantMetrics := map[string]int{
		"requeued":             0,
		"skipped_nontransient": 0,
		"replayed":             1,
		"failed":               0,
		"remaining":            1,
		"unreadable":           1,
		"pruned_dead":          0,
		"dead_remaining":       0,
		"pruned_tmp":           0,
	}
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
func (s *spoolStoreManagementStub) PreviewOfflineMigrations(context.Context) ([]int64, error) {
	return nil, nil
}
func (s *spoolStoreManagementStub) InspectOneOffRepairRetirement(context.Context) (apptypes.OneOffRepairRetirement, error) {
	return apptypes.OneOffRepairRetirement{}, nil
}
func (s *spoolStoreManagementStub) InspectBoundDrop(context.Context) (apptypes.BoundDropInspection, error) {
	return apptypes.BoundDropInspection{}, nil
}
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

func (s *spoolEventUsecaseStub) Log(_ context.Context, message string, _ types.EventKind, _ types.Client, _ types.Agent, _ types.SessionID, _ types.Workspace, _ apptypes.LogRedaction) (apptypes.EventWriteResult, error) {
	s.logCalls++
	s.lastMessage = message
	return apptypes.EventWriteResult{}, s.logErr
}
func (s *spoolEventUsecaseStub) DeleteTranscript(context.Context, types.EventID) error {
	return nil
}
func (s *spoolEventUsecaseStub) Audit(context.Context, apptypes.AuditInput, apptypes.AuditRedaction) (apptypes.EventWriteResult, *model.CommandAudit, error) {
	return apptypes.EventWriteResult{}, nil, nil
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

	check := (&RootCLI{}).inspectHookSpoolFilesystemMetadata()
	if check.Status != doctorStatusWarn {
		t.Fatalf("status=%q, want warn", check.Status)
	}
	if !check.AutoFixAvailable || check.StructuredFixFunc == nil {
		t.Fatal("large-store hook-spool check must expose filesystem requeue/prune")
	}
	if !strings.Contains(check.Message, "pending=1") || !strings.Contains(check.Message, "dead=1") {
		t.Fatalf("message=%q, want pending and dead counts", check.Message)
	}
	if !strings.Contains(check.Message, "hook spool files") || !strings.Contains(check.Message, "metadata-only") || !strings.Contains(check.Message, doctorStoreIndependentLabel) {
		t.Fatalf("message=%q, want labeled metadata-only file units", check.Message)
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
	if !strings.Contains(check.Message, "1 decoded pending") || !strings.Contains(check.Message, "1 terminal") {
		t.Fatalf("message=%q, want decoded pending and terminal counts", check.Message)
	}
	if !strings.Contains(check.Message, "filesystem pending files (store-independent)=1") {
		t.Fatalf("message=%q, want filesystem pending file count for comparison", check.Message)
	}
}

func TestHookSpoolDeadLetterRequeueable(t *testing.T) {
	t.Parallel()
	cases := map[string]bool{
		"failed to ping SQLite DB: context deadline exceeded":                true,
		"Kimi usage session index scan cancelled: context deadline exceeded": true,
		"failed to insert hook delivery ledger row: database is locked":      true,
		"invalid Kimi usage record metadata":                                 true,
		"conflicting duplicate Claude assistant usage":                       true,
		"invalid Claude usage JSON event":                                    false,
		"":                                                                   false,
	}
	for lastError, want := range cases {
		if got := hookSpoolDeadLetterRequeueable(lastError); got != want {
			t.Fatalf("hookSpoolDeadLetterRequeueable(%q) = %t, want %t", lastError, got, want)
		}
	}
}

func TestRequeueHookSpoolDeadLetters_MovesTransientAndKeepsPoison(t *testing.T) {
	stateDir := t.TempDir()
	t.Setenv(hookStateDirEnvKey, stateDir)
	deadDir := filepath.Join(stateDir, "spool", hookSpoolDeadDirName)
	if err := os.MkdirAll(deadDir, 0o700); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	writeDead := func(name, lastError string) {
		t.Helper()
		record := hookSpoolRecord{
			SchemaVersion: hookSpoolSchemaVersion,
			Command:       "usage",
			Client:        "kimi",
			Payload:       `{"sentinel":"PRIVATE-BODY"}`,
			CreatedAt:     time.Now().UTC(),
			AttemptCount:  hookSpoolRetryLimit,
			LastError:     lastError,
		}
		encoded, err := json.Marshal(record)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(deadDir, name), append(encoded, '\n'), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	writeDead("deadline.json", "failed to ping SQLite DB: context deadline exceeded")
	writeDead("kimi.json", "invalid Kimi usage record metadata")
	writeDead("poison.json", "invalid Claude usage JSON event")

	requeued, skipped, remaining, err := requeueHookSpoolDeadLetters(context.Background(), time.Now().UTC(), true)
	if err != nil {
		t.Fatalf("dry-run requeue: %v", err)
	}
	if requeued != 2 || skipped != 1 {
		t.Fatalf("dry-run requeued=%d skipped=%d remaining=%d", requeued, skipped, remaining)
	}
	if entries, err := os.ReadDir(deadDir); err != nil || len(entries) != 3 {
		t.Fatalf("dry-run must leave dead files, entries=%v err=%v", entries, err)
	}

	requeued, skipped, remaining, err = requeueHookSpoolDeadLetters(context.Background(), time.Now().UTC(), false)
	if err != nil {
		t.Fatalf("requeue: %v", err)
	}
	if requeued != 2 || skipped != 1 || remaining != 1 {
		t.Fatalf("requeued=%d skipped=%d remaining=%d, want 2, 1, 1", requeued, skipped, remaining)
	}
	if _, err := os.Stat(filepath.Join(deadDir, "poison.json")); err != nil {
		t.Fatalf("poison must stay in dead: %v", err)
	}
	if _, err := os.Stat(filepath.Join(deadDir, "deadline.json")); !os.IsNotExist(err) {
		t.Fatalf("deadline dead-letter must leave dead, stat err=%v", err)
	}
	pending, err := os.ReadDir(filepath.Join(stateDir, "spool"))
	if err != nil {
		t.Fatal(err)
	}
	pendingJSON := 0
	for _, entry := range pending {
		if !entry.IsDir() && strings.HasSuffix(entry.Name(), ".json") {
			pendingJSON++
			body, readErr := os.ReadFile(filepath.Join(stateDir, "spool", entry.Name()))
			if readErr != nil {
				t.Fatal(readErr)
			}
			var record hookSpoolRecord
			if err := json.Unmarshal(body, &record); err != nil {
				t.Fatal(err)
			}
			if record.AttemptCount != 0 || record.LastError != "" {
				t.Fatalf("requeued record = %+v, want reset attempt and last_error", record)
			}
		}
	}
	if pendingJSON != 2 {
		t.Fatalf("pending JSON count=%d, want 2", pendingJSON)
	}
}

func writeTransientDeadLetter(t *testing.T, deadDir, name string) {
	t.Helper()
	record := hookSpoolRecord{
		SchemaVersion: hookSpoolSchemaVersion,
		Command:       "usage",
		Client:        "kimi",
		Payload:       `{"sentinel":"PRIVATE-BODY"}`,
		CreatedAt:     time.Now().UTC(),
		AttemptCount:  hookSpoolRetryLimit,
		LastError:     "failed to ping SQLite DB: context deadline exceeded",
	}
	encoded, err := json.Marshal(record)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(deadDir, name), append(encoded, '\n'), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestRequeueHookSpoolDeadLettersUntil_DrainsBeyondBatchLimitWithinBudget(t *testing.T) {
	stateDir := t.TempDir()
	t.Setenv(hookStateDirEnvKey, stateDir)
	deadDir := filepath.Join(stateDir, "spool", hookSpoolDeadDirName)
	if err := os.MkdirAll(deadDir, 0o700); err != nil {
		t.Fatal(err)
	}
	const extra = 17
	total := hookSpoolDeadRequeueLimit + extra
	for i := 0; i < total; i++ {
		writeTransientDeadLetter(t, deadDir, fmt.Sprintf("t-%04d.json", i))
	}
	writeDead := hookSpoolRecord{
		SchemaVersion: hookSpoolSchemaVersion,
		Command:       "usage",
		Client:        "claude",
		Payload:       `{}`,
		CreatedAt:     time.Now().UTC(),
		AttemptCount:  hookSpoolRetryLimit,
		LastError:     "invalid Claude usage JSON event",
	}
	encoded, err := json.Marshal(writeDead)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(deadDir, "poison.json"), append(encoded, '\n'), 0o600); err != nil {
		t.Fatal(err)
	}

	dryRequeued, drySkipped, _, err := requeueHookSpoolDeadLettersUntil(context.Background(), time.Now().UTC(), true, time.Time{})
	if err != nil {
		t.Fatalf("dry-run: %v", err)
	}
	if dryRequeued != total || drySkipped != 1 {
		t.Fatalf("dry-run requeued=%d skipped=%d, want %d and 1", dryRequeued, drySkipped, total)
	}
	if entries, err := os.ReadDir(deadDir); err != nil || len(entries) != total+1 {
		t.Fatalf("dry-run must leave dead files, n=%d err=%v", len(entries), err)
	}

	requeued, skipped, remaining, err := requeueHookSpoolDeadLettersUntil(context.Background(), time.Now().UTC(), false, hookSpoolDeadRequeueNow().Add(hookSpoolDeadRequeueDoctorWall))
	if err != nil {
		t.Fatalf("requeue: %v", err)
	}
	if requeued != total || skipped != 1 || remaining != 1 {
		t.Fatalf("requeued=%d skipped=%d remaining=%d, want %d, 1, 1", requeued, skipped, remaining, total)
	}
}

func TestRequeueHookSpoolDeadLettersUntil_StopsWhenWallClockExpires(t *testing.T) {
	stateDir := t.TempDir()
	t.Setenv(hookStateDirEnvKey, stateDir)
	deadDir := filepath.Join(stateDir, "spool", hookSpoolDeadDirName)
	if err := os.MkdirAll(deadDir, 0o700); err != nil {
		t.Fatal(err)
	}
	total := hookSpoolDeadRequeueLimit + 40
	for i := 0; i < total; i++ {
		writeTransientDeadLetter(t, deadDir, fmt.Sprintf("t-%04d.json", i))
	}

	origin := time.Date(2026, 8, 18, 0, 0, 0, 0, time.UTC)
	calls := 0
	hookSpoolDeadRequeueClock = func() time.Time {
		calls++
		// The deadline is passed in by the caller; the second loop check
		// must be past the 45s wall so only the first batch is requeued.
		if calls >= 2 {
			return origin.Add(hookSpoolDeadRequeueDoctorWall + time.Second)
		}
		return origin
	}
	t.Cleanup(func() { hookSpoolDeadRequeueClock = nil })

	requeued, _, remaining, err := requeueHookSpoolDeadLettersUntil(context.Background(), origin, false, origin.Add(hookSpoolDeadRequeueDoctorWall))
	if err != nil {
		t.Fatalf("requeue: %v", err)
	}
	if requeued != hookSpoolDeadRequeueLimit {
		t.Fatalf("requeued=%d, want first batch only (%d)", requeued, hookSpoolDeadRequeueLimit)
	}
	if remaining != total-hookSpoolDeadRequeueLimit {
		t.Fatalf("remaining=%d, want %d", remaining, total-hookSpoolDeadRequeueLimit)
	}
}

func TestFixHookSpoolRequeueThenDrain_DryRunPreviewsFullDrain(t *testing.T) {
	stateDir := t.TempDir()
	t.Setenv(hookStateDirEnvKey, stateDir)
	deadDir := filepath.Join(stateDir, "spool", hookSpoolDeadDirName)
	if err := os.MkdirAll(deadDir, 0o700); err != nil {
		t.Fatal(err)
	}
	total := hookSpoolDeadRequeueLimit + 9
	for i := 0; i < total; i++ {
		writeTransientDeadLetter(t, deadDir, fmt.Sprintf("t-%04d.json", i))
	}
	result, err := (&RootCLI{}).fixHookSpoolRequeueThenDrain(context.Background(), time.Now().UTC(), true)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(result.Action, fmt.Sprintf("would requeue %d transient", total)) {
		t.Fatalf("dry-run action=%q, want full planned count %d", result.Action, total)
	}
	if !strings.Contains(result.Action, fmt.Sprintf("drain up to %d pending hook spool record(s)", total)) {
		t.Fatalf("dry-run action=%q, want drain plan for %d requeued records", result.Action, total)
	}
	if entries, err := os.ReadDir(deadDir); err != nil || len(entries) != total {
		t.Fatalf("dry-run must leave files, n=%d err=%v", len(entries), err)
	}
}

func TestFixHookSpoolRequeueThenDrain_SharedWallBoundsDrain(t *testing.T) {
	origin := time.Date(2026, 8, 21, 0, 0, 0, 0, time.UTC)
	persistPending := func(t *testing.T, n int) {
		t.Helper()
		base := time.Now().UTC().Add(-time.Minute)
		for i := 0; i < n; i++ {
			if _, err := persistHookSpoolRecord(hookSpoolRecord{
				SchemaVersion: hookSpoolSchemaVersion,
				Command:       "prompt",
				Client:        "claude",
				Payload:       fmt.Sprintf(`{"prompt":"wall-%d","session_id":"s-wall-%d","cwd":"/tmp"}`, i, i),
				CreatedAt:     base.Add(time.Duration(i) * time.Millisecond),
			}); err != nil {
				t.Fatalf("persist %d: %v", i, err)
			}
		}
	}

	t.Run("wall hit after one replay leaves remaining", func(t *testing.T) {
		stateDir := t.TempDir()
		t.Setenv(hookStateDirEnvKey, stateDir)
		eventStub := &spoolEventUsecaseStub{}
		root := NewRootCLI(
			WithStoreManagement(&spoolStoreManagementStub{}),
			WithEvent(eventStub),
		)
		const total = 5
		persistPending(t, total)
		// The clock jumps past the 45s wall as soon as the first replay
		// commits, so the drain must stop before claiming the next record
		// instead of overrunning inside a 200-record round.
		hookSpoolDeadRequeueClock = func() time.Time {
			if eventStub.logCalls > 0 {
				return origin.Add(hookSpoolDeadRequeueDoctorWall + time.Second)
			}
			return origin
		}
		t.Cleanup(func() { hookSpoolDeadRequeueClock = nil })

		result, err := root.fixHookSpoolRequeueThenDrain(context.Background(), time.Now().UTC(), false)
		if err != nil {
			t.Fatalf("fix: %v", err)
		}
		if result.Metrics["replayed"] == 0 || result.Metrics["replayed"] >= total {
			t.Fatalf("replayed=%d, want at least one replay but << %d", result.Metrics["replayed"], total)
		}
		if result.Metrics["remaining"] == 0 {
			t.Fatalf("remaining=0, want leftover pending records: metrics=%v", result.Metrics)
		}
		if !strings.Contains(result.Action, fmt.Sprintf("remaining=%d", result.Metrics["remaining"])) {
			t.Fatalf("action=%q, want remaining=%d", result.Action, result.Metrics["remaining"])
		}
		if eventStub.logCalls != result.Metrics["replayed"] {
			t.Fatalf("logCalls=%d, want replayed=%d", eventStub.logCalls, result.Metrics["replayed"])
		}
	})

	t.Run("two pending drain to empty inside the wall", func(t *testing.T) {
		stateDir := t.TempDir()
		t.Setenv(hookStateDirEnvKey, stateDir)
		eventStub := &spoolEventUsecaseStub{}
		root := NewRootCLI(
			WithStoreManagement(&spoolStoreManagementStub{}),
			WithEvent(eventStub),
		)
		persistPending(t, 2)
		hookSpoolDeadRequeueClock = func() time.Time { return origin }
		t.Cleanup(func() { hookSpoolDeadRequeueClock = nil })

		result, err := root.fixHookSpoolRequeueThenDrain(context.Background(), time.Now().UTC(), false)
		if err != nil {
			t.Fatalf("fix: %v", err)
		}
		if result.Metrics["replayed"] != 2 || result.Metrics["remaining"] != 0 {
			t.Fatalf("replayed=%d remaining=%d, want 2 and 0", result.Metrics["replayed"], result.Metrics["remaining"])
		}
		if !strings.Contains(result.Action, "remaining=0") {
			t.Fatalf("action=%q, want remaining=0", result.Action)
		}
		if eventStub.logCalls != 2 {
			t.Fatalf("logCalls=%d, want 2", eventStub.logCalls)
		}
	})

	t.Run("dry run replays nothing and moves no files", func(t *testing.T) {
		stateDir := t.TempDir()
		t.Setenv(hookStateDirEnvKey, stateDir)
		eventStub := &spoolEventUsecaseStub{}
		root := NewRootCLI(
			WithStoreManagement(&spoolStoreManagementStub{}),
			WithEvent(eventStub),
		)
		persistPending(t, 2)
		// The clock is stuck past the wall to prove dry-run never consults it.
		hookSpoolDeadRequeueClock = func() time.Time {
			return origin.Add(hookSpoolDeadRequeueDoctorWall + time.Second)
		}
		t.Cleanup(func() { hookSpoolDeadRequeueClock = nil })

		result, err := root.fixHookSpoolRequeueThenDrain(context.Background(), time.Now().UTC(), true)
		if err != nil {
			t.Fatalf("dry-run fix: %v", err)
		}
		if !strings.Contains(result.Action, "drain up to 2 pending hook spool record(s)") {
			t.Fatalf("dry-run action=%q, want drain plan for 2 records", result.Action)
		}
		if eventStub.logCalls != 0 {
			t.Fatalf("logCalls=%d, want 0 for dry-run", eventStub.logCalls)
		}
		if remaining, err := countHookSpoolRecordPaths(); err != nil || remaining != 2 {
			t.Fatalf("pending files=%d err=%v, want 2 untouched", remaining, err)
		}
	})
}

func TestFixHookSpoolRequeueThenDrain_DoesNotResetWallForDrain(t *testing.T) {
	stateDir := t.TempDir()
	t.Setenv(hookStateDirEnvKey, stateDir)
	deadDir := filepath.Join(stateDir, "spool", hookSpoolDeadDirName)
	if err := os.MkdirAll(deadDir, 0o700); err != nil {
		t.Fatal(err)
	}
	const extra = 10
	for i := 0; i < hookSpoolDeadRequeueLimit+extra; i++ {
		writeTransientDeadLetter(t, deadDir, fmt.Sprintf("t-%04d.json", i))
	}
	eventStub := &spoolEventUsecaseStub{}
	root := NewRootCLI(
		WithStoreManagement(&spoolStoreManagementStub{}),
		WithEvent(eventStub),
	)
	origin := time.Date(2026, 8, 21, 0, 0, 0, 0, time.UTC)
	calls := 0
	hookSpoolDeadRequeueClock = func() time.Time {
		calls++
		// Call 1 takes the shared fixer deadline, call 2 starts the first
		// requeue batch, and call 3 (the second batch) is past the 45s wall.
		// The drain must see the same exhausted wall, not a fresh 45s.
		if calls >= 3 {
			return origin.Add(hookSpoolDeadRequeueDoctorWall + time.Second)
		}
		return origin
	}
	t.Cleanup(func() { hookSpoolDeadRequeueClock = nil })

	result, err := root.fixHookSpoolRequeueThenDrain(context.Background(), origin, false)
	if err != nil {
		t.Fatalf("fix: %v", err)
	}
	if result.Metrics["requeued"] != hookSpoolDeadRequeueLimit {
		t.Fatalf("requeued=%d, want first batch only (%d)", result.Metrics["requeued"], hookSpoolDeadRequeueLimit)
	}
	if result.Metrics["replayed"] != 0 || eventStub.logCalls != 0 {
		t.Fatalf("replayed=%d logCalls=%d, want no drain replay after requeue consumed the wall", result.Metrics["replayed"], eventStub.logCalls)
	}
	if result.Metrics["remaining"] != hookSpoolDeadRequeueLimit {
		t.Fatalf("remaining=%d, want %d requeued records left pending", result.Metrics["remaining"], hookSpoolDeadRequeueLimit)
	}
	if !strings.Contains(result.Action, fmt.Sprintf("remaining=%d", hookSpoolDeadRequeueLimit)) {
		t.Fatalf("action=%q, want remaining=%d", result.Action, hookSpoolDeadRequeueLimit)
	}
}

func TestApplyDoctorFixes_SkipsAfterWall(t *testing.T) {
	origin := time.Date(2026, 8, 21, 0, 0, 0, 0, time.UTC)
	warnCheck := func(name string, fix doctorStructuredFixFunc) doctorCheck {
		return doctorCheck{
			Name:              name,
			Status:            doctorStatusWarn,
			Severity:          doctorSeverityWarn,
			AutoFixAvailable:  true,
			StructuredFixFunc: fix,
		}
	}

	t.Run("apply skips later auto-fixes once the wall is exhausted", func(t *testing.T) {
		firstDone := false
		secondRan := false
		hookSpoolDeadRequeueClock = func() time.Time {
			if firstDone {
				return origin.Add(hookSpoolDeadRequeueDoctorWall + time.Second)
			}
			return origin
		}
		t.Cleanup(func() { hookSpoolDeadRequeueClock = nil })
		root := NewRootCLI(WithStoreManagement(&spoolStoreManagementStub{}))
		report := &doctorReport{Checks: []doctorCheck{
			warnCheck("hook-spool", func(context.Context, bool) (doctorFixResult, error) {
				firstDone = true
				return doctorFixResult{Action: "drained hook spool: remaining=3"}, nil
			}),
			warnCheck("transcript", func(context.Context, bool) (doctorFixResult, error) {
				secondRan = true
				return doctorFixResult{Action: "second fix ran"}, nil
			}),
		}}

		fixes := root.applyDoctorFixes(context.Background(), report, false)
		if len(fixes) != 2 {
			t.Fatalf("fixes=%v, want two", fixes)
		}
		if fixes[0].Action != "drained hook spool: remaining=3" {
			t.Fatalf("first action=%q", fixes[0].Action)
		}
		if secondRan {
			t.Fatal("second fix ran after the wall was exhausted")
		}
		if fixes[1].Action != "skip: doctor --fix wall exhausted" {
			t.Fatalf("second action=%q, want wall-exhausted skip", fixes[1].Action)
		}
	})

	t.Run("dry run never skips on the wall", func(t *testing.T) {
		secondRan := false
		// The clock is stuck past the wall; dry-run previews must still run.
		hookSpoolDeadRequeueClock = func() time.Time {
			return origin.Add(hookSpoolDeadRequeueDoctorWall + time.Second)
		}
		t.Cleanup(func() { hookSpoolDeadRequeueClock = nil })
		root := NewRootCLI(WithStoreManagement(&spoolStoreManagementStub{}))
		report := &doctorReport{Checks: []doctorCheck{
			warnCheck("hook-spool", func(context.Context, bool) (doctorFixResult, error) {
				return doctorFixResult{Action: "would drain"}, nil
			}),
			warnCheck("transcript", func(context.Context, bool) (doctorFixResult, error) {
				secondRan = true
				return doctorFixResult{Action: "would prune"}, nil
			}),
		}}

		fixes := root.applyDoctorFixes(context.Background(), report, true)
		if len(fixes) != 2 || !secondRan {
			t.Fatalf("fixes=%v secondRan=%v, want both dry-run previews", fixes, secondRan)
		}
	})
}

func TestInspectHookSpoolFilesystemMetadata_FixRequeuesAndDrains(t *testing.T) {
	writeDeadLetter := func(t *testing.T, deadDir, name, payload, lastError string) {
		t.Helper()
		record := hookSpoolRecord{
			SchemaVersion: hookSpoolSchemaVersion,
			Command:       "prompt",
			Client:        "claude",
			Payload:       payload,
			CreatedAt:     time.Now().UTC(),
			AttemptCount:  hookSpoolRetryLimit,
			LastError:     lastError,
		}
		encoded, err := json.Marshal(record)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(deadDir, name), append(encoded, '\n'), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	persistPending := func(t *testing.T, prompt string) {
		t.Helper()
		if _, err := persistHookSpoolRecord(hookSpoolRecord{
			SchemaVersion: hookSpoolSchemaVersion,
			Command:       "prompt",
			Client:        "claude",
			Payload:       fmt.Sprintf(`{"prompt":%q,"session_id":"s-fix","cwd":"/tmp"}`, prompt),
			CreatedAt:     time.Now().UTC().Add(-time.Minute),
		}); err != nil {
			t.Fatal(err)
		}
	}
	setupSpool := func(t *testing.T) (stateDir, deadDir string) {
		t.Helper()
		stateDir = t.TempDir()
		t.Setenv(hookStateDirEnvKey, stateDir)
		deadDir = filepath.Join(stateDir, "spool", hookSpoolDeadDirName)
		if err := os.MkdirAll(deadDir, 0o700); err != nil {
			t.Fatal(err)
		}
		return stateDir, deadDir
	}

	t.Run("dry-run names drain and leaves files unchanged", func(t *testing.T) {
		_, deadDir := setupSpool(t)
		writeDeadLetter(t, deadDir, "deadline.json", `{"prompt":"PRIVATE-BODY","session_id":"s-dead","cwd":"/tmp"}`, "failed to ping SQLite DB: context deadline exceeded")
		persistPending(t, "pending-prompt")

		check := (&RootCLI{}).inspectHookSpoolFilesystemMetadata()
		if !check.AutoFixAvailable || check.StructuredFixFunc == nil {
			t.Fatal("expected filesystem auto-fix")
		}
		result, err := check.StructuredFixFunc(context.Background(), true)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(result.Action, "drain up to 2 pending hook spool record(s)") {
			t.Fatalf("dry-run action=%q, want drain plan for pending+requeued", result.Action)
		}
		if strings.Contains(result.Action, "PRIVATE-BODY") {
			t.Fatalf("dry-run action leaked payload body: %q", result.Action)
		}
		if entries, err := os.ReadDir(deadDir); err != nil || len(entries) != 1 {
			t.Fatalf("dry-run must leave dead letters, n=%d err=%v", len(entries), err)
		}
		if pending, err := countHookSpoolPendingPaths(time.Now().UTC()); err != nil || pending != 1 {
			t.Fatalf("dry-run must leave pending files, pending=%d err=%v", pending, err)
		}
	})

	t.Run("apply requeues transient dead letters and drains pending", func(t *testing.T) {
		_, deadDir := setupSpool(t)
		writeDeadLetter(t, deadDir, "deadline.json", `{"prompt":"PRIVATE-BODY","session_id":"s-dead","cwd":"/tmp"}`, "failed to ping SQLite DB: context deadline exceeded")
		persistPending(t, "pending-prompt")

		eventStub := &spoolEventUsecaseStub{}
		root := NewRootCLI(
			WithStoreManagement(&spoolStoreManagementStub{}),
			WithEvent(eventStub),
		)
		check := root.inspectHookSpoolFilesystemMetadata()
		if !check.AutoFixAvailable || check.StructuredFixFunc == nil {
			t.Fatal("expected filesystem auto-fix")
		}
		result, err := check.StructuredFixFunc(context.Background(), false)
		if err != nil {
			t.Fatal(err)
		}
		if result.Metrics["requeued"] != 1 {
			t.Fatalf("requeued=%d, want 1", result.Metrics["requeued"])
		}
		if result.Metrics["replayed"] != 2 || !strings.Contains(result.Action, "replayed=2") {
			t.Fatalf("result=%+v, want replayed=2 (pending + requeued)", result)
		}
		if strings.Contains(result.Action, "PRIVATE-BODY") {
			t.Fatalf("action leaked payload body: %q", result.Action)
		}
		if _, err := os.Stat(filepath.Join(deadDir, "deadline.json")); !os.IsNotExist(err) {
			t.Fatalf("dead letter must be requeued, stat err=%v", err)
		}
		if pending, err := countHookSpoolRecordPaths(); err != nil || pending != 0 {
			t.Fatalf("drain must empty the spool, pending=%d err=%v", pending, err)
		}
		if eventStub.logCalls != 2 {
			t.Fatalf("logCalls=%d, want 2", eventStub.logCalls)
		}
	})

	t.Run("apply skips non-transient dead letters", func(t *testing.T) {
		_, deadDir := setupSpool(t)
		writeDeadLetter(t, deadDir, "poison.json", `{"prompt":"x","session_id":"s-poison","cwd":"/tmp"}`, "invalid Claude usage JSON event")

		// A single dead letter is below the WARN threshold, so drive the
		// shared fixer directly instead of via the check.
		result, err := (&RootCLI{}).fixHookSpoolRequeueThenDrain(context.Background(), time.Now().UTC(), false)
		if err != nil {
			t.Fatal(err)
		}
		if result.Metrics["skipped_nontransient"] != 1 || result.Metrics["requeued"] != 0 {
			t.Fatalf("metrics=%v, want skipped_nontransient=1 requeued=0", result.Metrics)
		}
		if _, err := os.Stat(filepath.Join(deadDir, "poison.json")); err != nil {
			t.Fatalf("non-transient dead letter must stay in dead/, stat err=%v", err)
		}
	})
}

func TestPruneHookSpoolDeadLetters_RemovesAgedFilesOnlyOnFix(t *testing.T) {
	stateDir := t.TempDir()
	t.Setenv(hookStateDirEnvKey, stateDir)
	deadDir := filepath.Join(stateDir, "spool", hookSpoolDeadDirName)
	if err := os.MkdirAll(deadDir, 0o700); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	aged := filepath.Join(deadDir, "aged.json")
	fresh := filepath.Join(deadDir, "fresh.json")
	if err := os.WriteFile(aged, []byte(`{"schema_version":1}`), 0o600); err != nil {
		t.Fatalf("WriteFile aged: %v", err)
	}
	if err := os.WriteFile(fresh, []byte(`{"schema_version":1}`), 0o600); err != nil {
		t.Fatalf("WriteFile fresh: %v", err)
	}
	agedAt := time.Now().UTC().Add(-hookSpoolDeadRetention - time.Hour)
	if err := os.Chtimes(aged, agedAt, agedAt); err != nil {
		t.Fatalf("Chtimes: %v", err)
	}

	dryPruned, _, err := pruneHookSpoolDeadLetters(time.Now().UTC(), true)
	if err != nil {
		t.Fatalf("dry-run prune: %v", err)
	}
	if dryPruned != 1 {
		t.Fatalf("dry-run pruned=%d, want 1", dryPruned)
	}
	if _, err := os.Stat(aged); err != nil {
		t.Fatalf("dry-run must not delete aged file: %v", err)
	}

	pruned, remaining, err := pruneHookSpoolDeadLetters(time.Now().UTC(), false)
	if err != nil {
		t.Fatalf("prune: %v", err)
	}
	if pruned != 1 || remaining != 1 {
		t.Fatalf("pruned=%d remaining=%d, want 1 and 1", pruned, remaining)
	}
	if _, err := os.Stat(aged); !os.IsNotExist(err) {
		t.Fatalf("aged dead-letter must be removed, stat err=%v", err)
	}
	if _, err := os.Stat(fresh); err != nil {
		t.Fatalf("fresh dead-letter must be kept: %v", err)
	}
}

func TestHookSpoolDeadOverThreshold(t *testing.T) {
	t.Parallel()
	if hookSpoolDeadOverThreshold(hookSpoolFilesystemStats{DeadCount: 1, DeadBytes: 10}) {
		t.Fatal("one small dead-letter must not warn")
	}
	if !hookSpoolDeadOverThreshold(hookSpoolFilesystemStats{DeadCount: hookSpoolDeadWarnCount}) {
		t.Fatal("count threshold must warn")
	}
	if !hookSpoolDeadOverThreshold(hookSpoolFilesystemStats{DeadBytes: hookSpoolDeadWarnBytes}) {
		t.Fatal("byte threshold must warn")
	}
}

func TestClaimHookSpoolRecord_FreshMtimeSurvivesStaleRecovery(t *testing.T) {
	stateDir := t.TempDir()
	t.Setenv(hookStateDirEnvKey, stateDir)
	path, err := persistHookSpoolRecord(hookSpoolRecord{
		SchemaVersion: hookSpoolSchemaVersion,
		Command:       "not-a-real-hook",
		Client:        "claude",
		Payload:       `{}`,
		CreatedAt:     time.Now().UTC().Add(-2 * time.Minute),
	})
	if err != nil {
		t.Fatalf("persist: %v", err)
	}
	// Simulate a long-pending record whose original mtime is already stale.
	old := time.Now().Add(-2 * hookSpoolInflightStaleAge)
	if err := os.Chtimes(path, old, old); err != nil {
		t.Fatalf("Chtimes original: %v", err)
	}

	claimedPath, ok, err := claimHookSpoolRecord(path)
	if err != nil {
		t.Fatalf("claimHookSpoolRecord: %v", err)
	}
	if !ok {
		t.Fatal("claimHookSpoolRecord ok=false, want true")
	}

	// Immediate recovery with wall clock must not restore a live claim whose
	// mtime was refreshed at claim time (rename alone would keep the old mtime).
	if err := recoverStaleClaimedHookSpoolRecords(time.Now().UTC()); err != nil {
		t.Fatalf("recover now: %v", err)
	}
	if _, err := os.Lstat(claimedPath); err != nil {
		t.Fatalf("claimed path must remain after recover(now): %v", err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("original .json must not be restored while claim is live, stat err=%v", err)
	}

	// Far-future recovery may release the claim once it is truly stale.
	future := time.Now().UTC().Add(2 * hookSpoolInflightStaleAge)
	if err := recoverStaleClaimedHookSpoolRecords(future); err != nil {
		t.Fatalf("recover future: %v", err)
	}
	if _, err := os.Lstat(claimedPath); !os.IsNotExist(err) {
		t.Fatalf("stale claim must be released, Lstat err=%v", err)
	}
	// Restored either under the original basename or a unique retry name.
	pending, err := listHookSpoolRecordPaths()
	if err != nil {
		t.Fatalf("listHookSpoolRecordPaths: %v", err)
	}
	if len(pending) != 1 {
		t.Fatalf("pending after stale release = %v, want exactly 1", pending)
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

func writeHookSpoolTmp(t *testing.T, spoolDir, name, body string, age time.Duration) string {
	t.Helper()
	if err := os.MkdirAll(spoolDir, 0o700); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	path := filepath.Join(spoolDir, name)
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("WriteFile %s: %v", name, err)
	}
	if age > 0 {
		at := time.Now().Add(-age)
		if err := os.Chtimes(path, at, at); err != nil {
			t.Fatalf("Chtimes %s: %v", name, err)
		}
	}
	return path
}

type disappearedSpoolEntry struct{ name string }

func (e disappearedSpoolEntry) Name() string               { return e.name }
func (e disappearedSpoolEntry) IsDir() bool                { return false }
func (e disappearedSpoolEntry) Type() os.FileMode          { return 0 }
func (e disappearedSpoolEntry) Info() (os.FileInfo, error) { return nil, os.ErrNotExist }

func TestInspectHookSpoolFilesystemStats_CountsAgedTmpAsStaleInflight(t *testing.T) {
	stateDir := t.TempDir()
	t.Setenv(hookStateDirEnvKey, stateDir)
	spoolDir := filepath.Join(stateDir, "spool")
	fresh := writeHookSpoolTmp(t, spoolDir, "fresh.json.tmp", "fresh-tmp\n", 0)
	aged := writeHookSpoolTmp(t, spoolDir, "aged.json.tmp", "aged-tmp-body\n", hookSpoolTmpStaleAge+time.Minute)

	stats, err := inspectHookSpoolFilesystemStats(time.Now().UTC())
	if err != nil {
		t.Fatalf("inspectHookSpoolFilesystemStats: %v", err)
	}
	if stats.StaleInflightCount != 1 || stats.PendingCount != 0 {
		t.Fatalf("stats=%+v, want stale_inflight=1 pending=0", stats)
	}
	if stats.StaleInflightBytes != int64(len("aged-tmp-body\n")) {
		t.Fatalf("stale bytes=%d, want aged tmp size", stats.StaleInflightBytes)
	}

	check := (&RootCLI{}).inspectHookSpoolFilesystemMetadata()
	if check.Status != doctorStatusWarn {
		t.Fatalf("status=%q, want warn", check.Status)
	}
	if !strings.Contains(check.Message, "stale_inflight=1") {
		t.Fatalf("message=%q, want stale_inflight=1", check.Message)
	}
	if _, err := os.Stat(fresh); err != nil {
		t.Fatalf("fresh tmp must remain: %v", err)
	}
	if _, err := os.Stat(aged); err != nil {
		t.Fatalf("aged-but-under-retention tmp must remain until 14d prune: %v", err)
	}
}

func TestPruneHookSpoolOrphanTmpFiles_RemovesAgedFilesOnlyOnFix(t *testing.T) {
	stateDir := t.TempDir()
	t.Setenv(hookStateDirEnvKey, stateDir)
	spoolDir := filepath.Join(stateDir, "spool")
	fresh := writeHookSpoolTmp(t, spoolDir, "fresh.json.tmp", "fresh\n", time.Second)
	stale := writeHookSpoolTmp(t, spoolDir, "hour-old.json.tmp", "hour\n", hookSpoolTmpStaleAge+time.Minute)
	prunable := writeHookSpoolTmp(t, spoolDir, "july.json.tmp", "july-orphan\n", hookSpoolDeadRetention+24*time.Hour)

	dryPruned, _, err := pruneHookSpoolOrphanTmpFiles(time.Now().UTC(), true)
	if err != nil {
		t.Fatalf("dry-run prune: %v", err)
	}
	if dryPruned != 1 {
		t.Fatalf("dry-run pruned=%d, want 1", dryPruned)
	}
	for _, path := range []string{fresh, stale, prunable} {
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("dry-run must not delete %s: %v", path, err)
		}
	}

	pruned, remaining, err := pruneHookSpoolOrphanTmpFiles(time.Now().UTC(), false)
	if err != nil {
		t.Fatalf("prune: %v", err)
	}
	if pruned != 1 || remaining != 0 {
		t.Fatalf("pruned=%d remaining=%d, want 1 and 0", pruned, remaining)
	}
	if _, err := os.Stat(prunable); !os.IsNotExist(err) {
		t.Fatalf("14d tmp must be removed, stat err=%v", err)
	}
	if _, err := os.Stat(fresh); err != nil {
		t.Fatalf("fresh tmp must be kept: %v", err)
	}
	if _, err := os.Stat(stale); err != nil {
		t.Fatalf("1h tmp must be kept until 14d: %v", err)
	}

	check := (&RootCLI{}).inspectHookSpoolFilesystemMetadata()
	if !check.AutoFixAvailable || check.StructuredFixFunc == nil {
		t.Fatal("stale tmp must expose doctor --fix")
	}
	result, err := check.StructuredFixFunc(context.Background(), false)
	if err != nil {
		t.Fatalf("StructuredFixFunc: %v", err)
	}
	if result.Metrics["pruned_tmp"] != 0 {
		t.Fatalf("second fix pruned_tmp=%d, want 0", result.Metrics["pruned_tmp"])
	}
}

func TestFixHookSpoolRequeueThenDrain_PrunesRetentionAgedTmp(t *testing.T) {
	stateDir := t.TempDir()
	t.Setenv(hookStateDirEnvKey, stateDir)
	spoolDir := filepath.Join(stateDir, "spool")
	july := writeHookSpoolTmp(t, spoolDir, "20260716T000000.000000000Z-grok.json.tmp", "grok-orphan\n", hookSpoolDeadRetention+time.Hour)
	july2 := writeHookSpoolTmp(t, spoolDir, "20260722T000000.000000000Z-kimi.json.tmp", "kimi-orphan\n", hookSpoolDeadRetention+2*time.Hour)
	fresh := writeHookSpoolTmp(t, spoolDir, "now.json.tmp", "now\n", 0)

	stats, err := inspectHookSpoolFilesystemStats(time.Now().UTC())
	if err != nil {
		t.Fatalf("inspectHookSpoolFilesystemStats: %v", err)
	}
	if stats.StaleInflightCount != 2 {
		t.Fatalf("stats=%+v, want stale_inflight=2", stats)
	}

	result, err := (&RootCLI{}).fixHookSpoolRequeueThenDrain(context.Background(), time.Now().UTC(), true)
	if err != nil {
		t.Fatalf("dry-run fix: %v", err)
	}
	if !strings.Contains(result.Action, "orphan tmp") {
		t.Fatalf("dry-run action=%q, want orphan tmp preview", result.Action)
	}
	if _, err := os.Stat(july); err != nil {
		t.Fatalf("dry-run must keep july tmp: %v", err)
	}

	result, err = (&RootCLI{}).fixHookSpoolRequeueThenDrain(context.Background(), time.Now().UTC(), false)
	if err != nil {
		t.Fatalf("fix: %v", err)
	}
	if result.Metrics["pruned_tmp"] != 2 {
		t.Fatalf("metrics=%v, want pruned_tmp=2", result.Metrics)
	}
	if !strings.Contains(result.Action, "pruned_tmp=2") {
		t.Fatalf("action=%q, want pruned_tmp=2", result.Action)
	}
	if _, err := os.Stat(july); !os.IsNotExist(err) {
		t.Fatalf("july tmp must be removed, stat err=%v", err)
	}
	if _, err := os.Stat(july2); !os.IsNotExist(err) {
		t.Fatalf("july2 tmp must be removed, stat err=%v", err)
	}
	if _, err := os.Stat(fresh); err != nil {
		t.Fatalf("fresh tmp must remain: %v", err)
	}
}

func TestAddHookSpoolFilesystemEntry_ToleratesDisappearedTmp(t *testing.T) {
	t.Parallel()
	var stats hookSpoolFilesystemStats
	if err := addHookSpoolFilesystemEntry(&stats, t.TempDir(), disappearedSpoolEntry{name: "gone.json.tmp"}, time.Now().UTC()); err != nil {
		t.Fatalf("disappeared tmp must be skipped: %v", err)
	}
	if stats != (hookSpoolFilesystemStats{}) {
		t.Fatalf("stats=%+v, want zero", stats)
	}
	if err := addHookSpoolFilesystemEntry(&stats, t.TempDir(), disappearedSpoolEntry{name: "gone.json"}, time.Now().UTC()); err == nil {
		t.Fatal("disappeared non-tmp must fail")
	}
}
