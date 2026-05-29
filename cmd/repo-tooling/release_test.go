package main

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestBumpVersionAcrossRepo_RewritesAllMarkers verifies the Go port of
// bump_version.py rewrites VERSION, every plugin manifest's first version
// field, and the landing markers (hero eyebrow major.minor, Homebrew bottle /
// Cellar full X.Y.Z) without emitting "marker not found" warnings when the
// markers are present and change.
func TestBumpVersionAcrossRepo_RewritesAllMarkers(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	write := func(rel, content string) {
		full := filepath.Join(root, rel)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatalf("MkdirAll(%s) error = %v", rel, err)
		}
		if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
			t.Fatalf("WriteFile(%s) error = %v", rel, err)
		}
	}
	write("VERSION", "0.1.0\n")
	for _, manifest := range bumpManifests {
		write(manifest, `{"name": "traceary", "version": "0.1.0"}`+"\n")
	}
	write("docs/landing/index.html", `<span class="hero-eyebrow"><span class="dot"></span>v0.1</span>`)
	write("docs/landing/components.jsx", "traceary--0.1.0 /Cellar/traceary/0.1.0")

	var errBuf bytes.Buffer
	if err := bumpVersionAcrossRepo(io.Discard, &errBuf, root, "1.2.3"); err != nil {
		t.Fatalf("bumpVersionAcrossRepo() error = %v", err)
	}
	if errBuf.Len() != 0 {
		t.Fatalf("bumpVersionAcrossRepo() emitted warnings on a fully-marked tree: %s", errBuf.String())
	}

	assertContains := func(rel, want string) {
		data, err := os.ReadFile(filepath.Join(root, rel))
		if err != nil {
			t.Fatalf("ReadFile(%s) error = %v", rel, err)
		}
		if !strings.Contains(string(data), want) {
			t.Fatalf("%s does not contain %q after bump:\n%s", rel, want, data)
		}
	}
	if got, err := os.ReadFile(filepath.Join(root, "VERSION")); err != nil || strings.TrimSpace(string(got)) != "1.2.3" {
		t.Fatalf("VERSION = %q (err=%v), want 1.2.3", string(got), err)
	}
	for _, manifest := range bumpManifests {
		assertContains(manifest, `"version": "1.2.3"`)
	}
	assertContains("docs/landing/index.html", `<span class="hero-eyebrow"><span class="dot"></span>v1.2`)
	assertContains("docs/landing/components.jsx", "traceary--1.2.3")
	assertContains("docs/landing/components.jsx", "/Cellar/traceary/1.2.3")
}

// TestBumpVersionAcrossRepo_RejectsNonSemver pins that a non-X.Y.Z version is
// rejected before any file is written.
func TestBumpVersionAcrossRepo_RejectsNonSemver(t *testing.T) {
	t.Parallel()

	if err := bumpVersionAcrossRepo(io.Discard, io.Discard, t.TempDir(), "1.2"); err == nil {
		t.Fatal("bumpVersionAcrossRepo(\"1.2\") = nil, want a format error")
	}
}

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
