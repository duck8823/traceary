package cli_test

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	apptypes "github.com/duck8823/traceary/application/types"
	"github.com/duck8823/traceary/domain/types"
	"github.com/duck8823/traceary/presentation/cli"
)

func TestRootCLI_SessionRepairOneShot_DefaultsToExplainableDryRun(t *testing.T) {
	t.Parallel()
	manifest := writeOneShotRepairManifest(t, false)
	repairStub := &oneShotRepairUsecaseStub{result: apptypes.OneShotRepairResult{
		EvidenceHash: strings.Repeat("a", 64),
		Before:       apptypes.OneShotRepairStats{ActiveCount: 2, StaleCount: 1},
		After:        apptypes.OneShotRepairStats{ActiveCount: 2, StaleCount: 1},
		Candidates: []apptypes.OneShotRepairCandidate{{
			SessionID: "session-1", StoredRuntimeMode: types.RuntimeModeInteractive, ProposedReason: types.TerminalReasonSuccess,
			CompletedAt: time.Date(2026, 7, 21, 10, 0, 0, 0, time.UTC), LatestActivityAt: time.Date(2026, 7, 20, 10, 0, 0, 0, time.UTC),
			EvidenceSource: apptypes.OneShotRepairEvidenceOperatorAttested, EvidenceRef: "run:42", Eligible: true, Decision: "eligible",
		}},
	}}
	storeStub := &storeManagementUsecaseStub{}
	stdout := &bytes.Buffer{}
	root := cli.NewRootCLI(cli.WithStoreManagement(storeStub), cli.WithOneShotRepair(repairStub)).Command()
	root.SetOut(stdout)
	root.SetErr(&bytes.Buffer{})
	root.SetArgs([]string{"session", "repair-one-shot", "--db-path", filepath.Join(t.TempDir(), "traceary.db"), "--evidence-file", manifest})

	if err := root.Execute(); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if repairStub.previewCalls != 1 || repairStub.applyCalls != 0 || storeStub.createBackupCalls != 0 || storeStub.initCalled {
		t.Fatalf("preview/apply/backup = %d/%d/%d", repairStub.previewCalls, repairStub.applyCalls, storeStub.createBackupCalls)
	}
	for _, want := range []string{"one-shot repair: dry-run", "stored_mode=interactive", "evidence_source=operator_attested_process_exit", "decision=eligible"} {
		if !strings.Contains(stdout.String(), want) {
			t.Fatalf("stdout missing %q:\n%s", want, stdout.String())
		}
	}
}

func TestRootCLI_SessionRepairOneShot_ApplyRequiresBackupAndDelegatesSafety(t *testing.T) {
	t.Parallel()
	manifest := writeOneShotRepairManifest(t, false)
	t.Run("requires backup", func(t *testing.T) {
		repairStub := &oneShotRepairUsecaseStub{}
		root := cli.NewRootCLI(cli.WithStoreManagement(&storeManagementUsecaseStub{}), cli.WithOneShotRepair(repairStub)).Command()
		root.SetOut(&bytes.Buffer{})
		root.SetErr(&bytes.Buffer{})
		root.SetArgs([]string{"session", "repair-one-shot", "--evidence-file", manifest, "--apply"})
		if err := root.Execute(); err == nil || !strings.Contains(err.Error(), "--backup") {
			t.Fatalf("Execute() error = %v, want backup requirement", err)
		}
		if repairStub.calls != 0 {
			t.Fatalf("repair calls = %d, want 0", repairStub.calls)
		}
	})

	t.Run("backs up before apply", func(t *testing.T) {
		storeStub := &storeManagementUsecaseStub{}
		repairStub := &oneShotRepairUsecaseStub{result: apptypes.OneShotRepairResult{ApplyMode: true}}
		backupPath := filepath.Join(t.TempDir(), "before.db")
		root := cli.NewRootCLI(cli.WithStoreManagement(storeStub), cli.WithOneShotRepair(repairStub)).Command()
		root.SetOut(&bytes.Buffer{})
		root.SetErr(&bytes.Buffer{})
		root.SetArgs([]string{"session", "repair-one-shot", "--db-path", filepath.Join(t.TempDir(), "traceary.db"), "--evidence-file", manifest, "--apply", "--backup", backupPath})
		if err := root.Execute(); err != nil {
			t.Fatalf("Execute() error = %v", err)
		}
		if storeStub.createBackupCalls != 0 || storeStub.initCalled || repairStub.previewCalls != 0 || repairStub.applyCalls != 1 || repairStub.applyParams.BackupPath != backupPath {
			t.Fatalf("backup/apply mismatch: store=%+v repair=%+v", storeStub, repairStub)
		}
	})

	t.Run("application safety failure is returned", func(t *testing.T) {
		storeStub := &storeManagementUsecaseStub{}
		repairStub := &oneShotRepairUsecaseStub{err: errors.New("backup failed")}
		root := cli.NewRootCLI(cli.WithStoreManagement(storeStub), cli.WithOneShotRepair(repairStub)).Command()
		root.SetOut(&bytes.Buffer{})
		root.SetErr(&bytes.Buffer{})
		root.SetArgs([]string{"session", "repair-one-shot", "--evidence-file", manifest, "--apply", "--backup", filepath.Join(t.TempDir(), "before.db")})
		if err := root.Execute(); err == nil {
			t.Fatal("Execute() error = nil, want backup error")
		}
		if repairStub.applyCalls != 1 {
			t.Fatalf("apply calls = %d, want 1", repairStub.applyCalls)
		}
		if storeStub.createBackupCalls != 0 || storeStub.initCalled {
			t.Fatal("presentation bypassed application safety orchestration")
		}
	})
}

func TestRootCLI_SessionRepairOneShot_RejectsUnknownManifestFields(t *testing.T) {
	t.Parallel()
	manifest := writeOneShotRepairManifest(t, true)
	repairStub := &oneShotRepairUsecaseStub{}
	root := cli.NewRootCLI(cli.WithStoreManagement(&storeManagementUsecaseStub{}), cli.WithOneShotRepair(repairStub)).Command()
	root.SetOut(&bytes.Buffer{})
	root.SetErr(&bytes.Buffer{})
	root.SetArgs([]string{"session", "repair-one-shot", "--evidence-file", manifest})
	if err := root.Execute(); err == nil {
		t.Fatal("Execute() error = nil, want strict manifest error")
	}
	if repairStub.calls != 0 {
		t.Fatalf("repair calls = %d, want 0", repairStub.calls)
	}
}

func TestRootCLI_SessionRepairOneShot_RejectsControlCharactersBeforeOutput(t *testing.T) {
	t.Parallel()
	content := `{"schema_version":"one-shot-repair-evidence/v1","entries":[{"session_id":"session-\u001b[31m","runtime_mode":"one_shot","terminal_reason":"success","completed_at":"2026-07-21T10:00:00Z","evidence_source":"operator_attested_process_exit","evidence_ref":"run:42"}]}`
	path := filepath.Join(t.TempDir(), "evidence.json")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	repairStub := &oneShotRepairUsecaseStub{}
	root := cli.NewRootCLI(cli.WithStoreManagement(&storeManagementUsecaseStub{}), cli.WithOneShotRepair(repairStub)).Command()
	root.SetOut(&bytes.Buffer{})
	root.SetErr(&bytes.Buffer{})
	root.SetArgs([]string{"session", "repair-one-shot", "--evidence-file", path})
	if err := root.Execute(); err == nil || !strings.Contains(err.Error(), "control characters") {
		t.Fatalf("Execute() error = %v, want control-character rejection", err)
	}
	if repairStub.calls != 0 {
		t.Fatalf("repair calls = %d, want 0", repairStub.calls)
	}
}

func writeOneShotRepairManifest(t *testing.T, unknownField bool) string {
	t.Helper()
	extra := ""
	if unknownField {
		extra = `,"guess_from_transcript":true`
	}
	content := `{"schema_version":"one-shot-repair-evidence/v1","entries":[{"session_id":"session-1","runtime_mode":"one_shot","terminal_reason":"success","completed_at":"2026-07-21T10:00:00Z","evidence_source":"operator_attested_process_exit","evidence_ref":"run:42"` + extra + `}]}`
	path := filepath.Join(t.TempDir(), "evidence.json")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}
