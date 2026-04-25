package anthropicmemory_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"golang.org/x/xerrors"
	"time"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/google/go-cmp/cmp"

	"github.com/duck8823/traceary/domain/model"
	"github.com/duck8823/traceary/domain/types"
	"github.com/duck8823/traceary/pkg/anthropicmemory"
)

func TestHandler_handleMemoryToolCommands(t *testing.T) {
	t.Parallel()

	repo := newMemoryToolRepositoryStub(t)
	sut, err := anthropicmemory.NewHandlerWithRepository(repo, fixedClock{})
	if err != nil {
		t.Fatalf("NewHandlerWithRepository() error = %v", err)
	}

	create := decodeInput(t, `{"command":"create","path":"/memories/project.md","file_text":"alpha\nbeta\n"}`)
	result, err := sut.HandleText(context.Background(), create)
	if err != nil {
		t.Fatalf("HandleText(create) error = %v", err)
	}
	if diff := cmp.Diff("File created successfully at: /memories/project.md", result); diff != "" {
		t.Fatalf("create result mismatch (-want +got):\n%s", diff)
	}

	view := decodeInput(t, `{"command":"view","path":"/memories/project.md","view_range":[1,1]}`)
	content, err := sut.Handle(context.Background(), view)
	if err != nil {
		t.Fatalf("Handle(view) error = %v", err)
	}
	if content.OfText == nil {
		t.Fatalf("Handle(view) did not return text content: %#v", content)
	}
	want := "Here's the content of /memories/project.md with line numbers:\n     1\talpha"
	if diff := cmp.Diff(want, content.OfText.Text); diff != "" {
		t.Fatalf("view content mismatch (-want +got):\n%s", diff)
	}

	insert := decodeInput(t, `{"command":"insert","path":"/memories/project.md","insert_line":1,"insert_text":"middle"}`)
	block, err := sut.HandleToolUse(context.Background(), "toolu_test", insert)
	if err != nil {
		t.Fatalf("HandleToolUse(insert) error = %v", err)
	}
	if block.ToolUseID != "toolu_test" {
		t.Fatalf("ToolUseID = %q, want toolu_test", block.ToolUseID)
	}
	if len(block.Content) != 1 || block.Content[0].OfText == nil {
		t.Fatalf("tool result content = %#v, want text", block.Content)
	}
	if !strings.Contains(block.Content[0].OfText.Text, "has been edited") {
		t.Fatalf("tool result text = %q, want edit confirmation", block.Content[0].OfText.Text)
	}
}

func TestDecodeInput_rejectsUnsupportedCommand(t *testing.T) {
	t.Parallel()

	_, err := anthropicmemory.DecodeInput(json.RawMessage(`{"command":"chmod","path":"/memories/a"}`))
	if err == nil {
		t.Fatal("DecodeInput() error = nil, want unsupported command error")
	}
}

func TestTool_usesPinnedMemoryTool(t *testing.T) {
	t.Parallel()

	tool := anthropicmemory.Tool()
	if tool.OfMemoryTool20250818 == nil {
		t.Fatalf("Tool().OfMemoryTool20250818 = nil")
	}
	encoded, err := json.Marshal(tool)
	if err != nil {
		t.Fatalf("json.Marshal(Tool()) error = %v", err)
	}
	if !strings.Contains(string(encoded), anthropicmemory.ToolVersion) {
		t.Fatalf("encoded tool = %s, want version %s", encoded, anthropicmemory.ToolVersion)
	}
}

func decodeInput(t *testing.T, raw string) anthropic.BetaMemoryTool20250818CommandUnion {
	t.Helper()
	input, err := anthropicmemory.DecodeInput(json.RawMessage(raw))
	if err != nil {
		t.Fatalf("DecodeInput(%s) error = %v", raw, err)
	}
	return input
}

type fixedClock struct{}

func (fixedClock) Now() time.Time {
	return time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
}

type memoryToolRepositoryStub struct {
	files map[string]*model.MemoryToolFile
}

func newMemoryToolRepositoryStub(t *testing.T) *memoryToolRepositoryStub {
	t.Helper()
	return &memoryToolRepositoryStub{files: map[string]*model.MemoryToolFile{}}
}

func (r *memoryToolRepositoryStub) Save(_ context.Context, file *model.MemoryToolFile) error {
	r.files[file.Path().String()] = file
	return nil
}

func (r *memoryToolRepositoryStub) FindByPath(_ context.Context, path types.MemoryToolPath) (types.Optional[*model.MemoryToolFile], error) {
	if file, ok := r.files[path.String()]; ok {
		return types.Some(file), nil
	}
	return types.None[*model.MemoryToolFile](), nil
}

func (r *memoryToolRepositoryStub) List(_ context.Context) ([]*model.MemoryToolFile, error) {
	files := make([]*model.MemoryToolFile, 0, len(r.files))
	for _, file := range r.files {
		files = append(files, file)
	}
	return files, nil
}

func (r *memoryToolRepositoryStub) DeletePathPrefix(_ context.Context, path types.MemoryToolPath) (int64, error) {
	var deleted int64
	for filePath := range r.files {
		memoryPath, err := types.NewMemoryToolPath(filePath)
		if err != nil {
			return 0, xerrors.Errorf("invalid stored memory tool path: %w", err)
		}
		if memoryPath == path || memoryPath.IsDescendantOf(path) {
			delete(r.files, filePath)
			deleted++
		}
	}
	return deleted, nil
}

func (r *memoryToolRepositoryStub) RenamePathPrefix(_ context.Context, oldPath types.MemoryToolPath, newPath types.MemoryToolPath, updatedAt time.Time) (int64, error) {
	moved := map[string]*model.MemoryToolFile{}
	var renamed int64
	for filePath, file := range r.files {
		memoryPath, err := types.NewMemoryToolPath(filePath)
		if err != nil {
			return 0, xerrors.Errorf("invalid stored memory tool path: %w", err)
		}
		if memoryPath == oldPath || memoryPath.IsDescendantOf(oldPath) {
			newFilePath, pathErr := types.NewMemoryToolPath(newPath.String() + strings.TrimPrefix(filePath, oldPath.String()))
			if pathErr != nil {
				return 0, xerrors.Errorf("invalid renamed memory tool path: %w", pathErr)
			}
			moved[newFilePath.String()] = model.MemoryToolFileOf(newFilePath, file.Content(), file.CreatedAt(), updatedAt)
			delete(r.files, filePath)
			renamed++
		}
	}
	for filePath, file := range moved {
		r.files[filePath] = file
	}
	return renamed, nil
}
