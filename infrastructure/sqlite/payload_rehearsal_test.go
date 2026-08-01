package sqlite_test

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io/fs"
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

	adapter := newRehearsalAdapter(t, migrations, live)
	config := rehearsalTestConfig(target, live, backup)
	preview, err := adapter.Preview(ctx, config)
	if err != nil {
		t.Fatalf("preview: %v", err)
	}
	if !preview.DryRunZeroWrite || !preview.LiveIdentityOnly {
		t.Fatalf("preview = %#v", preview)
	}
	if preview.ActivationReadiness.ActivationAllowed {
		t.Fatal("v0.34 preview allowed activation")
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
	runAliasConfig := config
	runAliasConfig.BackupPath = target
	if _, err = adapter.Run(ctx, runAliasConfig, false); !errors.Is(err, infra.ErrUnsafeRehearsalTarget) {
		t.Fatalf("run backup alias error=%v", err)
	}

	result, err := adapter.Run(ctx, config, false)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if result.State != "completed" || result.EncodedRows != 4 {
		t.Fatalf("result = %#v", result)
	}
	if !result.ActivationReadiness.RehearsalComplete || result.ActivationReadiness.ActivationAllowed {
		t.Fatalf("completion readiness = %#v", result.ActivationReadiness)
	}
	for _, tc := range []struct{ invalid, restore string }{
		{`UPDATE store_format_state SET minimum_reader_version=999`, `UPDATE store_format_state SET minimum_reader_version=34`},
		{`UPDATE store_format_state SET maximum_payload_format=999`, `UPDATE store_format_state SET maximum_payload_format=1`},
		{`DELETE FROM store_format_state`, `INSERT INTO store_format_state VALUES(1,34,1)`},
		{`UPDATE store_format_state SET minimum_reader_version=-1`, `UPDATE store_format_state SET minimum_reader_version=34`},
	} {
		db, openErr := sql.Open("sqlite", target)
		if openErr != nil {
			t.Fatal(openErr)
		}
		if _, execErr := db.Exec(tc.invalid); execErr != nil {
			t.Fatal(execErr)
		}
		_ = db.Close()
		if _, rollbackErr := adapter.Rollback(ctx, config); rollbackErr == nil {
			t.Fatalf("rollback accepted incompatible current target: %s", tc.invalid)
		}
		db, openErr = sql.Open("sqlite", target)
		if openErr != nil {
			t.Fatal(openErr)
		}
		if _, execErr := db.Exec(tc.restore); execErr != nil {
			t.Fatal(execErr)
		}
		_ = db.Close()
	}
	legacyCurrent, openErr := sql.Open("sqlite", target)
	if openErr != nil {
		t.Fatal(openErr)
	}
	if _, execErr := legacyCurrent.Exec(`DROP TABLE store_format_state`); execErr != nil {
		t.Fatal(execErr)
	}
	_ = legacyCurrent.Close()
	if recovered, rollbackErr := adapter.Rollback(ctx, config); rollbackErr != nil || !recovered.RollbackVerified || recovered.ActivationReadiness.ScrubPassed || recovered.ActivationReadiness.ScrubStatus != apptypes.ReadinessUnknown {
		t.Fatalf("rollback from completed recovery state: %#v %v", recovered, rollbackErr)
	}
	result, err = adapter.Run(ctx, config, false)
	if err != nil || result.State != "completed" {
		t.Fatalf("rerun after recovery rollback: %#v %v", result, err)
	}
	check, err := sql.Open("sqlite", target)
	if err != nil {
		t.Fatal(err)
	}
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
	var runID string
	if err = check.QueryRow(`SELECT run_id FROM payload_rehearsal_runs LIMIT 1`).Scan(&runID); err != nil {
		t.Fatal(err)
	}
	invalidRow := `INSERT INTO payload_rehearsal_rows(run_id,table_kind,field_kind,source_primary_key,source_sha256,payload,codec,format_version,plaintext_bytes,stored_bytes,payload_sha256) VALUES(?,?,?,?,?,x'00','zstd',1,0,1,?)`
	if _, err = check.Exec(invalidRow, runID, "unknown", "body", "x", "00", "00"); err == nil {
		t.Fatal("unknown rehearsal lane was accepted")
	}
	if _, err = check.Exec(invalidRow, runID, "events", "output", "x", "00", "00"); err == nil {
		t.Fatal("inconsistent rehearsal lane was accepted")
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
	check, err = sql.Open("sqlite", target)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = check.Exec(`UPDATE payload_rehearsal_checkpoints SET changed_rows=changed_rows+1 WHERE rowid=(SELECT min(rowid) FROM payload_rehearsal_checkpoints)`); err != nil {
		t.Fatal(err)
	}
	_ = check.Close()
	if _, err = adapter.Scrub(ctx, config); err == nil {
		t.Fatal("checkpoint cardinality drift was accepted")
	}
	check, err = sql.Open("sqlite", target)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = check.Exec(`UPDATE payload_rehearsal_checkpoints SET changed_rows=changed_rows-1 WHERE rowid=(SELECT min(rowid) FROM payload_rehearsal_checkpoints)`); err != nil {
		t.Fatal(err)
	}
	_ = check.Close()
	scrub, err := adapter.Scrub(ctx, config)
	if err != nil {
		t.Fatalf("scrub: %v", err)
	}
	if scrub.State != "scrubbed" {
		t.Fatalf("scrub = %#v", scrub)
	}
	if !scrub.ActivationReadiness.ScrubPassed || scrub.ActivationReadiness.ActivationAllowed {
		t.Fatalf("scrub readiness = %#v", scrub.ActivationReadiness)
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
	backupBytes, err := os.ReadFile(backup)
	if err != nil {
		t.Fatal(err)
	}
	targetBytes, err := os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(backup, targetBytes, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err = adapter.Rollback(ctx, config); err == nil {
		t.Fatal("swapped rollback artifact was accepted")
	}
	if err = os.WriteFile(backup, backupBytes, 0600); err != nil {
		t.Fatal(err)
	}
	aliasConfig := config
	aliasConfig.BackupPath = target
	if _, err = adapter.Rollback(ctx, aliasConfig); !errors.Is(err, infra.ErrUnsafeRehearsalTarget) {
		t.Fatalf("target backup alias error = %v", err)
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
	adapter := newRehearsalAdapter(t, migrations, live)
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
	time.Sleep(50 * time.Millisecond)
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
	originalPath := target + ".original"
	replacement := target + ".replacement"
	copyTestFile(t, target, replacement)
	if err = os.Rename(target, originalPath); err != nil {
		t.Fatal(err)
	}
	if err = os.Rename(replacement, target); err != nil {
		t.Fatal(err)
	}
	if _, err = adapter.Run(ctx, config, true); err == nil {
		t.Fatal("replacement inode resumed")
	}
	if err = os.Remove(target); err != nil {
		t.Fatal(err)
	}
	if err = os.Rename(originalPath, target); err != nil {
		t.Fatal(err)
	}
	keeper, err := sql.Open("sqlite", target)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = keeper.Exec(`PRAGMA journal_mode=WAL`); err != nil {
		t.Fatal(err)
	}
	var sidecarProbe int
	if err = keeper.QueryRow(`SELECT count(*) FROM payload_rehearsal_rows`).Scan(&sidecarProbe); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = keeper.Close() }()
	type resumeResult struct {
		metrics apptypes.PayloadRehearsalMetrics
		err     error
	}
	results := make(chan resumeResult, 2)
	start := make(chan struct{})
	for i := 0; i < 2; i++ {
		go func() { <-start; m, e := adapter.Run(ctx, config, true); results <- resumeResult{m, e} }()
	}
	close(start)
	first, second := <-results, <-results
	successes := 0
	var resumed apptypes.PayloadRehearsalMetrics
	for _, r := range []resumeResult{first, second} {
		if r.err == nil {
			successes++
			resumed = r.metrics
		}
	}
	if successes != 1 {
		t.Fatalf("concurrent resume successes=%d errors=%v/%v", successes, first.err, second.err)
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
	if _, err = check.Exec(`INSERT INTO events(id,kind,agent,session_id,body,created_at) VALUES('blocked-complete','prompt','test','session','body','2026-08-01T00:00:00Z')`); err == nil {
		t.Fatal("completed rehearsal released canonical freeze before scrub")
	}
	if err = check.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err = adapter.Scrub(ctx, config); err != nil {
		t.Fatalf("final scrub: %v", err)
	}
	check, err = sql.Open("sqlite", target)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = check.Close() }()
	if _, err = check.Exec(`INSERT INTO events(id,kind,agent,session_id,body,created_at) VALUES('after-scrub','prompt','test','session','body','2026-08-01T00:00:00Z')`); err != nil {
		t.Fatalf("scrubbed rehearsal kept freeze active: %v", err)
	}
}

func TestPayloadRehearsalCommitsBoundedPrefixBeforeOversizeRow(t *testing.T) {
	ctx := context.Background()
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	live, target, backup := filepath.Join(dir, "live.db"), filepath.Join(dir, "target.db"), filepath.Join(dir, "backup.db")
	migrations, _ := sqliteschema.Migrations()
	if err = infra.NewStoreManagementDatasource(infra.NewDatabase(live, migrations)).Initialize(ctx); err != nil {
		t.Fatal(err)
	}
	db, _ := sql.Open("sqlite", live)
	_, err = db.Exec(`INSERT INTO events(id,kind,agent,session_id,body,created_at) VALUES('a','prompt','t','s','12345','2026-08-01T00:00:00Z'),('b','prompt','t','s','12345678901234567890','2026-08-01T00:00:00Z')`)
	if err != nil {
		t.Fatal(err)
	}
	_ = db.Close()
	copyTestFile(t, live, target)
	adapter := newRehearsalAdapter(t, migrations, live)
	config := rehearsalTestConfig(target, live, backup)
	config.StoredByteLimit = 10
	if _, err = adapter.Run(ctx, config, false); err == nil {
		t.Fatal("oversize row did not pause")
	}
	check, _ := sql.Open("sqlite", target)
	defer func() { _ = check.Close() }()
	var rows int
	if err = check.QueryRow(`SELECT count(*) FROM payload_rehearsal_rows`).Scan(&rows); err != nil {
		t.Fatal(err)
	}
	if rows != 1 {
		t.Fatalf("bounded prefix rows=%d", rows)
	}
}

func TestPayloadRehearsalBindsClaimedLiveToConfiguredCanonicalStore(t *testing.T) {
	ctx := context.Background()
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	actual, decoy := filepath.Join(dir, "actual.db"), filepath.Join(dir, "decoy.db")
	migrations, _ := sqliteschema.Migrations()
	for _, p := range []string{actual, decoy} {
		if err = infra.NewStoreManagementDatasource(infra.NewDatabase(p, migrations)).Initialize(ctx); err != nil {
			t.Fatal(err)
		}
	}
	adapter := newRehearsalAdapter(t, migrations, actual)
	config := rehearsalTestConfig(actual, decoy, filepath.Join(dir, "backup.db"))
	if _, err = adapter.Run(ctx, config, false); !errors.Is(err, infra.ErrUnsafeRehearsalTarget) {
		t.Fatalf("decoy live binding error=%v", err)
	}
}

func TestPayloadRehearsalEnforcesWALHardCap(t *testing.T) {
	ctx := context.Background()
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	live, target, backup := filepath.Join(dir, "live.db"), filepath.Join(dir, "target.db"), filepath.Join(dir, "backup.db")
	migrations, _ := sqliteschema.Migrations()
	if err = infra.NewStoreManagementDatasource(infra.NewDatabase(live, migrations)).Initialize(ctx); err != nil {
		t.Fatal(err)
	}
	db, _ := sql.Open("sqlite", live)
	_, err = db.Exec(`INSERT INTO events(id,kind,agent,session_id,body,created_at) VALUES('a','prompt','t','s','payload','2026-08-01T00:00:00Z')`)
	if err != nil {
		t.Fatal(err)
	}
	_ = db.Close()
	copyTestFile(t, live, target)
	adapter := newRehearsalAdapter(t, migrations, live)
	config := rehearsalTestConfig(target, live, backup)
	config.MaxWALBytes = 1
	result, err := adapter.Run(ctx, config, false)
	if err == nil {
		t.Fatal("WAL hard cap was not enforced")
	}
	if result.PeakWALBytes <= config.MaxWALBytes {
		t.Fatalf("peak WAL=%d", result.PeakWALBytes)
	}
	if result.RollbackDigest == "" || !result.RollbackVerified {
		t.Fatalf("rollback artifact was not prepared before WAL preflight: %#v", result)
	}
}

func TestPayloadRehearsalScrubPersistsCappedPrefixAndLeasesWorkers(t *testing.T) {
	ctx := context.Background()
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	live, target, backup := filepath.Join(dir, "live.db"), filepath.Join(dir, "target.db"), filepath.Join(dir, "backup.db")
	migrations, _ := sqliteschema.Migrations()
	if err = infra.NewStoreManagementDatasource(infra.NewDatabase(live, migrations)).Initialize(ctx); err != nil {
		t.Fatal(err)
	}
	db, _ := sql.Open("sqlite", live)
	for i, body := range []string{"abcdefghijklmnopqrstuvwxyz0123456789AAAA", "abcdefghijklmnopqrstuvwxyz0123456789BBBB"} {
		if _, err = db.Exec(`INSERT INTO events(id,kind,agent,session_id,body,created_at) VALUES(?,'prompt','t','s',?,'2026-08-01T00:00:00Z')`, fmt.Sprintf("e%d", i), body); err != nil {
			t.Fatal(err)
		}
	}
	_ = db.Close()
	copyTestFile(t, live, target)
	adapter := newRehearsalAdapter(t, migrations, live)
	config := rehearsalTestConfig(target, live, backup)
	if _, err = adapter.Run(ctx, config, false); err != nil {
		t.Fatal(err)
	}
	check, _ := sql.Open("sqlite", target)
	var first, second int64
	if err = check.QueryRow(`SELECT stored_bytes FROM payload_rehearsal_rows ORDER BY source_primary_key LIMIT 1`).Scan(&first); err != nil {
		t.Fatal(err)
	}
	if err = check.QueryRow(`SELECT stored_bytes FROM payload_rehearsal_rows ORDER BY source_primary_key LIMIT 1 OFFSET 1`).Scan(&second); err != nil {
		t.Fatal(err)
	}
	_ = check.Close()
	config.ScrubByteLimit = max(first, second) + 1
	check, _ = sql.Open("sqlite", target)
	if _, err = check.Exec(`UPDATE payload_rehearsal_checkpoints SET scrub_last_primary_key='zzzz',scrubbed_rows=changed_rows WHERE table_kind='events' AND field_kind='body'`); err != nil {
		t.Fatal(err)
	}
	_ = check.Close()
	if _, err = adapter.Scrub(ctx, config); err == nil {
		t.Fatal("ahead scrub checkpoint skipped unverified rows")
	}
	check, _ = sql.Open("sqlite", target)
	if _, err = check.Exec(`UPDATE payload_rehearsal_checkpoints SET scrub_last_primary_key='',scrubbed_rows=0 WHERE table_kind='events' AND field_kind='body'`); err != nil {
		t.Fatal(err)
	}
	_ = check.Close()
	if _, err = adapter.Scrub(ctx, config); err == nil {
		t.Fatal("scrub cap did not pause")
	}
	check, _ = sql.Open("sqlite", target)
	var last string
	var scrubbed int
	if err = check.QueryRow(`SELECT scrub_last_primary_key,scrubbed_rows FROM payload_rehearsal_checkpoints WHERE table_kind='events' AND field_kind='body'`).Scan(&last, &scrubbed); err != nil {
		t.Fatal(err)
	}
	_ = check.Close()
	if last == "" || scrubbed != 1 {
		t.Fatalf("scrub progress last=%q rows=%d", last, scrubbed)
	}
	type result struct{ err error }
	results := make(chan result, 2)
	start := make(chan struct{})
	for i := 0; i < 2; i++ {
		go func() { <-start; _, e := adapter.Scrub(ctx, config); results <- result{e} }()
	}
	close(start)
	a, b := <-results, <-results
	success := 0
	if a.err == nil {
		success++
	}
	if b.err == nil {
		success++
	}
	if success != 1 {
		t.Fatalf("concurrent scrub successes=%d errors=%v/%v", success, a.err, b.err)
	}
}

func TestPayloadRehearsalPreviewReportsMigrationRequiredWithoutWriting(t *testing.T) {
	ctx := context.Background()
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	live, target := filepath.Join(dir, "live.db"), filepath.Join(dir, "raw-copy.db")
	migrations, _ := sqliteschema.Migrations()
	if err = infra.NewStoreManagementDatasource(infra.NewDatabase(live, migrations)).Initialize(ctx); err != nil {
		t.Fatal(err)
	}
	db, _ := sql.Open("sqlite", target)
	if _, err = db.Exec(`CREATE TABLE events(id TEXT PRIMARY KEY,body TEXT NOT NULL);CREATE TABLE command_audits(event_id TEXT PRIMARY KEY,command_text TEXT NOT NULL,input_text TEXT NOT NULL,output_text TEXT NOT NULL);INSERT INTO events VALUES('e','body')`); err != nil {
		t.Fatal(err)
	}
	_ = db.Close()
	adapter := newRehearsalAdapter(t, migrations, live)
	config := rehearsalTestConfig(target, live, filepath.Join(dir, "backup.db"))
	before, err := os.Stat(target)
	if err != nil {
		t.Fatal(err)
	}
	metrics, err := adapter.Preview(ctx, config)
	if err != nil {
		t.Fatal(err)
	}
	after, err := os.Stat(target)
	if err != nil {
		t.Fatal(err)
	}
	if !metrics.MigrationRequired || !metrics.DryRunZeroWrite || before.Size() != after.Size() || before.ModTime() != after.ModTime() {
		t.Fatalf("raw preview metrics=%#v", metrics)
	}
}

func TestPayloadRehearsalRejectsLiveAliases(t *testing.T) {
	dir := t.TempDir()
	live := filepath.Join(dir, "live.db")
	if err := os.WriteFile(live, []byte("not sqlite"), 0600); err != nil {
		t.Fatal(err)
	}
	adapter := newRehearsalAdapter(t, nil, live)
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
	adapter := newRehearsalAdapter(t, migrations, live)
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
	legacyAdapter := newRehearsalAdapter(t, migrations, legacy)
	if _, err = legacyAdapter.Preview(ctx, legacyConfig); err != nil {
		t.Fatalf("legacy live compatibility: %v", err)
	}
}

func newRehearsalAdapter(t *testing.T, migrations fs.FS, live string) *infra.PayloadRehearsalAdapter {
	t.Helper()
	adapter, err := infra.NewPayloadRehearsalAdapter(migrations, live)
	if err != nil {
		t.Fatal(err)
	}
	return adapter
}

func rehearsalTestConfig(target, live, backup string) apptypes.PayloadRehearsalConfig {
	return apptypes.PayloadRehearsalConfig{TargetPath: target, LivePath: live, BackupPath: backup, BatchRows: 2, StoredByteLimit: 1 << 20, DecodedByteLimit: 1 << 20, WallTimeLimit: time.Minute, LockTimeLimit: time.Second, ScrubByteLimit: 1 << 20, ScrubTimeLimit: time.Minute, MaxWALBytes: 1 << 30}
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
