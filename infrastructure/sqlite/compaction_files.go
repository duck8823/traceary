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
	"syscall"

	"github.com/duck8823/traceary/domain"
)

const maxCompactionJournalBytes = 1 << 20

// CompactionFileJournal is an append-only, capped protocol journal.
type CompactionFileJournal struct{ Dir string }

func (j *CompactionFileJournal) path(id string) (string, error) {
	if len(id) != 32 || strings.Trim(id, "0123456789abcdef") != "" {
		return "", fmt.Errorf("invalid compaction run id")
	}
	return filepath.Join(j.Dir, id+".jsonl"), nil
}

func (j *CompactionFileJournal) Create(ctx context.Context, run domain.CompactionRun) error {
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
	if err := os.MkdirAll(j.Dir, 0o700); err != nil {
		return err
	}
	f, err := openFileNoFollow(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return fmt.Errorf("create compaction journal: %w", err)
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

func (j *CompactionFileJournal) Append(ctx context.Context, run domain.CompactionRun) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	path, err := j.path(run.ID)
	if err != nil {
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
	if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
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

func (j *CompactionFileJournal) Load(ctx context.Context, id string) (domain.CompactionRun, error) {
	if err := ctx.Err(); err != nil {
		return domain.CompactionRun{}, err
	}
	path, err := j.path(id)
	if err != nil {
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
	return nil
}

func validateRunAppend(previous, next domain.CompactionRun) error {
	if previous.ID != next.ID || previous.SourcePath != next.SourcePath || previous.CandidatePath != next.CandidatePath || previous.RollbackPath != next.RollbackPath || previous.SourceIdentity != next.SourceIdentity || previous.Resources != next.Resources || !previous.CreatedAt.Equal(next.CreatedAt) {
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

// StoreReplacementFiles implements fenced, same-directory replacement.
type StoreReplacementFiles struct{ recoveryHook func(string) error }

func (StoreReplacementFiles) Plan(ctx context.Context, run domain.CompactionRun) (domain.CompactionRun, error) {
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
	if err := rejectSQLiteSidecars(run.SourcePath); err != nil {
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
	required, margin, err := compactionRequiredBytes(id.Size, 0)
	if err != nil {
		return run, err
	}
	if required > available {
		return run, fmt.Errorf("insufficient free space: need %d bytes, have %d", required, available)
	}
	run.SourceIdentity = id
	exchangeCapability := probeReplacementCapabilities(filepath.Dir(run.SourcePath)) == nil
	leaseCapability := probeStoreLeaseCapability(ctx, run.SourcePath) == nil
	run.Resources = domain.CompactionResourcePlan{RequiredBytes: required, DestinationBytes: uint64(id.Size), TemporaryBytes: 0, SafetyMarginBytes: margin, AvailableBytes: available, FilesystemDevice: id.Device, LeaseCapability: leaseCapability, ExchangeCapability: exchangeCapability}
	if !run.Resources.ExchangeCapability {
		return run, errors.New("atomic exchange capability unavailable")
	}
	if !run.Resources.LeaseCapability {
		return run, errors.New("cross-process store lease capability unavailable")
	}
	return run, nil
}

func (StoreReplacementFiles) Recheck(ctx context.Context, run domain.CompactionRun) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if !run.Resources.LeaseCapability {
		return errors.New("cross-process store lease capability unavailable; apply fails closed")
	}
	current, err := inspectRegularFile(run.SourcePath)
	if err != nil {
		return err
	}
	if current != run.SourceIdentity {
		return errors.New("source identity drift after plan")
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
	return rejectSQLiteSidecars(run.SourcePath)
}

func (f StoreReplacementFiles) PrepareCandidate(ctx context.Context, run domain.CompactionRun) (domain.StoreFileIdentity, error) {
	if err := ctx.Err(); err != nil {
		return domain.StoreFileIdentity{}, err
	}
	if run.Phase != domain.CompactionCopyIntent && run.Phase != domain.CompactionCopyRetryIntent {
		return domain.StoreFileIdentity{}, errors.New("candidate prepare requires copy intent")
	}
	if run.CandidatePath != run.SourcePath+".compact-"+run.ID {
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

func (f StoreReplacementFiles) RemoveOwnedPartialCandidate(ctx context.Context, run domain.CompactionRun, observation domain.CompactionObservation) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if run.Phase != domain.CompactionCandidatePrepared || observation.Orientation != domain.OrientationCandidateReady || observation.CandidateCondition != domain.CandidateConditionOwnedIncomplete {
		return errors.New("partial candidate cleanup is not aggregate-authorized")
	}
	if run.CandidatePath != run.SourcePath+".compact-"+run.ID {
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
func (StoreReplacementFiles) FenceCandidate(ctx context.Context, run domain.CompactionRun) (domain.CompactionRun, error) {
	if err := ctx.Err(); err != nil {
		return run, err
	}
	if err := rejectSQLiteSidecars(run.CandidatePath); err != nil {
		return run, err
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

func (StoreReplacementFiles) Exchange(ctx context.Context, run domain.CompactionRun) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	left, right := run.SourcePath, run.CandidatePath
	if run.Phase == domain.CompactionRollbackSwapIntent {
		right = run.RollbackPath
	}
	current, err := inspectRegularFile(left)
	if err != nil {
		return err
	}
	if run.Phase == domain.CompactionRollbackSwapIntent {
		if current != run.Candidate {
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
	if err := syncFile(left); err != nil {
		return err
	}
	if err := syncFile(right); err != nil {
		return err
	}
	return syncDirectory(filepath.Dir(left))
}

func (StoreReplacementFiles) PublishRollback(ctx context.Context, run domain.CompactionRun) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if _, err := os.Lstat(run.RollbackPath); !os.IsNotExist(err) {
		return errors.New("rollback destination exists")
	}
	if err := renameNoReplace(run.CandidatePath, run.RollbackPath); err != nil {
		return fmt.Errorf("publish rollback without replacement: %w", err)
	}
	if err := syncFile(run.RollbackPath); err != nil {
		return err
	}
	return syncDirectory(filepath.Dir(run.SourcePath))
}

func (StoreReplacementFiles) Observe(_ context.Context, run domain.CompactionRun) (domain.CompactionObservation, error) {
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
	case run.Candidate != (domain.StoreFileIdentity{}) && source == run.Candidate && candidateExists && candidate == run.SourceIdentity && !rollbackExists:
		obs.Orientation = domain.OrientationSwapped
	case run.Candidate != (domain.StoreFileIdentity{}) && source == run.Candidate && !candidateExists && rollbackExists && rollback == run.SourceIdentity:
		obs.Orientation = domain.OrientationRollbackReady
	case source == run.SourceIdentity && !candidateExists && rollbackExists && rollback == run.Candidate:
		obs.Orientation = domain.OrientationRolledBack
	default:
		return obs, errors.New("unknown compaction file orientation")
	}
	return obs, nil
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
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return domain.StoreFileIdentity{}, errors.New("file identity unsupported")
	}
	return domain.StoreFileIdentity{Device: uint64(stat.Dev), Inode: uint64(stat.Ino), Size: info.Size(), ModUnix: info.ModTime().UnixNano()}, nil
}

func rejectSQLiteSidecars(path string) error {
	for _, suffix := range []string{"-wal", "-shm", "-journal"} {
		if _, err := os.Lstat(path + suffix); err == nil {
			return fmt.Errorf("SQLite sidecar exists: %s", path+suffix)
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
