package model

import (
	"path/filepath"
	"strings"
)

// newHookScriptCommand builds the shell command string that executes a bundled
// hook script with the Traceary binary pre-configured via TRACEARY_BIN.
func newHookScriptCommand(
	scriptsDir string,
	tracearyBin string,
	scriptName string,
	args ...string,
) string {
	parts := make([]string, 0, 3+len(args))
	parts = append(parts,
		"TRACEARY_BIN="+shellQuoteHookValue(tracearyBin),
		"bash",
		shellQuoteHookValue(filepath.Join(scriptsDir, scriptName)),
	)
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
