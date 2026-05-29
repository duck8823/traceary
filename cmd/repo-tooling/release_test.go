package main

import (
	"os"
	"path/filepath"
	"testing"
)

// TestVerifyChangelogReleases_PassesOnCurrentTree mirrors running
// scripts/verify_changelog_releases.py against the repository: the bilingual
// changelogs are in sync and cover the released tags (or, in a shallow clone
// with no tags, the no-tags warning path still passes).
func TestVerifyChangelogReleases_PassesOnCurrentTree(t *testing.T) {
	t.Parallel()

	root, err := findRepoRoot()
	if err != nil {
		t.Fatalf("findRepoRoot() error = %v", err)
	}
	if _, err := verifyChangelogReleases(root); err != nil {
		t.Fatalf("verifyChangelogReleases() error = %v", err)
	}
}

// TestVerifyChangelogReleases_FlagsBilingualMismatch pins that diverging
// English/Japanese release headings fail before any git lookup.
func TestVerifyChangelogReleases_FlagsBilingualMismatch(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	write := func(name, content string) {
		if err := os.WriteFile(filepath.Join(root, name), []byte(content), 0o644); err != nil {
			t.Fatalf("WriteFile(%s) error = %v", name, err)
		}
	}
	write("VERSION", "0.2.0\n")
	write("CHANGELOG.md", "# Changelog\n\n## [v0.2.0] - 2026-01-02\n\n- thing\n")
	write("CHANGELOG.ja.md", "# Changelog\n\n## [v0.1.0] - 2026-01-01\n\n- thing\n")

	if _, err := verifyChangelogReleases(root); err == nil {
		t.Fatal("verifyChangelogReleases() = nil, want an error for mismatched bilingual changelogs")
	}
}
