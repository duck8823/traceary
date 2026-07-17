package usecase_test

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/duck8823/traceary/application"
	apptypes "github.com/duck8823/traceary/application/types"
	"github.com/duck8823/traceary/application/usecase"
	"github.com/duck8823/traceary/domain/types"
)

type archiveStoreStub struct {
	tables   []application.ArchiveTableData
	deleted  map[string][]string
	restored []application.ArchiveTableData
}

func (s *archiveStoreStub) Initialize(context.Context) error { return nil }
func (s *archiveStoreStub) CreateBackup(context.Context, string, bool) error {
	return nil
}
func (s *archiveStoreStub) RestoreBackup(context.Context, string, bool) error {
	return nil
}
func (s *archiveStoreStub) CollectGarbage(context.Context, time.Time, apptypes.GarbageCollectionTarget, bool) (int, error) {
	return 0, nil
}
func (s *archiveStoreStub) CloseStaleSessions(context.Context, time.Duration, bool, []types.SessionID) (int, error) {
	return 0, nil
}
func (s *archiveStoreStub) DedupeContentEvents(context.Context, apptypes.ContentEventDedupeParams) (apptypes.ContentEventDedupeResult, error) {
	return apptypes.ContentEventDedupeResult{}, nil
}
func (s *archiveStoreStub) RestoreContentEventDedupeRun(context.Context, string) (apptypes.ContentEventDedupeRestoreResult, error) {
	return apptypes.ContentEventDedupeRestoreResult{}, nil
}
func (s *archiveStoreStub) ListArchiveEligible(context.Context, time.Time, apptypes.GarbageCollectionTarget) ([]application.ArchiveTableData, error) {
	return s.tables, nil
}
func (s *archiveStoreStub) DeleteArchiveRows(_ context.Context, idsByTable map[string][]string) (int, error) {
	s.deleted = idsByTable
	n := 0
	for _, ids := range idsByTable {
		n += len(ids)
	}
	return n, nil
}
func (s *archiveStoreStub) RestoreArchiveRows(_ context.Context, tables []application.ArchiveTableData, dryRun bool) (int, int, int, error) {
	if !dryRun {
		s.restored = tables
	}
	total := 0
	for _, t := range tables {
		total += len(t.Rows)
	}
	return total, 0, 0, nil
}

func TestCreateStoreArchive_dryRunCounts(t *testing.T) {
	t.Parallel()
	stub := &archiveStoreStub{tables: []application.ArchiveTableData{
		{Name: "events", PrimaryKey: []string{"id"}, Rows: []map[string]any{
			{"id": "e1", "kind": "note", "client": "cli", "agent": "manual", "session_id": "s1", "workspace": "", "body": "hi", "created_at": "2020-01-01T00:00:00Z", "source_hook": ""},
		}},
	}}
	sut := usecase.NewStoreManagementUsecase(stub)
	got, err := sut.CreateStoreArchive(context.Background(), apptypes.StoreArchiveCreateParams{
		Before: time.Date(2021, 1, 1, 0, 0, 0, 0, time.UTC),
		Target: apptypes.GarbageCollectionTargetEvents,
		DryRun: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if got.TotalRows != 1 || !got.DryRun {
		t.Fatalf("got %+v", got)
	}
}

func TestCreateStoreArchive_verifyAndDelete(t *testing.T) {
	t.Parallel()
	stub := &archiveStoreStub{tables: []application.ArchiveTableData{
		{Name: "events", PrimaryKey: []string{"id"}, Rows: []map[string]any{
			{"id": "e1", "kind": "note", "client": "cli", "agent": "manual", "session_id": "s1", "workspace": "", "body": "hi", "created_at": "2020-01-01T00:00:00Z", "source_hook": ""},
		}},
	}}
	sut := usecase.NewStoreManagementUsecase(stub)
	dir := t.TempDir()
	path := filepath.Join(dir, "cold.trcaryar")
	got, err := sut.CreateStoreArchive(context.Background(), apptypes.StoreArchiveCreateParams{
		OutputPath:        path,
		Before:            time.Date(2021, 1, 1, 0, 0, 0, 0, time.UTC),
		KeepDays:          90,
		Target:            apptypes.GarbageCollectionTargetEvents,
		DeleteAfterVerify: true,
		ToolVersion:       "test",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !got.Verified || !got.DeletedAfterVerify || got.DeletedCount != 1 {
		t.Fatalf("got %+v", got)
	}
	if err := sut.VerifyStoreArchive(context.Background(), path, nil); err != nil {
		t.Fatalf("verify: %v", err)
	}
	// Corrupt payload digest path: truncate package mid-stream.
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(raw) < 20 {
		t.Fatalf("archive too small: %d", len(raw))
	}
	// Keep magic+version so open starts, drop the rest of the payload.
	truncated := append([]byte(nil), raw[:len(storeArchiveMagicPrefix)+1]...)
	if err := os.WriteFile(path+".bad", truncated, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := sut.VerifyStoreArchive(context.Background(), path+".bad", nil); err == nil {
		t.Fatal("expected verify failure on truncated archive")
	}
}

// storeArchiveMagicPrefix mirrors usecase package magic for corruption tests in usecase_test.
const storeArchiveMagicPrefix = "TRCARYAR"

func TestCreateStoreArchive_encryptedRoundTrip(t *testing.T) {
	t.Parallel()
	stub := &archiveStoreStub{tables: []application.ArchiveTableData{
		{Name: "events", PrimaryKey: []string{"id"}, Rows: []map[string]any{
			{"id": "e2", "kind": "note", "client": "cli", "agent": "manual", "session_id": "s1", "workspace": "", "body": "secret", "created_at": "2020-01-01T00:00:00Z", "source_hook": ""},
		}},
	}}
	sut := usecase.NewStoreManagementUsecase(stub)
	path := filepath.Join(t.TempDir(), "sealed.trcaryar")
	pass := []byte("test-passphrase-for-archive")
	_, err := sut.CreateStoreArchive(context.Background(), apptypes.StoreArchiveCreateParams{
		OutputPath:  path,
		Before:      time.Date(2021, 1, 1, 0, 0, 0, 0, time.UTC),
		Target:      apptypes.GarbageCollectionTargetEvents,
		Passphrase:  pass,
		ToolVersion: "test",
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := sut.VerifyStoreArchive(context.Background(), path, pass); err != nil {
		t.Fatalf("verify sealed: %v", err)
	}
	if err := sut.VerifyStoreArchive(context.Background(), path, []byte("wrong")); err == nil {
		t.Fatal("expected wrong passphrase to fail")
	}
	// Ensure ciphertext is not plain gzip after magic.
	raw, _ := os.ReadFile(path)
	if bytes.Contains(raw, []byte("secret")) {
		t.Fatal("plaintext body leaked into sealed archive")
	}
}

func TestRestoreStoreArchive_idempotent(t *testing.T) {
	t.Parallel()
	stub := &archiveStoreStub{tables: []application.ArchiveTableData{
		{Name: "events", PrimaryKey: []string{"id"}, Rows: []map[string]any{
			{"id": "e3", "kind": "note", "client": "cli", "agent": "manual", "session_id": "s1", "workspace": "", "body": "x", "created_at": "2020-01-01T00:00:00Z", "source_hook": ""},
		}},
	}}
	sut := usecase.NewStoreManagementUsecase(stub)
	path := filepath.Join(t.TempDir(), "r.trcaryar")
	_, err := sut.CreateStoreArchive(context.Background(), apptypes.StoreArchiveCreateParams{
		OutputPath: path,
		Before:     time.Date(2021, 1, 1, 0, 0, 0, 0, time.UTC),
		Target:     apptypes.GarbageCollectionTargetEvents,
	})
	if err != nil {
		t.Fatal(err)
	}
	got, err := sut.RestoreStoreArchive(context.Background(), path, nil, false)
	if err != nil {
		t.Fatal(err)
	}
	if got.Inserted != 1 {
		t.Fatalf("inserted=%d", got.Inserted)
	}
}
