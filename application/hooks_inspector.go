package application

// HooksInspector inspects a hook configuration JSON payload and reports
// high-level state relevant to diagnostics. Implementations live in the
// infrastructure layer so presentation code can depend only on this
// interface.
type HooksInspector interface {
	// Inspect parses the given hook configuration content and reports
	// whether it contains a top-level "hooks" field and whether any
	// Traceary-managed hook was detected.
	//
	// Returned errors are the sentinel errors defined in hooks_errors.go:
	// ErrHookConfigNotJSONObject when the payload is not a JSON object and
	// ErrHookConfigInvalidHooksField when the "hooks" field has the wrong
	// shape.
	Inspect(content []byte) (hasHooksField bool, hasTracearyManagedHook bool, err error)
}
