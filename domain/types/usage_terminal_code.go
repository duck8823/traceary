package types

import "golang.org/x/xerrors"

// UsageTerminalCode is the privacy-safe closed terminal classification used
// by usage adapters. Free-form host reasons never cross this boundary.
type UsageTerminalCode string

const (
	// UsageTerminalSuccess records normal terminal completion.
	UsageTerminalSuccess UsageTerminalCode = "success"
	// UsageTerminalFailure records a terminal failure.
	UsageTerminalFailure UsageTerminalCode = "failure"
	// UsageTerminalTimeout records deadline expiration.
	UsageTerminalTimeout UsageTerminalCode = "timeout"
	// UsageTerminalSignal records operating-system signal termination.
	UsageTerminalSignal UsageTerminalCode = "signal"
	// UsageTerminalAbortedStream records a stream without normal completion.
	UsageTerminalAbortedStream UsageTerminalCode = "aborted_stream"
	// UsageTerminalUnknown records a terminal boundary without a safer class.
	UsageTerminalUnknown UsageTerminalCode = "unknown"
)

// UsageTerminalCodeFrom restores a validated terminal code.
func UsageTerminalCodeFrom(value string) (UsageTerminalCode, error) {
	code := UsageTerminalCode(value)
	switch code {
	case UsageTerminalSuccess,
		UsageTerminalFailure,
		UsageTerminalTimeout,
		UsageTerminalSignal,
		UsageTerminalAbortedStream,
		UsageTerminalUnknown:
		return code, nil
	default:
		return "", xerrors.Errorf("unsupported usage terminal code: %q", value)
	}
}

// String returns the persisted representation.
func (c UsageTerminalCode) String() string { return string(c) }
