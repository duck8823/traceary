package sqlite

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
)

type sqliteSidecar struct {
	path string
}

// cleanupStaleSQLiteSidecars requires the caller to declare that it holds the
// exclusive store lease for the entire call. That declaration licenses
// recovery because every cooperating physical SQLite connection holds the
// shared form of that lease, so a reader or writer cannot keep a sidecar live
// while this cleanup runs. Non-cooperating SQLite clients remain outside that
// advisory-lock boundary, just as they are for the existing replacement
// protocol.
func cleanupStaleSQLiteSidecars(ctx context.Context, storePath string) error {
	var stale []sqliteSidecar
	for _, suffix := range []string{"-wal", "-shm", "-journal"} {
		if err := ctx.Err(); err != nil {
			return fmt.Errorf("sidecar cleanup canceled: %w", err)
		}
		path := storePath + suffix
		info, err := os.Lstat(path)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return fmt.Errorf("stat SQLite sidecar %s: %w", path, err)
		}
		if suffix == "-journal" || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
			return sqliteSidecarRefusal(path, info)
		}
		if suffix == "-wal" && info.Size() != 0 {
			return sqliteSidecarRefusal(path, info)
		}
		// The shm file contains no database content and is never fsynced. When
		// the WAL's mxFrame is zero, the WAL is empty and all content comes from
		// the main database file, so a regular shm is stale regardless of size.
		if suffix == "-wal" || suffix == "-shm" {
			stale = append(stale, sqliteSidecar{path: path})
		}
	}
	for _, sidecar := range stale {
		if err := ctx.Err(); err != nil {
			return fmt.Errorf("sidecar cleanup canceled: %w", err)
		}
		if err := os.Remove(sidecar.path); err != nil {
			return fmt.Errorf("remove stale SQLite sidecar %s: %w", sidecar.path, err)
		}
	}
	if len(stale) > 0 {
		if err := syncDirectory(filepath.Dir(storePath)); err != nil {
			return fmt.Errorf("sync SQLite sidecar cleanup directory: %w", err)
		}
	}
	return rejectSQLiteSidecars(storePath)
}

func sqliteSidecarRefusal(path string, info os.FileInfo) error {
	return fmt.Errorf("SQLite sidecar %s is %s with size %d bytes; stop other Traceary processes and retry", path, sqliteSidecarType(info), info.Size())
}

func sqliteSidecarType(info os.FileInfo) string {
	mode := info.Mode()
	switch {
	case mode.IsRegular():
		return "a regular file"
	case mode&os.ModeSymlink != 0:
		return "a symlink"
	case mode&os.ModeNamedPipe != 0:
		return "a FIFO"
	case mode.IsDir():
		return "a directory"
	default:
		return fmt.Sprintf("a %s", mode.Type().String())
	}
}
