package model

import (
	"github.com/duck8823/traceary/domain/types"
)

// HookCommand represents a single hook command entry bundled with a Traceary
// hook configuration. It is the smallest unit of a Hooks aggregate.
type HookCommand struct {
	name        string
	commandType string
	command     string
	timeout     types.Optional[int]
	description string
	managedKey  string
}

// HookCommandOf constructs a HookCommand.
//
// managedKey is a stable identifier that marks the command as Traceary-managed
// (for example the hook script filename plus its action argument). It is used
// during merges to replace previously installed Traceary hooks without
// touching user-defined hooks.
func HookCommandOf(
	name string,
	commandType string,
	command string,
	timeout types.Optional[int],
	description string,
	managedKey string,
) HookCommand {
	return HookCommand{
		name:        name,
		commandType: commandType,
		command:     command,
		timeout:     timeout,
		description: description,
		managedKey:  managedKey,
	}
}

// Name returns the optional display name of the hook command.
func (c HookCommand) Name() string { return c.name }

// CommandType returns the hook command type (e.g. "command").
func (c HookCommand) CommandType() string { return c.commandType }

// Command returns the shell command string executed by the hook.
func (c HookCommand) Command() string { return c.command }

// Timeout returns the optional timeout value in milliseconds.
func (c HookCommand) Timeout() types.Optional[int] { return c.timeout }

// Description returns the optional human readable description.
func (c HookCommand) Description() string { return c.description }

// ManagedKey returns the stable identifier used to detect Traceary-managed
// hooks. An empty string means the command is not Traceary-managed.
func (c HookCommand) ManagedKey() string { return c.managedKey }

// HookEntry represents a single matcher entry inside a hook event array.
type HookEntry struct {
	matcher  types.Optional[string]
	commands []HookCommand
}

// HookEntryOf constructs a HookEntry.
func HookEntryOf(matcher types.Optional[string], commands []HookCommand) HookEntry {
	copied := make([]HookCommand, len(commands))
	copy(copied, commands)
	return HookEntry{
		matcher:  matcher,
		commands: copied,
	}
}

// Matcher returns the optional matcher pattern for this entry.
func (e HookEntry) Matcher() types.Optional[string] { return e.matcher }

// Commands returns a copy of the commands attached to this entry.
func (e HookEntry) Commands() []HookCommand {
	copied := make([]HookCommand, len(e.commands))
	copy(copied, e.commands)
	return copied
}

// Hooks is the aggregate of hook entries grouped by event name.
type Hooks struct {
	eventOrder []string
	events     map[string][]HookEntry
}

// HooksOf constructs a Hooks aggregate from an ordered list of event names
// and the entries keyed by event name. The ordering is preserved so callers
// can render deterministic output.
func HooksOf(eventOrder []string, events map[string][]HookEntry) Hooks {
	orderCopy := make([]string, len(eventOrder))
	copy(orderCopy, eventOrder)

	eventsCopy := make(map[string][]HookEntry, len(events))
	for event, entries := range events {
		entriesCopy := make([]HookEntry, len(entries))
		copy(entriesCopy, entries)
		eventsCopy[event] = entriesCopy
	}

	return Hooks{
		eventOrder: orderCopy,
		events:     eventsCopy,
	}
}

// EventOrder returns the ordered event names that compose this Hooks
// aggregate. The returned slice is a copy and can be mutated safely.
func (h Hooks) EventOrder() []string {
	copied := make([]string, len(h.eventOrder))
	copy(copied, h.eventOrder)
	return copied
}

// Entries returns a copy of the entries registered for the given event name.
// It returns nil when the event has no entries.
func (h Hooks) Entries(event string) []HookEntry {
	entries, ok := h.events[event]
	if !ok {
		return nil
	}
	copied := make([]HookEntry, len(entries))
	copy(copied, entries)
	return copied
}

// Events returns a map of all events to their entries. Mutating the returned
// map does not affect the aggregate.
func (h Hooks) Events() map[string][]HookEntry {
	result := make(map[string][]HookEntry, len(h.events))
	for event, entries := range h.events {
		copied := make([]HookEntry, len(entries))
		copy(copied, entries)
		result[event] = copied
	}
	return result
}
