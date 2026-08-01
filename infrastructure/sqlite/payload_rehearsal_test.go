package sqlite_test

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	_ "modernc.org/sqlite"

	apptypes "github.com/duck8823/traceary/application/types"
	infra "github.com/duck8823/traceary/infrastructure/sqlite"
	sqliteschema "github.com/duck8823/traceary/schema/sqlite"
)

func TestPayloadRehearsalPreservesCanonicalRowsAndScrubsShadowRows(t *testing.T) {
	ctx := context.Background()
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	live := filepath.Join(dir, "live.db")
	target := filepath.Join(dir, "target.db")
	backup := filepath.Join(dir, "rollback.db")
	migrations, err := sqliteschema.Migrations()
	if err != nil {
		t.Fatal(err)
	}
	if err = infra.NewStoreManagementDatasource(infra.NewDatabase(live, migrations)).Initialize(ctx); err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite", live)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(`INSERT INTO events(id,kind,agent,session_id,body,created_at) VALUES('event-1','prompt','test','session-1','plain body','2026-08-01T00:00:00Z')`); err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(`INSERT INTO command_audits(event_id,command_text,input_text,output_text) VALUES('event-1','echo','input','output')`); err != nil {
		t.Fatal(err)
	}
	if err = db.Close(); err != nil {
		t.Fatal(err)
	}
	copyTestFile(t, live, target)

	adapter := infra.NewPayloadRehearsalAdapter(migrations)
	config := rehearsalTestConfig(target, live, backup)
	preview, err := adapter.Preview(ctx, config)
	if err != nil {
		t.Fatalf("preview: %v", err)
	}
	if !preview.DryRunZeroWrite || !preview.LiveIdentityOnly {
		t.Fatalf("preview = %#v", preview)
	}
	if err = os.WriteFile(target+"-wal", []byte("unexpected"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err = adapter.Preview(ctx, config); !errors.Is(err, infra.ErrRehearsalNeedsCleanDB) {
		t.Fatalf("preview with sidecar error = %v", err)
	}
	if err = os.Remove(target + "-wal"); err != nil {
		t.Fatal(err)
	}

	result, err := adapter.Run(ctx, config, false)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if result.State != "completed" || result.EncodedRows != 4 {
		t.Fatalf("result = %#v", result)
	}
	check, err := sql.Open("sqlite", target)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = check.Close() }()
	var body string
	if err = check.QueryRow(`SELECT body FROM events WHERE id='event-1'`).Scan(&body); err != nil {
		t.Fatal(err)
	}
	if body != "plain body" {
		t.Fatalf("canonical body changed: %q", body)
	}
	var shadow int
	if err = check.QueryRow(`SELECT count(*) FROM payload_rehearsal_rows`).Scan(&shadow); err != nil {
		t.Fatal(err)
	}
	if shadow != 4 {
		t.Fatalf("shadow rows = %d", shadow)
	}
	if err = check.Close(); err != nil {
		t.Fatal(err)
	}

	check, err = sql.Open("sqlite", target)
	if err != nil {
		t.Fatal(err)
	}
	var originalShadow []byte
	var shadowRowID int64
	if err = check.QueryRow(`SELECT rowid,payload FROM payload_rehearsal_rows ORDER BY table_kind,field_kind,source_primary_key LIMIT 1`).Scan(&shadowRowID, &originalShadow); err != nil {
		t.Fatal(err)
	}
	originalShadow = append([]byte(nil), originalShadow...)
	if _, err = check.Exec(`UPDATE payload_rehearsal_rows SET payload=x'00' WHERE rowid=?`, shadowRowID); err != nil {
		t.Fatal(err)
	}
	if err = check.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err = adapter.Scrub(ctx, config); err == nil {
		t.Fatal("corrupt shadow payload was accepted")
	}
	check, err = sql.Open("sqlite", target)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = check.Exec(`UPDATE payload_rehearsal_rows SET payload=? WHERE rowid=?`, originalShadow, shadowRowID); err != nil {
		t.Fatal(err)
	}
	if err = check.Close(); err != nil {
		t.Fatal(err)
	}
	scrub, err := adapter.Scrub(ctx, config)
	if err != nil {
		t.Fatalf("scrub: %v", err)
	}
	if scrub.State != "scrubbed" {
		t.Fatalf("scrub = %#v", scrub)
	}
	check, err = sql.Open("sqlite", target)
	if err != nil {
		t.Fatal(err)
	}
	var scrubbedRows int
	if err = check.QueryRow(`SELECT sum(scrubbed_rows) FROM payload_rehearsal_checkpoints`).Scan(&scrubbedRows); err != nil {
		t.Fatal(err)
	}
	if err = check.Close(); err != nil {
		t.Fatal(err)
	}
	if scrubbedRows != 4 {
		t.Fatalf("persisted scrub rows = %d", scrubbedRows)
	}
	rolledBack, err := adapter.Rollback(ctx, config)
	if err != nil {
		t.Fatalf("rollback: %v", err)
	}
	if !rolledBack.RollbackVerified {
		t.Fatalf("rollback = %#v", rolledBack)
	}
	check, err = sql.Open("sqlite", target)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = check.Close() }()
	if err = check.QueryRow(`SELECT count(*) FROM payload_rehearsal_rows`).Scan(&shadow); err != nil {
		t.Fatal(err)
	}
	if shadow != 0 {
		t.Fatalf("rollback retained %d shadow rows", shadow)
	}
}

func TestPayloadRehearsalResumesAfterArbitraryCommittedBatch(t *testing.T) {
	ctx := context.Background()
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	live, target, backup := filepath.Join(dir, "live.db"), filepath.Join(dir, "target.db"), filepath.Join(dir, "backup.db")
	migrations, err := sqliteschema.Migrations()
	if err != nil {
		t.Fatal(err)
	}
	if err = infra.NewStoreManagementDatasource(infra.NewDatabase(live, migrations)).Initialize(ctx); err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite", live)
	if err != nil {
		t.Fatal(err)
	}
	tx, err := db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 200; i++ {
		id := fmt.Sprintf("event-%04d", i)
		if _, err = tx.Exec(`INSERT INTO events(id,kind,agent,session_id,body,created_at) VALUES(?,'prompt','test','session',?,'2026-08-01T00:00:00Z')`, id, strings.Repeat("payload", 32)); err != nil {
			t.Fatal(err)
		}
	}
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}
	if err = db.Close(); err != nil {
		t.Fatal(err)
	}
	copyTestFile(t, live, target)
	adapter := infra.NewPayloadRehearsalAdapter(migrations)
	config := rehearsalTestConfig(target, live, backup)
	config.BatchRows = 1
	config.WallTimeLimit = time.Minute
	runCtx, cancel := context.WithCancel(ctx)
	done := make(chan error, 1)
	go func() { _, runErr := adapter.Run(runCtx, config, false); done <- runErr }()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if _, statErr := os.Stat(backup); statErr == nil {
			break
		}
		time.Sleep(time.Millisecond)
	}
	for time.Now().Before(deadline) {
		probe, openErr := sql.Open("sqlite", target)
		if openErr == nil {
			var n int
			queryErr := probe.QueryRow(`SELECT count(*) FROM payload_rehearsal_rows`).Scan(&n)
			_ = probe.Close()
			if queryErr == nil && n > 0 {
				cancel()
				break
			}
		}
		time.Sleep(time.Millisecond)
	}
	cancel()
	if err = <-done; !errors.Is(err, context.Canceled) {
		t.Fatalf("interrupted run error = %v", err)
	}
	frozen, err := sql.Open("sqlite", target)
	if err != nil {
		t.Fatal(err)
	}
	_, freezeErr := frozen.Exec(`INSERT INTO events(id,kind,agent,session_id,body,created_at) VALUES('blocked','prompt','test','session','body','2026-08-01T00:00:00Z')`)
	_ = frozen.Close()
	if freezeErr == nil {
		t.Fatal("paused rehearsal did not freeze canonical inserts")
	}
	resumed, err := adapter.Run(ctx, config, true)
	if err != nil {
		t.Fatalf("resume: %v", err)
	}
	if resumed.State != "completed" {
		t.Fatalf("resume state = %s", resumed.State)
	}
	check, err := sql.Open("sqlite", target)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = check.Close() }()
	var count, distinct int
	if err = check.QueryRow(`SELECT count(*),count(DISTINCT table_kind||':'||field_kind||':'||source_primary_key) FROM payload_rehearsal_rows`).Scan(&count, &distinct); err != nil {
		t.Fatal(err)
	}
	if count != 200 || distinct != count {
		t.Fatalf("shadow count/distinct = %d/%d", count, distinct)
	}
	if _, err = check.Exec(`INSERT INTO events(id,kind,agent,session_id,body,created_at) VALUES('after-complete','prompt','test','session','body','2026-08-01T00:00:00Z')`); err != nil {
		t.Fatalf("terminal rehearsal kept freeze active: %v", err)
	}
}

func TestPayloadRehearsalRejectsLiveAliases(t *testing.T) {
	dir := t.TempDir()
	live := filepath.Join(dir, "live.db")
	if err := os.WriteFile(live, []byte("not sqlite"), 0600); err != nil {
		t.Fatal(err)
	}
	adapter := infra.NewPayloadRehearsalAdapter(nil)
	for _, tc := range []struct {
		name, target string
		prepare      func() error
	}{
		{"same path", live, func() error { return nil }},
		{"symlink", filepath.Join(dir, "link.db"), func() error { return os.Symlink(live, filepath.Join(dir, "link.db")) }},
		{"hardlink", filepath.Join(dir, "hard.db"), func() error { return os.Link(live, filepath.Join(dir, "hard.db")) }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := tc.prepare(); err != nil {
				t.Fatal(err)
			}
			c := rehearsalTestConfig(tc.target, live, filepath.Join(dir, "backup-"+tc.name))
			_, err := adapter.Preview(context.Background(), c)
			if !errors.Is(err, infra.ErrUnsafeRehearsalTarget) {
				t.Fatalf("error = %v", err)
			}
		})
	}
}

func TestPayloadRehearsalAppliesCommonCompatibilityGateToLiveAndTarget(t *testing.T) {
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
	for _, path := range []string{live, target} {
		if err = infra.NewStoreManagementDatasource(infra.NewDatabase(path, migrations)).Initialize(ctx); err != nil {
			t.Fatal(err)
		}
	}
	adapter := infra.NewPayloadRehearsalAdapter(migrations)
	config := rehearsalTestConfig(target, live, filepath.Join(dir, "backup.db"))
	mutate := func(t *testing.T, path, statement string) {
		t.Helper()
		db, openErr := sql.Open("sqlite", path)
		if openErr != nil {
			t.Fatal(openErr)
		}
		if _, execErr := db.Exec(statement); execErr != nil {
			_ = db.Close()
			t.Fatal(execErr)
		}
		if closeErr := db.Close(); closeErr != nil {
			t.Fatal(closeErr)
		}
	}
	for _, tc := range []struct{ name, path, invalid, restore string }{
		{"live future reader", live, `UPDATE store_format_state SET minimum_reader_version=999`, `UPDATE store_format_state SET minimum_reader_version=34`},
		{"live future format", live, `UPDATE store_format_state SET maximum_payload_format=999`, `UPDATE store_format_state SET maximum_payload_format=1`},
		{"live missing state", live, `DELETE FROM store_format_state`, `INSERT INTO store_format_state VALUES(1,34,1)`},
		{"live invalid state", live, `UPDATE store_format_state SET minimum_reader_version=-1`, `UPDATE store_format_state SET minimum_reader_version=34`},
		{"target future reader", target, `UPDATE store_format_state SET minimum_reader_version=999`, `UPDATE store_format_state SET minimum_reader_version=34`},
		{"target future format", target, `UPDATE store_format_state SET maximum_payload_format=999`, `UPDATE store_format_state SET maximum_payload_format=1`},
		{"target missing state", target, `DELETE FROM store_format_state`, `INSERT INTO store_format_state VALUES(1,34,1)`},
		{"target invalid state", target, `UPDATE store_format_state SET maximum_payload_format=-1`, `UPDATE store_format_state SET maximum_payload_format=1`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			mutate(t, tc.path, tc.invalid)
			if _, previewErr := adapter.Preview(ctx, config); previewErr == nil {
				t.Fatal("incompatible store was accepted")
			}
			mutate(t, tc.path, tc.restore)
		})
	}

	legacy := filepath.Join(dir, "legacy-live.db")
	legacyDB, err := sql.Open("sqlite", legacy)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = legacyDB.Exec(`CREATE TABLE events(id TEXT PRIMARY KEY,body TEXT NOT NULL); CREATE TABLE command_audits(event_id TEXT PRIMARY KEY,command_text TEXT NOT NULL,input_text TEXT NOT NULL,output_text TEXT NOT NULL)`); err != nil {
		t.Fatal(err)
	}
	if err = legacyDB.Close(); err != nil {
		t.Fatal(err)
	}
	legacyConfig := config
	legacyConfig.LivePath = legacy
	if _, err = adapter.Preview(ctx, legacyConfig); err != nil {
		t.Fatalf("legacy live compatibility: %v", err)
	}
}

func rehearsalTestConfig(target, live, backup string) apptypes.PayloadRehearsalConfig {
	return apptypes.PayloadRehearsalConfig{TargetPath: target, LivePath: live, BackupPath: backup, BatchRows: 2, StoredByteLimit: 1 << 20, DecodedByteLimit: 1 << 20, WallTimeLimit: time.Minute, LockTimeLimit: time.Second, ScrubByteLimit: 1 << 20, ScrubTimeLimit: time.Minute}
}
func copyTestFile(t *testing.T, source, dest string) {
	t.Helper()
	b, err := os.ReadFile(source)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(dest, b, 0600); err != nil {
		t.Fatal(err)
	}
}
