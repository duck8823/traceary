package sqlite

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestRuntimeSQLiteOpenInventoryIsExplicit(t *testing.T) {
	_, current, _, _ := runtime.Caller(0)
	root := filepath.Clean(filepath.Join(filepath.Dir(current), "..", ".."))
	allowed := map[string]int{
		"infrastructure/filesystem/file_retention_datasource_unix.go": 1, // copied backup FD verification.
		"infrastructure/sqlite/compaction_sqlite.go":                  2, // EX-held source/candidate only.
		"infrastructure/sqlite/database.go":                           1, // in-memory driver probe only.
		"infrastructure/sqlite/payload_rehearsal.go":                  6, // copied rehearsal targets only.
		"infrastructure/sqlite/payload_rehearsal_migration.go":        1, // copied migration target.
		"infrastructure/sqlite/payload_rehearsal_target.go":           1, // copied rehearsal target.
	}
	seen := map[string]int{}
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil || entry.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return walkErr
		}
		body, readErr := os.ReadFile(path)
		if readErr != nil {
			return fmt.Errorf("read runtime SQLite opener: %w", readErr)
		}
		count := bytes.Count(body, []byte(`sql.Open("sqlite"`))
		if count == 0 {
			return nil
		}
		rel, _ := filepath.Rel(root, path)
		seen[filepath.ToSlash(rel)] = count
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	for path, count := range seen {
		if allowed[path] != count {
			t.Errorf("unclassified direct SQLite opens: %s count=%d", path, count)
		}
	}
	for path, count := range allowed {
		if seen[path] != count {
			t.Errorf("direct SQLite inventory changed: %s got=%d want=%d", path, seen[path], count)
		}
	}
}
