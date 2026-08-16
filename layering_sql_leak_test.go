package main

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"golang.org/x/xerrors"
)

// sqlConnectionTypePattern matches a connection-typed reference (*sql.DB or
// *sql.Conn) as a Go identifier, not a substring of an unrelated word.
var sqlConnectionTypePattern = regexp.MustCompile(`\*sql\.(DB|Conn)\b`)

// TestDomainAndApplicationDoNotLeakSQLConnectionTypes guards the layer
// boundary #1722 depends on: Database.WithReadScope hands datasources a
// shared *sql.DB internally, but that connection type must never surface in
// a domain or application signature. If it does, the read-scope primitive
// (or some unrelated change) has punched a hole through the repository
// interface that hides infrastructure lifetime management from callers who
// cannot see it.
func TestDomainAndApplicationDoNotLeakSQLConnectionTypes(t *testing.T) {
	t.Parallel()
	for _, root := range []string{"domain", "application"} {
		root := root
		t.Run(root, func(t *testing.T) {
			t.Parallel()
			err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
				if err != nil {
					return err
				}
				if d.IsDir() || !strings.HasSuffix(path, ".go") {
					return nil
				}
				contents, readErr := os.ReadFile(path)
				if readErr != nil {
					return xerrors.Errorf("read %s: %w", path, readErr)
				}
				if loc := sqlConnectionTypePattern.FindIndex(contents); loc != nil {
					t.Errorf("%s references a connection-typed value (%q); domain/application must depend only on repository interfaces, never *sql.DB or *sql.Conn", path, contents[loc[0]:loc[1]])
				}
				return nil
			})
			if err != nil {
				t.Fatalf("failed to walk %s: %v", root, err)
			}
		})
	}
}
