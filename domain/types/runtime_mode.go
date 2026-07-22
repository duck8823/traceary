package types

import (
	"slices"
	"strings"

	"golang.org/x/xerrors"
)

// RuntimeMode identifies the lifecycle contract under which a session runs.
// Its zero value is invalid so interactive and one-shot sessions can never be
// confused by an omitted field.
type RuntimeMode string

const (
	// RuntimeModeInteractive is a foreground session with an explicit host lifecycle.
	RuntimeModeInteractive RuntimeMode = "interactive"
	// RuntimeModeOneShot is a bounded invocation expected to finalize once.
	RuntimeModeOneShot RuntimeMode = "one_shot"
	// RuntimeModeResumed continues a previously established session lifecycle.
	RuntimeModeResumed RuntimeMode = "resumed"
	// RuntimeModeBackground runs independently from a foreground interactive lifecycle.
	RuntimeModeBackground RuntimeMode = "background"
)

var knownRuntimeModes = []RuntimeMode{
	RuntimeModeInteractive,
	RuntimeModeOneShot,
	RuntimeModeResumed,
	RuntimeModeBackground,
}

// RuntimeModeFrom restores a validated RuntimeMode from its persisted value.
func RuntimeModeFrom(value string) (RuntimeMode, error) {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return RuntimeMode(""), xerrors.New("runtime mode must not be empty")
	}
	mode := RuntimeMode(trimmed)
	if !slices.Contains(knownRuntimeModes, mode) {
		return RuntimeMode(""), xerrors.Errorf(
			"unknown runtime mode: %s (allowed values: %s)",
			trimmed,
			strings.Join(KnownRuntimeModeStrings(), ", "),
		)
	}
	return mode, nil
}

// String returns the persisted runtime-mode value.
func (m RuntimeMode) String() string { return string(m) }

// KnownRuntimeModeStrings returns all supported persisted values.
func KnownRuntimeModeStrings() []string {
	values := make([]string, 0, len(knownRuntimeModes))
	for _, mode := range knownRuntimeModes {
		values = append(values, mode.String())
	}
	return values
}
