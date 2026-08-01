package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	apptypes "github.com/duck8823/traceary/application/types"
	sqliteschema "github.com/duck8823/traceary/schema/sqlite"
)

func TestMigrationPreflightRejectsLowCapWithoutTargetMutation(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	live, target := filepath.Join(dir, "live.db"), filepath.Join(dir, "target.db")
	migrations, err := sqliteschema.Migrations()
	if err != nil {
		t.Fatal(err)
	}
	legacy := fstest.MapFS{}
	paths, err := fs.Glob(migrations, "*.sql")
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range paths {
		if strings.HasPrefix(filepath.Base(path), "000037_") {
			continue
		}
		body, readErr := fs.ReadFile(migrations, path)
		if readErr != nil {
			t.Fatal(readErr)
		}
		legacy[path] = &fstest.MapFile{Data: body}
	}
	if err = NewStoreManagementDatasource(NewDatabase(live, migrations)).Initialize(ctx); err != nil {
		t.Fatal(err)
	}
	if err = NewStoreManagementDatasource(NewDatabase(target, legacy)).Initialize(ctx); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}
	adapter, err := NewPayloadRehearsalAdapter(migrations, live)
	if err != nil {
		t.Fatal(err)
	}
	frame, err := minimumWALFrameBytes(ctx, target)
	if err != nil {
		t.Fatal(err)
	}
	config := apptypes.PayloadRehearsalConfig{TargetPath: target, LivePath: live, BackupPath: filepath.Join(dir, "backup.db"), BatchRows: 2, StoredByteLimit: 1 << 20, DecodedByteLimit: 1 << 20, WallTimeLimit: time.Minute, LockTimeLimit: time.Second, ScrubByteLimit: 1 << 20, ScrubTimeLimit: time.Minute}
	config.MaxWALBytes = frame
	if _, err = adapter.Run(ctx, config, false); err == nil {
		t.Fatal("migration exceeded the cap without failing")
	}
	after, err := os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != string(before) {
		t.Fatal("low-cap migration preflight mutated the target database")
	}
	check, err := sql.Open("sqlite", immutableRehearsalDSN(target))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = check.Close() }()
	var rehearsalTable int
	if err = check.QueryRow(`SELECT EXISTS(SELECT 1 FROM sqlite_schema WHERE name='payload_rehearsal_runs')`).Scan(&rehearsalTable); err != nil {
		t.Fatal(err)
	}
	if rehearsalTable != 0 {
		t.Fatal("migration 37 was committed despite failed reservation")
	}
}

func TestLiveCompatibilityReplaysWAL(t *testing.T) {
	_, config, _ := newSwapRehearsalFixture(t)
	db, err := sql.Open("sqlite", config.LivePath)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	if _, err = db.Exec(`PRAGMA journal_mode=WAL`); err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(`PRAGMA wal_autocheckpoint=0`); err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(`UPDATE store_format_state SET minimum_reader_version=999 WHERE singleton=1`); err != nil {
		t.Fatal(err)
	}
	if _, err = inspectLiveCompatibility(context.Background(), config.LivePath); err == nil {
		t.Fatal("future reader version present only in live WAL was accepted")
	}
}

func TestPayloadRehearsalRejectsLiveDatabaseAsBackup(t *testing.T) {
	adapter, config, _ := newSwapRehearsalFixture(t)
	config.BackupPath = config.LivePath
	if _, err := adapter.Run(context.Background(), config, false); !errors.Is(err, ErrUnsafeRehearsalTarget) {
		t.Fatalf("error=%v", err)
	}
}

func TestPayloadRehearsalRejectsFutureTargetBeforeMigrationWrite(t *testing.T) {
	adapter, config, _ := newSwapRehearsalFixture(t)
	db, err := sql.Open("sqlite", config.TargetPath)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(`UPDATE store_format_state SET minimum_reader_version=999 WHERE singleton=1`); err != nil {
		t.Fatal(err)
	}
	if err = db.Close(); err != nil {
		t.Fatal(err)
	}
	before, err := fileDigest(config.TargetPath)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = adapter.Run(context.Background(), config, false); err == nil {
		t.Fatal("future target was accepted")
	}
	after, err := fileDigest(config.TargetPath)
	if err != nil {
		t.Fatal(err)
	}
	if after != before {
		t.Fatal("future target was mutated before compatibility rejection")
	}
}

func TestPayloadRehearsalRejectsBackupThroughSymlinkedAncestor(t *testing.T) {
	adapter, config, _ := newSwapRehearsalFixture(t)
	realDir := t.TempDir()
	link := filepath.Join(t.TempDir(), "linked")
	if err := os.Symlink(realDir, link); err != nil {
		t.Fatal(err)
	}
	config.BackupPath = filepath.Join(link, "backup.db")
	if _, err := adapter.Run(context.Background(), config, false); !errors.Is(err, ErrUnsafeRehearsalTarget) {
		t.Fatalf("error=%v", err)
	}
	if _, err := os.Stat(filepath.Join(realDir, "backup.db")); !os.IsNotExist(err) {
		t.Fatalf("backup was created through symlinked ancestor: %v", err)
	}
}

func TestPayloadRehearsalRejectsOversizePayloadBeforeBlobMaterialization(t *testing.T) {
	adapter, config, _ := newSwapRehearsalFixture(t)
	db, err := sql.Open("sqlite", config.TargetPath)
	if err != nil {
		t.Fatal(err)
	}
	const payloadBytes = 32 << 20
	if _, err = db.Exec(`UPDATE events SET body=zeroblob(?)`, payloadBytes); err != nil {
		t.Fatal(err)
	}
	if err = db.Close(); err != nil {
		t.Fatal(err)
	}
	config.StoredByteLimit = 1024
	config.DecodedByteLimit = 1024
	config.MaxWALBytes = 1 << 30
	runtime.GC()
	var before, after runtime.MemStats
	runtime.ReadMemStats(&before)
	_, err = adapter.Run(context.Background(), config, false)
	runtime.ReadMemStats(&after)
	if err == nil {
		t.Fatal("oversize source payload was accepted")
	}
	if allocated := after.TotalAlloc - before.TotalAlloc; allocated >= payloadBytes/2 {
		t.Fatalf("oversize payload appears materialized before cap: allocated=%d", allocated)
	}
}

func TestTerminalTransitionDoesNotCommitWithoutReservedWALFrame(t *testing.T) {
	for _, maximum := range []int64{42000, 43000, 44000, 45000} {
		t.Run(fmt.Sprint(maximum), func(t *testing.T) {
			adapter, config, _ := newSwapRehearsalFixture(t)
			metrics, err := adapter.Run(context.Background(), config, false)
			if err != nil {
				t.Fatal(err)
			}
			db, err := sql.Open("sqlite", writableRehearsalDSN(config.TargetPath, config.LockTimeLimit))
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = db.Close() }()
			const lease = "terminal-budget-lease"
			if _, err = db.Exec(`UPDATE payload_rehearsal_runs SET state='running',lease_token=? WHERE run_id=?`, lease, metrics.RunID); err != nil {
				t.Fatal(err)
			}
			frame, err := minimumWALFrameBytes(context.Background(), config.TargetPath)
			if err != nil {
				t.Fatal(err)
			}
			peak := int64(38000)
			guard := rehearsalMutationGuard(func() error { return nil })
			fingerprint, fingerprintErr := rehearsalSchemaFingerprint(context.Background(), db)
			if fingerprintErr != nil {
				t.Fatal(fingerprintErr)
			}
			session := walBudgetedMutationSession{db: db, path: config.TargetPath, expectedSchemaSHA: fingerprint, frameBytes: frame, maximum: maximum, peak: &peak}
			if err = transitionTerminalWithinWALBudget(context.Background(), session, metrics.RunID, lease, apptypes.PayloadRehearsalCompleted, guard); err == nil {
				t.Fatal("terminal transition ignored WAL reservation")
			}
			var state string
			if err = db.QueryRow(`SELECT state FROM payload_rehearsal_runs WHERE run_id=?`, metrics.RunID).Scan(&state); err != nil {
				t.Fatal(err)
			}
			if state == "completed" || state == "scrubbed" {
				t.Fatalf("terminal state persisted after WAL budget failure: %s", state)
			}
		})
	}
}

func TestPhysicalBackupRejectsCopyCorruptionAndSourceDrift(t *testing.T) {
	for _, mode := range []string{"destination", "source"} {
		t.Run(mode, func(t *testing.T) {
			_, config, _ := newSwapRehearsalFixture(t)
			destination := filepath.Join(t.TempDir(), "backup.db")
			hook := func() {
				path := destination
				if mode == "source" {
					path = config.TargetPath
				}
				file, err := os.OpenFile(path, os.O_WRONLY|os.O_APPEND, 0600)
				if err != nil {
					t.Error(err)
					return
				}
				_, _ = file.Write([]byte("corruption"))
				_ = file.Close()
			}
			if _, err := ensurePhysicalBackupWithHook(config.TargetPath, destination, hook); err == nil {
				t.Fatal("unstable/corrupt copy was verified")
			}
		})
	}
}

func TestMigrationRecoveryRejectsTamperedValidSQLiteBackup(t *testing.T) {
	adapter, config, _ := newSwapRehearsalFixture(t)
	db, err := sql.Open("sqlite", config.TargetPath)
	if err != nil {
		t.Fatal(err)
	}
	for _, statement := range []string{
		`DROP TABLE payload_rehearsal_rows`,
		`DROP TABLE payload_rehearsal_checkpoints`,
		`DROP TABLE payload_rehearsal_runs`,
		`DELETE FROM schema_migrations WHERE version=37`,
	} {
		if _, err = db.Exec(statement); err != nil {
			t.Fatal(err)
		}
	}
	if err = db.Close(); err != nil {
		t.Fatal(err)
	}
	frame, err := minimumWALFrameBytes(context.Background(), config.TargetPath)
	if err != nil {
		t.Fatal(err)
	}
	config.MaxWALBytes = frame
	adapter.beforeMigrationRecovery = func() {
		tampered := filepath.Join(t.TempDir(), "valid-but-different.db")
		migrations, migrationErr := sqliteschema.Migrations()
		if migrationErr != nil {
			t.Error(migrationErr)
			return
		}
		if migrationErr = NewStoreManagementDatasource(NewDatabase(tampered, migrations)).Initialize(context.Background()); migrationErr != nil {
			t.Error(migrationErr)
			return
		}
		if migrationErr = copyFileAtomic(tampered, config.BackupPath); migrationErr != nil {
			t.Error(migrationErr)
		}
	}
	metrics, err := adapter.Run(context.Background(), config, false)
	if err == nil {
		t.Fatal("tampered valid SQLite rollback artifact was accepted")
	}
	if metrics.RollbackVerified {
		t.Fatalf("tampered rollback artifact reported verified: %#v", metrics)
	}
}

func TestObserveWALPeakRejectsNonRegularSidecar(t *testing.T) {
	path := filepath.Join(t.TempDir(), "target.db")
	if err := os.Symlink("missing", path+"-wal"); err != nil {
		t.Fatal(err)
	}
	var peak int64
	if err := observeWALPeak(path, 0, 1<<20, &peak); err == nil {
		t.Fatal("non-regular WAL sidecar was treated as absent")
	}
}
