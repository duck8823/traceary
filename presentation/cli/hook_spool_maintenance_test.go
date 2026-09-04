package cli

import (
	"bytes"
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	apptypes "github.com/duck8823/traceary/application/types"
	"github.com/duck8823/traceary/application/usecase"
	"github.com/duck8823/traceary/domain/types"
	"golang.org/x/xerrors"
)

func TestDeferredHookEmitsSuccessAndSkipsDrain(t *testing.T) {
	stateDir := t.TempDir()
	t.Setenv(hookStateDirEnvKey, stateDir)
	var drainEntries atomic.Int32
	hookSpoolDrainEntryProbe = func() { drainEntries.Add(1) }
	t.Cleanup(func() { hookSpoolDrainEntryProbe = nil })

	c := &RootCLI{}
	wrapped := xerrors.Errorf("failed to ping SQLite DB: %w", &apptypes.StoreMaintenancePendingError{StorePath: "/tmp/store.db"})
	err := c.runHookDurably(context.Background(), "prompt", hookInvocationSpec{
		Command: "prompt",
		Client:  "codex",
		DBPath:  "/tmp/store.db",
	}, strings.NewReader(`{"prompt":"hello"}`), func(io.Reader) error {
		return wrapped
	})
	if err != nil {
		t.Fatalf("runHookDurably error = %v, want nil host success", err)
	}
	if drainEntries.Load() != 0 {
		t.Fatalf("drain-entry probe = %d, want 0", drainEntries.Load())
	}
	pending, err := countHookSpoolPendingPaths(time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	if pending != 1 {
		t.Fatalf("pending spool records = %d, want 1", pending)
	}
}

func TestHookDefersAtMarkerDeterministicRace(t *testing.T) {
	stateDir := t.TempDir()
	t.Setenv(hookStateDirEnvKey, stateDir)
	var drainEntries atomic.Int32
	hookSpoolDrainEntryProbe = func() { drainEntries.Add(1) }
	t.Cleanup(func() { hookSpoolDrainEntryProbe = nil })

	c := &RootCLI{}
	err := c.runHookDurably(context.Background(), "session", hookInvocationSpec{
		Command: "session",
		Client:  "claude",
	}, strings.NewReader(`{"session_id":"s"}`), func(io.Reader) error {
		return &apptypes.StoreMaintenancePendingError{StorePath: "store.db"}
	})
	if err != nil {
		t.Fatalf("host success want nil, got %v", err)
	}
	if drainEntries.Load() != 0 {
		t.Fatalf("drain-entry probe = %d, want 0", drainEntries.Load())
	}
	pending, err := countHookSpoolPendingPaths(time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	if pending != 1 {
		t.Fatalf("pending spool records = %d, want 1", pending)
	}
}

func TestRunHookPromptWithConsolidationSkipsSinkWhenWriteDeferred(t *testing.T) {
	stateDir := t.TempDir()
	t.Setenv(hookStateDirEnvKey, stateDir)
	var drainEntries atomic.Int32
	hookSpoolDrainEntryProbe = func() { drainEntries.Add(1) }
	t.Cleanup(func() { hookSpoolDrainEntryProbe = nil })

	var pressureCalls atomic.Int32
	root := NewRootCLI(
		WithStoreManagement(&maintenancePendingStoreStub{err: &apptypes.StoreMaintenancePendingError{StorePath: "store.db"}}),
		WithEvent(&spoolEventUsecaseStub{}),
		WithConsolidationPressure(&countingConsolidationPressure{calls: &pressureCalls}),
	)
	stdout := &bytes.Buffer{}
	payload := `{"prompt":"hello","session_id":"sess-1","cwd":"/tmp"}`
	err := root.runHookPromptWithConsolidation(context.Background(), stdout, "codex", strings.NewReader(payload), filepath.Join(t.TempDir(), "store.db"))
	if err != nil {
		t.Fatalf("prompt command error = %v, want host success", err)
	}
	if drainEntries.Load() != 0 {
		t.Fatalf("drain-entry probe = %d, want 0", drainEntries.Load())
	}
	pending, err := countHookSpoolPendingPaths(time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	if pending != 1 {
		t.Fatalf("pending spool records = %d, want 1", pending)
	}
	if pressureCalls.Load() != 0 {
		t.Fatalf("consolidation pressure calls = %d, want 0 after deferral", pressureCalls.Load())
	}
}

func TestNoCommittedOrDeferredDispositionTypeExists(t *testing.T) {
	body, err := os.ReadFile("hook_spool.go")
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(body, []byte("DispositionCommitted")) || bytes.Contains(body, []byte("type hookDisposition")) {
		t.Fatal("must not introduce a committed/deferred disposition type")
	}
}

type maintenancePendingStoreStub struct {
	spoolStoreManagementStub
	err error
}

func (s *maintenancePendingStoreStub) Initialize(context.Context) error { return s.err }

type countingConsolidationPressure struct {
	calls *atomic.Int32
}

func (c *countingConsolidationPressure) Check(context.Context, types.SessionID, usecase.ConsolidationPolicy) (usecase.ConsolidationPressureResult, error) {
	c.calls.Add(1)
	return usecase.ConsolidationPressureResult{}, nil
}
