package application

import "errors"

// ErrHookConfigNotJSONObject indicates that a hook configuration file was
// successfully read but its top-level payload was not a JSON object.
var ErrHookConfigNotJSONObject = errors.New("config file must be a JSON object")

// ErrHookConfigInvalidHooksField indicates that a hook configuration file
// contained a top-level "hooks" field that was not an object of hook arrays.
var ErrHookConfigInvalidHooksField = errors.New("hooks field must be an object of hook arrays")
