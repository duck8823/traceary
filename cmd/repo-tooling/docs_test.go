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

// TestVerifyAntigravityStatus_PassesOnCurrentTree pins that the current docs
// tree carries no stale "Antigravity has no hook / captures nothing" wording
// after the v0.21.1 supported-state update.
func TestVerifyAntigravityStatus_PassesOnCurrentTree(t *testing.T) {
	t.Parallel()

	root, err := findRepoRoot()
	if err != nil {
		t.Fatalf("findRepoRoot() error = %v", err)
	}
	problems, err := verifyAntigravityStatus(root)
	if err != nil {
		t.Fatalf("verifyAntigravityStatus() error = %v", err)
	}
	if len(problems) > 0 {
		t.Fatalf("verifyAntigravityStatus() reported stale wording on the current tree: %v", problems)
	}
}

// TestVerifyAntigravityStatus_FlagsStaleWording pins that a reintroduced stale
// current-state claim is reported, while legitimate historical wording is not.
func TestVerifyAntigravityStatus_FlagsStaleWording(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	docsDir := filepath.Join(root, "docs")
	if err := os.MkdirAll(docsDir, 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	stale := "# Doc\n\n[日本語](./stale.ja.md)\n\nThe successor host, Antigravity, emits no Traceary lifecycle events yet.\n"
	if err := os.WriteFile(filepath.Join(docsDir, "stale.md"), []byte(stale), 0o644); err != nil {
		t.Fatalf("WriteFile(stale.md) error = %v", err)
	}
	// Historical phrasing that must NOT trip the guard.
	ok := "# Doc\n\n[English](./stale.md)\n\nv0.21.0 shipped diagnostics only because no public contract was confirmed at the time.\n"
	if err := os.WriteFile(filepath.Join(docsDir, "stale.ja.md"), []byte(ok), 0o644); err != nil {
		t.Fatalf("WriteFile(stale.ja.md) error = %v", err)
	}

	problems, err := verifyAntigravityStatus(root)
	if err != nil {
		t.Fatalf("verifyAntigravityStatus() error = %v", err)
	}
	if len(problems) != 1 {
		t.Fatalf("verifyAntigravityStatus() problems = %v, want exactly one stale finding", problems)
	}
}

// TestVerifyAntigravityStatus_SkipsChangelog pins that intentionally historical
// CHANGELOG wording is exempt from the stale-status scan.
func TestVerifyAntigravityStatus_SkipsChangelog(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	changelog := "# Changelog\n\n[日本語](./CHANGELOG.ja.md)\n\nv0.21.0: Antigravity emits no Traceary lifecycle events yet.\n"
	if err := os.WriteFile(filepath.Join(root, "CHANGELOG.md"), []byte(changelog), 0o644); err != nil {
		t.Fatalf("WriteFile(CHANGELOG.md) error = %v", err)
	}

	problems, err := verifyAntigravityStatus(root)
	if err != nil {
		t.Fatalf("verifyAntigravityStatus() error = %v", err)
	}
	if len(problems) != 0 {
		t.Fatalf("verifyAntigravityStatus() flagged CHANGELOG wording: %v", problems)
	}
}

// TestVerifyLanding_PassesOnCurrentTree is the Go equivalent of running
// scripts/verify_landing.py against the repository.
func TestVerifyLanding_PassesOnCurrentTree(t *testing.T) {
	t.Parallel()

	root, err := findRepoRoot()
	if err != nil {
		t.Fatalf("findRepoRoot() error = %v", err)
	}
	if _, err := verifyLanding(root); err != nil {
		t.Fatalf("verifyLanding() error = %v", err)
	}
}

// TestVerifyLanding_FlagsEyebrowDrift pins that a hero eyebrow whose
// major.minor differs from VERSION is reported.
func TestVerifyLanding_FlagsEyebrowDrift(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "VERSION"), []byte("0.2.0\n"), 0o644); err != nil {
		t.Fatalf("WriteFile(VERSION) error = %v", err)
	}
	landingDir := filepath.Join(root, "docs", "landing")
	if err := os.MkdirAll(landingDir, 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(landingDir, "index.html"),
		[]byte(`<span class="hero-eyebrow"><span class="dot"></span>v0.1 stale</span>`), 0o644); err != nil {
		t.Fatalf("WriteFile(index.html) error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(landingDir, "components.jsx"),
		[]byte("traceary--0.2.0 /Cellar/traceary/0.2.0"), 0o644); err != nil {
		t.Fatalf("WriteFile(components.jsx) error = %v", err)
	}

	if _, err := verifyLanding(root); err == nil {
		t.Fatal("verifyLanding() = nil, want an error for a drifted hero eyebrow")
	}
}
