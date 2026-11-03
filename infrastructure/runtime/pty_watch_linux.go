//go:build linux

package runtime

import (
	"context"
	"errors"
	domain "github.com/duck8823/traceary/domain/runtime"
	"golang.org/x/sys/unix"
	"os/signal"
	"runtime"
	"syscall"
	"unsafe"
)

func checkReapPolicy(ctx context.Context) error {
	if signal.Ignored(syscall.SIGCHLD) {
		return domain.ErrUnsafe
	}
	// These Linux ABIs place handler then flags in native-width words and use an
	// 8-byte kernel sigset. MIPS has a different layout/mask; do not guess it.
	switch runtime.GOARCH {
	case "amd64", "arm64", "386", "arm", "ppc64", "ppc64le", "s390x", "riscv64", "loong64":
	default:
		return domain.ErrUnsupported
	}
	var old [8]uintptr
	for {
		if ctx.Err() != nil {
			return domain.ErrUnsafe
		}
		_, _, err := unix.RawSyscall6(unix.SYS_RT_SIGACTION, uintptr(syscall.SIGCHLD), 0, uintptr(unsafe.Pointer(&old)), 8, 0, 0)
		runtime.KeepAlive(&old)
		if errors.Is(err, unix.EINTR) {
			continue
		}
		// Linux generic SA_NOCLDWAIT=2; SIG_IGN=1. No new action or addresses exposed.
		if err != 0 || old[0] == 1 || old[1]&2 != 0 {
			return domain.ErrUnsafe
		}
		return nil
	}
}

type childExitWatch struct {
	fd         int
	initialErr error
}

func prepareExitWatch(context.Context) (*childExitWatch, error) { return &childExitWatch{fd: -1}, nil }
func (w *childExitWatch) bind(ctx context.Context, pid int) error {
	for {
		if ctx.Err() != nil {
			w.initialErr = domain.ErrUnsafe
			return w.initialErr
		}
		fd, err := unix.PidfdOpen(pid, 0)
		if errors.Is(err, unix.EINTR) {
			continue
		}
		if errors.Is(err, unix.ESRCH) {
			w.initialErr = domain.ErrChanged
			return w.initialErr
		}
		if err != nil {
			w.initialErr = domain.ErrUnsafe
			return w.initialErr
		}
		w.fd = fd
		return nil
	}
}
func (w *childExitWatch) wait() error {
	if w.initialErr != nil {
		return w.initialErr
	}
	polls := []unix.PollFd{{Fd: int32(w.fd), Events: unix.POLLIN}}
	for {
		n, err := unix.Poll(polls, -1)
		if errors.Is(err, unix.EINTR) {
			continue
		}
		if err != nil || polls[0].Revents&(unix.POLLERR|unix.POLLNVAL) != 0 {
			return domain.ErrUnsafe
		}
		if n > 0 && polls[0].Revents&(unix.POLLIN|unix.POLLHUP) != 0 {
			return nil
		}
	}
}
func (w *childExitWatch) close() {
	if w.fd >= 0 {
		_ = unix.Close(w.fd)
		w.fd = -1
	}
}
