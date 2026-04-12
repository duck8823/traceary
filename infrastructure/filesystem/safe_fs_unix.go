//go:build unix

package filesystem

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"syscall"

	"golang.org/x/sys/unix"
	"golang.org/x/xerrors"
)

// descendToDir opens the directory at absPath using an fd-pinned walk.
// Each intermediate component is opened with O_NOFOLLOW so an attacker
// who races to substitute an ancestor for a symbolic link is rejected
// with ELOOP. System-whitelisted aliases (macOS /var, /tmp, /etc) are
// traversed without O_NOFOLLOW. The returned file descriptor must be
// closed by the caller.
func descendToDir(absPath string) (int, error) {
	if !filepath.IsAbs(absPath) {
		return -1, xerrors.Errorf("path must be absolute: %s", absPath)
	}
	cleaned := filepath.Clean(absPath)

	currentFD, err := unix.Open("/", unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC, 0)
	if err != nil {
		return -1, xerrors.Errorf("failed to open root: %w", err)
	}
	if cleaned == "/" {
		return currentFD, nil
	}

	parts := strings.Split(strings.TrimPrefix(cleaned, "/"), "/")
	currentPath := "/"
	for _, part := range parts {
		if part == "" {
			continue
		}
		nextPath := filepath.Join(currentPath, part)
		flag := unix.O_RDONLY | unix.O_DIRECTORY | unix.O_CLOEXEC
		if !systemSymlinkWhitelist[nextPath] {
			flag |= unix.O_NOFOLLOW
		}
		nextFD, err := unix.Openat(currentFD, part, flag, 0)
		_ = unix.Close(currentFD)
		if err != nil {
			if errors.Is(err, unix.ELOOP) || errors.Is(err, unix.EMLINK) {
				return -1, xerrors.Errorf("refusing to descend into symbolic link: %s", nextPath)
			}
			return -1, xerrors.Errorf("failed to open %s: %w", nextPath, err)
		}
		currentFD = nextFD
		currentPath = nextPath
	}
	return currentFD, nil
}

// safeMkdirAll creates the directory tree at absPath, rejecting any
// attacker-supplied symbolic link encountered along the way. Unlike
// os.MkdirAll, each intermediate directory is opened with O_NOFOLLOW so
// a concurrent attacker cannot redirect the traversal between the check
// and the subsequent file operations.
func safeMkdirAll(absPath string, perm os.FileMode) error {
	if !filepath.IsAbs(absPath) {
		return xerrors.Errorf("path must be absolute: %s", absPath)
	}
	cleaned := filepath.Clean(absPath)
	if cleaned == "/" {
		return nil
	}

	currentFD, err := unix.Open("/", unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC, 0)
	if err != nil {
		return xerrors.Errorf("failed to open root: %w", err)
	}
	defer func() {
		if currentFD >= 0 {
			_ = unix.Close(currentFD)
		}
	}()

	parts := strings.Split(strings.TrimPrefix(cleaned, "/"), "/")
	currentPath := "/"
	for _, part := range parts {
		if part == "" {
			continue
		}
		nextPath := filepath.Join(currentPath, part)

		if err := unix.Mkdirat(currentFD, part, uint32(perm.Perm())); err != nil && !errors.Is(err, unix.EEXIST) {
			return xerrors.Errorf("failed to mkdir %s: %w", nextPath, err)
		}

		flag := unix.O_RDONLY | unix.O_DIRECTORY | unix.O_CLOEXEC
		if !systemSymlinkWhitelist[nextPath] {
			flag |= unix.O_NOFOLLOW
		}
		nextFD, err := unix.Openat(currentFD, part, flag, 0)
		_ = unix.Close(currentFD)
		currentFD = -1
		if err != nil {
			if errors.Is(err, unix.ELOOP) || errors.Is(err, unix.EMLINK) {
				return xerrors.Errorf("refusing to descend into symbolic link: %s", nextPath)
			}
			return xerrors.Errorf("failed to open %s: %w", nextPath, err)
		}
		currentFD = nextFD
		currentPath = nextPath
	}
	return nil
}

// safeReadFile reads the file at absPath using an fd-pinned walk that
// rejects symbolic links anywhere in the path (including the leaf).
// Returns fs.ErrNotExist when the file is missing so callers can detect
// a fresh-install case without a separate Lstat.
func safeReadFile(absPath string) ([]byte, error) {
	cleaned := filepath.Clean(absPath)
	dir := filepath.Dir(cleaned)
	base := filepath.Base(cleaned)

	parentFD, err := descendToDir(dir)
	if err != nil {
		return nil, err
	}
	defer func() { _ = unix.Close(parentFD) }()

	fd, err := unix.Openat(parentFD, base, unix.O_RDONLY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		if errors.Is(err, unix.ENOENT) {
			return nil, &os.PathError{Op: "open", Path: absPath, Err: syscall.ENOENT}
		}
		if errors.Is(err, unix.ELOOP) || errors.Is(err, unix.EMLINK) {
			return nil, xerrors.Errorf("refusing to open symbolic link: %s", absPath)
		}
		return nil, xerrors.Errorf("failed to open %s: %w", absPath, err)
	}

	f := os.NewFile(uintptr(fd), absPath)
	defer func() { _ = f.Close() }()

	data, err := io.ReadAll(f)
	if err != nil {
		return nil, xerrors.Errorf("failed to read %s: %w", absPath, err)
	}
	return data, nil
}

// safeWriteFile writes data to absPath using an fd-pinned walk that
// rejects symbolic links anywhere in the path. The parent directory
// must already exist (call safeMkdirAll first). Write errors and
// delayed errors surfaced by Close are both propagated to the caller.
func safeWriteFile(absPath string, data []byte, perm os.FileMode) (retErr error) {
	cleaned := filepath.Clean(absPath)
	dir := filepath.Dir(cleaned)
	base := filepath.Base(cleaned)

	parentFD, err := descendToDir(dir)
	if err != nil {
		return err
	}
	defer func() { _ = unix.Close(parentFD) }()

	fd, err := unix.Openat(
		parentFD,
		base,
		unix.O_WRONLY|unix.O_CREAT|unix.O_TRUNC|unix.O_NOFOLLOW|unix.O_CLOEXEC,
		uint32(perm.Perm()),
	)
	if err != nil {
		if errors.Is(err, unix.ELOOP) || errors.Is(err, unix.EMLINK) {
			return xerrors.Errorf("refusing to open symbolic link: %s", absPath)
		}
		return xerrors.Errorf("failed to open %s: %w", absPath, err)
	}

	f := os.NewFile(uintptr(fd), absPath)
	defer func() {
		closeErr := f.Close()
		if retErr == nil && closeErr != nil {
			retErr = xerrors.Errorf("failed to close %s: %w", absPath, closeErr)
		}
	}()

	if _, err := f.Write(data); err != nil {
		return xerrors.Errorf("failed to write %s: %w", absPath, err)
	}
	return nil
}

// safeChmod changes the permission bits of absPath using an fd-pinned
// walk that rejects symbolic links anywhere in the path.
func safeChmod(absPath string, perm os.FileMode) error {
	cleaned := filepath.Clean(absPath)
	dir := filepath.Dir(cleaned)
	base := filepath.Base(cleaned)

	parentFD, err := descendToDir(dir)
	if err != nil {
		return err
	}
	defer func() { _ = unix.Close(parentFD) }()

	fd, err := unix.Openat(parentFD, base, unix.O_RDONLY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		if errors.Is(err, unix.ELOOP) || errors.Is(err, unix.EMLINK) {
			return xerrors.Errorf("refusing to open symbolic link: %s", absPath)
		}
		return xerrors.Errorf("failed to open %s: %w", absPath, err)
	}
	defer func() { _ = unix.Close(fd) }()

	if err := unix.Fchmod(fd, uint32(perm.Perm())); err != nil {
		return xerrors.Errorf("failed to chmod %s: %w", absPath, err)
	}
	return nil
}
