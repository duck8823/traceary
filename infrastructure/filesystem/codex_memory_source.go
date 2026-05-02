package filesystem

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"io/fs"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"golang.org/x/xerrors"

	"github.com/duck8823/traceary/application"
	apptypes "github.com/duck8823/traceary/application/types"
	domtypes "github.com/duck8823/traceary/domain/types"
)

// codexMemorySource parses ~/.codex/memories/*.md files into durable-memory
// candidates. Legacy MEMORY.md keeps its handbook section allow-list, while
// additional Markdown files are treated as user-authored memory shards.
type codexMemorySource struct {
	maxBulletBytes int64
	maxFileBytes   int64
}

// NewCodexMemorySource returns the default Codex memory reader. The
// configured limits are generous enough for real Markdown memory files but still
// clamp pathological inputs so a runaway file never blocks the import.
func NewCodexMemorySource() application.CodexMemorySource {
	return &codexMemorySource{
		maxBulletBytes: defaultCodexMaxBulletBytes,
		maxFileBytes:   defaultCodexMaxFileBytes,
	}
}

const (
	defaultCodexMaxBulletBytes int64 = 32 * 1024
	defaultCodexMaxFileBytes   int64 = 2 * 1024 * 1024
	codexMemoryFileName              = "MEMORY.md"
)

// Load reads Markdown files from criteria.Root and extracts memory bullets.
// It returns candidates plus non-fatal warnings for the CLI to surface;
// parser failures that make the run unsafe (bad root, path escape,
// unreadable file) still come back as errors.
func (s *codexMemorySource) Load(
	ctx context.Context,
	criteria apptypes.CodexImportCriteria,
) ([]apptypes.ImportedMemoryCandidate, []string, error) {
	if ctx == nil {
		return nil, nil, xerrors.Errorf("context must not be nil")
	}

	rootInput := strings.TrimSpace(criteria.Root)
	if rootInput == "" {
		return nil, nil, xerrors.Errorf("codex memory root must not be empty")
	}

	root, err := filepath.Abs(rootInput)
	if err != nil {
		return nil, nil, xerrors.Errorf("failed to resolve codex memory root: %w", err)
	}
	// A symlinked root (for example dotfile setups that point ~/.codex at
	// the real directory) is expected; resolveCodexMemoryFile re-checks
	// path containment against the fully evaluated root so we still refuse
	// anything that escapes it.
	rootInfo, err := os.Stat(root)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil, nil
		}
		return nil, nil, xerrors.Errorf("failed to stat codex memory root %q: %w", root, err)
	}
	if !rootInfo.IsDir() {
		return nil, nil, xerrors.Errorf("codex memory root is not a directory: %s", root)
	}

	walkRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		return nil, nil, xerrors.Errorf("failed to resolve codex memory root %q: %w", root, err)
	}
	memoryPaths, err := discoverCodexMemoryMarkdownFiles(ctx, walkRoot)
	if err != nil {
		return nil, nil, err
	}

	candidates := make([]apptypes.ImportedMemoryCandidate, 0)
	var warnings []string
	for _, memoryPath := range memoryPaths {
		select {
		case <-ctx.Done():
			return nil, warnings, xerrors.Errorf("codex memory import cancelled: %w", ctx.Err())
		default:
		}

		fileCandidates, fileWarnings, err := s.loadCodexMemoryMarkdownFile(ctx, walkRoot, memoryPath, criteria)
		if err != nil {
			return nil, warnings, err
		}
		warnings = append(warnings, fileWarnings...)
		candidates = append(candidates, fileCandidates...)
	}

	return candidates, warnings, nil
}

func discoverCodexMemoryMarkdownFiles(ctx context.Context, root string) ([]string, error) {
	var paths []string
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}
		if walkErr != nil {
			return xerrors.Errorf("failed to inspect codex memory path %s: %w", path, walkErr)
		}
		if path == root {
			return nil
		}
		if entry.Type()&os.ModeSymlink != 0 {
			if strings.EqualFold(filepath.Ext(entry.Name()), ".md") {
				return xerrors.Errorf("codex memory file must not be a symlink: %s", path)
			}
			return nil
		}
		if entry.IsDir() {
			return nil
		}
		if !strings.EqualFold(filepath.Ext(entry.Name()), ".md") {
			return nil
		}
		paths = append(paths, path)
		return nil
	})
	if err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return nil, xerrors.Errorf("codex memory import cancelled: %w", ctxErr)
		}
		return nil, xerrors.Errorf("failed to discover codex memory markdown files under %s: %w", root, err)
	}
	return paths, nil
}

func (s *codexMemorySource) loadCodexMemoryMarkdownFile(
	_ context.Context,
	root string,
	memoryPath string,
	criteria apptypes.CodexImportCriteria,
) ([]apptypes.ImportedMemoryCandidate, []string, error) {
	containedPath, err := resolveCodexMemoryFile(root, memoryPath)
	if err != nil {
		return nil, nil, err
	}
	if containedPath == "" {
		return nil, nil, nil
	}

	fileInfo, err := os.Stat(containedPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil, nil
		}
		return nil, nil, xerrors.Errorf("failed to stat %s: %w", containedPath, err)
	}
	if !fileInfo.Mode().IsRegular() {
		return nil, nil, xerrors.Errorf("codex memory file is not a regular file: %s", containedPath)
	}
	if fileInfo.Size() > s.maxFileBytes {
		return nil, []string{fmt.Sprintf("codex memory file %s exceeds size guard (%d bytes); skipping", containedPath, fileInfo.Size())}, nil
	}

	file, err := os.Open(containedPath)
	if err != nil {
		return nil, nil, xerrors.Errorf("failed to open %s: %w", containedPath, err)
	}
	defer func() { _ = file.Close() }()

	parseMode := codexParseModeGeneric
	if isLegacyCodexMemoryFile(root, containedPath) {
		parseMode = codexParseModeLegacyHandbook
	}
	parsed, parseWarnings, err := parseCodexMemoryFile(file, s.maxBulletBytes, parseMode)
	warnings := prefixCodexMemoryWarnings(containedPath, parseWarnings)
	if err != nil {
		return nil, warnings, xerrors.Errorf("failed to parse %s: %w", containedPath, err)
	}

	candidates := make([]apptypes.ImportedMemoryCandidate, 0, len(parsed.bullets))
	for _, bullet := range parsed.bullets {
		memoryType, ok := codexMemoryTypeForBullet(bullet, containedPath)
		if !ok {
			continue
		}

		scope, scopeWarning, err := resolveCandidateScope(bullet.cwd, criteria.WorkspaceFallback)
		if err != nil {
			warnings = append(warnings, fmt.Sprintf("skipped bullet at %s:%d (scope resolution failed: %v)", containedPath, bullet.startLine, err))
			continue
		}
		if scopeWarning != "" {
			warnings = append(warnings, scopeWarning)
		}
		if scope == nil {
			warnings = append(warnings, fmt.Sprintf("skipped bullet at %s:%d (no workspace scope; provide --workspace or add applies_to hint)", containedPath, bullet.startLine))
			continue
		}

		evidence, err := domtypes.EvidenceRefFrom(
			domtypes.EvidenceRefKindFile,
			fmt.Sprintf("%s#L%d-L%d", containedPath, bullet.startLine, bullet.endLine),
		)
		if err != nil {
			warnings = append(warnings, fmt.Sprintf("skipped bullet at %s:%d (evidence ref rejected: %v)", containedPath, bullet.startLine, err))
			continue
		}
		artifact, err := domtypes.ArtifactRefFrom(domtypes.ArtifactRefKindFile, containedPath)
		if err != nil {
			warnings = append(warnings, fmt.Sprintf("skipped bullet at %s:%d (artifact ref rejected: %v)", containedPath, bullet.startLine, err))
			continue
		}

		candidates = append(candidates, apptypes.ImportedMemoryCandidate{
			MemoryType:   memoryType,
			Scope:        scope,
			Fact:         bullet.fact,
			EvidenceRefs: []domtypes.EvidenceRef{evidence},
			ArtifactRefs: []domtypes.ArtifactRef{artifact},
			SourcePath:   containedPath,
		})
	}
	return candidates, warnings, nil
}

func prefixCodexMemoryWarnings(path string, warnings []string) []string {
	if len(warnings) == 0 {
		return nil
	}
	prefixed := make([]string, 0, len(warnings))
	for _, warning := range warnings {
		prefixed = append(prefixed, fmt.Sprintf("%s: %s", path, warning))
	}
	return prefixed
}

func isLegacyCodexMemoryFile(root, path string) bool {
	return filepath.Clean(path) == filepath.Join(filepath.Clean(root), codexMemoryFileName)
}

// resolveCodexMemoryFile ensures the candidate file stays inside root after
// symlink resolution. Returning "" means the file is absent — a fresh Codex
// install where nothing has been captured yet — which is not an error.
func resolveCodexMemoryFile(root, memoryPath string) (string, error) {
	resolved, err := filepath.EvalSymlinks(memoryPath)
	if err != nil {
		if os.IsNotExist(err) {
			return "", nil
		}
		return "", xerrors.Errorf("failed to resolve %s: %w", memoryPath, err)
	}
	resolvedRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		return "", xerrors.Errorf("failed to resolve root %s: %w", root, err)
	}
	rel, err := filepath.Rel(resolvedRoot, resolved)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", xerrors.Errorf("codex memory file escapes the configured root: %s", resolved)
	}
	return resolved, nil
}

type codexMemoryBullet struct {
	section   string
	fact      string
	cwd       string
	startLine int
	endLine   int
}

type codexMemoryParseResult struct {
	bullets []codexMemoryBullet
}

const (
	codexSectionUserPreferences = "User preferences"
	codexSectionReusable        = "Reusable knowledge"
	codexSectionFailures        = "Failures and how to do differently"
	codexSectionGeneric         = "Generic memory"
)

var knownCodexSections = map[string]struct{}{
	codexSectionUserPreferences: {},
	codexSectionReusable:        {},
	codexSectionFailures:        {},
}

func codexMemoryTypeForBullet(bullet codexMemoryBullet, sourcePath string) (domtypes.MemoryType, bool) {
	return sectionMemoryType(bullet.section, sourcePath)
}

func sectionMemoryType(section string, sourcePath string) (domtypes.MemoryType, bool) {
	switch section {
	case codexSectionUserPreferences:
		return domtypes.MemoryTypePreference, true
	case codexSectionReusable, codexSectionFailures:
		return domtypes.MemoryTypeLesson, true
	}

	lower := strings.ToLower(section + " " + strings.TrimSuffix(filepath.Base(sourcePath), filepath.Ext(sourcePath)))
	switch {
	case strings.Contains(lower, "preference"):
		return domtypes.MemoryTypePreference, true
	case strings.Contains(lower, "decision"):
		return domtypes.MemoryTypeDecision, true
	case strings.Contains(lower, "constraint") ||
		strings.Contains(lower, "rule") ||
		strings.Contains(lower, "policy") ||
		strings.Contains(lower, "format"):
		return domtypes.MemoryTypeConstraint, true
	case strings.Contains(lower, "failure") ||
		strings.Contains(lower, "lesson") ||
		strings.Contains(lower, "how to do differently"):
		return domtypes.MemoryTypeLesson, true
	default:
		return domtypes.MemoryTypeLesson, true
	}
}

type codexParseMode int

const (
	codexParseModeLegacyHandbook codexParseMode = iota
	codexParseModeGeneric
)

// parseCodexMemoryFile scans one Codex Markdown memory file top-down. State
// is carried through three pointers: the current Task Group's
// `applies_to: cwd=...` hint, the current section heading, and the bullet
// currently being collected. Bullets close when we encounter another bullet,
// a new heading, or end-of-file.
//
// The reader uses bufio.Reader.ReadString rather than bufio.Scanner so a
// single pathological line (for example a malformed handbook with a
// multi-megabyte one-line bullet) degrades to a size-guard warning instead
// of bubbling up as a fatal scanner error that aborts the whole import.
func parseCodexMemoryFile(reader io.Reader, maxBulletBytes int64, mode codexParseMode) (codexMemoryParseResult, []string, error) {
	var result codexMemoryParseResult
	var warnings []string

	bufReader := bufio.NewReader(reader)

	var (
		currentSection string
		currentCWD     string
		currentBullet  *codexMemoryBullet
		bulletBuilder  strings.Builder
	)
	lineNo := 0

	flushBullet := func() {
		if currentBullet == nil {
			return
		}
		fact := strings.TrimSpace(bulletBuilder.String())
		if fact == "" {
			currentBullet = nil
			bulletBuilder.Reset()
			return
		}
		if mode == codexParseModeGeneric {
			currentBullet.fact = fact
			result.bullets = append(result.bullets, *currentBullet)
		} else if _, ok := knownCodexSections[currentBullet.section]; ok {
			currentBullet.fact = fact
			result.bullets = append(result.bullets, *currentBullet)
		}
		currentBullet = nil
		bulletBuilder.Reset()
	}

	for {
		rawLine, err := bufReader.ReadString('\n')
		if rawLine == "" && err != nil {
			if err == io.EOF {
				break
			}
			return codexMemoryParseResult{}, warnings, xerrors.Errorf("failed to read codex MEMORY.md: %w", err)
		}
		lineNo++
		if int64(len(rawLine)) > maxBulletBytes {
			warnings = append(warnings, fmt.Sprintf("line %d exceeds size guard (%d bytes); skipping", lineNo, len(rawLine)))
			if currentBullet != nil {
				flushBullet()
			}
			if err == io.EOF {
				break
			}
			continue
		}
		line := strings.TrimRight(rawLine, "\n")
		line = strings.TrimRight(line, "\r")

		if heading, ok := parseCodexHeading(line); ok {
			flushBullet()
			if heading.level == 1 {
				currentCWD = ""
				currentSection = ""
			}
			if mode == codexParseModeGeneric {
				currentSection = heading.title
			} else if heading.level == 2 {
				if _, known := knownCodexSections[heading.title]; known {
					currentSection = heading.title
				} else {
					currentSection = ""
				}
			}
			continue
		}
		if cwd, ok := parseAppliesTo(line); ok {
			flushBullet()
			currentCWD = cwd
			continue
		}
		if line == "" {
			if currentBullet != nil {
				flushBullet()
			}
			continue
		}
		if text, ok := parseMarkdownListItem(line); ok {
			flushBullet()
			if currentSection == "" && mode == codexParseModeLegacyHandbook {
				continue
			}
			section := currentSection
			if section == "" {
				section = codexSectionGeneric
			}
			currentBullet = &codexMemoryBullet{
				section:   section,
				cwd:       currentCWD,
				startLine: lineNo,
				endLine:   lineNo,
			}
			bulletBuilder.Reset()
			bulletBuilder.WriteString(text)
			if int64(bulletBuilder.Len()) > maxBulletBytes {
				warnings = append(warnings, fmt.Sprintf("bullet at line %d exceeds size guard; skipping", lineNo))
				currentBullet = nil
				bulletBuilder.Reset()
			}
			continue
		}
		if strings.HasPrefix(line, "  ") && currentBullet != nil {
			bulletBuilder.WriteString("\n")
			bulletBuilder.WriteString(strings.TrimLeft(line, " "))
			currentBullet.endLine = lineNo
			if int64(bulletBuilder.Len()) > maxBulletBytes {
				warnings = append(warnings, fmt.Sprintf("bullet starting at line %d exceeds size guard; skipping", currentBullet.startLine))
				currentBullet = nil
				bulletBuilder.Reset()
			}
			continue
		}

		if currentBullet != nil {
			flushBullet()
		}
		if err == io.EOF {
			break
		}
	}
	flushBullet()

	return result, warnings, nil
}

var orderedMarkdownListItemPattern = regexp.MustCompile(`^\d+[\.)]\s+(.+)$`)

func parseMarkdownListItem(line string) (string, bool) {
	trimmed := strings.TrimLeft(line, " ")
	if len(line)-len(trimmed) > 3 {
		return "", false
	}
	for _, marker := range []string{"- ", "* ", "+ "} {
		if strings.HasPrefix(trimmed, marker) {
			return strings.TrimPrefix(trimmed, marker), true
		}
	}
	match := orderedMarkdownListItemPattern.FindStringSubmatch(trimmed)
	if match == nil {
		return "", false
	}
	return match[1], true
}

type codexHeading struct {
	level int
	title string
}

func parseCodexHeading(line string) (codexHeading, bool) {
	trimmed := strings.TrimRight(line, " \t")
	switch {
	case strings.HasPrefix(trimmed, "### "):
		return codexHeading{level: 3, title: strings.TrimSpace(strings.TrimPrefix(trimmed, "###"))}, true
	case strings.HasPrefix(trimmed, "## "):
		return codexHeading{level: 2, title: strings.TrimSpace(strings.TrimPrefix(trimmed, "##"))}, true
	case strings.HasPrefix(trimmed, "# "):
		return codexHeading{level: 1, title: strings.TrimSpace(strings.TrimPrefix(trimmed, "#"))}, true
	default:
		return codexHeading{}, false
	}
}

// parseAppliesTo extracts the `cwd=...` fragment from a Codex `applies_to`
// declaration. Codex writes this as `applies_to: cwd=/path; other=...` —
// semicolons delimit multiple constraints and everything after the first
// `cwd=` wins.
func parseAppliesTo(line string) (string, bool) {
	trimmed := strings.TrimSpace(line)
	const prefix = "applies_to:"
	if !strings.HasPrefix(trimmed, prefix) {
		return "", false
	}
	body := strings.TrimSpace(strings.TrimPrefix(trimmed, prefix))
	for _, part := range strings.Split(body, ";") {
		part = strings.TrimSpace(part)
		if strings.HasPrefix(part, "cwd=") {
			return strings.TrimSpace(strings.TrimPrefix(part, "cwd=")), true
		}
	}
	return "", true
}

// resolveCandidateScope turns a candidate's cwd hint (and an optional --workspace
// fallback) into a WorkspaceScope. When a raw cwd path is provided, the
// resolver tries to upgrade it to `github.com/org/repo` via `git remote` so
// the imported candidate shares a scope with other Traceary signals written
// from the same working tree. If the path does not resolve, the scope falls
// back to the cleaned absolute path so the candidate is still usable.
func resolveCandidateScope(
	cwdHint string,
	fallback domtypes.Workspace,
) (domtypes.MemoryScope, string, error) {
	cwdHint = strings.TrimSpace(cwdHint)
	if cwdHint != "" {
		if workspace, warning := normaliseCwdToWorkspace(cwdHint); workspace != "" {
			scope, err := newWorkspaceScope(workspace)
			if err != nil {
				return nil, "", err
			}
			return scope, warning, nil
		}
	}
	if fallback.String() == "" {
		return nil, "", nil
	}
	scope, err := newWorkspaceScope(fallback.String())
	if err != nil {
		return nil, "", err
	}
	return scope, "", nil
}

func newWorkspaceScope(value string) (domtypes.MemoryScope, error) {
	workspace, err := domtypes.WorkspaceFrom(value)
	if err != nil {
		return nil, xerrors.Errorf("invalid workspace scope value: %w", err)
	}
	return domtypes.WorkspaceScopeOf(workspace), nil
}

// normaliseCwdToWorkspace tries to turn an absolute path into the shared
// `github.com/org/repo` form Traceary uses elsewhere. When the path does
// not point at a usable git work tree the function falls back to the
// cleaned absolute path so the caller still has a durable scope value.
func normaliseCwdToWorkspace(cwdHint string) (string, string) {
	cleaned := filepath.Clean(cwdHint)
	if cleaned == "" {
		return "", ""
	}
	if strings.HasPrefix(cleaned, "~") {
		return cleaned, ""
	}
	if remote, ok := readGitRemoteForPath(cleaned); ok {
		if workspace, ok := normaliseRemoteURL(remote); ok {
			return workspace, ""
		}
	}
	return cleaned, ""
}

// readGitRemoteForPath reads the origin remote out of a git repository
// without shelling out. It tolerates non-git directories, worktrees, and
// missing config files — any failure drops the caller back to the cleaned
// absolute path.
func readGitRemoteForPath(path string) (string, bool) {
	configPath := filepath.Join(path, ".git", "config")
	data, err := os.ReadFile(configPath)
	if err != nil {
		return "", false
	}
	const header = "[remote \"origin\"]"
	idx := strings.Index(string(data), header)
	if idx < 0 {
		return "", false
	}
	rest := string(data)[idx+len(header):]
	for _, line := range strings.Split(rest, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "[") {
			break
		}
		if strings.HasPrefix(line, "url") {
			parts := strings.SplitN(line, "=", 2)
			if len(parts) != 2 {
				continue
			}
			return strings.TrimSpace(parts[1]), true
		}
	}
	return "", false
}

// normaliseRemoteURL converts the common github URL forms Codex might
// encounter back to a host/owner/repo identifier.
func normaliseRemoteURL(raw string) (string, bool) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", false
	}
	if strings.HasPrefix(raw, "git@") {
		// git@github.com:owner/repo.git
		trimmed := strings.TrimPrefix(raw, "git@")
		host, path, ok := strings.Cut(trimmed, ":")
		if !ok {
			return "", false
		}
		return strings.ToLower(host) + "/" + strings.TrimSuffix(path, ".git"), true
	}
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		return "", false
	}
	cleaned := strings.TrimPrefix(u.Path, "/")
	cleaned = strings.TrimSuffix(cleaned, ".git")
	if cleaned == "" {
		return "", false
	}
	return strings.ToLower(u.Host) + "/" + cleaned, true
}
