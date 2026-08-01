package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	apptypes "github.com/duck8823/traceary/application/types"
)

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
	peak := frame
	rejectingMaximum := 2*frame - 1
	guard := rehearsalMutationGuard(func() error { return nil })
	if err = transitionTerminalWithinWALBudget(context.Background(), db, config.TargetPath, metrics.RunID, lease, apptypes.PayloadRehearsalCompleted, frame, rejectingMaximum, &peak, guard); err == nil {
		t.Fatal("terminal transition ignored WAL reservation")
	}
	var state string
	if err = db.QueryRow(`SELECT state FROM payload_rehearsal_runs WHERE run_id=?`, metrics.RunID).Scan(&state); err != nil {
		t.Fatal(err)
	}
	if state == "completed" || state == "scrubbed" {
		t.Fatalf("terminal state persisted after WAL budget failure: %s", state)
	}
}
