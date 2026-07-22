//go:build unix && !darwin && !linux

package filesystem

import "errors"

func renameFileRetentionNoReplace(_ int, _, _ string) error {
	return errors.New("atomic no-replace rename is unsupported on this platform")
}
