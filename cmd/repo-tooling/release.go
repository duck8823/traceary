package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/spf13/cobra"
	"golang.org/x/xerrors"
)

var (
	semverTagRe        = regexp.MustCompile(`^v\d+\.\d+\.\d+$`)
	changelogHeadingRe = regexp.MustCompile(`(?m)^## \[(v\d+\.\d+\.\d+)\] - `)
)

func newReleaseCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "release",
		Short: "Release-preparation checks",
	}
	verifyChangelog := &cobra.Command{
		Use:   "verify-changelog",
		Short: "Verify bilingual changelog coverage for released versions",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			root, err := findRepoRoot()
			if err != nil {
				return err
			}
			warnings, err := verifyChangelogReleases(root)
			if err != nil {
				return err
			}
			for _, warning := range warnings {
				_, _ = fmt.Fprintln(cmd.ErrOrStderr(), "warning: "+warning)
			}
			if _, err := fmt.Fprintln(cmd.OutOrStdout(), "changelog release coverage check passed"); err != nil {
				return xerrors.Errorf("failed to write verify result: %w", err)
			}
			return nil
		},
	}
	cmd.AddCommand(verifyChangelog)
	return cmd
}

// verifyChangelogReleases reproduces scripts/verify_changelog_releases.py: the
// English and Japanese changelogs must list the same release headings in the
// same order, must include the current VERSION, and must cover every released
// git tag up to the current VERSION. It returns non-fatal warnings (e.g. a
// shallow clone with no tags) alongside a nil error when the check passes.
func verifyChangelogReleases(root string) ([]string, error) {
	current, err := readReleaseVersion(root)
	if err != nil {
		return nil, err
	}
	enVersions, err := readChangelogVersions(root, "CHANGELOG.md")
	if err != nil {
		return nil, err
	}
	jaVersions, err := readChangelogVersions(root, "CHANGELOG.ja.md")
	if err != nil {
		return nil, err
	}

	if !equalStrings(enVersions, jaVersions) {
		var problems []string
		if missing := missingFrom(enVersions, jaVersions); len(missing) > 0 {
			problems = append(problems, "missing in CHANGELOG.ja.md: "+strings.Join(missing, ", "))
		}
		if missing := missingFrom(jaVersions, enVersions); len(missing) > 0 {
			problems = append(problems, "missing in CHANGELOG.md: "+strings.Join(missing, ", "))
		}
		if len(problems) == 0 {
			problems = append(problems, "release heading order differs between CHANGELOG.md and CHANGELOG.ja.md")
		}
		return nil, xerrors.Errorf("%s", strings.Join(problems, "; "))
	}

	if !containsString(enVersions, current) {
		return nil, xerrors.Errorf("CHANGELOG.md is missing the current VERSION entry %s", current)
	}
	if !containsString(jaVersions, current) {
		return nil, xerrors.Errorf("CHANGELOG.ja.md is missing the current VERSION entry %s", current)
	}

	tags, err := gitReleaseTags(root)
	if err != nil {
		return nil, err
	}
	if len(tags) == 0 {
		return []string{"no semantic release tags were found locally; verified only VERSION and bilingual changelog parity"}, nil
	}

	currentKey := versionKey(current)
	var missing []string
	for _, tag := range tags {
		if versionKeyLessOrEqual(versionKey(tag), currentKey) && !containsString(enVersions, tag) {
			missing = append(missing, tag)
		}
	}
	if len(missing) > 0 {
		return nil, xerrors.Errorf("missing changelog coverage for released tags: %s (up to the current VERSION; run in a full clone or update CHANGELOG.md / CHANGELOG.ja.md)", strings.Join(missing, ", "))
	}

	return nil, nil
}

func readReleaseVersion(root string) (string, error) {
	data, err := os.ReadFile(filepath.Join(root, "VERSION"))
	if err != nil {
		return "", xerrors.Errorf("missing file: VERSION")
	}
	version := strings.TrimSpace(string(data))
	tag := "v" + version
	if !semverTagRe.MatchString(tag) {
		return "", xerrors.Errorf("VERSION must contain a semantic version like 0.5.0, got %q", version)
	}
	return tag, nil
}

func readChangelogVersions(root, name string) ([]string, error) {
	data, err := os.ReadFile(filepath.Join(root, name))
	if err != nil {
		return nil, xerrors.Errorf("missing file: %s", name)
	}
	matches := changelogHeadingRe.FindAllStringSubmatch(string(data), -1)
	versions := make([]string, 0, len(matches))
	for _, match := range matches {
		versions = append(versions, match[1])
	}
	if len(versions) == 0 {
		return nil, xerrors.Errorf("%s does not contain any release headings", name)
	}

	seen := map[string]int{}
	for _, version := range versions {
		seen[version]++
	}
	var duplicates []string
	for version, count := range seen {
		if count > 1 {
			duplicates = append(duplicates, version)
		}
	}
	if len(duplicates) > 0 {
		sort.Strings(duplicates)
		return nil, xerrors.Errorf("%s contains duplicate release headings: %s", name, strings.Join(duplicates, ", "))
	}

	return versions, nil
}

func gitReleaseTags(root string) ([]string, error) {
	cmd := exec.Command("git", "tag", "--list", "v*", "--sort=version:refname")
	cmd.Dir = root
	output, err := cmd.Output()
	if err != nil {
		return nil, xerrors.Errorf("failed to list git release tags: %w", err)
	}
	var tags []string
	for _, line := range strings.Split(string(output), "\n") {
		tag := strings.TrimSpace(line)
		if semverTagRe.MatchString(tag) {
			tags = append(tags, tag)
		}
	}
	return tags, nil
}

func versionKey(tag string) [3]int {
	parts := strings.SplitN(strings.TrimPrefix(tag, "v"), ".", 3)
	var key [3]int
	for i := 0; i < 3 && i < len(parts); i++ {
		key[i], _ = strconv.Atoi(parts[i])
	}
	return key
}

func versionKeyLessOrEqual(a, b [3]int) bool {
	for i := 0; i < 3; i++ {
		if a[i] != b[i] {
			return a[i] < b[i]
		}
	}
	return true
}

func missingFrom(want, have []string) []string {
	set := toSet(have)
	var missing []string
	for _, value := range want {
		if !set[value] {
			missing = append(missing, value)
		}
	}
	return missing
}

func containsString(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}
