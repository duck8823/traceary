package types

import (
	"slices"
	"strings"

	"golang.org/x/xerrors"
)

// EvidenceRefKind classifies evidence references.
type EvidenceRefKind string

const (
	// EvidenceRefKindEvent references an event.
	EvidenceRefKindEvent EvidenceRefKind = "event"
	// EvidenceRefKindSession references a session.
	EvidenceRefKindSession EvidenceRefKind = "session"
	// EvidenceRefKindURL references a URL.
	EvidenceRefKindURL EvidenceRefKind = "url"
	// EvidenceRefKindFile references a file.
	EvidenceRefKindFile EvidenceRefKind = "file"
	// EvidenceRefKindIssue references an issue.
	EvidenceRefKindIssue EvidenceRefKind = "issue"
	// EvidenceRefKindPR references a pull request.
	EvidenceRefKindPR EvidenceRefKind = "pr"
)

var knownEvidenceRefKinds = []EvidenceRefKind{
	EvidenceRefKindEvent,
	EvidenceRefKindSession,
	EvidenceRefKindURL,
	EvidenceRefKindFile,
	EvidenceRefKindIssue,
	EvidenceRefKindPR,
}

// EvidenceRef stores a reference that supports a durable memory.
type EvidenceRef struct {
	kind  EvidenceRefKind
	value string
}

// EvidenceRefKindOf creates an EvidenceRefKind from a string.
func EvidenceRefKindOf(value string) (EvidenceRefKind, error) {
	trimmedValue := strings.TrimSpace(value)
	if trimmedValue == "" {
		return EvidenceRefKind(""), xerrors.Errorf("evidence ref kind must not be empty")
	}
	if slices.Contains(knownEvidenceRefKinds, EvidenceRefKind(trimmedValue)) {
		return EvidenceRefKind(trimmedValue), nil
	}
	return EvidenceRefKind(""), xerrors.Errorf("unknown evidence ref kind: %s", trimmedValue)
}

// EvidenceRefOf creates an EvidenceRef.
func EvidenceRefOf(kind EvidenceRefKind, value string) (EvidenceRef, error) {
	trimmedValue := strings.TrimSpace(value)
	if trimmedValue == "" {
		return EvidenceRef{}, xerrors.Errorf("evidence ref value must not be empty")
	}
	if _, err := EvidenceRefKindOf(kind.String()); err != nil {
		return EvidenceRef{}, err
	}
	return EvidenceRef{kind: kind, value: trimmedValue}, nil
}

// Kind returns the evidence ref kind.
func (e EvidenceRef) Kind() EvidenceRefKind { return e.kind }

// Value returns the evidence ref value.
func (e EvidenceRef) Value() string { return e.value }

// String returns the string representation.
func (e EvidenceRefKind) String() string { return string(e) }
