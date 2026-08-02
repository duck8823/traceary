//go:build darwin || linux

package sqlite

import (
	"context"
	"errors"
	"fmt"
	"os"
	"syscall"
	"time"
)

type advisoryLease interface{ Close() error }

type unixAdvisoryLease struct{ file *os.File }

func acquireAdvisoryLease(ctx context.Context, path string, exclusive bool) (advisoryLease, error) {
	fd, err := syscall.Open(path, syscall.O_CREAT|syscall.O_RDWR|syscall.O_CLOEXEC|syscall.O_NOFOLLOW, 0o600)
	if err != nil {
		return nil, fmt.Errorf("open store lease: %w", err)
	}
	file := os.NewFile(uintptr(fd), path)
	var stat syscall.Stat_t
	if err := syscall.Fstat(fd, &stat); err != nil || stat.Mode&syscall.S_IFMT != syscall.S_IFREG {
		_ = file.Close()
		if err != nil {
			return nil, fmt.Errorf("inspect store lease: %w", err)
		}
		return nil, errors.New("store lease is not a regular file")
	}
	mode := syscall.LOCK_SH | syscall.LOCK_NB
	if exclusive {
		mode = syscall.LOCK_EX | syscall.LOCK_NB
	}
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	for {
		err = syscall.Flock(fd, mode)
		if err == nil {
			return &unixAdvisoryLease{file: file}, nil
		}
		if !errors.Is(err, syscall.EWOULDBLOCK) && !errors.Is(err, syscall.EAGAIN) {
			_ = file.Close()
			return nil, fmt.Errorf("acquire store lease: %w", err)
		}
		select {
		case <-ctx.Done():
			_ = file.Close()
			return nil, fmt.Errorf("wait for store lease: %w", ctx.Err())
		case <-ticker.C:
		}
	}
}

func (l *unixAdvisoryLease) Close() error {
	unlockErr := syscall.Flock(int(l.file.Fd()), syscall.LOCK_UN)
	closeErr := l.file.Close()
	if unlockErr != nil {
		return fmt.Errorf("unlock store lease: %w", unlockErr)
	}
	if closeErr != nil {
		return fmt.Errorf("close store lease: %w", closeErr)
	}
	return nil
}
