package usecase_test

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/duck8823/traceary/application/usecase"
	"github.com/duck8823/traceary/domain/model"
	"github.com/duck8823/traceary/domain/types"
)

func TestMemoryToolUsecase_viewFileLineNumbersByteMatch(t *testing.T) {
	t.Parallel()

	repo := newMemoryToolRepositoryStub(t, map[string]string{
		"/memories/notes.txt": strings.Join([]string{
			"Hello World",
			"This is line two",
			"Line three",
			"Line four",
			"Line five",
			"Line six",
			"Line seven",
			"Line eight",
			"Line nine",
			"Line ten",
			"Line eleven",
			"Line twelve",
			"Line thirteen",
			"Line fourteen",
			"Line fifteen",
			"Line sixteen",
			"Line seventeen",
			"Line eighteen",
			"Line nineteen",
			"Line twenty",
			"Line twenty-one",
			"Line twenty-two",
			"Line twenty-three",
			"Line twenty-four",
			"Line twenty-five",
			"Line twenty-six",
			"Line twenty-seven",
			"Line twenty-eight",
			"Line twenty-nine",
			"Line thirty",
			"Line thirty-one",
			"Line thirty-two",
			"Line thirty-three",
			"Line thirty-four",
			"Line thirty-five",
			"Line thirty-six",
			"Line thirty-seven",
			"Line thirty-eight",
			"Line thirty-nine",
			"Line forty",
			"Line forty-one",
			"Line forty-two",
			"Line forty-three",
			"Line forty-four",
			"Line forty-five",
			"Line forty-six",
			"Line forty-seven",
			"Line forty-eight",
			"Line forty-nine",
			"Line fifty",
			"Line fifty-one",
			"Line fifty-two",
			"Line fifty-three",
			"Line fifty-four",
			"Line fifty-five",
			"Line fifty-six",
			"Line fifty-seven",
			"Line fifty-eight",
			"Line fifty-nine",
			"Line sixty",
			"Line sixty-one",
			"Line sixty-two",
			"Line sixty-three",
			"Line sixty-four",
			"Line sixty-five",
			"Line sixty-six",
			"Line sixty-seven",
			"Line sixty-eight",
			"Line sixty-nine",
			"Line seventy",
			"Line seventy-one",
			"Line seventy-two",
			"Line seventy-three",
			"Line seventy-four",
			"Line seventy-five",
			"Line seventy-six",
			"Line seventy-seven",
			"Line seventy-eight",
			"Line seventy-nine",
			"Line eighty",
			"Line eighty-one",
			"Line eighty-two",
			"Line eighty-three",
			"Line eighty-four",
			"Line eighty-five",
			"Line eighty-six",
			"Line eighty-seven",
			"Line eighty-eight",
			"Line eighty-nine",
			"Line ninety",
			"Line ninety-one",
			"Line ninety-two",
			"Line ninety-three",
			"Line ninety-four",
			"Line ninety-five",
			"Line ninety-six",
			"Line ninety-seven",
			"Line ninety-eight",
			"Line ninety-nine",
			"Line one hundred",
		}, "\n"),
	})
	sut := usecase.NewMemoryToolUsecase(repo, fixedClock{})

	got, err := sut.View(context.Background(), "/memories/notes.txt", []int{1, 100})
	if err != nil {
		t.Fatalf("View() error = %v", err)
	}

	want := "Here's the content of /memories/notes.txt with line numbers:\n" +
		"     1\tHello World\n" +
		"     2\tThis is line two\n" +
		"     3\tLine three\n" +
		"     4\tLine four\n" +
		"     5\tLine five\n" +
		"     6\tLine six\n" +
		"     7\tLine seven\n" +
		"     8\tLine eight\n" +
		"     9\tLine nine\n" +
		"    10\tLine ten\n" +
		strings.Join(makeExpectedNumberedLines(11, 99), "\n") + "\n" +
		"   100\tLine one hundred"
	if got != want {
		t.Fatalf("View() output byte mismatch:\n got: %q\nwant: %q", got, want)
	}
}

func TestMemoryToolUsecase_allCommandsRoundTrip(t *testing.T) {
	t.Parallel()

	repo := newMemoryToolRepositoryStub(t, nil)
	sut := usecase.NewMemoryToolUsecase(repo, fixedClock{})
	ctx := context.Background()

	result, err := sut.Create(ctx, "/memories/notes.txt", "alpha\nbeta\n")
	assertResult(t, result, err)
	result, err = sut.Insert(ctx, "/memories/notes.txt", 1, "inserted\n")
	assertResult(t, result, err)
	result, err = sut.StrReplace(ctx, "/memories/notes.txt", "beta", "gamma")
	assertResult(t, result, err)
	got, err := sut.View(ctx, "/memories/notes.txt", nil)
	if err != nil {
		t.Fatalf("View() error = %v", err)
	}
	if !strings.Contains(got, "     2\tinserted") || !strings.Contains(got, "     3\tgamma") {
		t.Fatalf("View() after edits = %q", got)
	}
	result, err = sut.Rename(ctx, "/memories/notes.txt", "/memories/archive/notes.txt")
	assertResult(t, result, err)
	result, err = sut.Delete(ctx, "/memories/archive")
	assertResult(t, result, err)
	got, err = sut.View(ctx, "/memories/archive/notes.txt", nil)
	if err != nil {
		t.Fatalf("View() deleted file error = %v", err)
	}
	if got != "The path /memories/archive/notes.txt does not exist. Please provide a valid path." {
		t.Fatalf("deleted View() = %q", got)
	}
}

func TestMemoryToolUsecase_insertInMiddlePreservesOriginalEOFState(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		content string
		want    string
	}{
		{
			name:    "original without EOF newline stays without EOF newline",
			content: "alpha\ngamma",
			want:    "alpha\nbeta\ngamma",
		},
		{
			name:    "original with EOF newline stays with EOF newline",
			content: "alpha\ngamma\n",
			want:    "alpha\nbeta\ngamma\n",
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			repo := newMemoryToolRepositoryStub(t, map[string]string{
				"/memories/notes.txt": tt.content,
			})
			sut := usecase.NewMemoryToolUsecase(repo, fixedClock{})

			if _, err := sut.Insert(context.Background(), "/memories/notes.txt", 1, "beta\n"); err != nil {
				t.Fatalf("Insert() error = %v", err)
			}

			got := string(repo.files["/memories/notes.txt"].Content())
			if got != tt.want {
				t.Fatalf("saved content = %q, want %q", got, tt.want)
			}
		})
	}
}

func assertResult(t *testing.T, _ string, err error) {
	t.Helper()
	if err != nil {
		t.Fatalf("command error = %v", err)
	}
}

func makeExpectedNumberedLines(start int, end int) []string {
	labels := map[int]string{
		11: "eleven", 12: "twelve", 13: "thirteen", 14: "fourteen", 15: "fifteen",
		16: "sixteen", 17: "seventeen", 18: "eighteen", 19: "nineteen", 20: "twenty",
		21: "twenty-one", 22: "twenty-two", 23: "twenty-three", 24: "twenty-four", 25: "twenty-five",
		26: "twenty-six", 27: "twenty-seven", 28: "twenty-eight", 29: "twenty-nine", 30: "thirty",
		31: "thirty-one", 32: "thirty-two", 33: "thirty-three", 34: "thirty-four", 35: "thirty-five",
		36: "thirty-six", 37: "thirty-seven", 38: "thirty-eight", 39: "thirty-nine", 40: "forty",
		41: "forty-one", 42: "forty-two", 43: "forty-three", 44: "forty-four", 45: "forty-five",
		46: "forty-six", 47: "forty-seven", 48: "forty-eight", 49: "forty-nine", 50: "fifty",
		51: "fifty-one", 52: "fifty-two", 53: "fifty-three", 54: "fifty-four", 55: "fifty-five",
		56: "fifty-six", 57: "fifty-seven", 58: "fifty-eight", 59: "fifty-nine", 60: "sixty",
		61: "sixty-one", 62: "sixty-two", 63: "sixty-three", 64: "sixty-four", 65: "sixty-five",
		66: "sixty-six", 67: "sixty-seven", 68: "sixty-eight", 69: "sixty-nine", 70: "seventy",
		71: "seventy-one", 72: "seventy-two", 73: "seventy-three", 74: "seventy-four", 75: "seventy-five",
		76: "seventy-six", 77: "seventy-seven", 78: "seventy-eight", 79: "seventy-nine", 80: "eighty",
		81: "eighty-one", 82: "eighty-two", 83: "eighty-three", 84: "eighty-four", 85: "eighty-five",
		86: "eighty-six", 87: "eighty-seven", 88: "eighty-eight", 89: "eighty-nine", 90: "ninety",
		91: "ninety-one", 92: "ninety-two", 93: "ninety-three", 94: "ninety-four", 95: "ninety-five",
		96: "ninety-six", 97: "ninety-seven", 98: "ninety-eight", 99: "ninety-nine",
	}
	lines := make([]string, 0, end-start+1)
	for i := start; i <= end; i++ {
		lines = append(lines, fmt.Sprintf("%6d\tLine %s", i, labels[i]))
	}
	return lines
}

type fixedClock struct{}

func (fixedClock) Now() time.Time {
	return time.Date(2026, 4, 25, 12, 0, 0, 0, time.UTC)
}

type memoryToolRepositoryStub struct {
	files map[string]*model.MemoryToolFile
}

func newMemoryToolRepositoryStub(t *testing.T, files map[string]string) *memoryToolRepositoryStub {
	t.Helper()
	repo := &memoryToolRepositoryStub{files: map[string]*model.MemoryToolFile{}}
	for pathString, content := range files {
		path, err := types.NewMemoryToolPath(pathString)
		if err != nil {
			t.Fatalf("NewMemoryToolPath(%q) error = %v", pathString, err)
		}
		repo.files[path.String()] = model.NewMemoryToolFile(path, []byte(content), fixedClock{}.Now())
	}
	return repo
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
	sort.Slice(files, func(i, j int) bool { return files[i].Path().String() < files[j].Path().String() })
	return files, nil
}

func (r *memoryToolRepositoryStub) DeletePathPrefix(_ context.Context, path types.MemoryToolPath) (int64, error) {
	var deleted int64
	for filePath := range r.files {
		if filePath == path.String() || strings.HasPrefix(filePath, path.String()+"/") {
			delete(r.files, filePath)
			deleted++
		}
	}
	return deleted, nil
}

func (r *memoryToolRepositoryStub) RenamePathPrefix(_ context.Context, oldPath types.MemoryToolPath, newPath types.MemoryToolPath, updatedAt time.Time) (int64, error) {
	moved := map[string]*model.MemoryToolFile{}
	var count int64
	for filePath, file := range r.files {
		if filePath == oldPath.String() || strings.HasPrefix(filePath, oldPath.String()+"/") {
			delete(r.files, filePath)
			newFilePath, _ := types.NewMemoryToolPath(newPath.String() + strings.TrimPrefix(filePath, oldPath.String()))
			moved[newFilePath.String()] = file.WithPath(newFilePath, updatedAt)
			count++
		}
	}
	for path, file := range moved {
		r.files[path] = file
	}
	return count, nil
}
