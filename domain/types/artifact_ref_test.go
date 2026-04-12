package types_test

import (
	"testing"

	"github.com/duck8823/traceary/domain/types"
)

func TestArtifactRefOf(t *testing.T) {
	t.Parallel()

	ref, err := types.ArtifactRefOf(types.ArtifactRefKindFile, "docs/spec.md")
	if err != nil {
		t.Fatalf("ArtifactRefOf() error = %v", err)
	}
	if ref.Kind() != types.ArtifactRefKindFile {
		t.Fatalf("Kind() = %v, want %v", ref.Kind(), types.ArtifactRefKindFile)
	}
	if ref.Value() != "docs/spec.md" {
		t.Fatalf("Value() = %q, want %q", ref.Value(), "docs/spec.md")
	}
}

func TestArtifactRefOf_RejectsInvalidInput(t *testing.T) {
	t.Parallel()

	if _, err := types.ArtifactRefOf(types.ArtifactRefKind(""), "value"); err == nil {
		t.Fatalf("ArtifactRefOf() error = nil, want error for empty kind")
	}
	if _, err := types.ArtifactRefOf(types.ArtifactRefKindFile, " "); err == nil {
		t.Fatalf("ArtifactRefOf() error = nil, want error for empty value")
	}
}
