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
		if next.ID != id {
			return last, errors.New("compaction journal run id mismatch")
		}
		if first {
			if next.Phase != domain.CompactionPlanned {
				return last, errors.New("compaction journal must start at planned")
			}
			first = false
		} else {
			advanced, err := last.Advance(next.Phase, next.UpdatedAt)
			if err != nil || advanced.Phase != next.Phase {
				return last, errors.New("invalid compaction journal transition")
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
type StoreReplacementFiles struct{}

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
	if id.Size < 0 || uint64(id.Size) > available {
		return run, fmt.Errorf("insufficient free space: need %d bytes, have %d", id.Size, available)
	}
	run.SourceIdentity = id
	return run, nil
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
	if err := os.Link(run.CandidatePath, run.RollbackPath); err != nil {
		return fmt.Errorf("publish rollback without replacement: %w", err)
	}
	if err := os.Remove(run.CandidatePath); err != nil {
		return err
	}
	if err := syncFile(run.RollbackPath); err != nil {
		return err
	}
	return syncDirectory(filepath.Dir(run.SourcePath))
}

func (StoreReplacementFiles) Orientation(_ context.Context, run domain.CompactionRun) (domain.CompactionPhase, error) {
	source, err := inspectRegularFile(run.SourcePath)
	if err != nil {
		return run.Phase, err
	}
	_, candidateErr := inspectRegularFile(run.CandidatePath)
	_, rollbackErr := inspectRegularFile(run.RollbackPath)
	if source == run.SourceIdentity {
		if run.Phase == domain.CompactionRollbackSwapIntent {
			return domain.CompactionRollbackSwapped, nil
		}
		return run.Phase, nil
	}
	if candidateErr == nil {
		return domain.CompactionSwapped, nil
	}
	if rollbackErr == nil {
		return domain.CompactionRollbackReady, nil
	}
	return run.Phase, errors.New("cannot determine compaction file orientation")
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
