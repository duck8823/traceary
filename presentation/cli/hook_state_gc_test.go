package cli

import (
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"
)

func TestGCHookStateResiduesRemovesStalePIDAndAgedMarkers(t *testing.T) {
	stateDir := t.TempDir()
	t.Setenv(hookStateDirEnvKey, stateDir)
	now := time.Now().UTC()

	stalePID := filepath.Join(stateDir, "codex-999999")
	staleRepo := filepath.Join(stateDir, "codex-999999-repo")
	livePID := filepath.Join(stateDir, "codex-"+strconv.Itoa(os.Getpid()))
	if err := os.WriteFile(stalePID, []byte("sess"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(staleRepo, []byte("ws"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(livePID, []byte("live"), 0o600); err != nil {
		t.Fatal(err)
	}
	old := now.Add(-hookStatePIDAgeFloor - time.Hour)
	if err := os.Chtimes(stalePID, old, old); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(staleRepo, old, old); err != nil {
		t.Fatal(err)
	}

	diagDir := filepath.Join(stateDir, "diagnostics")
	endedDir := filepath.Join(stateDir, "ended")
	if err := os.MkdirAll(diagDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(endedDir, 0o755); err != nil {
		t.Fatal(err)
	}
	agedDiag := filepath.Join(diagDir, "old.json")
	freshDiag := filepath.Join(diagDir, "fresh.json")
	agedEnded := filepath.Join(endedDir, "old-ended")
	if err := os.WriteFile(agedDiag, []byte(`{"status":"started"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(freshDiag, []byte(`{"status":"started"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(agedEnded, []byte("1"), 0o600); err != nil {
		t.Fatal(err)
	}
	agedAt := now.Add(-hookStateResidueRetention - time.Hour)
	if err := os.Chtimes(agedDiag, agedAt, agedAt); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(agedEnded, agedAt, agedAt); err != nil {
		t.Fatal(err)
	}

	check := inspectHookStateResidueMetadata(now)
	if check.Status != doctorStatusWarn {
		t.Fatalf("status=%q, want warn", check.Status)
	}

	result, err := gcHookStateResidues(now, hookStateGCDoctorBudget, false)
	if err != nil {
		t.Fatalf("gc: %v", err)
	}
	if result.Removed < 4 {
		t.Fatalf("removed=%d, want at least stale pid pair + aged diag + aged ended", result.Removed)
	}
	if _, err := os.Stat(stalePID); !os.IsNotExist(err) {
		t.Fatal("stale per-PID state must be removed")
	}
	if _, err := os.Stat(staleRepo); !os.IsNotExist(err) {
		t.Fatal("stale per-PID repo state must be removed")
	}
	if _, err := os.Stat(livePID); err != nil {
		t.Fatalf("live pid state must remain: %v", err)
	}
	if _, err := os.Stat(freshDiag); err != nil {
		t.Fatalf("fresh diagnostic must remain: %v", err)
	}
	if _, err := os.Stat(agedDiag); !os.IsNotExist(err) {
		t.Fatal("aged diagnostic must be removed")
	}
	if _, err := os.Stat(agedEnded); !os.IsNotExist(err) {
		t.Fatal("aged ended marker must be removed")
	}
}
