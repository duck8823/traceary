package types

import (
	"path"
	"strings"

	"golang.org/x/xerrors"
)

// Workspace is a value object representing the work context.
// The format is not restricted; typical values include GitHub repository
// paths (github.com/org/repo) and absolute filesystem paths.
type Workspace string

// WorkspaceFrom creates a Workspace from a string.
func WorkspaceFrom(value string) (Workspace, error) {
	trimmedValue := strings.TrimSpace(value)
	if trimmedValue == "" {
		return Workspace(""), xerrors.Errorf("workspace must not be empty")
	}
	return Workspace(trimmedValue), nil
}

// String returns the string representation.
func (w Workspace) String() string { return string(w) }

// IsLocalPath reports whether the workspace value looks like an absolute
// filesystem path. Only filesystem-style workspaces participate in
// parent/child fallback; values that look like remote URLs
// (e.g. github.com/org/repo) are kept as exact matches because their path
// segments do not represent filesystem ancestry.
func (w Workspace) IsLocalPath() bool {
	value := string(w)
	return strings.HasPrefix(value, "/") || isWindowsDriveAbsolute(value)
}

// AncestorWorkspaces returns the ancestor workspaces of w in order from
// the closest parent to the filesystem root. For non-filesystem workspaces
// (e.g. github.com/org/repo) the result is empty so callers always get
// exact-match behavior. The receiver itself is never included.
func (w Workspace) AncestorWorkspaces() []Workspace {
	if !w.IsLocalPath() {
		return nil
	}
	if isWindowsDriveAbsolute(string(w)) {
		return windowsDriveAncestorWorkspaces(string(w))
	}
	cleaned := path.Clean(string(w))
	if cleaned == "/" || cleaned == "." {
		return nil
	}

	ancestors := make([]Workspace, 0)
	for {
		parent := path.Dir(cleaned)
		if parent == cleaned {
			break
		}
		ancestors = append(ancestors, Workspace(parent))
		if parent == "/" {
			break
		}
		cleaned = parent
	}
	return ancestors
}

func isWindowsDriveAbsolute(value string) bool {
	if len(value) < 3 {
		return false
	}
	drive := value[0]
	if !isASCIIAlpha(drive) {
		return false
	}
	return value[1] == ':' && isPathSeparator(value[2])
}

func isASCIIAlpha(value byte) bool {
	return (value >= 'A' && value <= 'Z') || (value >= 'a' && value <= 'z')
}

func isPathSeparator(value byte) bool {
	return value == '/' || value == '\\'
}

func windowsDriveAncestorWorkspaces(value string) []Workspace {
	separator := "/"
	if value[2] == '\\' {
		separator = "\\"
	}
	slashValue := strings.ReplaceAll(value, "\\", "/")
	volume := slashValue[:2]
	cleanedRest := path.Clean(slashValue[2:])
	if cleanedRest == "." {
		cleanedRest = "/"
	}
	if cleanedRest == "/" {
		return nil
	}

	ancestors := make([]Workspace, 0)
	for {
		parentRest := path.Dir(cleanedRest)
		parent := volume + parentRest
		if separator == "\\" {
			parent = strings.ReplaceAll(parent, "/", "\\")
		}
		ancestors = append(ancestors, Workspace(parent))
		if parentRest == "/" {
			break
		}
		cleanedRest = parentRest
	}
	return ancestors
}
