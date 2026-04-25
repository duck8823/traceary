package sqlite_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/duck8823/traceary/domain/model"
	"github.com/duck8823/traceary/domain/types"
	"github.com/duck8823/traceary/infrastructure/sqlite"
)

func TestMemoryToolFileDatasource_roundTripThroughRepository(t *testing.T) {
	t.Parallel()

	dbPath := filepath.Join(t.TempDir(), "traceary.db")
	db := sqlite.NewDatabase(dbPath, os.DirFS("../../schema/sqlite/migrations"))
	store := sqlite.NewStoreManagementDatasource(db)
	if err := store.Initialize(context.Background()); err != nil {
		t.Fatalf("Initialize() error = %v", err)
	}

	sut := sqlite.NewMemoryToolFileDatasource(db)
	path := mustMemoryToolPath(t, "/memories/project/notes.txt")
	now := time.Date(2026, 4, 25, 12, 0, 0, 0, time.UTC)
	file := model.NewMemoryToolFile(path, []byte("alpha\nbeta\n"), now)

	if err := sut.Save(context.Background(), file); err != nil {
		t.Fatalf("Save() error = %v", err)
	}
	restored, err := sut.FindByPath(context.Background(), path)
	if err != nil {
		t.Fatalf("FindByPath() error = %v", err)
	}
	got, ok := restored.Value()
	if !ok {
		t.Fatalf("FindByPath() returned none")
	}
	if got.Path().String() != path.String() || string(got.Content()) != "alpha\nbeta\n" || got.SizeBytes() != int64(len("alpha\nbeta\n")) {
		t.Fatalf("restored file = path:%q content:%q size:%d", got.Path().String(), string(got.Content()), got.SizeBytes())
	}

	newPath := mustMemoryToolPath(t, "/memories/archive/notes.txt")
	if renamed, err := sut.RenamePathPrefix(context.Background(), path, newPath, now.Add(time.Hour)); err != nil {
		t.Fatalf("RenamePathPrefix() error = %v", err)
	} else if renamed != 1 {
		t.Fatalf("RenamePathPrefix() renamed = %d, want 1", renamed)
	}
	if deleted, err := sut.DeletePathPrefix(context.Background(), mustMemoryToolPath(t, "/memories/archive")); err != nil {
		t.Fatalf("DeletePathPrefix() error = %v", err)
	} else if deleted != 1 {
		t.Fatalf("DeletePathPrefix() deleted = %d, want 1", deleted)
	}
}

func TestMemoryToolFileDatasource_deletePathPrefixUsesExactCaseSensitiveDescendantSemantics(t *testing.T) {
	t.Parallel()

	sut := newTestMemoryToolFileDatasource(t)
	ctx := context.Background()
	now := time.Date(2026, 4, 25, 12, 0, 0, 0, time.UTC)
	for _, rawPath := range []string{
		"/memories/a_1/file.txt",
		"/memories/ab1/file.txt",
		"/memories/A_1/file.txt",
	} {
		path := mustMemoryToolPath(t, rawPath)
		if err := sut.Save(ctx, model.NewMemoryToolFile(path, []byte(rawPath), now)); err != nil {
			t.Fatalf("Save(%q) error = %v", rawPath, err)
		}
	}

	deleted, err := sut.DeletePathPrefix(ctx, mustMemoryToolPath(t, "/memories/a_1"))
	if err != nil {
		t.Fatalf("DeletePathPrefix() error = %v", err)
	}
	if deleted != 1 {
		t.Fatalf("DeletePathPrefix() deleted = %d, want 1", deleted)
	}
	assertMemoryToolPathMissing(t, sut, "/memories/a_1/file.txt")
	assertMemoryToolPathExists(t, sut, "/memories/ab1/file.txt")
	assertMemoryToolPathExists(t, sut, "/memories/A_1/file.txt")
}

func TestMemoryToolFileDatasource_renamePathPrefixUsesExactCaseSensitiveDescendantSemantics(t *testing.T) {
	t.Parallel()

	sut := newTestMemoryToolFileDatasource(t)
	ctx := context.Background()
	now := time.Date(2026, 4, 25, 12, 0, 0, 0, time.UTC)
	for _, rawPath := range []string{
		"/memories/a_1/file.txt",
		"/memories/ab1/file.txt",
		"/memories/A_1/file.txt",
	} {
		path := mustMemoryToolPath(t, rawPath)
		if err := sut.Save(ctx, model.NewMemoryToolFile(path, []byte(rawPath), now)); err != nil {
			t.Fatalf("Save(%q) error = %v", rawPath, err)
		}
	}

	renamed, err := sut.RenamePathPrefix(ctx, mustMemoryToolPath(t, "/memories/a_1"), mustMemoryToolPath(t, "/memories/renamed"), now.Add(time.Hour))
	if err != nil {
		t.Fatalf("RenamePathPrefix() error = %v", err)
	}
	if renamed != 1 {
		t.Fatalf("RenamePathPrefix() renamed = %d, want 1", renamed)
	}
	assertMemoryToolPathMissing(t, sut, "/memories/a_1/file.txt")
	assertMemoryToolPathExists(t, sut, "/memories/renamed/file.txt")
	assertMemoryToolPathExists(t, sut, "/memories/ab1/file.txt")
	assertMemoryToolPathExists(t, sut, "/memories/A_1/file.txt")
}

func mustMemoryToolPath(t *testing.T, raw string) types.MemoryToolPath {
	t.Helper()
	path, err := types.NewMemoryToolPath(raw)
	if err != nil {
		t.Fatalf("NewMemoryToolPath(%q) error = %v", raw, err)
	}
	return path
}

func newTestMemoryToolFileDatasource(t *testing.T) *sqlite.MemoryToolFileDatasource {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "traceary.db")
	db := sqlite.NewDatabase(dbPath, os.DirFS("../../schema/sqlite/migrations"))
	store := sqlite.NewStoreManagementDatasource(db)
	if err := store.Initialize(context.Background()); err != nil {
		t.Fatalf("Initialize() error = %v", err)
	}
	return sqlite.NewMemoryToolFileDatasource(db)
}

func assertMemoryToolPathExists(t *testing.T, sut *sqlite.MemoryToolFileDatasource, rawPath string) {
	t.Helper()
	got, err := sut.FindByPath(context.Background(), mustMemoryToolPath(t, rawPath))
	if err != nil {
		t.Fatalf("FindByPath(%q) error = %v", rawPath, err)
	}
	if _, ok := got.Value(); !ok {
		t.Fatalf("FindByPath(%q) returned none", rawPath)
	}
}

func assertMemoryToolPathMissing(t *testing.T, sut *sqlite.MemoryToolFileDatasource, rawPath string) {
	t.Helper()
	got, err := sut.FindByPath(context.Background(), mustMemoryToolPath(t, rawPath))
	if err != nil {
		t.Fatalf("FindByPath(%q) error = %v", rawPath, err)
	}
	if file, ok := got.Value(); ok {
		t.Fatalf("FindByPath(%q) returned %q", rawPath, file.Path().String())
	}
}
