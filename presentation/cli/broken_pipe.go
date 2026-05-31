package cli

import (
	"errors"
	"io"
	"os"
	"strings"
	"syscall"
)

// IsBrokenPipeError reports whether err represents an output stream closed by
// the downstream reader. This is normal for scripted inspection commands piped
// into early-closing consumers such as head, jq filters, or byte limiters.
func IsBrokenPipeError(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, syscall.EPIPE) || errors.Is(err, io.ErrClosedPipe) || errors.Is(err, os.ErrClosed) {
		return true
	}

	// Some callers surface SIGPIPE as text rather than an unwrap-friendly errno.
	// Keep this fallback narrow so ordinary generation/query errors remain loud.
	message := strings.ToLower(err.Error())
	return strings.Contains(message, "signal: broken pipe") ||
		(strings.Contains(message, "write") && strings.Contains(message, "broken pipe"))
}
