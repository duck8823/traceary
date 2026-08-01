package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"runtime"
	"testing"
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
