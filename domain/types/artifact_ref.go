package types

import (
	"slices"
	"strings"

	"golang.org/x/xerrors"
)

// ArtifactRefKind classifies artifact references.
type ArtifactRefKind string

const (
	// ArtifactRefKindURL references a URL.
	ArtifactRefKindURL ArtifactRefKind = "url"
	// ArtifactRefKindFile references a file.
	ArtifactRefKindFile ArtifactRefKind = "file"
	// ArtifactRefKindIssue references an issue.
	ArtifactRefKindIssue ArtifactRefKind = "issue"
	// ArtifactRefKindPR references a pull request.
	ArtifactRefKindPR ArtifactRefKind = "pr"
)

var knownArtifactRefKinds = []ArtifactRefKind{
	ArtifactRefKindURL,
	ArtifactRefKindFile,
	ArtifactRefKindIssue,
	ArtifactRefKindPR,
}

// ArtifactRef stores a reference to an artifact related to a memory.
type ArtifactRef struct {
	kind  ArtifactRefKind
	value string
}

// ArtifactRefKindFrom creates an ArtifactRefKind from a string.
func ArtifactRefKindFrom(value string) (ArtifactRefKind, error) {
	trimmedValue := strings.TrimSpace(value)
	if trimmedValue == "" {
		return ArtifactRefKind(""), xerrors.Errorf("artifact ref kind must not be empty")
	}
	if slices.Contains(knownArtifactRefKinds, ArtifactRefKind(trimmedValue)) {
		return ArtifactRefKind(trimmedValue), nil
	}
	return ArtifactRefKind(""), xerrors.Errorf("unknown artifact ref kind: %s", trimmedValue)
}

// ArtifactRefFrom creates an ArtifactRef.
func ArtifactRefFrom(kind ArtifactRefKind, value string) (ArtifactRef, error) {
	trimmedValue := strings.TrimSpace(value)
	if trimmedValue == "" {
		return ArtifactRef{}, xerrors.Errorf("artifact ref value must not be empty")
	}
	if _, err := ArtifactRefKindFrom(kind.String()); err != nil {
		return ArtifactRef{}, err
	}
	return ArtifactRef{kind: kind, value: trimmedValue}, nil
}

// Kind returns the artifact ref kind.
func (a ArtifactRef) Kind() ArtifactRefKind { return a.kind }

// Value returns the artifact ref value.
func (a ArtifactRef) Value() string { return a.value }

// String returns the string representation.
func (a ArtifactRefKind) String() string { return string(a) }
