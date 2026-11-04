//go:build darwin || linux

package runtime

import "golang.org/x/sys/unix"

func validHistoryHintRoot(root string) bool {
	if !validRuntimeRoot(root) {
		return false
	}
	fd, err := privateRuntimeDir(root, false)
	if err != nil {
		return false
	}
	_ = unix.Close(fd)
	return true
}
