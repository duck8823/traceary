package filesystem

import (
	"encoding/json"

	"golang.org/x/xerrors"

	"github.com/duck8823/traceary/domain/model"
)

// hookSettingsDocument is the top-level JSON shape written to hook
// configuration files. Field names follow the existing schema so the output
// is byte-compatible with previously installed files.
type hookSettingsDocument struct {
	Hooks map[string][]hookMatcherDocument `json:"hooks"`
}

// hookMatcherDocument represents a single matcher entry inside a hook event
// array. Matcher is an optional pointer so it can be omitted when not set.
type hookMatcherDocument struct {
	Matcher *string               `json:"matcher,omitempty"`
	Hooks   []hookCommandDocument `json:"hooks"`
}

// hookCommandDocument mirrors the JSON shape of a single hook command entry.
type hookCommandDocument struct {
	Name        string `json:"name,omitempty"`
	Type        string `json:"type"`
	Command     string `json:"command"`
	Timeout     *int   `json:"timeout,omitempty"`
	Description string `json:"description,omitempty"`
}

// marshalHooks renders the Hooks aggregate to the canonical indented JSON
// form written to hook configuration files.
func marshalHooks(hooks model.Hooks) ([]byte, error) {
	document := hooksDocumentOf(hooks)
	encoded, err := json.MarshalIndent(document, "", "  ")
	if err != nil {
		return nil, xerrors.Errorf("failed to marshal hook settings document: %w", err)
	}

	return encoded, nil
}

// hooksDocumentOf converts a Hooks aggregate into the serializable document
// form. It preserves the order of events via hooks.EventOrder so that the
// rendered output is deterministic.
func hooksDocumentOf(hooks model.Hooks) hookSettingsDocument {
	events := make(map[string][]hookMatcherDocument, len(hooks.EventOrder()))
	for _, event := range hooks.EventOrder() {
		entries := hooks.Entries(event)
		events[event] = matchersDocumentOf(entries)
	}

	return hookSettingsDocument{Hooks: events}
}

func matchersDocumentOf(entries []model.HookEntry) []hookMatcherDocument {
	matchers := make([]hookMatcherDocument, 0, len(entries))
	for _, entry := range entries {
		matcher := hookMatcherDocument{
			Hooks: commandsDocumentOf(entry.Commands()),
		}
		if value, ok := entry.Matcher().Value(); ok {
			matcherValue := value
			matcher.Matcher = &matcherValue
		}
		matchers = append(matchers, matcher)
	}

	return matchers
}

func commandsDocumentOf(commands []model.HookCommand) []hookCommandDocument {
	documents := make([]hookCommandDocument, 0, len(commands))
	for _, command := range commands {
		document := hookCommandDocument{
			Name:        command.Name(),
			Type:        command.CommandType(),
			Command:     command.Command(),
			Description: command.Description(),
		}
		if value, ok := command.Timeout().Value(); ok {
			timeoutValue := value
			document.Timeout = &timeoutValue
		}
		documents = append(documents, document)
	}

	return documents
}
