package sqlite_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/duck8823/traceary/application"
	"github.com/duck8823/traceary/application/usecase"
	"github.com/duck8823/traceary/domain/attestation"
	"github.com/duck8823/traceary/domain/model"
	"github.com/duck8823/traceary/domain/types"
	"github.com/duck8823/traceary/infrastructure/sqlite"
)

func TestSavePublishesAttestationAnchorAndRedeliveryDoesNotAddALine(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	path, events := newAttestationTestStore(t)

	first := hookDeliveryTestEvent(t, "event-1", "session-1", "/repo", "/repo", "same body", "event_id:delivery-1")
	if err := events.Save(ctx, first); err != nil {
		t.Fatalf("Save(first) error = %v", err)
	}
	anchorPath := sqlite.AttestationAnchorPath(path)
	firstBody, err := os.ReadFile(anchorPath)
	if err != nil {
		t.Fatalf("ReadFile(%s) after Save error = %v", anchorPath, err)
	}
	firstRecords, err := attestation.ParseAnchorFile(firstBody)
	if err != nil {
		t.Fatalf("ParseAnchorFile() after Save error = %v", err)
	}
	if len(firstRecords) != 1 {
		t.Fatalf("anchor records after Save = %d, want 1", len(firstRecords))
	}
	if firstRecords[0].Seq != 1 {
		t.Fatalf("anchor seq = %d, want 1", firstRecords[0].Seq)
	}

	db := openAttestationDB(t, path)
	defer func() { _ = db.Close() }()
	var head string
	if err := db.QueryRow(`SELECT head_sha256 FROM attestation_head WHERE singleton = 1`).Scan(&head); err != nil {
		t.Fatalf("read head: %v", err)
	}
	if !strings.EqualFold(firstRecords[0].Head, head) {
		t.Fatalf("anchor head = %q, store head = %q", firstRecords[0].Head, head)
	}

	retry := hookDeliveryTestEvent(t, "event-2", "session-1", "/repo", "/repo", "same body", "event_id:delivery-1")
	if err := events.Save(ctx, retry); err != nil {
		t.Fatalf("Save(retry) error = %v", err)
	}
	secondBody, err := os.ReadFile(anchorPath)
	if err != nil {
		t.Fatalf("ReadFile after redelivery error = %v", err)
	}
	if string(secondBody) != string(firstBody) {
		t.Fatalf("redelivery changed anchor file:\n%s\nvs\n%s", secondBody, firstBody)
	}
}

func TestInspectAttestationAnchor_HealsMissingFileAndFailsOnTamper(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	path, events := newAttestationTestStore(t)
	prompt := model.EventOf(
		types.EventID("prompt-anchor"), types.EventKindPrompt,
		types.Client("cli"), types.Agent("codex"),
		types.SessionID("session-1"), types.Workspace("ws"),
		"anchor me",
		time.Date(2026, 8, 14, 18, 0, 0, 0, time.UTC),
	)
	if err := events.Save(ctx, prompt); err != nil {
		t.Fatalf("Save() error = %v", err)
	}

	database := sqlite.NewDatabase(path, onDiskSQLiteMigrations(t))
	inspector := sqlite.NewAttestationAnchorInspector(database)
	opts := application.AttestationAnchorInspectOptions{StorePath: path, OpenStore: true}

	anchorPath := sqlite.AttestationAnchorPath(path)
	if err := os.Remove(anchorPath); err != nil {
		t.Fatalf("Remove(%s) error = %v", anchorPath, err)
	}
	healed, err := inspector.InspectAttestationAnchor(ctx, opts)
	if err != nil {
		t.Fatalf("Inspect after missing file error = %v", err)
	}
	if !healed.Published || healed.Relation != string(attestation.AnchorMatches) {
		t.Fatalf("healed state = %+v, want published matches", healed)
	}
	if _, err := os.Stat(anchorPath); err != nil {
		t.Fatalf("healed file missing: %v", err)
	}

	original, err := os.ReadFile(anchorPath)
	if err != nil {
		t.Fatalf("ReadFile original error = %v", err)
	}
	tampered := strings.Replace(string(original), healed.FileHead, strings.Repeat("ab", 32), 1)
	if tampered == string(original) {
		t.Fatal("failed to tamper file head")
	}
	if err := os.WriteFile(anchorPath, []byte(tampered), 0o600); err != nil {
		t.Fatalf("WriteFile tampered anchor: %v", err)
	}
	mismatch, err := inspector.InspectAttestationAnchor(ctx, opts)
	if err != nil {
		t.Fatalf("Inspect after file tamper error = %v", err)
	}
	if mismatch.Relation != string(attestation.AnchorMismatch) {
		t.Fatalf("file tamper relation = %q, want mismatch", mismatch.Relation)
	}

	if err := os.WriteFile(anchorPath, original, 0o600); err != nil {
		t.Fatalf("restore anchor: %v", err)
	}
	db := openAttestationDB(t, path)
	if _, err := db.Exec(`UPDATE attestation_head SET head_sha256 = ? WHERE singleton = 1`, strings.Repeat("cd", 32)); err != nil {
		t.Fatalf("tamper store head: %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	if _, err := inspector.InspectAttestationAnchor(ctx, opts); err == nil {
		t.Fatal("Inspect after store head tamper error = nil")
	}
}

func TestInspectAttestationAnchor_LargeStoreReadsFileOnly(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	dir := t.TempDir()
	storePath := filepath.Join(dir, "not-a-store.db")
	record := attestation.AnchorRecord{
		Version:     attestation.AnchorFormatVersion,
		Seq:         3,
		Head:        attestation.GenesisHex(),
		PublishedAt: "2026-08-14T18:00:00Z",
	}
	line, err := attestation.FormatAnchorLine(record)
	if err != nil {
		t.Fatalf("FormatAnchorLine() error = %v", err)
	}
	if err := os.WriteFile(storePath+attestation.AnchorFileSuffix, append(line, '\n'), 0o600); err != nil {
		t.Fatalf("WriteFile sidecar: %v", err)
	}

	inspector := sqlite.NewAttestationAnchorInspector(nil)
	state, err := inspector.InspectAttestationAnchor(ctx, application.AttestationAnchorInspectOptions{
		StorePath: storePath,
		OpenStore: false,
	})
	if err != nil {
		t.Fatalf("Inspect(file-only) error = %v", err)
	}
	if state.Relation != "file_ok" || state.FileSeq != 3 {
		t.Fatalf("file-only state = %+v, want file_ok seq=3", state)
	}
	if _, err := os.Stat(storePath); !os.IsNotExist(err) {
		t.Fatalf("file-only inspect created or opened a store: %v", err)
	}
}

func TestPublishAttestationAnchor_DoesNotAppendConflictingHead(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	path, events := newAttestationTestStore(t)
	prompt := model.EventOf(
		types.EventID("prompt-conflict"), types.EventKindPrompt,
		types.Client("cli"), types.Agent("codex"),
		types.SessionID("session-1"), types.Workspace("ws"),
		"conflict",
		time.Date(2026, 8, 14, 18, 30, 0, 0, time.UTC),
	)
	if err := events.Save(ctx, prompt); err != nil {
		t.Fatalf("Save() error = %v", err)
	}
	anchorPath := sqlite.AttestationAnchorPath(path)
	before, err := os.ReadFile(anchorPath)
	if err != nil {
		t.Fatalf("ReadFile before: %v", err)
	}
	db := openAttestationDB(t, path)
	defer func() { _ = db.Close() }()
	if err := sqlite.PublishAttestationAnchorFromDB(ctx, db, path); err != nil {
		t.Fatalf("identical republish error = %v", err)
	}
	same, err := os.ReadFile(anchorPath)
	if err != nil {
		t.Fatalf("ReadFile after republish: %v", err)
	}
	if string(same) != string(before) {
		t.Fatal("identical republish appended a line")
	}

	records, err := attestation.ParseAnchorFile(before)
	if err != nil {
		t.Fatalf("ParseAnchorFile: %v", err)
	}
	records[0].Head = strings.Repeat("ef", 32)
	line, err := attestation.FormatAnchorLine(records[0])
	if err != nil {
		t.Fatalf("FormatAnchorLine tampered: %v", err)
	}
	if err := os.WriteFile(anchorPath, append(line, '\n'), 0o600); err != nil {
		t.Fatalf("WriteFile conflict: %v", err)
	}
	if err := sqlite.PublishAttestationAnchorFromDB(ctx, db, path); err == nil {
		t.Fatal("PublishAttestationAnchorFromDB() error = nil for conflicting head")
	}
	after, err := os.ReadFile(anchorPath)
	if err != nil {
		t.Fatalf("ReadFile after conflict: %v", err)
	}
	if strings.Count(string(after), "\n") != 1 {
		t.Fatalf("conflict publish changed line count: %q", after)
	}
}

func TestCompactKeepsAttestationAnchorSidecar(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	path, events := newAttestationTestStore(t)
	first := hookPromptAt(t, "evt-anchor-1", "session-dup", "same prompt", time.Date(2026, 8, 14, 19, 0, 0, 0, time.UTC), "event_id:a1")
	second := hookPromptAt(t, "evt-anchor-2", "session-dup", "same prompt", time.Date(2026, 8, 14, 19, 0, 1, 0, time.UTC), "event_id:a2")
	if err := events.Save(ctx, first); err != nil {
		t.Fatalf("Save(first) error = %v", err)
	}
	if err := events.Save(ctx, second); err != nil {
		t.Fatalf("Save(second) error = %v", err)
	}
	anchorPath := sqlite.AttestationAnchorPath(path)
	if _, err := os.Stat(anchorPath); err != nil {
		t.Fatalf("Stat before compact: %v", err)
	}

	svc := usecase.NewStoreCompactionUsecase(
		path,
		&sqlite.CompactionFileJournal{Dir: filepath.Join(t.TempDir(), "journal")},
		sqlite.SQLiteCompactionBuilder{},
		sqlite.StoreReplacementFiles{CallerHoldsExclusiveLease: true},
		sqlite.StoreLeaseCoordinator{},
	)
	if _, err := svc.Compact(ctx, application.CompactInput{
		Source:   path,
		KeepDays: 90,
		Now:      time.Date(2026, 8, 14, 20, 0, 0, 0, time.UTC),
	}); err != nil {
		t.Fatalf("Compact() error = %v", err)
	}
	after, err := os.ReadFile(anchorPath)
	if err != nil {
		t.Fatalf("ReadFile after compact: %v", err)
	}
	if len(after) == 0 {
		t.Fatal("compact removed the attestation sidecar")
	}
	if _, err := attestation.ParseAnchorFile(after); err != nil {
		t.Fatalf("ParseAnchorFile after compact: %v", err)
	}

	db := openAttestationDB(t, path)
	defer func() { _ = db.Close() }()
	if err := sqlite.VerifyAttestationChain(ctx, db); err != nil {
		t.Fatalf("VerifyAttestationChain after compact: %v", err)
	}
	if err := (sqlite.SQLiteCompactionBuilder{}).VerifyPair(ctx, path, path); err != nil {
		t.Fatalf("VerifyPair(source, source) after compact: %v", err)
	}
}
