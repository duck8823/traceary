package types_test

import (
	"testing"

	"github.com/duck8823/traceary/domain/types"
)

func TestNewMemoryToolPath_rejectsMaliciousTraversalInputs(t *testing.T) {
	t.Parallel()

	tests := []string{
		"../memories/file.txt",
		"/tmp/memories/file.txt",
		"/memories/../secrets.txt",
		"/memories/..\\secrets.txt",
		"/memories/a/../../secrets.txt",
		"/memories/%2e%2e%2fsecrets.txt",
		"/memories/%2E%2E%2Fsecrets.txt",
		"/memories/%2e%2e%5csecrets.txt",
		"/memories/%252e%252e%252fsecrets.txt",
		"/memories2/secrets.txt",
		"/memories/%2e%2e",
		"/memories/%2E%2e/%2e%2E/secrets.txt",
	}

	for _, input := range tests {
		input := input
		t.Run(input, func(t *testing.T) {
			t.Parallel()

			if got, err := types.NewMemoryToolPath(input); err == nil {
				t.Fatalf("NewMemoryToolPath(%q) = %q, want error", input, got.String())
			}
		})
	}
}

func TestNewMemoryToolPath_canonicalizesSafeInputs(t *testing.T) {
	t.Parallel()

	got, err := types.NewMemoryToolPath("/memories/project/./notes.txt")
	if err != nil {
		t.Fatalf("NewMemoryToolPath() error = %v", err)
	}
	if got.String() != "/memories/project/notes.txt" {
		t.Fatalf("path = %q, want /memories/project/notes.txt", got.String())
	}
}
