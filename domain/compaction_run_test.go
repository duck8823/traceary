package domain

import (
	"testing"
	"time"
)

func TestCompactionRunAdvanceRejectsSkippedTransition(t *testing.T) {
	run := CompactionRun{Phase: CompactionPlanned}
	if _, err := run.Advance(CompactionCandidateVerified, time.Now()); err == nil {
		t.Fatal("Advance() accepted skipped transition")
	}
	got, err := run.Advance(CompactionCopyIntent, time.Now())
	if err != nil || got.Phase != CompactionCopyIntent {
		t.Fatalf("Advance() = %q, %v", got.Phase, err)
	}
}
