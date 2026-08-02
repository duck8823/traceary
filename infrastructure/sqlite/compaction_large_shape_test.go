package sqlite

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/duck8823/traceary/domain"
)

func TestCompactionPlan_21Point4GiBShape(t *testing.T) {
	if os.Getenv("TRACEARY_RUN_21GB_COMPACTION_SHAPE") != "1" {
		t.Skip("set TRACEARY_RUN_21GB_COMPACTION_SHAPE=1 for the large-shape harness")
	}
	dir := t.TempDir()
	source := filepath.Join(dir, "store.db")
	f, err := os.OpenFile(source, os.O_CREATE|os.O_RDWR|os.O_EXCL, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	const bytes = int64(21_400_000_000)
	if err := f.Truncate(bytes); err != nil {
		_ = f.Close()
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	run := domain.CompactionRun{SourcePath: source, CandidatePath: source + ".candidate", RollbackPath: source + ".rollback"}
	planned, err := (StoreReplacementFiles{}).Plan(context.Background(), run)
	if err != nil {
		t.Fatal(err)
	}
	if planned.SourceIdentity.Size != bytes {
		t.Fatalf("planned size=%d want %d", planned.SourceIdentity.Size, bytes)
	}
}
