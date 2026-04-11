package usecase

import (
	"errors"

	"github.com/duck8823/traceary/domain/port"
)

// IsSessionLookupNotFound reports whether err is a session-lookup not-found error.
func IsSessionLookupNotFound(err error) bool {
	return errors.Is(err, port.ErrSessionNotFound) || errors.Is(err, port.ErrActiveSessionNotFound)
}
