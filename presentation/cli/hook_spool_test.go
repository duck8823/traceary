package cli

import (
	"context"
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

	check := inspectHookSpoolDiagnostics([]string{"claude"})
	if check.Status != doctorStatusWarn || !strings.Contains(check.Message, "1 pending") {
		t.Fatalf("doctor check = %#v", check)
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
