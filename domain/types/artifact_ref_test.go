package types_test

import (
	"testing"

	"github.com/duck8823/traceary/domain/types"
)

func TestArtifactRefFrom(t *testing.T) {
	t.Parallel()

	ref, err := types.ArtifactRefFrom(types.ArtifactRefKindFile, "docs/spec.md")
	if err != nil {
		t.Fatalf("ArtifactRefFrom() error = %v", err)
	}
	if ref.Kind() != types.ArtifactRefKindFile {
		t.Fatalf("Kind() = %v, want %v", ref.Kind(), types.ArtifactRefKindFile)
	}
	if ref.Value() != "docs/spec.md" {
		t.Fatalf("Value() = %q, want %q", ref.Value(), "docs/spec.md")
	}
}

func TestArtifactRefFrom_RejectsInvalidInput(t *testing.T) {
	t.Parallel()

	if _, err := types.ArtifactRefFrom(types.ArtifactRefKind(""), "value"); err == nil {
		t.Fatalf("ArtifactRefFrom() error = nil, want error for empty kind")
	}
	if _, err := types.ArtifactRefFrom(types.ArtifactRefKindFile, " "); err == nil {
		t.Fatalf("ArtifactRefFrom() error = nil, want error for empty value")
	}
}
