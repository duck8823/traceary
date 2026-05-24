package cli_test

import (
	"bytes"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strconv"
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
		"../../README.ja.md",
		"../../docs/cli/README.md",
		"../../docs/cli/README.ja.md",
		"../../docs/interactive/README.md",
		"../../docs/interactive/README.ja.md",
		"../../docs/memory/README.md",
		"../../docs/memory/README.ja.md",
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

func TestProductionCliStringLiteralsDoNotExposeLegacyMemoryCandidateGlossary(t *testing.T) {
	paths, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatalf("glob production cli sources: %v", err)
	}
	for _, path := range paths {
		if strings.HasSuffix(path, "_test.go") {
			continue
		}
		t.Run(path, func(t *testing.T) {
			fileSet := token.NewFileSet()
			file, err := parser.ParseFile(fileSet, path, nil, 0)
			if err != nil {
				t.Fatalf("parse %s: %v", path, err)
			}
			ast.Inspect(file, func(node ast.Node) bool {
				lit, ok := node.(*ast.BasicLit)
				if !ok || lit.Kind != token.STRING {
					return true
				}
				value, err := strconv.Unquote(lit.Value)
				if err != nil {
					t.Fatalf("unquote %s: %v", fileSet.Position(lit.Pos()), err)
				}
				text := normalizeLegacyGlossaryText(value)
				for _, forbidden := range legacyMemoryCandidateGlossaryTerms() {
					if strings.Contains(text, forbidden) {
						t.Fatalf("%s leaked legacy glossary %q:\n%s", fileSet.Position(lit.Pos()), forbidden, value)
					}
				}
				return true
			})
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
		"generated candidate",
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
