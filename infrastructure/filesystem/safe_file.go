package filesystem

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"

	"golang.org/x/xerrors"
)

// systemSymlinkWhitelist lists system-level symbolic links that are not
// attacker-controllable and therefore safe to pass through when walking
// ancestor paths. macOS exposes /var → /private/var, /tmp → /private/tmp,
// and /etc → /private/etc; traversing these does not bypass any guard
// because the target is a system path that a non-privileged attacker
// cannot substitute.
var systemSymlinkWhitelist = map[string]bool{
	"/var": true,
	"/tmp": true,
	"/etc": true,
}

// rejectSymlink returns an error if the given path — or any of its
// ancestor directories — is a symbolic link outside the system
// whitelist. Non-existent components are treated as safe (they cannot
// be symlinks). This refuses attacker-supplied symlinks through
// user-writable hook configuration locations such as
// ~/.claude/settings.json or the installed hook scripts directory,
// including the case where the leaf file is fresh but a parent
// directory (e.g. .claude) is a symlink that would redirect MkdirAll /
// ReadFile / WriteFile into an unrelated tree.
func rejectSymlink(path string) error {
	current := filepath.Clean(path)
	for {
		if err := checkNotSymlink(current); err != nil {
			return err
		}
		parent := filepath.Dir(current)
		if parent == current {
			return nil
		}
		current = parent
	}
}

// checkNotSymlink returns an error when path exists and is a symbolic
// link that is not in systemSymlinkWhitelist. A non-existent path is
// treated as safe.
func checkNotSymlink(path string) error {
	info, err := os.Lstat(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil
		}
		return xerrors.Errorf("failed to stat %s: %w", path, err)
	}
	if info.Mode()&fs.ModeSymlink != 0 {
		if systemSymlinkWhitelist[path] {
			return nil
		}
		return xerrors.Errorf("refusing to operate on symbolic link: %s", path)
	}
	return nil
}
