package filesystem

import (
	"encoding/json"
	"regexp"
	"strings"

	"golang.org/x/xerrors"

	"github.com/duck8823/traceary/domain/model"
)

// mergeHooksDocument merges the desired Hooks aggregate with the existing JSON
// settings bytes, replacing all previously Traceary-managed hooks while
// preserving user-defined ones and any unrelated top-level fields.
func mergeHooksDocument(existingContent []byte, hooks model.Hooks) ([]byte, error) {
	if len(strings.TrimSpace(string(existingContent))) == 0 {
		return marshalHooks(hooks)
	}

	root := map[string]json.RawMessage{}
	if err := json.Unmarshal(existingContent, &root); err != nil {
		return nil, xerrors.Errorf("existing settings file must contain a JSON object: %w", err)
	}

	existingHooks := map[string][]hookMatcherDocument{}
	if hooksValue, ok := root["hooks"]; ok && len(strings.TrimSpace(string(hooksValue))) > 0 {
		if err := json.Unmarshal(hooksValue, &existingHooks); err != nil {
			return nil, xerrors.Errorf("existing hooks field must be a JSON object whose values are hook arrays: %w", err)
		}
	}

	desired := hooksDocumentOf(hooks)
	for event, desiredMatchers := range desired.Hooks {
		existingHooks[event] = mergeHookMatchers(existingHooks[event], desiredMatchers)
	}

	encodedHooks, err := json.MarshalIndent(existingHooks, "", "  ")
	if err != nil {
		return nil, xerrors.Errorf("failed to marshal merged hooks JSON: %w", err)
	}
	root["hooks"] = encodedHooks

	encodedRoot, err := json.MarshalIndent(root, "", "  ")
	if err != nil {
		return nil, xerrors.Errorf("failed to marshal merged settings JSON: %w", err)
	}

	return encodedRoot, nil
}

func mergeHookMatchers(existing []hookMatcherDocument, desired []hookMatcherDocument) []hookMatcherDocument {
	merged := make([]hookMatcherDocument, 0, len(existing)+len(desired))
	for _, matcher := range existing {
		filteredCommands := make([]hookCommandDocument, 0, len(matcher.Hooks))
		for _, command := range matcher.Hooks {
			if isTracearyManagedHookCommandDocument(command) {
				continue
			}
			filteredCommands = append(filteredCommands, command)
		}
		if len(filteredCommands) == 0 {
			continue
		}
		matcher.Hooks = filteredCommands
		merged = append(merged, matcher)
	}

	return append(merged, desired...)
}

// isTracearyManagedHookCommandDocument returns true when a hook command
// document is known to be Traceary-managed. Detection uses a stable identifier
// formed from the hook script filename (and its action arguments for compact
// variants) so it catches every known Traceary hook entry even when the user
// changed the parent directory or TRACEARY_BIN value.
func isTracearyManagedHookCommandDocument(command hookCommandDocument) bool {
	if strings.HasPrefix(strings.TrimSpace(command.Name), "traceary-") {
		return true
	}

	return extractTracearyManagedKey(command.Command) != ""
}

var tracearyHookScriptPattern = regexp.MustCompile(`traceary-(session|audit|compact|prompt)\.sh`)

// extractTracearyManagedKey returns the stable managed key (scriptName + args
// joined with ":") for a raw command string if and only if the command is a
// Traceary-managed hook invocation. Returns an empty string for unrelated
// commands.
func extractTracearyManagedKey(commandValue string) string {
	trimmedValue := strings.TrimSpace(commandValue)
	if trimmedValue == "" {
		return ""
	}

	if directKey := extractTracearyDirectManagedKey(trimmedValue); directKey != "" {
		return directKey
	}

	match := tracearyHookScriptPattern.FindStringIndex(trimmedValue)
	if match == nil {
		return ""
	}

	scriptName := trimmedValue[match[0]:match[1]]
	// The script name match ends inside a quoted shell token that contains
	// the script path (for example `'/scripts/traceary-session.sh'`). Find
	// the closing single-quote so subsequent arg parsing starts at the next
	// token rather than picking up the current token's terminator as an
	// empty argument.
	tail := trimmedValue[match[1]:]
	if closingIndex := strings.IndexByte(tail, '\''); closingIndex >= 0 {
		tail = tail[closingIndex+1:]
	}

	args := extractManagedKeyArgs(tail)
	parts := make([]string, 0, 1+len(args))
	parts = append(parts, scriptName)
	parts = append(parts, args...)

	return strings.Join(parts, ":")
}

func extractTracearyDirectManagedKey(commandValue string) string {
	tokens := parseShellSingleQuoted(commandValue)
	if len(tokens) < 4 {
		return ""
	}
	if tokens[1] != "hook" {
		return ""
	}

	switch tokens[2] {
	case "session":
		if len(tokens) != 5 {
			return ""
		}
		return managedKeyOf("traceary-session.sh", tokens[3], tokens[4])
	case "audit":
		if len(tokens) != 4 {
			return ""
		}
		return managedKeyOf("traceary-audit.sh", tokens[3])
	case "compact":
		if len(tokens) != 5 {
			return ""
		}
		return managedKeyOf("traceary-compact.sh", tokens[3], tokens[4])
	case "prompt":
		if len(tokens) != 4 {
			return ""
		}
		return managedKeyOf("traceary-prompt.sh", tokens[3])
	default:
		return ""
	}
}

// extractManagedKeyArgs extracts the trailing single-quoted arguments that
// follow a Traceary hook script invocation. The stable managed key keeps the
// client positional argument (for example "claude") so legacy script-based
// commands and direct `traceary hook ...` commands normalize to the same key.
func extractManagedKeyArgs(tail string) []string {
	tokens := parseShellSingleQuoted(tail)
	if len(tokens) == 0 {
		return nil
	}

	return tokens
}

// parseShellSingleQuoted walks the remainder of a shell command line and
// extracts every single-quoted token in order. It is intentionally narrow in
// scope: the Traceary hook command strings only use single quotes.
func parseShellSingleQuoted(remainder string) []string {
	var tokens []string
	cursor := 0
	for cursor < len(remainder) {
		if remainder[cursor] != '\'' {
			cursor++
			continue
		}
		endIndex := strings.IndexByte(remainder[cursor+1:], '\'')
		if endIndex < 0 {
			return tokens
		}
		tokens = append(tokens, remainder[cursor+1:cursor+1+endIndex])
		cursor += endIndex + 2
	}

	return tokens
}
