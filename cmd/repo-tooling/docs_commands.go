package main

import (
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/spf13/cobra"
	"golang.org/x/xerrors"

	"github.com/duck8823/traceary/presentation/cli"
)

var (
	docsCommandHeadingRe = regexp.MustCompile(`^## \[?v[0-9]+\.[0-9]+\.[0-9]+`)
	docsTracearyRe       = regexp.MustCompile(`\btraceary\b`)
	docsPlainWordRe      = regexp.MustCompile(`^[a-z][a-z0-9-]*$`)
)

type docsCommandSkipped struct {
	Path   string `json:"path"`
	Reason string `json:"reason"`
}

type docsCommandReport struct {
	Problems            []string             `json:"problems"`
	FencedGroupCommands []string             `json:"fenced_group_commands"`
	InlineGroupMentions []string             `json:"inline_group_mentions"`
	Skipped             []docsCommandSkipped `json:"skipped"`
	FilesScanned        int                  `json:"files_scanned"`
}

// verifyDocsCommands checks only command-shaped examples: fenced code blocks
// and inline code spans. Changelog history is deliberately excluded because it
// records commands from released versions, not instructions for this version.
func verifyDocsCommands(root string) (docsCommandReport, error) {
	paths, err := docsCommandPaths(root)
	if err != nil {
		return docsCommandReport{}, err
	}
	commandRoot := cli.NewRootCLI().Command()
	report := docsCommandReport{FilesScanned: len(paths)}
	var skipped []docsCommandSkipped
	for _, path := range paths {
		data, err := os.ReadFile(filepath.Join(root, path))
		if err != nil {
			return docsCommandReport{}, xerrors.Errorf("failed to read %s: %w", path, err)
		}
		text := string(data)
		if isChangelogPath(path) {
			var current bool
			text, current, err = currentChangelogSection(text)
			if err != nil {
				return docsCommandReport{}, xerrors.Errorf("%s: %w", path, err)
			}
			if !current {
				skipped = append(skipped, docsCommandSkipped{Path: path, Reason: "no current release section"})
				continue
			}
			skipped = append(skipped, docsCommandSkipped{Path: path, Reason: "historical release sections excluded"})
		}
		for _, invocation := range documentedTracearyCommands(text) {
			verifyDocumentedCommand(commandRoot, path, invocation, &report)
		}
	}
	sort.Strings(report.Problems)
	sort.Strings(report.FencedGroupCommands)
	sort.Strings(report.InlineGroupMentions)
	sort.Slice(skipped, func(i, j int) bool { return skipped[i].Path < skipped[j].Path })
	if report.Problems == nil {
		report.Problems = []string{}
	}
	if report.FencedGroupCommands == nil {
		report.FencedGroupCommands = []string{}
	}
	if report.InlineGroupMentions == nil {
		report.InlineGroupMentions = []string{}
	}
	if skipped == nil {
		skipped = []docsCommandSkipped{}
	}
	report.Skipped = skipped
	return report, nil
}

type documentedCommand struct {
	Line    int
	Path    []string
	InFence bool
}

func verifyDocumentedCommand(root *cobra.Command, file string, invocation documentedCommand, report *docsCommandReport) {
	// The bare root is an executable compatibility entrypoint, not a group;
	// its RunE opens the cockpit or prints deterministic help.
	if len(invocation.Path) == 0 {
		return
	}
	current := root
	for _, word := range invocation.Path {
		var next *cobra.Command
		for _, child := range current.Commands() {
			if child.Name() == word || docsContainsString(child.Aliases, word) {
				next = child
				break
			}
		}
		// A leaf command owns the remaining tokens as arguments. This is
		// important for commands such as `traceary hook compact claude`:
		// `claude` is an argument, not a subcommand.
		if next == nil && len(current.Commands()) == 0 {
			break
		}
		if next == nil {
			report.Problems = append(report.Problems, xerrors.Errorf("%s:%d: traceary %s does not resolve to a command", file, invocation.Line, strings.Join(invocation.Path, " ")).Error())
			return
		}
		current = next
	}
	if len(current.Commands()) > 0 {
		children := make([]string, 0, len(current.Commands()))
		for _, child := range current.Commands() {
			children = append(children, child.Name())
		}
		sort.Strings(children)
		finding := xerrors.Errorf("%s:%d: traceary %s is a group command and does not execute an action; use one of its subcommands: %s", file, invocation.Line, strings.Join(invocation.Path, " "), strings.Join(children, ", ")).Error()
		if invocation.InFence {
			report.FencedGroupCommands = append(report.FencedGroupCommands, finding)
		} else {
			report.InlineGroupMentions = append(report.InlineGroupMentions, finding)
		}
	}
}

func docsContainsString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

func docsCommandPaths(root string) ([]string, error) {
	var paths []string
	for _, path := range []string{"CHANGELOG.md", "CHANGELOG.ja.md"} {
		if _, err := os.Stat(filepath.Join(root, path)); err == nil {
			paths = append(paths, path)
		} else if !os.IsNotExist(err) {
			return nil, xerrors.Errorf("failed to stat %s: %w", path, err)
		}
	}
	docsDir := filepath.Join(root, "docs")
	if _, err := os.Stat(docsDir); err != nil {
		if os.IsNotExist(err) {
			return paths, nil
		}
		return nil, xerrors.Errorf("failed to stat docs: %w", err)
	}
	err := filepath.Walk(docsDir, func(path string, info os.FileInfo, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if !info.IsDir() && strings.HasSuffix(info.Name(), ".md") {
			rel, err := filepath.Rel(root, path)
			if err != nil {
				return xerrors.Errorf("failed to resolve docs path %s: %w", path, err)
			}
			paths = append(paths, rel)
		}
		return nil
	})
	if err != nil {
		return nil, xerrors.Errorf("failed to walk docs directory: %w", err)
	}
	sort.Strings(paths)
	return paths, nil
}

func isChangelogPath(path string) bool {
	return path == "CHANGELOG.md" || path == "CHANGELOG.ja.md"
}

func currentChangelogSection(text string) (string, bool, error) {
	lines := strings.Split(text, "\n")
	start := -1
	for i, line := range lines {
		if docsCommandHeadingRe.MatchString(line) {
			start = i
			break
		}
	}
	if start < 0 {
		return "", false, xerrors.New("current release heading not found")
	}
	end := len(lines)
	for i := start + 1; i < len(lines); i++ {
		if docsCommandHeadingRe.MatchString(lines[i]) {
			end = i
			break
		}
	}
	// Keep leading newlines so findings retain their original changelog line.
	return strings.Repeat("\n", start) + strings.Join(lines[start:end], "\n"), true, nil
}

func documentedTracearyCommands(text string) []documentedCommand {
	var commands []documentedCommand
	inFence := false
	fenceChar := byte(0)
	fenceLength := 0
	for lineNumber, line := range strings.Split(text, "\n") {
		trimmed := strings.TrimSpace(line)
		if !inFence && (strings.HasPrefix(trimmed, "```") || strings.HasPrefix(trimmed, "~~~")) {
			inFence = true
			fenceChar = trimmed[0]
			fenceLength = 0
			for fenceLength < len(trimmed) && trimmed[fenceLength] == fenceChar {
				fenceLength++
			}
			continue
		}
		if inFence {
			if isFenceClose(trimmed, fenceChar, fenceLength) {
				inFence = false
				continue
			}
			for _, command := range commandsFromLine(line, lineNumber+1) {
				command.InFence = true
				commands = append(commands, command)
			}
			continue
		}
		commands = append(commands, commandsFromInline(line, lineNumber+1)...)
	}
	return commands
}

func isFenceClose(line string, char byte, length int) bool {
	if len(line) < length || line[0] != char {
		return false
	}
	for i := 0; i < length; i++ {
		if line[i] != char {
			return false
		}
	}
	return strings.TrimSpace(line[length:]) == ""
}

func commandsFromInline(line string, lineNumber int) []documentedCommand {
	var segments []string
	for rest := line; ; {
		start := strings.IndexByte(rest, '`')
		if start < 0 {
			break
		}
		run := 1
		for start+run < len(rest) && rest[start+run] == '`' {
			run++
		}
		end := strings.Index(rest[start+run:], strings.Repeat("`", run))
		if end < 0 {
			break
		}
		segments = append(segments, rest[start+run:start+run+end])
		rest = rest[start+run+end+run:]
	}
	var commands []documentedCommand
	for _, segment := range segments {
		commands = append(commands, commandsFromLine(segment, lineNumber)...)
	}
	return commands
}

func commandsFromLine(line string, lineNumber int) []documentedCommand {
	var commands []documentedCommand
	for _, match := range docsTracearyRe.FindAllStringIndex(line, -1) {
		words := strings.Fields(line[match[1]:])
		var path []string
		for _, word := range words {
			if !docsPlainWordRe.MatchString(word) {
				break
			}
			path = append(path, word)
		}
		commands = append(commands, documentedCommand{Line: lineNumber, Path: path})
	}
	return commands
}

func writeDocsCommandsJSON(out io.Writer, report docsCommandReport) error {
	if err := json.NewEncoder(out).Encode(report); err != nil {
		return xerrors.Errorf("failed to write verify result: %w", err)
	}
	return nil
}

func writeDocsCommandsSummary(out io.Writer, report docsCommandReport) error {
	if _, err := io.WriteString(out, "Summary: class 1 unresolved="); err != nil {
		return xerrors.Errorf("failed to write verify summary: %w", err)
	}
	if _, err := io.WriteString(out, xerrors.Errorf("%d, class 2 fenced groups=%d, class 3 inline group mentions=%d, files scanned=%d\n", len(report.Problems), len(report.FencedGroupCommands), len(report.InlineGroupMentions), report.FilesScanned).Error()); err != nil {
		return xerrors.Errorf("failed to write verify summary: %w", err)
	}
	return nil
}
