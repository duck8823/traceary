//go:build darwin || linux

package sqlite

import (
	"errors"
	"io"
	"math"
	"os"
	"strconv"
	"strings"

	apptypes "github.com/duck8823/traceary/application/types"
	"golang.org/x/sys/unix"
)

// consultHistoryMaintenanceMarker never follows a symlink, blocks on a FIFO,
// reads an unbounded file, or deletes a stale marker owned by existing maintenance.
// Unverifiable markers mean busy; a dead marker PID is not native-run authority.
func consultHistoryMaintenanceMarker(storePath string) error {
	pending := &apptypes.StoreMaintenancePendingError{StorePath: storePath}
	path, err := CompactPendingMarkerPath(storePath)
	if err != nil {
		return pending
	}
	fd, err := unix.Open(path, unix.O_RDONLY|unix.O_CLOEXEC|unix.O_NOFOLLOW|unix.O_NONBLOCK, 0)
	if errors.Is(err, unix.ENOENT) {
		return nil
	}
	if err != nil {
		return pending
	}
	file := os.NewFile(uintptr(fd), "history maintenance marker")
	defer func() { _ = file.Close() }()
	var before, after, named unix.Stat_t
	if unix.Fstat(fd, &before) != nil || before.Mode&unix.S_IFMT != unix.S_IFREG || before.Uid != uint32(os.Geteuid()) || before.Nlink != 1 || before.Mode&0022 != 0 || before.Size <= 0 || before.Size > 64 {
		return pending
	}
	raw, err := io.ReadAll(io.LimitReader(file, 65))
	if err != nil || int64(len(raw)) != before.Size || unix.Fstat(fd, &after) != nil || unix.Lstat(path, &named) != nil {
		return pending
	}
	if !sameHistoryMarker(before, after) || !sameHistoryMarker(before, named) {
		return pending
	}
	text := strings.TrimSpace(string(raw))
	pid, err := strconv.Atoi(text)
	if err != nil || pid <= 0 || pid > math.MaxInt32 || strconv.Itoa(pid) != text {
		return pending
	}
	// Signal zero inspects only this bounded maintenance marker. Never signal,
	// adopt or decide native session exclusivity from any saved runtime PID.
	if err = unix.Kill(pid, 0); errors.Is(err, unix.ESRCH) {
		return nil // Read-only: legacy maintenance retains marker cleanup ownership.
	}
	return pending
}

func sameHistoryMarker(a, b unix.Stat_t) bool {
	return a.Dev == b.Dev && a.Ino == b.Ino && a.Mode == b.Mode && a.Uid == b.Uid && a.Nlink == b.Nlink && a.Size == b.Size && a.Ctim == b.Ctim && a.Mtim == b.Mtim
}
