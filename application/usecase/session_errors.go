package usecase

import (
	"errors"

	"github.com/duck8823/traceary/application/queryservice"
)

// IsSessionLookupNotFound reports whether err is a session-lookup not-found error.
func IsSessionLookupNotFound(err error) bool {
	return errors.Is(err, queryservice.ErrSessionNotFound) || errors.Is(err, queryservice.ErrActiveSessionNotFound)
}
