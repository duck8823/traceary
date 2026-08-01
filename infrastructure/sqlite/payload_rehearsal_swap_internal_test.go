package sqlite

import (
	"context"
	"database/sql"
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
	for _, boundary := range []string{"initialize", "start-run", "batch", "lane-complete", "run-complete"} {
		t.Run(boundary, func(t *testing.T) {
			adapter, config, swap := newSwapRehearsalFixture(t)
			switch boundary {
			case "initialize":
				adapter.beforeInitialize = swap
			case "start-run":
				adapter.beforeStartRun = swap
			default:
				adapter.beforePersistence = func(kind string) {
					if kind == boundary {
						swap()
					}
				}
			}
			if _, err := adapter.Run(context.Background(), config, false); !errors.Is(err, ErrUnsafeRehearsalTarget) {
				t.Fatalf("error=%v", err)
			}
		})
	}
}

func TestPayloadRehearsalRejectsTargetSwapAtScrubWriteBoundaries(t *testing.T) {
	for _, boundary := range []string{"scrub-progress", "scrub-complete"} {
		t.Run(boundary, func(t *testing.T) {
			adapter, config, swap := newSwapRehearsalFixture(t)
			if _, err := adapter.Run(context.Background(), config, false); err != nil {
				t.Fatal(err)
			}
			adapter.beforePersistence = func(kind string) {
				if kind == boundary {
					swap()
				}
			}
			if _, err := adapter.Scrub(context.Background(), config); !errors.Is(err, ErrUnsafeRehearsalTarget) {
				t.Fatalf("error=%v", err)
			}
		})
	}
}

func newSwapRehearsalFixture(t *testing.T) (*PayloadRehearsalAdapter, apptypes.PayloadRehearsalConfig, func()) {
	t.Helper()
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
	db, err := sql.Open("sqlite", live)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(`INSERT INTO events(id,kind,agent,session_id,body,created_at) VALUES('event-1','prompt','test','session-1','body','2026-08-01T00:00:00Z')`); err != nil {
		t.Fatal(err)
	}
	if err = db.Close(); err != nil {
		t.Fatal(err)
	}
	if err = copyFileAtomic(live, target); err != nil {
		t.Fatal(err)
	}
	adapter, err := NewPayloadRehearsalAdapter(migrations, live)
	if err != nil {
		t.Fatal(err)
	}
	swapped := false
	swap := func() {
		if swapped {
			return
		}
		swapped = true
		replacement := target + ".replacement"
		original := target + ".original"
		if err := copyFileAtomic(target, replacement); err != nil {
			t.Error(err)
			return
		}
		if err := os.Rename(target, original); err != nil {
			t.Error(err)
			return
		}
		if err := os.Rename(replacement, target); err != nil {
			t.Error(err)
		}
	}
	config := apptypes.PayloadRehearsalConfig{
		TargetPath: target, LivePath: live, BackupPath: filepath.Join(dir, "backup.db"),
		BatchRows: 1, StoredByteLimit: 1 << 20, DecodedByteLimit: 1 << 20,
		WallTimeLimit: time.Minute, LockTimeLimit: time.Second,
		ScrubByteLimit: 1 << 20, ScrubTimeLimit: time.Minute, MaxWALBytes: 1 << 20,
	}
	return adapter, config, swap
}
