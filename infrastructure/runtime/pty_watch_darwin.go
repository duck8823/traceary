//go:build darwin

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

// This is Darwin's public old-action ABI (usigactiont), NOT the larger private
// new-action ABI with trampoline. newact is always null: this never changes policy.
func checkReapPolicy(ctx context.Context) error {
	if signal.Ignored(syscall.SIGCHLD) {
		return domain.ErrUnsafe
	}
	var old struct {
		handler uintptr
		mask    uint32
		flags   int32
	}
	for {
		if ctx.Err() != nil {
			return domain.ErrUnsafe
		}
		//nolint:staticcheck // SA1019: pure-Go read-only sigaction has no public libSystem wrapper; verified old-action ABI, null newact, fail closed on syscall failure.
		_, _, err := unix.Syscall6(unix.SYS_SIGACTION, uintptr(syscall.SIGCHLD), 0, uintptr(unsafe.Pointer(&old)), 0, 0, 0)
		runtime.KeepAlive(&old)
		if errors.Is(err, unix.EINTR) {
			continue
		}
		// SA_NOCLDWAIT=0x20 in Darwin sys/signal.h; SIG_IGN=1. Never publish addresses.
		if err != 0 || old.handler == 1 || old.flags&0x20 != 0 {
			return domain.ErrUnsafe
		}
		return nil
	}
}

type childExitWatch struct {
	fd         int
	pid        uint64
	initialErr error
}

func prepareExitWatch(ctx context.Context) (*childExitWatch, error) {
	for {
		if ctx.Err() != nil {
			return nil, domain.ErrUnsafe
		}
		syscall.ForkLock.RLock()
		fd, err := unix.Kqueue()
		if err == nil {
			unix.CloseOnExec(fd)
		}
		syscall.ForkLock.RUnlock()
		if errors.Is(err, unix.EINTR) {
			continue
		}
		if err != nil {
			return nil, domain.ErrUnsafe
		}
		return &childExitWatch{fd: fd}, nil
	}
}
func (w *childExitWatch) bind(ctx context.Context, pid int) error {
	w.pid = uint64(pid)
	changes := []unix.Kevent_t{{Ident: w.pid, Filter: unix.EVFILT_PROC, Flags: unix.EV_ADD | unix.EV_ONESHOT | unix.EV_RECEIPT, Fflags: unix.NOTE_EXIT}}
	events := make([]unix.Kevent_t, 1)
	zero := unix.Timespec{}
	for {
		if ctx.Err() != nil {
			w.initialErr = domain.ErrUnsafe
			return w.initialErr
		}
		n, err := unix.Kevent(w.fd, changes, events, &zero)
		if errors.Is(err, unix.EINTR) {
			continue
		}
		if err != nil || n != 1 || events[0].Flags&unix.EV_ERROR == 0 {
			w.initialErr = domain.ErrUnsafe
			return w.initialErr
		}
		if events[0].Data == int64(unix.ESRCH) {
			w.initialErr = domain.ErrChanged
			return w.initialErr
		}
		if events[0].Data != 0 {
			w.initialErr = domain.ErrUnsafe
			return w.initialErr
		}
		return nil
	}
}
func (w *childExitWatch) wait() error {
	if w.initialErr != nil {
		return w.initialErr
	}
	events := make([]unix.Kevent_t, 1)
	for {
		n, err := unix.Kevent(w.fd, nil, events, nil)
		if errors.Is(err, unix.EINTR) {
			continue
		}
		if err != nil {
			return domain.ErrUnsafe
		}
		if n == 0 {
			continue
		}
		if n != 1 || events[0].Flags&unix.EV_ERROR != 0 || events[0].Ident != w.pid || events[0].Filter != unix.EVFILT_PROC || events[0].Fflags&unix.NOTE_EXIT == 0 {
			return domain.ErrUnsafe
		}
		return nil
	}
}
func (w *childExitWatch) close() {
	if w.fd >= 0 {
		_ = unix.Close(w.fd)
		w.fd = -1
	}
}
