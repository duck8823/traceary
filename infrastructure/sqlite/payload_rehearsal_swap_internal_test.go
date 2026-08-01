package sqlite

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	apptypes "github.com/duck8823/traceary/application/types"
	sqliteschema "github.com/duck8823/traceary/schema/sqlite"
)

func TestPayloadRehearsalRejectsTargetSwapAtWriteBoundaries(t *testing.T) {
	if _, err := NewPayloadRehearsalAdapter(nil, ""); err == nil {
		t.Fatal("empty configured live path was accepted")
	}
	for _, boundary := range []string{"initialize", "start-run"} {
		t.Run(boundary, func(t *testing.T) {
			ctx := context.Background()
			dir, err := filepath.EvalSymlinks(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			live, target := filepath.Join(dir, "live.db"), filepath.Join(dir, "target.db")
			migrations, err := sqliteschema.Migrations()
			if err != nil {
				t.Fatal(err)
			}
			if err = NewStoreManagementDatasource(NewDatabase(live, migrations)).Initialize(ctx); err != nil {
				t.Fatal(err)
			}
			bytes, err := os.ReadFile(live)
			if err != nil {
				t.Fatal(err)
			}
			if err = os.WriteFile(target, bytes, 0600); err != nil {
				t.Fatal(err)
			}
			adapter, constructorErr := NewPayloadRehearsalAdapter(migrations, live)
			if constructorErr != nil {
				t.Fatal(constructorErr)
			}
			swap := func() {
				replacement := target + ".replacement"
				original := target + ".original"
				if copyErr := copyFileAtomic(target, replacement); copyErr != nil {
					t.Error(copyErr)
					return
				}
				if renameErr := os.Rename(target, original); renameErr != nil {
					t.Error(renameErr)
					return
				}
				if renameErr := os.Rename(replacement, target); renameErr != nil {
					t.Error(renameErr)
				}
			}
			if boundary == "initialize" {
				adapter.beforeInitialize = swap
			} else {
				adapter.beforeStartRun = swap
			}
			config := apptypes.PayloadRehearsalConfig{TargetPath: target, LivePath: live, BackupPath: filepath.Join(dir, "backup.db"), BatchRows: 1, StoredByteLimit: 1 << 20, DecodedByteLimit: 1 << 20, WallTimeLimit: time.Minute, LockTimeLimit: time.Second, ScrubByteLimit: 1 << 20, ScrubTimeLimit: time.Minute, MaxWALBytes: 1 << 20}
			if _, err = adapter.Run(ctx, config, false); !errors.Is(err, ErrUnsafeRehearsalTarget) {
				t.Fatalf("error=%v", err)
			}
		})
	}
}
