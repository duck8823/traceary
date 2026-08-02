package sqlite

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/duck8823/traceary/domain"
)

func TestCompactionFileJournalRoundTripAndTransitionValidation(t *testing.T) {
	j := &CompactionFileJournal{Dir: t.TempDir()}
	run := domain.CompactionRun{ID: "0123456789abcdef0123456789abcdef", Phase: domain.CompactionPlanned, CreatedAt: time.Now(), UpdatedAt: time.Now()}
	if err := j.Create(context.Background(), run); err != nil {
		t.Fatal(err)
	}
	run.Phase = domain.CompactionCandidateVerified
	if err := j.Append(context.Background(), run); err != nil {
		t.Fatal(err)
	}
	if _, err := j.Load(context.Background(), run.ID); err == nil {
		t.Fatal("Load accepted a skipped transition")
	}
}

func TestStoreReplacementFilesRejectsSymlinkAndSidecar(t *testing.T) {
	dir := t.TempDir()
	source := filepath.Join(dir, "store.db")
	if err := os.WriteFile(source, []byte("source"), 0o600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "link.db")
	if err := os.Symlink(source, link); err != nil {
		t.Fatal(err)
	}
	run := domain.CompactionRun{SourcePath: link, CandidatePath: filepath.Join(dir, "candidate"), RollbackPath: filepath.Join(dir, "rollback")}
	if _, err := (StoreReplacementFiles{}).Plan(context.Background(), run); err == nil {
		t.Fatal("Plan accepted symlink")
	}
	if err := os.WriteFile(source+"-wal", nil, 0o600); err != nil {
		t.Fatal(err)
	}
	run.SourcePath = source
	if _, err := (StoreReplacementFiles{}).Plan(context.Background(), run); err == nil {
		t.Fatal("Plan accepted WAL sidecar")
	}
}

func TestAtomicExchangePreservesBothInodes(t *testing.T) {
	dir := t.TempDir()
	left, right := filepath.Join(dir, "left"), filepath.Join(dir, "right")
	if err := os.WriteFile(left, []byte("old"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(right, []byte("new"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := atomicExchange(left, right); err != nil {
		t.Skipf("atomic exchange unavailable: %v", err)
	}
	gotLeft, _ := os.ReadFile(left)
	gotRight, _ := os.ReadFile(right)
	if string(gotLeft) != "new" || string(gotRight) != "old" {
		t.Fatalf("exchange = %q/%q", gotLeft, gotRight)
	}
}
