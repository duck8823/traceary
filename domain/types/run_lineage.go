package types

import (
	"strings"
	"unicode/utf8"

	"golang.org/x/xerrors"
)

const maxRunIDBytes = 512

// RunIdentity identifies one opaque execution inside a host namespace.
type RunIdentity struct {
	host  string
	runID string
}

// RunIdentityFrom validates a namespaced opaque run identity. The run ID is
// preserved byte-for-byte; only the host namespace is normalized.
func RunIdentityFrom(host, runID string) (RunIdentity, error) {
	host = strings.TrimSpace(host)
	if host == "" || !utf8.ValidString(host) {
		return RunIdentity{}, xerrors.Errorf("run identity host must be valid non-empty UTF-8")
	}
	if !utf8.ValidString(runID) || strings.TrimSpace(runID) == "" {
		return RunIdentity{}, xerrors.Errorf("run ID must be valid non-whitespace UTF-8")
	}
	if len([]byte(runID)) > maxRunIDBytes {
		return RunIdentity{}, xerrors.Errorf("run ID must not exceed %d bytes", maxRunIDBytes)
	}
	return RunIdentity{host: host, runID: runID}, nil
}

// Host returns the normalized host namespace.
func (i RunIdentity) Host() string { return i.host }

// RunID returns the opaque run identifier byte-for-byte.
func (i RunIdentity) RunID() string { return i.runID }
