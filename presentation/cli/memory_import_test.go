package cli_test

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
	"time"

	apptypes "github.com/duck8823/traceary/application/types"
	domtypes "github.com/duck8823/traceary/domain/types"
	"github.com/duck8823/traceary/presentation/cli"
)

func buildMemoryImportStubDetails(t *testing.T, fact string) apptypes.MemoryDetails {
	t.Helper()
	workspace, err := domtypes.WorkspaceFrom("github.com/example/repo")
	if err != nil {
		t.Fatalf("WorkspaceFrom: %v", err)
	}
	summary, err := apptypes.MemorySummaryOf(
		domtypes.MemoryID("memory-import-cli"),
		domtypes.MemoryTypePreference,
		domtypes.WorkspaceScopeOf(workspace),
		fact,
		domtypes.MemoryStatusCandidate,
		domtypes.ConfidenceMedium,
		domtypes.MemorySourceImported,
		domtypes.None[domtypes.MemoryID](),
		domtypes.None[time.Time](),
		time.Date(2026, 4, 19, 0, 0, 0, 0, time.UTC),
		domtypes.None[time.Time](),
		time.Date(2026, 4, 19, 0, 0, 0, 0, time.UTC),
		time.Date(2026, 4, 19, 0, 0, 0, 0, time.UTC),
	)
	if err != nil {
		t.Fatalf("MemorySummaryOf: %v", err)
	}
	return apptypes.MemoryDetailsOf(summary, nil, nil)
}

func TestMemoryImportCodex_TextOutput(t *testing.T) {
	t.Parallel()

	importStub := &memoryUsecaseStub{
		importResult: apptypes.MemoryImportResult{
			Imported:              []apptypes.MemoryDetails{buildMemoryImportStubDetails(t, "prefer bulleted commits")},
			SkippedDuplicateCount: 2,
			SkippedRejectedCount:  1,
			Warnings:              []string{"bullet at line 42 skipped: unsupported section"},
		},
	}
	root := cli.NewRootCLI(
		cli.WithStoreManagement(&storeManagementUsecaseStub{}),
		cli.WithMemory(importStub),
	)
	cmd := root.Command()
	stdout := &bytes.Buffer{}
	stderr := &bytes.Buffer{}
	cmd.SetOut(stdout)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"memory", "import", "codex", "--db-path", t.TempDir() + "/traceary.db", "--root", "/tmp/codex-memories"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}

	out := stdout.String()
	if !strings.Contains(out, "imported=1 duplicates=2 rejected_blocked=1") {
		t.Fatalf("expected summary line in stdout, got %q", out)
	}
	if !strings.Contains(out, "prefer bulleted commits") {
		t.Fatalf("expected imported fact in stdout, got %q", out)
	}
	if !strings.Contains(stderr.String(), "bullet at line 42") {
		t.Fatalf("expected warning in stderr, got %q", stderr.String())
	}
	if len(importStub.importCalls) != 1 {
		t.Fatalf("expected 1 ImportCodex call, got %d", len(importStub.importCalls))
	}
	if importStub.importCalls[0].Root != "/tmp/codex-memories" {
		t.Fatalf("root = %q, want /tmp/codex-memories", importStub.importCalls[0].Root)
	}
}

func TestMemoryImportCodex_JSONOutput(t *testing.T) {
	t.Parallel()

	importStub := &memoryUsecaseStub{
		importResult: apptypes.MemoryImportResult{
			Imported:              []apptypes.MemoryDetails{buildMemoryImportStubDetails(t, "always update docs")},
			SkippedDuplicateCount: 0,
			SkippedRejectedCount:  0,
		},
	}
	root := cli.NewRootCLI(
		cli.WithStoreManagement(&storeManagementUsecaseStub{}),
		cli.WithMemory(importStub),
	)
	cmd := root.Command()
	stdout := &bytes.Buffer{}
	cmd.SetOut(stdout)
	cmd.SetErr(&bytes.Buffer{})
	cmd.SetArgs([]string{"memory", "import", "codex", "--db-path", t.TempDir() + "/traceary.db", "--root", "/tmp/codex-memories", "--json"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}

	var payload struct {
		Imported []struct {
			Summary struct {
				MemoryID string `json:"memory_id"`
				Fact     string `json:"fact"`
			} `json:"summary"`
		} `json:"imported"`
		SkippedDuplicateCount int `json:"skipped_duplicate_count"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &payload); err != nil {
		t.Fatalf("json.Unmarshal: %v (out=%q)", err, stdout.String())
	}
	if len(payload.Imported) != 1 || payload.Imported[0].Summary.Fact != "always update docs" {
		t.Fatalf("unexpected json payload: %+v", payload)
	}
}

func TestMemoryImportCodex_ErrorsWhenUsecaseMissing(t *testing.T) {
	t.Parallel()

	root := cli.NewRootCLI(
		cli.WithStoreManagement(&storeManagementUsecaseStub{}),
	)
	cmd := root.Command()
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(&bytes.Buffer{})
	cmd.SetArgs([]string{"memory", "import", "codex", "--db-path", t.TempDir() + "/traceary.db", "--root", t.TempDir()})
	if err := cmd.Execute(); err == nil {
		t.Fatalf("expected error when memory import usecase is missing")
	}
}
