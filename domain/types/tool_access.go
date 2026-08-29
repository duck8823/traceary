package types

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"strings"
	"unicode/utf8"

	"golang.org/x/xerrors"
)

// ToolAccess classifies a host tool by the kind of access its output represents.
// Unknown is the safe default: an unrecognised tool keeps full capture.
type ToolAccess string

const (
	// ToolAccessUnknown keeps the full-capture contract.
	ToolAccessUnknown ToolAccess = "unknown"
	// ToolAccessReadOnly records access facts instead of output text.
	ToolAccessReadOnly ToolAccess = "read_only"

	readOnlyOutputMetadataCapture = "metadata_only"
	readOnlyTargetLimit           = 8
	readOnlyTargetMaxBytes        = 512
)

// ReadOnlyOutputMetadata is the access-fact record stored instead of output text.
// Truncated reports that the full response exceeded the configured capture cap,
// i.e. what the previous contract would have truncated.
type ReadOnlyOutputMetadata struct {
	paths     []string
	bytes     int
	sha256    string
	truncated bool
}

// Paths returns the access targets, bounded and ordered as captured.
func (m ReadOnlyOutputMetadata) Paths() []string { return append([]string(nil), m.paths...) }

// Bytes returns the redacted response size in bytes.
func (m ReadOnlyOutputMetadata) Bytes() int { return m.bytes }

// SHA256 returns the lower-case hex digest of the redacted response.
func (m ReadOnlyOutputMetadata) SHA256() string { return m.sha256 }

// Truncated reports that the full response exceeded the capture cap.
func (m ReadOnlyOutputMetadata) Truncated() bool { return m.truncated }

type readOnlyOutputMetadataJSON struct {
	Bytes     int      `json:"bytes"`
	Capture   string   `json:"capture"`
	Paths     []string `json:"paths,omitempty"`
	SHA256    string   `json:"sha256"`
	Truncated bool     `json:"truncated"`
}

// EncodeReadOnlyOutputMetadata returns the canonical JSON stored on the audit
// row and hashed into the delivery fingerprint. Keys are sorted; HTML is not
// escaped; there is no trailing newline.
func EncodeReadOnlyOutputMetadata(metadata ReadOnlyOutputMetadata) (string, error) {
	payload := readOnlyOutputMetadataJSON{
		Bytes:     metadata.bytes,
		Capture:   readOnlyOutputMetadataCapture,
		Paths:     metadata.Paths(),
		SHA256:    metadata.sha256,
		Truncated: metadata.truncated,
	}
	if len(payload.Paths) == 0 {
		payload.Paths = nil
	}
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(payload); err != nil {
		return "", xerrors.Errorf("encode read-only output metadata: %w", err)
	}
	return strings.TrimSuffix(buf.String(), "\n"), nil
}

// DecodeReadOnlyOutputMetadata restores persisted metadata. Empty input is None.
func DecodeReadOnlyOutputMetadata(raw string) (Optional[ReadOnlyOutputMetadata], error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return None[ReadOnlyOutputMetadata](), nil
	}
	var payload readOnlyOutputMetadataJSON
	if err := json.Unmarshal([]byte(trimmed), &payload); err != nil {
		return None[ReadOnlyOutputMetadata](), xerrors.Errorf("decode read-only output metadata: %w", err)
	}
	if payload.Capture != readOnlyOutputMetadataCapture {
		return None[ReadOnlyOutputMetadata](), xerrors.Errorf("decode read-only output metadata: unknown capture %q", payload.Capture)
	}
	return Some(ReadOnlyOutputMetadataRestore(payload.Paths, payload.Bytes, payload.SHA256, payload.Truncated)), nil
}

// ReadOnlyOutputMetadataOf derives the access facts from the redacted response.
// maxOutputBytes <= 0 means "no cap", so nothing is reported as truncated.
func ReadOnlyOutputMetadataOf(paths []string, output string, maxOutputBytes int) ReadOnlyOutputMetadata {
	sum := sha256.Sum256([]byte(output))
	truncated := maxOutputBytes > 0 && len(output) > maxOutputBytes
	return ReadOnlyOutputMetadata{
		paths:     boundReadOnlyTargets(paths),
		bytes:     len(output),
		sha256:    hex.EncodeToString(sum[:]),
		truncated: truncated,
	}
}

// ReadOnlyOutputMetadataRestore rebuilds a persisted value without recomputing it.
func ReadOnlyOutputMetadataRestore(paths []string, bytes int, digest string, truncated bool) ReadOnlyOutputMetadata {
	return ReadOnlyOutputMetadata{
		paths:     boundReadOnlyTargets(paths),
		bytes:     bytes,
		sha256:    digest,
		truncated: truncated,
	}
}

var readOnlyToolsByHost = map[Client]map[string]struct{}{
	Client("claude"): {
		"Read": {}, "NotebookRead": {}, "Grep": {}, "Glob": {}, "WebFetch": {},
	},
	Client("grok"): {
		"read_file": {}, "grep": {}, "list_dir": {},
	},
	Client("kimi"): {
		"Read": {}, "Grep": {}, "Glob": {}, "ReadMediaFile": {},
	},
	Client("gemini"): {
		"read_file": {}, "read_many_files": {}, "list_directory": {}, "glob": {}, "search_file_content": {},
	},
}

var readOnlyToolTargetKeysByHost = map[Client]map[string][]string{
	Client("claude"): {
		"Read":         {"file_path"},
		"NotebookRead": {"notebook_path"},
		"Grep":         {"pattern", "path"},
		"Glob":         {"pattern", "path"},
		"WebFetch":     {"url"},
	},
	Client("grok"): {
		"read_file": {"target_file", "path", "file_path", "absolute_path"},
		"grep":      {"pattern", "path"},
		"list_dir":  {"target_directory", "path", "directory", "dir"},
	},
	Client("kimi"): {
		"Read":          {"path", "file_path"},
		"Grep":          {"pattern", "path"},
		"Glob":          {"pattern", "path"},
		"ReadMediaFile": {"path", "file_path"},
	},
	Client("gemini"): {
		"read_file":           {"absolute_path", "path", "file_path"},
		"read_many_files":     {"paths", "path"},
		"list_directory":      {"path", "directory", "dir"},
		"glob":                {"pattern", "path"},
		"search_file_content": {"pattern", "path"},
	},
}

var readOnlyGenericTargetKeys = []string{
	"path", "file_path", "absolute_path", "target_file", "paths", "directory", "dir", "pattern", "url", "target_directory",
}

// ToolAccessOf classifies a host tool. Host is the hook client argument
// ("claude", "codex", "gemini", "grok", "kimi", "antigravity"), not Client("hook").
func ToolAccessOf(host Client, toolName string) ToolAccess {
	name := strings.TrimSpace(toolName)
	if host.String() == "" || name == "" {
		return ToolAccessUnknown
	}
	tools, ok := readOnlyToolsByHost[host]
	if !ok {
		return ToolAccessUnknown
	}
	if _, ok := tools[name]; ok {
		return ToolAccessReadOnly
	}
	return ToolAccessUnknown
}

// ReadOnlyToolTargetsOf extracts the access targets a read-only tool named in its
// input. It returns nil for tools without a path-like target.
func ReadOnlyToolTargetsOf(host Client, toolName string, toolInput map[string]any) []string {
	if ToolAccessOf(host, toolName) != ToolAccessReadOnly {
		return nil
	}
	name := strings.TrimSpace(toolName)
	keys := append([]string(nil), readOnlyToolTargetKeysByHost[host][name]...)
	keys = append(keys, readOnlyGenericTargetKeys...)
	var collected []string
	seen := map[string]struct{}{}
	for _, key := range keys {
		appendTargetValues(&collected, seen, toolInput[key])
	}
	return boundReadOnlyTargets(collected)
}

func appendTargetValues(dst *[]string, seen map[string]struct{}, value any) {
	switch typed := value.(type) {
	case string:
		addReadOnlyTarget(dst, seen, typed)
	case []any:
		for _, item := range typed {
			if text, ok := item.(string); ok {
				addReadOnlyTarget(dst, seen, text)
			}
		}
	case []string:
		for _, text := range typed {
			addReadOnlyTarget(dst, seen, text)
		}
	}
}

func addReadOnlyTarget(dst *[]string, seen map[string]struct{}, value string) {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return
	}
	if _, ok := seen[trimmed]; ok {
		return
	}
	seen[trimmed] = struct{}{}
	*dst = append(*dst, trimmed)
}

func boundReadOnlyTargets(paths []string) []string {
	if len(paths) == 0 {
		return nil
	}
	out := make([]string, 0, len(paths))
	seen := map[string]struct{}{}
	for _, path := range paths {
		trimmed := strings.TrimSpace(path)
		if trimmed == "" {
			continue
		}
		if utf8.RuneCountInString(trimmed) > 0 && len(trimmed) > readOnlyTargetMaxBytes {
			trimmed = takeUTF8PrefixBytes(trimmed, readOnlyTargetMaxBytes)
		}
		if _, ok := seen[trimmed]; ok {
			continue
		}
		seen[trimmed] = struct{}{}
		out = append(out, trimmed)
		if len(out) == readOnlyTargetLimit {
			break
		}
	}
	return out
}

func takeUTF8PrefixBytes(value string, limit int) string {
	if limit <= 0 || len(value) <= limit {
		return value
	}
	for limit > 0 && !utf8.ValidString(value[:limit]) {
		limit--
	}
	return value[:limit]
}
