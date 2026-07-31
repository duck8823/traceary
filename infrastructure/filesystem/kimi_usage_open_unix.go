//go:build unix

package filesystem

import (
	"os"

	"golang.org/x/sys/unix"
)

// kimiUsageOpenFlags adds O_NONBLOCK so a hostile FIFO cannot block the
// contained open before fstat, plus O_CLOEXEC so the descriptor never leaks
// into child processes.
const kimiUsageOpenFlags = os.O_RDONLY | unix.O_NONBLOCK | unix.O_CLOEXEC
