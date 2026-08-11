package sqlite

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/duck8823/traceary/application/usecase"
	"github.com/duck8823/traceary/domain"
	"github.com/google/go-cmp/cmp"
)

func TestCompactionFileJournalRoundTripAndTransitionValidation(t *testing.T) {
	j := &CompactionFileJournal{Dir: filepath.Join(t.TempDir(), "journal")}
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

func TestPreparedStoreUpgradeJournalRejectsSymlinkWithoutMutatingTarget(t *testing.T) {
	root := t.TempDir()
	victim := filepath.Join(root, "victim")
	if err := os.Mkdir(victim, 0o755); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(root, "journal")
	if err := os.Symlink(victim, link); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	now := time.Now()
	run := domain.CompactionRun{ID: "0123456789abcdef0123456789abcdef", SourcePath: "/tmp/source", CandidatePath: "/tmp/candidate", RollbackPath: "/tmp/rollback", SourceIdentity: domain.StoreFileIdentity{Device: 1, Inode: 2, Size: 3}, Resources: domain.CompactionResourcePlan{RequiredBytes: 4, DestinationBytes: 3, FilesystemDevice: 1, LeaseCapability: true, ExchangeCapability: true}, Phase: domain.CompactionPlanned, CreatedAt: now, UpdatedAt: now}
	if err := (&PreparedStoreUpgradeFileJournal{Dir: link}).Create(context.Background(), run); err == nil {
		t.Fatal("Create accepted a symlink journal directory")
	}
	info, err := os.Stat(victim)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o755 {
		t.Fatalf("victim mode mutated to %o", info.Mode().Perm())
	}
}

func TestCompactionFileJournalLoadsLegacyCopyRetryGolden(t *testing.T) {
	dir := t.TempDir()
	if err := os.Chmod(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile("testdata/compaction-copy-retry-v1.jsonl")
	if err != nil {
		t.Fatal(err)
	}
	id := "0123456789abcdef0123456789abcdef"
	if err = os.WriteFile(filepath.Join(dir, id+".jsonl"), data, 0o600); err != nil {
		t.Fatal(err)
	}
	run, err := (&CompactionFileJournal{Dir: dir}).Load(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	if run.Phase != domain.CompactionCandidatePrepared || run.PreparedAttempt != 2 || run.PreparedCandidateIdentity.Inode != 4 || run.Operation != "" {
		t.Fatalf("legacy retry run = %+v", run)
	}
}

func TestPreparedStoreUpgradeJournalFindActiveRejectsAmbiguity(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "journal")
	journal := &PreparedStoreUpgradeFileJournal{Dir: dir}
	now := time.Now().UTC()
	first := preparedJournalRun("11111111111111111111111111111111", now)
	if err := journal.Create(context.Background(), first); err != nil {
		t.Fatal(err)
	}
	got, err := journal.FindActive(context.Background(), first.Operation, first.SourcePath, first.ConsumerBinding)
	if err != nil || got.ID != first.ID {
		t.Fatalf("FindActive = %q, %v", got.ID, err)
	}
	second := preparedJournalRun("22222222222222222222222222222222", now)
	if err = journal.Create(context.Background(), second); err != nil {
		t.Fatal(err)
	}
	if _, err = journal.FindActive(context.Background(), first.Operation, first.SourcePath, first.ConsumerBinding); err == nil {
		t.Fatal("FindActive selected one of two ambiguous active runs")
	}
}

func TestPreparedStoreUpgradeJournalFindActiveRejectsSymlinkEntry(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "journal")
	journal := &PreparedStoreUpgradeFileJournal{Dir: dir}
	run := preparedJournalRun("33333333333333333333333333333333", time.Now().UTC())
	if err := journal.Create(context.Background(), run); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(dir, run.ID+".jsonl"), filepath.Join(dir, "44444444444444444444444444444444.jsonl")); err != nil {
		t.Fatal(err)
	}
	if _, err := journal.FindActive(context.Background(), run.Operation, run.SourcePath, run.ConsumerBinding); err == nil {
		t.Fatal("FindActive accepted a symlink journal entry")
	}
}

func preparedJournalRun(id string, now time.Time) domain.PreparedStoreUpgradeRun {
	return domain.PreparedStoreUpgradeRun{
		ID:              id,
		SourcePath:      "/tmp/rehearsal.db",
		CandidatePath:   "/tmp/rehearsal.db.prepare-" + id,
		RollbackPath:    "/tmp/rehearsal.db.rollback-" + id,
		Phase:           domain.PreparedStoreUpgradePlanned,
		SourceIdentity:  domain.StoreFileIdentity{Device: 1, Inode: 2, Size: 4096},
		Resources:       domain.PreparedStoreUpgradeResourcePlan{RequiredBytes: 8192, DestinationBytes: 4096, FilesystemDevice: 1, LeaseCapability: true, ExchangeCapability: true},
		Operation:       domain.PreparedStoreUpgradeOperationPayloadRehearsalMigration,
		ConsumerBinding: "binding",
		PlanDigest:      strings.Repeat("a", 64),
		SourceDigest:    strings.Repeat("b", 64),
		Budget:          domain.PreparedStoreUpgradeBudget{WallTimeLimit: time.Minute, PublishLockLimit: time.Second, OwnedDiskByteLimit: 1 << 20, WALByteLimit: 1 << 19, TemporaryByteLimit: 1 << 19},
		CreatedAt:       now,
		UpdatedAt:       now,
	}
}

func TestCompactionFileJournalLoadRejectsPersistedIntermediateTamper(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "journal")
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
	dir := filepath.Join(t.TempDir(), "journal")
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
	if _, err := (StoreReplacementFiles{}).Plan(context.Background(), run); err != nil && strings.Contains(err.Error(), "sidecar") {
		t.Fatalf("Plan rejected stale zero-byte WAL: %v", err)
	}
	if _, err := os.Lstat(source + "-wal"); !os.IsNotExist(err) {
		t.Fatalf("stale WAL was not removed: %v", err)
	}
}

func TestStoreReplacementFilesRecheckCleansSidecarCreatedAfterPlan(t *testing.T) {
	dir := t.TempDir()
	source := filepath.Join(dir, "store.db")
	if err := os.WriteFile(source, []byte("source"), 0o600); err != nil {
		t.Fatal(err)
	}
	run, err := (StoreReplacementFiles{}).Plan(context.Background(), domain.CompactionRun{
		SourcePath:    source,
		CandidatePath: filepath.Join(dir, "candidate"),
		RollbackPath:  filepath.Join(dir, "rollback"),
	})
	if err != nil {
		if errors.Is(err, ErrPreparedUpgradeUnsupported) {
			t.Skipf("compaction replacement unsupported: %v", err)
		}
		t.Fatal(err)
	}
	if err := os.WriteFile(source+"-wal", nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := (StoreReplacementFiles{}).Recheck(context.Background(), run); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(source + "-wal"); !os.IsNotExist(err) {
		t.Fatalf("sidecar created after plan was not removed: %v", err)
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
	run.PreparedCandidateIdentity, err = inspectRegularFile(candidate)
	if err != nil {
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

func TestInsufficientCompactionSpaceErrorStatesOperatorCosts(t *testing.T) {
	tests := []struct {
		name      string
		required  uint64
		available uint64
		source    int64
		want      string
	}{
		{
			name:      "reports shortfall and retained rollback",
			required:  1100,
			available: 700,
			source:    1000,
			want: `insufficient free space: free 400 more bytes to proceed (need 1100, have 700)
	most of that is a worst-case reservation: VACUUM INTO cannot report the compacted size until it has written it, so the requirement assumes the result could be as large as the 1000-byte source. It is usually far smaller, but reserving less risks a half-written candidate on a full disk
	the space is not all returned when the run succeeds: a rollback copy of the source is kept as <db>.rollback-<run id>, and nothing removes it. Deleting it is your decision, and gives up "store compact rollback" for that run`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := insufficientCompactionSpaceError(tt.required, tt.available, tt.source).Error()
			if diff := cmp.Diff(tt.want, got); diff != "" {
				t.Errorf("insufficient space error (-want +got):\n%s", diff)
			}
		})
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

func TestCompactionDestructiveBoundariesRejectHardLinkedArtifacts(t *testing.T) {
	for _, target := range []string{"source-before-exchange", "candidate-before-fence", "candidate-before-publish", "rollback-before-exchange"} {
		t.Run(target, func(t *testing.T) {
			dir := t.TempDir()
			source, candidate, rollback := filepath.Join(dir, "source"), filepath.Join(dir, "candidate"), filepath.Join(dir, "rollback")
			for path, body := range map[string]string{source: "source", candidate: "candidate", rollback: "rollback"} {
				if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			sourceID, _ := inspectRegularFile(source)
			candidateID, _ := inspectRegularFile(candidate)
			run := domain.CompactionRun{SourcePath: source, CandidatePath: candidate, RollbackPath: rollback, SourceIdentity: sourceID, Candidate: candidateID, PreparedCandidateIdentity: candidateID}
			var linked, actionPath string
			switch target {
			case "source-before-exchange":
				linked = source
				run.Phase = domain.CompactionSwapIntent
				actionPath = "exchange"
			case "candidate-before-fence":
				linked = candidate
				run.Phase = domain.CompactionScrubInProgress
				actionPath = "fence"
			case "candidate-before-publish":
				linked = candidate
				run.Phase = domain.CompactionRollbackPublishIntent
				actionPath = "publish"
				_ = os.Remove(rollback)
			case "rollback-before-exchange":
				linked = rollback
				run.Phase = domain.CompactionRollbackSwapIntent
				run.SourceIdentity, run.Candidate = mustIDs(t, rollback, source)
				actionPath = "exchange"
			}
			if err := os.Link(linked, linked+".alias"); err != nil {
				t.Fatal(err)
			}
			files := StoreReplacementFiles{}
			var err error
			switch actionPath {
			case "fence":
				_, err = files.FenceCandidate(context.Background(), run)
			case "publish":
				err = files.PublishRollback(context.Background(), run)
			default:
				err = files.Exchange(context.Background(), run)
			}
			if err == nil {
				t.Fatal("hard-linked artifact crossed destructive boundary")
			}
		})
	}
}

func TestRecoveredExchangeDurabilityFaultsDoNotAdvanceIntent(t *testing.T) {
	points := []string{"before_recovery_file_sync_0", "after_recovery_file_sync_0", "before_recovery_file_sync_1", "after_recovery_file_sync_1", "before_recovery_dir_sync", "after_recovery_dir_sync"}
	for _, point := range points {
		t.Run(point, func(t *testing.T) {
			dir := t.TempDir()
			source, candidate := filepath.Join(dir, "source"), filepath.Join(dir, "candidate")
			_ = os.WriteFile(source, []byte("compacted"), 0o600)
			_ = os.WriteFile(candidate, []byte("original"), 0o600)
			candidateID, _ := inspectRegularFile(source)
			sourceID, _ := inspectRegularFile(candidate)
			run := domain.CompactionRun{ID: "0123456789abcdef0123456789abcdef", SourcePath: source, CandidatePath: candidate, RollbackPath: filepath.Join(dir, "rollback"), SourceIdentity: sourceID, Candidate: candidateID, PreparedCandidateIdentity: candidateID, Phase: domain.CompactionSwapIntent}
			journal := &observationTrackingJournal{run: run}
			files := StoreReplacementFiles{recoveryHook: func(got string) error {
				if got == point {
					return errors.New("stop")
				}
				return nil
			}}
			service := usecase.NewStoreCompactionUsecase(source, journal, SQLiteCompactionBuilder{}, files, StoreLeaseCoordinator{})
			if _, err := service.Resume(context.Background(), run.ID); err == nil {
				t.Fatal("fault not returned")
			}
			if journal.run.Phase != domain.CompactionSwapIntent {
				t.Fatalf("journal advanced to %s", journal.run.Phase)
			}
		})
	}
}

func mustIDs(t *testing.T, first, second string) (domain.StoreFileIdentity, domain.StoreFileIdentity) {
	t.Helper()
	a, err := inspectRegularFile(first)
	if err != nil {
		t.Fatal(err)
	}
	b, err := inspectRegularFile(second)
	if err != nil {
		t.Fatal(err)
	}
	return a, b
}
