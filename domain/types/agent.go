package types

import (
	"strings"

	"golang.org/x/xerrors"
)

// Agent is a value object representing the actor that produced an event.
type Agent string

// AgentOf creates an Agent from a string.
func AgentOf(value string) (Agent, error) {
	trimmedValue := strings.TrimSpace(value)
	if trimmedValue == "" {
		return Agent(""), xerrors.Errorf("agent must not be empty")
	}
	return Agent(trimmedValue), nil
}

// String returns the string representation.
func (a Agent) String() string { return string(a) }
