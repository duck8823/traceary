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
		sidecars   []sidecarFixture
		wantRemove bool
		wantType   string
	}{
		{name: "zero byte WAL", sidecars: []sidecarFixture{{suffix: "-wal"}}, wantRemove: true},
		{name: "SHM alone", sidecars: []sidecarFixture{{suffix: "-shm"}}, wantRemove: true},
		{name: "zero byte WAL beside 32 KB SHM", sidecars: []sidecarFixture{{suffix: "-wal"}, {suffix: "-shm", content: make([]byte, 32*1024)}}, wantRemove: true},
		{name: "non-zero WAL", sidecars: []sidecarFixture{{suffix: "-wal", content: []byte("live")}}, wantType: "regular file"},
		{name: "zero byte journal", sidecars: []sidecarFixture{{suffix: "-journal"}}, wantType: "regular file"},
		{name: "non-zero journal", sidecars: []sidecarFixture{{suffix: "-journal", content: []byte("live")}}, wantType: "regular file"},
		{name: "symlink WAL", sidecars: []sidecarFixture{{suffix: "-wal", makePath: func(path string) error { return os.Symlink("target", path) }}}, wantType: "symlink"},
		{name: "FIFO SHM", sidecars: []sidecarFixture{{suffix: "-shm", makePath: func(path string) error { return syscall.Mkfifo(path, 0o600) }}}, wantType: "FIFO"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			store := filepath.Join(dir, "store.db")
			if err := os.WriteFile(store, []byte("store"), 0o600); err != nil {
				t.Fatal(err)
			}
			for _, fixture := range tt.sidecars {
				sidecar := store + fixture.suffix
				var err error
				if fixture.makePath != nil {
					err = fixture.makePath(sidecar)
				} else {
					err = os.WriteFile(sidecar, fixture.content, 0o600)
				}
				if err != nil {
					t.Skipf("sidecar fixture unavailable: %v", err)
				}
			}

			err := cleanupStaleSQLiteSidecars(context.Background(), store)
			if tt.wantRemove {
				if err != nil {
					t.Fatal(err)
				}
				for _, fixture := range tt.sidecars {
					if _, statErr := os.Lstat(store + fixture.suffix); !errors.Is(statErr, os.ErrNotExist) {
						t.Fatalf("sidecar remains: %v", statErr)
					}
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tt.wantType) || !strings.Contains(err.Error(), "stop other Traceary processes and retry") {
				t.Fatalf("error = %v, want type %q and recovery instruction", err, tt.wantType)
			}
			if _, statErr := os.Lstat(store + tt.sidecars[0].suffix); statErr != nil {
				t.Fatalf("refused sidecar was changed: %v", statErr)
			}
		})
	}
}

type sidecarFixture struct {
	suffix   string
	content  []byte
	makePath func(string) error
}
