package sqlite

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/duck8823/traceary/domain"
)

func TestCompactionFileJournalRoundTripAndTransitionValidation(t *testing.T) {
	j := &CompactionFileJournal{Dir: t.TempDir()}
	now := time.Now()
	run := domain.CompactionRun{ID: "0123456789abcdef0123456789abcdef", SourcePath: "/tmp/source", CandidatePath: "/tmp/candidate", RollbackPath: "/tmp/rollback", SourceIdentity: domain.StoreFileIdentity{Device: 1, Inode: 2, Size: 3}, Resources: domain.CompactionResourcePlan{RequiredBytes: 4, DestinationBytes: 3, FilesystemDevice: 1, LeaseCapability: true, ExchangeCapability: true}, Phase: domain.CompactionPlanned, CreatedAt: now, UpdatedAt: now}
	if err := j.Create(context.Background(), run); err != nil {
		t.Fatal(err)
	}
	run.Phase = domain.CompactionCandidateVerified
	if err := j.Append(context.Background(), run); err == nil {
		t.Fatal("Append accepted a skipped transition")
	}
}

func TestCompactionFileJournalLoadRejectsPersistedIntermediateTamper(t *testing.T) {
	dir := t.TempDir()
	j := &CompactionFileJournal{Dir: dir}
	now := time.Now()
	run := domain.CompactionRun{ID: "11111111111111111111111111111111", SourcePath: "/tmp/source", CandidatePath: "/tmp/candidate", RollbackPath: "/tmp/rollback", SourceIdentity: domain.StoreFileIdentity{Device: 1, Inode: 2, Size: 3}, Resources: domain.CompactionResourcePlan{RequiredBytes: 4, DestinationBytes: 3, FilesystemDevice: 1, ExchangeCapability: true}, Phase: domain.CompactionPlanned, CreatedAt: now, UpdatedAt: now}
	if err := j.Create(context.Background(), run); err != nil {
		t.Fatal(err)
	}
	run, _ = run.Advance(domain.CompactionCopyIntent, now.Add(time.Second))
	if err := j.Append(context.Background(), run); err != nil {
		t.Fatal(err)
	}
	run.PreparedCandidateIdentity = domain.StoreFileIdentity{Device: 1, Inode: 9}
	run.PreparedAttempt = 1
	run, _ = run.Advance(domain.CompactionCandidatePrepared, now.Add(2*time.Second))
	if err := j.Append(context.Background(), run); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, run.ID+".jsonl")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(string(data), "\n")
	lines[1] = strings.Replace(lines[1], "/tmp/source", "/tmp/tampered", 1)
	if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := j.Load(context.Background(), run.ID); err == nil {
		t.Fatal("Load accepted persisted intermediate tamper")
	}
}

func TestCompactionFileJournalRejectsTruncationAndImmutableTamper(t *testing.T) {
	dir := t.TempDir()
	j := &CompactionFileJournal{Dir: dir}
	now := time.Now()
	run := domain.CompactionRun{ID: "abcdef0123456789abcdef0123456789", SourcePath: "/a", CandidatePath: "/b", RollbackPath: "/c", SourceIdentity: domain.StoreFileIdentity{Device: 1, Inode: 2, Size: 3}, Resources: domain.CompactionResourcePlan{RequiredBytes: 4, DestinationBytes: 3, FilesystemDevice: 1, LeaseCapability: true, ExchangeCapability: true}, Phase: domain.CompactionPlanned, CreatedAt: now, UpdatedAt: now}
	if err := j.Create(context.Background(), run); err != nil {
		t.Fatal(err)
	}
	tampered := run
	tampered.SourcePath = "/other"
	tampered.Phase = domain.CompactionCopyIntent
	tampered.UpdatedAt = now.Add(time.Second)
	if err := j.Append(context.Background(), tampered); err == nil {
		t.Fatal("Append accepted immutable path tamper")
	}
	path := filepath.Join(dir, run.ID+".jsonl")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data[:len(data)-1], 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := j.Load(context.Background(), run.ID); err == nil {
		t.Fatal("Load accepted truncated final record")
	}
}

func TestValidateRunAppendCandidateIdentityIsWriteOnce(t *testing.T) {
	now := time.Now()
	previous := domain.CompactionRun{ID: "id", SourcePath: "s", CandidatePath: "c", RollbackPath: "r", SourceIdentity: domain.StoreFileIdentity{Device: 1, Inode: 1}, Candidate: domain.StoreFileIdentity{Device: 1, Inode: 2}, Phase: domain.CompactionCandidateVerified, CreatedAt: now, UpdatedAt: now}
	next := previous
	next.Phase = domain.CompactionSwapIntent
	next.UpdatedAt = now.Add(time.Second)
	next.Candidate.Inode = 3
	if err := validateRunAppend(previous, next); err == nil {
		t.Fatal("accepted candidate identity mutation")
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

func TestStoreReplacementFilesRejectsCandidateReplacementAfterVerification(t *testing.T) {
	dir := t.TempDir()
	source := filepath.Join(dir, "store.db")
	candidate := filepath.Join(dir, "candidate")
	rollback := filepath.Join(dir, "rollback")
	if err := os.WriteFile(source, []byte("source"), 0o600); err != nil {
		t.Fatal(err)
	}
	run, err := (StoreReplacementFiles{}).Plan(context.Background(), domain.CompactionRun{SourcePath: source, CandidatePath: candidate, RollbackPath: rollback})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(candidate, []byte("candidate"), 0o600); err != nil {
		t.Fatal(err)
	}
	run, err = (StoreReplacementFiles{}).FenceCandidate(context.Background(), run)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(candidate); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(candidate, []byte("replacement"), 0o600); err != nil {
		t.Fatal(err)
	}
	run.Phase = domain.CompactionSwapIntent
	if err := (StoreReplacementFiles{}).Exchange(context.Background(), run); err == nil {
		t.Fatal("Exchange accepted replaced candidate")
	}
	got, _ := os.ReadFile(source)
	if string(got) != "source" {
		t.Fatal("source changed after rejected exchange")
	}
}

func TestStoreReplacementFilesObserveRejectsFencedCandidateReadyConflict(t *testing.T) {
	dir := t.TempDir()
	source := filepath.Join(dir, "source")
	candidate := filepath.Join(dir, "candidate")
	if err := os.WriteFile(source, []byte("s"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(candidate, []byte("c"), 0o600); err != nil {
		t.Fatal(err)
	}
	sourceID, _ := inspectRegularFile(source)
	candidateID, _ := inspectRegularFile(candidate)
	run := domain.CompactionRun{SourcePath: source, CandidatePath: candidate, RollbackPath: filepath.Join(dir, "rollback"), SourceIdentity: sourceID, Candidate: candidateID}
	run.Candidate.Inode++
	if _, err := (StoreReplacementFiles{}).Observe(context.Background(), run); err == nil {
		t.Fatal("Observe accepted candidate identity conflict")
	}
}

func TestCompactionRequiredBytesIsOverflowSafe(t *testing.T) {
	if _, _, err := compactionRequiredBytes(-1, 0); err == nil {
		t.Fatal("accepted negative size")
	}
	required, margin, err := compactionRequiredBytes(100, 0)
	if err != nil {
		t.Fatal(err)
	}
	if required != uint64(100)+(64<<20) || margin != 64<<20 {
		t.Fatalf("required=%d margin=%d", required, margin)
	}
}

func TestRemoveOwnedPartialCandidateFaultBoundaries(t *testing.T) {
	for _, point := range []string{"before_unlink", "after_unlink", "after_dir_sync"} {
		t.Run(point, func(t *testing.T) {
			dir := t.TempDir()
			source := filepath.Join(dir, "store.db")
			id := "0123456789abcdef0123456789abcdef"
			candidate := source + ".compact-" + id
			if err := os.WriteFile(source, []byte("source"), 0o600); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(candidate, make([]byte, 1024*1024), 0o600); err != nil {
				t.Fatal(err)
			}
			sourceID, _ := inspectRegularFile(source)
			candidateID, _ := inspectRegularFile(candidate)
			run := domain.CompactionRun{ID: id, SourcePath: source, CandidatePath: candidate, RollbackPath: source + ".rollback-" + id, SourceIdentity: sourceID, PreparedCandidateIdentity: candidateID, PreparedAttempt: 1, Phase: domain.CompactionCandidatePrepared}
			obs := domain.CompactionObservation{Orientation: domain.OrientationCandidateReady, Source: sourceID, Candidate: candidateID, CandidateExists: true, CandidateCondition: domain.CandidateConditionOwnedIncomplete}
			files := StoreReplacementFiles{recoveryHook: func(got string) error {
				if got == point {
					return errors.New("stop")
				}
				return nil
			}}
			if err := files.RemoveOwnedPartialCandidate(context.Background(), run, obs); err == nil {
				t.Fatal("fault was not returned")
			}
			_, statErr := os.Lstat(candidate)
			if point == "before_unlink" && statErr != nil {
				t.Fatal("candidate removed before authorized unlink")
			}
			if point != "before_unlink" && !os.IsNotExist(statErr) {
				t.Fatal("candidate still exists after unlink boundary")
			}
			gotSource, _ := os.ReadFile(source)
			if string(gotSource) != "source" {
				t.Fatal("source mutated")
			}
		})
	}
}

func TestPrepareCandidateFaultBoundaries(t *testing.T) {
	for _, point := range []string{"before_create", "after_create", "after_file_sync", "after_create_dir_sync"} {
		t.Run(point, func(t *testing.T) {
			dir := t.TempDir()
			source := filepath.Join(dir, "store")
			if err := os.WriteFile(source, []byte("source"), 0o600); err != nil {
				t.Fatal(err)
			}
			sourceID, _ := inspectRegularFile(source)
			id := "abcdef0123456789abcdef0123456789"
			run := domain.CompactionRun{ID: id, SourcePath: source, CandidatePath: source + ".compact-" + id, SourceIdentity: sourceID, Phase: domain.CompactionCopyIntent}
			files := StoreReplacementFiles{recoveryHook: func(got string) error {
				if got == point {
					return errors.New("stop")
				}
				return nil
			}}
			if _, err := files.PrepareCandidate(context.Background(), run); err == nil {
				t.Fatal("fault not returned")
			}
			info, statErr := os.Lstat(run.CandidatePath)
			if point == "before_create" {
				if !os.IsNotExist(statErr) {
					t.Fatal("candidate created before create boundary")
				}
			} else {
				if statErr != nil || info.Size() != 0 {
					t.Fatal("prepared empty candidate not retained")
				}
			}
		})
	}
}
