package main

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"golang.org/x/xerrors"
)

func TestArchiveAndFileRetentionSymbolsAreAbsentFromRuntimeSources(t *testing.T) {
	t.Parallel()
	archivePattern := regexp.MustCompile(`ArchiveSegment|StoreArchive`)
	fileRetentionPattern := regexp.MustCompile(`FileRetention|file_retention`)
	allowArchive := map[string]bool{
		filepath.Join("infrastructure", "sqlite", "drop_archive_segments.go"):             true,
		filepath.Join("infrastructure", "sqlite", "prepared_migration_catalog.go"):        true,
		filepath.Join("infrastructure", "sqlite", "prepared_upgrade_verifier.go"):         true,
		filepath.Join("infrastructure", "sqlite", "prepared_upgrade_migration_recipe.go"): true,
		filepath.Join("infrastructure", "sqlite", "migrate.go"):                           true,
		filepath.Join("application", "types", "archive_segments_nonempty.go"):             true,
	}
	assertRuntimeSymbolAbsence(t, archivePattern, allowArchive, "ArchiveSegment/StoreArchive")
	assertRuntimeSymbolAbsence(t, fileRetentionPattern, map[string]bool{}, "FileRetention/file_retention")
}

func TestMemoryEdgeSymbolsAreAbsentFromRuntimeSources(t *testing.T) {
	t.Parallel()
	pattern := regexp.MustCompile(`MemoryEdge|memory_edges`)
	allow := map[string]bool{
		filepath.Join("infrastructure", "sqlite", "drop_memory_edges.go"):                 true,
		filepath.Join("infrastructure", "sqlite", "prepared_migration_catalog.go"):        true,
		filepath.Join("infrastructure", "sqlite", "prepared_upgrade_verifier.go"):         true,
		filepath.Join("infrastructure", "sqlite", "prepared_upgrade_migration_recipe.go"): true,
		filepath.Join("infrastructure", "sqlite", "migrate.go"):                           true,
		filepath.Join("application", "types", "memory_edges_nonempty.go"):                 true,
		filepath.Join("application", "usecase", "bundle_rows.go"):                         true,
	}
	assertRuntimeSymbolAbsence(t, pattern, allow, "MemoryEdge/memory_edges")
	for path := range allow {
		assertFileMatches(t, path, pattern)
	}
}

func TestHookArchiveThenGCModeIsGone(t *testing.T) {
	t.Parallel()
	pattern := regexp.MustCompile(`archive_then_gc|runOpportunisticArchiveThenGC|hook_archive_auto`)
	err := filepath.WalkDir("presentation", func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || strings.HasSuffix(path, "_test.go") || !strings.HasSuffix(path, ".go") {
			return nil
		}
		body, readErr := os.ReadFile(path)
		if readErr != nil {
			return xerrors.Errorf("read %s: %w", path, readErr)
		}
		if pattern.Match(body) {
			t.Errorf("%s still names archive_then_gc / hook_archive_auto", path)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}
