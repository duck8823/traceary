package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/duck8823/traceary/application"
	apptypes "github.com/duck8823/traceary/application/types"
	"github.com/duck8823/traceary/application/usecase"
	sqliteschema "github.com/duck8823/traceary/schema/sqlite"
)

type liveDriftAfterPreview struct {
	application.PayloadRehearsalPreview
	live string
}

//nolint:wrapcheck // test fault injector preserves the underlying failure.
func (p liveDriftAfterPreview) Preview(ctx context.Context, c apptypes.PayloadRehearsalConfig) (apptypes.PayloadRehearsalMetrics, error) {
	m, err := p.PayloadRehearsalPreview.Preview(ctx, c)
	if err != nil {
		return m, err
	}
	db, openErr := sql.Open("sqlite", p.live)
	if openErr != nil {
		return m, openErr
	}
	_, driftErr := db.Exec(`UPDATE events SET body_codec='zstd' WHERE id=(SELECT min(id) FROM events)`)
	_ = db.Close()
	return m, driftErr
}

func TestPayloadRehearsalRealSQLiteRejectsLiveDriftAfterPreviewBeforeBackup(t *testing.T) {
	adapter, config, _ := newSwapRehearsalFixture(t)
	u := usecase.NewPayloadRehearsalUsecase(liveDriftAfterPreview{PayloadRehearsalPreview: adapter, live: config.LivePath}, adapter, adapter, adapter)
	if _, err := u.Run(context.Background(), config); !errors.Is(err, ErrLivePayloadNotIdentityOnly) {
		t.Fatalf("error = %v", err)
	}
	if _, err := os.Stat(config.BackupPath); !os.IsNotExist(err) {
		t.Fatalf("backup created after live drift: %v", err)
	}
}

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
			if _, err := adapter.Run(context.Background(), config, apptypes.PayloadRehearsalRunCommand{Mode: apptypes.PayloadRehearsalStart}); !errors.Is(err, ErrUnsafeRehearsalTarget) {
				t.Fatalf("error=%v", err)
			}
		})
	}
}

func TestPayloadRehearsalRejectsTargetSwapAtScrubWriteBoundaries(t *testing.T) {
	for _, boundary := range []string{"scrub-progress", "scrub-complete"} {
		t.Run(boundary, func(t *testing.T) {
			adapter, config, swap := newSwapRehearsalFixture(t)
			if _, err := adapter.Run(context.Background(), config, apptypes.PayloadRehearsalRunCommand{Mode: apptypes.PayloadRehearsalStart}); err != nil {
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

func TestPayloadRehearsalFreezesVerifiedShadowRowsAcrossScrubTransactions(t *testing.T) {
	adapter, config, _ := newSwapRehearsalFixture(t)
	if _, err := adapter.Run(context.Background(), config, apptypes.PayloadRehearsalRunCommand{Mode: apptypes.PayloadRehearsalStart}); err != nil {
		t.Fatal(err)
	}
	var mutationErr error
	adapter.beforePersistence = func(kind string) {
		if kind != "scrub-progress" || mutationErr != nil {
			return
		}
		db, err := sql.Open("sqlite", writableRehearsalDSN(config.TargetPath, time.Second))
		if err != nil {
			mutationErr = err
			return
		}
		_, mutationErr = db.Exec(`UPDATE payload_rehearsal_rows SET payload=x'00' WHERE rowid=(SELECT min(rowid) FROM payload_rehearsal_rows)`)
		_ = db.Close()
	}
	result, err := adapter.Scrub(context.Background(), config)
	if err != nil {
		t.Fatalf("scrub after rejected mutation: %v", err)
	}
	if mutationErr == nil {
		t.Fatal("same-inode shadow mutation between scrub read and checkpoint was accepted")
	}
	if result.State != "scrubbed" || !result.ActivationReadiness.ScrubPassed {
		t.Fatalf("result = %#v", result)
	}
}

func TestPayloadRehearsalRejectsTargetSwapBeforeWallTimePause(t *testing.T) {
	adapter, config, swap := newSwapRehearsalFixture(t)
	config.WallTimeLimit = time.Nanosecond
	adapter.beforePersistence = func(kind string) {
		if kind == "pause" {
			swap()
		}
	}
	if _, err := adapter.Run(context.Background(), config, apptypes.PayloadRehearsalRunCommand{Mode: apptypes.PayloadRehearsalStart}); !errors.Is(err, ErrUnsafeRehearsalTarget) {
		t.Fatalf("error=%v", err)
	}
}

func TestRehearsalRecoveryMutationsDoNotWriteWhenGuardFails(t *testing.T) {
	adapter, config, _ := newSwapRehearsalFixture(t)
	metrics, err := adapter.Run(context.Background(), config, apptypes.PayloadRehearsalRunCommand{Mode: apptypes.PayloadRehearsalStart})
	if err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite", writableRehearsalDSN(config.TargetPath, time.Second))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	const lease = "retained-lease"
	if _, err = db.Exec(`UPDATE payload_rehearsal_runs SET state='running',lease_token=? WHERE run_id=?`, lease, metrics.RunID); err != nil {
		t.Fatal(err)
	}
	reject := rehearsalMutationGuard(func() error { return ErrUnsafeRehearsalTarget })
	frame, frameErr := minimumWALFrameBytes(context.Background(), config.TargetPath)
	if frameErr != nil {
		t.Fatal(frameErr)
	}
	fingerprint, fingerprintErr := rehearsalSchemaFingerprint(context.Background(), db)
	if fingerprintErr != nil {
		t.Fatal(fingerprintErr)
	}
	peak := frame
	session := walBudgetedMutationSession{db: db, path: config.TargetPath, expectedSchemaSHA: fingerprint, frameBytes: frame, maximum: config.MaxWALBytes, peak: &peak, lockLimit: time.Second}
	if err = pauseRunState(context.Background(), session, metrics.RunID, lease, reject); !errors.Is(err, ErrUnsafeRehearsalTarget) {
		t.Fatalf("pause error=%v", err)
	}
	_ = releaseScrubLease(context.Background(), session, metrics.RunID, lease, reject)
	var state, retained string
	if err = db.QueryRow(`SELECT state,coalesce(lease_token,'') FROM payload_rehearsal_runs WHERE run_id=?`, metrics.RunID).Scan(&state, &retained); err != nil {
		t.Fatal(err)
	}
	if state != "running" || retained != lease {
		t.Fatalf("guarded recovery mutated state=%q lease=%q", state, retained)
	}
}

func TestPayloadRehearsalRollbackRejectsTargetSwapBeforeRename(t *testing.T) {
	adapter, config, swap := newSwapRehearsalFixture(t)
	config.MaxWALBytes = 16 << 20
	db, err := sql.Open("sqlite", config.TargetPath)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(`UPDATE events SET body=zeroblob(1048576)`); err != nil {
		t.Fatal(err)
	}
	if err = db.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err = adapter.Run(context.Background(), config, apptypes.PayloadRehearsalRunCommand{Mode: apptypes.PayloadRehearsalStart}); err != nil {
		t.Fatal(err)
	}
	adapter.duringRollbackHeavyVerify = swap
	if _, err = adapter.Rollback(context.Background(), config); !errors.Is(err, ErrUnsafeRehearsalTarget) {
		t.Fatalf("rollback error=%v", err)
	}
}

func TestAtomicCopyRejectsTemporaryPathSubstitution(t *testing.T) {
	dir := t.TempDir()
	source := filepath.Join(dir, "source")
	destination := filepath.Join(dir, "destination")
	if err := os.WriteFile(source, []byte("source-data"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(destination, []byte("destination-data"), 0o600); err != nil {
		t.Fatal(err)
	}
	hook := func() error {
		matches, err := filepath.Glob(filepath.Join(dir, ".traceary-atomic-copy-*.tmp"))
		if err != nil || len(matches) != 1 {
			return errors.New("atomic temporary file not found")
		}
		if err = os.Rename(matches[0], matches[0]+".stolen"); err != nil {
			return fmt.Errorf("replace atomic temporary path: %w", err)
		}
		if err = os.Symlink(source, matches[0]); err != nil {
			return fmt.Errorf("substitute atomic temporary path: %w", err)
		}
		return nil
	}
	if err := copyFileAtomicWithHook(source, destination, hook); !errors.Is(err, ErrUnsafeRehearsalTarget) {
		t.Fatalf("copy error=%v", err)
	}
	contents, err := os.ReadFile(destination)
	if err != nil {
		t.Fatal(err)
	}
	if string(contents) != "destination-data" {
		t.Fatalf("destination changed: %q", contents)
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
