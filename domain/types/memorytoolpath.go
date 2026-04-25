package types

import (
	"net/url"
	"path/filepath"
	"strings"

	"golang.org/x/xerrors"
)

const memoryToolRootPath = "/memories"

// MemoryToolPath is a canonical, traversal-safe path inside the Anthropic
// memory tool root.
type MemoryToolPath string

// NewMemoryToolPath validates and canonicalizes a memory tool path.
func NewMemoryToolPath(raw string) (MemoryToolPath, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return "", xerrors.Errorf("memory tool path must not be empty")
	}
	if hasEncodedTraversal(trimmed) {
		return "", xerrors.Errorf("memory tool path must not contain encoded traversal: %s", raw)
	}
	if hasTraversal(trimmed) {
		return "", xerrors.Errorf("memory tool path must not contain traversal: %s", raw)
	}
	if !strings.HasPrefix(trimmed, memoryToolRootPath) {
		return "", xerrors.Errorf("memory tool path must start with /memories: %s", raw)
	}

	normalized := strings.ReplaceAll(trimmed, "\\", "/")
	cleaned := filepath.ToSlash(filepath.Clean(normalized))
	if !isMemoryToolPathWithinRoot(cleaned) {
		return "", xerrors.Errorf("memory tool path escapes /memories: %s", raw)
	}
	if hasHiddenEmptyOrParentSegment(cleaned) {
		return "", xerrors.Errorf("memory tool path contains an invalid segment: %s", raw)
	}

	return MemoryToolPath(cleaned), nil
}

// String returns the canonical path string.
func (p MemoryToolPath) String() string { return string(p) }

// IsRoot reports whether the path is exactly /memories.
func (p MemoryToolPath) IsRoot() bool { return p.String() == memoryToolRootPath }

// IsDescendantOf reports whether p is below the supplied directory path.
func (p MemoryToolPath) IsDescendantOf(parent MemoryToolPath) bool {
	parentString := parent.String()
	return p.String() != parentString && strings.HasPrefix(p.String(), parentString+"/")
}

func isMemoryToolPathWithinRoot(path string) bool {
	return path == memoryToolRootPath || strings.HasPrefix(path, memoryToolRootPath+"/")
}

func hasTraversal(path string) bool {
	normalized := strings.ReplaceAll(path, "\\", "/")
	return normalized == ".." ||
		strings.HasPrefix(normalized, "../") ||
		strings.Contains(normalized, "/../") ||
		strings.HasSuffix(normalized, "/..")
}

func hasEncodedTraversal(path string) bool {
	decoded := path
	for range 3 {
		next, err := url.PathUnescape(decoded)
		if err != nil {
			return true
		}
		if next == decoded {
			break
		}
		decoded = next
	}
	return decoded != path && hasTraversal(strings.ToLower(decoded))
}

func hasHiddenEmptyOrParentSegment(path string) bool {
	for _, segment := range strings.Split(path, "/") {
		if segment == "" {
			continue
		}
		if segment == "." || segment == ".." {
			return true
		}
	}
	return false
}
