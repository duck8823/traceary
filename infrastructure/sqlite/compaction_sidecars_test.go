package sqlite

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
)

func TestCleanupStaleSQLiteSidecars(t *testing.T) {
	tests := []struct {
		name       string
		suffix     string
		content    []byte
		makePath   func(string) error
		wantRemove bool
		wantType   string
	}{
		{name: "zero byte WAL", suffix: "-wal", wantRemove: true},
		{name: "zero byte SHM", suffix: "-shm", wantRemove: true},
		{name: "non-zero WAL", suffix: "-wal", content: []byte("live"), wantType: "regular file"},
		{name: "zero byte journal", suffix: "-journal", wantType: "regular file"},
		{name: "non-zero journal", suffix: "-journal", content: []byte("live"), wantType: "regular file"},
		{name: "symlink WAL", suffix: "-wal", makePath: func(path string) error { return os.Symlink("target", path) }, wantType: "symlink"},
		{name: "FIFO SHM", suffix: "-shm", makePath: func(path string) error { return syscall.Mkfifo(path, 0o600) }, wantType: "FIFO"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			store := filepath.Join(dir, "store.db")
			if err := os.WriteFile(store, []byte("store"), 0o600); err != nil {
				t.Fatal(err)
			}
			sidecar := store + tt.suffix
			var err error
			if tt.makePath != nil {
				err = tt.makePath(sidecar)
			} else {
				err = os.WriteFile(sidecar, tt.content, 0o600)
			}
			if err != nil {
				t.Skipf("sidecar fixture unavailable: %v", err)
			}

			err = cleanupStaleSQLiteSidecars(context.Background(), store)
			if tt.wantRemove {
				if err != nil {
					t.Fatal(err)
				}
				if _, statErr := os.Lstat(sidecar); !errors.Is(statErr, os.ErrNotExist) {
					t.Fatalf("sidecar remains: %v", statErr)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tt.wantType) || !strings.Contains(err.Error(), "stop other Traceary processes and retry") {
				t.Fatalf("error = %v, want type %q and recovery instruction", err, tt.wantType)
			}
			if _, statErr := os.Lstat(sidecar); statErr != nil {
				t.Fatalf("refused sidecar was changed: %v", statErr)
			}
		})
	}
}
