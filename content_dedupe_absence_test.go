package main

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"golang.org/x/xerrors"
)

var contentDedupeSymbolPattern = regexp.MustCompile(`ContentDedupe|content_dedupe`)

var deleteFromEventsPattern = regexp.MustCompile(`(?i)DELETE\s+FROM\s+events`)

// allowListedContentDedupeFiles are the v81 remnants that must keep the
// retired table name: the restore-or-refuse hook, Layer-2 verifier, catalog
// SHA pin, and the empty-canonical probe that refuses to DROP a non-empty
// archive inline. Nothing else in non-test runtime sources.
var allowListedContentDedupeFiles = map[string]bool{
	filepath.Join("infrastructure", "sqlite", "prepared_migration_catalog.go"): true,
	filepath.Join("infrastructure", "sqlite", "restore_dedupe_archive.go"):     true,
	filepath.Join("infrastructure", "sqlite", "drop_dedupe_archive.go"):        true,
	filepath.Join("infrastructure", "sqlite", "prepared_upgrade_verifier.go"):  true,
	filepath.Join("infrastructure", "sqlite", "migrate.go"):                    true,
	filepath.Join("application", "types", "dedupe_archive_restore.go"):         true,
}

var allowListedEventDeletionFiles = map[string]bool{
	filepath.Join("infrastructure", "sqlite", "sql", "delete_transcript_event.sql"): true,
}

func TestContentDedupeSymbolsAreAbsentFromRuntimeSources(t *testing.T) {
	t.Parallel()
	roots := []string{"domain", "application", "presentation", "infrastructure", "cmd"}
	var unexpected []string
	for _, root := range roots {
		err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() {
				return nil
			}
			if !strings.HasSuffix(path, ".go") && !strings.HasSuffix(path, ".sql") {
				return nil
			}
			if strings.HasSuffix(path, "_test.go") {
				return nil
			}
			if strings.Contains(path, filepath.Join("schema", "sqlite", "migrations")) {
				return nil
			}
			if allowListedContentDedupeFiles[path] {
				return nil
			}
			contents, readErr := os.ReadFile(path)
			if readErr != nil {
				return xerrors.Errorf("read %s: %w", path, readErr)
			}
			if loc := contentDedupeSymbolPattern.FindIndex(contents); loc != nil {
				unexpected = append(unexpected, path+": "+string(contents[loc[0]:loc[1]]))
			}
			return nil
		})
		if err != nil {
			t.Fatalf("failed to walk %s: %v", root, err)
		}
	}
	if len(unexpected) > 0 {
		t.Fatalf("content-dedupe symbols leaked into runtime sources:\n%s", strings.Join(unexpected, "\n"))
	}
}

func TestProductionEventDeletionIsOnlyKimiTranscriptSupersede(t *testing.T) {
	t.Parallel()
	roots := []string{"domain", "application", "presentation", "infrastructure", "cmd"}
	var unexpected []string
	for _, root := range roots {
		err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() {
				return nil
			}
			if !strings.HasSuffix(path, ".go") && !strings.HasSuffix(path, ".sql") {
				return nil
			}
			if strings.HasSuffix(path, "_test.go") {
				return nil
			}
			if strings.Contains(path, filepath.Join("schema", "sqlite", "migrations")) {
				return nil
			}
			if allowListedEventDeletionFiles[path] {
				return nil
			}
			contents, readErr := os.ReadFile(path)
			if readErr != nil {
				return xerrors.Errorf("read %s: %w", path, readErr)
			}
			if loc := deleteFromEventsPattern.FindIndex(contents); loc != nil {
				end := loc[1]
				if end+20 < len(contents) {
					end += 20
				} else {
					end = len(contents)
				}
				unexpected = append(unexpected, path+": "+string(contents[loc[0]:end]))
			}
			return nil
		})
		if err != nil {
			t.Fatalf("failed to walk %s: %v", root, err)
		}
	}
	if len(unexpected) > 0 {
		t.Fatalf("unexpected DELETE FROM events in runtime sources:\n%s", strings.Join(unexpected, "\n"))
	}
}

func TestAllowListedContentDedupeFilesStillNameTheTable(t *testing.T) {
	t.Parallel()
	for path := range allowListedContentDedupeFiles {
		contents, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read %s: %v", path, err)
		}
		if !contentDedupeSymbolPattern.Match(contents) {
			t.Fatalf("%s is allow-listed but no longer names the retired table", path)
		}
	}
}
