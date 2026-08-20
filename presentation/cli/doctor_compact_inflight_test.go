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

func TestInspectCompactInFlightJournals_AbandonedLeftoverIsFixable(t *testing.T) {
	root := t.TempDir()
	dbPath := filepath.Join(root, "traceary.db")
	if err := os.WriteFile(dbPath, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	leftover := dbPath + ".compact-deadbeefdeadbeefdeadbeefdeadbeef-journal"
	if err := os.WriteFile(leftover, []byte("sqlite-journal"), 0o600); err != nil {
		t.Fatal(err)
	}
	work := filepath.Join(root, "traceary.db.compact-deadbeefdeadbeefdeadbeefdeadbeef.work-journal")
	if err := os.WriteFile(work, []byte("work"), 0o600); err != nil {
		t.Fatal(err)
	}
	check := inspectCompactInFlightJournals(dbPath, time.Now().UTC())
	if check.Status != doctorStatusWarn || !check.AutoFixAvailable || check.StructuredFixFunc == nil {
		t.Fatalf("check=%#v, want warn with leftover autofix", check)
	}
	if !strings.Contains(check.Message, leftover) {
		t.Fatalf("message=%q, want leftover path", check.Message)
	}
	if _, err := check.StructuredFixFunc(t.Context(), true); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(leftover); err != nil {
		t.Fatalf("dry-run removed leftover: %v", err)
	}
	if _, err := check.StructuredFixFunc(t.Context(), false); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(leftover); !os.IsNotExist(err) {
		t.Fatalf("leftover still present: %v", err)
	}
	if _, err := os.Lstat(work); !os.IsNotExist(err) {
		t.Fatalf("work-journal still present: %v", err)
	}
	after := inspectCompactInFlightJournals(dbPath, time.Now().UTC())
	if after.Status != doctorStatusPass {
		t.Fatalf("after=%#v, want pass", after)
	}
}

func TestInspectCompactInFlightJournals_DoesNotDeleteInFlightWork(t *testing.T) {
	root := t.TempDir()
	dbPath := filepath.Join(root, "traceary.db")
	dir := filepath.Join(root, compactJournalDirName)
	if err := os.Mkdir(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	run := domain.CompactionRun{
		ID:        "dddddddddddddddddddddddddddddddd",
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
	work := dbPath + ".compact-" + run.ID + "-journal"
	if err := os.WriteFile(work, []byte("live"), 0o600); err != nil {
		t.Fatal(err)
	}
	check := inspectCompactInFlightJournals(dbPath, time.Now().UTC())
	if check.AutoFixAvailable {
		t.Fatalf("in-flight swap_intent leftover must not be auto-fixed: %+v", check)
	}
	if _, err := os.Lstat(work); err != nil {
		t.Fatalf("in-flight work missing: %v", err)
	}
}
