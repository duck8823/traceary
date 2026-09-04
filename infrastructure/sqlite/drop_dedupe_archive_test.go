package sqlite_test

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/klauspost/compress/zstd"

	"github.com/duck8823/traceary/application"
	apptypes "github.com/duck8823/traceary/application/types"
	"github.com/duck8823/traceary/application/usecase"
	"github.com/duck8823/traceary/domain"
	"github.com/duck8823/traceary/infrastructure/sqlite"
	sqliteschema "github.com/duck8823/traceary/schema/sqlite"
)

func TestDropDedupeArchive_EmptyArchiveUpgrade(t *testing.T) {
	if testing.Short() {
		t.Skip("candidate upgrade")
	}
	if runtime.GOOS != "darwin" && runtime.GOOS != "linux" {
		t.Skip("advisory lease unsupported")
	}
	ctx := context.Background()
	dir := t.TempDir()
	target := filepath.Join(dir, "store.db")
	seedLegacyStore(t, target, 81)
	insertSourceEvent(t, target, "e-keep")
	if !tablePresent(t, target, "event_content_dedupe_archive") {
		t.Fatal("archive table missing before upgrade")
	}
	if archiveCount(t, target) != 0 {
		t.Fatal("operator-shaped fixture must start empty")
	}
	before := eventCount(t, target)
	beforeDigest := eventsRowDigest(t, target)

	all, err := sqliteschema.Migrations()
	if err != nil {
		t.Fatal(err)
	}
	runUpgradeOn(ctx, t, dir, target, all)

	if tablePresent(t, target, "event_content_dedupe_archive") {
		t.Fatal("archive table survived v81")
	}
	assertMinimumReaderVersion(t, target, 37)
	assertSchemaMigrationSuffix(t, target, 81, "000081_drop_event_content_dedupe_archive.sql")
	assertQuickCheckOK(t, target)
	if got := eventCount(t, target); got != before {
		t.Fatalf("events count = %d, want unchanged %d", got, before)
	}
	if got := eventsRowDigest(t, target); got != beforeDigest {
		t.Fatal("empty-archive upgrade rewrote events rows")
	}
}

func TestDropDedupeArchive_PopulatedRestore(t *testing.T) {
	if testing.Short() {
		t.Skip("candidate upgrade")
	}
	if runtime.GOOS != "darwin" && runtime.GOOS != "linux" {
		t.Skip("advisory lease unsupported")
	}
	ctx := context.Background()
	dir := t.TempDir()
	target := filepath.Join(dir, "store.db")
	seedLegacyStore(t, target, 81)
	insertSourceEvent(t, target, "e-keep")
	insertCommandAuditForEvent(t, target, "e-keep")
	insertArchiveIdentityRow(t, target, "arch-legacy", "legacy archived body", true)
	insertArchiveZstdRow(t, target, "arch-zstd", "zstd archived body")
	insertRefinement(t, target, "s-heal", "arch-legacy", "e-keep")
	sourceCount := eventCount(t, target)
	sourceDigest := eventsRowDigest(t, target)
	auditBefore := commandAuditCount(t, target)
	if archiveCount(t, target) != 2 {
		t.Fatalf("archive rows = %d, want 2", archiveCount(t, target))
	}

	all, err := sqliteschema.Migrations()
	if err != nil {
		t.Fatal(err)
	}
	runUpgradeOn(ctx, t, dir, target, all)

	if tablePresent(t, target, "event_content_dedupe_archive") {
		t.Fatal("archive table survived populated restore")
	}
	assertMinimumReaderVersion(t, target, 37)
	assertQuickCheckOK(t, target)
	if got := eventCount(t, target); got != sourceCount+2 {
		t.Fatalf("events count = %d, want source %d + 2", got, sourceCount)
	}
	if got := eventsRowDigestExcluding(t, target, []string{"arch-legacy", "arch-zstd"}); got != sourceDigest {
		t.Fatal("restored candidate rewrote source event rows")
	}
	if commandAuditCount(t, target) != auditBefore {
		t.Fatalf("command_audits count changed: %d -> %d", auditBefore, commandAuditCount(t, target))
	}
	assertEventPresent(t, target, "arch-legacy")
	assertEventPresent(t, target, "arch-zstd")
	db := sqlite.OpenStoreForTest(target)
	defer func() { _ = db.Close() }()
	plaintext, err := sqlite.LoadEventPlaintextForTest(ctx, db, "arch-zstd")
	if err != nil {
		t.Fatalf("decode restored zstd row: %v", err)
	}
	if string(plaintext) != "zstd archived body" {
		t.Fatalf("restored zstd plaintext = %q", plaintext)
	}
	if !refinementEndpointsResolve(t, target) {
		t.Fatal("refinement coverage endpoints do not resolve after restore")
	}
}

func TestDropDedupeArchive_RefusalVariants(t *testing.T) {
	if testing.Short() {
		t.Skip("candidate upgrade")
	}
	if runtime.GOOS != "darwin" && runtime.GOOS != "linux" {
		t.Skip("advisory lease unsupported")
	}
	cases := []struct {
		name   string
		reason string
		event  string
		seed   func(*testing.T, string)
	}{
		{
			name:   "id collision",
			reason: "event already exists in events",
			event:  "e-keep",
			seed: func(t *testing.T, path string) {
				insertSourceEvent(t, path, "e-keep")
				insertArchiveIdentityRow(t, path, "e-keep", "colliding body", false)
			},
		},
		{
			name:   "corrupt codec",
			reason: "payload decode failed",
			event:  "arch-corrupt",
			seed: func(t *testing.T, path string) {
				insertSourceEvent(t, path, "e-keep")
				insertCorruptCodecArchiveRow(t, path, "arch-corrupt")
			},
		},
		{
			name:   "dangling endpoint",
			reason: "session_refinements coverage endpoint does not resolve",
			event:  "missing-endpoint",
			seed: func(t *testing.T, path string) {
				insertSourceEvent(t, path, "e-keep")
				insertArchiveIdentityRow(t, path, "arch-ok", "ok body", false)
				insertRefinement(t, path, "s-dangle", "missing-endpoint", "e-keep")
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			dir := t.TempDir()
			target := filepath.Join(dir, "store.db")
			seedLegacyStore(t, target, 81)
			tc.seed(t, target)
			beforeCount := eventCount(t, target)
			beforeDigest := eventsRowDigest(t, target)
			beforeArchive := archiveCount(t, target)
			beforeBytes := readStoreBytes(t, target)

			all, err := sqliteschema.Migrations()
			if err != nil {
				t.Fatal(err)
			}
			err = runUpgradeExpectError(ctx, t, dir, target, all)
			var refused *apptypes.DedupeArchiveRestoreRefusedError
			if !errors.As(err, &refused) {
				t.Fatalf("error = %v, want DedupeArchiveRestoreRefusedError", err)
			}
			if refused.RowCount != beforeArchive {
				t.Fatalf("RowCount = %d, want %d", refused.RowCount, beforeArchive)
			}
			if refused.Reason != tc.reason {
				t.Fatalf("Reason = %q, want %q", refused.Reason, tc.reason)
			}
			if tc.event != "" && refused.EventID != tc.event {
				t.Fatalf("EventID = %q, want %q", refused.EventID, tc.event)
			}
			if !tablePresent(t, target, "event_content_dedupe_archive") {
				t.Fatal("refusal dropped the archive table on the source")
			}
			if eventCount(t, target) != beforeCount {
				t.Fatal("refusal changed source events count")
			}
			if eventsRowDigest(t, target) != beforeDigest {
				t.Fatal("refusal changed source events digest")
			}
			if archiveCount(t, target) != beforeArchive {
				t.Fatal("refusal changed source archive count")
			}
			afterBytes := readStoreBytes(t, target)
			if string(afterBytes) != string(beforeBytes) {
				t.Fatal("refusal mutated source file bytes")
			}
		})
	}
}

func TestRestoreDedupeArchiveConservationLaw(t *testing.T) {
	if testing.Short() {
		t.Skip("verifier")
	}
	if runtime.GOOS != "darwin" && runtime.GOOS != "linux" {
		t.Skip("advisory lease unsupported")
	}
	ctx := context.Background()
	dir := t.TempDir()
	source := filepath.Join(dir, "source.db")
	seedLegacyStore(t, source, 81)
	insertSourceEvent(t, source, "e-keep")
	insertArchiveIdentityRow(t, source, "arch-1", "archived one", false)
	insertArchiveIdentityRow(t, source, "arch-2", "archived two", false)
	all, err := sqliteschema.Migrations()
	if err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(source)
	if err != nil {
		t.Fatal(err)
	}
	recipe := &sqlite.PreparedUpgradeMigrationRecipe{PreparedMigrationCandidateRecipe: sqlite.PreparedMigrationCandidateRecipe{
		Migrations: all,
		Verifier:   sqlite.PreparedMigrationVerifier{Migrations: all},
	}}
	journal := &sqlite.PreparedStoreUpgradeFileJournal{Dir: filepath.Join(dir, "journal")}
	svc := usecase.NewPreparedStoreUpgradeUsecase(source, journal, sqlite.NewPreparedStoreUpgradeFilesForTest(true, nil), sqlite.StoreLeaseCoordinator{}, map[domain.PreparedStoreUpgradeOperation]application.PreparedCandidateRecipe{
		domain.PreparedStoreUpgradeOperationOfflineMigrationUpgrade: recipe,
	})
	run, err := svc.Plan(ctx, application.PreparedStoreUpgradeCommand{
		Operation:       domain.PreparedStoreUpgradeOperationOfflineMigrationUpgrade,
		TargetPath:      source,
		ConsumerBinding: "v81-law",
		Budget:          upgradeBudget(uint64(info.Size())),
	})
	if err != nil {
		t.Fatal(err)
	}
	run, err = svc.Prepare(ctx, run.ID)
	if err != nil {
		t.Fatal(err)
	}
	verifier := sqlite.PreparedMigrationVerifier{Migrations: all}
	if _, err := verifier.VerifyUpgradePair(ctx, source, run.CandidatePath, run.PlanDigest); err != nil {
		t.Fatalf("restored candidate: %v", err)
	}

	t.Run("dropped archived row", func(t *testing.T) {
		corrupt := copyCandidate(t, run.CandidatePath)
		db, err := sql.Open("sqlite", corrupt)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := db.Exec(`DELETE FROM events WHERE id = 'arch-1'`); err != nil {
			t.Fatal(err)
		}
		_ = db.Close()
		if _, err := verifier.VerifyUpgradePair(ctx, source, corrupt, run.PlanDigest); err == nil {
			t.Fatal("silently dropped archived row must fail the restore conservation law")
		}
	})
}

func runUpgradeExpectError(ctx context.Context, t *testing.T, dir, target string, all fs.FS) error {
	t.Helper()
	info, err := os.Stat(target)
	if err != nil {
		t.Fatal(err)
	}
	journal := &sqlite.PreparedStoreUpgradeFileJournal{Dir: filepath.Join(dir, "journal")}
	recipe := &sqlite.PreparedUpgradeMigrationRecipe{PreparedMigrationCandidateRecipe: sqlite.PreparedMigrationCandidateRecipe{
		Migrations: all,
		Verifier:   sqlite.PreparedMigrationVerifier{Migrations: all},
	}}
	svc := usecase.NewPreparedStoreUpgradeUsecase(target, journal, sqlite.NewPreparedStoreUpgradeFilesForTest(true, nil), sqlite.StoreLeaseCoordinator{}, map[domain.PreparedStoreUpgradeOperation]application.PreparedCandidateRecipe{
		domain.PreparedStoreUpgradeOperationOfflineMigrationUpgrade: recipe,
	})
	_, err = svc.RunUpgrade(ctx, application.PreparedStoreUpgradeCommand{
		Operation:       domain.PreparedStoreUpgradeOperationOfflineMigrationUpgrade,
		TargetPath:      target,
		ConsumerBinding: "refuse",
		Budget:          upgradeBudget(uint64(info.Size())),
	})
	if err == nil {
		t.Fatal("want restore refusal")
	}
	return err
}

func tablePresent(t *testing.T, path, name string) bool {
	t.Helper()
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	var n int
	if err := db.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name=?`, name).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n == 1
}

func eventCount(t *testing.T, path string) int {
	t.Helper()
	return countSQL(t, path, `SELECT COUNT(*) FROM events`)
}

func archiveCount(t *testing.T, path string) int {
	t.Helper()
	return countSQL(t, path, `SELECT COUNT(*) FROM event_content_dedupe_archive`)
}

func commandAuditCount(t *testing.T, path string) int {
	t.Helper()
	return countSQL(t, path, `SELECT COUNT(*) FROM command_audits`)
}

func countSQL(t *testing.T, path, query string) int {
	t.Helper()
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	var n int
	if err := db.QueryRow(query).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func eventsRowDigest(t *testing.T, path string) string {
	t.Helper()
	return eventsRowDigestExcluding(t, path, nil)
}

func eventsRowDigestExcluding(t *testing.T, path string, skip []string) string {
	t.Helper()
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	rows, err := db.Query(`SELECT id, kind, body, COALESCE(body_codec, ''), COALESCE(body_sha256, '') FROM events ORDER BY id`)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = rows.Close() }()
	skipSet := map[string]struct{}{}
	for _, id := range skip {
		skipSet[id] = struct{}{}
	}
	sum := sha256.New()
	for rows.Next() {
		var id, kind, codec, digest string
		var body []byte
		if err := rows.Scan(&id, &kind, &body, &codec, &digest); err != nil {
			t.Fatal(err)
		}
		if _, skipped := skipSet[id]; skipped {
			continue
		}
		_, _ = sum.Write([]byte(id))
		_, _ = sum.Write([]byte(kind))
		_, _ = sum.Write(body)
		_, _ = sum.Write([]byte(codec))
		_, _ = sum.Write([]byte(digest))
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return hex.EncodeToString(sum.Sum(nil))
}

func assertEventPresent(t *testing.T, path, id string) {
	t.Helper()
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	var got string
	if err := db.QueryRow(`SELECT id FROM events WHERE id = ?`, id).Scan(&got); err != nil {
		t.Fatalf("event %s missing: %v", id, err)
	}
}

func insertArchiveIdentityRow(t *testing.T, path, id, body string, legacyNullCodec bool) {
	t.Helper()
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	now := time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC).Format(time.RFC3339Nano)
	if legacyNullCodec {
		_, err = db.Exec(`INSERT INTO event_content_dedupe_archive
			(id, kind, client, agent, session_id, workspace, body, created_at, source_hook,
			 kept_event_id, dedupe_run_id, archived_at, group_key, reason)
			VALUES (?, 'prompt', 'hook', 'codex', 's', 'w', ?, ?, NULL, 'e-keep', 'run-legacy', ?, 'k', 'dup')`,
			id, body, now, now)
	} else {
		sum := sha256.Sum256([]byte(body))
		_, err = db.Exec(`INSERT INTO event_content_dedupe_archive
			(id, kind, client, agent, session_id, workspace, body, created_at, source_hook,
			 kept_event_id, dedupe_run_id, archived_at, group_key, reason,
			 body_codec, body_format_version, body_plaintext_bytes, body_encoded_bytes, body_sha256)
			VALUES (?, 'prompt', 'hook', 'codex', 's', 'w', ?, ?, NULL, 'e-keep', 'run-id', ?, 'k', 'dup',
			 'identity', 1, ?, ?, ?)`,
			id, body, now, now, len(body), len(body), hex.EncodeToString(sum[:]))
	}
	if err != nil {
		t.Fatal(err)
	}
}

func insertArchiveZstdRow(t *testing.T, path, id, plaintext string) {
	t.Helper()
	enc, err := zstd.NewWriter(nil, zstd.WithEncoderConcurrency(1))
	if err != nil {
		t.Fatal(err)
	}
	stored := enc.EncodeAll([]byte(plaintext), nil)
	if err := enc.Close(); err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256([]byte(plaintext))
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	now := time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC).Format(time.RFC3339Nano)
	_, err = db.Exec(`INSERT INTO event_content_dedupe_archive
		(id, kind, client, agent, session_id, workspace, body, created_at, source_hook,
		 kept_event_id, dedupe_run_id, archived_at, group_key, reason,
		 body_codec, body_format_version, body_plaintext_bytes, body_encoded_bytes, body_sha256)
		VALUES (?, 'transcript', 'hook', 'codex', 's', 'w', ?, ?, NULL, 'e-keep', 'run-zstd', ?, 'k', 'dup',
		 'zstd', 1, ?, ?, ?)`,
		id, stored, now, now, len(plaintext), len(stored), hex.EncodeToString(sum[:]))
	if err != nil {
		t.Fatal(err)
	}
}

func insertCorruptCodecArchiveRow(t *testing.T, path, id string) {
	t.Helper()
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	now := time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC).Format(time.RFC3339Nano)
	_, err = db.Exec(`INSERT INTO event_content_dedupe_archive
		(id, kind, client, agent, session_id, workspace, body, created_at, source_hook,
		 kept_event_id, dedupe_run_id, archived_at, group_key, reason,
		 body_codec, body_format_version, body_plaintext_bytes, body_encoded_bytes, body_sha256)
		VALUES (?, 'prompt', 'hook', 'codex', 's', 'w', ?, ?, NULL, 'e-keep', 'run-bad', ?, 'k', 'dup',
		 'zstd', 1, 12, 3, 'deadbeef')`,
		id, []byte("not"), now, now)
	if err != nil {
		t.Fatal(err)
	}
}

func insertCommandAuditForEvent(t *testing.T, path, eventID string) {
	t.Helper()
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	if _, err := db.Exec(`INSERT INTO command_audits(event_id, command_text, input_text, output_text) VALUES (?, 'echo hi', '', '')`, eventID); err != nil {
		t.Fatal(err)
	}
}

func insertRefinement(t *testing.T, path, sessionID, fromID, toID string) {
	t.Helper()
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	now := time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC).Format(time.RFC3339Nano)
	_, err = db.Exec(`INSERT INTO session_refinements(
		session_id, generation, covers_from_event_id, covers_to_event_id,
		summary, keywords, produced_by, produced_at, degraded)
		VALUES (?, 1, ?, ?, 'summary', '', 'test', ?, 0)`, sessionID, fromID, toID, now)
	if err != nil {
		t.Fatal(err)
	}
}

func refinementEndpointsResolve(t *testing.T, path string) bool {
	t.Helper()
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	rows, err := db.Query(`SELECT covers_from_event_id, covers_to_event_id FROM session_refinements`)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var fromID, toID string
		if err := rows.Scan(&fromID, &toID); err != nil {
			t.Fatal(err)
		}
		for _, id := range []string{fromID, toID} {
			var got string
			if err := db.QueryRow(`SELECT id FROM events WHERE id = ?`, id).Scan(&got); err != nil {
				return false
			}
		}
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return true
}

func readStoreBytes(t *testing.T, path string) []byte {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return data
}
