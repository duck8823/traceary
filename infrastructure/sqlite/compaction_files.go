//nolint:wrapcheck,errcheck,revive // Protocol methods deliberately preserve syscall failure identity.
package sqlite

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/duck8823/traceary/domain"
)

const (
	maxCompactionJournalBytes = 1 << 20
	maxActiveUpgradeJournals  = 1024
)

// ErrPreparedUpgradeUnsupported is the stable fail-closed classification used
// when the platform cannot prove atomic exchange/lease/identity semantics.
var ErrPreparedUpgradeUnsupported = errors.New("prepared store upgrade is unsupported on this platform")

// PreparedStoreUpgradeFileJournal is the shared append-only, capped protocol journal.
type PreparedStoreUpgradeFileJournal struct{ Dir string }

// CompactionFileJournal preserves the legacy constructor and JSONL namespace.
type CompactionFileJournal = PreparedStoreUpgradeFileJournal

func (j *PreparedStoreUpgradeFileJournal) path(id string) (string, error) {
	if len(id) != 32 || strings.Trim(id, "0123456789abcdef") != "" {
		return "", fmt.Errorf("invalid compaction run id")
	}
	return filepath.Join(j.Dir, id+".jsonl"), nil
}

func (j *PreparedStoreUpgradeFileJournal) Create(ctx context.Context, run domain.CompactionRun) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	path, err := j.path(run.ID)
	if err != nil {
		return err
	}
	if err := validateInitialRun(run); err != nil {
		return err
	}
	if _, err := os.Lstat(j.Dir); os.IsNotExist(err) {
		if err = os.Mkdir(j.Dir, 0o700); err != nil {
			return err
		}
	} else if err != nil {
		return err
	}
	if err := validateJournalDirectory(j.Dir); err != nil {
		return err
	}
	f, err := openFileNoFollow(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return fmt.Errorf("create compaction journal: %w", err)
	}
	if err := f.Chmod(0o600); err != nil {
		_ = f.Close()
		return err
	}
	createdInfo, err := f.Stat()
	if err != nil || !createdInfo.Mode().IsRegular() || createdInfo.Mode().Perm() != 0o600 || fileLinkCount(createdInfo) != 1 {
		_ = f.Close()
		return errors.New("invalid created compaction journal")
	}
	if err := writeJournalRecord(f, run); err != nil {
		_ = f.Close()
		return err
	}
	if err := f.Sync(); err != nil {
		_ = f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	return syncDirectory(j.Dir)
}

func (j *PreparedStoreUpgradeFileJournal) Append(ctx context.Context, run domain.CompactionRun) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	path, err := j.path(run.ID)
	if err != nil {
		return err
	}
	if err := validateJournalDirectory(j.Dir); err != nil {
		return err
	}
	previous, err := j.Load(ctx, run.ID)
	if err != nil {
		return fmt.Errorf("load journal before append: %w", err)
	}
	if err := validateRunAppend(previous, run); err != nil {
		return err
	}
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 || info.Mode().Perm() != 0o600 || fileLinkCount(info) != 1 {
		return errors.New("compaction journal is not a regular file")
	}
	record, err := json.Marshal(run)
	if err != nil {
		return err
	}
	if info.Size()+int64(len(record))+1 > maxCompactionJournalBytes {
		return errors.New("compaction journal size limit exceeded")
	}
	f, err := openFileNoFollow(path, os.O_WRONLY|os.O_APPEND, 0)
	if err != nil {
		return err
	}
	opened, err := f.Stat()
	if err != nil || !os.SameFile(info, opened) || opened.Mode().Perm() != 0o600 || fileLinkCount(opened) != 1 {
		_ = f.Close()
		return errors.New("compaction journal identity changed")
	}
	if _, err := f.Write(append(record, '\n')); err != nil {
		_ = f.Close()
		return err
	}
	if err := f.Sync(); err != nil {
		_ = f.Close()
		return err
	}
	return f.Close()
}

func (j *PreparedStoreUpgradeFileJournal) Load(ctx context.Context, id string) (domain.CompactionRun, error) {
	if err := ctx.Err(); err != nil {
		return domain.CompactionRun{}, err
	}
	path, err := j.path(id)
	if err != nil {
		return domain.CompactionRun{}, err
	}
	if err := validateJournalDirectory(j.Dir); err != nil {
		return domain.CompactionRun{}, err
	}
	f, err := openFileNoFollow(path, os.O_RDONLY, 0)
	if err != nil {
		return domain.CompactionRun{}, err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return domain.CompactionRun{}, err
	}
	if !info.Mode().IsRegular() || info.Mode().Perm() != 0o600 || fileLinkCount(info) != 1 {
		return domain.CompactionRun{}, errors.New("invalid compaction journal permissions or links")
	}
	if info.Size() <= 0 || info.Size() > maxCompactionJournalBytes {
		return domain.CompactionRun{}, errors.New("invalid compaction journal size")
	}
	lastByte := []byte{0}
	if _, err := f.ReadAt(lastByte, info.Size()-1); err != nil {
		return domain.CompactionRun{}, err
	}
	if lastByte[0] != '\n' {
		return domain.CompactionRun{}, errors.New("truncated compaction journal record")
	}
	limited := io.LimitReader(f, maxCompactionJournalBytes+1)
	s := bufio.NewScanner(limited)
	s.Buffer(make([]byte, 4096), maxCompactionJournalBytes)
	var last domain.CompactionRun
	first := true
	for s.Scan() {
		var next domain.CompactionRun
		dec := json.NewDecoder(strings.NewReader(s.Text()))
		dec.DisallowUnknownFields()
		if err := dec.Decode(&next); err != nil {
			return last, fmt.Errorf("decode compaction journal: %w", err)
		}
		var extra any
		if err := dec.Decode(&extra); err != io.EOF {
			return last, errors.New("trailing compaction journal data")
		}
		if next.ID != id {
			return last, errors.New("compaction journal run id mismatch")
		}
		if first {
			if err := validateInitialRun(next); err != nil {
				return last, fmt.Errorf("invalid initial journal record: %w", err)
			}
			first = false
		} else {
			if err := validateRunAppend(last, next); err != nil {
				return last, fmt.Errorf("invalid compaction journal transition: %w", err)
			}
		}
		last = next
	}
	if err := s.Err(); err != nil {
		return last, err
	}
	if last.ID == "" {
		return last, errors.New("empty compaction journal")
	}
	return last, nil
}

// FindActive finds exactly one non-terminal run bound to an operation, target,
// and opaque consumer. It never guesses by modification time.
func (j *PreparedStoreUpgradeFileJournal) FindActive(ctx context.Context, operation domain.PreparedStoreUpgradeOperation, target, binding string) (domain.PreparedStoreUpgradeRun, error) {
	if !operation.Known() || target == "" || binding == "" {
		return domain.PreparedStoreUpgradeRun{}, errors.New("invalid prepared upgrade lookup")
	}
	entries, err := os.ReadDir(j.Dir)
	if os.IsNotExist(err) {
		return domain.PreparedStoreUpgradeRun{}, os.ErrNotExist
	}
	if err != nil {
		return domain.PreparedStoreUpgradeRun{}, err
	}
	if err := validateJournalDirectory(j.Dir); err != nil {
		return domain.PreparedStoreUpgradeRun{}, err
	}
	if len(entries) > maxActiveUpgradeJournals {
		return domain.PreparedStoreUpgradeRun{}, errors.New("prepared upgrade journal entry limit exceeded")
	}
	canonicalTarget := filepath.Clean(target)
	var match domain.PreparedStoreUpgradeRun
	for _, entry := range entries {
		if err := ctx.Err(); err != nil {
			return domain.PreparedStoreUpgradeRun{}, err
		}
		name := entry.Name()
		if entry.Type()&os.ModeSymlink != 0 || !strings.HasSuffix(name, ".jsonl") {
			return domain.PreparedStoreUpgradeRun{}, errors.New("invalid prepared upgrade journal entry")
		}
		info, infoErr := entry.Info()
		if infoErr != nil || !info.Mode().IsRegular() || info.Mode().Perm() != 0o600 || fileLinkCount(info) != 1 || info.Size() <= 0 || info.Size() > maxCompactionJournalBytes {
			return domain.PreparedStoreUpgradeRun{}, errors.New("invalid prepared upgrade journal file")
		}
		id := strings.TrimSuffix(name, ".jsonl")
		run, loadErr := j.Load(ctx, id)
		if loadErr != nil {
			return domain.PreparedStoreUpgradeRun{}, loadErr
		}
		if run.Operation != operation || filepath.Clean(run.SourcePath) != canonicalTarget || run.ConsumerBinding != binding || run.Phase == domain.PreparedStoreUpgradeRolledBack {
			continue
		}
		if match.ID != "" {
			return domain.PreparedStoreUpgradeRun{}, errors.New("ambiguous active prepared upgrade")
		}
		match = run
	}
	if match.ID == "" {
		return match, os.ErrNotExist
	}
	return match, nil
}

func validateJournalDirectory(path string) error {
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 || info.Mode().Perm() != 0o700 || fileLinkCount(info) < 1 {
		return errors.New("invalid prepared upgrade journal directory")
	}
	return nil
}

func fileLinkCount(info os.FileInfo) uint64 {
	id, err := platformStoreFileIdentity(info)
	if err != nil {
		return 0
	}
	return id.Links
}

func validateInitialRun(run domain.CompactionRun) error {
	if run.Phase != domain.CompactionPlanned || run.SourcePath == "" || run.CandidatePath == "" || run.RollbackPath == "" || run.CreatedAt.IsZero() || run.UpdatedAt.IsZero() {
		return errors.New("invalid initial compaction run")
	}
	if run.SourceIdentity == (domain.StoreFileIdentity{}) {
		return errors.New("initial source identity is required")
	}
	if run.PreparedCandidateIdentity != (domain.StoreFileIdentity{}) || run.PreparedAttempt != 0 {
		return errors.New("initial prepared candidate identity must be empty")
	}
	if run.Resources.RequiredBytes == 0 || run.Resources.DestinationBytes == 0 || run.Resources.FilesystemDevice != run.SourceIdentity.Device || !run.Resources.ExchangeCapability {
		return errors.New("initial resource plan is incomplete")
	}
	if run.UpdatedAt.Before(run.CreatedAt) {
		return errors.New("initial timestamps are inconsistent")
	}
	if run.Operation != "" && (!run.Operation.Known() || run.ConsumerBinding == "") {
		return errors.New("initial prepared upgrade binding is invalid")
	}
	if run.Operation == domain.PreparedStoreUpgradeOperationPayloadRehearsalMigration && (run.PlanDigest == "" || run.SourceDigest == "" || run.Budget.WallTimeLimit <= 0 || run.Budget.PublishLockLimit <= 0 || run.Budget.PublishLockLimit > time.Second || run.Budget.OwnedDiskByteLimit == 0 || run.Budget.WALByteLimit == 0 || run.Budget.TemporaryByteLimit == 0) {
		return errors.New("initial prepared migration plan or budget is invalid")
	}
	return nil
}

func validateRunAppend(previous, next domain.CompactionRun) error {
	if previous.ID != next.ID || previous.SourcePath != next.SourcePath || previous.CandidatePath != next.CandidatePath || previous.RollbackPath != next.RollbackPath || previous.SourceIdentity != next.SourceIdentity || previous.SourceDigest != next.SourceDigest || previous.Resources != next.Resources || previous.Operation != next.Operation || previous.ConsumerBinding != next.ConsumerBinding || previous.PlanDigest != next.PlanDigest || previous.Budget != next.Budget || !previous.CreatedAt.Equal(next.CreatedAt) {
		return errors.New("immutable compaction run fields changed")
	}
	if next.UpdatedAt.Before(previous.UpdatedAt) {
		return errors.New("compaction timestamp moved backwards")
	}
	if previous.Candidate != (domain.StoreFileIdentity{}) && previous.Candidate != next.Candidate {
		return errors.New("candidate identity changed after fencing")
	}
	if previous.Candidate == (domain.StoreFileIdentity{}) && next.Candidate != (domain.StoreFileIdentity{}) && next.Phase != domain.CompactionCandidateVerified {
		return errors.New("candidate identity appeared outside verification")
	}
	if next.Phase == domain.CompactionCandidateVerified && !next.Candidate.SameInode(next.PreparedCandidateIdentity) {
		return errors.New("verified candidate differs from prepared inode")
	}
	if previous.Evidence != (domain.PreparedCandidateEvidence{}) && previous.Evidence != next.Evidence {
		return errors.New("candidate evidence changed after verification")
	}
	if previous.Evidence == (domain.PreparedCandidateEvidence{}) && next.Evidence != (domain.PreparedCandidateEvidence{}) && next.Phase != domain.CompactionCandidateVerified {
		return errors.New("candidate evidence appeared outside verification")
	}
	if next.Phase == domain.CompactionCandidatePrepared && (previous.Phase == domain.CompactionCopyIntent || previous.Phase == domain.CompactionCopyRetryIntent) {
		if next.PreparedCandidateIdentity == (domain.StoreFileIdentity{}) || next.PreparedCandidateIdentity.Size != 0 || next.PreparedAttempt != previous.PreparedAttempt+1 {
			return errors.New("prepared candidate identity/attempt is invalid")
		}
	} else if next.PreparedCandidateIdentity != previous.PreparedCandidateIdentity || next.PreparedAttempt != previous.PreparedAttempt {
		return errors.New("prepared candidate identity changed outside prepare transition")
	}
	if _, err := previous.Advance(next.Phase, next.UpdatedAt); err != nil {
		return err
	}
	return nil
}

func writeJournalRecord(w io.Writer, run domain.CompactionRun) error {
	record, err := json.Marshal(run)
	if err != nil {
		return err
	}
	if len(record)+1 > maxCompactionJournalBytes {
		return errors.New("compaction journal record too large")
	}
	_, err = w.Write(append(record, '\n'))
	return err
}

// PreparedStoreUpgradeFiles implements fenced, same-directory replacement.
type PreparedStoreUpgradeFiles struct {
	// CallerHoldsExclusiveLease declares that the caller holds the exclusive
	// store lease for the entire call. This is the precondition that licenses
	// recovery of stale SQLite sidecars.
	CallerHoldsExclusiveLease bool
	recoveryHook              func(string) error
}

// StoreReplacementFiles preserves the legacy compaction composition surface.
type StoreReplacementFiles = PreparedStoreUpgradeFiles

func (f PreparedStoreUpgradeFiles) Plan(ctx context.Context, run domain.CompactionRun) (domain.CompactionRun, error) {
	if err := ctx.Err(); err != nil {
		return run, err
	}
	if filepath.Dir(run.SourcePath) != filepath.Dir(run.CandidatePath) || filepath.Dir(run.SourcePath) != filepath.Dir(run.RollbackPath) {
		return run, errors.New("compaction paths must share one directory")
	}
	for _, path := range []string{run.CandidatePath, run.RollbackPath} {
		if _, err := os.Lstat(path); !os.IsNotExist(err) {
			return run, fmt.Errorf("compaction output already exists: %s", path)
		}
	}
	if err := validateStoreLinkIdentity(run.SourcePath); err != nil {
		return run, err
	}
	if err := f.checkSQLiteSidecars(ctx, run.SourcePath); err != nil {
		return run, err
	}
	// Once the cleanup returns, the store has no WAL/SHM at all -- stale ones
	// were removed and a live one would have been refused -- so it is safe to
	// open. This runs before the digest deliberately: hashing a 24 GiB
	// source only to copy 16 GiB of dead index into the candidate is the exact
	// waste the check exists to prevent.
	//
	if err := (PreparedStoreUpgradeFiles{}).RejectRetiredSearchIndex(ctx, run); err != nil {
		return run, err
	}
	id, err := inspectRegularFile(run.SourcePath)
	if err != nil {
		return run, err
	}
	available, err := availableBytes(filepath.Dir(run.SourcePath))
	if err != nil {
		return run, err
	}
	temporary := uint64(0)
	if run.Operation == domain.PreparedStoreUpgradeOperationPayloadRehearsalMigration {
		if ^uint64(0)-run.Budget.WALByteLimit < run.Budget.TemporaryByteLimit {
			return run, errors.New("prepared upgrade resource size overflow")
		}
		temporary = run.Budget.WALByteLimit + run.Budget.TemporaryByteLimit
	}
	required, margin, err := compactionRequiredBytes(id.Size, temporary)
	if err != nil {
		return run, err
	}
	if required > available {
		return run, insufficientCompactionSpaceError(required, available, id.Size)
	}
	run.SourceIdentity = id
	digest, err := fileDigest(run.SourcePath)
	if err != nil {
		return run, errors.New("cannot digest prepared upgrade source")
	}
	run.SourceDigest = digest
	exchangeCapability := probeReplacementCapabilities(filepath.Dir(run.SourcePath)) == nil
	var leaseCapability bool
	if f.CallerHoldsExclusiveLease {
		// Do not probe here: flock locks attach to the open file description, so
		// a fresh descriptor conflicts with the caller's held exclusive lease
		// and retries until ctx is done.
		leaseCapability = true
	} else {
		leaseCapability = probeStoreLeaseCapability(ctx, run.SourcePath) == nil
	}
	run.Resources = domain.CompactionResourcePlan{RequiredBytes: required, DestinationBytes: uint64(id.Size), TemporaryBytes: temporary, SafetyMarginBytes: margin, AvailableBytes: available, FilesystemDevice: id.Device, LeaseCapability: leaseCapability, ExchangeCapability: exchangeCapability}
	if !run.Resources.ExchangeCapability {
		return run, errors.New("atomic exchange capability unavailable")
	}
	if !run.Resources.LeaseCapability {
		return run, errors.New("cross-process store lease capability unavailable")
	}
	return run, nil
}

// insufficientCompactionSpaceError explains a refusal the operator has to act
// on, which "need N, have M" does not: it leaves them to subtract two
// eleven-digit numbers, and it implies the whole requirement is real when most
// of it is a reservation against an unknown. It is written across several
// lines because it is read in a terminal, at the moment the store is already
// too large to compact.
func insufficientCompactionSpaceError(required, available uint64, sourceSize int64) error {
	return fmt.Errorf(`insufficient free space: free %d more bytes to proceed (need %d, have %d)
	most of that is a worst-case reservation: VACUUM INTO cannot report the compacted size until it has written it, so the requirement assumes the result could be as large as the %d-byte source. It is usually far smaller, but reserving less risks a half-written candidate on a full disk
	the space is not all returned when the run succeeds: a rollback copy of the source is kept as <db>.rollback-<run id>, and nothing removes it. Deleting it is your decision, and gives up "store compact rollback" for that run`,
		required-available, required, available, sourceSize)
}

func (f PreparedStoreUpgradeFiles) Recheck(ctx context.Context, run domain.CompactionRun) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if !run.Resources.LeaseCapability {
		return errors.New("cross-process store lease capability unavailable; apply fails closed")
	}
	if err := validateStoreLinkIdentity(run.SourcePath); err != nil {
		return err
	}
	current, err := inspectRegularFile(run.SourcePath)
	if err != nil {
		return err
	}
	if current != run.SourceIdentity {
		return errors.New("source identity drift after plan")
	}
	digest, err := fileDigest(run.SourcePath)
	if err != nil || digest != run.SourceDigest {
		return errors.New("prepared upgrade source content changed")
	}
	available, err := availableBytes(filepath.Dir(run.SourcePath))
	if err != nil {
		return err
	}
	if available < run.Resources.RequiredBytes {
		return fmt.Errorf("free space drift: need %d bytes, have %d", run.Resources.RequiredBytes, available)
	}
	if current.Device != run.Resources.FilesystemDevice || !atomicExchangeSupported() {
		return errors.New("planned filesystem capability drift")
	}
	// Apply and resume call Recheck while holding the exclusive store lease.
	// Clean up the same stale zero-byte sidecars that Plan handles; a reader
	// can create them after Plan returns but before apply/resume starts.
	return f.checkSQLiteSidecars(ctx, run.SourcePath)
}

func (f PreparedStoreUpgradeFiles) checkSQLiteSidecars(ctx context.Context, storePath string) error {
	if f.CallerHoldsExclusiveLease {
		return cleanupStaleSQLiteSidecars(ctx, storePath)
	}
	return rejectSQLiteSidecars(storePath)
}

// RecheckForPublish performs only constant-count identity/link/sidecar checks.
// It deliberately does not hash, open SQLite, inspect free space, or copy data
// while the adjacent exclusive lease is held.
//
// Unlike Recheck, this check remains strict: cleanup has already happened
// under the lease, so a sidecar appearing here means a live opener appeared
// mid-run and the run must abort.
func (PreparedStoreUpgradeFiles) RecheckForPublish(ctx context.Context, run domain.PreparedStoreUpgradeRun) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	for _, path := range []string{run.SourcePath, run.CandidatePath} {
		if err := validateStoreLinkIdentity(path); err != nil {
			return err
		}
		if err := rejectSQLiteSidecars(path); err != nil {
			return err
		}
	}
	source, err := inspectRegularFile(run.SourcePath)
	if err != nil {
		return err
	}
	candidate, err := inspectRegularFile(run.CandidatePath)
	if err != nil {
		return err
	}
	if source != run.SourceIdentity || candidate != run.Candidate || !candidate.SameInode(run.PreparedCandidateIdentity) {
		return errors.New("prepared upgrade publication identity fence changed")
	}
	if _, exists, err := inspectOptionalRegularFile(run.RollbackPath); err != nil || exists {
		return errors.New("prepared upgrade rollback destination is not empty")
	}
	return nil
}

func (f PreparedStoreUpgradeFiles) PrepareCandidate(ctx context.Context, run domain.CompactionRun) (domain.StoreFileIdentity, error) {
	if err := ctx.Err(); err != nil {
		return domain.StoreFileIdentity{}, err
	}
	if run.Phase != domain.CompactionCopyIntent && run.Phase != domain.CompactionCopyRetryIntent {
		return domain.StoreFileIdentity{}, errors.New("candidate prepare requires copy intent")
	}
	if run.CandidatePath != ownedCandidatePath(run) {
		return domain.StoreFileIdentity{}, errors.New("candidate path is not owned by run")
	}
	if f.recoveryHook != nil {
		if err := f.recoveryHook("before_create"); err != nil {
			return domain.StoreFileIdentity{}, err
		}
	}
	file, err := openFileNoFollow(run.CandidatePath, os.O_CREATE|os.O_EXCL|os.O_RDWR, 0o600)
	if err != nil {
		return domain.StoreFileIdentity{}, fmt.Errorf("create prepared candidate: %w", err)
	}
	if f.recoveryHook != nil {
		if err := f.recoveryHook("after_create"); err != nil {
			_ = file.Close()
			return domain.StoreFileIdentity{}, err
		}
	}
	if err := file.Sync(); err != nil {
		_ = file.Close()
		return domain.StoreFileIdentity{}, fmt.Errorf("sync prepared candidate: %w", err)
	}
	if f.recoveryHook != nil {
		if err := f.recoveryHook("after_file_sync"); err != nil {
			_ = file.Close()
			return domain.StoreFileIdentity{}, err
		}
	}
	if err := file.Close(); err != nil {
		return domain.StoreFileIdentity{}, err
	}
	identity, err := inspectRegularFile(run.CandidatePath)
	if err != nil {
		return identity, err
	}
	if identity.Size != 0 || identity.Device != run.SourceIdentity.Device {
		return identity, errors.New("prepared candidate identity is invalid")
	}
	if err := syncDirectory(filepath.Dir(run.CandidatePath)); err != nil {
		return identity, fmt.Errorf("sync directory after candidate create: %w", err)
	}
	if f.recoveryHook != nil {
		if err := f.recoveryHook("after_create_dir_sync"); err != nil {
			return identity, err
		}
	}
	return identity, nil
}

func (f PreparedStoreUpgradeFiles) RemoveOwnedPartialCandidate(ctx context.Context, run domain.CompactionRun, observation domain.CompactionObservation) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if (run.Phase != domain.CompactionCandidatePrepared && run.Phase != domain.CompactionCopyRetryIntent) || observation.Orientation != domain.OrientationCandidateReady || (run.Phase == domain.CompactionCandidatePrepared && observation.CandidateCondition != domain.CandidateConditionOwnedIncomplete) {
		return errors.New("partial candidate cleanup is not aggregate-authorized")
	}
	if run.CandidatePath != ownedCandidatePath(run) {
		return errors.New("candidate path is not owned by run")
	}
	if observation.RollbackExists {
		return errors.New("rollback path exists during copy recovery")
	}
	source, err := inspectRegularFile(run.SourcePath)
	if err != nil {
		return err
	}
	candidate, err := inspectRegularFile(run.CandidatePath)
	if err != nil {
		return err
	}
	if source != run.SourceIdentity || candidate != observation.Candidate || !candidate.SameInode(run.PreparedCandidateIdentity) || candidate.Device != source.Device {
		return errors.New("partial candidate identity fence changed")
	}
	if f.recoveryHook != nil {
		if err := f.recoveryHook("before_unlink"); err != nil {
			return err
		}
	}
	if err := os.Remove(run.CandidatePath); err != nil {
		return fmt.Errorf("unlink owned partial candidate: %w", err)
	}
	if f.recoveryHook != nil {
		if err := f.recoveryHook("after_unlink"); err != nil {
			return err
		}
	}
	if err := syncDirectory(filepath.Dir(run.CandidatePath)); err != nil {
		return fmt.Errorf("sync directory after partial candidate unlink: %w", err)
	}
	if f.recoveryHook != nil {
		if err := f.recoveryHook("after_dir_sync"); err != nil {
			return err
		}
	}
	return nil
}

func ownedCandidatePath(run domain.PreparedStoreUpgradeRun) string {
	if run.Operation == domain.PreparedStoreUpgradeOperationPayloadRehearsalMigration {
		return run.SourcePath + ".prepare-" + run.ID
	}
	return run.SourcePath + ".compact-" + run.ID
}

func compactionRequiredBytes(destination int64, temporary uint64) (uint64, uint64, error) {
	if destination < 0 {
		return 0, 0, errors.New("negative source size")
	}
	base := uint64(destination)
	margin := base / 10
	if margin < 64<<20 {
		margin = 64 << 20
	}
	if ^uint64(0)-base < temporary || ^uint64(0)-(base+temporary) < margin {
		return 0, 0, errors.New("compaction resource size overflow")
	}
	return base + temporary + margin, margin, nil
}

func probeReplacementCapabilities(dir string) (err error) {
	left, err := os.CreateTemp(dir, ".traceary-exchange-left-")
	if err != nil {
		return err
	}
	leftPath := left.Name()
	defer func() { _ = os.Remove(leftPath) }()
	if err := left.Close(); err != nil {
		return err
	}
	right, err := os.CreateTemp(dir, ".traceary-exchange-right-")
	if err != nil {
		return err
	}
	rightPath := right.Name()
	defer func() { _ = os.Remove(rightPath) }()
	if err := right.Close(); err != nil {
		return err
	}
	if err := atomicExchange(leftPath, rightPath); err != nil {
		return fmt.Errorf("probe atomic exchange: %w", err)
	}
	publish, err := os.CreateTemp(dir, ".traceary-publish-source-")
	if err != nil {
		return err
	}
	publishPath := publish.Name()
	defer func() { _ = os.Remove(publishPath) }()
	if err := publish.Close(); err != nil {
		return err
	}
	target := publishPath + ".target"
	defer func() { _ = os.Remove(target) }()
	if err := renameNoReplace(publishPath, target); err != nil {
		return fmt.Errorf("probe no-replace rename: %w", err)
	}
	if err := renameNoReplace(target, publishPath); err != nil {
		return fmt.Errorf("restore no-replace probe: %w", err)
	}
	return syncDirectory(dir)
}

// FenceCandidate captures the verified candidate identity before swap intent.
func (PreparedStoreUpgradeFiles) FenceCandidate(ctx context.Context, run domain.CompactionRun) (domain.CompactionRun, error) {
	if err := ctx.Err(); err != nil {
		return run, err
	}
	if err := rejectSQLiteSidecars(run.CandidatePath); err != nil {
		return run, err
	}
	for _, path := range []string{run.SourcePath, run.CandidatePath} {
		if err := validateStoreLinkIdentity(path); err != nil {
			return run, err
		}
	}
	identity, err := inspectRegularFile(run.CandidatePath)
	if err != nil {
		return run, err
	}
	if identity.Device != run.SourceIdentity.Device {
		return run, errors.New("candidate is not on source filesystem")
	}
	if !identity.SameInode(run.PreparedCandidateIdentity) {
		return run, errors.New("candidate inode differs from prepared identity")
	}
	run.Candidate = identity
	return run, nil
}

func (f PreparedStoreUpgradeFiles) Exchange(ctx context.Context, run domain.CompactionRun) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	left, right := run.SourcePath, run.CandidatePath
	if run.Phase == domain.CompactionRollbackSwapIntent {
		right = run.RollbackPath
	}
	for _, path := range []string{left, right} {
		if err := validateStoreLinkIdentity(path); err != nil {
			return err
		}
	}
	current, err := inspectRegularFile(left)
	if err != nil {
		return err
	}
	if run.Phase == domain.CompactionRollbackSwapIntent {
		if !samePublishedFile(current, run.Candidate) {
			return errors.New("compacted source identity fence changed")
		}
	} else if current != run.SourceIdentity {
		return errors.New("source identity fence changed")
	}
	rightIdentity, err := inspectRegularFile(right)
	if err != nil {
		return err
	}
	if run.Phase == domain.CompactionRollbackSwapIntent {
		if rightIdentity != run.SourceIdentity {
			return errors.New("rollback identity fence changed")
		}
	} else if rightIdentity != run.Candidate {
		return errors.New("candidate identity fence changed")
	}
	if err := rejectSQLiteSidecars(left); err != nil {
		return err
	}
	if err := atomicExchange(left, right); err != nil {
		return err
	}
	if f.recoveryHook != nil {
		if err := f.recoveryHook("after_exchange"); err != nil {
			return err
		}
	}
	if err := syncFile(left); err != nil {
		return err
	}
	if f.recoveryHook != nil {
		if err := f.recoveryHook("after_exchange_left_sync"); err != nil {
			return err
		}
	}
	if err := syncFile(right); err != nil {
		return err
	}
	if f.recoveryHook != nil {
		if err := f.recoveryHook("after_exchange_right_sync"); err != nil {
			return err
		}
	}
	if f.recoveryHook != nil {
		if err := f.recoveryHook("before_exchange_dir_sync"); err != nil {
			return err
		}
	}
	if err := syncDirectory(filepath.Dir(left)); err != nil {
		return err
	}
	if f.recoveryHook != nil {
		return f.recoveryHook("after_exchange_dir_sync")
	}
	return nil
}

func (f PreparedStoreUpgradeFiles) PublishRollback(ctx context.Context, run domain.CompactionRun) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if _, err := os.Lstat(run.RollbackPath); !os.IsNotExist(err) {
		return errors.New("rollback destination exists")
	}
	for _, path := range []string{run.SourcePath, run.CandidatePath} {
		if err := validateStoreLinkIdentity(path); err != nil {
			return err
		}
	}
	if err := renameNoReplace(run.CandidatePath, run.RollbackPath); err != nil {
		return fmt.Errorf("publish rollback without replacement: %w", err)
	}
	if f.recoveryHook != nil {
		if err := f.recoveryHook("after_publish_rename"); err != nil {
			return err
		}
	}
	if err := syncFile(run.RollbackPath); err != nil {
		return err
	}
	if f.recoveryHook != nil {
		if err := f.recoveryHook("after_publish_file_sync"); err != nil {
			return err
		}
	}
	if f.recoveryHook != nil {
		if err := f.recoveryHook("before_publish_dir_sync"); err != nil {
			return err
		}
	}
	if err := syncDirectory(filepath.Dir(run.SourcePath)); err != nil {
		return err
	}
	if f.recoveryHook != nil {
		return f.recoveryHook("after_publish_dir_sync")
	}
	return nil
}

func (PreparedStoreUpgradeFiles) Observe(_ context.Context, run domain.CompactionRun) (domain.CompactionObservation, error) {
	source, err := inspectRegularFile(run.SourcePath)
	if err != nil {
		return domain.CompactionObservation{}, err
	}
	candidate, candidateExists, err := inspectOptionalRegularFile(run.CandidatePath)
	if err != nil {
		return domain.CompactionObservation{}, err
	}
	rollback, rollbackExists, err := inspectOptionalRegularFile(run.RollbackPath)
	if err != nil {
		return domain.CompactionObservation{}, err
	}
	obs := domain.CompactionObservation{Source: source, Candidate: candidate, Rollback: rollback, CandidateExists: candidateExists, RollbackExists: rollbackExists}
	switch run.Phase {
	case domain.CompactionCopyComplete, domain.CompactionCandidateSyncIntent, domain.CompactionCandidateSynced,
		domain.CompactionScrubInProgress:
		if !candidateExists || !candidate.SameInode(run.PreparedCandidateIdentity) {
			return obs, errors.New("candidate inode differs from prepared identity")
		}
	}
	identities := map[[2]uint64]string{}
	for path, id := range map[string]domain.StoreFileIdentity{"source": source, "candidate": candidate, "rollback": rollback} {
		if id == (domain.StoreFileIdentity{}) {
			continue
		}
		key := [2]uint64{id.Device, id.Inode}
		if previous := identities[key]; previous != "" {
			return obs, fmt.Errorf("duplicate compaction inode at %s and %s", previous, path)
		}
		identities[key] = path
	}
	switch {
	case source == run.SourceIdentity && !candidateExists && !rollbackExists:
		obs.Orientation = domain.OrientationSourceOriginal
	case source == run.SourceIdentity && candidateExists && !rollbackExists:
		if run.Candidate != (domain.StoreFileIdentity{}) && candidate != run.Candidate {
			return obs, errors.New("candidate-ready identity conflicts with journal fence")
		}
		obs.Orientation = domain.OrientationCandidateReady
	case run.Candidate != (domain.StoreFileIdentity{}) && samePublishedFile(source, run.Candidate) && candidateExists && candidate == run.SourceIdentity && !rollbackExists:
		obs.Orientation = domain.OrientationSwapped
	case run.Candidate != (domain.StoreFileIdentity{}) && samePublishedFile(source, run.Candidate) && !candidateExists && rollbackExists && rollback == run.SourceIdentity:
		obs.Orientation = domain.OrientationRollbackReady
	case source == run.SourceIdentity && !candidateExists && rollbackExists && samePublishedFile(rollback, run.Candidate):
		obs.Orientation = domain.OrientationRolledBack
	default:
		return obs, errors.New("unknown compaction file orientation")
	}
	return obs, nil
}

// samePublishedFile accepts expected content mutation after publication while
// preserving the immutable object, mode, and link-count fence.
func samePublishedFile(current, published domain.StoreFileIdentity) bool {
	return current.SameInode(published) && current.Mode == published.Mode && current.Links == published.Links
}

func (f PreparedStoreUpgradeFiles) SyncRecoveredOrientation(ctx context.Context, run domain.CompactionRun, observation domain.CompactionObservation) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	var paths []string
	switch observation.Orientation {
	case domain.OrientationSwapped:
		paths = []string{run.SourcePath, run.CandidatePath}
	case domain.OrientationRollbackReady, domain.OrientationRolledBack:
		paths = []string{run.SourcePath, run.RollbackPath}
	default:
		return errors.New("cannot sync unknown recovered orientation")
	}
	for index, path := range paths {
		if f.recoveryHook != nil {
			if err := f.recoveryHook(fmt.Sprintf("before_recovery_file_sync_%d", index)); err != nil {
				return err
			}
		}
		if err := syncFile(path); err != nil {
			return err
		}
		if f.recoveryHook != nil {
			if err := f.recoveryHook(fmt.Sprintf("after_recovery_file_sync_%d", index)); err != nil {
				return err
			}
		}
	}
	if f.recoveryHook != nil {
		if err := f.recoveryHook("before_recovery_dir_sync"); err != nil {
			return err
		}
	}
	if err := syncDirectory(filepath.Dir(run.SourcePath)); err != nil {
		return err
	}
	if f.recoveryHook != nil {
		return f.recoveryHook("after_recovery_dir_sync")
	}
	return nil
}

func inspectOptionalRegularFile(path string) (domain.StoreFileIdentity, bool, error) {
	id, err := inspectRegularFile(path)
	if os.IsNotExist(err) {
		return id, false, nil
	}
	return id, err == nil, err
}

func inspectRegularFile(path string) (domain.StoreFileIdentity, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return domain.StoreFileIdentity{}, err
	}
	if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
		return domain.StoreFileIdentity{}, fmt.Errorf("refuse non-regular or symlink path: %s", path)
	}
	return platformStoreFileIdentity(info)
}

func rejectSQLiteSidecars(path string) error {
	for _, suffix := range []string{"-wal", "-shm", "-journal"} {
		sidecarPath := path + suffix
		if info, err := os.Lstat(sidecarPath); err == nil {
			return sqliteSidecarRefusal(sidecarPath, info)
		} else if !os.IsNotExist(err) {
			return err
		}
	}
	return nil
}

func syncFile(path string) error {
	f, err := openFileNoFollow(path, os.O_RDONLY, 0)
	if err != nil {
		return err
	}
	defer f.Close()
	return f.Sync()
}
func syncDirectory(path string) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	return f.Sync()
}
