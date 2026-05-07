package tui

import (
	"io"
	"os"

	"golang.org/x/term"
)

// IsTerminal reports whether w is backed by a *os.File that points at a TTY.
// Non-file writers (in-memory buffers, pipes captured in tests) always
// report false so test runs stay deterministic.
func IsTerminal(w io.Writer) bool {
	f, ok := w.(*os.File)
	if !ok {
		return false
	}
	return term.IsTerminal(int(f.Fd()))
}

// IsTerminalFile reports whether the given file handle points at a TTY. nil
// returns false so callers can pass os.Stdin/os.Stdout without nil checks
// in test contexts where the stream may be replaced.
func IsTerminalFile(f *os.File) bool {
	if f == nil {
		return false
	}
	return term.IsTerminal(int(f.Fd()))
}

// Interactive reports whether the given input/output pair is suitable for an
// interactive Bubble Tea program. Both must be TTYs; otherwise callers
// should fall back to a non-interactive snapshot.
func Interactive(in, out *os.File) bool {
	return IsTerminalFile(in) && IsTerminalFile(out)
}
