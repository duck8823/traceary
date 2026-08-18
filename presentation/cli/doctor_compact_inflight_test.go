package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/duck8823/traceary/domain"
)

func TestInspectCompactInFlightJournals_WarnsOnCandidatePrepared(t *testing.T) {
	root := t.TempDir()
	dbPath := filepath.Join(root, "traceary.db")
	if err := os.WriteFile(dbPath, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(root, compactJournalDirName)
	if err := os.Mkdir(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	updated := time.Now().UTC().Add(-2 * time.Hour)
	run := domain.CompactionRun{
		ID:        "cc172b5a0123456789abcdef01234567",
		Phase:     domain.CompactionCandidatePrepared,
		UpdatedAt: updated,
	}
	encoded, err := json.Marshal(run)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, run.ID+".jsonl"), append(encoded, '\n'), 0o600); err != nil {
		t.Fatal(err)
	}
	check := inspectCompactInFlightJournals(dbPath, time.Now().UTC())
	if check.Status != doctorStatusWarn {
		t.Fatalf("status=%q, want warn", check.Status)
	}
	if !strings.Contains(check.Message, run.ID) || !strings.Contains(check.Message, "candidate_prepared") {
		t.Fatalf("message=%q", check.Message)
	}
}

func TestInspectCompactInFlightJournals_IgnoresAbandonedAndCommitted(t *testing.T) {
	root := t.TempDir()
	dbPath := filepath.Join(root, "traceary.db")
	if err := os.WriteFile(dbPath, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(root, compactJournalDirName)
	if err := os.Mkdir(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	for _, run := range []domain.CompactionRun{
		{ID: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", Phase: domain.CompactionAbandoned, UpdatedAt: time.Now()},
		{ID: "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb", Phase: domain.CompactionCommitted, UpdatedAt: time.Now()},
	} {
		encoded, err := json.Marshal(run)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, run.ID+".jsonl"), append(encoded, '\n'), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	check := inspectCompactInFlightJournals(dbPath, time.Now().UTC())
	if check.Status != doctorStatusPass {
		t.Fatalf("status=%q message=%q, want pass", check.Status, check.Message)
	}
}

func TestInspectCompactInFlightJournals_SwapIntentWarnsWithoutAutoFix(t *testing.T) {
	root := t.TempDir()
	dbPath := filepath.Join(root, "traceary.db")
	dir := filepath.Join(root, compactJournalDirName)
	if err := os.Mkdir(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	run := domain.CompactionRun{
		ID:        "cccccccccccccccccccccccccccccccc",
		Phase:     domain.CompactionSwapIntent,
		UpdatedAt: time.Now().UTC(),
	}
	encoded, err := json.Marshal(run)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, run.ID+".jsonl"), append(encoded, '\n'), 0o600); err != nil {
		t.Fatal(err)
	}
	check := inspectCompactInFlightJournals(dbPath, time.Now().UTC())
	if check.Status != doctorStatusWarn || check.AutoFixAvailable {
		t.Fatalf("check=%#v, want warn without autofix", check)
	}
}
