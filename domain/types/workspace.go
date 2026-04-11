package types

import (
	"strings"

	"golang.org/x/xerrors"
)

// Workspace is a value object representing the work context (e.g. github.com/org/repo, /abs/path).
type Workspace string

// WorkspaceOf creates a Workspace from a string.
func WorkspaceOf(value string) (Workspace, error) {
	trimmedValue := strings.TrimSpace(value)
	if trimmedValue == "" {
		return Workspace(""), xerrors.Errorf("workspace must not be empty")
	}
	return Workspace(trimmedValue), nil
}

// String returns the string representation.
func (w Workspace) String() string { return string(w) }
