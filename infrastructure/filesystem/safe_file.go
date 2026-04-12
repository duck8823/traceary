package filesystem

import (
	"errors"
	"io/fs"
	"os"

	"golang.org/x/xerrors"
)

// rejectSymlink returns an error if the given path exists and is a symbolic
// link. A non-existent path is treated as safe (the caller will create a
// fresh file). This refuses to follow an attacker-supplied symlink through
// user-writable hook configuration locations such as ~/.claude/settings.json
// or the installed hook scripts directory.
func rejectSymlink(path string) error {
	info, err := os.Lstat(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil
		}
		return xerrors.Errorf("failed to stat %s: %w", path, err)
	}
	if info.Mode()&fs.ModeSymlink != 0 {
		return xerrors.Errorf("refusing to operate on symbolic link: %s", path)
	}
	return nil
}
