package sqlite

import (
	"context"
	"database/sql"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"testing/fstest"
	"time"
)

func migrationsBeforeCodecFoundation(t *testing.T) fs.FS {
	t.Helper()
	entries, err := os.ReadDir("../../schema/sqlite/migrations")
	if err != nil {
		t.Fatal(err)
	}
	out := fstest.MapFS{}
	for _, entry := range entries {
		if entry.IsDir() || entry.Name() >= "000036" {
			continue
		}
		data, readErr := os.ReadFile(filepath.Join("../../schema/sqlite/migrations", entry.Name()))
		if readErr != nil {
			t.Fatal(readErr)
		}
		out[entry.Name()] = &fstest.MapFile{Data: data}
	}
	return out
}

func TestCodecFoundationMigrationIsSchemaOnlyUnderOneSecond(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "store.db")
	if err := NewDatabase(path, migrationsBeforeCodecFoundation(t)).initialize(ctx); err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite", sqliteDSN(path))
	if err != nil {
		t.Fatal(err)
	}
	tx, err := db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	stmt, err := tx.Prepare(`INSERT INTO events(id,kind,agent,session_id,body,created_at,client,workspace) VALUES(?,'note','a','s','body','2026-08-03T00:00:00Z','c','w')`)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 20_000; i++ {
		if _, err = stmt.Exec(fmt.Sprintf("event-%08d", i)); err != nil {
			t.Fatal(err)
		}
	}
	_ = stmt.Close()
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}
	_ = db.Close()
	all, _ := fs.Sub(os.DirFS("../.."), "schema/sqlite/migrations")
	started := time.Now()
	if err = NewDatabase(path, all).initialize(ctx); err != nil {
		t.Fatal(err)
	}
	if elapsed := time.Since(started); elapsed >= time.Second {
		t.Fatalf("codec migrations took %s, want <1s", elapsed)
	}
	db, err = sql.Open("sqlite", sqliteDSN(path))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	var mode string
	var indexes int
	if err = db.QueryRow(`SELECT mode,(SELECT COUNT(*) FROM sqlite_schema WHERE type='index' AND name LIKE '%nonidentity%') FROM payload_codec_compatibility_state`).Scan(&mode, &indexes); err != nil {
		t.Fatal(err)
	}
	if mode != "counter" || indexes != 0 {
		t.Fatalf("mode=%q indexes=%d", mode, indexes)
	}
}

func TestPayloadCodecCountersTrackCommitRollbackAndConcurrentLanes(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "store.db")
	migrations, _ := fs.Sub(os.DirFS("../.."), "schema/sqlite/migrations")
	store := NewDatabase(path, migrations)
	if err := store.initialize(ctx); err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite", sqliteDSN(path))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	//nolint:wrapcheck // Test helper returns the driver error for assertion.
	insertEvent := func(id string) error {
		_, e := db.Exec(`INSERT INTO events(id,kind,agent,session_id,body,created_at,client,workspace,body_codec) VALUES(?,'note','a','s','body','2026-08-03T00:00:00Z','c','w','zstd')`, id)
		return e
	}
	if err = insertEvent("base"); err != nil {
		t.Fatal(err)
	}
	tx, _ := db.Begin()
	if _, err = tx.Exec(`UPDATE events SET body_codec='identity' WHERE id='base'`); err != nil {
		t.Fatal(err)
	}
	if err = tx.Rollback(); err != nil {
		t.Fatal(err)
	}
	const workers = 8
	var wg sync.WaitGroup
	errs := make(chan error, workers)
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func(i int) { defer wg.Done(); errs <- insertEvent(fmt.Sprintf("concurrent-%d", i)) }(i)
	}
	wg.Wait()
	close(errs)
	for e := range errs {
		if e != nil {
			t.Fatal(e)
		}
	}
	if _, err = db.Exec(`INSERT INTO events(id,kind,agent,session_id,body,created_at,client,workspace) VALUES('audit-event','note','a','s','body','2026-08-03T00:00:00Z','c','w')`); err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(`INSERT INTO command_audits(event_id,command_text,input_text,output_text,command_codec,input_codec,output_codec) VALUES('audit-event','c','i','o','zstd','identity','zstd')`); err != nil {
		t.Fatal(err)
	}
	var body, command, input, output int
	if err = db.QueryRow(`SELECT event_body_nonidentity,audit_command_nonidentity,audit_input_nonidentity,audit_output_nonidentity FROM payload_codec_compatibility_state`).Scan(&body, &command, &input, &output); err != nil {
		t.Fatal(err)
	}
	if body != workers+1 || command != 1 || input != 0 || output != 1 {
		t.Fatalf("counters=%d/%d/%d/%d", body, command, input, output)
	}
	if _, err = db.Exec(`DELETE FROM command_audits WHERE event_id='audit-event'`); err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(`UPDATE payload_codec_compatibility_state SET event_body_nonidentity=0`); err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(`DELETE FROM events WHERE id='base'`); err == nil {
		t.Fatal("counter underflow did not fail closed")
	}
}

func TestGlobalCodecCompatibilitySelectsCounterAndLegacyShapes(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "store.db")
	migrations, _ := fs.Sub(os.DirFS("../.."), "schema/sqlite/migrations")
	if err := NewDatabase(path, migrations).initialize(ctx); err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite", sqliteDSN(path))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	found, err := globalNonIdentityPayload(ctx, db)
	if err != nil || found {
		t.Fatalf("counter identity found=%v err=%v", found, err)
	}
	if _, err = db.Exec(`UPDATE payload_codec_compatibility_state SET state='invalid'`); err != nil {
		t.Fatal(err)
	}
	if _, err = globalNonIdentityPayload(ctx, db); err == nil {
		t.Fatal("invalid state did not fail closed")
	}
	if _, err = db.Exec(`UPDATE payload_codec_compatibility_state SET state='valid',mode='legacy_index'; CREATE INDEX idx_events_nonidentity_body_codec ON events(body_codec) WHERE body_codec IS NOT NULL AND body_codec<>'identity'; CREATE INDEX idx_command_audits_nonidentity_command_codec ON command_audits(command_codec) WHERE command_codec IS NOT NULL AND command_codec<>'identity'; CREATE INDEX idx_command_audits_nonidentity_input_codec ON command_audits(input_codec) WHERE input_codec IS NOT NULL AND input_codec<>'identity'; CREATE INDEX idx_command_audits_nonidentity_output_codec ON command_audits(output_codec) WHERE output_codec IS NOT NULL AND output_codec<>'identity'`); err != nil {
		t.Fatal(err)
	}
	found, err = globalNonIdentityPayload(ctx, db)
	if err != nil || found {
		t.Fatalf("legacy identity found=%v err=%v", found, err)
	}
}
