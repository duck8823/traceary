package application

// HookDuplicate describes duplicate Traceary-managed hook registrations for
// the same host event, matcher, and managed command key.
type HookDuplicate struct {
	Event      string
	Matcher    string
	ManagedKey string
	Count      int
}

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
	// DuplicateManagedHooks reports Traceary-managed hook registrations that
	// appear more than once for the same event, matcher, and managed key.
	// Missing hooks fields return an empty slice. Returned errors are the
	// same sentinels as Inspect.
	DuplicateManagedHooks(content []byte) ([]HookDuplicate, error)
	// ExtractManagedKeyFromEntry returns the canonical Traceary-managed
	// key extracted from a hook entry's name / command pair, or an
	// empty string if the entry is not Traceary-managed. Presentation
	// code uses this to recognise installed hook entries without
	// re-implementing the command parsing.
	ExtractManagedKeyFromEntry(name, command string) string
}
