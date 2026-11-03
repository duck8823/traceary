//go:build unix

package runtime

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"hash/fnv"
	"io"
	"os"
	"path/filepath"
	"strings"
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
	if budget <= 0 || budget > MaxRegistryBytes {
		return nil, domain.ErrLimit
	}
	fd, err := registryDir(root, true)
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
	fd, err := registryDir(s.root, false)
	if err != nil {
		return err
	}
	defer func() { _ = unix.Close(fd) }()
	witness, err := directoryWitness(fd)
	if err != nil || witness != s.witness {
		return domain.ErrChanged
	}
	lock, err := unix.Openat(fd, "registry.lock", unix.O_RDWR|unix.O_CREAT|unix.O_NOFOLLOW|unix.O_CLOEXEC|unix.O_NONBLOCK, 0600)
	if err != nil {
		return domain.ErrUnsafe
	}
	defer func() { _ = unix.Close(lock) }()
	var initial unix.Stat_t
	if err = unix.Fstat(lock, &initial); err != nil || !privateRegular(initial) {
		return domain.ErrUnsafe
	}
	for {
		err = unix.Flock(lock, unix.LOCK_EX|unix.LOCK_NB)
		if err == nil {
			break
		}
		if !errors.Is(err, unix.EWOULDBLOCK) && !errors.Is(err, unix.EAGAIN) {
			return domain.ErrUnsafe
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

// registryDir never follows symlinks, and never repairs permissions on pre-existing paths.
func registryDir(root string, create bool) (int, error) {
	if !filepath.IsAbs(root) || filepath.Clean(root) != root || root == "/" {
		return -1, domain.ErrInvalid
	}
	fd, err := unix.Open("/", unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC, 0)
	if err != nil {
		return -1, domain.ErrUnsafe
	}
	parts := strings.Split(strings.TrimPrefix(root, "/"), "/")
	for i, part := range parts {
		next, e := unix.Openat(fd, part, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
		if errors.Is(e, unix.ENOENT) && create {
			if e = unix.Mkdirat(fd, part, 0700); e != nil && !errors.Is(e, unix.EEXIST) {
				_ = unix.Close(fd)
				return -1, domain.ErrUnsafe
			}
			next, e = unix.Openat(fd, part, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
		}
		_ = unix.Close(fd)
		if e != nil {
			return -1, domain.ErrUnsafe
		}
		fd = next
		var stat unix.Stat_t
		if e = unix.Fstat(fd, &stat); e != nil {
			_ = unix.Close(fd)
			return -1, domain.ErrUnsafe
		}
		uid := uint32(os.Geteuid())
		trusted := stat.Uid == uid || stat.Uid == 0
		writable := stat.Mode&0022 != 0
		trustedSticky := stat.Uid == 0 && stat.Mode&unix.S_ISVTX != 0
		if !trusted || writable && !trustedSticky || i == len(parts)-1 && (stat.Uid != uid || stat.Mode&0777 != 0700) {
			_ = unix.Close(fd)
			return -1, domain.ErrUnsafe
		}
	}
	return fd, nil
}
func (s *RegistryStore) read(dir int) (*domain.Registry, *unix.Stat_t, error) {
	fd, err := unix.Openat(dir, "registry.json", unix.O_RDONLY|unix.O_NOFOLLOW|unix.O_CLOEXEC|unix.O_NONBLOCK, 0)
	if errors.Is(err, unix.ENOENT) {
		return domain.NewRegistry(), nil, nil
	}
	if err != nil {
		return nil, nil, domain.ErrUnsafe
	}
	f := os.NewFile(uintptr(fd), "runtime registry")
	defer func() { _ = f.Close() }()
	var stat unix.Stat_t
	if err = unix.Fstat(fd, &stat); err != nil || !privateRegular(stat) {
		return nil, nil, domain.ErrUnsafe
	}
	if stat.Size > s.budget {
		return nil, nil, domain.ErrLimit
	}
	data, err := io.ReadAll(io.LimitReader(f, s.budget+1))
	if err != nil {
		return nil, nil, xerrors.Errorf("read runtime registry: %w", err)
	}
	if int64(len(data)) > s.budget {
		return nil, nil, domain.ErrLimit
	}
	r, err := decodeRegistry(data)
	if err != nil {
		return nil, nil, err
	}
	return r, &stat, nil
}
func (s *RegistryStore) write(ctx context.Context, dir int, data []byte, old *unix.Stat_t) error {
	random := make([]byte, 16)
	if _, err := rand.Read(random); err != nil {
		return domain.ErrUnsafe
	}
	name := ".registry-" + hex.EncodeToString(random)
	fd, err := unix.Openat(dir, name, unix.O_WRONLY|unix.O_CREAT|unix.O_EXCL|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0600)
	if err != nil {
		return domain.ErrUnsafe
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
		return domain.ErrUnsafe
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
	err = unix.Fstatat(dir, "registry.json", &current, unix.AT_SYMLINK_NOFOLLOW)
	if old == nil {
		if !errors.Is(err, unix.ENOENT) {
			return domain.ErrChanged
		}
	} else if err != nil || !privateRegular(current) || current.Dev != old.Dev || current.Ino != old.Ino {
		return domain.ErrChanged
	}
	if err = unix.Renameat(dir, name, dir, "registry.json"); err != nil {
		return xerrors.Errorf("replace runtime registry: %w", err)
	}
	present = false
	if err = unix.Fsync(dir); err != nil {
		return ErrCommitUncertain
	}
	if err = s.verifyRoot(dir); err != nil {
		return ErrCommitUncertain
	}
	return nil
}

type projectRecord struct {
	ID      string `json:"id"`
	Root    string `json:"root"`
	Common  string `json:"common"`
	Witness string `json:"witness"`
}
type worktreeRecord struct {
	ID      string `json:"id"`
	Project string `json:"project"`
	Path    string `json:"path"`
	Common  string `json:"common"`
	Store   string `json:"store"`
	Witness string `json:"witness"`
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

// uniqueJSONValue rejects duplicate object keys before decoding into a Go struct,
// whose normal JSON semantics would silently accept the last occurrence.
func uniqueJSONValue(d *json.Decoder, depth int) error {
	if depth > 8 {
		return domain.ErrInvalid
	}
	token, err := d.Token()
	if err != nil {
		return domain.ErrInvalid
	}
	delimiter, ok := token.(json.Delim)
	if !ok {
		return nil
	}
	switch delimiter {
	case '{':
		keys := make(map[string]struct{})
		for d.More() {
			key, err := d.Token()
			if err != nil {
				return domain.ErrInvalid
			}
			name, ok := key.(string)
			if !ok || name != strings.ToLower(name) {
				return domain.ErrInvalid
			}
			if _, exists := keys[name]; exists {
				return domain.ErrInvalid
			}
			keys[name] = struct{}{}
			if len(keys) > 16 {
				return domain.ErrInvalid
			}
			if err = uniqueJSONValue(d, depth+1); err != nil {
				return err
			}
		}
		end, err := d.Token()
		if err != nil || end != json.Delim('}') {
			return domain.ErrInvalid
		}
	case '[':
		count := 0
		for d.More() {
			count++
			if count > domain.MaxWorktrees+domain.MaxProjects {
				return domain.ErrLimit
			}
			if err = uniqueJSONValue(d, depth+1); err != nil {
				return err
			}
		}
		end, err := d.Token()
		if err != nil || end != json.Delim(']') {
			return domain.ErrInvalid
		}
	default:
		return domain.ErrInvalid
	}
	return nil
}

// directoryWitness derives identity from the no-follow-walked descriptor, never
// from a separate path lookup that could name a replacement directory.
func directoryWitness(fd int) (string, error) {
	var stat unix.Stat_t
	if unix.Fstat(fd, &stat) != nil || stat.Uid != uint32(os.Geteuid()) || stat.Mode&unix.S_IFMT != unix.S_IFDIR || stat.Mode&0777 != 0700 {
		return "", domain.ErrUnsafe
	}
	return fmt.Sprintf("%d:%d:%d", stat.Uid, stat.Dev, stat.Ino), nil
}
func (s *RegistryStore) verifyRoot(dir int) error {
	current, err := registryDir(s.root, false)
	if err != nil {
		return err
	}
	defer func() { _ = unix.Close(current) }()
	opened, err := directoryWitness(dir)
	if err != nil {
		return err
	}
	named, err := directoryWitness(current)
	if err != nil {
		return err
	}
	if opened != s.witness || named != s.witness {
		return domain.ErrChanged
	}
	return nil
}
