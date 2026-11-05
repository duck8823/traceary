//go:build darwin || linux

package runtime

import (
	"bytes"
	"encoding/json"
	"io"
	"unicode/utf8"

	domain "github.com/duck8823/traceary/domain/runtime"
	"github.com/google/uuid"
)

// antigravityStatuslineSnapshot is representation only, never native provenance,
// provider qualification, current interaction or recovery/control authority.
type antigravityStatuslineSnapshot struct {
	SessionID      string
	CWD            string
	ProjectDir     string
	Model          string
	AccountPresent bool
}

// decodeAntigravityStatusline borrows metadata from one callback. It performs no
// selection, deduplication, ordering, source authentication or publication.
func decodeAntigravityStatusline(data []byte) (antigravityStatuslineSnapshot, error) {
	empty := antigravityStatuslineSnapshot{}
	if len(data) > maxNativeStartBytes {
		return empty, domain.ErrLimit
	}
	if !utf8.Valid(data) {
		return empty, domain.ErrInvalid
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	if err := antigravityStatuslineJSONValue(decoder, 0); err != nil {
		return empty, err
	}
	if _, err := decoder.Token(); err != io.EOF {
		return empty, domain.ErrInvalid
	}
	fields, err := nativeHistoryFields(data, "session_id", "conversation_id", "cwd", "workspace", "model", "version", "product", "email")
	if err != nil {
		return empty, err
	}
	version, err := nativeHistoryString(fields, "version")
	if err != nil {
		return empty, err
	}
	product, err := nativeHistoryString(fields, "product")
	if err != nil {
		return empty, err
	}
	if version != "1.5.0" || product != "antigravity" {
		return empty, domain.ErrUnsupported
	}
	var ids [2]string
	for i, key := range []string{"session_id", "conversation_id"} {
		raw := bytes.TrimSpace(fields[key])
		if len(raw) == 0 || raw[0] != '"' || json.Unmarshal(raw, &ids[i]) != nil {
			return empty, domain.ErrInvalid
		}
	}
	if ids[0] == "" || ids[1] == "" {
		return empty, domain.ErrNotFound
	}
	if ids[0] != ids[1] {
		return empty, domain.ErrChanged
	}
	id, err := uuid.Parse(ids[0])
	if err != nil || id.String() != ids[0] || id.Version() != 4 || id.Variant() != uuid.RFC4122 {
		return empty, domain.ErrInvalid
	}
	cwd, err := nativeHistoryString(fields, "cwd")
	if err != nil || !canonicalNativePath(cwd) {
		return empty, domain.ErrInvalid
	}
	workspace, err := nativeHistoryFields(fields["workspace"], "current_dir", "project_dir")
	if err != nil {
		return empty, err
	}
	current, err := nativeHistoryString(workspace, "current_dir")
	if err != nil || !canonicalNativePath(current) {
		return empty, domain.ErrInvalid
	}
	project, err := nativeHistoryString(workspace, "project_dir")
	if err != nil || !canonicalNativePath(project) {
		return empty, domain.ErrInvalid
	}
	if cwd != current {
		return empty, domain.ErrChanged
	}
	model, err := nativeHistoryFields(fields["model"], "id", "display_name")
	if err != nil {
		return empty, err
	}
	label, err := nativeHistoryString(model, "id")
	if err != nil {
		return empty, err
	}
	display, err := nativeHistoryString(model, "display_name")
	if err != nil {
		return empty, err
	}
	slug := antigravityStatuslineModel(label)
	if slug == "" || antigravityStatuslineModel(display) == "" {
		return empty, domain.ErrUnsupported
	}
	if label != display {
		return empty, domain.ErrChanged
	}
	present := false
	if raw, ok := fields["email"]; ok && !bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		var value string
		if json.Unmarshal(raw, &value) != nil || len(value) > 320 {
			return empty, domain.ErrInvalid
		}
		// Presence is not an authentication/provider class. Never retain the value
		// or its hash, including on a malformed or unsupported callback.
		present = value != ""
	}
	return antigravityStatuslineSnapshot{SessionID: ids[0], CWD: cwd, ProjectDir: project, Model: slug, AccountPresent: present}, nil
}

// Only literal native catalog labels are normalized. This is format selection,
// not execution certification for these models or any compatible source range.
func antigravityStatuslineModel(label string) string {
	switch label {
	case "Gemini 4 Argon (Medium)":
		return "gemini-4-argon-medium"
	case "Gemini 4 Argon (High)":
		return "gemini-4-argon-high"
	case "Gemini 4 Argon (Low)":
		return "gemini-4-argon-low"
	default:
		return ""
	}
}

// The private journal codec has different key casing/cardinality rules. Keep
// vendor topology validation separate, without weakening any existing limits.
func antigravityStatuslineJSONValue(decoder *json.Decoder, depth int) error {
	token, err := decoder.Token()
	if err != nil {
		return domain.ErrInvalid
	}
	delimiter, container := token.(json.Delim)
	if !container {
		return nil
	}
	if depth >= 16 {
		return domain.ErrLimit
	}
	switch delimiter {
	case '{':
		keys := make(map[string]struct{})
		for decoder.More() {
			token, err = decoder.Token()
			if err != nil {
				return domain.ErrInvalid
			}
			key, ok := token.(string)
			if !ok {
				return domain.ErrInvalid
			}
			if _, exists := keys[key]; exists {
				return domain.ErrInvalid
			}
			keys[key] = struct{}{}
			if len(keys) > 128 {
				return domain.ErrLimit
			}
			if err = antigravityStatuslineJSONValue(decoder, depth+1); err != nil {
				return err
			}
		}
		end, e := decoder.Token()
		if e != nil || end != json.Delim('}') {
			return domain.ErrInvalid
		}
	case '[':
		count := 0
		for decoder.More() {
			count++
			if count > 128 {
				return domain.ErrLimit
			}
			if err = antigravityStatuslineJSONValue(decoder, depth+1); err != nil {
				return err
			}
		}
		end, e := decoder.Token()
		if e != nil || end != json.Delim(']') {
			return domain.ErrInvalid
		}
	default:
		return domain.ErrInvalid
	}
	return nil
}
