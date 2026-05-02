package usecase

import (
	"os"
	"path/filepath"

	"golang.org/x/xerrors"
)

// activationFileWriter abstracts the filesystem operations the
// memory-activation usecase needs to safely inspect, read, and write
// a single managed file. Implementations must enforce the v0.12 Codex
// activation safety contract independently for every file they touch:
// reject symlinks and directories, preserve existing permissions,
// create parent directories with restrictive permissions, write
// through a temp file in the same directory, sync, and rename
// atomically. The interface is intentionally narrow so the same
// contract can be reused for the v0.13 host-context stub file and the
// external memory file in later issues.
type activationFileWriter interface {
	// Inspect lstats the path and returns its FileInfo. The boolean is
	// true when the path exists. An error is returned for unsafe
	// targets (symlinks, directories) or when stat itself fails.
	// Status callers use Inspect to surface "invalid" before reading
	// or writing.
	Inspect(path string) (info os.FileInfo, exists bool, err error)
	// ReadIfExists returns the existing content. exists=false when the
	// file does not exist. Other errors (including symlink rejection)
	// are returned as-is.
	ReadIfExists(path string) (content string, exists bool, err error)
	// WriteAtomic writes the content via a temp file in the same
	// directory, then renames atomically. Existing file permissions
	// are preserved; new files are created with 0o600. Parent
	// directories are created with 0o700 when missing. Symlink and
	// directory targets are rejected with the same error wording the
	// v0.12 Codex activation reported.
	WriteAtomic(path string, content string) error
}

// osActivationFileWriter is the default activationFileWriter backed by
// the local filesystem via the `os` package. It is the only writer the
// activation usecase wires up in v0.13.0-2; downstream issues can swap
// in a stricter writer (for example one that uses the fd-pinned safe
// FS helpers in `infrastructure/filesystem`) without touching
// memoryActivationUsecase.
type osActivationFileWriter struct{}

// Inspect reports the file state. Symlinks and directories are
// rejected so callers cannot accidentally rewrite arbitrary files
// reached through a managed path.
func (osActivationFileWriter) Inspect(path string) (os.FileInfo, bool, error) {
	info, err := os.Lstat(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, false, nil
		}
		return nil, false, xerrors.Errorf("failed to stat activation target %s: %w", path, err)
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return nil, true, xerrors.Errorf("activation target symlinks are not supported: %s", path)
	}
	if info.IsDir() {
		return nil, true, xerrors.Errorf("activation target is a directory: %s", path)
	}
	return info, true, nil
}

// ReadIfExists reads the file content, returning exists=false when the
// file does not exist. It first runs Inspect so read-only plan/status
// paths follow the same symlink and directory refusal contract as
// WriteAtomic.
func (w osActivationFileWriter) ReadIfExists(path string) (string, bool, error) {
	if _, exists, err := w.Inspect(path); err != nil || !exists {
		return "", exists, err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return "", false, nil
		}
		return "", false, xerrors.Errorf("failed to read activation target %s: %w", path, err)
	}
	return string(data), true, nil
}

// WriteAtomic creates the parent directory with 0o700 if missing,
// writes content to a temp file in the same directory, syncs, and
// renames atomically. Existing files keep their permissions; brand-new
// files are created with 0o600. Symlink and directory targets are
// rejected before any write occurs so a malicious symlink target
// cannot be replaced with attacker-controlled content.
func (w osActivationFileWriter) WriteAtomic(path string, content string) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return xerrors.Errorf("failed to create activation target directory %s: %w", dir, err)
	}
	perm := os.FileMode(0o600)
	info, exists, err := w.Inspect(path)
	if err != nil {
		return err
	}
	if exists {
		if mode := info.Mode().Perm(); mode != 0 {
			perm = mode
		}
	}
	tmp, err := os.CreateTemp(dir, ".traceary-"+filepath.Base(path)+".*.tmp")
	if err != nil {
		return xerrors.Errorf("failed to create temporary activation target in %s: %w", dir, err)
	}
	tmpPath := tmp.Name()
	cleanup := true
	defer func() {
		if cleanup {
			_ = os.Remove(tmpPath)
		}
	}()
	if err := tmp.Chmod(perm); err != nil {
		_ = tmp.Close()
		return xerrors.Errorf("failed to chmod temporary activation target %s: %w", tmpPath, err)
	}
	if _, err := tmp.WriteString(content); err != nil {
		_ = tmp.Close()
		return xerrors.Errorf("failed to write temporary activation target %s: %w", tmpPath, err)
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return xerrors.Errorf("failed to sync temporary activation target %s: %w", tmpPath, err)
	}
	if err := tmp.Close(); err != nil {
		return xerrors.Errorf("failed to close temporary activation target %s: %w", tmpPath, err)
	}
	if err := os.Rename(tmpPath, path); err != nil {
		return xerrors.Errorf("failed to replace activation target %s: %w", path, err)
	}
	cleanup = false
	return nil
}
