package cli_test

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"github.com/duck8823/traceary/presentation/cli"
)

func TestVisibleHelpDoesNotExposeLegacyMemoryCandidateGlossary(t *testing.T) {
	for _, locale := range []string{"en", "ja"} {
		t.Run(locale, func(t *testing.T) {
			t.Setenv("TRACEARY_LANG", locale)

			for _, path := range visibleCommandPaths(cli.NewRootCLI().Command(), nil) {
				name := "traceary"
				if len(path) > 0 {
					name += " " + strings.Join(path, " ")
				}
				t.Run(name, func(t *testing.T) {
					root := cli.NewRootCLI()
					cmd := root.Command()
					stdout := &bytes.Buffer{}
					cmd.SetOut(stdout)
					cmd.SetErr(&bytes.Buffer{})
					args := append(append([]string{}, path...), "--help")
					cmd.SetArgs(args)

					if err := cmd.Execute(); err != nil {
						t.Fatalf("execute %v: %v", args, err)
					}
					help := normalizeLegacyGlossaryText(stdout.String())
					for _, forbidden := range legacyMemoryCandidateGlossaryTerms() {
						if strings.Contains(help, forbidden) {
							t.Fatalf("help for %s leaked legacy glossary %q:\n%s", name, forbidden, help)
						}
					}
				})
			}
		})
	}
}

func TestDocumentationAndGoldensDoNotExposeLegacyMemoryCandidateGlossary(t *testing.T) {
	paths := []string{
		"../../README.md",
		"../../docs/cli/README.md",
		"../../docs/cli/README.ja.md",
		"../../docs/interactive/README.md",
		"../../docs/interactive/README.ja.md",
		"testdata/top/snapshot_text.golden",
		"testdata/top/snapshot_text_empty.golden",
	}
	cockpitGoldens, err := filepath.Glob("testdata/cockpit/*.golden.txt")
	if err != nil {
		t.Fatalf("glob cockpit goldens: %v", err)
	}
	paths = append(paths, cockpitGoldens...)

	for _, path := range paths {
		t.Run(path, func(t *testing.T) {
			content, err := os.ReadFile(path)
			if err != nil {
				t.Fatalf("read %s: %v", path, err)
			}
			text := normalizeLegacyGlossaryText(string(content))
			for _, forbidden := range legacyMemoryCandidateGlossaryTerms() {
				if strings.Contains(text, forbidden) {
					t.Fatalf("%s leaked legacy glossary %q:\n%s", path, forbidden, text)
				}
			}
		})
	}
}

func legacyMemoryCandidateGlossaryTerms() []string {
	return []string{
		"candidate durable memory",
		"candidate durable memories",
		"candidate memory",
		"candidate memories",
		"candidate inbox",
		"durable memory candidate",
		"durable memory candidates",
		"durable-memory candidate",
		"durable-memory candidates",
		"source candidate",
		"traceary memory distill",
		"traceary memory extract",
		"候補 durable memory",
		"候補 memory",
		"candidate な durable memory",
		"低品質 candidate",
	}
}

func normalizeLegacyGlossaryText(text string) string {
	text = strings.ReplaceAll(text, "CANDIDATE MEMORIES", "")
	return strings.ToLower(text)
}

func visibleCommandPaths(cmd *cobra.Command, prefix []string) [][]string {
	paths := [][]string{append([]string{}, prefix...)}
	for _, child := range cmd.Commands() {
		if child.Hidden {
			continue
		}
		childPath := append(append([]string{}, prefix...), child.Name())
		paths = append(paths, visibleCommandPaths(child, childPath)...)
	}
	return paths
}
