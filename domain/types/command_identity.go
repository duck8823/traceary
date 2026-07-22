package types

import (
	"path"
	"strings"
	"unicode"
)

// CommandName is a normalized executable or wrapper basename. Unknown is an
// explicit value for legacy rows or invocations without an underlying command.
type CommandName string

// CommandNameUnknown is the explicit identity for legacy or unavailable data.
const CommandNameUnknown CommandName = "unknown"

func (n CommandName) String() string { return string(n) }

// CommandIdentity separates the verified wrapper from the executable used as
// the report aggregation key.
type CommandIdentity struct {
	wrapper Optional[CommandName]
	command CommandName
}

// CommandIdentityFrom conservatively normalizes a new raw command. Traceary
// currently unwraps only the observed rtk grammar; every other first token is
// treated as the executable rather than guessed as a wrapper.
func CommandIdentityFrom(raw string) CommandIdentity {
	tokens, valid := commandPrefixTokens(raw, 3)
	if !valid || len(tokens) == 0 {
		return CommandIdentity{command: CommandNameUnknown}
	}
	first := commandTokenName(tokens[0])
	if first != "rtk" {
		return CommandIdentity{command: first}
	}

	identity := CommandIdentity{wrapper: Some(CommandName("rtk")), command: CommandNameUnknown}
	if len(tokens) < 2 {
		return identity
	}
	commandIndex := 1
	if tokens[1] == "proxy" {
		commandIndex = 2
	}
	if commandIndex < len(tokens) {
		candidate := commandTokenName(tokens[commandIndex])
		if candidate != "" && !strings.HasPrefix(candidate.String(), "-") {
			identity.command = candidate
		}
	}
	return identity
}

// CommandIdentityOf restores already-normalized persisted values without
// reparsing raw legacy text.
func CommandIdentityOf(wrapper Optional[CommandName], command CommandName) CommandIdentity {
	if strings.TrimSpace(command.String()) == "" {
		command = CommandNameUnknown
	}
	return CommandIdentity{wrapper: wrapper, command: command}
}

// Wrapper returns the verified wrapper, when present.
func (i CommandIdentity) Wrapper() Optional[CommandName] { return i.wrapper }

// Command returns the normalized executable or explicit unknown value.
func (i CommandIdentity) Command() CommandName {
	if strings.TrimSpace(i.command.String()) == "" {
		return CommandNameUnknown
	}
	return i.command
}

func commandTokenName(token string) CommandName {
	trimmed := strings.TrimSpace(token)
	if trimmed == "" || strings.ContainsAny(trimmed, "$`;&|<>()") {
		return CommandNameUnknown
	}
	return CommandName(path.Base(trimmed))
}

// commandPrefixTokens reads only the command prefix needed for identity. It is
// quote-aware but intentionally not a shell evaluator: expansions and control
// operators remain ordinary token text and are never executed.
func commandPrefixTokens(raw string, limit int) ([]string, bool) {
	if limit <= 0 {
		return nil, false
	}
	var (
		tokens  []string
		builder strings.Builder
		quote   rune
		escaped bool
		inToken bool
	)
	flush := func() bool {
		if !inToken {
			return false
		}
		tokens = append(tokens, builder.String())
		builder.Reset()
		inToken = false
		return len(tokens) >= limit
	}
	for _, r := range raw {
		if escaped {
			builder.WriteRune(r)
			inToken = true
			escaped = false
			continue
		}
		if r == '\\' && quote != '\'' {
			escaped = true
			inToken = true
			continue
		}
		if quote != 0 {
			if r == quote {
				quote = 0
				inToken = true
				continue
			}
			builder.WriteRune(r)
			inToken = true
			continue
		}
		if r == '\'' || r == '"' {
			quote = r
			inToken = true
			continue
		}
		if unicode.IsSpace(r) {
			if flush() {
				return tokens, true
			}
			continue
		}
		builder.WriteRune(r)
		inToken = true
	}
	if escaped || quote != 0 {
		return tokens, false
	}
	flush()
	return tokens, true
}
