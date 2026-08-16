package cli

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
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

func TestGCHookStateResiduesRemovesEmptyAgedDiagnostic(t *testing.T) {
	t.Setenv("TRACEARY_LANG", "en")
	stateDir := t.TempDir()
	t.Setenv(hookStateDirEnvKey, stateDir)
	now := time.Now().UTC()
	diagDir := filepath.Join(stateDir, "diagnostics")
	if err := os.MkdirAll(diagDir, 0o755); err != nil {
		t.Fatal(err)
	}
	emptyAged := filepath.Join(diagDir, "claude-SessionEnd-empty.json")
	if err := os.WriteFile(emptyAged, []byte{}, 0o600); err != nil {
		t.Fatal(err)
	}
	agedAt := now.Add(-hookStateResidueRetention - time.Hour)
	if err := os.Chtimes(emptyAged, agedAt, agedAt); err != nil {
		t.Fatal(err)
	}

	check := inspectHookStateResidueMetadata(now)
	if check.Status != doctorStatusWarn {
		t.Fatalf("status=%q, want warn", check.Status)
	}
	if !strings.Contains(check.Message, "aged_diagnostics=1") || !strings.Contains(check.Message, "aged_ended=0") {
		t.Fatalf("message=%q, want aged population labels", check.Message)
	}

	result, err := gcHookStateResidues(now, hookStateGCDoctorBudget, false)
	if err != nil {
		t.Fatalf("gc: %v", err)
	}
	if result.Removed != 1 {
		t.Fatalf("removed=%d, want 1", result.Removed)
	}
	if _, err := os.Stat(emptyAged); !os.IsNotExist(err) {
		t.Fatal("empty aged diagnostic must be GC-eligible residue")
	}
}

func TestGCHookStateResiduesUntilDrainsMultipleBatchesBeforeDeadline(t *testing.T) {
	stateDir := t.TempDir()
	t.Setenv(hookStateDirEnvKey, stateDir)
	now := time.Now().UTC()
	writeStaleHookPIDFiles(t, stateDir, now, 5)

	deadline := now.Add(time.Hour)
	result, err := gcHookStateResiduesUntil(now, deadline, 2, 0, false, func() time.Time { return now })
	if err != nil {
		t.Fatalf("until: %v", err)
	}
	if result.Removed != 5 || result.Remaining != 0 {
		t.Fatalf("removed=%d remaining=%d, want 5/0", result.Removed, result.Remaining)
	}
}

func TestGCHookStateResiduesUntilStopsWhenDeadlineHasPassed(t *testing.T) {
	stateDir := t.TempDir()
	t.Setenv(hookStateDirEnvKey, stateDir)
	now := time.Now().UTC()
	writeStaleHookPIDFiles(t, stateDir, now, 5)

	deadline := now.Add(-time.Second)
	result, err := gcHookStateResiduesUntil(now, deadline, 2, 0, false, func() time.Time { return now })
	if err != nil {
		t.Fatalf("until: %v", err)
	}
	if result.Removed != 0 || result.Remaining != 5 {
		t.Fatalf("removed=%d remaining=%d, want 0/5", result.Removed, result.Remaining)
	}
}

func TestPrioritizeHookStateResidueCandidatesReservesDiagnostics(t *testing.T) {
	stateDir := t.TempDir()
	t.Setenv(hookStateDirEnvKey, stateDir)
	now := time.Now().UTC()
	pids := writeStaleHookPIDFiles(t, stateDir, now, 3)

	diagDir := filepath.Join(stateDir, "diagnostics")
	if err := os.MkdirAll(diagDir, 0o755); err != nil {
		t.Fatal(err)
	}
	agedDiag := filepath.Join(diagDir, "old.json")
	if err := os.WriteFile(agedDiag, []byte(`{}`), 0o600); err != nil {
		t.Fatal(err)
	}
	agedAt := now.Add(-hookStateResidueRetention - time.Hour)
	if err := os.Chtimes(agedDiag, agedAt, agedAt); err != nil {
		t.Fatal(err)
	}

	result, err := gcHookStateResiduesWithReserve(now, 2, 1, false)
	if err != nil {
		t.Fatalf("gc: %v", err)
	}
	if result.Removed != 2 || result.Remaining != 2 {
		t.Fatalf("removed=%d remaining=%d, want 2/2", result.Removed, result.Remaining)
	}
	if _, err := os.Stat(agedDiag); !os.IsNotExist(err) {
		t.Fatal("reserved diagnostic must be removed in the first batch")
	}
	removedPID := 0
	for _, path := range pids {
		if _, err := os.Stat(path); os.IsNotExist(err) {
			removedPID++
		}
	}
	if removedPID != 1 {
		t.Fatalf("removed PID files=%d, want 1", removedPID)
	}
}

func writeStaleHookPIDFiles(t *testing.T, stateDir string, now time.Time, n int) []string {
	t.Helper()
	old := now.Add(-hookStatePIDAgeFloor - time.Hour)
	paths := make([]string, 0, n)
	for i := 0; i < n; i++ {
		path := filepath.Join(stateDir, "codex-99999"+strconv.Itoa(i))
		if err := os.WriteFile(path, []byte("sess"), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := os.Chtimes(path, old, old); err != nil {
			t.Fatal(err)
		}
		paths = append(paths, path)
	}
	return paths
}
