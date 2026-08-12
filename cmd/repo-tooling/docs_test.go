package main

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/google/go-cmp/cmp"
)

func TestVerifyDocsCommands(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name         string
		path         string
		content      string
		wantProblems []string
		wantFenced   []string
		wantInline   []string
		wantSkipped  []docsCommandSkipped
	}{
		{
			name:         "real leaf command in fenced block passes",
			path:         "docs/example.md",
			content:      "`traceary doctor`\n\n```sh\ntraceary doctor\n```\n",
			wantProblems: []string{},
		},
		{
			name:         "unresolvable inline path fails",
			path:         "docs/example.md",
			content:      "Use `traceary store search-projection rebuild` here.\n",
			wantProblems: []string{"docs/example.md:1: traceary store search-projection rebuild does not resolve to a command"},
		},
		{
			name:         "unresolvable fenced path fails",
			path:         "docs/example.md",
			content:      "```sh\ntraceary store search-projection rebuild\n```\n",
			wantProblems: []string{"docs/example.md:2: traceary store search-projection rebuild does not resolve to a command"},
		},
		{
			name:       "group command in a fence fails",
			path:       "docs/example.md",
			content:    "```sh\ntraceary store backup\n```\n",
			wantFenced: []string{"docs/example.md:2: traceary store backup is a group command and does not execute an action; use one of its subcommands: create, restore"},
		},
		{
			name:       "group command inline is reported without failing",
			path:       "docs/example.md",
			content:    "Run `traceary store backup` before upgrading.\n",
			wantInline: []string{"docs/example.md:1: traceary store backup is a group command and does not execute an action; use one of its subcommands: create, restore"},
		},
		{
			name:         "leaf command followed by flags and arguments passes",
			path:         "docs/example.md",
			content:      "```sh\ntraceary doctor --json RUN_ID | cat\n```\n",
			wantProblems: []string{},
		},
		{
			name:         "historical changelog section is skipped",
			path:         "CHANGELOG.md",
			content:      "# Changelog\n\n## [v0.34.0]\n\n`traceary doctor`\n\n## [v0.33.1]\n\n`traceary removed-command`\n",
			wantProblems: []string{},
			wantSkipped:  []docsCommandSkipped{{Path: "CHANGELOG.md", Reason: "historical release sections excluded"}},
		},
		{
			name:         "changelog without current release fails loudly",
			path:         "CHANGELOG.md",
			content:      "# Changelog\n\n`traceary removed-command`\n",
			wantProblems: nil,
			wantSkipped:  nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			root := t.TempDir()
			path := filepath.Join(root, tt.path)
			if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
				t.Fatalf("MkdirAll() error = %v", err)
			}
			if err := os.WriteFile(path, []byte(tt.content), 0o644); err != nil {
				t.Fatalf("WriteFile() error = %v", err)
			}
			report, err := verifyDocsCommands(root)
			if tt.name == "changelog without current release fails loudly" {
				if err == nil {
					t.Fatal("verifyDocsCommands() error = nil, want missing current release error")
				}
				return
			}
			if err != nil {
				t.Fatalf("verifyDocsCommands() error = %v", err)
			}
			wantProblems := tt.wantProblems
			if wantProblems == nil {
				wantProblems = []string{}
			}
			wantSkipped := tt.wantSkipped
			if wantSkipped == nil {
				wantSkipped = []docsCommandSkipped{}
			}
			wantFenced := tt.wantFenced
			if wantFenced == nil {
				wantFenced = []string{}
			}
			wantInline := tt.wantInline
			if wantInline == nil {
				wantInline = []string{}
			}
			if diff := cmp.Diff(wantProblems, report.Problems); diff != "" {
				t.Errorf("problems mismatch (-want +got):\n%s", diff)
			}
			if diff := cmp.Diff(wantFenced, report.FencedGroupCommands); diff != "" {
				t.Errorf("fenced groups mismatch (-want +got):\n%s", diff)
			}
			if diff := cmp.Diff(wantInline, report.InlineGroupMentions); diff != "" {
				t.Errorf("inline mentions mismatch (-want +got):\n%s", diff)
			}
			if diff := cmp.Diff(wantSkipped, report.Skipped); diff != "" {
				t.Errorf("skipped mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

func TestWriteDocsCommandsSummaryOnSuccess(t *testing.T) {
	var out bytes.Buffer
	err := writeDocsCommandsSummary(&out, docsCommandReport{FilesScanned: 3})
	if err != nil {
		t.Fatalf("writeDocsCommandsSummary() error = %v", err)
	}
	want := "Summary: class 1 unresolved=0, class 2 fenced groups=0, class 3 inline group mentions=0, files scanned=3\n"
	if out.String() != want {
		t.Errorf("summary = %q, want %q", out.String(), want)
	}
}

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
