package filesystem

import (
	"encoding/json"
	"errors"

	"golang.org/x/xerrors"
)

// ErrHookConfigNotJSONObject indicates that a hook configuration file was
// successfully read but its top-level payload was not a JSON object.
var ErrHookConfigNotJSONObject = errors.New("config file must be a JSON object")

// ErrHookConfigInvalidHooksField indicates that a hook configuration file
// contained a top-level "hooks" field that was not an object of hook arrays.
var ErrHookConfigInvalidHooksField = errors.New("hooks field must be an object of hook arrays")

// HooksInspection reports the high-level state of a client hooks
// configuration file after parsing it.
type HooksInspection struct {
	// HasHooksField is true when the file contained a top-level "hooks" key.
	HasHooksField bool
	// HasTracearyManagedHook is true when at least one hook command in the
	// file matched a Traceary-managed script.
	HasTracearyManagedHook bool
}

// InspectHookConfigContent inspects a hook configuration JSON payload and
// reports whether it contains any Traceary-managed hook commands. It returns
// ErrHookConfigNotJSONObject when the payload is not a JSON object and
// ErrHookConfigInvalidHooksField when the "hooks" field has the wrong shape.
func InspectHookConfigContent(content []byte) (HooksInspection, error) {
	root := map[string]json.RawMessage{}
	if err := json.Unmarshal(content, &root); err != nil {
		return HooksInspection{}, xerrors.Errorf("%w: %v", ErrHookConfigNotJSONObject, err)
	}

	inspection := HooksInspection{}
	hooksValue, ok := root["hooks"]
	if !ok {
		return inspection, nil
	}
	inspection.HasHooksField = true

	hooksMap := map[string][]hookMatcherDocument{}
	if err := json.Unmarshal(hooksValue, &hooksMap); err != nil {
		return inspection, xerrors.Errorf("%w: %v", ErrHookConfigInvalidHooksField, err)
	}

	for _, matchers := range hooksMap {
		for _, matcher := range matchers {
			for _, command := range matcher.Hooks {
				if isTracearyManagedHookCommandDocument(command) {
					inspection.HasTracearyManagedHook = true
					return inspection, nil
				}
			}
		}
	}

	return inspection, nil
}
