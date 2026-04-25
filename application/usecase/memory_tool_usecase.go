package usecase

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"golang.org/x/xerrors"

	"github.com/duck8823/traceary/domain/model"
	"github.com/duck8823/traceary/domain/types"
)

const maxMemoryToolLines = 999999

// MemoryToolUsecase implements Anthropic's native memory-tool commands.
type MemoryToolUsecase interface {
	View(ctx context.Context, path string, viewRange []int) (string, error)
	Create(ctx context.Context, path string, fileText string) (string, error)
	StrReplace(ctx context.Context, path string, oldStr string, newStr string) (string, error)
	Insert(ctx context.Context, path string, insertLine int, insertText string) (string, error)
	Delete(ctx context.Context, path string) (string, error)
	Rename(ctx context.Context, oldPath string, newPath string) (string, error)
}

type memoryToolUsecase struct {
	repository model.MemoryToolFileRepository
	clock      types.Clock
}

// NewMemoryToolUsecase creates a usecase for Anthropic memory-tool commands.
func NewMemoryToolUsecase(repository model.MemoryToolFileRepository, clock types.Clock) MemoryToolUsecase {
	if clock == nil {
		clock = types.SystemClock{}
	}
	return &memoryToolUsecase{repository: repository, clock: clock}
}

// View shows directory contents or file contents with optional line ranges.
func (u *memoryToolUsecase) View(ctx context.Context, rawPath string, viewRange []int) (string, error) {
	path, err := types.NewMemoryToolPath(rawPath)
	if err != nil {
		return "", xerrors.Errorf("invalid memory tool path: %w", err)
	}
	file, err := u.repository.FindByPath(ctx, path)
	if err != nil {
		return "", xerrors.Errorf("failed to find memory tool file: %w", err)
	}
	if found, ok := file.Value(); ok {
		return renderMemoryToolFile(path, string(found.Content()), viewRange)
	}
	files, err := u.repository.List(ctx)
	if err != nil {
		return "", xerrors.Errorf("failed to list memory tool files: %w", err)
	}
	if path.IsRoot() || hasDescendant(files, path) {
		return renderMemoryToolDirectory(path, files), nil
	}
	return fmt.Sprintf("The path %s does not exist. Please provide a valid path.", path.String()), nil
}

// Create creates a new memory-tool file.
func (u *memoryToolUsecase) Create(ctx context.Context, rawPath string, fileText string) (string, error) {
	path, err := types.NewMemoryToolPath(rawPath)
	if err != nil {
		return "", xerrors.Errorf("invalid memory tool path: %w", err)
	}
	if path.IsRoot() {
		return fmt.Sprintf("Error: File %s already exists", path.String()), nil
	}
	if exists, err := u.pathExists(ctx, path); err != nil {
		return "", err
	} else if exists {
		return fmt.Sprintf("Error: File %s already exists", path.String()), nil
	}
	file := model.NewMemoryToolFile(path, []byte(fileText), u.clock.Now())
	if err := u.repository.Save(ctx, file); err != nil {
		return "", xerrors.Errorf("failed to save memory tool file: %w", err)
	}
	return fmt.Sprintf("File created successfully at: %s", path.String()), nil
}

// StrReplace replaces a unique verbatim string in a memory-tool file.
func (u *memoryToolUsecase) StrReplace(ctx context.Context, rawPath string, oldStr string, newStr string) (string, error) {
	path, file, ok, err := u.findFile(ctx, rawPath)
	if err != nil {
		return "", err
	}
	if !ok {
		return fmt.Sprintf("Error: The path %s does not exist. Please provide a valid path.", path.String()), nil
	}

	content := string(file.Content())
	count := strings.Count(content, oldStr)
	switch count {
	case 0:
		return fmt.Sprintf("No replacement was performed, old_str `%s` did not appear verbatim in %s.", oldStr, path.String()), nil
	case 1:
	default:
		return fmt.Sprintf("No replacement was performed. Multiple occurrences of old_str `%s` in lines: %s. Please ensure it is unique", oldStr, occurrenceLines(content, oldStr)), nil
	}

	updatedContent := strings.Replace(content, oldStr, newStr, 1)
	if err := u.repository.Save(ctx, file.WithContent([]byte(updatedContent), u.clock.Now())); err != nil {
		return "", xerrors.Errorf("failed to save replaced memory tool file: %w", err)
	}
	rendered, err := renderMemoryToolFile(path, updatedContent, nil)
	if err != nil {
		return "", err
	}
	return "The memory file has been edited.\n" + rendered, nil
}

// Insert inserts text at a bounded line number.
func (u *memoryToolUsecase) Insert(ctx context.Context, rawPath string, insertLine int, insertText string) (string, error) {
	path, file, ok, err := u.findFile(ctx, rawPath)
	if err != nil {
		return "", err
	}
	if !ok {
		return fmt.Sprintf("Error: The path %s does not exist", path.String()), nil
	}
	content := string(file.Content())
	lines, trailingNewline := splitMemoryToolLines(content)
	if insertLine < 0 || insertLine > len(lines) {
		return fmt.Sprintf("Error: Invalid `insert_line` parameter: %d. It should be within the range of lines of the file: [0, %d]", insertLine, len(lines)), nil
	}
	insertLines, insertTrailingNewline := splitMemoryToolLines(insertText)
	updatedLines := make([]string, 0, len(lines)+len(insertLines))
	updatedLines = append(updatedLines, lines[:insertLine]...)
	updatedLines = append(updatedLines, insertLines...)
	updatedLines = append(updatedLines, lines[insertLine:]...)
	updatedContent := joinMemoryToolLines(updatedLines, trailingNewline || insertTrailingNewline)
	if err := u.repository.Save(ctx, file.WithContent([]byte(updatedContent), u.clock.Now())); err != nil {
		return "", xerrors.Errorf("failed to save inserted memory tool file: %w", err)
	}
	return fmt.Sprintf("The file %s has been edited.", path.String()), nil
}

// Delete deletes a file or directory recursively.
func (u *memoryToolUsecase) Delete(ctx context.Context, rawPath string) (string, error) {
	path, err := types.NewMemoryToolPath(rawPath)
	if err != nil {
		return "", xerrors.Errorf("invalid memory tool path: %w", err)
	}
	deleted, err := u.repository.DeletePathPrefix(ctx, path)
	if err != nil {
		return "", xerrors.Errorf("failed to delete memory tool path: %w", err)
	}
	if deleted == 0 {
		return fmt.Sprintf("Error: The path %s does not exist", path.String()), nil
	}
	return fmt.Sprintf("Successfully deleted %s", path.String()), nil
}

// Rename renames or moves a file or directory.
func (u *memoryToolUsecase) Rename(ctx context.Context, rawOldPath string, rawNewPath string) (string, error) {
	oldPath, err := types.NewMemoryToolPath(rawOldPath)
	if err != nil {
		return "", xerrors.Errorf("invalid source memory tool path: %w", err)
	}
	newPath, err := types.NewMemoryToolPath(rawNewPath)
	if err != nil {
		return "", xerrors.Errorf("invalid destination memory tool path: %w", err)
	}
	if exists, err := u.pathExists(ctx, oldPath); err != nil {
		return "", err
	} else if !exists {
		return fmt.Sprintf("Error: The path %s does not exist", oldPath.String()), nil
	}
	if exists, err := u.pathExists(ctx, newPath); err != nil {
		return "", err
	} else if exists {
		return fmt.Sprintf("Error: The destination %s already exists", newPath.String()), nil
	}
	if _, err := u.repository.RenamePathPrefix(ctx, oldPath, newPath, u.clock.Now()); err != nil {
		return "", xerrors.Errorf("failed to rename memory tool path: %w", err)
	}
	return fmt.Sprintf("Successfully renamed %s to %s", oldPath.String(), newPath.String()), nil
}

func (u *memoryToolUsecase) findFile(ctx context.Context, rawPath string) (types.MemoryToolPath, *model.MemoryToolFile, bool, error) {
	path, err := types.NewMemoryToolPath(rawPath)
	if err != nil {
		return "", nil, false, xerrors.Errorf("invalid memory tool path: %w", err)
	}
	file, err := u.repository.FindByPath(ctx, path)
	if err != nil {
		return "", nil, false, xerrors.Errorf("failed to find memory tool file: %w", err)
	}
	if found, ok := file.Value(); ok {
		return path, found, true, nil
	}
	return path, nil, false, nil
}

func (u *memoryToolUsecase) pathExists(ctx context.Context, path types.MemoryToolPath) (bool, error) {
	file, err := u.repository.FindByPath(ctx, path)
	if err != nil {
		return false, xerrors.Errorf("failed to find memory tool file: %w", err)
	}
	if _, ok := file.Value(); ok {
		return true, nil
	}
	files, err := u.repository.List(ctx)
	if err != nil {
		return false, xerrors.Errorf("failed to list memory tool files: %w", err)
	}
	return path.IsRoot() || hasDescendant(files, path), nil
}

func hasDescendant(files []*model.MemoryToolFile, path types.MemoryToolPath) bool {
	for _, file := range files {
		if file.Path().IsDescendantOf(path) {
			return true
		}
	}
	return false
}

func renderMemoryToolFile(path types.MemoryToolPath, content string, viewRange []int) (string, error) {
	lines, _ := splitMemoryToolLines(content)
	if len(lines) > maxMemoryToolLines {
		return fmt.Sprintf("File %s exceeds maximum line limit of 999,999 lines.", path.String()), nil
	}
	start, end, err := normalizeViewRange(viewRange, len(lines))
	if err != nil {
		return "", err
	}
	var builder strings.Builder
	fmt.Fprintf(&builder, "Here's the content of %s with line numbers:", path.String())
	for index := start; index < end; index++ {
		fmt.Fprintf(&builder, "\n%6d\t%s", index+1, lines[index])
	}
	return builder.String(), nil
}

func normalizeViewRange(viewRange []int, lineCount int) (int, int, error) {
	if len(viewRange) == 0 {
		return 0, lineCount, nil
	}
	if len(viewRange) != 2 {
		return 0, 0, xerrors.Errorf("view_range must contain exactly two integers")
	}
	start := viewRange[0]
	end := viewRange[1]
	if start < 1 {
		return 0, 0, xerrors.Errorf("view_range start must be >= 1")
	}
	if end == -1 || end > lineCount {
		end = lineCount
	}
	if end < start-1 {
		return 0, 0, xerrors.Errorf("view_range end must be >= start")
	}
	return start - 1, end, nil
}

func renderMemoryToolDirectory(path types.MemoryToolPath, files []*model.MemoryToolFile) string {
	entries := directoryEntries(path, files)
	var builder strings.Builder
	fmt.Fprintf(&builder, "Here're the files and directories up to 2 levels deep in %s, excluding hidden items and node_modules:", path.String())
	for _, entry := range entries {
		fmt.Fprintf(&builder, "\n%5s\t%s", humanMemoryToolSize(entry.size), entry.path)
	}
	return builder.String()
}

type memoryToolDirectoryEntry struct {
	path string
	size int64
}

func directoryEntries(root types.MemoryToolPath, files []*model.MemoryToolFile) []memoryToolDirectoryEntry {
	sizes := map[string]int64{root.String(): 0}
	rootDepth := memoryToolDepth(root.String())
	for _, file := range files {
		filePath := file.Path().String()
		if !isVisibleDescendantOrSelf(root.String(), filePath) {
			continue
		}
		segments := strings.Split(strings.Trim(filePath, "/"), "/")
		for depth := rootDepth + 1; depth <= len(segments) && depth <= rootDepth+2; depth++ {
			entryPath := "/" + strings.Join(segments[:depth], "/")
			sizes[entryPath] += file.SizeBytes()
		}
		sizes[root.String()] += file.SizeBytes()
	}
	entries := make([]memoryToolDirectoryEntry, 0, len(sizes))
	for path, size := range sizes {
		entries = append(entries, memoryToolDirectoryEntry{path: path, size: size})
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].path < entries[j].path })
	return entries
}

func isVisibleDescendantOrSelf(root string, path string) bool {
	if path != root && !strings.HasPrefix(path, root+"/") {
		return false
	}
	for _, segment := range strings.Split(strings.TrimPrefix(strings.Trim(path, "/"), "memories/"), "/") {
		if segment == "" {
			continue
		}
		if strings.HasPrefix(segment, ".") || segment == "node_modules" {
			return false
		}
	}
	return true
}

func memoryToolDepth(path string) int {
	return len(strings.Split(strings.Trim(path, "/"), "/"))
}

func humanMemoryToolSize(size int64) string {
	const unit = 1024
	if size < unit {
		return fmt.Sprintf("%dB", size)
	}
	value := float64(size) / unit
	for _, suffix := range []string{"K", "M", "G", "T"} {
		if value < unit {
			return fmt.Sprintf("%.1f%s", value, suffix)
		}
		value /= unit
	}
	return fmt.Sprintf("%.1fP", value)
}

func occurrenceLines(content string, needle string) string {
	lines, _ := splitMemoryToolLines(content)
	found := make([]string, 0)
	for index, line := range lines {
		if strings.Contains(line, needle) {
			found = append(found, fmt.Sprintf("%d", index+1))
		}
	}
	return strings.Join(found, ", ")
}

func splitMemoryToolLines(content string) ([]string, bool) {
	if content == "" {
		return []string{}, false
	}
	trailingNewline := strings.HasSuffix(content, "\n")
	trimmed := strings.TrimSuffix(content, "\n")
	return strings.Split(trimmed, "\n"), trailingNewline
}

func joinMemoryToolLines(lines []string, trailingNewline bool) string {
	content := strings.Join(lines, "\n")
	if trailingNewline {
		content += "\n"
	}
	return content
}
