package filesystem

import "strings"

// newHookRuntimeCommand builds the shell command string that invokes the
// hidden Go hook runtime entrypoints directly.
func newHookRuntimeCommand(tracearyBin string, args ...string) string {
	parts := make([]string, 0, 1+len(args))
	parts = append(parts, shellQuoteHookValue(tracearyBin))
	for _, arg := range args {
		parts = append(parts, shellQuoteHookValue(arg))
	}
	return strings.Join(parts, " ")
}

// shellQuoteHookValue wraps a value in single quotes, escaping nested quotes
// so it can be safely embedded inside a bash command line.
func shellQuoteHookValue(value string) string {
	return "'" + strings.ReplaceAll(value, "'", `'"'"'`) + "'"
}

// managedKeyOf builds a stable identifier for a Traceary-managed hook
// command. The key is formed from the hook script filename and the action
// arguments joined with ":" so that different variants of the same script
// (for example post-compact vs session-start-compact) can be distinguished.
func managedKeyOf(scriptName string, args ...string) string {
	parts := make([]string, 0, 1+len(args))
	parts = append(parts, scriptName)
	parts = append(parts, args...)
	return strings.Join(parts, ":")
}
