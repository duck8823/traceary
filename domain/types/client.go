package types

import (
	"strings"

	"golang.org/x/xerrors"
)

// Client is a value object representing the recording channel.
// Values are not restricted to a fixed set; new channels may be introduced
// by additional integrations (e.g. cli, hook, mcp).
type Client string

// ClientOf creates a Client from a string.
func ClientOf(value string) (Client, error) {
	trimmedValue := strings.TrimSpace(value)
	if trimmedValue == "" {
		return Client(""), xerrors.Errorf("client must not be empty")
	}
	return Client(trimmedValue), nil
}

// String returns the string representation.
func (c Client) String() string { return string(c) }
