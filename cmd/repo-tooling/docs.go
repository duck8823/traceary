package main

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/spf13/cobra"
	"golang.org/x/xerrors"
)

// docsI18nTopLines is the number of leading lines scanned for the
// cross-language switch link.
const docsI18nTopLines = 8

// docsI18nExclude lists AI-agent instruction files that are not user-facing
// documentation and therefore need no en/ja pair.
var docsI18nExclude = map[string]bool{
	"CLAUDE.md": true,
	"AGENTS.md": true,
	"GEMINI.md": true,
}

func newDocsCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "docs",
		Short: "Documentation checks",
	}
	verifyI18n := &cobra.Command{
		Use:   "verify-i18n",
		Short: "Verify English/Japanese documentation pairs",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			root, err := findRepoRoot()
			if err != nil {
				return err
			}
			problems, err := verifyDocsI18n(root)
			if err != nil {
				return err
			}
			if len(problems) > 0 {
				// Fold the aggregate report into the returned error so cobra
				// surfaces it; SilenceErrors is left off so unexpected
				// findRepoRoot / walk failures stay visible in CLI/CI too.
				return xerrors.Errorf("documentation i18n check failed:\n- %s", strings.Join(problems, "\n- "))
			}
			if _, err := fmt.Fprintln(cmd.OutOrStdout(), "documentation i18n check passed"); err != nil {
				return xerrors.Errorf("failed to write verify result: %w", err)
			}
			return nil
		},
	}
	cmd.AddCommand(verifyI18n)
	return cmd
}

// verifyDocsI18n reproduces scripts/verify_docs_i18n.py: every in-scope English
// doc needs a `.ja.md` pair (and vice versa), and each must carry a
// cross-language switch link near the top. It returns the list of problems
// (empty means the check passed) rather than failing fast, mirroring the
// Python script's aggregate report.
func verifyDocsI18n(root string) ([]string, error) {
	english, japanese, err := collectMarkdownFiles(root)
	if err != nil {
		return nil, err
	}
	englishSet := toSet(english)
	japaneseSet := toSet(japanese)

	var problems []string

	sort.Strings(english)
	for _, path := range english {
		jaPath := jaVariant(path)
		if !japaneseSet[jaPath] {
			problems = append(problems, fmt.Sprintf("%s: missing Japanese pair %s", path, jaPath))
			continue
		}
		switchProblems, err := verifyLanguageSwitch(root, path, "[日本語]", expectedSwitchTargets(path))
		if err != nil {
			return nil, err
		}
		problems = append(problems, switchProblems...)
	}

	sort.Strings(japanese)
	for _, path := range japanese {
		enPath, ok := enVariant(path)
		if !ok {
			continue
		}
		if !englishSet[enPath] {
			problems = append(problems, fmt.Sprintf("%s: missing English pair %s", path, enPath))
			continue
		}
		switchProblems, err := verifyLanguageSwitch(root, path, "[English]", expectedSwitchTargets(path))
		if err != nil {
			return nil, err
		}
		problems = append(problems, switchProblems...)
	}

	return problems, nil
}

// collectMarkdownFiles returns the in-scope English and Japanese markdown paths
// (repo-relative): top-level `*.md` excluding the AI-agent instruction files,
// plus every `docs/**/*.md`.
func collectMarkdownFiles(root string) (english, japanese []string, err error) {
	classify := func(rel string) {
		if strings.HasSuffix(rel, ".ja.md") {
			japanese = append(japanese, rel)
			return
		}
		english = append(english, rel)
	}

	topLevel, err := os.ReadDir(root)
	if err != nil {
		return nil, nil, xerrors.Errorf("failed to read repository root: %w", err)
	}
	for _, entry := range topLevel {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".md") || docsI18nExclude[entry.Name()] {
			continue
		}
		classify(entry.Name())
	}

	docsDir := filepath.Join(root, "docs")
	if _, statErr := os.Stat(docsDir); statErr == nil {
		walkErr := filepath.WalkDir(docsDir, func(path string, d fs.DirEntry, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			if d.IsDir() || !strings.HasSuffix(d.Name(), ".md") {
				return nil
			}
			rel, relErr := filepath.Rel(root, path)
			if relErr != nil {
				return xerrors.Errorf("failed to resolve docs path %s: %w", path, relErr)
			}
			classify(rel)
			return nil
		})
		if walkErr != nil {
			return nil, nil, xerrors.Errorf("failed to walk docs directory: %w", walkErr)
		}
	}

	return english, japanese, nil
}

func jaVariant(path string) string {
	return strings.TrimSuffix(path, ".md") + ".ja.md"
}

func enVariant(path string) (string, bool) {
	if !strings.HasSuffix(path, ".ja.md") {
		return "", false
	}
	return strings.TrimSuffix(path, ".ja.md") + ".md", true
}

func expectedSwitchTargets(path string) []string {
	var pairName string
	if enPath, ok := enVariant(path); ok {
		pairName = filepath.Base(enPath)
	} else {
		pairName = filepath.Base(jaVariant(path))
	}
	return []string{"(" + pairName + ")", "(./" + pairName + ")"}
}

func verifyLanguageSwitch(root, path, expectedLabel string, expectedTargets []string) ([]string, error) {
	data, err := os.ReadFile(filepath.Join(root, path))
	if err != nil {
		return nil, xerrors.Errorf("failed to read %s: %w", path, err)
	}
	lines := strings.Split(string(data), "\n")
	if len(lines) > docsI18nTopLines {
		lines = lines[:docsI18nTopLines]
	}
	head := strings.Join(lines, "\n")

	var problems []string
	if !strings.Contains(head, expectedLabel) {
		problems = append(problems, fmt.Sprintf("%s: missing language switch label %q near the top", path, expectedLabel))
	}
	if !containsAny(head, expectedTargets) {
		problems = append(problems, fmt.Sprintf("%s: missing language switch target near the top; expected one of %s", path, strings.Join(quoteAll(expectedTargets), ", ")))
	}
	return problems, nil
}

func containsAny(haystack string, needles []string) bool {
	for _, needle := range needles {
		if strings.Contains(haystack, needle) {
			return true
		}
	}
	return false
}

func quoteAll(values []string) []string {
	quoted := make([]string, len(values))
	for i, value := range values {
		quoted[i] = fmt.Sprintf("%q", value)
	}
	return quoted
}

func toSet(values []string) map[string]bool {
	set := make(map[string]bool, len(values))
	for _, value := range values {
		set[value] = true
	}
	return set
}
