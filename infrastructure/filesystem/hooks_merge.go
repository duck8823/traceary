package filesystem

import (
	"encoding/json"
	"path/filepath"
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
	desiredDirectCommands := directManagedCommandSetOf(desired)
	merged := make([]hookMatcherDocument, 0, len(existing)+len(desired))
	for _, matcher := range existing {
		filteredCommands := make([]hookCommandDocument, 0, len(matcher.Hooks))
		for _, command := range matcher.Hooks {
			if isTracearyManagedHookCommandDocument(command, desiredDirectCommands) {
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
func isTracearyManagedHookCommandDocument(command hookCommandDocument, desiredDirectCommands map[directManagedCommand]struct{}) bool {
	if strings.HasPrefix(strings.TrimSpace(command.Name), "traceary-") {
		return true
	}

	if extractTracearyManagedKey(command.Command) != "" {
		return true
	}

	directCommand, ok := parseTracearyDirectManagedCommand(command.Command)
	if !ok {
		return false
	}

	_, managed := desiredDirectCommands[directCommand]
	return managed
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
	directCommand, ok := parseTracearyDirectManagedCommand(commandValue)
	if !ok {
		return ""
	}
	if !isTracearyBinaryToken(directCommand.binaryToken) {
		return ""
	}

	return directCommand.managedKey
}

type directManagedCommand struct {
	binaryToken string
	managedKey  string
}

func parseTracearyDirectManagedCommand(commandValue string) (directManagedCommand, bool) {
	tokens := parseShellWords(commandValue)
	if len(tokens) < 4 {
		return directManagedCommand{}, false
	}
	if tokens[1] != "hook" {
		return directManagedCommand{}, false
	}

	directCommand := directManagedCommand{binaryToken: tokens[0]}

	switch tokens[2] {
	case "session":
		if len(tokens) != 5 {
			return directManagedCommand{}, false
		}
		directCommand.managedKey = managedKeyOf("traceary-session.sh", tokens[3], tokens[4])
		return directCommand, true
	case "audit":
		if len(tokens) != 4 {
			return directManagedCommand{}, false
		}
		directCommand.managedKey = managedKeyOf("traceary-audit.sh", tokens[3])
		return directCommand, true
	case "compact":
		if len(tokens) != 5 {
			return directManagedCommand{}, false
		}
		directCommand.managedKey = managedKeyOf("traceary-compact.sh", tokens[3], tokens[4])
		return directCommand, true
	case "prompt":
		if len(tokens) != 4 {
			return directManagedCommand{}, false
		}
		directCommand.managedKey = managedKeyOf("traceary-prompt.sh", tokens[3])
		return directCommand, true
	default:
		return directManagedCommand{}, false
	}
}

func directManagedCommandSetOf(matchers []hookMatcherDocument) map[directManagedCommand]struct{} {
	commands := map[directManagedCommand]struct{}{}
	for _, matcher := range matchers {
		for _, command := range matcher.Hooks {
			directCommand, ok := parseTracearyDirectManagedCommand(command.Command)
			if !ok {
				continue
			}
			commands[directCommand] = struct{}{}
		}
	}

	return commands
}

// extractManagedKeyArgs extracts the trailing single-quoted arguments that
// follow a Traceary hook script invocation. The stable managed key keeps the
// client positional argument (for example "claude") so legacy script-based
// commands and direct `traceary hook ...` commands normalize to the same key.
func extractManagedKeyArgs(tail string) []string {
	tokens := parseShellWords(tail)
	if len(tokens) == 0 {
		return nil
	}

	return tokens
}

func isTracearyBinaryToken(token string) bool {
	trimmedToken := strings.TrimSpace(token)
	if trimmedToken == "" {
		return false
	}

	base := filepath.Base(trimmedToken)
	return base == "traceary"
}

// parseShellWords tokenizes the limited shell command format used by Traceary
// hook configs. It supports whitespace-separated words plus single-quoted and
// double-quoted segments so values produced by shellQuoteHookValue (including
// apostrophe escapes like '"'"'"'"'"'"'"'"') can be reconstructed correctly.
func parseShellWords(remainder string) []string {
	var tokens []string
	var current strings.Builder
	inSingleQuotes := false
	inDoubleQuotes := false
	escaped := false
	tokenStarted := false

	flush := func() {
		if !tokenStarted {
			return
		}
		tokens = append(tokens, current.String())
		current.Reset()
		tokenStarted = false
	}

	for index := 0; index < len(remainder); index++ {
		character := remainder[index]

		if escaped {
			current.WriteByte(character)
			tokenStarted = true
			escaped = false
			continue
		}

		switch {
		case inSingleQuotes:
			if character == '\'' {
				inSingleQuotes = false
				continue
			}
			current.WriteByte(character)
			tokenStarted = true
		case inDoubleQuotes:
			switch character {
			case '"':
				inDoubleQuotes = false
			case '\\':
				if index+1 >= len(remainder) {
					current.WriteByte(character)
					tokenStarted = true
					continue
				}
				index++
				current.WriteByte(remainder[index])
				tokenStarted = true
			default:
				current.WriteByte(character)
				tokenStarted = true
			}
		default:
			switch character {
			case '\'':
				inSingleQuotes = true
				tokenStarted = true
			case '"':
				inDoubleQuotes = true
				tokenStarted = true
			case '\\':
				escaped = true
				tokenStarted = true
			case ' ', '\t', '\n', '\r':
				flush()
			default:
				current.WriteByte(character)
				tokenStarted = true
			}
		}
	}

	if escaped {
		current.WriteByte('\\')
		tokenStarted = true
	}
	flush()

	return tokens
}
