package main

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"golang.org/x/xerrors"
)

var runLineageSymbolPattern = regexp.MustCompile(`run_lineage|RunLineage`)

// allowListedRunLineageFiles are the §3 remnants that must keep the retired
// table name: the bounded legacy importer, preflight message/SQL, and nothing
// else in non-test runtime sources.
var allowListedRunLineageFiles = map[string]bool{
	filepath.Join("application", "usecase", "bundle_rows.go"):                  true,
	filepath.Join("application", "types", "bound_drop.go"):                     true,
	filepath.Join("infrastructure", "sqlite", "bound_drop.go"):                 true,
	filepath.Join("infrastructure", "sqlite", "prepared_migration_catalog.go"): true,
}

func TestDroppedRunLineageSymbolsAreAbsentFromRuntimeSources(t *testing.T) {
	t.Parallel()
	roots := []string{"domain", "application", "presentation", "infrastructure"}
	var unexpected []string
	for _, root := range roots {
		err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() {
				if path == filepath.Join("infrastructure", "sqlite", "sql") {
					return nil
				}
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
			if allowListedRunLineageFiles[path] {
				return nil
			}
			contents, readErr := os.ReadFile(path)
			if readErr != nil {
				return xerrors.Errorf("read %s: %w", path, readErr)
			}
			if loc := runLineageSymbolPattern.FindIndex(contents); loc != nil {
				unexpected = append(unexpected, path+": "+string(contents[loc[0]:loc[1]]))
			}
			return nil
		})
		if err != nil {
			t.Fatalf("failed to walk %s: %v", root, err)
		}
	}
	if len(unexpected) > 0 {
		t.Fatalf("retired run_lineages symbols leaked into runtime sources:\n%s", strings.Join(unexpected, "\n"))
	}
}

func TestAllowListedRetiredEntryFilesStillNameTheTable(t *testing.T) {
	t.Parallel()
	for path := range allowListedRunLineageFiles {
		contents, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read %s: %v", path, err)
		}
		if !runLineageSymbolPattern.Match(contents) {
			t.Fatalf("%s is allow-listed but no longer names the retired table", path)
		}
	}
}
