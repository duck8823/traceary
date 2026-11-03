package runtime

import (
	"encoding/json"
	domain "github.com/duck8823/traceary/domain/runtime"
	"strings"
)

// uniqueJSONValue rejects duplicate object keys before decoding into a Go struct,
// whose normal JSON semantics would silently accept the last occurrence.
func uniqueJSONValue(d *json.Decoder, depth int) error {
	if depth > 8 {
		return domain.ErrInvalid
	}
	token, err := d.Token()
	if err != nil {
		return domain.ErrInvalid
	}
	delimiter, ok := token.(json.Delim)
	if !ok {
		return nil
	}
	switch delimiter {
	case '{':
		keys := make(map[string]struct{})
		for d.More() {
			key, err := d.Token()
			if err != nil {
				return domain.ErrInvalid
			}
			name, ok := key.(string)
			if !ok || name != strings.ToLower(name) {
				return domain.ErrInvalid
			}
			if _, exists := keys[name]; exists {
				return domain.ErrInvalid
			}
			keys[name] = struct{}{}
			if len(keys) > 16 {
				return domain.ErrInvalid
			}
			if err = uniqueJSONValue(d, depth+1); err != nil {
				return err
			}
		}
		end, err := d.Token()
		if err != nil || end != json.Delim('}') {
			return domain.ErrInvalid
		}
	case '[':
		count := 0
		for d.More() {
			count++
			if count > domain.MaxWorktrees+domain.MaxProjects {
				return domain.ErrLimit
			}
			if err = uniqueJSONValue(d, depth+1); err != nil {
				return err
			}
		}
		end, err := d.Token()
		if err != nil || end != json.Delim(']') {
			return domain.ErrInvalid
		}
	default:
		return domain.ErrInvalid
	}
	return nil
}
