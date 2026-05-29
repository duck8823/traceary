package main

import (
	"os"
	"path/filepath"
	"testing"
)

// TestVerifyDocsI18n_PassesOnCurrentTree is the Go equivalent of running
// scripts/verify_docs_i18n.py against the repository: every in-scope doc has a
// language pair and a top-of-file language-switch link.
func TestVerifyDocsI18n_PassesOnCurrentTree(t *testing.T) {
	t.Parallel()

	root, err := findRepoRoot()
	if err != nil {
		t.Fatalf("findRepoRoot() error = %v", err)
	}
	problems, err := verifyDocsI18n(root)
	if err != nil {
		t.Fatalf("verifyDocsI18n() error = %v", err)
	}
	if len(problems) > 0 {
		t.Fatalf("verifyDocsI18n() reported problems on the current tree: %v", problems)
	}
}

// TestVerifyDocsI18n_FlagsMissingPair pins that an in-scope English doc without
// a Japanese pair is reported rather than silently passing.
func TestVerifyDocsI18n_FlagsMissingPair(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "README.md"), []byte("# Title\n"), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	problems, err := verifyDocsI18n(root)
	if err != nil {
		t.Fatalf("verifyDocsI18n() error = %v", err)
	}
	if len(problems) == 0 {
		t.Fatal("verifyDocsI18n() reported no problems for an unpaired English doc, want a missing-pair problem")
	}
}
