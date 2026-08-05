package types_test

import (
	"testing"
	"time"

	apptypes "github.com/duck8823/traceary/application/types"
)

func TestArchiveSequenceBudgetEnforcesHardMaxima(t *testing.T) {
	t.Parallel()
	valid := apptypes.ArchiveSequenceBudget{Rows: apptypes.ArchiveSequenceMaxRows, StoredBytes: apptypes.ArchiveSequenceMaxStoredBytes, WriteBytes: apptypes.ArchiveSequenceMaxWriteBytes, WallTime: apptypes.ArchiveSequenceMaxWallTime, LockTime: apptypes.ArchiveSequenceMaxLockTime}
	if !valid.Valid() {
		t.Fatal("maximum budget rejected")
	}
	tests := []apptypes.ArchiveSequenceBudget{valid, valid, valid, valid, valid}
	tests[0].Rows++
	tests[1].StoredBytes++
	tests[2].WriteBytes++
	tests[3].WallTime += time.Nanosecond
	tests[4].LockTime += time.Nanosecond
	for index, budget := range tests {
		if budget.Valid() {
			t.Fatalf("over-limit budget %d accepted", index)
		}
	}
}
