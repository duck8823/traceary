package cli

import (
	"io"
	"os"
	"strings"

	"golang.org/x/xerrors"
)

// colorMode captures the resolved effective color state for a read command.
// Only two terminal states matter at render time — either colors are on or
// off — so the resolver collapses the "auto" triaging into one of these.
type colorMode int

const (
	colorModeOff colorMode = iota
	colorModeOn
)

const (
	colorValueAuto   = "auto"
	colorValueAlways = "always"
	colorValueNever  = "never"
)

// ANSI escape sequences used by the compact formatter. Kept together here
// so both the renderer and the tests can reach them by name.
const (
	ansiReset      = "\x1b[0m"
	ansiRedBold    = "\x1b[1;31m"
	ansiCyan       = "\x1b[36m"
	ansiMagenta    = "\x1b[35m"
	ansiDim        = "\x1b[2m"
	ansiColorCount = 4
)

// readColorFlagUsage returns the shared --color usage string.
func readColorFlagUsage() string {
	return Localize(
		"colorize compact text output: auto|always|never (default auto; NO_COLOR env disables coloring)",
		"コンパクト出力の色付け: auto|always|never (既定 auto; NO_COLOR 環境変数が設定されていると色付けを無効化)",
	)
}

// validateColorValue checks an auto/always/never token, case-sensitive to
// match other flag values in the codebase.
func validateColorValue(value string) (string, error) {
	trimmed := strings.TrimSpace(value)
	switch trimmed {
	case colorValueAuto, colorValueAlways, colorValueNever:
		return trimmed, nil
	case "":
		return "", nil
	}
	return "", xerrors.Errorf(
		Localize(
			"invalid --color value %q (supported: auto, always, never)",
			"--color の値 %q は不正です (利用可能: auto, always, never)",
		),
		value,
	)
}

// resolveColorMode picks the effective color mode for a read command using
// this precedence: NO_COLOR env > --color flag > config > built-in auto.
// The stdoutIsTerminal callback lets tests stub TTY detection instead of
// depending on the real file descriptor.
func resolveColorMode(flagValue string, flagSet bool, configValue string, wideOrJSON bool, stdoutIsTerminal func() bool) (colorMode, error) {
	if wideOrJSON {
		// --wide / --json keep the legacy / machine-readable contract;
		// never colorize regardless of user settings.
		return colorModeOff, nil
	}
	if _, envSet := os.LookupEnv("NO_COLOR"); envSet {
		return colorModeOff, nil
	}

	var effective string
	switch {
	case flagSet:
		cleaned, err := validateColorValue(flagValue)
		if err != nil {
			return colorModeOff, err
		}
		effective = cleaned
	case configValue != "":
		cleaned, err := validateColorValue(configValue)
		if err != nil {
			return colorModeOff, xerrors.Errorf(
				Localize(
					"invalid read.color in config: %w",
					"config の read.color が不正です: %w",
				),
				err,
			)
		}
		effective = cleaned
	}
	if effective == "" {
		effective = colorValueAuto
	}

	switch effective {
	case colorValueAlways:
		return colorModeOn, nil
	case colorValueNever:
		return colorModeOff, nil
	default: // auto
		if stdoutIsTerminal != nil && stdoutIsTerminal() {
			return colorModeOn, nil
		}
		return colorModeOff, nil
	}
}

// isTerminalWriter reports whether the given writer is a TTY. A non-*os.File
// writer (for example the in-memory bytes.Buffer used in tests) always
// returns false so tests stay deterministic.
func isTerminalWriter(w io.Writer) bool {
	f, ok := w.(*os.File)
	if !ok {
		return false
	}
	info, err := f.Stat()
	if err != nil {
		return false
	}
	return (info.Mode() & os.ModeCharDevice) != 0
}

// applyCompactRowHighlight wraps a rendered compact row with ANSI sequences
// based on the event's kind. Callers must check colorEnabled before calling;
// this helper does not re-check the mode so the condition stays localized
// at the caller.
func applyCompactRowHighlight(row string, kind string, exitCode int, exitCodeSet bool) string {
	ansi := highlightEscapeFor(kind, exitCode, exitCodeSet)
	if ansi == "" {
		return row
	}
	return ansi + row + ansiReset
}

func highlightEscapeFor(kind string, exitCode int, exitCodeSet bool) string {
	switch kind {
	case "command_executed":
		if exitCodeSet && exitCode != 0 {
			return ansiRedBold
		}
		return ""
	case "prompt":
		return ansiCyan
	case "compact_summary":
		return ansiMagenta
	case "session_started", "session_ended":
		return ansiDim
	}
	return ""
}
