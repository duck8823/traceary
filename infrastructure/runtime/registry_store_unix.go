//go:build unix

package runtime

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"hash/fnv"
	"io"
	"os"
	"sync"
	"time"
	"unicode/utf8"

	"golang.org/x/sys/unix"
	"golang.org/x/xerrors"

	domain "github.com/duck8823/traceary/domain/runtime"
)

// MaxRegistryBytes is the R0 runtime metadata envelope ceiling.
const MaxRegistryBytes int64 = 128 * 1024 * 1024

// ErrCommitUncertain means rename succeeded but durability or root identity was lost.
var ErrCommitUncertain = errors.New("runtime registry commit outcome uncertain; reload and reconcile before retry")

// macOS flock ownership alone does not serialize independent descriptors in one
// process. Fixed stripes bound memory while serializing same-root local callers;
// the OS lock below remains authoritative between processes.
var registryProcessLocks [64]sync.Mutex

// RegistryStore persists only registry metadata, independently of the core SQLite store.
// The root is pinned by physical identity; each operation opens it descriptor-relatively.
type RegistryStore struct {
	root, witness string
	budget        int64
}

// NewRegistryStore creates an owner-private root and pins it. Budget may only tighten R0.
func NewRegistryStore(root string, budget int64) (*RegistryStore, error) {
	return newRegistryStore(root, budget, true)
}

// OpenRegistryStore opens an existing private root without creating any directory.
func OpenRegistryStore(root string, budget int64) (*RegistryStore, error) {
	return newRegistryStore(root, budget, false)
}
func newRegistryStore(root string, budget int64, create bool) (*RegistryStore, error) {
	if budget <= 0 || budget > MaxRegistryBytes {
		return nil, domain.ErrLimit
	}
	fd, err := privateRuntimeDir(root, create)
	if err != nil {
		return nil, xerrors.Errorf("load runtime registry: %w", err)
	}
	defer func() { _ = unix.Close(fd) }()
	witness, err := directoryWitness(fd)
	if err != nil {
		return nil, xerrors.Errorf("load runtime registry: %w", err)
	}
	return &RegistryStore{root, witness, budget}, nil
}

// Load returns an independently validated aggregate under the same OS lock as writers.
func (s *RegistryStore) Load(ctx context.Context) (*domain.Registry, error) {
	var result *domain.Registry
	err := s.locked(ctx, func(fd int) error {
		r, _, err := s.read(fd)
		if err != nil {
			return err
		}
		if err = s.verifyRoot(fd); err != nil {
			return err
		}
		result = r
		return nil
	})
	if err != nil {
		return nil, xerrors.Errorf("load runtime registry: %w", err)
	}
	return result, nil
}

// Update serializes independent instances and commits only successful, uncanceled changes.
func (s *RegistryStore) Update(ctx context.Context, mutate func(*domain.Registry) error) error {
	if mutate == nil {
		return domain.ErrInvalid
	}
	return s.locked(ctx, func(fd int) error {
		r, stamp, err := s.read(fd)
		if err != nil {
			return err
		}
		before := r.Snapshot().Revision
		if err = mutate(r); err != nil {
			return err
		}
		if err = ctx.Err(); err != nil {
			return xerrors.Errorf("runtime registry canceled: %w", err)
		}
		if r.Snapshot().Revision == before {
			return nil
		}
		data, err := encodeRegistry(r)
		if err != nil {
			return err
		}
		if int64(len(data)) > s.budget {
			return domain.ErrLimit
		}
		return s.write(ctx, fd, data, stamp)
	})
}
func (s *RegistryStore) locked(ctx context.Context, fn func(int) error) error {
	h := fnv.New32a()
	_, _ = h.Write([]byte(s.root))
	local := &registryProcessLocks[h.Sum32()%uint32(len(registryProcessLocks))]
	for !local.TryLock() {
		timer := time.NewTimer(10 * time.Millisecond)
		select {
		case <-ctx.Done():
			timer.Stop()
			return xerrors.Errorf("wait for runtime registry: %w", ctx.Err())
		case <-timer.C:
		}
	}
	defer local.Unlock()

	if err := ctx.Err(); err != nil {
		return xerrors.Errorf("runtime registry canceled: %w", err)
	}
	fd, err := privateRuntimeDir(s.root, false)
	if err != nil {
		return err
	}
	defer func() { _ = unix.Close(fd) }()
	witness, err := directoryWitness(fd)
	if err != nil || witness != s.witness {
		return domain.ErrChanged
	}
	lock, err := openPrivateLock(fd, "registry.lock")
	if err != nil {
		return xerrors.Errorf("open runtime metadata lock (%v): %w", err, domain.ErrUnsafe)
	}
	defer func() { _ = unix.Close(lock) }()
	var initial unix.Stat_t
	if err = unix.Fstat(lock, &initial); err != nil || !privateRegular(initial) {
		return xerrors.Errorf("verify runtime metadata lock attributes: %w", domain.ErrUnsafe)
	}
	for {
		err = unix.Flock(lock, unix.LOCK_EX|unix.LOCK_NB)
		if err == nil {
			break
		}
		if !errors.Is(err, unix.EWOULDBLOCK) && !errors.Is(err, unix.EAGAIN) {
			return xerrors.Errorf("acquire runtime metadata lock: %w", domain.ErrUnsafe)
		}
		timer := time.NewTimer(10 * time.Millisecond)
		select {
		case <-ctx.Done():
			timer.Stop()
			return xerrors.Errorf("wait for runtime registry: %w", ctx.Err())
		case <-timer.C:
		}
	}
	defer func() { _ = unix.Flock(lock, unix.LOCK_UN) }()
	var current unix.Stat_t
	if err = unix.Fstatat(fd, "registry.lock", &current, unix.AT_SYMLINK_NOFOLLOW); err != nil || !privateRegular(current) || current.Dev != initial.Dev || current.Ino != initial.Ino {
		return domain.ErrChanged
	}
	if err = ctx.Err(); err != nil {
		return xerrors.Errorf("runtime registry canceled: %w", err)
	}
	if err = s.verifyRoot(fd); err != nil {
		return err
	}
	return fn(fd)
}
func privateRegular(s unix.Stat_t) bool {
	return s.Uid == uint32(os.Geteuid()) && s.Mode&unix.S_IFMT == unix.S_IFREG && s.Mode&0777 == 0600 && s.Nlink == 1
}

func (s *RegistryStore) read(dir int) (*domain.Registry, *unix.Stat_t, error) {
	data, stamp, err := s.readMetadata(dir, "registry.json", s.budget)
	if err != nil {
		return nil, nil, err
	}
	if stamp == nil {
		return domain.NewRegistry(), nil, nil
	}
	r, err := decodeRegistry(data)
	return r, stamp, err
}
func (s *RegistryStore) readMetadata(dir int, name string, budget int64) ([]byte, *unix.Stat_t, error) {
	fd, err := unix.Openat(dir, name, unix.O_RDONLY|unix.O_NOFOLLOW|unix.O_CLOEXEC|unix.O_NONBLOCK, 0)
	if errors.Is(err, unix.ENOENT) {
		return nil, nil, nil
	}
	if err != nil {
		return nil, nil, xerrors.Errorf("open runtime metadata file: %w", domain.ErrUnsafe)
	}
	f := os.NewFile(uintptr(fd), "runtime registry")
	defer func() { _ = f.Close() }()
	var stat unix.Stat_t
	if err = unix.Fstat(fd, &stat); err != nil || !privateRegular(stat) {
		return nil, nil, xerrors.Errorf("verify runtime metadata file attributes: %w", domain.ErrUnsafe)
	}
	if stat.Size > budget {
		return nil, nil, domain.ErrLimit
	}
	data, err := io.ReadAll(io.LimitReader(f, budget+1))
	if err != nil {
		return nil, nil, xerrors.Errorf("read runtime registry: %w", err)
	}
	if int64(len(data)) > budget {
		return nil, nil, domain.ErrLimit
	}
	return data, &stat, nil
}
func (s *RegistryStore) write(ctx context.Context, dir int, data []byte, old *unix.Stat_t) error {
	return s.writeMetadata(ctx, dir, "registry.json", data, old)
}

// metadataBudget runs under the shared root lock. It accounts committed metadata
// across owners; temporary atomic-write copies are bounded separately by file size.
func (s *RegistryStore) metadataBudget(dir int, target string, size int64) error {
	if size < 0 || size > s.budget {
		return domain.ErrLimit
	}
	total := size
	for _, name := range []string{"registry.json", "execution.json", "recovery.json", "receipts.json", "history.json"} {
		if name == target {
			continue
		}
		var stamp unix.Stat_t
		err := unix.Fstatat(dir, name, &stamp, unix.AT_SYMLINK_NOFOLLOW)
		if errors.Is(err, unix.ENOENT) {
			continue
		}
		if err != nil || !privateRegular(stamp) {
			return xerrors.Errorf("verify sibling runtime metadata attributes: %w", domain.ErrUnsafe)
		}
		if stamp.Size < 0 || stamp.Size > s.budget-total {
			return domain.ErrLimit
		}
		total += stamp.Size
	}
	return nil
}
func (s *RegistryStore) writeMetadata(ctx context.Context, dir int, target string, data []byte, old *unix.Stat_t) error {
	return s.writeMetadataChecked(ctx, dir, target, data, old, nil)
}

// writeMetadataChecked adds an operation-specific external ownership check at
// the actual replace boundary. Ordinary writers keep their existing nil policy.
func (s *RegistryStore) writeMetadataChecked(ctx context.Context, dir int, target string, data []byte, old *unix.Stat_t, ownership func() error) error {
	if err := s.metadataBudget(dir, target, int64(len(data))); err != nil {
		return err
	}
	random := make([]byte, 16)
	if _, err := rand.Read(random); err != nil {
		return xerrors.Errorf("generate runtime metadata temporary identity: %w", domain.ErrUnsafe)
	}
	name := ".registry-" + hex.EncodeToString(random)
	fd, err := unix.Openat(dir, name, unix.O_WRONLY|unix.O_CREAT|unix.O_EXCL|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0600)
	if err != nil {
		return xerrors.Errorf("create runtime metadata temporary: %w", domain.ErrUnsafe)
	}
	f := os.NewFile(uintptr(fd), "runtime registry temporary")
	present := true
	defer func() {
		_ = f.Close()
		if present {
			_ = unix.Unlinkat(dir, name, 0)
		}
	}()
	if err = f.Chmod(0600); err != nil {
		return xerrors.Errorf("set runtime metadata temporary permissions: %w", domain.ErrUnsafe)
	}
	if _, err = f.Write(data); err != nil {
		return xerrors.Errorf("write runtime registry: %w", err)
	}
	if err = f.Sync(); err != nil {
		return xerrors.Errorf("sync runtime registry: %w", err)
	}
	if err = f.Close(); err != nil {
		return xerrors.Errorf("close runtime registry: %w", err)
	}
	if err = ctx.Err(); err != nil {
		return xerrors.Errorf("runtime registry canceled: %w", err)
	}
	if err = s.verifyRoot(dir); err != nil {
		return err
	}
	var current unix.Stat_t
	err = unix.Fstatat(dir, target, &current, unix.AT_SYMLINK_NOFOLLOW)
	if old == nil {
		if !errors.Is(err, unix.ENOENT) {
			return domain.ErrChanged
		}
	} else if err != nil || !privateRegular(current) || current.Dev != old.Dev || current.Ino != old.Ino {
		return domain.ErrChanged
	}
	if ownership != nil {
		if err = ownership(); err != nil {
			return err
		}
	}
	if err = unix.Renameat(dir, name, dir, target); err != nil {
		return xerrors.Errorf("replace runtime registry: %w", err)
	}
	present = false
	if err = unix.Fsync(dir); err != nil {
		return ErrCommitUncertain
	}
	if err = s.verifyRoot(dir); err != nil {
		return ErrCommitUncertain
	}
	if ownership != nil {
		if err = ownership(); err != nil {
			return ErrCommitUncertain
		}
	}
	return nil
}

type registryEnvelope struct {
	Version   int              `json:"version"`
	Revision  *uint64          `json:"revision"`
	Projects  []projectRecord  `json:"projects"`
	Worktrees []worktreeRecord `json:"worktrees"`
}

func encodeRegistry(r *domain.Registry) ([]byte, error) {
	state := r.Snapshot()
	doc := registryEnvelope{Version: 1, Revision: &state.Revision, Projects: make([]projectRecord, 0, len(state.Projects)), Worktrees: make([]worktreeRecord, 0, len(state.Worktrees))}
	for _, p := range state.Projects {
		doc.Projects = append(doc.Projects, projectRecord{string(p.ID()), p.Root(), p.CommonDir(), p.Witness()})
	}
	for _, w := range state.Worktrees {
		doc.Worktrees = append(doc.Worktrees, worktreeRecord{string(w.ID()), string(w.Project()), w.Path(), w.CommonDir(), w.Store(), w.Witness()})
	}
	data, err := json.Marshal(doc)
	if err != nil {
		return nil, domain.ErrInvalid
	}
	return data, nil
}
func decodeRegistry(data []byte) (*domain.Registry, error) {
	if !utf8.Valid(data) {
		return nil, domain.ErrInvalid
	}
	strict := json.NewDecoder(bytes.NewReader(data))
	if err := uniqueJSONValue(strict, 0); err != nil {
		return nil, xerrors.Errorf("load runtime registry: %w", err)
	}
	// Discriminate versions before applying v1-specific required/unknown fields.
	var header struct {
		Version *int `json:"version"`
	}
	if err := json.Unmarshal(data, &header); err != nil || header.Version == nil {
		return nil, domain.ErrInvalid
	}
	if *header.Version != 1 {
		return nil, domain.ErrUnsupported
	}

	var doc registryEnvelope
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&doc); err != nil {
		return nil, domain.ErrInvalid
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return nil, domain.ErrInvalid
	}
	if doc.Version != 1 {
		return nil, domain.ErrUnsupported
	}
	if doc.Revision == nil || doc.Projects == nil || doc.Worktrees == nil {
		return nil, domain.ErrInvalid
	}
	if len(doc.Projects) > domain.MaxProjects || len(doc.Worktrees) > domain.MaxWorktrees {
		return nil, domain.ErrLimit
	}
	state := domain.RegistryState{Revision: *doc.Revision, Projects: make([]domain.Project, 0, len(doc.Projects)), Worktrees: make([]domain.Worktree, 0, len(doc.Worktrees))}
	for _, v := range doc.Projects {
		p, err := domain.ProjectOf(v.Root, v.Common, v.Witness)
		if err != nil {
			return nil, xerrors.Errorf("load runtime registry: %w", err)
		}
		if string(p.ID()) != v.ID {
			return nil, domain.ErrInvalid
		}
		state.Projects = append(state.Projects, p)
	}
	for _, v := range doc.Worktrees {
		w, err := domain.WorktreeOf(domain.ProjectID(v.Project), v.Path, v.Common, v.Store, v.Witness)
		if err != nil {
			return nil, xerrors.Errorf("load runtime registry: %w", err)
		}
		if string(w.ID()) != v.ID {
			return nil, domain.ErrInvalid
		}
		state.Worktrees = append(state.Worktrees, w)
	}
	registry, err := domain.RegistryFrom(state)
	if err != nil {
		return nil, xerrors.Errorf("validate persisted registry: %w", err)
	}
	return registry, nil
}

func (s *RegistryStore) verifyRoot(dir int) error { return verifyRuntimeRoot(s.root, s.witness, dir) }
