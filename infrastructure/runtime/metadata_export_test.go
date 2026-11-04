//go:build darwin || linux

package runtime

import (
	"context"
	domain "github.com/duck8823/traceary/domain/runtime"
	"golang.org/x/sys/unix"
)

// FailMetadataCommitForTest injects one object-local precommit failure or an
// actual committed-but-uncertain replacement. No production or global hook.
func FailMetadataCommitForTest(store *MetadataStore, at int, afterCommit bool) {
	original := store.commit
	calls := 0
	store.commit = func(ctx context.Context, fd int, name string, data []byte, stamp *unix.Stat_t, guard func() error) error {
		calls++
		if calls == at && !afterCommit {
			return domain.ErrUnsafe
		}
		if err := original(ctx, fd, name, data, stamp, guard); err != nil {
			return err
		}
		if calls == at {
			return ErrCommitUncertain
		}
		return nil
	}
}

// ReplaceMetadataLeaseAtCommitForTest checks the actual pre-rename guard, not
// only the pre-call lease check. Only a generated fixture object's lock changes.
func ReplaceMetadataLeaseAtCommitForTest(store *MetadataStore) {
	original := store.commit
	store.commit = func(ctx context.Context, fd int, name string, data []byte, stamp *unix.Stat_t, guard func() error) error {
		if unix.Unlinkat(fd, "daemon.lock", 0) != nil {
			return domain.ErrUnsafe
		}
		lock, err := openPrivateLock(fd, "daemon.lock")
		if err != nil {
			return domain.ErrUnsafe
		}
		_ = unix.Close(lock)
		return original(ctx, fd, name, data, stamp, guard)
	}
}

// CancelMetadataAfterFirstCommitForTest exercises cancellation after an actual
// first replacement, before the next side effect. There is no global hook.
func CancelMetadataAfterFirstCommitForTest(store *MetadataStore, cancel context.CancelFunc) {
	original := store.commit
	store.commit = func(ctx context.Context, fd int, name string, data []byte, stamp *unix.Stat_t, guard func() error) error {
		if err := original(ctx, fd, name, data, stamp, guard); err != nil {
			return err
		}
		cancel()
		return nil
	}
}
