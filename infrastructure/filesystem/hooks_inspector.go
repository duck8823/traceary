package filesystem

import (
	"encoding/json"

	"golang.org/x/xerrors"

	"github.com/duck8823/traceary/application"
)

// HooksInspector implements application.HooksInspector by parsing the JSON
// payload stored by a target client's hook configuration file.
type HooksInspector struct{}

// NewHooksInspector constructs a HooksInspector.
func NewHooksInspector() *HooksInspector {
	return &HooksInspector{}
}

// Inspect parses the given hook configuration content and reports whether
// it contains a top-level "hooks" field and whether any Traceary-managed
// hook was detected. It returns application.ErrHookConfigNotJSONObject when
// the payload is not a JSON object and
// application.ErrHookConfigInvalidHooksField when the "hooks" field has the
// wrong shape.
func (i *HooksInspector) Inspect(content []byte) (bool, bool, error) {
	root := map[string]json.RawMessage{}
	if err := json.Unmarshal(content, &root); err != nil {
		return false, false, xerrors.Errorf("%w: %v", application.ErrHookConfigNotJSONObject, err)
	}

	hooksValue, ok := root["hooks"]
	if !ok {
		return false, false, nil
	}

	hooksMap := map[string][]hookMatcherDocument{}
	if err := json.Unmarshal(hooksValue, &hooksMap); err != nil {
		return true, false, xerrors.Errorf("%w: %v", application.ErrHookConfigInvalidHooksField, err)
	}

	for _, matchers := range hooksMap {
		for _, matcher := range matchers {
			for _, command := range matcher.Hooks {
				if isTracearyManagedHookCommandDocument(command, nil) {
					return true, true, nil
				}
			}
		}
	}

	return true, false, nil
}

// ExtractManagedKeyFromEntry delegates to the free ExtractTracearyManagedKeyFromEntry
// function so presentation code can consume the canonical-key extraction
// through the application.HooksInspector interface without importing the
// infrastructure package directly.
func (i *HooksInspector) ExtractManagedKeyFromEntry(name, command string) string {
	return ExtractTracearyManagedKeyFromEntry(name, command)
}
