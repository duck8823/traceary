package main

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
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
	verifyLandingCmd := &cobra.Command{
		Use:   "verify-landing",
		Short: "Verify docs/landing/ version markers stay in sync with VERSION",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			root, err := findRepoRoot()
			if err != nil {
				return err
			}
			version, err := verifyLanding(root)
			if err != nil {
				return err
			}
			if _, err := fmt.Fprintf(cmd.OutOrStdout(), "OK: docs/landing/ in sync with VERSION %s\n", version); err != nil {
				return xerrors.Errorf("failed to write verify result: %w", err)
			}
			return nil
		},
	}
	cmd.AddCommand(verifyI18n)
	cmd.AddCommand(verifyLandingCmd)
	return cmd
}

var (
	landingVersionRe = regexp.MustCompile(`^\d+\.\d+\.\d+$`)
	landingEyebrowRe = regexp.MustCompile(`<span class="hero-eyebrow"><span class="dot"></span>v(\d+\.\d+)\b`)
	landingBottleRe  = regexp.MustCompile(`traceary--(\d+\.\d+\.\d+)`)
	landingCellarRe  = regexp.MustCompile(`/Cellar/traceary/(\d+\.\d+\.\d+)`)
)

// verifyLanding reproduces scripts/verify_landing.py: the landing page's hero
// eyebrow (major.minor) and the Homebrew bottle / Cellar version markers
// (full X.Y.Z) must stay in sync with VERSION. It returns the validated VERSION
// string on success.
func verifyLanding(root string) (string, error) {
	versionData, err := os.ReadFile(filepath.Join(root, "VERSION"))
	if err != nil {
		return "", xerrors.Errorf("missing VERSION")
	}
	version := strings.TrimSpace(string(versionData))
	if !landingVersionRe.MatchString(version) {
		return "", xerrors.Errorf("VERSION is not X.Y.Z: %q", version)
	}
	parts := strings.Split(version, ".")
	majorMinor := parts[0] + "." + parts[1]

	indexPath := filepath.Join("docs", "landing", "index.html")
	indexText, err := os.ReadFile(filepath.Join(root, indexPath))
	if err != nil {
		return "", xerrors.Errorf("missing %s", indexPath)
	}
	eyebrow := landingEyebrowRe.FindStringSubmatch(string(indexText))
	if eyebrow == nil {
		return "", xerrors.Errorf("%s: hero eyebrow version marker not found", indexPath)
	}
	if eyebrow[1] != majorMinor {
		return "", xerrors.Errorf("%s: hero eyebrow says v%s but VERSION is %s (expected v%s)", indexPath, eyebrow[1], version, majorMinor)
	}

	componentsPath := filepath.Join("docs", "landing", "components.jsx")
	componentsText, err := os.ReadFile(filepath.Join(root, componentsPath))
	if err != nil {
		return "", xerrors.Errorf("missing %s", componentsPath)
	}
	components := string(componentsText)

	if err := checkLandingMarkerDrift(componentsPath, components, landingBottleRe, version,
		"no `traceary--X.Y.Z` bottle markers found", "bottle versions"); err != nil {
		return "", err
	}
	if err := checkLandingMarkerDrift(componentsPath, components, landingCellarRe, version,
		"no `/Cellar/traceary/X.Y.Z` markers found", "Cellar versions"); err != nil {
		return "", err
	}

	return version, nil
}

func checkLandingMarkerDrift(path, text string, pattern *regexp.Regexp, version, emptyMessage, driftLabel string) error {
	matches := pattern.FindAllStringSubmatch(text, -1)
	if len(matches) == 0 {
		return xerrors.Errorf("%s: %s", path, emptyMessage)
	}
	driftSet := map[string]bool{}
	for _, match := range matches {
		if match[1] != version {
			driftSet[match[1]] = true
		}
	}
	if len(driftSet) > 0 {
		drift := make([]string, 0, len(driftSet))
		for value := range driftSet {
			drift = append(drift, value)
		}
		sort.Strings(drift)
		return xerrors.Errorf("%s: %s [%s] do not match VERSION %s", path, driftLabel, strings.Join(drift, " "), version)
	}
	return nil
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
